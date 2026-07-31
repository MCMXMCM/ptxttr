package httpx

import (
	"bytes"
	"context"
	"html/template"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ptxt-nstr/internal/config"
	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
	templatesfs "ptxt-nstr/internal/templates"
	staticfs "ptxt-nstr/web/static"
)

var serverCloseTimeout = 5 * time.Second

type Server struct {
	cfg config.Config
	// seedCrawlViewerHex is the WoT center (hex) used by the seed-contact
	// crawler for outbox routing; set by successful prewarmLoggedOutSeedNow.
	seedCrawlViewerHex string
	seedCrawlViewerMu  sync.RWMutex
	// loggedOutSeedCenterHex is the bootstrap WoT center whose resolved-author
	// cache must be invalidated when the seed-contact frontier expands.
	loggedOutSeedCenterHex   string
	loggedOutSeedCenterMu    sync.RWMutex
	store                    *store.Store
	nostr                    *nostrx.Client
	templates                *template.Template
	metrics                  *appMetrics
	warmer                   *warmQueue
	avatarCache              *avatarCache
	nip05Cache               *ttlCache[nostrx.NIP05VerificationResult]
	resolvedAuthors          *resolvedAuthorsCache
	activeViewers            *activeViewers
	hydrationTouches         *hydrationTouchCache
	searchStoreCache         *ttlCache[store.SearchNotesResult]
	tagStoreCache            *ttlCache[store.SearchNotesResult]
	tagPageCache             *ttlCache[TagPageData]
	guestFeedCache           *ttlCache[FeedPageData]
	anonymousHTMLCache       *ttlCache[anonymousHTMLDocument]
	threadTelemetry          *threadTelemetryHub
	readsTrendingCache       *ttlCache[[]TrendingNote]
	relayTrendingCache       *ttlCache[relayTrendingSnapshot]
	searchLimiter            *searchLimiter
	anonymousLimiter         *searchLimiter
	anonymousOptionalLimiter *searchLimiter
	botLimiter               *searchLimiter
	viewerLimiter            *searchLimiter
	searchGroup              *searchStoreSingleFlight
	tagGroup                 *tagSingleFlight
	refreshMu                sync.Mutex
	inFlight                 map[string]bool
	ctx                      context.Context
	cancel                   context.CancelFunc
	backgroundWG             sync.WaitGroup
	lastRequestAt            atomic.Int64
	activeRequests           atomic.Int64
	maintenanceSeed          atomic.Bool
	maintenanceViewer        atomic.Bool
	maintenanceHydration     atomic.Bool
	maintenanceTrending      atomic.Bool
	userAsyncQueue           chan func()
	relayWriteSem            chan struct{}

	healthProbeFails  atomic.Uint32
	healthLastOK      atomic.Bool
	healthLastProbeMS atomic.Int64
	healthDegraded    atomic.Bool

	nip50Mu            sync.Mutex
	nip50FallbackAt    []time.Time
	seedCrawlIndex     atomic.Uint64
	hotFeedCrawlCursor atomic.Uint64
	viewerGraphCursor  atomic.Uint64
	viewerNoteCursor   atomic.Uint64
	// debugAnonymousAuthors keeps notes inserted through /debug/seed-note
	// reachable under the same anonymous-scope policy exercised by e2e tests.
	// The endpoint is only registered when Debug is enabled.
	debugAnonymousAuthors sync.Map
}

