package httpx

import (
	"context"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/thread"
)

const threadTreeFetchLimit = 500

type ThreadTreeData struct {
	Root  nostrx.Event
	Nodes []thread.Node
}

func (s *Server) buildThreadTreeData(ctx context.Context, focus nostrx.Event, storeOnly bool, relays []string) ThreadTreeData {
	if focus.ID == "" {
		return ThreadTreeData{}
	}
	replies, _ := s.threadTreeReplies(ctx, focus.ID, storeOnly, relays)
	return buildThreadTreeDataFromReplies(focus, replies)
}

// buildThreadTreeDataFromReplies builds HN-style tree nodes from a flat reply
// slice already gathered (e.g. by threadTreeReplies) without a second walk.
func buildThreadTreeDataFromReplies(focus nostrx.Event, replies []nostrx.Event) ThreadTreeData {
	if focus.ID == "" {
		return ThreadTreeData{}
	}
	view := thread.BuildSelected(focus, focus, replies)
	return ThreadTreeData{
		Root:  focus,
		Nodes: view.Nodes,
	}
}

// threadTreeReplies BFS-walks from rootID up to thread.MaxDepth. The second
// return is true when the walk may have omitted events: a layer hit the page
// cap (threadRepliesPage hasMore), or the walk stopped at thread.MaxDepth
// with a non-empty frontier of parent ids.
func (s *Server) threadTreeReplies(ctx context.Context, rootID string, storeOnly bool, relays []string) ([]nostrx.Event, bool) {
	if s == nil || rootID == "" {
		return nil, false
	}
	seen := map[string]bool{rootID: true}
	parentIDs := []string{rootID}
	out := make([]nostrx.Event, 0, threadTreeFetchLimit)
	truncated := false
	var depth int
	// Walk the full reply chain (same cap as thread.MaxDepth) instead of a
	// shallow slice + synthetic "cont. thread" rows. Nested list layout/CSS
	// handles dense-branch presentation in the HN-style tree.
	for depth = 0; depth < thread.MaxDepth && len(parentIDs) > 0; depth++ {
		replies, _, _, layerHasMore := s.threadRepliesPage(ctx, 0, "", threadTreeFetchLimit, storeOnly, relays, parentIDs...)
		if layerHasMore {
			truncated = true
		}
		if len(replies) == 0 {
			break
		}
		nextParents := make([]string, 0, len(replies))
		for _, reply := range replies {
			if reply.ID == "" || seen[reply.ID] {
				continue
			}
			seen[reply.ID] = true
			out = append(out, reply)
			nextParents = append(nextParents, reply.ID)
		}
		parentIDs = nextParents
	}
	if depth == thread.MaxDepth && len(parentIDs) > 0 {
		truncated = true
	}
	return out, truncated
}
