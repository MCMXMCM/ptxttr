package httpx

import (
	"context"
	"time"
)

const (
	// activeViewerTrendingTicker matches runDefaultSeedGuestFeedHotLoop's
	// 3-minute cadence. Trending recompute itself is throttled by
	// activeViewerTrendingMinFreshness, so this just bounds how often we
	// even check the active-viewer set.
	activeViewerTrendingTicker = 3 * time.Minute
	// activeViewerTrendingWindow is how recently a viewer must have made a
	// request to count as "active" for the hot loop. Long enough to bridge a
	// few ticker periods plus normal browsing pauses, short enough that idle
	// users do not keep doing background work indefinitely.
	activeViewerTrendingWindow = 10 * time.Minute
	// activeViewerTrendingMaxFanout caps how many viewers we warm per tick
	// so a sudden flood of signed-in traffic cannot blow up SQLite contention.
	// Snapshot returns newest-first, so under pressure we favor the most
	// recently active viewers.
	activeViewerTrendingMaxFanout = 32
	// activeViewerTrendingTickTimeout bounds the per-tick context. Each
	// computeAndStoreCohortTrending is mostly local SQLite work, but with a
	// fanout of 32 and two timeframes per viewer we want a hard ceiling.
	activeViewerTrendingTickTimeout = 90 * time.Second
)

// runActiveViewerTrendingHotLoop keeps per-cohort trending_cache rows warm
// for signed-in users who have made a request recently. This is the
// signed-in analog of runDefaultSeedGuestFeedHotLoop: that loop covers the
// canonical guest cohort; this one covers each active viewer's WoT cohort.
//
// Without this loop, switching from the chronological feed to trend24h /
// trend7d for a logged-in WoT user can fall through to a synchronous heavy
// SQL path (TrendingSummariesByKinds with a large IN clause) because
// runTrendingSweeper only ever refreshes the cohort_key="" global row.
func (s *Server) runActiveViewerTrendingHotLoop() {
	if s == nil {
		return
	}
	ctx0, cancel0 := context.WithTimeout(s.ctx, activeViewerTrendingTickTimeout)
	s.warmActiveViewerTrending(ctx0)
	cancel0()

	ticker := time.NewTicker(activeViewerTrendingTicker)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(s.ctx, activeViewerTrendingTickTimeout)
			s.warmActiveViewerTrending(ctx)
			cancel()
		}
	}
}

func (s *Server) warmActiveViewerTrending(ctx context.Context) {
	s.tryRunMaintenanceWork(func() {
		s.warmActiveViewerTrendingBody(ctx)
	})
}

// warmActiveViewerTrendingBody iterates the active viewer snapshot and rebuilds
// trending_cache rows for any cohort that is missing or older than
// activeViewerTrendingMinFreshness(). It deliberately does not touch
// relays: the chronological feed the user is browsing already triggers
// refreshFeedForTrendingAsync as needed. This loop only re-ranks notes
// already present in the local store.
func (s *Server) warmActiveViewerTrendingBody(ctx context.Context) {
	if s == nil || s.store == nil || s.activeViewers == nil || s.resolvedAuthors == nil {
		return
	}
	defer s.observe("trending.active_viewer.warm_tick", time.Now())
	now := time.Now()
	viewers := s.activeViewers.Snapshot(now, activeViewerTrendingWindow)
	if len(viewers) == 0 {
		return
	}
	if len(viewers) > activeViewerTrendingMaxFanout {
		viewers = viewers[:activeViewerTrendingMaxFanout]
	}
	freshness := activeViewerTrendingMinFreshness(s.cfg.TrendingMinRecompute)
	timeframes := []string{trending24h, trending1w}
	for _, v := range viewers {
		s.metrics.Add("trending.active_viewer.warm_attempt", 1)
		if ctx.Err() != nil {
			return
		}
		// Skip viewers whose cohort is no longer in the resolvedAuthors
		// cache: re-running BFS in the background would defeat the whole
		// point of "use only what's already hot".
		cohort, ok := s.resolvedAuthors.get(resolvedAuthorsCacheKey(v.Viewer, v.WoT), now)
		if !ok || len(cohort) == 0 {
			s.metrics.Add("trending.active_viewer.warm_skip_no_resolution", 1)
			continue
		}
		cohortKey := authorsCacheKey(cohort)
		if cohortKey == "" {
			s.metrics.Add("trending.active_viewer.warm_skip_empty_cohort", 1)
			continue
		}
		for _, tf := range timeframes {
			if ctx.Err() != nil {
				return
			}
			s.warmActiveViewerTrendingCohort(ctx, tf, cohortKey, cohort, freshness, now)
		}
	}
}

// warmActiveViewerTrendingCohort recomputes one (timeframe, cohort) pair if
// the existing cache row is missing or stale beyond the freshness floor.
// Coordinated with refreshTrendingCacheAsync via the shared beginRefresh key
// so an on-demand request and the loop never recompute the same cohort
// concurrently.
func (s *Server) warmActiveViewerTrendingCohort(ctx context.Context, timeframe, cohortKey string, cohort []string, freshness time.Duration, now time.Time) {
	timeframe = normalizeTrendingTimeframe(timeframe)
	if _, computedAt, err := s.store.ReadTrendingCache(ctx, timeframe, cohortKey); err == nil && computedAt > 0 {
		if now.Sub(time.Unix(computedAt, 0)) < freshness {
			s.metrics.Add("trending.active_viewer.warm_skip_fresh", 1)
			return
		}
	}
	refreshKey := "trending:" + timeframe + ":" + cohortKey
	if !s.beginRefresh(refreshKey) {
		s.metrics.Add("trending.active_viewer.warm_skip_in_flight", 1)
		return
	}
	defer s.endRefresh(refreshKey)
	if _, err := s.computeAndStoreCohortTrending(ctx, timeframe, cohortKey, cohort, now); err != nil {
		s.metrics.Add("trending.active_viewer.warm_error", 1)
		return
	}
	s.metrics.Add("trending.active_viewer.warm_success", 1)
}

// activeViewerTrendingMinFreshness picks the staleness threshold below which
// we skip recomputing. We aim to refresh at half the configured min recompute
// so the cohort cache never returns rows the on-demand path would itself
// treat as "due for refresh" (ReadTrendingCache + minRecompute check in
// trendingItems). The 5-minute floor avoids hammering SQLite when the
// configured min recompute is set very low for tests or local debugging.
func activeViewerTrendingMinFreshness(min time.Duration) time.Duration {
	if min <= 0 {
		min = 20 * time.Minute
	}
	freshness := min / 2
	if freshness < 5*time.Minute {
		freshness = 5 * time.Minute
	}
	return freshness
}
