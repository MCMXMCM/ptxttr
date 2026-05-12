package httpx

import (
	"context"
	"log/slog"
	"time"

	"ptxt-nstr/internal/store"
)

type hydrationBatch struct {
	entityType string
	limit      int
	metric     string
	warm       func(context.Context, []store.HydrationTarget, []string)
}

func (s *Server) runSweeper(interval, timeout time.Duration, job func(context.Context)) {
	if s == nil || s.store == nil {
		return
	}
	if interval <= 0 {
		interval = time.Minute
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(s.ctx, timeout)
			job(ctx)
			cancel()
		}
	}
}

func uniqueHydrationIDs(items []store.HydrationTarget) []string {
	seen := make(map[string]bool, len(items))
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if item.EntityID == "" || seen[item.EntityID] {
			continue
		}
		seen[item.EntityID] = true
		ids = append(ids, item.EntityID)
	}
	return ids
}

func (s *Server) warmHydrationAuthors(ctx context.Context, items []store.HydrationTarget, baseRelays []string) {
	for _, pubkey := range uniqueHydrationIDs(items) {
		s.warmAuthor(pubkey, s.authorMetadataRelays(ctx, pubkey, baseRelays))
	}
}

func (s *Server) warmHydrationThreads(_ context.Context, items []store.HydrationTarget, baseRelays []string) {
	s.warmThread(uniqueHydrationIDs(items), baseRelays)
}

func (s *Server) sweepHydrationBatch(ctx context.Context, baseRelays []string, batch hydrationBatch) {
	items, err := s.store.StaleHydrationBatch(ctx, batch.entityType, time.Now().Unix(), batch.limit)
	if err != nil {
		slog.Debug("sweep hydration batch failed", "entity_type", batch.entityType, "err", err)
		return
	}
	batch.warm(ctx, items, baseRelays)
	s.metrics.Add(batch.metric, int64(len(uniqueHydrationIDs(items))))
}
