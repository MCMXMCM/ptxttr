package httpx

import (
	"context"
	"strings"
	"time"

	"ptxt-nstr/internal/nostrx"
)

// deferGuestLoggedOutFeedFirstPage is true for logged-out first pages using
// the default WoT seed (or WoT off) for recent, trend24h, and trend7d so SSR
// and fragments can skip expensive work until durable snapshots or async warm.
func deferGuestLoggedOutFeedFirstPage(req feedRequest) bool {
	pub := strings.TrimSpace(req.Pubkey)
	if pub != "" {
		if _, err := nostrx.DecodeIdentifier(pub); err == nil {
			return false
		}
	}
	switch normalizeFeedSort(req.SortMode) {
	case feedSortRecent, feedSortTrend24h, feedSortTrend7d:
	default:
		return false
	}
	if req.Cursor != 0 || req.CursorID != "" {
		return false
	}
	if req.WoT.Enabled && !isDefaultLoggedOutSeed(req.SeedPubkey) {
		return false
	}
	return true
}

// homeFeedShellPageData returns feed chrome for SSR: heading fields, cached trending,
// and profiles from trending. The feed body is filled only from the in-memory guest
// TTL cache when WoT is off (firehose path); WoT-on canonical default-seed shells
// intentionally omit persisted SQLite snapshot notes here so SSR never blocks on large
// snapshot JSON—notes load via ?fragment=1 / async warmers. For WoT-enabled guests we
// avoid cohort resolution on this path; first paint stays bounded.
func (s *Server) homeFeedShellPageData(ctx context.Context, req feedRequest) FeedPageData {
	data := s.feedHeadingData(req)
	tf := normalizeTrendingTimeframe(req.Timeframe)
	trending := s.trendingData(ctx, tf, req.Relays, true)
	profEvents := make([]nostrx.Event, 0, len(trending))
	for _, item := range trending {
		profEvents = append(profEvents, item.Event)
	}
	data.Trending = trending
	data.TrendingTimeframe = tf
	data.Profiles = s.profilesFor(ctx, profEvents)
	data.Feed = nil
	data.ReferencedEvents = map[string]nostrx.Event{}
	data.ReplyCounts = map[string]int{}
	data.ReactionTotals = map[string]int{}
	data.ReactionViewers = map[string]string{}
	data.HasMore = false
	data.Cursor = 0
	data.CursorID = ""

	// For logged-out firehose (WoT off) the guest TTL cache is cheap to query
	// because it does not require cohort resolution. For WoT-enabled guest flows
	// we skip guestFeedCache here and rely on durable snapshots so the shell
	// never blocks on resolveRequestAuthors.
	if s != nil && s.guestFeedCache != nil && deferGuestLoggedOutFeedFirstPage(req) && !req.WoT.Enabled {
		resolved := s.resolveRequestAuthors(ctx, req.Pubkey, req.SeedPubkey, req.Relays, req.WoT)
		sortMode := normalizeFeedSort(req.SortMode)
		key, ok := s.guestFeedCacheKey(req, resolved, sortMode, tf, false)
		if ok && key != "" {
			if cached, hit := s.guestFeedCache.get(key, time.Now()); hit && len(cached.Feed) > 0 {
				data.Feed = cached.Feed
				data.ReferencedEvents = cached.ReferencedEvents
				data.ReplyCounts = cached.ReplyCounts
				data.ReactionTotals = cached.ReactionTotals
				data.ReactionViewers = cached.ReactionViewers
				data.Cursor = cached.Cursor
				data.CursorID = cached.CursorID
				data.HasMore = cached.HasMore
				if data.Profiles == nil {
					data.Profiles = make(map[string]nostrx.Profile)
				}
				for pk, prof := range cached.Profiles {
					if _, exists := data.Profiles[pk]; !exists {
						data.Profiles[pk] = prof
					}
				}
			}
		}
	}
	// mergePersistedDefaultSeedGuestFeedIntoShell only applies to the canonical
	// default-seed shape; skip calling it here so the SSR shell never blocks on
	// snapshot JSON (notes still load via scheduleGuestFeedFragmentWarm / ?fragment=1).
	if len(data.Feed) == 0 && s.isCanonicalDefaultLoggedOutGuestFeedRequest(req) {
		// Expected when deferring snapshot notes to fragments, not an error signal for dashboards.
		s.metrics.Add("feed.guest_shell_snapshot_skip_canonical", 1)
	}
	if len(data.Feed) == 0 && normalizeFeedSort(req.SortMode) != feedSortRecent {
		s.mergeGuestCanonicalTrendSnapshotIntoShell(ctx, &data, req)
	}
	return data
}

// mergeGuestCanonicalTrendSnapshotIntoShell fills the feed from durable
// canonical guest snapshots for trend24h/trend7d (see feed_default_seed_hot).
func (s *Server) mergeGuestCanonicalTrendSnapshotIntoShell(ctx context.Context, data *FeedPageData, req feedRequest) {
	if s == nil || s.store == nil || data == nil || len(data.Feed) > 0 {
		return
	}
	if !s.isGuestCanonicalSnapshotTarget(req) {
		return
	}
	sm := normalizeFeedSort(req.SortMode)
	if sm != feedSortTrend24h && sm != feedSortTrend7d {
		return
	}
	key := guestCanonicalFeedSnapshotKey(sm, req.Relays)
	rec, ok, err := s.store.GetFeedSnapshot(ctx, key)
	if err != nil || !ok || rec == nil || len(rec.Feed) == 0 {
		return
	}
	if rec.RelaysHash != "" && rec.RelaysHash != hashStringSlice(req.Relays) {
		return
	}
	mergeFeedSnapshotRecordIntoFeedPageData(data, rec, false)
	data.FeedSort = sm
}

// scheduleGuestFeedFragmentWarm precomputes the logged-out first feed fragment
// in the background so the browser's ?fragment=1 request often hits the guest
// TTL cache. Deduplicated per cache key via beginRefresh.
func (s *Server) scheduleGuestFeedFragmentWarm(req feedRequest) {
	if s == nil || !deferGuestLoggedOutFeedFirstPage(req) {
		return
	}
	s.runBackgroundUserAsync(func() {
		ctx, cancel := context.WithTimeout(s.ctx, 45*time.Second)
		defer cancel()
		s.tryWarmDeferredGuestFeedFragmentIfCold(ctx, req)
	})
}
