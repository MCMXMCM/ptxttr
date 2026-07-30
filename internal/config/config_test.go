package config

import (
	"testing"
	"time"
)

func TestLoadHotFeedCrawlerDefaultsAndOverrides(t *testing.T) {
	for _, key := range []string{
		"PTXT_HOT_FEED_CRAWLER_ENABLED",
		"PTXT_HOT_FEED_CRAWLER_INTERVAL",
		"PTXT_HOT_FEED_CRAWLER_COHORT_LIMIT",
		"PTXT_HOT_FEED_CRAWLER_AUTHOR_LIMIT",
		"PTXT_HOT_FEED_CRAWLER_FETCH_LIMIT",
		"PTXT_HOT_FEED_CRAWLER_LOOKBACK",
		"PTXT_HOT_FEED_CRAWLER_SNAPSHOT_THROTTLE",
	} {
		t.Setenv(key, "")
	}

	cfg := Load()
	if !cfg.HotFeedCrawlerEnabled {
		t.Fatal("HotFeedCrawlerEnabled = false, want true by default")
	}
	if cfg.HotFeedCrawlerInterval != DefaultHotFeedCrawlerInterval {
		t.Fatalf("HotFeedCrawlerInterval = %v, want %v", cfg.HotFeedCrawlerInterval, DefaultHotFeedCrawlerInterval)
	}
	if cfg.HotFeedCrawlerCohortLimit != DefaultHotFeedCrawlerCohortLimit {
		t.Fatalf("HotFeedCrawlerCohortLimit = %d, want %d", cfg.HotFeedCrawlerCohortLimit, DefaultHotFeedCrawlerCohortLimit)
	}
	if cfg.HotFeedCrawlerAuthorLimit != DefaultHotFeedCrawlerAuthorLimit {
		t.Fatalf("HotFeedCrawlerAuthorLimit = %d, want %d", cfg.HotFeedCrawlerAuthorLimit, DefaultHotFeedCrawlerAuthorLimit)
	}
	if cfg.HotFeedCrawlerFetchLimit != DefaultHotFeedCrawlerFetchLimit {
		t.Fatalf("HotFeedCrawlerFetchLimit = %d, want %d", cfg.HotFeedCrawlerFetchLimit, DefaultHotFeedCrawlerFetchLimit)
	}
	if cfg.HotFeedCrawlerLookback != DefaultHotFeedCrawlerLookback {
		t.Fatalf("HotFeedCrawlerLookback = %v, want %v", cfg.HotFeedCrawlerLookback, DefaultHotFeedCrawlerLookback)
	}
	if cfg.HotFeedCrawlerSnapshotThrottle != DefaultHotFeedCrawlerSnapshotThrottle {
		t.Fatalf("HotFeedCrawlerSnapshotThrottle = %v, want %v", cfg.HotFeedCrawlerSnapshotThrottle, DefaultHotFeedCrawlerSnapshotThrottle)
	}

	t.Setenv("PTXT_HOT_FEED_CRAWLER_ENABLED", "0")
	t.Setenv("PTXT_HOT_FEED_CRAWLER_INTERVAL", "10s")
	t.Setenv("PTXT_HOT_FEED_CRAWLER_COHORT_LIMIT", "3")
	t.Setenv("PTXT_HOT_FEED_CRAWLER_AUTHOR_LIMIT", "7")
	t.Setenv("PTXT_HOT_FEED_CRAWLER_FETCH_LIMIT", "11")
	t.Setenv("PTXT_HOT_FEED_CRAWLER_LOOKBACK", "12h")
	t.Setenv("PTXT_HOT_FEED_CRAWLER_SNAPSHOT_THROTTLE", "30s")

	cfg = Load()
	if cfg.HotFeedCrawlerEnabled {
		t.Fatal("HotFeedCrawlerEnabled = true, want false override")
	}
	if cfg.HotFeedCrawlerInterval != 10*time.Second ||
		cfg.HotFeedCrawlerCohortLimit != 3 ||
		cfg.HotFeedCrawlerAuthorLimit != 7 ||
		cfg.HotFeedCrawlerFetchLimit != 11 ||
		cfg.HotFeedCrawlerLookback != 12*time.Hour ||
		cfg.HotFeedCrawlerSnapshotThrottle != 30*time.Second {
		t.Fatalf("hot feed override config not applied: %#v", cfg)
	}
}

