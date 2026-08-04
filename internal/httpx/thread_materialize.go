package httpx

import (
	"context"
	"strings"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/thread"
)

type threadProjectionState string

const (
	threadProjectionReady threadProjectionState = "ready"
	threadProjectionStale threadProjectionState = "stale"
	threadProjectionMiss  threadProjectionState = "miss"
)

func (s *Server) threadProjectionStatus(ctx context.Context, selectedID string) (threadProjectionState, string) {
	if s == nil || s.store == nil {
		return threadProjectionMiss, ""
	}
	selectedID = thread.NormalizeHexEventID(selectedID)
	selected := s.eventFromStore(ctx, selectedID)
	if selected == nil {
		return threadProjectionMiss, ""
	}
	lookup := func(id string) *nostrx.Event { return s.eventFromStore(ctx, thread.NormalizeHexEventID(id)) }
	rootID := thread.NormalizeHexEventID(resolveThreadRootID(*selected, lookup))
	if rootID == "" {
		rootID = probableThreadRootID(*selected)
	}
	rootID = thread.NormalizeHexEventID(rootID)
	if rootID == "" || lookup(rootID) == nil {
		return threadProjectionMiss, rootID
	}
	cache, fresh, err := s.store.ThreadGraphCache(ctx, rootID)
	if err != nil || cache == nil {
		return threadProjectionMiss, rootID
	}
	if !fresh {
		if rebuilt, buildErr := s.store.BuildThreadGraphCache(ctx, rootID, 500); buildErr == nil && rebuilt != nil {
			cache = rebuilt
			fresh = true
			s.metrics.Add("thread.graph_cache.sync_rebuild", 1)
		}
	}
	if !selectedParentChainCoveredByIDs(rootID, *selected, cache.EventIDs, cache.ParentByID) {
		// Stale-while-revalidate is safe only for a coherent selected path. A
		// missing ancestor is a cache miss so navigation gets the bounded
		// foreground materialization pass instead of painting a parentless reply.
		return threadProjectionMiss, rootID
	}
	ids := append([]string{rootID, selectedID}, cache.EventIDs...)
	stored, err := s.store.GetEvents(ctx, uniqueNonEmptyStrings(ids))
	if err != nil {
		return threadProjectionMiss, rootID
	}
	for _, id := range uniqueNonEmptyStrings(ids) {
		if stored[id] == nil {
			return threadProjectionMiss, rootID
		}
	}
	if !fresh || !s.threadHydrateContextReady(ctx, rootID) || !s.threadHydrateRepliesReady(ctx, rootID) {
		return threadProjectionStale, rootID
	}
	return threadProjectionReady, rootID
}

func (s *Server) enqueueInteractiveThreadMaterialization(viewer, selectedID string, relays []string) bool {
	if s == nil || s.intentWarmer == nil {
		return false
	}
	selectedID = thread.NormalizeHexEventID(selectedID)
	if selectedID == "" {
		return false
	}
	key := "threadMaterialize:" + selectedID
	if s.warmer != nil && s.warmer.hasPending(key) {
		s.metrics.Add("warm.threadMaterialize.promoted", 1)
	}
	return s.intentWarmer.enqueue(warmJob{
		key:      key,
		kind:     "threadMaterialize",
		viewer:   strings.TrimSpace(viewer),
		eventIDs: []string{selectedID},
		relays:   append([]string(nil), relays...),
	})
}

func (s *Server) materializeThread(ctx context.Context, viewer, selectedID string, requestRelays []string) {
	if s == nil || s.store == nil || s.nostr == nil {
		return
	}
	selectedID = thread.NormalizeHexEventID(selectedID)
	selected := s.eventFromStore(ctx, selectedID)
	if selected == nil {
		return
	}
	relays := s.threadHydrationRelays(ctx, viewer, selected, selected, requestRelays)
	lookup := func(id string) *nostrx.Event {
		return s.threadContextEventByIDEx(ctx, thread.NormalizeHexEventID(id), relays, true)
	}
	rootID := thread.NormalizeHexEventID(resolveThreadRootID(*selected, lookup))
	if rootID == "" {
		rootID = thread.NormalizeHexEventID(probableThreadRootID(*selected))
	}
	root := lookup(rootID)
	if root == nil {
		return
	}

	// The root-tag query fills the whole compliant thread graph; the selected
	// pass covers relays that only index direct parent tags.
	s.refreshRepliesBackground(ctx, rootID, viewer, root, relays)
	if selectedID != rootID && ctx.Err() == nil {
		s.refreshRepliesBackground(ctx, selectedID, viewer, root, relays)
	}
	if ctx.Err() != nil {
		return
	}
	s.markThreadHydrateContextWarmed(ctx, rootID)
	s.buildThreadGraphCache(ctx, rootID)

	cache, _, _ := s.store.ThreadGraphCache(ctx, rootID)
	ids := []string{rootID, selectedID}
	if cache != nil {
		ids = append(ids, cache.EventIDs...)
	}
	events := s.eventsByIDInOrder(ctx, limitedStrings(uniqueNonEmptyStrings(ids), 100), true, nil)
	pinnedIDs := make([]string, 0, len(events))
	pubkeys := make([]string, 0, len(events))
	for _, event := range events {
		pinnedIDs = append(pinnedIDs, event.ID)
		pubkeys = append(pubkeys, event.PubKey)
	}
	_ = s.store.PinHotThread(ctx, rootID, pinnedIDs, 24*time.Hour, 50)
	for _, pubkey := range limitedStrings(uniqueNonEmptyStrings(pubkeys), maxWarmThreadProfileAuthors) {
		if ctx.Err() != nil {
			return
		}
		s.refreshAuthor(ctx, pubkey, relays)
	}
	for _, id := range limitedStrings(uniqueNonEmptyStrings([]string{rootID, selectedID}), 2) {
		if ctx.Err() != nil {
			return
		}
		s.refreshReactionsForNote(ctx, id, relays)
	}
	s.metrics.Add("thread.materialize.complete", 1)
}