func New(cfg config.Config, st *store.Store, nostrClient *nostrx.Client) (*Server, error) {
	tmpl, err := template.New("").Funcs(templateFuncs()).ParseFS(templatesfs.FS, "*.html")
	if err != nil {
		return nil, err
	}
	server := &Server{
		cfg:                      cfg,
		store:                    st,
		nostr:                    nostrClient,
		templates:                tmpl,
		metrics:                  newAppMetrics(),
		avatarCache:              newAvatarCache(avatarCacheCapacity),
		nip05Cache:               newNIP05VerificationCache(),
		resolvedAuthors:          newResolvedAuthorsCache(),
		activeViewers:            newActiveViewers(),
		hydrationTouches:         newHydrationTouchCache(hydrationTouchDebounceTTL, hydrationTouchCacheMaxLen),
		searchStoreCache:         newSearchStoreCache(),
		tagStoreCache:            newTagStoreCache(),
		tagPageCache:             newTagPageCache(),
		guestFeedCache:           newGuestFeedPageCache(),
		anonymousHTMLCache:       newAnonymousHTMLCache(),
		threadTelemetry:          newThreadTelemetryHub(),
		readsTrendingCache:       newReadsTrendingCache(),
		relayTrendingCache:       newRelayTrendingCache(),
		searchLimiter:            newSearchLimiter(cfg.SearchRateBurst, cfg.SearchRatePerSec),
		anonymousLimiter:         newSearchLimiter(cfg.AnonymousRateBurst, cfg.AnonymousRatePerSec),
		anonymousOptionalLimiter: newSearchLimiter(cfg.AnonymousRateBurst, cfg.AnonymousRatePerSec),
		botLimiter:               newSearchLimiter(cfg.BotRateBurst, cfg.BotRatePerSec),
		viewerLimiter:            newSearchLimiter(cfg.ViewerRateBurst, cfg.ViewerRatePerSec),
		searchGroup:              newSearchStoreSingleFlight(),
		tagGroup:                 newTagSingleFlight(),
		inFlight:                 make(map[string]bool),
		userAsyncQueue:           make(chan func(), userAsyncQueueCapacity),
		relayWriteSem:            make(chan struct{}, userAsyncWorkerCount),
	}
	server.ctx, server.cancel = context.WithCancel(context.Background())
	server.store.SetEventRetention(cfg.EventRetention)
	server.store.SetRetentionPolicy(cfg.RetentionByAccess)
	server.store.SetDiskRetentionPolicy(cfg.DBDiskMaxPercent, cfg.DBDiskPruneTargetPercent)
	if st != nil {
		st.SetSidecarMetricSink(func(name string, delta int64) {
			server.metrics.Add(name, delta)
		})
	}
	// Zero until the first HTTP request: avoids treating a brand-new server as
	// "foreground hot" for maintenance_gate (see foregroundBusy).
	server.lastRequestAt.Store(0)
	nostrClient.SetIngestVerifyParallel(cfg.IngestVerifyParallel)
	nostrClient.SetNegentropyCache(st)
	nostrClient.SetRelayMaxOutboundConns(cfg.RelayMaxOutboundConns)
	if !server.shareServerMode() {
		warmWorkers := cfg.WarmWorkers
		if warmWorkers <= 0 {
			warmWorkers = 2
		}
		server.warmer = newWarmQueue(server, warmWorkers, cfg.WarmQueueCapacity)
	}
	for range userAsyncWorkerCount {
		server.runBackground(server.runUserAsyncWorker)
	}
	if cfg.HydrationEnabled && server.allowLegacyRelayBackend() {
		if cfg.RebuildProjections {
			server.runBackgroundWithTimeout("projection rebuild", 30*time.Second, server.store.RebuildProjections)
		}
		server.runBackground(server.runHydrationSweeper)
		server.runBackground(server.runTrendingSweeper)
		if cfg.ActiveViewerTrendingEnabled {
			server.runBackground(server.runActiveViewerTrendingHotLoop)
		}
	}
	// No relays: skip bootstrap loop (tests) to avoid retry spam on sqlite.
	if server.allowLegacyRelayBackend() && (len(server.cfg.DefaultRelays) > 0 || len(server.cfg.MetadataRelays) > 0) {
		if cfg.GuestSliceV2Enabled {
			server.runBackground(server.runGuestSliceScheduler)
			server.runBackground(server.runGuestWALMonitor)
		} else {
			server.runBackground(server.runDefaultSeedPrewarmLoop)
			server.runBackground(server.runDefaultSeedGuestFeedHotLoop)
			if cfg.HotFeedCrawlerEnabled {
				server.runBackground(server.runHotFeedCrawler)
			}
		}
	}
	if server.allowLegacyRelayBackend() && !cfg.GuestSliceV2Enabled {
		server.runBackground(server.runSeedCrawler)
	}
	if cfg.ViewerCrawlerEnabled && server.allowLegacyRelayBackend() {
		server.runBackground(server.runViewerCrawler)
	}
	if server.shareServerMode() {
		slog.Info("share server mode: legacy relay crawlers, warmers, and signed-in SSR relay fetch disabled")
		if len(server.cfg.DefaultRelays) > 0 || len(server.cfg.MetadataRelays) > 0 {
			server.runBackground(server.runClientModeSeedGraphBootstrap)
		}
	}
	if cfg.HealthProbeEnabled {
		if base, ok := healthProbeBaseURL(cfg.Addr); ok {
			server.healthLastOK.Store(true)
			server.runBackground(func() { server.runHealthProbeLoop(base) })
		} else {
			slog.Warn("health probe enabled but listen addr not suitable for loopback probe; skipping", "addr", cfg.Addr)
		}
	}
	if cfg.PprofAddr != "" {
		server.startPprofListener(cfg.PprofAddr)
	}
	return server, nil
}

