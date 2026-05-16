package httpx

import (
	"context"
	"sort"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
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
	if minRecompute <= 0 {
		minRecompute = 20 * time.Minute
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
	if cacheOnly {
		if cohortKey != "" {
			if global, _, gerr := s.store.ReadTrendingCache(ctx, timeframe, ""); gerr == nil && len(global) > 0 {
				s.metrics.Add("trending.sidebar_global_stale_fallback", 1)
				return global
			}
		}
		return []store.TrendingItem{}
	}
	items, _ = s.computeAndStoreCohortTrending(ctx, timeframe, cohortKey, authors, now)
	return items
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
	if err := s.store.WriteTrendingCache(ctx, timeframe, cohortKey, items, now.Unix()); err != nil {
		s.metrics.Add("trending.recompute_error", 1)
	}
	return items, nil
}

func (s *Server) buildTrendingItemsFromRecent(ctx context.Context, timeframe string, authors []string, now time.Time) ([]store.TrendingItem, error) {
	const candidateLimit = trendingCacheLimit * 8
	scanLimit := trendingScanLimit
	timeframe = normalizeTrendingTimeframe(timeframe)
	since := trendingSince(timeframe, now)
	membership := authorMembership{}
	if len(authors) > 0 {
		membership = newAuthorMembership(authors)
	}
	candidates := make([]nostrx.Event, 0, candidateLimit)
	seen := make(map[string]struct{}, candidateLimit)
	cursor := int64(0)
	cursorID := ""
	scanned := 0
	for scanned < scanLimit && len(candidates) < candidateLimit {
		batchLimit := min(scanFeedChunkSize, scanLimit-scanned)
		if batchLimit <= 0 {
			break
		}
		batch, err := s.store.RecentSummariesByKinds(ctx, noteTimelineKinds, since, cursor, cursorID, batchLimit)
		if err != nil || len(batch) == 0 {
			break
		}
		scanned += len(batch)
		for _, event := range batch {
			if len(membership.exact) > 0 && !membership.Contains(event.PubKey) {
				continue
			}
			if event.ID == "" {
				continue
			}
			if _, ok := seen[event.ID]; ok {
				continue
			}
			seen[event.ID] = struct{}{}
			candidates = append(candidates, event)
			if len(candidates) >= candidateLimit {
				break
			}
		}
		last := batch[len(batch)-1]
		cursor = last.CreatedAt
		cursorID = last.ID
		if len(batch) < batchLimit {
			break
		}
	}
	if len(candidates) == 0 {
		return []store.TrendingItem{}, nil
	}
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.ID)
	}
	stats, err := s.store.ReplyStatsByNoteIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	reactStats, _, rerr := s.store.ReactionStatsByNoteIDs(ctx, ids, "")
	if rerr != nil {
		reactStats = nil
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		ri := reactStats[candidates[i].ID].Total
		rj := reactStats[candidates[j].ID].Total
		// Score blends direct replies with deduped reaction total (up+down); tune weights here if needed.
		left := trendingEngagementScore(stats[candidates[i].ID].DirectReplies, ri)
		right := trendingEngagementScore(stats[candidates[j].ID].DirectReplies, rj)
		if left != right {
			return left > right
		}
		if candidates[i].CreatedAt != candidates[j].CreatedAt {
			return candidates[i].CreatedAt > candidates[j].CreatedAt
		}
		return candidates[i].ID > candidates[j].ID
	})
	items := make([]store.TrendingItem, 0, min(len(candidates), trendingCacheLimit))
	for _, candidate := range candidates {
		if trendingNoteIsReply(candidate) {
			continue
		}
		replyCount := stats[candidate.ID].DirectReplies
		rTot := 0
		if reactStats != nil {
			rTot = reactStats[candidate.ID].Total
		}
		if replyCount <= 0 && rTot <= 0 {
			continue
		}
		items = append(items, store.TrendingItem{
			NoteID:     candidate.ID,
			ReplyCount: replyCount,
		})
		if len(items) >= trendingCacheLimit {
			break
		}
	}
	if len(items) < trendingSidebarBackfillMin && len(candidates) > len(items) {
		inRanked := make(map[string]struct{}, len(items))
		for _, item := range items {
			inRanked[item.NoteID] = struct{}{}
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			if candidates[i].CreatedAt != candidates[j].CreatedAt {
				return candidates[i].CreatedAt > candidates[j].CreatedAt
			}
			return candidates[i].ID > candidates[j].ID
		})
		for _, candidate := range candidates {
			if _, ok := inRanked[candidate.ID]; ok {
				continue
			}
			if trendingNoteIsReply(candidate) {
				continue
			}
			items = append(items, store.TrendingItem{
				NoteID:     candidate.ID,
				ReplyCount: stats[candidate.ID].DirectReplies,
			})
			inRanked[candidate.ID] = struct{}{}
			if len(items) >= trendingCacheLimit || len(items) >= trendingSidebarBackfillMin {
				break
			}
		}
	}
	return items, nil
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
