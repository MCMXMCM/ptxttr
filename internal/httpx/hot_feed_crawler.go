package httpx

import (
	"context"
	"time"

	"ptxt-nstr/internal/config"
)

const (
	hotFeedCrawlerTickTimeout = 90 * time.Second
)

type hotFeedCohort struct {
	name     string
	req      feedRequest
	resolved requestAuthors
	authors  []string
	key      string
}

func (s *Server) runHotFeedCrawler() {
	if s == nil || s.store == nil || s.nostr == nil || !s.cfg.HotFeedCrawlerEnabled {
		return
	}
	if len(s.cfg.DefaultRelays) == 0 && len(s.cfg.MetadataRelays) == 0 {
		return
	}
	ctx0, cancel0 := context.WithTimeout(s.ctx, hotFeedCrawlerTickTimeout)
	s.warmHotFeedTick(ctx0)
	cancel0()

	interval := s.cfg.HotFeedCrawlerInterval
	if interval <= 0 {
		interval = config.DefaultHotFeedCrawlerInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(s.ctx, hotFeedCrawlerTickTimeout)
			s.warmHotFeedTick(ctx)
			cancel()
		}
	}
}

func (s *Server) warmHotFeedTick(ctx context.Context) {
	s.tryRunMaintenanceWork(maintenanceLaneSeed, func() {
		s.warmHotFeedTickBody(ctx)
	})
}

func (s *Server) warmHotFeedTickBody(ctx context.Context) {
	if s == nil || s.store == nil {
		return
	}
	defer s.observe("crawler.hot_feed.tick", time.Now())
	cohorts := s.hotFeedCohorts(ctx, time.Now())
	if len(cohorts) == 0 {
		s.metrics.Add("crawler.hot_feed.skipped", 1)
		return
	}
	authorLimit := hotFeedAuthorLimit(s.cfg.HotFeedCrawlerAuthorLimit)
	fetchLimit := hotFeedFetchLimit(s.cfg.HotFeedCrawlerFetchLimit)
	var since int64
	if lookback := hotFeedLookback(s.cfg.HotFeedCrawlerLookback); lookback > 0 {
		since = time.Now().Add(-lookback).Unix()
	}
	for _, cohort := range cohorts {
		if ctx.Err() != nil {
			return
		}
		selected := rotateHotFeedAuthors(cohort.authors, authorLimit, s.hotFeedCrawlCursor.Add(1))
		if len(selected) == 0 {
			s.metrics.Add("crawler.hot_feed.skip_empty_authors", 1)
			continue
		}
		viewer := cohort.resolved.viewerForMuteFilter()
		relays := s.filterCrawlerRelays(s.outboxSeedRelays(ctx, viewer, selected, cohort.req.Relays))
		if len(relays) == 0 {
			relays = s.crawlRelays(cohort.req.Relays)
		}
		fetched := s.refreshRecent(ctx, viewer, selected, 0, fetchLimit, relays, since)
		if fetched < 0 {
			s.metrics.Add("crawler.hot_feed.refresh_error", 1)
			continue
		}
		s.metrics.Add("crawler.hot_feed.cohort_success", 1)
		s.metrics.Add("crawler.hot_feed.refresh_events", int64(fetched))
		s.refreshHotFeedSnapshots(ctx, cohort)
	}
}

func (s *Server) hotFeedCohorts(ctx context.Context, now time.Time) []hotFeedCohort {
	if s == nil {
		return nil
	}
	limit := s.cfg.HotFeedCrawlerCohortLimit
	if limit <= 0 {
		limit = config.DefaultHotFeedCrawlerCohortLimit
	}
	cohorts := make([]hotFeedCohort, 0, limit)
	defaultReq := s.canonicalDefaultLoggedOutGuestFeedRequest()
	if cohort, ok := s.hotFeedCohortFromRequest(ctx, "default_seed", defaultReq); ok {
		cohorts = append(cohorts, cohort)
	}
	if len(cohorts) >= limit || s.activeViewers == nil {
		return cohorts
	}
	seen := make(map[string]struct{}, len(cohorts)+limit)
	seedViewers := make(map[string]struct{}, len(cohorts))
	for _, cohort := range cohorts {
		seen[cohort.key] = struct{}{}
		if cohort.resolved.loggedOut && cohort.resolved.wotViewerPubkey != "" {
			seedViewers[cohort.resolved.wotViewerPubkey] = struct{}{}
		}
	}
	for _, viewer := range s.activeViewers.Snapshot(now, activeViewerTrendingWindow) {
		if len(cohorts) >= limit {
			break
		}
		if _, isSeedViewer := seedViewers[viewer.Viewer]; isSeedViewer {
			continue
		}
		cohort, ok := s.hotFeedCohortFromCachedViewer(now, viewer)
		if !ok {
			continue
		}
		if _, exists := seen[cohort.key]; exists {
			continue
		}
		seen[cohort.key] = struct{}{}
		cohorts = append(cohorts, cohort)
	}
	return cohorts
}