func TestGuestSliceV2UsesSmallInstanceWorkerDefaults(t *testing.T) {
	for _, key := range []string{
		"PTXT_WARM_WORKERS", "PTXT_WARM_QUEUE_CAPACITY", "PTXT_WARM_JOB_TIMEOUT_MS",
		"PTXT_RELAY_MAX_OUTBOUND_CONNS", "PTXT_HYDRATION_NOTE_REPLIES_BATCH",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("PTXT_GUEST_SLICE_V2", "1")
	cfg := Load()
	if !cfg.GuestSliceV2Enabled || cfg.WarmWorkers != 2 || cfg.WarmQueueCapacity != 128 ||
		cfg.WarmJobTimeout != 20*time.Second || cfg.RelayMaxOutboundConns != 12 || cfg.HydrationNoteRepliesBatch != 8 {
		t.Fatalf("unexpected v2 small-instance defaults: %#v", cfg)
	}
}

func TestTrafficShieldUsesSmallInstanceDefaults(t *testing.T) {
	for _, key := range []string{
		"PTXT_ANON_RATE_BURST", "PTXT_ANON_RATE_PER_SEC",
		"PTXT_BOT_RATE_BURST", "PTXT_BOT_RATE_PER_SEC",
	} {
		t.Setenv(key, "")
	}
	cfg := Load()
	if cfg.AnonymousRateBurst != 30 || cfg.AnonymousRatePerSec != 2 ||
		cfg.BotRateBurst != 6 || cfg.BotRatePerSec != 0.1 {
		t.Fatalf("unexpected traffic shield defaults: %#v", cfg)
	}
}

func TestDurationEnvFallsBackForInvalidValues(t *testing.T) {
	t.Setenv("PTXT_TEST_TIMEOUT", "not-a-number")
	if got := durationEnv("PTXT_TEST_TIMEOUT", 1500*time.Millisecond); got != 1500*time.Millisecond {
		t.Fatalf("durationEnv invalid = %s, want fallback", got)
	}
	t.Setenv("PTXT_TEST_TIMEOUT", "0")
	if got := durationEnv("PTXT_TEST_TIMEOUT", 1500*time.Millisecond); got != 1500*time.Millisecond {
		t.Fatalf("durationEnv zero = %s, want fallback", got)
	}
}

func TestDurationEnvParsesMilliseconds(t *testing.T) {
	t.Setenv("PTXT_TEST_TIMEOUT", "2500")
	if got := durationEnv("PTXT_TEST_TIMEOUT", time.Second); got != 2500*time.Millisecond {
		t.Fatalf("durationEnv = %s, want 2500ms", got)
	}
}

func TestBoolEnvParsesKnownValues(t *testing.T) {
	t.Setenv("PTXT_TEST_BOOL", "yes")
	if !boolEnv("PTXT_TEST_BOOL", false) {
		t.Fatal("boolEnv yes = false, want true")
	}
	t.Setenv("PTXT_TEST_BOOL", "off")
	if boolEnv("PTXT_TEST_BOOL", true) {
		t.Fatal("boolEnv off = true, want false")
	}
	t.Setenv("PTXT_TEST_BOOL", "maybe")
	if !boolEnv("PTXT_TEST_BOOL", true) {
		t.Fatal("boolEnv unknown did not use fallback")
	}
}

func TestIntEnvFallsBackForInvalidValues(t *testing.T) {
	t.Setenv("PTXT_TEST_INT", "not-a-number")
	if got := intEnv("PTXT_TEST_INT", 42); got != 42 {
		t.Fatalf("intEnv invalid = %d, want fallback", got)
	}
	t.Setenv("PTXT_TEST_INT", "123")
	if got := intEnv("PTXT_TEST_INT", 42); got != 123 {
		t.Fatalf("intEnv = %d, want 123", got)
	}
}

func TestNonNegativeIntEnvAllowsZero(t *testing.T) {
	t.Setenv("PTXT_TEST_NON_NEGATIVE", "0")
	if got := nonNegativeIntEnv("PTXT_TEST_NON_NEGATIVE", 42); got != 0 {
		t.Fatalf("nonNegativeIntEnv zero = %d, want 0", got)
	}
	t.Setenv("PTXT_TEST_NON_NEGATIVE", "-1")
	if got := nonNegativeIntEnv("PTXT_TEST_NON_NEGATIVE", 42); got != 42 {
		t.Fatalf("nonNegativeIntEnv negative = %d, want fallback", got)
	}
}

func TestFloatEnvFallsBackForInvalidValues(t *testing.T) {
	t.Setenv("PTXT_TEST_FLOAT", "not-a-number")
	if got := floatEnv("PTXT_TEST_FLOAT", 1.5); got != 1.5 {
		t.Fatalf("floatEnv invalid = %f, want fallback", got)
	}
	t.Setenv("PTXT_TEST_FLOAT", "2.25")
	if got := floatEnv("PTXT_TEST_FLOAT", 1.5); got != 2.25 {
		t.Fatalf("floatEnv = %f, want 2.25", got)
	}
}

func TestDurationEnvDurationParsesStrings(t *testing.T) {
	t.Setenv("PTXT_TEST_WINDOW", "2h")
	if got := durationEnvDuration("PTXT_TEST_WINDOW", time.Hour); got != 2*time.Hour {
		t.Fatalf("durationEnvDuration = %s, want 2h", got)
	}
	t.Setenv("PTXT_TEST_WINDOW", "bad")
	if got := durationEnvDuration("PTXT_TEST_WINDOW", time.Hour); got != time.Hour {
		t.Fatalf("durationEnvDuration invalid = %s, want fallback", got)
	}
}

func TestSeedAuthorNoteLookbackEnv(t *testing.T) {
	t.Setenv("PTXT_SEED_LB", "720h")
	if got := seedAuthorNoteLookbackEnv("PTXT_SEED_LB", 24*time.Hour); got != 720*time.Hour {
		t.Fatalf("lookback = %s, want 720h", got)
	}
	t.Setenv("PTXT_SEED_LB", "0")
	if got := seedAuthorNoteLookbackEnv("PTXT_SEED_LB", 24*time.Hour); got != 0 {
		t.Fatalf("lookback disabled = %s, want 0", got)
	}
	t.Setenv("PTXT_SEED_LB", "")
	if got := seedAuthorNoteLookbackEnv("PTXT_SEED_LB", 24*time.Hour); got != 24*time.Hour {
		t.Fatalf("lookback empty = %s, want fallback", got)
	}
}

func TestIngestVerifyParallelEnv(t *testing.T) {
	t.Setenv("PTXT_INGEST_VERIFY_PARALLEL", "")
	if got := ingestVerifyParallelEnv(); got != 0 {
		t.Fatalf("empty = %d, want 0", got)
	}
	t.Setenv("PTXT_INGEST_VERIFY_PARALLEL", "4")
	if got := ingestVerifyParallelEnv(); got != 4 {
		t.Fatalf("4 = %d", got)
	}
	t.Setenv("PTXT_INGEST_VERIFY_PARALLEL", "99")
	if got := ingestVerifyParallelEnv(); got != 32 {
		t.Fatalf("cap = %d, want 32", got)
	}
	t.Setenv("PTXT_INGEST_VERIFY_PARALLEL", "-1")
	if got := ingestVerifyParallelEnv(); got != 0 {
		t.Fatalf("negative = %d, want 0", got)
	}
}

func TestServerModeEnvDefaultsAndCompatibility(t *testing.T) {
	t.Setenv("PTXT_SERVER_MODE", "")
	t.Setenv("PTXT_OPTIONAL_RELAY_BACKEND", "")
	t.Setenv("PTXT_SHARE_SERVER_TRANSITIONAL_FALLBACKS", "")
	cfg := Load()
	if cfg.ServerMode != ServerModeApp {
		t.Fatalf("default server mode = %q, want %q", cfg.ServerMode, ServerModeApp)
	}
	if cfg.ShareServerTransitionalFallbacks {
		t.Fatal("default share transitional fallbacks = true, want false")
	}

	t.Setenv("PTXT_OPTIONAL_RELAY_BACKEND", "1")
	cfg = Load()
	if cfg.ServerMode != ServerModeShare {
		t.Fatalf("optional relay backend compatibility mode = %q, want %q", cfg.ServerMode, ServerModeShare)
	}

	t.Setenv("PTXT_SERVER_MODE", "app")
	cfg = Load()
	if cfg.ServerMode != ServerModeApp {
		t.Fatalf("explicit app mode = %q, want %q", cfg.ServerMode, ServerModeApp)
	}

	t.Setenv("PTXT_SERVER_MODE", "share")
	cfg = Load()
	if cfg.ServerMode != ServerModeShare {
		t.Fatalf("explicit share mode = %q, want %q", cfg.ServerMode, ServerModeShare)
	}

	t.Setenv("PTXT_SHARE_SERVER_TRANSITIONAL_FALLBACKS", "0")
	cfg = Load()
	if cfg.ShareServerTransitionalFallbacks {
		t.Fatal("share transitional fallbacks override = true, want false")
	}
}
