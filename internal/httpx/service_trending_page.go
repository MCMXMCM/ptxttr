package httpx

import (
	"context"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

// trendingRankKey is the sort key for ranked feed pages (score desc, then time, then id).
type trendingRankKey struct {
	score     int
	createdAt int64
	id        string
}

func trendingEngagementScore(directReplies, reactionTotal int) int {
	return directReplies + reactionTotal
}

// trendingRankFollowsCursor reports whether item ranks below anchor (belongs on later pages).
func trendingRankFollowsCursor(item, anchor trendingRankKey) bool {
	if anchor.id == "" {
		return true
	}
	if item.score < anchor.score {
		return true
	}
	if item.score > anchor.score {
		return false
	}
	if item.createdAt < anchor.createdAt {
		return true
	}
	if item.createdAt > anchor.createdAt {
		return false
	}
	return item.id < anchor.id
}

func (s *Server) resolveTrendingFeedCursor(ctx context.Context, cursor int64, cursorID, timeframe, cohortKey string, authors []string) trendingRankKey {
	if cursorID != "" {
		key := trendingRankKey{score: int(cursor), id: cursorID}
		if ev, err := s.store.GetEvent(ctx, cursorID); err == nil && ev != nil {
			key.createdAt = ev.CreatedAt
		}
		return key
	}
	if cursor <= 0 {
		return trendingRankKey{}
	}
	items := s.trendingItems(ctx, timeframe, cohortKey, authors, true)
	idx := int(cursor) - 1
	if idx >= 0 && idx < len(items) {
		keys, _ := s.trendingRankKeysAndEvents(ctx, items[idx:idx+1])
		if key, ok := keys[items[idx].NoteID]; ok {
			return key
		}
	}
	return trendingRankKey{}
}

func (s *Server) trendingRankKeyForEvent(ctx context.Context, event nostrx.Event) trendingRankKey {
	key := trendingRankKey{id: event.ID, createdAt: event.CreatedAt}
	stats, _ := s.store.ReplyStatsByNoteIDs(ctx, []string{event.ID})
	if stats != nil {
		key.score = stats[event.ID].DirectReplies
	}
	reactStats, _, _ := s.store.ReactionStatsByNoteIDs(ctx, []string{event.ID}, "")
	if reactStats != nil {
		key.score = trendingEngagementScore(key.score, reactStats[event.ID].Total)
	}
	return key
}

func (s *Server) trendingRankKeysAndEvents(ctx context.Context, items []store.TrendingItem) (map[string]trendingRankKey, map[string]*nostrx.Event) {
	if len(items) == 0 {
		return nil, nil
	}
	ids := noteIDsFromTrendingItems(items)
	byID := s.eventsByIDFromStore(ctx, ids)
	reactStats, _, _ := s.store.ReactionStatsByNoteIDs(ctx, ids, "")
	keys := make(map[string]trendingRankKey, len(items))
	for _, item := range items {
		if item.NoteID == "" {
			continue
		}
		key := trendingRankKey{score: item.ReplyCount, id: item.NoteID}
		if ev := byID[item.NoteID]; ev != nil {
			key.createdAt = ev.CreatedAt
		}
		if reactStats != nil {
			key.score = trendingEngagementScore(key.score, reactStats[item.NoteID].Total)
		}
		keys[item.NoteID] = key
	}
	return keys, byID
}

func (s *Server) rankedTrendingFeedPageFromCache(ctx context.Context, timeframe, cohortKey string, authors []string, after trendingRankKey, limit int) ([]nostrx.Event, bool, trendingRankKey, bool) {
	if limit <= 0 {
		return nil, false, after, false
	}
	items := s.trendingItems(ctx, timeframe, cohortKey, authors, true)
	if len(items) == 0 {
		return nil, false, after, false
	}
	keys, byID := s.trendingRankKeysAndEvents(ctx, items)
	pageLimit := limit + 1
	events := make([]nostrx.Event, 0, pageLimit)
	for _, item := range items {
		event := byID[item.NoteID]
		if event == nil {
			continue
		}
		key, ok := keys[item.NoteID]
		if !ok {
			key = trendingRankKey{score: item.ReplyCount, createdAt: event.CreatedAt, id: event.ID}
		}
		if !trendingRankFollowsCursor(key, after) {
			continue
		}
		events = append(events, *event)
		if len(events) >= pageLimit {
			break
		}
	}
	if len(events) == 0 {
		return nil, false, after, len(items) > 0
	}
	events, hasMore := trimRankedOverfetch(events, limit)
	lastKey := keys[events[len(events)-1].ID]
	return events, hasMore, lastKey, true
}

func rankedFeedPaginationCursor(s *Server, ctx context.Context, events []nostrx.Event, rankAfter trendingRankKey, muteViewer string) (int64, string) {
	tail := events[len(events)-1]
	if muteViewer != "" && rankAfter.id != "" && rankAfter.id != tail.ID {
		return int64(rankAfter.score), rankAfter.id
	}
	key := s.trendingRankKeyForEvent(ctx, tail)
	return int64(key.score), key.id
}

func trimRankedOverfetch(events []nostrx.Event, limit int) ([]nostrx.Event, bool) {
	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}
	return events, hasMore
}

func noteIDsFromTrendingItems(items []store.TrendingItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if item.NoteID != "" {
			ids = append(ids, item.NoteID)
		}
	}
	return ids
}
