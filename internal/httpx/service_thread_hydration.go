package httpx

import (
	"context"
	"time"

	"ptxt-nstr/internal/nostrx"
)

type refreshRepliesMode int

const (
	refreshRepliesSync refreshRepliesMode = iota
	refreshRepliesBackground
)

func (s *Server) refreshReplies(ctx context.Context, eventID string, relays []string) {
	s.refreshRepliesMode(ctx, eventID, relays, "", nil, nil, refreshRepliesSync)
}

func (s *Server) refreshRepliesBackground(ctx context.Context, eventID string, viewer string, root *nostrx.Event, requestRelays []string) {
	relays := s.threadHydrationRelays(ctx, viewer, root, root, requestRelays)
	s.refreshRepliesMode(ctx, eventID, relays, viewer, root, requestRelays, refreshRepliesBackground)
}

func (s *Server) refreshRepliesMode(ctx context.Context, eventID string, relays []string, viewer string, root *nostrx.Event, requestRelays []string, mode refreshRepliesMode) {
	if eventID == "" {
		return
	}
	if mode == refreshRepliesBackground && (root != nil || viewer != "" || len(requestRelays) > 0) {
		relays = s.threadHydrationRelays(ctx, viewer, root, root, requestRelays)
	}
	stored := s.threadStoredReplyCount(ctx, eventID)
	if s.finishRepliesHydrationIfSufficient(ctx, eventID, stored) {
		return
	}
	n := s.refreshRepliesPass(ctx, eventID, relays, "thread.relay_pass.outbox")
	n = maxReplyCount(n, stored)
	if s.finishRepliesHydrationIfSufficient(ctx, eventID, n) {
		return
	}
	// Outbox TTL still fresh: avoid indexer/NIP-50 when the store already has replies.
	if s.store != nil && !s.store.ShouldRefresh(ctx, "thread", eventID, threadTTL) {
		s.recordNoteRepliesHydration(ctx, eventID, n)
		return
	}
	indexer := s.indexerRelays()
	if len(indexer) > 0 {
		n2 := s.refreshRepliesPass(ctx, eventID, indexer, "thread.relay_pass.indexer")
		if n2 > n {
			n = n2
		}
	}
	n = maxReplyCount(n, stored)
	if s.finishRepliesHydrationIfSufficient(ctx, eventID, n) {
		return
	}
	if mode == refreshRepliesBackground && !s.threadReplyCountSufficient(n) && s.allowNIP50Fallback() {
		n3 := s.refreshRepliesNIP50(ctx, eventID)
		if n3 > n {
			n = n3
		}
	}
	if n == 0 {
		s.metrics.Add("hydration.note_replies.miss", 1)
	} else {
		s.metrics.Add("hydration.note_replies.events", int64(n))
	}
	s.recordNoteRepliesHydration(ctx, eventID, n)
}

func (s *Server) threadReplyCountSufficient(count int) bool {
	return count >= threadMinRepliesBeforeIndexer
}

func (s *Server) finishRepliesHydrationIfSufficient(ctx context.Context, eventID string, count int) bool {
	if !s.threadReplyCountSufficient(count) {
		return false
	}
	s.recordNoteRepliesHydration(ctx, eventID, count)
	return true
}

func maxReplyCount(n, stored int) int {
	if stored > n {
		return stored
	}
	return n
}

func (s *Server) threadStoredReplyCount(ctx context.Context, parentID string) int {
	if s == nil || s.store == nil || parentID == "" {
		return 0
	}
	counts, err := s.store.ReplyCounts(ctx, []string{parentID})
	if err != nil {
		return 0
	}
	return counts[parentID]
}

func (s *Server) threadExpectedDirectReplyPageCount(ctx context.Context, parentID string, limit int) int {
	if s == nil || s.store == nil || parentID == "" || limit <= 0 {
		return 0
	}
	stats, err := s.store.ReplyStatsByNoteIDs(ctx, []string{parentID})
	if err == nil {
		return min(stats[parentID].DirectReplies, limit)
	}
	edges, err := s.store.ThreadEdgesCursor(ctx, []string{parentID}, 0, "", limit)
	if err != nil {
		return 0
	}
	return len(edges)
}

func (s *Server) refreshRepliesPass(ctx context.Context, eventID string, relays []string, metric string) int {
	if len(relays) == 0 {
		return 0
	}
	s.metrics.Add(metric, 1)
	result := s.refreshCached(ctx, "thread", eventID, threadTTL, relays, nostrx.Query{
		Kinds: []int{nostrx.KindTextNote},
		Tags:  map[string][]string{"e": {eventID}},
		Limit: 200,
	})
	if result > 0 {
		return result
	}
	return 0
}

func (s *Server) recordNoteRepliesHydration(ctx context.Context, eventID string, result int) {
	success := result >= 0
	_ = s.store.MarkHydrationAttempt(ctx, "noteReplies", eventID, success, noteRepliesHydrationRetryWindow)
	if success {
		s.markThreadHydrateRepliesReady(ctx, eventID)
	}
}

func (s *Server) refreshRepliesNIP50(ctx context.Context, eventID string) int {
	indexer := s.nip50Relays()
	if len(indexer) == 0 || eventID == "" {
		return 0
	}
	s.metrics.Add("thread.nip50.fallback", 1)
	events, err := s.nostr.FetchFrom(ctx, indexer, nostrx.Query{
		Kinds:  []int{nostrx.KindTextNote},
		Search: eventID,
		Limit:  200,
	})
	if err != nil || len(events) == 0 {
		return -1
	}
	if _, err := s.store.SaveEvents(ctx, events); err != nil {
		return -1
	}
	s.invalidateResolvedAuthorsForEvents(events)
	return len(events)
}

func (s *Server) allowNIP50Fallback() bool {
	if s == nil {
		return false
	}
	limit := s.cfg.NIP50FallbackRatePerMin
	if limit <= 0 {
		limit = 30
	}
	now := time.Now()
	cutoff := now.Add(-time.Minute)
	s.nip50Mu.Lock()
	defer s.nip50Mu.Unlock()
	kept := s.nip50FallbackAt[:0]
	for _, t := range s.nip50FallbackAt {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= limit {
		s.nip50FallbackAt = kept
		return false
	}
	s.nip50FallbackAt = append(kept, now)
	return true
}
