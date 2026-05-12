package httpx

import (
	"context"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"ptxt-nstr/internal/config"
	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
	templatesfs "ptxt-nstr/internal/templates"
	staticfs "ptxt-nstr/web/static"
)

type Server struct {
	cfg config.Config
	// seedCrawlViewerHex is the WoT center (hex) used by the seed-contact
	// crawler for outbox routing; set by successful prewarmLoggedOutSeedNow.
	seedCrawlViewerHex string
	seedCrawlViewerMu  sync.RWMutex
	store              *store.Store
	nostr              *nostrx.Client
	templates          *template.Template
	metrics            *appMetrics
	warmer             *warmQueue
	avatarCache        *avatarCache
	resolvedAuthors    *resolvedAuthorsCache
	activeViewers      *activeViewers
	hydrationTouches   *hydrationTouchCache
	searchStoreCache   *ttlCache[store.SearchNotesResult]
	searchPageCache    *ttlCache[SearchPageData]
	tagStoreCache      *ttlCache[store.SearchNotesResult]
	tagPageCache       *ttlCache[TagPageData]
	guestFeedCache     *ttlCache[FeedPageData]
	searchLimiter      *searchLimiter
	searchGroup        *searchSingleFlight
	tagGroup           *tagSingleFlight
	refreshMu          sync.Mutex
	inFlight           map[string]bool
	ctx                context.Context
	cancel             context.CancelFunc
	backgroundWG       sync.WaitGroup
	lastRequestAt      atomic.Int64
	activeRequests     atomic.Int64
	maintenanceRunning atomic.Bool
	userAsyncQueue     chan func()
	relayWriteSem      chan struct{}

	healthProbeFails  atomic.Uint32
	healthLastOK      atomic.Bool
	healthLastProbeMS atomic.Int64
	healthDegraded    atomic.Bool
}

func New(cfg config.Config, st *store.Store, nostrClient *nostrx.Client) (*Server, error) {
	tmpl, err := template.New("").Funcs(templateFuncs()).ParseFS(templatesfs.FS, "*.html")
	if err != nil {
		return nil, err
	}
	server := &Server{
		cfg:              cfg,
		store:            st,
		nostr:            nostrClient,
		templates:        tmpl,
		metrics:          newAppMetrics(),
		avatarCache:      newAvatarCache(avatarCacheCapacity),
		resolvedAuthors:  newResolvedAuthorsCache(),
		activeViewers:    newActiveViewers(),
		hydrationTouches: newHydrationTouchCache(hydrationTouchDebounceTTL, hydrationTouchCacheMaxLen),
		searchStoreCache: newSearchStoreCache(),
		searchPageCache:  newSearchPageCache(),
		tagStoreCache:    newTagStoreCache(),
		tagPageCache:     newTagPageCache(),
		guestFeedCache:   newGuestFeedPageCache(),
		searchLimiter:    newSearchLimiter(cfg.SearchRateBurst, cfg.SearchRatePerSec),
		searchGroup:      newSearchSingleFlight(),
		tagGroup:         newTagSingleFlight(),
		inFlight:         make(map[string]bool),
		userAsyncQueue:   make(chan func(), userAsyncQueueCapacity),
		relayWriteSem:    make(chan struct{}, userAsyncWorkerCount),
	}
	server.ctx, server.cancel = context.WithCancel(context.Background())
	server.store.SetEventRetention(cfg.EventRetention)
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
	server.warmer = newWarmQueue(server, 2)
	for range userAsyncWorkerCount {
		server.runBackground(server.runUserAsyncWorker)
	}
	if cfg.HydrationEnabled {
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
	if len(server.cfg.DefaultRelays) > 0 || len(server.cfg.MetadataRelays) > 0 {
		server.runBackground(server.runDefaultSeedPrewarmLoop)
		server.runBackground(server.runDefaultSeedGuestFeedHotLoop)
	}
	server.runBackground(server.runSeedCrawler)
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

func (s *Server) Close() {
	if s == nil {
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.warmer != nil {
		s.warmer.close()
	}
	s.backgroundWG.Wait()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticRoot()))))
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/", s.handleHome)
	mux.HandleFunc("/feed", s.handleFeed)
	mux.HandleFunc("/reads", s.handleReads)
	mux.HandleFunc("/reads/", s.handleRead)
	mux.HandleFunc("/bookmarks", s.handleBookmarks)
	mux.HandleFunc("/notifications", s.handleNotifications)
	mux.HandleFunc("/settings", s.handleSettings)
	mux.HandleFunc("/about", s.handleAbout)
	mux.HandleFunc("/profile/edit", s.handleEditProfile)
	mux.HandleFunc("/search", s.handleSearch)
	mux.HandleFunc("/tag/", s.handleTag)
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/relays", s.handleRelays)
	mux.HandleFunc("/trending", s.handleTrending)
	mux.HandleFunc("/api/relay-info", s.handleRelayInfo)
	mux.HandleFunc("/api/reply-counts", s.handleReplyCounts)
	mux.HandleFunc("/api/reaction-stats", s.handleReactionStats)
	mux.HandleFunc("/api/reactions", s.handleReactionsAPI)
	mux.HandleFunc("/api/bookmarks", s.handleBookmarksAPI)
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/profile", s.handleProfileAPI)
	mux.HandleFunc("/api/relay-insight", s.handleRelayInsightAPI)
	mux.HandleFunc("/api/mentions", s.handleMentionsAPI)
	mux.HandleFunc("/api/event/", s.handleEventAPI)
	if s.cfg.Debug {
		mux.HandleFunc("/debug/cache", s.handleDebugCache)
		mux.HandleFunc("/debug/metrics", s.handleDebugMetrics)
		mux.HandleFunc("/debug/runtime", s.handleDebugRuntime)
		mux.HandleFunc("/debug/event", s.handleDebugEvent)
		mux.HandleFunc("/debug/profile", s.handleDebugProfile)
		mux.HandleFunc("/debug/firehose", s.handleDebugFirehose)
	}
	// pprof + expvar live on a separate listener bound to PprofAddr (default
	// 127.0.0.1:6060) regardless of cfg.Debug so on-host triage (SSM/SSH)
	// can grab a goroutine dump or heap profile without restarting the
	// process. See pprof.go:startPprofListener.
	mux.HandleFunc("/u/", s.handleUser)
	mux.HandleFunc("/e/", s.handleEvent)
	mux.HandleFunc("/thread/", s.handleThread)
	mux.HandleFunc("/og/", s.handleOGImage)
	mux.HandleFunc("/services/oembed", s.handleOEmbed)
	mux.HandleFunc(avatarPathPrefix, s.handleAvatar)
	coalesce := newCoalesceMiddleware(coalesceConfig{
		Enabled: s.cfg.CoalesceEnabled,
		Buckets: s.cfg.CoalesceBuckets,
		Timeout: s.cfg.CoalesceTimeout,
	})
	return logging(s, withTimeout(requestTimeout(s.cfg.RequestTimeout), coalesce(mux)))
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
