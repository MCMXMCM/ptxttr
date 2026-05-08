package httpx

import (
	"context"
	"sort"
	"time"

	"ptxt-nstr/internal/nostrx"
)

// sortThreadRepliesStable matches threadRepliesPage ordering (created_at, then id).
func sortThreadRepliesStable(events []nostrx.Event) {
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].CreatedAt == events[j].CreatedAt {
			return events[i].ID < events[j].ID
		}
		return events[i].CreatedAt < events[j].CreatedAt
	})
}

func (s *Server) refreshReplies(ctx context.Context, eventID string, relays []string) {
	result := s.refreshCached(ctx, "thread", eventID, threadTTL, relays, nostrx.Query{
		Kinds: []int{nostrx.KindTextNote},
		Tags:  map[string][]string{"e": {eventID}},
		Limit: 200,
	})
	_ = s.store.MarkHydrationAttempt(ctx, "noteReplies", eventID, result >= 0, noteRepliesHydrationRetryWindow)
}

func (s *Server) refreshReactionsForNote(ctx context.Context, eventID string, relays []string) {
	if eventID == "" {
		return
	}
	result := s.refreshCached(ctx, "noteReactions", eventID, 10*time.Minute, relays, nostrx.Query{
		Kinds: []int{nostrx.KindReaction},
		Tags:  map[string][]string{"e": {eventID}},
		Limit: 80,
	})
	_ = s.store.MarkHydrationAttempt(ctx, "noteReactions", eventID, result >= 0, noteReactionsHydrationRetryWindow)
}

func (s *Server) threadRepliesPage(ctx context.Context, cursor int64, cursorID string, limit int, storeOnly bool, relays []string, parentIDs ...string) ([]nostrx.Event, int64, string, bool) {
	defer s.observe("thread.replies_sql", time.Now())
	if limit <= 0 {
		limit = 25
	}
	pageLimit := limit + 1
	seen := make(map[string]bool)
	var replies []nostrx.Event
	edges, err := s.store.ThreadEdgesCursor(ctx, parentIDs, cursor, cursorID, pageLimit)
	if err == nil && len(edges) > 0 {
		hasMore := len(edges) > limit
		if hasMore {
			edges = edges[:limit]
		}
		edgeIDs := make([]string, 0, len(edges))
		for _, edge := range edges {
			edgeIDs = append(edgeIDs, edge.NoteID)
		}
		var indexed map[string]*nostrx.Event
		if storeOnly {
			indexed = s.eventsByIDFromStore(ctx, edgeIDs)
		} else {
			indexed = s.eventsByID(ctx, edgeIDs, relays)
		}
		for _, edge := range edges {
			event := indexed[edge.NoteID]
			if event == nil || seen[event.ID] {
				continue
			}
			seen[event.ID] = true
			replies = append(replies, *event)
		}
		sortThreadRepliesStable(replies)
		var nextCursor int64
		var nextID string
		if len(edges) > 0 {
			last := edges[len(edges)-1]
			nextCursor = last.CreatedAt
			nextID = last.NoteID
		}
		return replies, nextCursor, nextID, hasMore
	}
	for _, id := range parentIDs {
		if id == "" || seen["query:"+id] {
			continue
		}
		seen["query:"+id] = true
		items, _ := s.store.RepliesTo(ctx, id, pageLimit)
		for _, item := range items {
			if seen[item.ID] {
				continue
			}
			seen[item.ID] = true
			replies = append(replies, item)
		}
	}
	sortThreadRepliesStable(replies)
	hasMore := len(replies) > limit
	if hasMore {
		replies = replies[:limit]
	}
	var nextCursor int64
	var nextID string
	if len(replies) > 0 {
		last := replies[len(replies)-1]
		nextCursor = last.CreatedAt
		nextID = last.ID
	}
	return replies, nextCursor, nextID, hasMore
}

func (s *Server) threadRelays(base []string, event nostrx.Event) []string {
	relays := append([]string(nil), base...)
	for _, tag := range event.Tags {
		if len(tag) >= 3 && (tag[0] == "e" || tag[0] == "a") {
			relays = append(relays, tag[2])
		}
		if len(tag) >= 2 && tag[0] == "r" {
			relays = append(relays, tag[1])
		}
	}
	return nostrx.NormalizeRelayList(relays, nostrx.MaxRelays)
}

func (s *Server) eventFromStore(ctx context.Context, id string) *nostrx.Event {
	if id == "" {
		return nil
	}
	event, err := s.store.GetEvent(ctx, id)
	if err != nil || event == nil {
		return nil
	}
	return event
}

func (s *Server) eventByID(ctx context.Context, id string, relays []string) *nostrx.Event {
	return s.eventByIDEx(ctx, id, relays, true)
}

// eventByIDEx returns the note from the store, or fetches from relays when
// allowRelayFetch is true (otherwise cache misses return nil).
func (s *Server) eventByIDEx(ctx context.Context, id string, relays []string, allowRelayFetch bool) *nostrx.Event {
	defer s.observe("event.by_id", time.Now())
	event, _ := s.store.GetEvent(ctx, id)
	if event != nil {
		s.metrics.Add("event.cache_hit", 1)
		return event
	}
	s.metrics.Add("event.cache_miss", 1)
	if !allowRelayFetch {
		return nil
	}
	s.metrics.Add("event.sync_fetch", 1)
	events, err := s.nostr.FetchFrom(ctx, relays, nostrx.Query{IDs: []string{id}, Limit: 5})
	if err != nil || len(events) == 0 {
		return nil
	}
	for _, fetched := range events {
		_ = s.store.SaveEvent(ctx, fetched)
	}
	return &events[0]
}

func (s *Server) eventsByID(ctx context.Context, ids []string, relays []string) map[string]*nostrx.Event {
	return s.eventsByIDEx(ctx, ids, relays, true)
}

func (s *Server) eventsByIDEx(ctx context.Context, ids []string, relays []string, allowRelayFetch bool) map[string]*nostrx.Event {
	defer s.observe("event.by_ids", time.Now())
	seen := make(map[string]bool, len(ids))
	ordered := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ordered = append(ordered, id)
	}
	out, _ := s.store.GetEvents(ctx, ordered)
	s.metrics.Add("event.cache_hit", int64(len(out)))
	var missing []string
	for _, id := range ordered {
		if out[id] == nil {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return out
	}
	s.metrics.Add("event.cache_miss", int64(len(missing)))
	if !allowRelayFetch {
		return out
	}
	s.metrics.Add("event.sync_fetch", int64(len(missing)))
	events, err := s.nostr.FetchFrom(ctx, relays, nostrx.Query{IDs: missing, Limit: max(5, len(missing))})
	if err != nil {
		return out
	}
	_, _ = s.store.SaveEvents(ctx, events)
	for _, fetched := range events {
		event := fetched
		out[event.ID] = &event
	}
	return out
}

func (s *Server) eventsByIDFromStore(ctx context.Context, ids []string) map[string]*nostrx.Event {
	defer s.observe("event.by_ids_store", time.Now())
	out, _ := s.store.GetEvents(ctx, ids)
	return out
}
