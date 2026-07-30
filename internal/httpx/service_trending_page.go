package httpx

import (
	"context"
	"sort"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

// trendingRankKey is the sort key for ranked feed pages (hot score desc, engagement desc, replies desc, reactions desc, time desc, then id).
type trendingRankKey struct {
	hotScore  float64
	score     int
	replies   int
	reactions int
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
	if item.hotScore < anchor.hotScore {
		return true
	}
	if item.hotScore > anchor.hotScore {
		return false
	}
	if item.score < anchor.score {
		return true
	}
	if item.score > anchor.score {
		return false
	}
	if item.replies < anchor.replies {
		return true
	}
	if item.replies > anchor.replies {
		return false
	}
	if item.reactions < anchor.reactions {
		return true
	}
	if item.reactions > anchor.reactions {
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
		if cached, ok := s.lookupTrendingCursorKey(ctx, timeframe, cohortKey, authors, nostrx.Event{ID: cursorID, CreatedAt: key.createdAt}); ok {
			return cached
		}
		if key.createdAt > 0 {
			stats, _ := s.store.ReplyStatsByNoteIDs(ctx, []string{cursorID})
			if stats != nil {
				key.replies = stats[cursorID].DirectReplies
			}
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
		key.replies = stats[event.ID].DirectReplies
		key.score = key.replies
	}
	reactStats, _, _ := s.store.ReactionStatsByNoteIDs(ctx, []string{event.ID}, "")
	if reactStats != nil {
		key.reactions = reactStats[event.ID].Total
		key.score = trendingEngagementScore(key.replies, key.reactions)
	}
	return key
}

func (s *Server) trendingRankKeysAndEvents(ctx context.Context, items []store.TrendingItem) (map[string]trendingRankKey, map[string]*nostrx.Event) {
	if len(items) == 0 {
		return nil, nil
	}
	ids := noteIDsFromTrendingItems(items)
	byID := s.eventsByIDFromStore(ctx, ids)
	keys := make(map[string]trendingRankKey, len(items))
	for _, item := range items {
		if item.NoteID == "" {
			continue
		}
		score := item.Score
		if score <= 0 {
			score = item.ReplyCount + item.ReactionCount
		}
		hotScore := item.HotScore
		if hotScore <= 0 && score > 0 {
			hotScore = float64(score)
		}
		key := trendingRankKey{hotScore: hotScore, score: score, replies: item.ReplyCount, reactions: item.ReactionCount, createdAt: item.NoteCreatedAt, id: item.NoteID}
		if ev := byID[item.NoteID]; ev != nil {
			key.createdAt = ev.CreatedAt
		}
		keys[item.NoteID] = key
	}
	return keys, byID
}

func (s *Server) rankedTrendingFeedPageFromItems(ctx context.Context, items []store.TrendingItem, after trendingRankKey, limit int) ([]nostrx.Event, bool, trendingRankKey, bool) {
	if limit <= 0 {
		return nil, false, after, false
	}
	if len(items) == 0 {
		return nil, false, after, false
	}
	keys, byID := s.trendingRankKeysAndEvents(ctx, items)
	pageLimit := limit + 1
	type rankedTrendingEvent struct {
		event nostrx.Event
		key   trendingRankKey
	}
	ranked := make([]rankedTrendingEvent, 0, len(items))
	for _, item := range items {
		event := byID[item.NoteID]
		if event == nil {
			continue
		}
		key, ok := keys[item.NoteID]
		if !ok {
			key = trendingRankKey{score: item.ReplyCount, replies: item.ReplyCount, createdAt: event.CreatedAt, id: event.ID}
		}
		ranked = append(ranked, rankedTrendingEvent{event: *event, key: key})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		return trendingRankKeyLess(ranked[i].key, ranked[j].key)
	})
	events := make([]nostrx.Event, 0, pageLimit)
	var lastKey trendingRankKey
	started := after.id == ""
	anchorFound := after.id == ""
	for _, item := range ranked {
		key := item.key
		if !started {
			if key.id == after.id {
				started = true
				anchorFound = true
			}
			continue
		}
		if after.hotScore == 0 && after.createdAt > 0 {
			if key.createdAt > after.createdAt || (key.createdAt == after.createdAt && key.id >= after.id) {
				continue
			}
		}
		events = append(events, item.event)
		lastKey = key
		if len(events) >= pageLimit {
			break
		}
	}
	if !anchorFound && after.id != "" {
		for _, item := range ranked {
			key := item.key
			if !trendingRankFollowsCursor(key, after) {
				continue
			}
			events = append(events, item.event)
			lastKey = key
			if len(events) >= pageLimit {
				break
			}
		}
	}
	if len(events) == 0 {
		return nil, false, after, len(items) > 0
	}
	events, hasMore := trimRankedOverfetch(events, limit)
	if len(events) > 0 {
		lastKey = keys[events[len(events)-1].ID]
	}
	return events, hasMore, lastKey, true
}

func trendingRankKeyLess(left, right trendingRankKey) bool {
	if left.hotScore != right.hotScore {
		return left.hotScore > right.hotScore
	}
	if left.score != right.score {
		return left.score > right.score
	}
	if left.replies != right.replies {
		return left.replies > right.replies
	}
	if left.reactions != right.reactions {
		return left.reactions > right.reactions
	}
	if left.createdAt != right.createdAt {
		return left.createdAt > right.createdAt
	}
	return left.id > right.id
}

func (s *Server) rankedTrendingFeedPageFromCache(ctx context.Context, timeframe, cohortKey string, authors []string, after trendingRankKey, limit int) ([]nostrx.Event, bool, trendingRankKey, bool) {
	return s.rankedTrendingFeedPageFromItems(ctx, s.trendingItems(ctx, timeframe, cohortKey, authors, true), after, limit)
}

const relayTrendingHotSearch = "sort:hot protocol:nostr"
const relayTrendingSnapshotLimit = 120

func relayTrendingFollowsCursor(event nostrx.Event, after trendingRankKey) bool {
	if after.id == "" {
		return true
	}
	afterCreatedAt := int64(after.score)
	if event.CreatedAt < afterCreatedAt {
		return true
	}
	if event.CreatedAt > afterCreatedAt {
		return false
	}
	return event.ID < after.id
}

func relayTrendingSnapshotKey(sortMode string, relays []string) string {
	return normalizeFeedSort(sortMode) + "|" + hashStringSlice(relays)
}

func relayTrendingFeedPageFromSnapshot(snapshot []nostrx.Event, after trendingRankKey, limit int) ([]nostrx.Event, bool, trendingRankKey, bool) {
	if limit <= 0 || len(snapshot) == 0 {
		return nil, false, after, false
	}
	start := 0
	if after.id != "" {
		found := false
		for idx, event := range snapshot {
			if event.ID == after.id {
				start = idx + 1
				found = true
				break
			}
		}
		if !found {
			filtered := make([]nostrx.Event, 0, limit+1)
			for _, event := range snapshot {
				if !relayTrendingFollowsCursor(event, after) {
					continue
				}
				filtered = append(filtered, event)
				if len(filtered) >= limit+1 {
					break
				}
			}
			if len(filtered) == 0 {
				return nil, false, after, false
			}
			filtered, hasMore := trimRankedOverfetch(filtered, limit)
			last := filtered[len(filtered)-1]
			return filtered, hasMore, trendingRankKey{score: int(last.CreatedAt), createdAt: last.CreatedAt, id: last.ID}, true
		}
	}
	if start >= len(snapshot) {
		return nil, false, after, false
	}
	end := min(start+limit+1, len(snapshot))
	events := append([]nostrx.Event(nil), snapshot[start:end]...)
	events, hasMore := trimRankedOverfetch(events, limit)
	last := events[len(events)-1]
	return events, hasMore, trendingRankKey{score: int(last.CreatedAt), createdAt: last.CreatedAt, id: last.ID}, true
}

func (s *Server) relayTrendingFeedPage(ctx context.Context, sortMode string, after trendingRankKey, limit int) ([]nostrx.Event, bool, trendingRankKey, bool) {
	if s == nil || s.nostr == nil || limit <= 0 {
		return nil, false, after, false
	}
	relays := s.trendingSearchRelays()
	if len(relays) == 0 {
		return nil, false, after, false
	}
	cacheKey := relayTrendingSnapshotKey(sortMode, relays)
	if snapshot, hit := s.relayTrendingCache.get(cacheKey, time.Now()); hit {
		return relayTrendingFeedPageFromSnapshot(snapshot.Events, after, limit)
	}
	query := nostrx.Query{
		Kinds:  noteTimelineKinds,
		Search: relayTrendingHotSearch,
		Since:  feedSortSince(sortMode, time.Now()),
		Limit:  relayTrendingSnapshotLimit,
	}
	fetched, err := s.nostr.FetchFirstFrom(ctx, relays, query)
	if err != nil || len(fetched) == 0 {
		return nil, false, after, false
	}
	if _, saveErr := s.store.SaveEvents(ctx, fetched); saveErr == nil {
		s.invalidateResolvedAuthorsForEvents(fetched)
	}
	snapshotEvents := make([]nostrx.Event, 0, min(len(fetched), relayTrendingSnapshotLimit))
	for _, event := range fetched {
		if isReplyEvent(event) {
			continue
		}
		snapshotEvents = append(snapshotEvents, event)
		if len(snapshotEvents) >= relayTrendingSnapshotLimit {
			break
		}
	}
	if len(snapshotEvents) == 0 {
		return nil, false, after, false
	}
	s.relayTrendingCache.put(cacheKey, relayTrendingSnapshot{Events: snapshotEvents}, time.Now())
	return relayTrendingFeedPageFromSnapshot(snapshotEvents, after, limit)
}

func rankedFeedPaginationCursor(s *Server, ctx context.Context, events []nostrx.Event, timeframe, cohortKey string, authors []string, allowGlobalFallback bool, rankAfter trendingRankKey) (int64, string) {
	tail := events[len(events)-1]
	key := s.rankedCursorKeyForEvent(ctx, timeframe, cohortKey, authors, allowGlobalFallback, tail, rankAfter)
	return int64(key.score), key.id
}

func (s *Server) rankedCursorKeyForEvent(ctx context.Context, timeframe, cohortKey string, authors []string, allowGlobalFallback bool, event nostrx.Event, rankAfter trendingRankKey) trendingRankKey {
	if event.ID == "" {
		return trendingRankKey{}
	}
	if key, ok := s.lookupTrendingCursorKey(ctx, timeframe, cohortKey, authors, event); ok {
		return key
	}
	if allowGlobalFallback && cohortKey != "" {
		if key, ok := s.lookupTrendingCursorKey(ctx, timeframe, "", nil, event); ok {
			return key
		}
	}
	if rankAfter.id == event.ID && rankAfter.score > 0 {
		rankAfter.createdAt = event.CreatedAt
		return rankAfter
	}
	key := s.trendingRankKeyForEvent(ctx, event)
	if key.createdAt == 0 {
		key.createdAt = event.CreatedAt
	}
	return key
}

func (s *Server) lookupTrendingCursorKey(ctx context.Context, timeframe, cohortKey string, authors []string, event nostrx.Event) (trendingRankKey, bool) {
	items, _, err := s.store.ReadTrendingCache(ctx, normalizeTrendingTimeframe(timeframe), cohortKey)
	if err != nil || len(items) == 0 {
		return trendingRankKey{}, false
	}
	if len(authors) > 0 {
		items = s.filterTrendingItemsToAuthors(ctx, items, authors)
	}
	for _, item := range items {
		if item.NoteID != event.ID {
			continue
		}
		score := item.Score
		if score <= 0 {
			score = item.ReplyCount + item.ReactionCount
		}
		hotScore := item.HotScore
		if hotScore <= 0 && score > 0 {
			hotScore = float64(score)
		}
		createdAt := event.CreatedAt
		if createdAt == 0 {
			createdAt = item.NoteCreatedAt
		}
		return trendingRankKey{hotScore: hotScore, score: score, replies: item.ReplyCount, reactions: item.ReactionCount, createdAt: createdAt, id: event.ID}, true
	}
	return trendingRankKey{}, false
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
