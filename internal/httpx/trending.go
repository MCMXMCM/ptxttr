package httpx

import (
	"context"
	"time"
)

func (s *Server) runTrendingSweeper() {
	interval := s.cfg.TrendingSweepInterval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	s.runSweeper(interval, 10*time.Second, s.recomputeTrending)
}

func (s *Server) recomputeTrending(ctx context.Context) {
	s.tryRunMaintenanceWork(maintenanceLaneTrending, func() {
		s.runWithRelayWriteBudget(ctx, "trending.recompute", func() {
			defer s.observe("trending.recompute", time.Now())
			now := time.Now()
			minRecompute := s.cfg.TrendingMinRecompute
			if minRecompute <= 0 || minRecompute > 10*time.Minute {
				minRecompute = 10 * time.Minute
			}
			frames := []string{trending24h, trending1w}
			for _, timeframe := range frames {
				_, computedAt, cacheErr := s.store.ReadTrendingCache(ctx, timeframe, "")
				if cacheErr == nil && computedAt > 0 && now.Unix()-computedAt < int64(minRecompute.Seconds()) {
					continue
				}
				_, _ = s.computeAndStoreTrending(ctx, timeframe, now)
			}
			req := s.canonicalDefaultLoggedOutGuestFeedRequest()
			if cohort, ok := s.hotFeedCohortFromRequest(ctx, "default_seed", req); ok {
				cohortKey := authorsCacheKey(cohort.authors)
				for _, timeframe := range frames {
					if ctx.Err() != nil {
						return
					}
					s.warmCohortTrendingIfStale(ctx, timeframe, cohortKey, cohort.authors, minRecompute, now, "trending.default_seed.warm")
				}
			}
		})
	})
}
