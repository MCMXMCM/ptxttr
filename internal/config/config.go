package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"ptxt-nstr/internal/nostrx"
)

// DefaultCoalesceBuckets is the default for PTXT_COALESCE_BUCKETS and for
// httpx coalesce middleware when the configured value is non-positive.
const DefaultCoalesceBuckets = 64

const (
	DefaultHotFeedCrawlerInterval         = 45 * time.Second
	DefaultHotFeedCrawlerCohortLimit      = 8
	DefaultHotFeedCrawlerAuthorLimit      = 80
	DefaultHotFeedCrawlerFetchLimit       = 80
	DefaultHotFeedCrawlerLookback         = 36 * time.Hour
	DefaultHotFeedCrawlerSnapshotThrottle = 2 * time.Minute
	ServerModeApp                         = "app"
	ServerModeShare                       = "share"
)

type Config struct {
	Addr           string
	DBPath         string
	RequestTimeout time.Duration
	// ServerMode is the authoritative runtime contract for the Go origin.
	// "app" preserves the legacy relay-backed server behavior; "share" limits
	// the process to a thin share/app-shell origin with no legacy warmers.
	ServerMode string
	// OptionalRelayBackend disables relay crawlers/hydration when the browser owns relay I/O.
	OptionalRelayBackend bool
	// ShareCacheEnabled allows one-shot relay fetches for OG/share SSR into minimal SQLite.
	ShareCacheEnabled bool
	// ShareServerTransitionalFallbacks keeps legacy share-mode fragment/API
	// fallbacks reachable while the client migration is still being observed.
	// Disable it to make share mode return hard failures for those routes.
	ShareServerTransitionalFallbacks bool
	// RebuildProjections controls full projection rebuild at startup.
	RebuildProjections bool
	// CompactOnStart triggers one-shot prune + vacuum at startup.
	CompactOnStart bool
	DefaultRelays  []string
	// MetadataRelays are preferred for profile/follow/relay-list hydration.
	MetadataRelays []string
	// IndexerRelays are heavy indexers used for thread reply discovery (second pass).
	IndexerRelays []string
	// IndexerNIP50Relays are relays that accept NIP-50 search (excludes search-only relays like search.nos.today that reject note-id queries).
	IndexerNIP50Relays []string
	// TrendingSearchRelays are relays that support NIP-50 hot/top search for
	// global trending feeds. These may include search-only relays.
	TrendingSearchRelays []string
	// IndexerMaxRelays caps the indexer relay tier per fetch.
	IndexerMaxRelays int
	// IndexerNIP50MaxRelays caps the NIP-50 fallback relay tier.
	IndexerNIP50MaxRelays int
	// TrendingSearchMaxRelays caps the hot-trending relay tier per fetch.
	TrendingSearchMaxRelays int
	// CuratedPubkeys are extra hex pubkeys to bootstrap and crawl (PTXT_CURATED_PUBKEYS).
	CuratedPubkeys []string
	// ThreadMaxRelays caps merged relays for thread hydration (primary pass).
	ThreadMaxRelays int
	// ThreadOutboxMaxRouteGroups caps grouped outbox fan-out for thread reply authors.
	ThreadOutboxMaxRouteGroups int
	// ThreadOutboxMaxRelaysPerAuthor caps per-author relays in thread outbox groups.
	ThreadOutboxMaxRelaysPerAuthor int
	// ThreadContextWarmMaxIDs caps ancestor/referenced IDs warmed after hydrate.
	ThreadContextWarmMaxIDs int
	// HydrationNoteRepliesBatch caps noteReplies targets per hydration sweeper tick.
	HydrationNoteRepliesBatch int
	// WarmWorkers is the number of warm-queue worker goroutines.
	WarmWorkers int
	// WarmQueueCapacity is the warm job channel buffer size.
	WarmQueueCapacity int
	// NIP50FallbackRatePerMin caps background NIP-50 reply fallbacks per minute.
	NIP50FallbackRatePerMin int
	// KnownViewerMax caps durable knownViewer hydration rows.
	KnownViewerMax int
	// ViewerCrawlerEnabled runs background crawl for previously signed-in viewers.
	ViewerCrawlerEnabled bool
	// ViewerCrawlerInterval is the delay between viewer crawler ticks.
	ViewerCrawlerInterval time.Duration
	// ViewerCrawlerDegreeTwoInterval controls how often degree-two graph notes are refreshed.
	ViewerCrawlerDegreeTwoInterval time.Duration
	// ViewerCrawlerDegreeThreeInterval controls how often bounded degree-three candidates are refreshed.
	ViewerCrawlerDegreeThreeInterval time.Duration
	// ViewerCrawlerReducedInterval controls direct-follow polling while the desktop is minimized.
	ViewerCrawlerReducedInterval time.Duration
	// ViewerCrawlerDirectMetadataInterval controls direct-follow kind-3 and relay metadata refresh.
	ViewerCrawlerDirectMetadataInterval time.Duration
	// ViewerCrawlerDegreeTwoMetadataInterval controls degree-two metadata refresh.
	ViewerCrawlerDegreeTwoMetadataInterval time.Duration
	// ViewerCrawlerDegreeThreeMetadataInterval controls degree-three metadata refresh.
	ViewerCrawlerDegreeThreeMetadataInterval time.Duration
	// ViewerCrawlerProfileInterval controls missing/stale desktop profile refresh.
	ViewerCrawlerProfileInterval time.Duration
	// ViewerCrawlerBatch caps known viewers processed per tick.
	ViewerCrawlerBatch int
	// ViewerCrawlerReplyWarmLimit caps thread warms per viewer per tick.
	ViewerCrawlerReplyWarmLimit int
	// ViewerCrawlerFollowEnqueuePerTick caps follows enqueued per viewer tick.
	ViewerCrawlerFollowEnqueuePerTick int
	// OutboxMaxRelaysPerAuthor caps per-author relay candidates for routing.
	OutboxMaxRelaysPerAuthor int
	// OutboxMaxRouteGroups limits grouped relay fetch fanout per request.
	OutboxMaxRouteGroups int
	// OutboxFoFSeeds caps followers-of-followers expansion when seeding routes.
	OutboxFoFSeeds int
	// FeedWindow bounds the logged-out firehose feed to the last N duration.
	FeedWindow time.Duration
	// EventRetention caps the total number of events kept in the cache.
	// When exceeded, the oldest events (by insertion time) are pruned FIFO.
	EventRetention int
	// RetentionByAccess evicts by last_accessed_at (LRU) instead of inserted_at.
	RetentionByAccess bool
	// DBDiskMaxPercent starts pruning event rows when SQLite files exceed this
	// percentage of the attached filesystem capacity. A zero value disables it.
	DBDiskMaxPercent int
	// DBDiskPruneTargetPercent is the target SQLite-file usage after disk-budget pruning.
	DBDiskPruneTargetPercent int
	// DBMaxBytes starts pruning when SQLite and its sidecars exceed this many
	// bytes. Zero disables the fixed-size budget.
	DBMaxBytes int64
	// DBPruneTargetBytes is the post-prune target for the fixed-size budget.
	DBPruneTargetBytes int64
	// DiskPressurePercent triggers automatic compact/vacuum when data volume usage exceeds this.
	DiskPressurePercent int
	// VacuumTimeout bounds full VACUUM and startup compact under disk pressure.
	VacuumTimeout time.Duration
	// HydrationEnabled controls background projection rebuild + stale hydrator.
	HydrationEnabled bool
	// HydrationSweepInterval controls stale hydration sweep pacing.
	HydrationSweepInterval time.Duration
	// GuestSliceV2Enabled replaces the overlapping anonymous seed and hot-feed
	// loops with one readiness-gated, resumable generation scheduler.
	GuestSliceV2Enabled      bool
	GuestSliceInterval       time.Duration
	GuestSliceBudget         time.Duration
	GuestSliceCohortLimit    int
	GuestSliceCandidateLimit int
	GuestSlicePublishLimit   int
	GuestSliceTrustLimit     int
	GuestSliceTrustTTL       time.Duration
	GuestSliceMetadataTTL    time.Duration
	// WOTMaxAuthors caps WoT-expanded authors before SQLite feed queries run.
	WOTMaxAuthors int
	// SearchRateBurst controls /search token-bucket burst size.
	SearchRateBurst int
	// SearchRatePerSec controls /search token-bucket refill rate.
	SearchRatePerSec float64
	// AnonymousRateBurst controls per-IP burst size for anonymous expensive routes.
	AnonymousRateBurst int
	// AnonymousRatePerSec controls per-IP refill rate for anonymous expensive routes.
	AnonymousRatePerSec float64
	// BotRateBurst controls per-IP burst size for bot-looking expensive routes.
	BotRateBurst int
	// BotRatePerSec controls per-IP refill rate for bot-looking expensive routes.
	BotRatePerSec float64
	// ViewerRateBurst controls per-viewer burst size for signed-in expensive routes.
	ViewerRateBurst int
	// ViewerRatePerSec controls per-viewer refill rate for signed-in expensive routes.
	ViewerRatePerSec float64
	// SeedCrawlerEnabled toggles the background WoT seed crawler.
	SeedCrawlerEnabled bool
	// SeedCrawlerInterval is the delay between crawl ticks (each tick processes a batch).
	SeedCrawlerInterval time.Duration
	// SeedCrawlerAuthorBatch caps stale seed contacts processed per tick.
	SeedCrawlerAuthorBatch int
	// SeedCrawlerFetchLimit caps notes requested per author in a single seed-crawl tick
	// (default 100; profile first page is 30; deeper history still bounded by lookback).
	SeedCrawlerFetchLimit int
	// SeedCrawlerAuthorNoteLookback is the oldest note created_at the seed crawler
	// will request per author (0 disables the lower bound). Limits deep history pulls.
	SeedCrawlerAuthorNoteLookback time.Duration
	// SeedCrawlerReplyWarmLimit caps thread-reply warms per author per tick.
	SeedCrawlerReplyWarmLimit int
	// SeedBootstrapFollowEnqueueLimit bounds the SQLite page size while enqueueing seed follows at startup.
	SeedBootstrapFollowEnqueueLimit int
	// SeedBootstrapFollowEnqueueMaxTotal caps total follows enqueued per seed at bootstrap (pages stop after this).
	SeedBootstrapFollowEnqueueMaxTotal int
	// SeedFrontierPauseThreshold skips or reduces bootstrap enqueue when stale seedContact rows exceed this.
	SeedFrontierPauseThreshold int
	// SeedBootstrapSecondaryMaxTotal caps bootstrap frontier enqueue for non-primary named seeds.
	SeedBootstrapSecondaryMaxTotal int
	// SeedContactMaxFailCount excludes seedContact rows from background work
	// after this many consecutive failures until re-touched.
	SeedContactMaxFailCount int
	// SeedContactFollowEnqueuePerTick bounds the SQLite page size used while
	// enqueueing discovered follows for one processed contact.
	SeedContactFollowEnqueuePerTick int
	// TrendingSweepInterval controls background trending recompute pacing.
	TrendingSweepInterval time.Duration
	// TrendingMinRecompute is the staleness floor before recomputing cache.
	TrendingMinRecompute time.Duration
	// ActiveViewerTrendingEnabled runs the per-viewer trending warm loop. Enabled
	// by default so signed-in WoT trend feeds and sidebars stay populated; set
	// PTXT_ACTIVE_VIEWER_TRENDING=0 on very small instances to reduce SQLite load.
	ActiveViewerTrendingEnabled bool
	// HotFeedCrawlerEnabled keeps recent note heads hot for the default seed
	// cohort and recently active signed-in viewers.
	HotFeedCrawlerEnabled bool
	// HotFeedCrawlerInterval is the delay between hot feed crawl ticks.
	HotFeedCrawlerInterval time.Duration
	// HotFeedCrawlerCohortLimit caps how many cohorts are processed per tick.
	HotFeedCrawlerCohortLimit int
	// HotFeedCrawlerAuthorLimit caps authors refreshed per cohort per tick.
	HotFeedCrawlerAuthorLimit int
	// HotFeedCrawlerFetchLimit caps notes requested per hot author batch.
	HotFeedCrawlerFetchLimit int
	// HotFeedCrawlerLookback bounds relay queries to recent notes.
	HotFeedCrawlerLookback time.Duration
	// HotFeedCrawlerSnapshotThrottle caps first-page snapshot rebuild frequency
	// per cohort.
	HotFeedCrawlerSnapshotThrottle time.Duration
	// ReplaceableHistory keeps superseded kind 0 / 3 / 10002 rows in SQLite.
	// When false, older revisions for the same (pubkey, kind) are deleted after insert.
	ReplaceableHistory bool
	// IngestVerifyParallel caps workers for staged relay batch validation in
	// nostrx.Client before fetched events are returned to the store path.
	IngestVerifyParallel int
	Debug                bool
	// CoalesceEnabled toggles per-URL request coalescing in front of GET
	// handlers. Off by default; only meaningful behind a CDN that can absorb
	// the 302 redirects late arrivers receive.
	CoalesceEnabled bool
	// CoalesceBuckets is the number of FNV-1a buckets URLs are hashed into.
	CoalesceBuckets int
	// CoalesceTimeout caps how long a contended request waits for the lead
	// renderer before timing out with 504.
	CoalesceTimeout time.Duration
	// RelayMaxOutboundConns caps concurrent outbound relay WebSocket operations
	// process-wide in nostrx (0 = unlimited).
	RelayMaxOutboundConns int
	// WarmJobTimeout bounds wall time for a single warm-queue job.
	WarmJobTimeout time.Duration
	// WarmMaxAuthorsPerJob caps authors processed per warm "authors" job (remainder re-enqueued).
	WarmMaxAuthorsPerJob int
	// WarmMaxNoteIDsPerJob caps note IDs per warm noteReplies / noteReactions job (remainder re-enqueued).
	WarmMaxNoteIDsPerJob int
	// HealthProbeEnabled runs a periodic HTTP self-probe to detect wedged handlers.
	HealthProbeEnabled bool
	// HealthProbeInterval is the delay between self-probes.
	HealthProbeInterval time.Duration
	// HealthProbePath is the URL path to GET (e.g. "/" or "/healthz").
	HealthProbePath string
	// HealthProbeTimeout bounds each probe request.
	HealthProbeTimeout time.Duration
	// HealthProbeDegradedThreshold marks /healthz degraded after this many consecutive probe failures.
	HealthProbeDegradedThreshold int
	// PprofAddr controls the always-on net/http/pprof + expvar listener.
	// Defaults to "127.0.0.1:6060" so goroutine/heap profiles are reachable
	// from on-host triage (SSM/SSH) without flipping Debug. Set empty to
	// disable. Bind to a non-loopback address only behind explicit auth.
	PprofAddr string
	// DesktopMode enables loopback-only helpers used by the Electron desktop shell.
	DesktopMode bool
	// DesktopSessionToken authenticates the Electron renderer to every private
	// loopback application route. It is never included in bootstrap data or logs.
	DesktopSessionToken string
}