func (s *Server) hotFeedCohortFromCachedViewer(now time.Time, viewer activeViewerEntry) (hotFeedCohort, bool) {
	if s == nil || s.resolvedAuthors == nil || viewer.Viewer == "" {
		return hotFeedCohort{}, false
	}
	authors, ok := s.resolvedAuthors.get(resolvedAuthorsCacheKey(viewer.Viewer, viewer.WoT), now)
	if !ok || len(authors) == 0 {
		s.metrics.Add("crawler.hot_feed.skip_no_resolution", 1)
		return hotFeedCohort{}, false
	}
	req := feedRequest{
		Pubkey:   viewer.Viewer,
		Limit:    30,
		Relays:   s.canonicalDefaultLoggedOutRelays(),
		SortMode: feedSortRecent,
		WoT:      viewer.WoT,
	}
	resolved := requestAuthors{
		allAuthors:      append([]string(nil), authors...),
		authors:         clampAuthorsWithLimit(authors, s.resolvedAuthorLimit(viewer.WoT)),
		userPubkey:      viewer.Viewer,
		wotViewerPubkey: viewer.Viewer,
		loggedOut:       false,
		wotEnabled:      viewer.WoT.Enabled,
	}
	cohort := hotFeedCohort{
		name:     "viewer",
		req:      req,
		resolved: resolved,
		authors:  append([]string(nil), authors...),
		key:      hotFeedCohortKey(resolved, req),
	}
	if cohort.key == "" {
		s.metrics.Add("crawler.hot_feed.skip_empty_cohort", 1)
		return hotFeedCohort{}, false
	}
	return cohort, true
}

func (s *Server) hotFeedCohortFromRequest(ctx context.Context, name string, req feedRequest) (hotFeedCohort, bool) {
	if req.Limit <= 0 {
		req.Limit = 30
	}
	if len(req.Relays) == 0 {
		req.Relays = s.canonicalDefaultLoggedOutRelays()
	}
	req.SortMode = normalizeFeedSort(req.SortMode)
	req.Timeframe = normalizeTrendingTimeframe(req.Timeframe)
	resolved := s.resolveRequestAuthors(ctx, req.Pubkey, req.SeedPubkey, req.Relays, req.WoT)
	authors := resolved.allAuthors
	if len(authors) == 0 {
		authors = resolved.authors
	}
	key := hotFeedCohortKey(resolved, req)
	if key == "" || len(authors) == 0 {
		return hotFeedCohort{}, false
	}
	return hotFeedCohort{
		name:     name,
		req:      req,
		resolved: resolved,
		authors:  authors,
		key:      key,
	}, true
}

func hotFeedCohortKey(resolved requestAuthors, req feedRequest) string {
	if resolved.loggedOut && resolved.wotEnabled {
		return "guest:" + authorsCacheKey(resolved.allAuthors)
	}
	if resolved.userPubkey != "" {
		return "viewer:" + resolvedAuthorsCacheKey(resolved.userPubkey, req.WoT)
	}
	return authorsCacheKey(resolved.authors)
}

func (s *Server) refreshHotFeedSnapshots(ctx context.Context, cohort hotFeedCohort) {
	if s == nil || s.store == nil || cohort.key == "" {
		return
	}
	throttle := s.cfg.HotFeedCrawlerSnapshotThrottle
	if throttle <= 0 {
		throttle = config.DefaultHotFeedCrawlerSnapshotThrottle
	}
	refreshKey := "snapshot:" + cohort.key
	if !s.store.ShouldRefresh(ctx, "hot_feed", refreshKey, throttle) {
		s.metrics.Add("crawler.hot_feed.snapshot_skip_fresh", 1)
		return
	}
	now := time.Now()
	cohortKey := authorsCacheKey(cohort.authors)
	if cohortKey != "" {
		freshness := activeViewerTrendingMinFreshness(s.cfg.TrendingMinRecompute)
		for _, tf := range []string{trending24h, trending1w} {
			if ctx.Err() != nil {
				return
			}
			s.warmCohortTrendingIfStale(ctx, tf, cohortKey, cohort.authors, freshness, now, "crawler.hot_feed.trending")
		}
	}
	for _, sort := range []string{feedSortRecent, feedSortTrend24h, feedSortTrend7d} {
		if ctx.Err() != nil {
			return
		}
		req := cohort.req
		req.Cursor = 0
		req.CursorID = ""
		req.Limit = 30
		req.SortMode = sort
		data := s.feedPageDataExResolved(ctx, req, false, feedPageDataOptions{
			lightStatsOnly:         true,
			guestCacheReadDisabled: true,
		}, cohort.resolved)
		if len(data.Feed) == 0 {
			continue
		}
		if s.isCanonicalDefaultLoggedOutGuestFeedRequest(req) {
			_ = s.persistDefaultSeedGuestFeedSnapshot(ctx, req, &data)
		}
		s.metrics.Add("crawler.hot_feed.snapshot_rebuild", 1)
	}
	s.store.MarkRefreshed(ctx, "hot_feed", refreshKey)
}

func rotateHotFeedAuthors(authors []string, limit int, cursor uint64) []string {
	if len(authors) == 0 {
		return nil
	}
	limit = hotFeedAuthorLimit(limit)
	if len(authors) <= limit {
		return append([]string(nil), authors...)
	}
	start := int((cursor * uint64(limit)) % uint64(len(authors)))
	out := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, authors[(start+i)%len(authors)])
	}
	return out
}

func hotFeedAuthorLimit(limit int) int {
	if limit <= 0 {
		return config.DefaultHotFeedCrawlerAuthorLimit
	}
	return limit
}

func hotFeedFetchLimit(limit int) int {
	if limit <= 0 {
		return config.DefaultHotFeedCrawlerFetchLimit
	}
	return limit
}

func hotFeedLookback(lookback time.Duration) time.Duration {
	if lookback < 0 {
		return 0
	}
	if lookback == 0 {
		return config.DefaultHotFeedCrawlerLookback
	}
	return lookback
}
