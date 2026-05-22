package httpx

import (
	"context"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
	"ptxt-nstr/internal/thread"
)

type ThreadAssembly struct {
	Root         nostrx.Event
	Selected     nostrx.Event
	ParentID     string
	Ancestors    []nostrx.Event
	TreeReplies  []nostrx.Event
	ParentByID   map[string]string
	NextCursor   int64
	NextCursorID string
	HasMore      bool
	Truncated    bool
	Incomplete   bool
}

func (s *Server) assembleThread(ctx context.Context, root, selected nostrx.Event, storeOnly bool, relays []string, lookup func(string) *nostrx.Event) ThreadAssembly {
	assembly := ThreadAssembly{
		Root:       root,
		Selected:   selected,
		ParentID:   thread.ParentID(root.ID, selected),
		ParentByID: make(map[string]string),
	}
	appendParent := func(event nostrx.Event, parentID string) {
		if event.ID == "" || event.ID == root.ID {
			return
		}
		if parentID == "" {
			parentID = thread.ParentID(root.ID, event)
		}
		assembly.ParentByID[event.ID] = thread.NormalizeHexEventID(parentID)
	}
	if selected.ID != "" && selected.ID != root.ID {
		appendParent(selected, assembly.ParentID)
	}

	current := selected
	seenAncestors := map[string]bool{selected.ID: true}
	for hops := 0; hops < thread.MaxDepth; hops++ {
		parentID := thread.NormalizeHexEventID(thread.ParentID(root.ID, current))
		if parentID == "" || parentID == root.ID || parentID == current.ID || seenAncestors[parentID] {
			break
		}
		if lookup == nil {
			break
		}
		parent := lookup(parentID)
		if parent == nil {
			break
		}
		assembly.Ancestors = append(assembly.Ancestors, *parent)
		appendParent(*parent, thread.ParentID(root.ID, *parent))
		seenAncestors[parentID] = true
		current = *parent
	}

	selectedChildren, selectedEdges, selectedHasMore := s.threadRepliesWithEdges(ctx, []string{selected.ID}, 0, "", threadTreeFetchLimit, storeOnly, relays)
	mergeParentEdges(assembly.ParentByID, selectedEdges)
	assembly.Truncated = assembly.Truncated || selectedHasMore
	if len(selectedChildren) < len(selectedEdges) {
		assembly.Incomplete = true
	}

	pathParentIDs := make([]string, 0, len(assembly.Ancestors))
	for _, ancestor := range assembly.Ancestors {
		if ancestor.ID != "" && ancestor.ID != selected.ID {
			pathParentIDs = append(pathParentIDs, ancestor.ID)
		}
	}
	pathReplies, pathEdges, pathHasMore := s.threadRepliesWithEdges(ctx, pathParentIDs, 0, "", threadTreeFetchLimit, storeOnly, relays)
	mergeParentEdges(assembly.ParentByID, pathEdges)
	assembly.Truncated = assembly.Truncated || pathHasMore
	if len(pathReplies) < len(pathEdges) {
		assembly.Incomplete = true
	}

	rootReplies, rootEdges, nextCursor, nextCursorID, rootHasMore := s.threadRootRepliesPage(ctx, root.ID, 0, "", threadTreeFetchLimit, storeOnly, relays)
	mergeParentEdges(assembly.ParentByID, rootEdges)
	assembly.NextCursor = nextCursor
	assembly.NextCursorID = nextCursorID
	assembly.HasMore = rootHasMore
	assembly.Truncated = assembly.Truncated || rootHasMore
	if len(rootReplies) < len(rootEdges) {
		assembly.Incomplete = true
	}

	combined := make([]nostrx.Event, 0, 1+len(assembly.Ancestors)+len(selectedChildren)+len(pathReplies)+len(rootReplies))
	if selected.ID != "" && selected.ID != root.ID {
		combined = append(combined, selected)
	}
	combined = append(combined, assembly.Ancestors...)
	combined = append(combined, selectedChildren...)
	combined = append(combined, pathReplies...)
	combined = append(combined, rootReplies...)
	assembly.TreeReplies = uniqueThreadEvents(combined)
	sortThreadRepliesStable(assembly.TreeReplies)
	assembly.ParentByID = repairThreadParentMap(root.ID, assembly.TreeReplies, assembly.ParentByID)
	return assembly
}

func (s *Server) threadRepliesWithEdges(ctx context.Context, parentIDs []string, cursor int64, cursorID string, limit int, storeOnly bool, relays []string) ([]nostrx.Event, []store.NoteLink, bool) {
	if s == nil || s.store == nil || len(parentIDs) == 0 {
		return nil, nil, false
	}
	if limit <= 0 {
		limit = threadTreeFetchLimit
	}
	edges, err := s.store.ThreadEdgesCursor(ctx, parentIDs, cursor, cursorID, limit+1)
	if err != nil || len(edges) == 0 {
		return nil, nil, false
	}
	hasMore := len(edges) > limit
	if hasMore {
		edges = edges[:limit]
	}
	return s.eventsForThreadEdges(ctx, edges, storeOnly, relays), edges, hasMore
}

