package httpx

import (
	"testing"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/thread"
)

func TestThreadContextEventIDsPreservesRootSelectedParentAndAncestors(t *testing.T) {
	root := nostrx.Event{ID: "root", PubKey: "aa", Kind: nostrx.KindTextNote}
	selected := nostrx.Event{ID: "selected", PubKey: "bb", Kind: nostrx.KindTextNote, Tags: [][]string{{"e", "parent", "", "reply"}, {"e", "root", "", "root"}}}
	parent := nostrx.Event{ID: "parent", PubKey: "cc", Kind: nostrx.KindTextNote, Tags: [][]string{{"e", "root", "", "root"}}}
	lookup := map[string]*nostrx.Event{
		"parent": &parent,
	}

	preserved := threadContextEventIDs("root", &root, &selected, &parent, func(id string) *nostrx.Event {
		return lookup[id]
	})
	for _, id := range []string{"root", "selected", "parent"} {
		if _, ok := preserved[id]; !ok {
			t.Fatalf("expected preserved id %q, got %v", id, preserved)
		}
	}
}

func TestPartitionRepliesByWoTIncludesTrustedAndPreservesContext(t *testing.T) {
	root := nostrx.Event{ID: "root", PubKey: "aa"}
	selected := nostrx.Event{ID: "selected", PubKey: "bb"}
	trustedReply := nostrx.Event{ID: "trusted", PubKey: "trusted-author", Kind: nostrx.KindTextNote, Tags: [][]string{{"e", "selected", "", "reply"}, {"e", "root", "", "root"}}}
	excludedReply := nostrx.Event{ID: "excluded", PubKey: "stranger", Kind: nostrx.KindTextNote, Tags: [][]string{{"e", "selected", "", "reply"}, {"e", "root", "", "root"}}}
	preservedReply := nostrx.Event{ID: "selected", PubKey: "bb"}

	membership := newAuthorMembership([]string{"aa", "bb", "trusted-author"})
	replies := []nostrx.Event{trustedReply, excludedReply, preservedReply}
	trusted, excluded := partitionRepliesByWoT(replies, "root", &root, &selected, nil, membership, nil)

	if len(trusted) != 2 {
		t.Fatalf("trusted = %d, want 2", len(trusted))
	}
	if len(excluded) != 1 || excluded[0].ID != "excluded" {
		t.Fatalf("excluded = %#v, want excluded only", excluded)
	}
}

func TestBridgeRepliesForTreeIncludesUntrustedParentChain(t *testing.T) {
	rootID := "root"
	trustedChild := nostrx.Event{
		ID:     "child",
		Kind:   nostrx.KindTextNote,
		PubKey: "trusted-author",
		Tags:   [][]string{{"e", "bridge", "", "reply"}, {"e", rootID, "", "root"}},
	}
	bridgeParent := nostrx.Event{
		ID:     "bridge",
		Kind:   nostrx.KindTextNote,
		PubKey: "stranger",
		Tags:   [][]string{{"e", rootID, "", "root"}},
	}
	trusted := []nostrx.Event{trustedChild}
	allReplies := []nostrx.Event{trustedChild, bridgeParent}
	parentByID := map[string]string{
		"child": "bridge",
	}

	bridges := bridgeRepliesForTree(trusted, allReplies, rootID, parentByID)
	if len(bridges) != 1 || bridges[0].ID != "bridge" {
		t.Fatalf("bridges = %#v, want bridge parent", bridges)
	}
}