func Load() Config {
	guestSliceV2 := boolEnv("PTXT_GUEST_SLICE_V2", false)
	warmWorkersDefault, warmQueueDefault := 4, 256
	warmTimeoutDefault := 90 * time.Second
	relayMaxDefault, hydrationReplyBatchDefault := 48, 32
	if guestSliceV2 {
		warmWorkersDefault, warmQueueDefault = 2, 128
		warmTimeoutDefault = 20 * time.Second
		relayMaxDefault, hydrationReplyBatchDefault = 12, 8
	}
	// Default relay set. nostr.wine is intentionally omitted: it is a paid /
	// member-only relay (see https://docs.nostr.wine), so anonymous reads
	// fail closed and every query against it just inflates latency and
	// failure metrics. Paying members can re-add it via PTXT_RELAYS once we
	// support per-relay NIP-42 auth.
	defaultRelays := splitEnv("PTXT_RELAYS", []string{
		"wss://relay.primal.net",
		"wss://relay.damus.io",
		"wss://nos.lol",
	})
	metadataRelays := splitEnv("PTXT_METADATA_RELAYS", defaultRelays)
	indexerRelays := splitEnv("PTXT_INDEXER_RELAYS", []string{
		"wss://relay.nostr.band",
		"wss://relay.primal.net",
	})
	indexerNIP50Relays := splitEnv("PTXT_INDEXER_NIP50_RELAYS", []string{
		"wss://relay.nostr.band",
		"wss://relay.primal.net",
	})
	trendingSearchRelays := splitEnv("PTXT_TRENDING_SEARCH_RELAYS", []string{
		"wss://relay.nostr.band",
		"wss://search.nos.today",
		"wss://relay.primal.net",
		"wss://relay.ditto.pub",
	})

	cfg := Config{
		Addr:                                     env("PTXT_ADDR", ":8080"),
		DBPath:                                   env("PTXT_DB", "data/ptxt-nstr.sqlite"),
		RequestTimeout:                           durationEnv("PTXT_REQUEST_TIMEOUT_MS", 3500*time.Millisecond),
		RebuildProjections:                       boolEnv("PTXT_REBUILD_PROJECTIONS", false),
		OptionalRelayBackend:                     boolEnv("PTXT_OPTIONAL_RELAY_BACKEND", false),
		ShareCacheEnabled:                        boolEnv("PTXT_SHARE_CACHE", false),
		ShareServerTransitionalFallbacks:         boolEnv("PTXT_SHARE_SERVER_TRANSITIONAL_FALLBACKS", false),
		CompactOnStart:                           boolEnv("PTXT_COMPACT_ON_START", false),
		DefaultRelays:                            nostrx.NormalizeRelayList(defaultRelays, nostrx.MaxRelays),
		MetadataRelays:                           nostrx.NormalizeRelayList(metadataRelays, nostrx.MaxRelays),
		IndexerRelays:                            nostrx.NormalizeRelayList(indexerRelays, 6),
		IndexerNIP50Relays:                       nostrx.NormalizeRelayList(indexerNIP50Relays, 4),
		TrendingSearchRelays:                     nostrx.NormalizeRelayList(trendingSearchRelays, 4),
		IndexerMaxRelays:                         intEnv("PTXT_INDEXER_MAX_RELAYS", 6),
		IndexerNIP50MaxRelays:                    intEnv("PTXT_INDEXER_NIP50_MAX_RELAYS", 4),
		TrendingSearchMaxRelays:                  intEnv("PTXT_TRENDING_SEARCH_MAX_RELAYS", 4),
		CuratedPubkeys:                           splitPubkeyEnv("PTXT_CURATED_PUBKEYS"),
		ThreadMaxRelays:                          intEnv("PTXT_THREAD_MAX_RELAYS", 16),
		ThreadOutboxMaxRouteGroups:               intEnv("PTXT_THREAD_OUTBOX_MAX_ROUTE_GROUPS", 8),
		ThreadOutboxMaxRelaysPerAuthor:           intEnv("PTXT_THREAD_OUTBOX_MAX_RELAYS_PER_AUTHOR", 0),
		ThreadContextWarmMaxIDs:                  intEnv("PTXT_THREAD_CONTEXT_WARM_MAX_IDS", 48),
		HydrationNoteRepliesBatch:                intEnv("PTXT_HYDRATION_NOTE_REPLIES_BATCH", hydrationReplyBatchDefault),
		OutboxMaxRelaysPerAuthor:                 intEnv("PTXT_OUTBOX_MAX_RELAYS_PER_AUTHOR", nostrx.MaxRelays),
		OutboxMaxRouteGroups:                     intEnv("PTXT_OUTBOX_MAX_ROUTE_GROUPS", 6),
		OutboxFoFSeeds:                           intEnv("PTXT_OUTBOX_FOF_SEEDS", 40),
		FeedWindow:                               durationEnvDuration("PTXT_FEED_WINDOW", 7*24*time.Hour),
		EventRetention:                           nonNegativeIntEnv("PTXT_EVENT_RETENTION", 20000),
		RetentionByAccess:                        boolEnv("PTXT_RETENTION_BY_ACCESS", false),
		DBDiskMaxPercent:                         intEnv("PTXT_DB_MAX_DISK_PERCENT", 0),
		DBDiskPruneTargetPercent:                 intEnv("PTXT_DB_PRUNE_TARGET_PERCENT", 0),
		DBMaxBytes:                               nonNegativeInt64Env("PTXT_DB_MAX_BYTES", 0),
		DBPruneTargetBytes:                       nonNegativeInt64Env("PTXT_DB_PRUNE_TARGET_BYTES", 0),
		DiskPressurePercent:                      intEnv("PTXT_DB_DISK_PRESSURE_PERCENT", 85),
		VacuumTimeout:                            durationEnvDuration("PTXT_VACUUM_TIMEOUT", 60*time.Minute),
		HydrationEnabled:                         boolEnv("PTXT_HYDRATION_ENABLED", true),
		HydrationSweepInterval:                   durationEnvDuration("PTXT_HYDRATION_SWEEP_INTERVAL", 5*time.Minute),
		GuestSliceV2Enabled:                      guestSliceV2,
		GuestSliceInterval:                       durationEnvDuration("PTXT_GUEST_SLICE_INTERVAL", 5*time.Minute),
		GuestSliceBudget:                         durationEnvDuration("PTXT_GUEST_SLICE_BUDGET", 45*time.Second),
		GuestSliceCohortLimit:                    intEnv("PTXT_GUEST_SLICE_COHORT_LIMIT", 600),
		GuestSliceCandidateLimit:                 intEnv("PTXT_GUEST_SLICE_CANDIDATE_LIMIT", 60),
		GuestSlicePublishLimit:                   intEnv("PTXT_GUEST_SLICE_PUBLISH_LIMIT", 30),
		GuestSliceTrustLimit:                     intEnv("PTXT_GUEST_SLICE_TRUST_LIMIT", 100000),
		GuestSliceTrustTTL:                       durationEnvDuration("PTXT_GUEST_SLICE_TRUST_TTL", 6*time.Hour),
		GuestSliceMetadataTTL:                    durationEnvDuration("PTXT_GUEST_SLICE_METADATA_TTL", 24*time.Hour),
		WOTMaxAuthors:                            intEnv("PTXT_WOT_MAX_AUTHORS", 240),
		SearchRateBurst:                          intEnv("PTXT_SEARCH_RATE_BURST", 5),
		SearchRatePerSec:                         floatEnv("PTXT_SEARCH_RATE_PER_SEC", 1),
		AnonymousRateBurst:                       intEnv("PTXT_ANON_RATE_BURST", 30),
		AnonymousRatePerSec:                      floatEnv("PTXT_ANON_RATE_PER_SEC", 2),
		BotRateBurst:                             intEnv("PTXT_BOT_RATE_BURST", 6),
		BotRatePerSec:                            floatEnv("PTXT_BOT_RATE_PER_SEC", 0.1),
		ViewerRateBurst:                          intEnv("PTXT_VIEWER_RATE_BURST", 240),
		ViewerRatePerSec:                         floatEnv("PTXT_VIEWER_RATE_PER_SEC", 16),
		SeedCrawlerEnabled:                       boolEnv("PTXT_SEED_CRAWLER_ENABLED", true),
		SeedCrawlerInterval:                      durationEnvDuration("PTXT_SEED_CRAWLER_INTERVAL", 20*time.Second),
		SeedCrawlerAuthorBatch:                   intEnv("PTXT_SEED_CRAWLER_AUTHOR_BATCH", 16),
		SeedCrawlerFetchLimit:                    intEnv("PTXT_SEED_CRAWLER_FETCH_LIMIT", 60),
		SeedCrawlerAuthorNoteLookback:            seedAuthorNoteLookbackEnv("PTXT_SEED_CRAWLER_AUTHOR_NOTE_LOOKBACK", 120*24*time.Hour),
		SeedCrawlerReplyWarmLimit:                intEnv("PTXT_SEED_CRAWLER_REPLY_WARM_LIMIT", 48),
		SeedBootstrapFollowEnqueueLimit:          intEnv("PTXT_SEED_BOOTSTRAP_FOLLOW_ENQUEUE_LIMIT", 80),
		SeedBootstrapFollowEnqueueMaxTotal:       intEnv("PTXT_SEED_BOOTSTRAP_FOLLOW_ENQUEUE_MAX_TOTAL", 80),
		SeedFrontierPauseThreshold:               intEnv("PTXT_SEED_FRONTIER_PAUSE_THRESHOLD", 1500),
		SeedBootstrapSecondaryMaxTotal:           intEnv("PTXT_SEED_BOOTSTRAP_SECONDARY_MAX_TOTAL", 40),
		SeedContactMaxFailCount:                  intEnv("PTXT_SEED_CONTACT_MAX_FAIL_COUNT", 12),
		SeedContactFollowEnqueuePerTick:          intEnv("PTXT_SEED_CONTACT_FOLLOW_ENQUEUE_PER_TICK", 120),
		TrendingSweepInterval:                    durationEnvDuration("PTXT_TRENDING_SWEEP_INTERVAL", 5*time.Minute),
		TrendingMinRecompute:                     durationEnvDuration("PTXT_TRENDING_MIN_RECOMPUTE", 20*time.Minute),
		ActiveViewerTrendingEnabled:              boolEnv("PTXT_ACTIVE_VIEWER_TRENDING", true),
		HotFeedCrawlerEnabled:                    boolEnv("PTXT_HOT_FEED_CRAWLER_ENABLED", true),
		HotFeedCrawlerInterval:                   durationEnvDuration("PTXT_HOT_FEED_CRAWLER_INTERVAL", DefaultHotFeedCrawlerInterval),
		HotFeedCrawlerCohortLimit:                intEnv("PTXT_HOT_FEED_CRAWLER_COHORT_LIMIT", DefaultHotFeedCrawlerCohortLimit),
		HotFeedCrawlerAuthorLimit:                intEnv("PTXT_HOT_FEED_CRAWLER_AUTHOR_LIMIT", DefaultHotFeedCrawlerAuthorLimit),
		HotFeedCrawlerFetchLimit:                 intEnv("PTXT_HOT_FEED_CRAWLER_FETCH_LIMIT", DefaultHotFeedCrawlerFetchLimit),
		HotFeedCrawlerLookback:                   durationEnvDuration("PTXT_HOT_FEED_CRAWLER_LOOKBACK", DefaultHotFeedCrawlerLookback),
		HotFeedCrawlerSnapshotThrottle:           durationEnvDuration("PTXT_HOT_FEED_CRAWLER_SNAPSHOT_THROTTLE", DefaultHotFeedCrawlerSnapshotThrottle),
		ReplaceableHistory:                       boolEnv("PTXT_REPLACEABLE_HISTORY", true),
		IngestVerifyParallel:                     ingestVerifyParallelEnv(),
		Debug:                                    boolEnv("PTXT_DEBUG", false),
		CoalesceEnabled:                          boolEnv("PTXT_COALESCE_ENABLED", false),
		CoalesceBuckets:                          intEnv("PTXT_COALESCE_BUCKETS", DefaultCoalesceBuckets),
		CoalesceTimeout:                          durationEnv("PTXT_COALESCE_TIMEOUT_MS", 4000*time.Millisecond),
		RelayMaxOutboundConns:                    relayMaxOutboundConnsEnv(relayMaxDefault),
		WarmJobTimeout:                           durationEnv("PTXT_WARM_JOB_TIMEOUT_MS", warmTimeoutDefault),
		WarmMaxAuthorsPerJob:                     intEnv("PTXT_WARM_MAX_AUTHORS_PER_JOB", 16),
		WarmMaxNoteIDsPerJob:                     intEnv("PTXT_WARM_MAX_NOTE_IDS_PER_JOB", 32),
		WarmWorkers:                              intEnv("PTXT_WARM_WORKERS", warmWorkersDefault),
		WarmQueueCapacity:                        intEnv("PTXT_WARM_QUEUE_CAPACITY", warmQueueDefault),
		NIP50FallbackRatePerMin:                  intEnv("PTXT_NIP50_FALLBACK_RATE", 30),
		KnownViewerMax:                           intEnv("PTXT_KNOWN_VIEWER_MAX", 512),
		ViewerCrawlerEnabled:                     boolEnv("PTXT_VIEWER_CRAWLER_ENABLED", true),
		ViewerCrawlerInterval:                    durationEnvDuration("PTXT_VIEWER_CRAWLER_INTERVAL", 30*time.Second),
		ViewerCrawlerDegreeTwoInterval:           durationEnvDuration("PTXT_VIEWER_CRAWLER_DEGREE2_INTERVAL", 5*time.Minute),
		ViewerCrawlerDegreeThreeInterval:         durationEnvDuration("PTXT_VIEWER_CRAWLER_DEGREE3_INTERVAL", 30*time.Minute),
		ViewerCrawlerReducedInterval:             durationEnvDuration("PTXT_VIEWER_CRAWLER_REDUCED_INTERVAL", 5*time.Minute),
		ViewerCrawlerDirectMetadataInterval:      durationEnvDuration("PTXT_VIEWER_CRAWLER_DIRECT_METADATA_INTERVAL", 15*time.Minute),
		ViewerCrawlerDegreeTwoMetadataInterval:   durationEnvDuration("PTXT_VIEWER_CRAWLER_DEGREE2_METADATA_INTERVAL", 6*time.Hour),
		ViewerCrawlerDegreeThreeMetadataInterval: durationEnvDuration("PTXT_VIEWER_CRAWLER_DEGREE3_METADATA_INTERVAL", 24*time.Hour),
		ViewerCrawlerProfileInterval:             durationEnvDuration("PTXT_VIEWER_CRAWLER_PROFILE_INTERVAL", 6*time.Hour),
		ViewerCrawlerBatch:                       intEnv("PTXT_VIEWER_CRAWLER_BATCH", 8),
		ViewerCrawlerReplyWarmLimit:              intEnv("PTXT_VIEWER_CRAWLER_REPLY_WARM_LIMIT", 48),
		ViewerCrawlerFollowEnqueuePerTick:        intEnv("PTXT_VIEWER_CRAWLER_FOLLOW_ENQUEUE_PER_TICK", 80),
		HealthProbeEnabled:                       boolEnv("PTXT_HEALTH_PROBE_ENABLED", false),
		HealthProbeInterval:                      durationEnvDuration("PTXT_HEALTH_PROBE_INTERVAL", 30*time.Second),
		HealthProbePath:                          env("PTXT_HEALTH_PROBE_PATH", "/healthz"),
		HealthProbeTimeout:                       durationEnv("PTXT_HEALTH_PROBE_TIMEOUT_MS", 12_000*time.Millisecond),
		HealthProbeDegradedThreshold:             intEnv("PTXT_HEALTH_PROBE_DEGRADED_THRESHOLD", 3),
		PprofAddr:                                pprofAddrEnv("PTXT_PPROF_ADDR", "127.0.0.1:6060"),
		DesktopMode:                              boolEnv("PTXT_DESKTOP_MODE", false),
		DesktopSessionToken:                      desktopSessionTokenEnv(),
	}
	cfg.ServerMode = serverModeEnv("PTXT_SERVER_MODE", cfg.OptionalRelayBackend)

	slog.Info(
		"config loaded",
		"addr", cfg.Addr,
		"db_path", cfg.DBPath,
		"server_mode", cfg.ServerMode,
		"rebuild_projections", cfg.RebuildProjections,
		"compact_on_start", cfg.CompactOnStart,
		"optional_relay_backend", cfg.OptionalRelayBackend,
		"share_cache_enabled", cfg.ShareCacheEnabled,
		"share_server_transitional_fallbacks", cfg.ShareServerTransitionalFallbacks,
		"default_relays", len(cfg.DefaultRelays),
		"metadata_relays", len(cfg.MetadataRelays),
		"outbox_max_relays_per_author", cfg.OutboxMaxRelaysPerAuthor,
		"outbox_max_route_groups", cfg.OutboxMaxRouteGroups,
		"outbox_fof_seeds", cfg.OutboxFoFSeeds,
		"feed_window", cfg.FeedWindow,
		"event_retention", cfg.EventRetention,
		"retention_by_access", cfg.RetentionByAccess,
		"db_disk_max_percent", cfg.DBDiskMaxPercent,
		"db_disk_prune_target_percent", cfg.DBDiskPruneTargetPercent,
		"db_max_bytes", cfg.DBMaxBytes,
		"db_prune_target_bytes", cfg.DBPruneTargetBytes,
		"disk_pressure_percent", cfg.DiskPressurePercent,
		"vacuum_timeout", cfg.VacuumTimeout,
		"hydration_enabled", cfg.HydrationEnabled,
		"hydration_sweep_interval", cfg.HydrationSweepInterval,
		"guest_slice_v2", cfg.GuestSliceV2Enabled,
		"guest_slice_interval", cfg.GuestSliceInterval,
		"guest_slice_budget", cfg.GuestSliceBudget,
		"guest_slice_cohort_limit", cfg.GuestSliceCohortLimit,
		"wot_max_authors", cfg.WOTMaxAuthors,
		"search_rate_burst", cfg.SearchRateBurst,
		"search_rate_per_sec", cfg.SearchRatePerSec,
		"seed_crawler_enabled", cfg.SeedCrawlerEnabled,
		"seed_crawler_interval", cfg.SeedCrawlerInterval,
		"seed_crawler_author_batch", cfg.SeedCrawlerAuthorBatch,
		"seed_crawler_fetch_limit", cfg.SeedCrawlerFetchLimit,
		"seed_crawler_author_note_lookback", cfg.SeedCrawlerAuthorNoteLookback,
		"seed_crawler_reply_warm_limit", cfg.SeedCrawlerReplyWarmLimit,
		"seed_bootstrap_follow_enqueue_limit", cfg.SeedBootstrapFollowEnqueueLimit,
		"seed_contact_max_fail_count", cfg.SeedContactMaxFailCount,
		"seed_contact_follow_enqueue_per_tick", cfg.SeedContactFollowEnqueuePerTick,
		"trending_sweep_interval", cfg.TrendingSweepInterval,
		"trending_min_recompute", cfg.TrendingMinRecompute,
		"active_viewer_trending", cfg.ActiveViewerTrendingEnabled,
		"hot_feed_crawler_enabled", cfg.HotFeedCrawlerEnabled,
		"hot_feed_crawler_interval", cfg.HotFeedCrawlerInterval,
		"hot_feed_crawler_cohort_limit", cfg.HotFeedCrawlerCohortLimit,
		"hot_feed_crawler_author_limit", cfg.HotFeedCrawlerAuthorLimit,
		"hot_feed_crawler_fetch_limit", cfg.HotFeedCrawlerFetchLimit,
		"hot_feed_crawler_lookback", cfg.HotFeedCrawlerLookback,
		"hot_feed_crawler_snapshot_throttle", cfg.HotFeedCrawlerSnapshotThrottle,
		"replaceable_history", cfg.ReplaceableHistory,
		"ingest_verify_parallel", cfg.IngestVerifyParallel,
		"debug_enabled", cfg.Debug,
		"coalesce_enabled", cfg.CoalesceEnabled,
		"coalesce_buckets", cfg.CoalesceBuckets,
		"coalesce_timeout", cfg.CoalesceTimeout,
		"relay_max_outbound_conns", cfg.RelayMaxOutboundConns,
		"warm_job_timeout", cfg.WarmJobTimeout,
		"warm_max_authors_per_job", cfg.WarmMaxAuthorsPerJob,
		"warm_max_note_ids_per_job", cfg.WarmMaxNoteIDsPerJob,
		"health_probe_enabled", cfg.HealthProbeEnabled,
		"health_probe_interval", cfg.HealthProbeInterval,
		"health_probe_path", cfg.HealthProbePath,
		"health_probe_timeout", cfg.HealthProbeTimeout,
		"health_probe_degraded_threshold", cfg.HealthProbeDegradedThreshold,
		"pprof_addr", cfg.PprofAddr,
		"desktop_mode", cfg.DesktopMode,
	)

	return cfg
}

