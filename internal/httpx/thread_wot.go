package httpx

import (
	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/thread"
)

type threadWoTPartition struct {
	TrustedReplies  []nostrx.Event
	BridgeReplies   []nostrx.Event
	FilteredReplies []nostrx.Event
	TreeReplies     []nostrx.Event
}

func threadNaturalParentID(rootID string, event nostrx.Event, parentByID map[string]string) string {
	if parentByID != nil {
		if parentID := thread.NormalizeHexEventID(parentByID[event.ID]); parentID != "" {
			return parentID
		}
	}
	return thread.NormalizeHexEventID(thread.ParentID(rootID, event))
}

func isDirectThreadReply(reply nostrx.Event, parentID, rootID string, parentByID map[string]string) bool {
	parentID = thread.NormalizeHexEventID(parentID)
	rootID = thread.NormalizeHexEventID(rootID)
	if reply.ID == "" || reply.ID == parentID {
		return false
	}
	return threadNaturalParentID(rootID, reply, parentByID) == parentID
}

func threadContextEventIDs(
	rootID string,
	root, selected *nostrx.Event,
	parent *nostrx.Event,
	lookup func(string) *nostrx.Event,
) map[string]struct{} {
	preserved := make(map[string]struct{})
	if root != nil && root.ID != "" {
		preserved[root.ID] = struct{}{}
	}
	if selected != nil && selected.ID != "" {
		preserved[selected.ID] = struct{}{}
	}
	if parent != nil && parent.ID != "" {
		preserved[parent.ID] = struct{}{}
	}
	if selected == nil || lookup == nil {
		return preserved
	}
	current := *selected
	rootID = thread.NormalizeHexEventID(rootID)
	for hops := 0; hops < thread.MaxDepth; hops++ {
		parentID := thread.NormalizeHexEventID(thread.ParentID(rootID, current))
		if parentID == "" || parentID == rootID || parentID == current.ID {
			break
		}
		if _, ok := preserved[parentID]; ok {
			break
		}
		preserved[parentID] = struct{}{}
		ancestor := lookup(parentID)
		if ancestor == nil {
			break
		}
		current = *ancestor
	}
	return preserved
}

func partitionRepliesByWoT(
	replies []nostrx.Event,
	rootID string,
	root, selected *nostrx.Event,
	parent *nostrx.Event,
	membership authorMembership,
	lookup func(string) *nostrx.Event,
) (trusted, excluded []nostrx.Event) {
	preserved := threadContextEventIDs(rootID, root, selected, parent, lookup)
	for _, event := range replies {
		if event.ID == "" {
			continue
		}
		if _, ok := preserved[event.ID]; ok {
			trusted = append(trusted, event)
			continue
		}
		pubkey := nostrx.CanonicalHex64(event.PubKey)
		if pubkey == "" || !membership.Contains(pubkey) {
			excluded = append(excluded, event)
			continue
		}
		trusted = append(trusted, event)
	}
	return trusted, excluded
}

func bridgeRepliesForTree(
	trusted, allReplies []nostrx.Event,
	rootID string,
	parentByID map[string]string,
) []nostrx.Event {
	rootID = thread.NormalizeHexEventID(rootID)
	trustedIDs := make(map[string]struct{}, len(trusted))
	for _, reply := range trusted {
		if reply.ID != "" {
			trustedIDs[reply.ID] = struct{}{}
		}
	}
	replyByID := make(map[string]nostrx.Event, len(allReplies))
	for _, reply := range allReplies {
		if reply.ID != "" {
			replyByID[reply.ID] = reply
		}
	}
	includedIDs := make(map[string]struct{}, len(trustedIDs))
	for id := range trustedIDs {
		includedIDs[id] = struct{}{}
	}
	bridges := make([]nostrx.Event, 0, 8)
	bridgeIDs := make(map[string]struct{})
	for _, reply := range trusted {
		parentID := threadNaturalParentID(rootID, reply, parentByID)
		for parentID != "" && parentID != rootID && !containsID(includedIDs, parentID) {
			parentEvent, ok := replyByID[parentID]
			if !ok {
				break
			}
			if !containsID(trustedIDs, parentID) {
				if _, seen := bridgeIDs[parentID]; !seen {
					bridgeIDs[parentID] = struct{}{}
					bridges = append(bridges, parentEvent)
				}
			}
			includedIDs[parentID] = struct{}{}
			parentID = threadNaturalParentID(rootID, parentEvent, parentByID)
		}
	}
	sortThreadRepliesStable(bridges)
	return bridges
}

func containsID(set map[string]struct{}, id string) bool {
	_, ok := set[id]
	return ok
}

func partitionThreadRepliesByWoT(
	replies []nostrx.Event,
	rootID string,
	root, selected *nostrx.Event,
	parent *nostrx.Event,
	parentByID map[string]string,
	membership authorMembership,
	lookup func(string) *nostrx.Event,
) threadWoTPartition {
	allReplies := uniqueThreadEvents(replies)
	trusted, excluded := partitionRepliesByWoT(allReplies, rootID, root, selected, parent, membership, lookup)
	bridges := bridgeRepliesForTree(trusted, allReplies, rootID, parentByID)
	treeReplies := uniqueThreadEvents(append(append([]nostrx.Event(nil), trusted...), bridges...))
	sortThreadRepliesStable(treeReplies)
	bridgeIDs := make(map[string]struct{}, len(bridges))
	for _, bridge := range bridges {
		if bridge.ID != "" {
			bridgeIDs[bridge.ID] = struct{}{}
		}
	}

	focusID := thread.NormalizeHexEventID(selected.ID)
	if focusID == "" && root != nil {
		focusID = thread.NormalizeHexEventID(root.ID)
	}
	filteredDirect := make([]nostrx.Event, 0, len(excluded))
	for _, reply := range excluded {
		// An out-of-scope note that parents a trusted reply is visible structural
		// context, not a separately hidden reply. Rendering it in both places
		// duplicates the parent and lets later filtering remove the very edge that
		// keeps the trusted child attached to the correct branch.
		if _, isBridge := bridgeIDs[reply.ID]; isBridge {
			continue
		}
		if isDirectThreadReply(reply, focusID, rootID, parentByID) {
			filteredDirect = append(filteredDirect, reply)
		}
	}
	sortThreadRepliesStable(filteredDirect)

	return threadWoTPartition{
		TrustedReplies:  trusted,
		BridgeReplies:   bridges,
		FilteredReplies: filteredDirect,
		TreeReplies:     treeReplies,
	}
}

func buildFilteredReplyNodes(events []nostrx.Event, depth int, parentID string) []thread.Node {
	if len(events) == 0 {
		return nil
	}
	if depth < 1 {
		depth = 1
	}
	parentID = thread.NormalizeHexEventID(parentID)
	nodes := make([]thread.Node, 0, len(events))
	for _, event := range events {
		nodes = append(nodes, thread.Node{
			Event:    event,
			Depth:    depth,
			ParentID: parentID,
		})
	}
	return nodes
}

func filteredReplyRailDepth(focusedView bool, selectedDepth int, focusIsRoot bool) int {
	if focusIsRoot {
		return 1
	}
	if focusedView {
		return 1
	}
	depth := selectedDepth
	if depth > 5 {
		return 5
	}
	if depth < 1 {
		return 1
	}
	return depth
}

func mergeUniqueThreadEvents(base, extra []nostrx.Event) []nostrx.Event {
	if len(extra) == 0 {
		return base
	}
	return uniqueThreadEvents(append(append([]nostrx.Event(nil), base...), extra...))
}
