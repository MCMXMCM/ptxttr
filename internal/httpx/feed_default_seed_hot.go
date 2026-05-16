package httpx

import (
	"context"
	"log/slog"
	"maps"
	"strings"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

const (
	defaultSeedGuestFeedHotTicker      = 3 * time.Minute
	defaultSeedGuestPeriodicThrottle   = 90 * time.Second
	defaultSeedGuestFeedScope          = "default_seed_guest_feed"
	defaultSeedGuestPeriodicAttemptKey = "periodic_attempt"
	defaultSeedGuestSnapshotSuccessKey = "snapshot_success"
)

func (s *Server) canonicalDefaultLoggedOutRelays() []string {
	if s == nil {
		return nil
	}
	return nostrx.NormalizeRelayList(append([]string(nil), s.cfg.DefaultRelays...), nostrx.MaxRelays)
}

// canonicalDefaultLoggedOutGuestFeedRequest matches anonymous home with default
// relays, default Jack seed, WoT enabled at the default depth, recent sort, and
// the default trending window (24h).
func (s *Server) canonicalDefaultLoggedOutGuestFeedRequest() feedRequest {
	return feedRequest{
		Pubkey:     "",
		SeedPubkey: defaultLoggedOutWOTSeedNPub,
		Cursor:     0,
		CursorID:   "",
		Limit:      30,
		Relays:     s.canonicalDefaultLoggedOutRelays(),
		Timeframe:  "",
		SortMode:   feedSortRecent,
		WoT:        webOfTrustOptions{Enabled: true, Depth: defaultLoggedOutWOTDepth},
	}
}

// canonicalGuestFeedRequestForSort returns the same canonical shape as the
// default logged-out home for the given sort (recent / trend24h / trend7d).
func (s *Server) canonicalGuestFeedRequestForSort(sortMode string) feedRequest {
	req := s.canonicalDefaultLoggedOutGuestFeedRequest()
	req.SortMode = sortMode
	return req
}

func (s *Server) isCanonicalDefaultLoggedOutGuestFeedRequest(req feedRequest) bool {
	if s == nil || !deferGuestLoggedOutFeedFirstPage(req) {
		return false
	}
	if !req.WoT.Enabled {
		return false
	}
	if req.WoT.Depth != defaultLoggedOutWOTDepth {
		return false
	}
	if normalizeTrendingTimeframe(req.Timeframe) != trending24h {
		return false
	}
	if !isDefaultLoggedOutSeed(req.SeedPubkey) {
		return false
	}
	if normalizeFeedSort(req.SortMode) != feedSortRecent {
		return false
	}
	want := hashStringSlice(s.canonicalDefaultLoggedOutRelays())
	got := hashStringSlice(req.Relays)
	return want == got
}

func (s *Server) guestFeedFirstPageCacheKey(ctx context.Context, req feedRequest) (string, bool) {
	if s == nil {
		return "", false
	}
	resolved := s.resolveRequestAuthors(ctx, req.Pubkey, req.SeedPubkey, req.Relays, req.WoT)
	return s.guestFeedCacheKey(req, resolved, normalizeFeedSort(req.SortMode), normalizeTrendingTimeframe(req.Timeframe), false)
}

func defaultSeedGuestWarmLockKey(cacheKey string) string {
	if cacheKey == "" {
		return ""
	}
	return "guestWarm:" + cacheKey
}

func (s *Server) feedPageDataToDefaultSeedSnapshot(req feedRequest, data *FeedPageData) *store.DefaultSeedGuestFeedSnapshot {
	if data == nil || len(data.Feed) == 0 {
		return nil
	}
	prof := make(map[string]store.DefaultSeedProfileSnap, len(data.Profiles))
	for k, p := range data.Profiles {
		prof[k] = store.DefaultSeedProfileSnap{
			PubKey:  p.PubKey,
			Name:    p.Name,
			Display: p.Display,
			About:   p.About,
			Picture: p.Picture,
			Website: p.Website,
			NIP05:   p.NIP05,
		}
	}
	snap := &store.DefaultSeedGuestFeedSnapshot{
		RelaysHash:     hashStringSlice(req.Relays),
		Feed:           append([]nostrx.Event(nil), data.Feed...),
		Profiles:       prof,
		Cursor:         data.Cursor,
		CursorID:       data.CursorID,
		HasMore:        data.HasMore,
		ComputedAtUnix: time.Now().Unix(),
	}
	if data.ReferencedEvents != nil {
		snap.ReferencedEvents = maps.Clone(data.ReferencedEvents)
	}
	if data.ReplyCounts != nil {
		snap.ReplyCounts = maps.Clone(data.ReplyCounts)
	}
	if data.ReactionTotals != nil {
		snap.ReactionTotals = maps.Clone(data.ReactionTotals)
	}
	if data.ReactionViewers != nil {
		snap.ReactionViewers = maps.Clone(data.ReactionViewers)
	}
	return snap
}

func (s *Server) persistDefaultSeedGuestFeedSnapshot(ctx context.Context, req feedRequest, data *FeedPageData) error {
	if s == nil || s.store == nil || data == nil || len(data.Feed) == 0 {
		return nil
	}
	if !s.isCanonicalDefaultLoggedOutGuestFeedRequest(req) {
		return nil
	}
	snap := s.feedPageDataToDefaultSeedSnapshot(req, data)
	if err := s.store.SetDefaultSeedGuestFeedSnapshot(ctx, snap); err != nil {
		slog.Warn("persist default seed guest feed snapshot failed", "err", err)
		return err
	}
	s.store.MarkRefreshed(ctx, defaultSeedGuestFeedScope, defaultSeedGuestSnapshotSuccessKey)
	return nil
}

// mergePersistedDefaultSeedGuestFeedIntoShell fills feed fields from the durable
// snapshot when the in-memory guest cache missed and the request matches the
// canonical default-seed shape. We intentionally serve the last known good
// snapshot immediately even if it is stale; callers keep refresh async.
func (s *Server) mergePersistedDefaultSeedGuestFeedIntoShell(ctx context.Context, data *FeedPageData, req feedRequest) bool {
	if s == nil || s.store == nil || data == nil {
		return false
	}
	if len(data.Feed) > 0 || !deferGuestLoggedOutFeedFirstPage(req) {
		return false
	}
	if !s.isCanonicalDefaultLoggedOutGuestFeedRequest(req) {
		return false
	}
	snap, ok, err := s.store.GetDefaultSeedGuestFeedSnapshot(ctx)
	if err != nil || !ok || snap == nil || len(snap.Feed) == 0 {
		return false
	}
	if snap.RelaysHash != hashStringSlice(req.Relays) {
		return false
	}
	data.Feed = snap.Feed
	data.ReferencedEvents = snap.ReferencedEvents
	if data.ReferencedEvents == nil {
		data.ReferencedEvents = map[string]nostrx.Event{}
	}
	data.ReplyCounts = snap.ReplyCounts
	if data.ReplyCounts == nil {
		data.ReplyCounts = map[string]int{}
	}
	data.ReactionTotals = snap.ReactionTotals
	if data.ReactionTotals == nil {
		data.ReactionTotals = map[string]int{}
	}
	data.ReactionViewers = snap.ReactionViewers
	if data.ReactionViewers == nil {
		data.ReactionViewers = map[string]string{}
	}
	data.Cursor = snap.Cursor
	data.CursorID = snap.CursorID
	data.HasMore = snap.HasMore
	if data.Profiles == nil {
		data.Profiles = make(map[string]nostrx.Profile)
	}
	for pk, row := range snap.Profiles {
		if _, exists := data.Profiles[pk]; exists {
			continue
		}
		data.Profiles[pk] = nostrx.Profile{
			PubKey:  row.PubKey,
			Name:    row.Name,
			Display: row.Display,
			About:   row.About,
			Picture: row.Picture,
			Website: row.Website,
			NIP05:   row.NIP05,
		}
	}
	// homeOrFeedDocumentData already schedules scheduleGuestFeedFragmentWarm(req).
	return true
}

// tryWarmDeferredGuestFeedFragmentIfCold runs the fragment warm path used by
// scheduleGuestFeedFragmentWarm and the post-bootstrap one-shot.
func (s *Server) tryWarmDeferredGuestFeedFragmentIfCold(ctx context.Context, req feedRequest) {
	if s == nil || !deferGuestLoggedOutFeedFirstPage(req) {
		return
	}
	cacheKey, ok := s.guestFeedFirstPageCacheKey(ctx, req)
	if !ok || cacheKey == "" {
		return
	}
	if _, hit := s.guestFeedCache.get(cacheKey, time.Now()); hit {
		return
	}
	warmKey := defaultSeedGuestWarmLockKey(cacheKey)
	if !s.beginRefresh(warmKey) {
		return
	}
	defer s.endRefresh(warmKey)
	// Re-check after beginRefresh: another goroutine may have filled the cache.
	if _, hit := s.guestFeedCache.get(cacheKey, time.Now()); hit {
		return
	}
	data := s.feedPageDataEx(ctx, req, false, feedPageDataOptions{lightStatsOnly: true})
	if s.isCanonicalDefaultLoggedOutGuestFeedRequest(req) && len(data.Feed) > 0 {
		_ = s.persistDefaultSeedGuestFeedSnapshot(ctx, req, &data)
	}
	s.warmGuestWoTCohortTrending(ctx, req)
}

// warmGuestWoTCohortTrending precomputes per-seed WoT trending_cache rows for
// logged-out guests so the sidebar and trend sorts are not empty on cold start.
func (s *Server) warmGuestWoTCohortTrending(ctx context.Context, req feedRequest) {
	if s == nil || !req.WoT.Enabled {
		return
	}
	seed := strings.TrimSpace(req.SeedPubkey)
	if seed == "" {
		return
	}
	resolved := s.resolveRequestAuthors(ctx, req.Pubkey, req.SeedPubkey, req.Relays, req.WoT)
	if !resolved.wotEnabled || len(resolved.allAuthors) == 0 {
		return
	}
	cohortKey := authorsCacheKey(resolved.allAuthors)
	for _, tf := range []string{trending24h, trending1w} {
		items, _, err := s.store.ReadTrendingCache(ctx, tf, cohortKey)
		if err == nil && len(items) > 0 {
			continue
		}
		s.refreshTrendingCacheAsync(tf, cohortKey, resolved.allAuthors)
	}
}

// tryPeriodicCanonicalDefaultSeedGuestFeed rebuilds the canonical default-seed
// first pages (recent + ranked sorts) on a timer so guest TTL cache, SQLite
// recent snapshot, and durable trend snapshots stay fresh.
func (s *Server) tryPeriodicCanonicalDefaultSeedGuestFeed(ctx context.Context) {
	if s == nil {
		return
	}
	s.tryRunMaintenanceWork(func() {
		if s.store != nil && !s.store.ShouldRefresh(ctx, defaultSeedGuestFeedScope, defaultSeedGuestPeriodicAttemptKey, defaultSeedGuestPeriodicThrottle) {
			return
		}
		for _, sort := range []string{feedSortRecent, feedSortTrend24h, feedSortTrend7d} {
			req := s.canonicalGuestFeedRequestForSort(sort)
			cacheKey, ok := s.guestFeedFirstPageCacheKey(ctx, req)
			if !ok || cacheKey == "" {
				continue
			}
			lockKey := defaultSeedGuestWarmLockKey(cacheKey)
			if !s.beginRefresh(lockKey) {
				continue
			}
			data := s.feedPageDataEx(ctx, req, false, feedPageDataOptions{lightStatsOnly: true})
			if len(data.Feed) > 0 {
				_ = s.persistDefaultSeedGuestFeedSnapshot(ctx, req, &data)
				resolved := s.resolveRequestAuthors(ctx, req.Pubkey, req.SeedPubkey, req.Relays, req.WoT)
				s.maybePersistFeedSnapshots(ctx, req, resolved, &data)
			}
			s.endRefresh(lockKey)
		}
		if s.store != nil {
			s.store.MarkRefreshed(ctx, defaultSeedGuestFeedScope, defaultSeedGuestPeriodicAttemptKey)
		}
	})
}

func (s *Server) runDefaultSeedGuestFeedHotLoop() {
	if s == nil {
		return
	}
	if len(s.cfg.DefaultRelays) == 0 && len(s.cfg.MetadataRelays) == 0 {
		return
	}
	ctx0, cancel0 := context.WithTimeout(s.ctx, 45*time.Second)
	s.tryPeriodicCanonicalDefaultSeedGuestFeed(ctx0)
	cancel0()

	ticker := time.NewTicker(defaultSeedGuestFeedHotTicker)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(s.ctx, 45*time.Second)
			s.tryPeriodicCanonicalDefaultSeedGuestFeed(ctx)
			cancel()
		}
	}
}

// scheduleCanonicalDefaultSeedGuestFeedWarmOneShot enqueues a background warm
// for the canonical default-seed first page (used after successful seed bootstrap).
func (s *Server) scheduleCanonicalDefaultSeedGuestFeedWarmOneShot() {
	if s == nil {
		return
	}
	s.runBackground(func() {
		ctx, cancel := context.WithTimeout(s.ctx, 45*time.Second)
		defer cancel()
		for _, sort := range []string{feedSortRecent, feedSortTrend24h, feedSortTrend7d} {
			req := s.canonicalGuestFeedRequestForSort(sort)
			s.tryWarmDeferredGuestFeedFragmentIfCold(ctx, req)
		}
	})
}

// resetGuestFeedCacheForTest clears the in-memory guest feed TTL cache (httpx tests only).
func (s *Server) resetGuestFeedCacheForTest() {
	if s != nil && s.guestFeedCache != nil {
		s.guestFeedCache.reset()
	}
}