func desktopSessionTokenEnv() string {
	if token := strings.TrimSpace(os.Getenv("PTXT_DESKTOP_SESSION_TOKEN")); token != "" {
		return token
	}
	// Compatibility with packages launched before the token protected all
	// application routes rather than only activity updates.
	return strings.TrimSpace(os.Getenv("PTXT_DESKTOP_ACTIVITY_TOKEN"))
}

// relayMaxOutboundConnsEnv returns the caller-selected runtime default when unset.
// Set PTXT_RELAY_MAX_OUTBOUND_CONNS=0 for unlimited (tests / debugging).
func relayMaxOutboundConnsEnv(fallback int) int {
	raw, ok := os.LookupEnv("PTXT_RELAY_MAX_OUTBOUND_CONNS")
	if !ok {
		return fallback
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return fallback
	}
	return v
}

func env(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

// splitPubkeyEnv parses comma-separated hex pubkeys or npubs from env.
func splitPubkeyEnv(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		pk, err := nostrx.DecodeIdentifier(part)
		if err == nil && pk != "" {
			out = append(out, pk)
			continue
		}
		slog.Warn("ignored invalid pubkey in env", "key", key, "value", part, "err", err)
	}
	return out
}

func splitEnv(key string, fallback []string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	var values []string
	for _, part := range strings.Split(raw, ",") {
		if value := strings.TrimSpace(part); value != "" {
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		return fallback
	}
	return values
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms <= 0 {
		return fallback
	}
	return time.Duration(ms) * time.Millisecond
}

func durationEnvDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
		return parsed
	}
	return fallback
}

