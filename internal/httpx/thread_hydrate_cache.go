package httpx

import (
	"context"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/thread"
)

const threadHydrateWarmTTL = 3 * time.Minute

const (
	threadHydrateContextScope = "thread_hydrate_ctx"
	threadHydrateRepliesScope = "thread_hydrate_replies"
)

func probableThreadRootID(event nostrx.Event) string {
	if id := thread.RootID(event); id != "" {
		return id
	}
	return event.ID
}

func (s *Server) markThreadHydrateContextWarmed(ctx context.Context, rootID string) {
	if s == nil || s.store == nil || rootID == "" {
		return
	}
	s.store.MarkRefreshed(ctx, threadHydrateContextScope, rootID)
}

func (s *Server) threadHydrateContextReady(ctx context.Context, rootID string) bool {
	if s == nil || s.store == nil || rootID == "" {
		return false
	}
	return !s.store.ShouldRefresh(ctx, threadHydrateContextScope, rootID, threadHydrateWarmTTL)
}

func (s *Server) markThreadHydrateRepliesReady(ctx context.Context, rootID string) {
	if s == nil || s.store == nil || rootID == "" {
		return
	}
	s.store.MarkRefreshed(ctx, threadHydrateRepliesScope, rootID)
}

func (s *Server) threadHydrateRepliesReady(ctx context.Context, rootID string) bool {
	if s == nil || s.store == nil || rootID == "" {
		return false
	}
	return !s.store.ShouldRefresh(ctx, threadHydrateRepliesScope, rootID, threadHydrateWarmTTL)
}

// threadHydrateStoreFirst is true when repeat hydrate can serve from the store without
// relay reply fetches: both context and root reply hydration completed recently.
func (s *Server) threadHydrateStoreFirst(ctx context.Context, fragment, rootID string) bool {
	if fragment != "hydrate" || rootID == "" {
		return false
	}
	return s.threadHydrateContextReady(ctx, rootID) && s.threadHydrateRepliesReady(ctx, rootID)
}

// threadHydrateWarmIDsComplete reports whether eventsByID resolved every warm id.
func threadHydrateWarmIDsComplete(ids []string, loaded map[string]*nostrx.Event) bool {
	if len(ids) == 0 || loaded == nil {
		return false
	}
	for _, id := range ids {
		id = thread.NormalizeHexEventID(id)
		if id == "" {
			continue
		}
		if loaded[id] == nil {
			return false
		}
	}
	return true
}
