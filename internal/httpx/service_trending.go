package httpx

import (
	"context"
	"math"
	"sort"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

const (
	trendingDegradedMinItems  = 10
	trendingHydrationTopLimit = 48
	trendingHydrationTail     = 16
)

func trendingNoteIsReply(event nostrx.Event) bool {
	for _, tag := range event.Tags {
		if len(tag) >= 4 && tag[0] == "e" && tag[3] == "reply" {
			return true
		}
	}
	return false
}

func normalizeTrendingTimeframe(value string) string {
	if value == trending1w {
		return trending1w
	}
	return trending24h
}

func trendingSince(timeframe string, now time.Time) int64 {
	if normalizeTrendingTimeframe(timeframe) == trending1w {
		return now.Add(-7 * 24 * time.Hour).Unix()
	}
	return now.Add(-24 * time.Hour).Unix()
}

func (s *Server) trendingData(ctx context.Context, timeframe string, cohortKey string, authors []string, _ []string, cacheOnly bool) []TrendingNote {
	defer s.observe("trending.data", time.Now())
	timeframe = normalizeTrendingTimeframe(timeframe)
	items := s.trendingItems(ctx, timeframe, cohortKey, authors, cacheOnly)
	if len(items) == 0 {
		return []TrendingNote{}
	}
	if len(items) > trendingLimit {
		items = items[:trendingLimit]
	}
	counts := make(map[string]int, len(items))
	for _, item := range items {
		if item.NoteID != "" {
			counts[item.NoteID] = item.ReplyCount
		}
	}
	events := s.eventsByIDFromStore(ctx, noteIDsFromTrendingItems(items))
	trending := make([]TrendingNote, 0, len(items))
	for _, item := range items {
		event := events[item.NoteID]
		if event == nil {
			continue
		}
		trending = append(trending, TrendingNote{
			Event:      *event,
			ReplyCount: counts[item.NoteID],
		})
	}
	return trending
}

func (s *Server) trendingItems(ctx context.Context, timeframe string, cohortKey string, authors []string, cacheOnly bool) []store.TrendingItem {
	timeframe = normalizeTrendingTimeframe(timeframe)
	now := time.Now()
	minRecompute := s.cfg.TrendingMinRecompute
	if minRecompute <= 0 || minRecompute > 10*time.Minute {
		minRecompute = 10 * time.Minute
	}
	items, computedAt, err := s.store.ReadTrendingCache(ctx, timeframe, cohortKey)
	if err != nil {
		s.metrics.Add("trending.cache_read_error", 1)
	} else if len(items) > 0 {
		if computedAt > 0 && now.Unix()-computedAt >= int64(minRecompute.Seconds()) {
			s.refreshTrendingCacheAsync(timeframe, cohortKey, authors)
		}
		return items
	}
	s.refreshTrendingCacheAsync(timeframe, cohortKey, authors)
	if cohortKey != "" {
		if global, _, gerr := s.store.ReadTrendingCache(ctx, timeframe, ""); gerr == nil && len(global) > 0 {
			s.metrics.Add("trending.sidebar_global_stale_fallback", 1)
			return s.filterTrendingItemsToAuthors(ctx, global, authors)
		}
		if cohortKey != "" {
			s.metrics.Add("trending_cache_empty_by_cohort", 1)
		}
	}
	if !cacheOnly {
		s.metrics.Add("trending.cache_miss.foreground_empty", 1)
	}
	return []store.TrendingItem{}
}

func (s *Server) filterTrendingItemsToAuthors(ctx context.Context, items []store.TrendingItem, authors []string) []store.TrendingItem {
	if len(items) == 0 || len(authors) == 0 {
		return items
	}
	membership := newAuthorMembership(authors)
	events := s.eventsByIDFromStore(ctx, noteIDsFromTrendingItems(items))
	out := items[:0]
	for _, item := range items {
		ev := events[item.NoteID]
		if ev == nil || !membership.Contains(ev.PubKey) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func (s *Server) computeAndStoreTrending(ctx context.Context, timeframe string, now time.Time) ([]store.TrendingItem, error) {
	return s.computeAndStoreCohortTrending(ctx, timeframe, "", nil, now)
}

func (s *Server) computeAndStoreCohortTrending(ctx context.Context, timeframe string, cohortKey string, authors []string, now time.Time) ([]store.TrendingItem, error) {
	items, err := s.buildTrendingItemsFromRecent(ctx, timeframe, authors, now)
	if err != nil {
		s.metrics.Add("trending.recompute_error", 1)
		return nil, err
	}
	if len(items) < trendingDegradedMinItems {
		if existing, _, readErr := s.store.ReadTrendingCache(ctx, timeframe, cohortKey); readErr == nil && len(existing) >= trendingDegradedMinItems {
			s.metrics.Add("trending.recompute_preserve_warm_cache", 1)
			s.hydrateTrendingCandidates(existing)
			return existing, nil
		}
	}
	if err := s.store.WriteTrendingCache(ctx, timeframe, cohortKey, items, now.Unix()); err != nil {
		s.metrics.Add("trending.recompute_error", 1)
	}
	s.metrics.Add("trending.recomputed", 1)
	s.hydrateTrendingCandidates(items)
	return items, nil
}

func (s *Server) buildTrendingItemsFromRecent(ctx context.Context, timeframe string, authors []string, now time.Time) ([]store.TrendingItem, error) {
	const candidateLimit = trendingCacheLimit * 8
	timeframe = normalizeTrendingTimeframe(timeframe)
	since := trendingSince(timeframe, now)
	candidates, err := s.store.TrendingCandidatesByKinds(ctx, noteTimelineKinds, since, authors, candidateLimit)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		s.metrics.Add("trending.candidates.empty", 1)
		return []store.TrendingItem{}, nil
	}
	s.metrics.Add("trending.candidates.selected", int64(len(candidates)))
	for idx := range candidates {
		candidates[idx].Score = trendingEngagementScore(candidates[idx].ReplyCount, candidates[idx].ReactionCount)
		candidates[idx].HotScore = trendingHotScore(candidates[idx].Score, candidates[idx].NoteCreatedAt, timeframe, now)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return trendingItemLess(candidates[i], candidates[j])
	})
	items := make([]store.TrendingItem, 0, min(len(candidates), trendingCacheLimit))
	for _, candidate := range candidates {
		items = append(items, candidate)
		if len(items) >= trendingCacheLimit {
			break
		}
	}
	s.metrics.Add("trending.items.computed", int64(len(items)))
	return items, nil
}

func trendingHalfLife(timeframe string) time.Duration {
	if normalizeTrendingTimeframe(timeframe) == trending1w {
		return 36 * time.Hour
	}
	return 6 * time.Hour
}

func trendingHotScore(engagement int, createdAt int64, timeframe string, now time.Time) float64 {
	if engagement <= 0 || createdAt <= 0 {
		return 0
	}
	age := now.Sub(time.Unix(createdAt, 0))
	if age < 0 {
		age = 0
	}
	halfLife := trendingHalfLife(timeframe).Seconds()
	if halfLife <= 0 {
		return float64(engagement)
	}
	return float64(engagement) * math.Pow(0.5, age.Seconds()/halfLife)
}

func trendingItemLess(left, right store.TrendingItem) bool {
	if left.HotScore != right.HotScore {
		return left.HotScore > right.HotScore
	}
	leftEngagement := left.ReplyCount + left.ReactionCount
	rightEngagement := right.ReplyCount + right.ReactionCount
	if leftEngagement != rightEngagement {
		return leftEngagement > rightEngagement
	}
	if left.ReplyCount != right.ReplyCount {
		return left.ReplyCount > right.ReplyCount
	}
	if left.ReactionCount != right.ReactionCount {
		return left.ReactionCount > right.ReactionCount
	}
	if left.NoteCreatedAt != right.NoteCreatedAt {
		return left.NoteCreatedAt > right.NoteCreatedAt
	}
	return left.NoteID > right.NoteID
}

func (s *Server) hydrateTrendingCandidates(items []store.TrendingItem) {
	if s == nil || len(items) == 0 || !s.allowLegacyWarmers() {
		return
	}
	limit := min(len(items), trendingHydrationTopLimit)
	ids := make([]string, 0, limit+trendingHydrationTail)
	seen := make(map[string]struct{}, limit+trendingHydrationTail)
	add := func(id string) {
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for i := 0; i < limit; i++ {
		add(items[i].NoteID)
	}
	for i := len(items) - 1; i >= 0 && len(ids) < limit+trendingHydrationTail; i-- {
		add(items[i].NoteID)
	}
	if len(ids) == 0 {
		return
	}
	s.metrics.Add("trending.hydration_candidates", int64(len(ids)))
	relays := s.crawlRelays(nil)
	s.touchHydrationTargets(s.ctx, noteReplyWarmTargets(ids))
	s.touchHydrationTargets(s.ctx, noteReactionWarmTargets(ids))
	s.enqueueWarmNotesForViewer("", "noteReplies", ids, relays)
	s.enqueueWarmNotesForViewer("", "noteReactions", ids, relays)
}

func (s *Server) refreshTrendingCacheAsync(timeframe string, cohortKey string, authors []string) {
	refreshKey := "trending:" + normalizeTrendingTimeframe(timeframe) + ":" + cohortKey
	if !s.beginRefresh(refreshKey) {
		return
	}
	cohortAuthors := append([]string(nil), authors...)
	s.runBackgroundUserAsync(func() {
		defer s.endRefresh(refreshKey)
		timeout := requestTimeout(s.cfg.RequestTimeout)
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		_, _ = s.computeAndStoreCohortTrending(ctx, timeframe, cohortKey, cohortAuthors, time.Now())
	})
}