func (s *Server) threadRootRepliesPage(ctx context.Context, rootID string, cursor int64, cursorID string, limit int, storeOnly bool, relays []string) ([]nostrx.Event, []store.NoteLink, int64, string, bool) {
	if s == nil || s.store == nil || rootID == "" {
		return nil, nil, 0, "", false
	}
	if limit <= 0 {
		limit = threadTreeFetchLimit
	}
	edges, err := s.store.ThreadRootEdgesCursor(ctx, rootID, cursor, cursorID, limit+1)
	if err != nil || len(edges) == 0 {
		return nil, nil, 0, "", false
	}
	hasMore := len(edges) > limit
	if hasMore {
		edges = edges[:limit]
	}
	var nextCursor int64
	var nextID string
	if len(edges) > 0 {
		last := edges[len(edges)-1]
		nextCursor = last.CreatedAt
		nextID = last.NoteID
	}
	return s.eventsForThreadEdges(ctx, edges, storeOnly, relays), edges, nextCursor, nextID, hasMore
}

func (s *Server) eventsForThreadEdges(ctx context.Context, edges []store.NoteLink, storeOnly bool, relays []string) []nostrx.Event {
	if len(edges) == 0 {
		return nil
	}
	ids := make([]string, 0, len(edges))
	for _, edge := range edges {
		if edge.NoteID != "" {
			ids = append(ids, edge.NoteID)
		}
	}
	var indexed map[string]*nostrx.Event
	if storeOnly {
		indexed = s.eventsByIDFromStore(ctx, ids)
	} else {
		indexed = s.eventsByID(ctx, ids, relays)
	}
	out := make([]nostrx.Event, 0, len(edges))
	seen := make(map[string]bool, len(edges))
	for _, edge := range edges {
		event := indexed[edge.NoteID]
		if event == nil || event.ID == "" || seen[event.ID] {
			continue
		}
		seen[event.ID] = true
		out = append(out, *event)
	}
	sortThreadRepliesStable(out)
	return out
}

func mergeParentEdges(parentByID map[string]string, edges []store.NoteLink) {
	for _, edge := range edges {
		if edge.NoteID == "" {
			continue
		}
		parentByID[edge.NoteID] = thread.NormalizeHexEventID(edge.ParentID)
	}
}

func uniqueThreadEvents(events []nostrx.Event) []nostrx.Event {
	seen := make(map[string]bool, len(events))
	out := make([]nostrx.Event, 0, len(events))
	for _, event := range events {
		if event.ID == "" || seen[event.ID] {
			continue
		}
		seen[event.ID] = true
		out = append(out, event)
	}
	return out
}

func repairThreadParentMap(rootID string, replies []nostrx.Event, parentByID map[string]string) map[string]string {
	rootID = thread.NormalizeHexEventID(rootID)
	repaired := make(map[string]string, len(parentByID)+len(replies))
	for id, parentID := range parentByID {
		repaired[id] = thread.NormalizeHexEventID(parentID)
	}
	available := make(map[string]struct{}, len(replies)+1)
	if rootID != "" {
		available[rootID] = struct{}{}
	}
	for _, reply := range replies {
		if reply.ID != "" {
			available[reply.ID] = struct{}{}
		}
	}
	for _, reply := range replies {
		if reply.ID == "" || reply.ID == rootID {
			continue
		}
		parentID := thread.NormalizeHexEventID(repaired[reply.ID])
		if parentID == "" {
			parentID = thread.NormalizeHexEventID(thread.ParentID(rootID, reply))
		}
		if parentID == "" || parentID == reply.ID {
			repaired[reply.ID] = rootID
			continue
		}
		if _, ok := available[parentID]; ok {
			repaired[reply.ID] = parentID
			continue
		}
		repaired[reply.ID] = closestKnownThreadAncestor(rootID, reply, available)
	}
	return repaired
}

func closestKnownThreadAncestor(rootID string, event nostrx.Event, available map[string]struct{}) string {
	rootID = thread.NormalizeHexEventID(rootID)
	for i := len(event.Tags) - 1; i >= 0; i-- {
		tag := event.Tags[i]
		if len(tag) < 2 || tag[0] != "e" {
			continue
		}
		ref := thread.NormalizeHexEventID(tag[1])
		if ref == "" || ref == event.ID {
			continue
		}
		if _, ok := available[ref]; ok {
			return ref
		}
	}
	return rootID
}
