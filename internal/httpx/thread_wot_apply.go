package httpx

import (
	"net/http"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/thread"
)

type threadWoTApplyResult struct {
	Replies            []nostrx.Event
	FullReplies        []nostrx.Event
	FilteredReplies    []nostrx.Event
	ParentByID         map[string]string
	Enabled            bool
	Deferred           bool
	FullReplyWalk      bool
	Membership         authorMembership
	ResolveEvent       func(string) *nostrx.Event
	Root               *nostrx.Event
	Selected           *nostrx.Event
	ParentID           string
	FilteredReplyNodes []thread.Node
	SelectedDepth      int
	FocusMode          bool
	FocusIsRoot        bool
}

func threadFragmentCacheable(fragment string) bool {
	return true
}

// Browser fragment requests carry viewer/WoT headers and are already marked
// private, no-store. Only canonical header-free fragments may defer filtering
// for shared-cache reuse; personalized fragments must apply the active trust
// scope before returning HTML.
func threadRequestDefersWoT(r *http.Request, fragment string) bool {
	return threadFragmentCacheable(fragment) && !appShellRequestIsPersonalized(r)
}

func (s *Server) applyThreadWoT(
	r *http.Request,
	fragment string,
	globalWoTEnabled bool,
	replies, fullReplies []nostrx.Event,
	fullReplyWalk bool,
	root, selected *nostrx.Event,
	parentID string,
	parentByID map[string]string,
	membership authorMembership,
	resolveEvent func(string) *nostrx.Event,
) threadWoTApplyResult {
	out := threadWoTApplyResult{
		Replies:       replies,
		FullReplies:   fullReplies,
		ParentByID:    parentByID,
		FullReplyWalk: fullReplyWalk,
		Membership:    membership,
		ResolveEvent:  resolveEvent,
		Root:          root,
		Selected:      selected,
		ParentID:      parentID,
	}
	if threadRequestDefersWoT(r, fragment) {
		out.Deferred = globalWoTEnabled
		return out
	}
	out.Enabled = effectiveThreadWoTEnabled(r, globalWoTEnabled)
	if !out.Enabled {
		return out
	}
	replySource := replies
	if fullReplyWalk {
		replySource = fullReplies
	}
	var parent *nostrx.Event
	if parentID != "" && root != nil && parentID != root.ID {
		parent = resolveEvent(parentID)
	}
	partition := partitionThreadRepliesByWoT(
		replySource,
		root.ID,
		root,
		selected,
		parent,
		repairThreadParentMap(root.ID, replySource, parentByID),
		membership,
		resolveEvent,
	)
	out.FilteredReplies = partition.FilteredReplies
	if fullReplyWalk {
		out.FullReplies = partition.TreeReplies
	}
	out.Replies = partition.TreeReplies
	out.ParentByID = repairThreadParentMap(root.ID, out.Replies, parentByID)
	return out
}

func (r threadWoTApplyResult) buildFilteredReplyNodes(view thread.View, selectedDepth int, rootID string) []thread.Node {
	if !r.Enabled || len(r.FilteredReplies) == 0 || r.Selected == nil || r.Root == nil {
		return nil
	}
	focusID := r.Selected.ID
	if focusID == "" {
		focusID = r.Root.ID
	}
	return buildFilteredReplyNodes(
		r.FilteredReplies,
		filteredReplyRailDepth(view.FocusMode, selectedDepth, focusID == rootID),
		focusID,
	)
}
