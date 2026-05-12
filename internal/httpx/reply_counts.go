package httpx

import (
	"context"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

func replyCountMap(stats map[string]store.ReplyStat) map[string]int {
	counts := make(map[string]int, len(stats))
	for id, stat := range stats {
		counts[id] = stat.DescendantReplies
	}
	return counts
}

func (s *Server) descendantReplyCounts(ctx context.Context, ids []string) (map[string]int, error) {
	stats, err := s.store.ReplyStatsByNoteIDs(ctx, ids)
	if err == nil {
		return replyCountMap(stats), nil
	}
	return s.store.ReplyCounts(ctx, ids)
}

func (s *Server) replyCounts(ctx context.Context, events []nostrx.Event) map[string]int {
	defer s.observe("store.reply_counts", time.Now())
	ids := extractEventIDs(events)
	counts, err := s.descendantReplyCounts(ctx, ids)
	if err == nil {
		return counts
	}
	counts = make(map[string]int, len(events))
	for _, event := range events {
		replies, _ := s.store.RepliesTo(ctx, event.ID, 500)
		counts[event.ID] = len(replies)
	}
	return counts
}