// runBackgroundWithTimeout spawns a tracked background goroutine that calls fn
// with a context bounded by timeout, logging non-nil errors at warn level.
func (s *Server) runBackgroundWithTimeout(name string, timeout time.Duration, fn func(context.Context) error) {
	s.runBackground(func() {
		ctx, cancel := context.WithTimeout(s.ctx, timeout)
		defer cancel()
		if err := fn(ctx); err != nil {
			slog.Warn(name+" failed", "err", err)
		}
	})
}

func (s *Server) runBackground(fn func()) {
	if s == nil {
		return
	}
	s.backgroundWG.Add(1)
	go func() {
		defer s.backgroundWG.Done()
		fn()
	}()
}

func (s *Server) Stop() {
	if s == nil || s.cancel == nil {
		return
	}
	s.cancel()
}

func (s *Server) Close() {
	if s == nil {
		return
	}
	s.Stop()
	done := make(chan struct{})
	go func() {
		if s.warmer != nil {
			s.warmer.close()
		}
		s.backgroundWG.Wait()
		close(done)
	}()
	timeout := serverCloseTimeout
	if timeout <= 0 {
		<-done
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		slog.Warn("server background shutdown timed out; exiting with goroutines still running", "timeout", timeout, "goroutines", string(buf[:n]))
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/static/", s.handleStaticAsset)
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/", s.handleHome)
	mux.HandleFunc("/feed", s.handleFeed)
	mux.HandleFunc("/reads", s.handleReads)
	mux.HandleFunc("/reads/", s.handleRead)
	mux.HandleFunc("/bookmarks", s.handleBookmarks)
	mux.HandleFunc("/notifications", s.handleNotifications)
	mux.HandleFunc("/settings", s.handleSettings)
	mux.HandleFunc("/about", s.handleAbout)
	mux.HandleFunc("/support", s.handleSupport)
	mux.HandleFunc("/ios-plain-text-nostr", s.handleMarketing)
	mux.HandleFunc("/terms", s.handleTerms)
	mux.HandleFunc("/privacy", s.handlePrivacy)
	mux.HandleFunc("/profile/edit", s.handleEditProfile)
	mux.HandleFunc("/search", s.handleSearch)
	mux.HandleFunc("/tag/", s.handleTag)
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/relays", s.handleRelays)
	mux.HandleFunc("/trending", s.handleTrending)
	mux.HandleFunc("/api/relay-info", s.handleRelayInfo)
	mux.HandleFunc("/api/guest-feed-status", s.handleGuestFeedStatus)
	mux.HandleFunc("/api/reply-counts", s.handleReplyCounts)
	mux.HandleFunc("/api/reaction-stats", s.handleReactionStats)
	mux.HandleFunc("/api/reactions", s.handleReactionsAPI)
	mux.HandleFunc("/api/wot-authors", s.handleWoTAuthors)
	mux.HandleFunc("/api/feed-notes", s.handleFeedNotesAPI)
	mux.HandleFunc("/api/search-notes", s.handleSearchNotesAPI)
	mux.HandleFunc("/api/tag-notes", s.handleTagNotesAPI)
	mux.HandleFunc("/api/profiles", s.handleProfilesAPI)
	mux.HandleFunc("/api/thread-preview", s.handleThreadPreviewAPI)
	mux.HandleFunc("/api/thread-telemetry", s.handleThreadTelemetry)
	mux.HandleFunc("/api/outbox-plan", s.handleOutboxPlanAPI)
	mux.HandleFunc("/api/avatar-meta", s.handleAvatarMetaAPI)
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/relay-insight", s.handleRelayInsightAPI)
	mux.HandleFunc("/api/share-preview", s.handleSharePreviewWarm)
	mux.HandleFunc("/api/shares", s.handleCreateShare)
	if s.cfg.Debug {
		mux.HandleFunc("/debug/cache", s.handleDebugCache)
		mux.HandleFunc("/debug/metrics", s.handleDebugMetrics)
		mux.HandleFunc("/debug/runtime", s.handleDebugRuntime)
		mux.HandleFunc("/debug/event", s.handleDebugEvent)
		mux.HandleFunc("/debug/profile", s.handleDebugProfile)
		mux.HandleFunc("/debug/firehose", s.handleDebugFirehose)
		mux.HandleFunc("/debug/seed-note", s.handleDebugSeedNote)
		mux.HandleFunc("/debug/seed-thread-wot", s.handleDebugSeedThreadWoT)
	}
	// pprof + expvar live on a separate listener bound to PprofAddr (default
	// 127.0.0.1:6060) regardless of cfg.Debug so on-host triage (SSM/SSH)
	// can grab a goroutine dump or heap profile without restarting the
	// process. See pprof.go:startPprofListener.
	mux.HandleFunc("/u/", s.handleUser)
	mux.HandleFunc("/s/", s.handleSharePage)
	mux.HandleFunc("/e/", s.handleEvent)
	mux.HandleFunc("/thread/", s.handleThread)
	mux.HandleFunc("/og/share/", s.handleShareOGImage)
	mux.HandleFunc("/og/", s.handleOGImage)
	mux.HandleFunc("/services/oembed", s.handleOEmbed)
	mux.HandleFunc(avatarPathPrefix, s.handleAvatar)
	if s.cfg.DesktopMode {
		mux.HandleFunc(desktopOpenExternalPath, s.handleDesktopOpenExternal)
		mux.HandleFunc(desktopStoragePath, s.handleDesktopStorage)
		mux.HandleFunc(desktopStorageClearPath, s.handleDesktopStorageClear)
		mux.HandleFunc(desktopFollowGraphPath, s.handleDesktopFollowGraph)
	}
	coalesce := newCoalesceMiddleware(coalesceConfig{
		Enabled: s.cfg.CoalesceEnabled,
		Buckets: s.cfg.CoalesceBuckets,
		Timeout: s.cfg.CoalesceTimeout,
	})
	return logging(s, withTimeout(requestTimeout(s.cfg.RequestTimeout), s.trafficShield(coalesce(s.guestRouteHeaders(mux)))))
}

func (s *Server) handleStaticAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	name, versioned := staticAssetName(r.URL.Path)
	if name == "" {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(staticRoot(), name)
	if err != nil {
		w.Header().Set("Cache-Control", "no-store")
		http.NotFound(w, r)
		return
	}
	if versioned {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if r.Method == http.MethodHead {
		return
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}

func staticAssetName(requestPath string) (string, bool) {
	trimmed := strings.TrimPrefix(path.Clean(requestPath), "/")
	if trimmed == "" || trimmed == "." || !strings.HasPrefix(trimmed, "static/") {
		return "", false
	}
	rel := strings.TrimPrefix(trimmed, "static/")
	if rel == "" {
		return "", false
	}
	if version, rest, ok := strings.Cut(rel, "/"); ok && staticAssetVersionSegment(version) {
		return rest, true
	}
	return rel, false
}

func staticAssetVersionSegment(segment string) bool {
	if len(segment) != len(staticfs.ReleaseVersion()) {
		return false
	}
	for _, r := range segment {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			continue
		}
		return false
	}
	return true
}

func staticRoot() fs.FS {
	sub, err := fs.Sub(staticfs.FS, ".")
	if err != nil {
		return staticfs.FS
	}
	return sub
}

func (s *Server) recentlyActive(window time.Duration) bool {
	if s == nil {
		return true
	}
	if window <= 0 {
		window = 10 * time.Minute
	}
	last := s.lastRequestAt.Load()
	if last <= 0 {
		return true
	}
	return time.Since(time.Unix(last, 0)) <= window
}
