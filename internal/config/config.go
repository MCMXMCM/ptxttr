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

type Config struct {
	Addr           string
	DBPath         string
	RequestTimeout time.Duration
	// RebuildProjections controls full projection rebuild at startup.
	RebuildProjections bool
	// CompactOnStart triggers one-shot prune + vacuum at startup.
	CompactOnStart bool
	DefaultRelays  []string
	// MetadataRelays are preferred for profile/follow/relay-list hydration.
	MetadataRelays []string
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
	// HydrationEnabled controls background projection rebuild + stale hydrator.
	HydrationEnabled bool
	// HydrationSweepInterval controls stale hydration sweep pacing.
	HydrationSweepInterval time.Duration
	// WOTMaxAuthors caps WoT-expanded authors before SQLite feed queries run.
	WOTMaxAuthors int
	// SearchRateBurst controls /search token-bucket burst size.
	SearchRateBurst int
	// SearchRatePerSec controls /search token-bucket refill rate.
	SearchRatePerSec float64
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
	// SeedBootstrapFollowEnqueueLimit bounds the SQLite page size used while
	// enqueueing Jack's direct follows at startup.
	SeedBootstrapFollowEnqueueLimit int
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
	// ActiveViewerTrendingEnabled runs the per-viewer trending warm loop. Off by
	// default on small instances to reduce SQLite load; enable with
	// PTXT_ACTIVE_VIEWER_TRENDING=1 when you want signed-in cohort trending kept hot.
	ActiveViewerTrendingEnabled bool
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
}