// seedAuthorNoteLookbackEnv parses PTXT_SEED_CRAWLER_AUTHOR_NOTE_LOOKBACK.
// "0", "0s", "off", or "false" disables the lower time bound (relay limit still caps volume).
func seedAuthorNoteLookbackEnv(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	lower := strings.ToLower(raw)
	if lower == "0" || lower == "0s" || lower == "off" || lower == "false" {
		return 0
	}
	if parsed, err := time.ParseDuration(raw); err == nil && parsed >= 0 {
		return parsed
	}
	return fallback
}

func intEnv(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func nonNegativeIntEnv(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func nonNegativeInt64Env(key string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func floatEnv(key string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func boolEnv(key string, fallback bool) bool {
	if value, ok := ParseBool(os.Getenv(key)); ok {
		return value
	}
	return fallback
}

// pprofAddrEnv resolves the pprof/expvar listen address. An unset variable
// returns the loopback default; explicit "off" / "false" / "disabled" / "0"
// disables the listener. Any other value (including a non-loopback host:port)
// is returned verbatim so operators can override deliberately.
func pprofAddrEnv(key, fallback string) string {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	trimmed := strings.TrimSpace(raw)
	switch strings.ToLower(trimmed) {
	case "", "off", "false", "disabled", "0":
		return ""
	}
	return trimmed
}

// ingestVerifyParallelEnv parses PTXT_INGEST_VERIFY_PARALLEL (0–32).
// Values 0 or 1 keep relay batch validation sequential; values 2–32 cap
// concurrent workers for staged relay ingest validation.
func ingestVerifyParallelEnv() int {
	raw := strings.TrimSpace(os.Getenv("PTXT_INGEST_VERIFY_PARALLEL"))
	if raw == "" {
		return 0
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return 0
	}
	if v > 32 {
		return 32
	}
	return v
}

func serverModeEnv(key string, optionalRelayBackend bool) string {
	raw, ok := os.LookupEnv(key)
	if ok {
		switch strings.ToLower(strings.TrimSpace(raw)) {
		case ServerModeApp:
			return ServerModeApp
		case ServerModeShare:
			return ServerModeShare
		}
	}
	if optionalRelayBackend {
		return ServerModeShare
	}
	return ServerModeApp
}

// ParseBool recognizes the common truthy/falsy token set used by both
// environment variables and HTTP query parameters. ok is false when raw is
// empty or unrecognized, letting callers fall back to their own defaults.
func ParseBool(raw string) (value bool, ok bool) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return false, false
	}
	switch raw {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}