func TestPartitionThreadRepliesByWoTPreservesTrustedReplyParentage(t *testing.T) {
	root := nostrx.Event{ID: "root", PubKey: "root-author"}
	bridgeParent := nostrx.Event{
		ID:     "bridge",
		Kind:   nostrx.KindTextNote,
		PubKey: "stranger",
		Tags:   [][]string{{"e", root.ID, "", "root"}, {"e", root.ID, "", "reply"}},
	}
	trustedChild := nostrx.Event{
		ID:     "child",
		Kind:   nostrx.KindTextNote,
		PubKey: "trusted-author",
		Tags:   [][]string{{"e", root.ID, "", "root"}, {"e", bridgeParent.ID, "", "reply"}},
	}
	partition := partitionThreadRepliesByWoT(
		[]nostrx.Event{bridgeParent, trustedChild},
		root.ID,
		&root,
		&root,
		nil,
		map[string]string{bridgeParent.ID: root.ID, trustedChild.ID: bridgeParent.ID},
		newAuthorMembership([]string{root.PubKey, trustedChild.PubKey}),
		nil,
	)

	if len(partition.TreeReplies) != 2 {
		t.Fatalf("TreeReplies = %#v, want bridge parent and trusted child", partition.TreeReplies)
	}
	if len(partition.FilteredReplies) != 0 {
		t.Fatalf("FilteredReplies = %#v, bridge parent must not also render as filtered", partition.FilteredReplies)
	}
	parentByID := repairThreadParentMap(root.ID, partition.TreeReplies, map[string]string{
		bridgeParent.ID: root.ID,
		trustedChild.ID: bridgeParent.ID,
	})
	view := thread.BuildSelectedWithParents(root, root, partition.TreeReplies, parentByID)
	if len(view.Nodes) != 1 || view.Nodes[0].Event.ID != bridgeParent.ID {
		t.Fatalf("root nodes = %#v, want bridge parent only", view.Nodes)
	}
	if len(view.Nodes[0].Children) != 1 || view.Nodes[0].Children[0].Event.ID != trustedChild.ID {
		t.Fatalf("bridge children = %#v, want trusted child nested beneath its parent", view.Nodes[0].Children)
	}
}

func TestPartitionThreadRepliesByWoTFiltersDirectRepliesOnly(t *testing.T) {
	root := nostrx.Event{ID: "root", PubKey: "aa"}
	selected := nostrx.Event{ID: "selected", PubKey: "bb"}
	directExcluded := nostrx.Event{
		ID:     "direct",
		Kind:   nostrx.KindTextNote,
		PubKey: "stranger",
		Tags:   [][]string{{"e", "selected", "", "reply"}, {"e", "root", "", "root"}},
	}
	nestedExcluded := nostrx.Event{
		ID:     "nested",
		Kind:   nostrx.KindTextNote,
		PubKey: "stranger2",
		Tags:   [][]string{{"e", "direct", "", "reply"}, {"e", "root", "", "root"}},
	}
	membership := newAuthorMembership([]string{"aa", "bb"})
	partition := partitionThreadRepliesByWoT(
		[]nostrx.Event{directExcluded, nestedExcluded},
		"root",
		&root,
		&selected,
		nil,
		map[string]string{"nested": "direct", "direct": "selected"},
		membership,
		nil,
	)
	if len(partition.FilteredReplies) != 1 || partition.FilteredReplies[0].ID != "direct" {
		t.Fatalf("FilteredReplies = %#v, want direct only", partition.FilteredReplies)
	}
	if len(partition.TreeReplies) != 0 {
		t.Fatalf("TreeReplies = %#v, want empty trusted tree", partition.TreeReplies)
	}
}

func TestIsDirectThreadReply(t *testing.T) {
	reply := nostrx.Event{ID: "reply", Kind: nostrx.KindTextNote, Tags: [][]string{{"e", "parent", "", "reply"}, {"e", "root", "", "root"}}}
	if !isDirectThreadReply(reply, "parent", "root", nil) {
		t.Fatal("expected direct reply")
	}
	if isDirectThreadReply(reply, "root", "root", nil) {
		t.Fatal("expected non-direct reply to root")
	}
}

func TestFilteredReplyRailDepthFocusedViewMatchesVisibleDirectReplies(t *testing.T) {
	if got := filteredReplyRailDepth(true, 2, false); got != 1 {
		t.Fatalf("filteredReplyRailDepth(focused) = %d, want 1", got)
	}
	if got := filteredReplyRailDepth(true, 5, false); got != 1 {
		t.Fatalf("filteredReplyRailDepth(deep focused) = %d, want 1", got)
	}
	if got := filteredReplyRailDepth(false, 2, false); got != 2 {
		t.Fatalf("filteredReplyRailDepth(thread view) = %d, want 2", got)
	}
}