func Load() Config {
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

	cfg := Config{
		Addr:                            env("PTXT_ADDR", ":8080"),
		DBPath:                          env("PTXT_DB", "data/ptxt-nstr.sqlite"),
		RequestTimeout:                  durationEnv("PTXT_REQUEST_TIMEOUT_MS", 3500*time.Millisecond),
		RebuildProjections:              boolEnv("PTXT_REBUILD_PROJECTIONS", false),
		CompactOnStart:                  boolEnv("PTXT_COMPACT_ON_START", false),
		DefaultRelays:                   nostrx.NormalizeRelayList(defaultRelays, nostrx.MaxRelays),
		MetadataRelays:                  nostrx.NormalizeRelayList(metadataRelays, nostrx.MaxRelays),
		OutboxMaxRelaysPerAuthor:        intEnv("PTXT_OUTBOX_MAX_RELAYS_PER_AUTHOR", nostrx.MaxRelays),
		OutboxMaxRouteGroups:            intEnv("PTXT_OUTBOX_MAX_ROUTE_GROUPS", 6),
		OutboxFoFSeeds:                  intEnv("PTXT_OUTBOX_FOF_SEEDS", 40),
		FeedWindow:                      durationEnvDuration("PTXT_FEED_WINDOW", 7*24*time.Hour),
		EventRetention:                  intEnv("PTXT_EVENT_RETENTION", 20000),
		HydrationEnabled:                boolEnv("PTXT_HYDRATION_ENABLED", true),
		HydrationSweepInterval:          durationEnvDuration("PTXT_HYDRATION_SWEEP_INTERVAL", 5*time.Minute),
		WOTMaxAuthors:                   intEnv("PTXT_WOT_MAX_AUTHORS", 240),
		SearchRateBurst:                 intEnv("PTXT_SEARCH_RATE_BURST", 5),
		SearchRatePerSec:                floatEnv("PTXT_SEARCH_RATE_PER_SEC", 1),
		SeedCrawlerEnabled:              boolEnv("PTXT_SEED_CRAWLER_ENABLED", true),
		SeedCrawlerInterval:             durationEnvDuration("PTXT_SEED_CRAWLER_INTERVAL", 20*time.Second),
		SeedCrawlerAuthorBatch:          intEnv("PTXT_SEED_CRAWLER_AUTHOR_BATCH", 16),
		SeedCrawlerFetchLimit:           intEnv("PTXT_SEED_CRAWLER_FETCH_LIMIT", 60),
		SeedCrawlerAuthorNoteLookback:   seedAuthorNoteLookbackEnv("PTXT_SEED_CRAWLER_AUTHOR_NOTE_LOOKBACK", 120*24*time.Hour),
		SeedCrawlerReplyWarmLimit:       intEnv("PTXT_SEED_CRAWLER_REPLY_WARM_LIMIT", 24),
		SeedBootstrapFollowEnqueueLimit: intEnv("PTXT_SEED_BOOTSTRAP_FOLLOW_ENQUEUE_LIMIT", 400),
		SeedContactMaxFailCount:         intEnv("PTXT_SEED_CONTACT_MAX_FAIL_COUNT", 12),
		SeedContactFollowEnqueuePerTick: intEnv("PTXT_SEED_CONTACT_FOLLOW_ENQUEUE_PER_TICK", 120),
		TrendingSweepInterval:           durationEnvDuration("PTXT_TRENDING_SWEEP_INTERVAL", 5*time.Minute),
		TrendingMinRecompute:            durationEnvDuration("PTXT_TRENDING_MIN_RECOMPUTE", 20*time.Minute),
		ActiveViewerTrendingEnabled:     boolEnv("PTXT_ACTIVE_VIEWER_TRENDING", false),
		ReplaceableHistory:              boolEnv("PTXT_REPLACEABLE_HISTORY", true),
		IngestVerifyParallel:            ingestVerifyParallelEnv(),
		Debug:                           boolEnv("PTXT_DEBUG", false),
		CoalesceEnabled:                 boolEnv("PTXT_COALESCE_ENABLED", false),
		CoalesceBuckets:                 intEnv("PTXT_COALESCE_BUCKETS", DefaultCoalesceBuckets),
		CoalesceTimeout:                 durationEnv("PTXT_COALESCE_TIMEOUT_MS", 4000*time.Millisecond),
		RelayMaxOutboundConns:           relayMaxOutboundConnsEnv(),
		WarmJobTimeout:                  durationEnv("PTXT_WARM_JOB_TIMEOUT_MS", 45_000*time.Millisecond),
		WarmMaxAuthorsPerJob:            intEnv("PTXT_WARM_MAX_AUTHORS_PER_JOB", 16),
		WarmMaxNoteIDsPerJob:            intEnv("PTXT_WARM_MAX_NOTE_IDS_PER_JOB", 12),
		HealthProbeEnabled:              boolEnv("PTXT_HEALTH_PROBE_ENABLED", false),
		HealthProbeInterval:             durationEnvDuration("PTXT_HEALTH_PROBE_INTERVAL", 30*time.Second),
		HealthProbePath:                 env("PTXT_HEALTH_PROBE_PATH", "/healthz"),
		HealthProbeTimeout:              durationEnv("PTXT_HEALTH_PROBE_TIMEOUT_MS", 12_000*time.Millisecond),
		HealthProbeDegradedThreshold:    intEnv("PTXT_HEALTH_PROBE_DEGRADED_THRESHOLD", 3),
		PprofAddr:                       pprofAddrEnv("PTXT_PPROF_ADDR", "127.0.0.1:6060"),
	}

	slog.Info(
		"config loaded",
		"addr", cfg.Addr,
		"db_path", cfg.DBPath,
		"rebuild_projections", cfg.RebuildProjections,
		"compact_on_start", cfg.CompactOnStart,
		"default_relays", len(cfg.DefaultRelays),
		"metadata_relays", len(cfg.MetadataRelays),
		"outbox_max_relays_per_author", cfg.OutboxMaxRelaysPerAuthor,
		"outbox_max_route_groups", cfg.OutboxMaxRouteGroups,
		"outbox_fof_seeds", cfg.OutboxFoFSeeds,
		"feed_window", cfg.FeedWindow,
		"event_retention", cfg.EventRetention,
		"hydration_enabled", cfg.HydrationEnabled,
		"hydration_sweep_interval", cfg.HydrationSweepInterval,
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
	)

	return cfg
}

// relayMaxOutboundConnsEnv returns default 48 concurrent relay sockets when unset.
// Set PTXT_RELAY_MAX_OUTBOUND_CONNS=0 for unlimited (tests / debugging).
func relayMaxOutboundConnsEnv() int {
	raw, ok := os.LookupEnv("PTXT_RELAY_MAX_OUTBOUND_CONNS")
	if !ok {
		return 48
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 48
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 0 {
		return 48
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
