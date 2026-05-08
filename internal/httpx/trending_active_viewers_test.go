package httpx

import (
	"context"
	"strings"
	"testing"
	"time"

	"ptxt-nstr/internal/nostrx"
)

// primeActiveViewerCohort sets up a viewer whose follow graph contains
// `author`, saves a single note from `author` with one reply, and resolves
// the WoT cohort so the resolvedAuthors cache is hot. Returns the resolved
// cohort key and the depth used.
func primeActiveViewerCohort(t *testing.T, srv *Server) (viewer string, cohortKey string, wot webOfTrustOptions) {
	t.Helper()
	ctx := context.Background()
	st := srv.store
	viewer = strings.Repeat("a", 64)
	author := strings.Repeat("b", 64)
	replier := strings.Repeat("c", 64)
	noteID := strings.Repeat("1", 64)
	replyID := strings.Repeat("2", 64)

	now := time.Now().Unix()
	for _, ev := range []nostrx.Event{
		{ID: strings.Repeat("f", 64), PubKey: viewer, CreatedAt: now - 100, Kind: nostrx.KindFollowList, Tags: [][]string{{"p", author}}},
		{ID: noteID, PubKey: author, CreatedAt: now - 60, Kind: nostrx.KindTextNote, Content: "trendable"},
		{ID: replyID, PubKey: replier, CreatedAt: now - 30, Kind: nostrx.KindTextNote, Tags: [][]string{{"e", noteID, "", "root"}, {"e", noteID, "", "reply"}}, Content: "reply"},
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatalf("SaveEvent(%s) error = %v", ev.ID, err)
		}
	}
	wot = webOfTrustOptions{Enabled: true, Depth: 2}
	cohort, _, loggedOut := srv.resolveAuthorsAll(ctx, viewer, nil, wot)
	if loggedOut || len(cohort) == 0 {
		t.Fatalf("resolveAuthorsAll loggedOut=%v len=%d", loggedOut, len(cohort))
	}
	cohortKey = authorsCacheKey(cohort)
	if cohortKey == "" {
		t.Fatalf("expected non-empty cohort key for resolved authors")
	}
	return viewer, cohortKey, wot
}

func TestWarmActiveViewerTrendingPopulatesBothTimeframes(t *testing.T) {
	srv, _ := testServer(t)
	ctx := context.Background()

	viewer, cohortKey, wot := primeActiveViewerCohort(t, srv)
	// resolveAuthorsAll above already touches the activeViewers LRU because
	// the touch is wired into resolveAuthorsAll; assert the wiring works.
	if got := srv.activeViewers.Len(); got != 1 {
		t.Fatalf("activeViewers.Len = %d, want 1 (resolveAuthorsAll should auto-touch)", got)
	}
	_ = viewer

	srv.warmActiveViewerTrending(ctx)

	for _, tf := range []string{trending24h, trending1w} {
		items, computedAt, err := srv.store.ReadTrendingCache(ctx, tf, cohortKey)
		if err != nil {
			t.Fatalf("ReadTrendingCache(%s) error = %v", tf, err)
		}
		if computedAt == 0 {
			t.Fatalf("ReadTrendingCache(%s, cohortKey=%q): expected non-zero computedAt", tf, cohortKey)
		}
		if len(items) == 0 {
			t.Fatalf("ReadTrendingCache(%s, cohortKey=%q): expected at least one item", tf, cohortKey)
		}
	}

	snap := srv.metrics.Snapshot()
	counters := snap["counters"].(map[string]int64)
	if counters["trending.active_viewer.warm_attempt"] == 0 {
		t.Fatalf("expected warm_attempt counter > 0, got snapshot=%v", counters)
	}
	if counters["trending.active_viewer.warm_success"] == 0 {
		t.Fatalf("expected warm_success counter > 0, got snapshot=%v", counters)
	}
	// Sanity: depths recorded should match the wot we used.
	if wot.Depth != 2 {
		t.Fatalf("test setup regression: wot.Depth = %d, want 2", wot.Depth)
	}
}

func TestWarmActiveViewerTrendingSkipsWhenNoResolution(t *testing.T) {
	srv, _ := testServer(t)
	ctx := context.Background()

	// Touch a viewer who has never had their authors resolved. The warmer
	// must not synthesise a cohort or run BFS in the background.
	viewer := strings.Repeat("d", 64)
	srv.activeViewers.Touch(viewer, webOfTrustOptions{Enabled: true, Depth: 2}, time.Now())

	srv.warmActiveViewerTrending(ctx)

	counters := srv.metrics.Snapshot()["counters"].(map[string]int64)
	if counters["trending.active_viewer.warm_skip_no_resolution"] == 0 {
		t.Fatalf("expected warm_skip_no_resolution > 0, got %v", counters)
	}
	if counters["trending.active_viewer.warm_success"] != 0 {
		t.Fatalf("expected warm_success == 0 when no resolution, got %d", counters["trending.active_viewer.warm_success"])
	}
}

func TestWarmActiveViewerTrendingSkipsFresh(t *testing.T) {
	srv, _ := testServer(t)
	ctx := context.Background()

	_, cohortKey, _ := primeActiveViewerCohort(t, srv)

	// First pass populates rows.
	srv.warmActiveViewerTrending(ctx)
	first, _, err := srv.store.ReadTrendingCache(ctx, trending24h, cohortKey)
	if err != nil || len(first) == 0 {
		t.Fatalf("first warm did not populate trending cache: err=%v len=%d", err, len(first))
	}

	// Reset metrics by reading current values, then warm again immediately.
	before := srv.metrics.Snapshot()["counters"].(map[string]int64)["trending.active_viewer.warm_skip_fresh"]

	srv.warmActiveViewerTrending(ctx)

	after := srv.metrics.Snapshot()["counters"].(map[string]int64)["trending.active_viewer.warm_skip_fresh"]
	if after-before == 0 {
		t.Fatalf("expected warm_skip_fresh to increment on second pass, before=%d after=%d", before, after)
	}
}

func TestWarmActiveViewerTrendingRespectsRefreshLock(t *testing.T) {
	srv, _ := testServer(t)
	ctx := context.Background()

	_, cohortKey, _ := primeActiveViewerCohort(t, srv)

	// Hold the same beginRefresh keys the warmer would acquire so the warmer
	// short-circuits both timeframes via the shared in-flight check.
	for _, tf := range []string{trending24h, trending1w} {
		if !srv.beginRefresh("trending:" + tf + ":" + cohortKey) {
			t.Fatalf("beginRefresh(%s) returned false unexpectedly", tf)
		}
		t.Cleanup(func(key string) func() {
			return func() { srv.endRefresh(key) }
		}("trending:" + tf + ":" + cohortKey))
	}

	srv.warmActiveViewerTrending(ctx)

	counters := srv.metrics.Snapshot()["counters"].(map[string]int64)
	if counters["trending.active_viewer.warm_skip_in_flight"] < 2 {
		t.Fatalf("expected warm_skip_in_flight >= 2 when refreshes are held, got %v", counters)
	}
	if counters["trending.active_viewer.warm_success"] != 0 {
		t.Fatalf("expected warm_success == 0 when refreshes are held, got %d", counters["trending.active_viewer.warm_success"])
	}
}
