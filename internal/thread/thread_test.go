package thread

import (
	"encoding/json"
	"strings"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

func TestBuildThreadTree(t *testing.T) {
	root := event("root", "alice", 1, nil)
	reply := event("reply", "bob", 2, [][]string{{"e", "root", "", "root"}})
	nested := event("nested", "carol", 3, [][]string{{"e", "root", "", "root"}, {"e", "reply", "", "reply"}})

	view := Build(root, []nostrx.Event{nested, reply})
	if len(view.Nodes) != 1 {
		t.Fatalf("expected one direct child, got %d", len(view.Nodes))
	}
	if view.Nodes[0].Event.ID != "reply" {
		t.Fatalf("expected reply first, got %s", view.Nodes[0].Event.ID)
	}
	if len(view.Nodes[0].Children) != 1 || view.Nodes[0].Children[0].Event.ID != "nested" {
		t.Fatalf("nested reply was not attached to parent: %#v", view.Nodes[0].Children)
	}
	if view.ReplyCount != 2 {
		t.Fatalf("expected reply count 2, got %d", view.ReplyCount)
	}
	if view.Nodes[0].ReplyCount != 1 {
		t.Fatalf("expected direct reply to have one descendant, got %d", view.Nodes[0].ReplyCount)
	}
	if view.MaxDepth != 2 {
		t.Fatalf("expected max depth 2, got %d", view.MaxDepth)
	}
}

func TestNormalizeHexEventIDLowercases64Hex(t *testing.T) {
	upper := "ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789"
	want := strings.ToLower(upper)
	if got := NormalizeHexEventID(upper); got != want {
		t.Fatalf("NormalizeHexEventID = %q, want %q", got, want)
	}
	if got := NormalizeHexEventID("not-hex"); got != "not-hex" {
		t.Fatalf("non-hex preserved: %q", got)
	}
}

// Amethyst emits 5-field e-tags: ["e", id, relay, marker, pubkey_hint].
func TestParentIDAmethystFiveFieldETags(t *testing.T) {
	raw := `{"id":"1c4d172d08e124077f7c38d2eef461fe9612661fd74cb81037877aff2b89d953","pubkey":"460c25e682fda7832b52d1f22d3d22b3176d972f60dcdc3212ed8c92ef85065c","kind":1,"tags":[["alt","x"],["e","513830fcc27659132ec96a09d0408c90c39f2dc3c64886a731c76a165515b846","wss://nostr.wine/","root","460c25e682fda7832b52d1f22d3d22b3176d972f60dcdc3212ed8c92ef85065c"],["e","2ce6ba640fb6493bdc839bfc3cfc1e5ae743aec7edaf86a3bdf7eaacba20ae3e","wss://nostr.wine/","","460c25e682fda7832b52d1f22d3d22b3176d972f60dcdc3212ed8c92ef85065c"],["e","77850b9a2dbd3c233f0b423f11ce6ffb90f8f106b9502e829e85100b835c399c","wss://relay.primal.net/","reply","036533caa872376946d4e4fdea4c1a0441eda38ca2d9d9417bb36006cbaabf58"],["p","460c25e682fda7832b52d1f22d3d22b3176d972f60dcdc3212ed8c92ef85065c"]],"content":"x","sig":"x"}`
	var ev nostrx.Event
	if err := json.Unmarshal([]byte(raw), &ev); err != nil {
		t.Fatal(err)
	}
	wantParent := "77850b9a2dbd3c233f0b423f11ce6ffb90f8f106b9502e829e85100b835c399c"
	if got := ParentID("", ev); got != wantParent {
		t.Fatalf("ParentID(\"\") = %q, want %q", got, wantParent)
	}
	wantRoot := "513830fcc27659132ec96a09d0408c90c39f2dc3c64886a731c76a165515b846"
	if got := ExplicitRootID(ev); got != wantRoot {
		t.Fatalf("ExplicitRootID = %q, want %q", got, wantRoot)
	}
	if got := RootID(ev); got != wantRoot {
		t.Fatalf("RootID = %q, want %q", got, wantRoot)
	}
}

func TestParentIDUppercaseEtagRefsMatchLowercaseRootID(t *testing.T) {
	rootHex := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	parentHex := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	reply := event("reply", "bob", 2, [][]string{
		{"e", strings.ToUpper(rootHex), "", "root"},
		{"e", strings.ToUpper(parentHex), "", "Reply"},
	})
	if got := ParentID(rootHex, reply); got != parentHex {
		t.Fatalf("ParentID = %q, want lowercased parent %q", got, parentHex)
	}
	if got := ExplicitRootID(reply); got != rootHex {
		t.Fatalf("ExplicitRootID = %q, want %q", got, rootHex)
	}
}

func TestRootAndParentID(t *testing.T) {
	reply := event("reply", "bob", 2, [][]string{
		{"e", "root", "", "root"},
		{"e", "parent", "", "reply"},
	})
	if got := RootID(reply); got != "root" {
		t.Fatalf("root id = %s, want root", got)
	}
	if got := ParentID("root", reply); got != "parent" {
		t.Fatalf("parent id = %s, want parent", got)
	}
}

func TestExplicitRootIDRequiresRootMarker(t *testing.T) {
	replyOnly := event("reply", "bob", 2, [][]string{
		{"e", "parent", "", "reply"},
	})
	if got := ExplicitRootID(replyOnly); got != "" {
		t.Fatalf("ExplicitRootID(replyOnly) = %q, want empty", got)
	}
	if got := ParentID("", replyOnly); got != "parent" {
		t.Fatalf("ParentID(\"\", replyOnly) = %q, want parent", got)
	}

	withRoot := event("nested", "carol", 3, [][]string{
		{"e", "root", "", "root"},
		{"e", "parent", "", "reply"},
	})
	if got := ExplicitRootID(withRoot); got != "root" {
		t.Fatalf("ExplicitRootID(withRoot) = %q, want root", got)
	}
}

func TestRootAndParentIDIgnoreReposts(t *testing.T) {
	repost := nostrx.Event{
		ID:        "repost",
		PubKey:    "bob",
		CreatedAt: 2,
		Kind:      nostrx.KindRepost,
		Tags: [][]string{
			{"e", "root", "", "root"},
			{"e", "parent", "", "reply"},
		},
	}
	if got := RootID(repost); got != "" {
		t.Fatalf("RootID(kind6) = %q, want empty", got)
	}
	if got := ParentID("root", repost); got != "" {
		t.Fatalf("ParentID(kind6) = %q, want empty", got)
	}
}

func TestBuildSelectedFocusModeDirectReply(t *testing.T) {
	// selected is a direct reply to root; parent context is the root.
	root := event("root", "alice", 1, nil)
	selected := event("selected", "bob", 2, [][]string{{"e", "root", "", "root"}})
	otherReply := event("other", "erin", 3, [][]string{{"e", "root", "", "root"}})
	child := event("child", "dave", 4, [][]string{{"e", "root", "", "root"}, {"e", "selected", "", "reply"}})

	view := BuildSelected(root, selected, []nostrx.Event{selected, otherReply, child})
	if !view.FocusMode {
		t.Fatalf("focus mode = false, want true")
	}
	if view.ParentNode != nil {
		t.Fatalf("parent node = %#v, want nil (root is parent)", view.ParentNode)
	}
	if view.SelectedNode == nil || view.SelectedNode.Event.ID != "selected" {
		t.Fatalf("selected node = %#v, want selected", view.SelectedNode)
	}
	if len(view.SelectedNode.Children) != 1 || view.SelectedNode.Children[0].Event.ID != "child" {
		t.Fatalf("selected node children = %#v, want [child]", view.SelectedNode.Children)
	}
	// Hidden = "other" (sibling of selected). Parent is root (rendered separately, doesn't count).
	if view.HiddenNodeCount != 1 {
		t.Fatalf("hidden count = %d, want 1", view.HiddenNodeCount)
	}
}

func TestBuildSelectedFocusModeNestedReply(t *testing.T) {
	// selected is two levels deep. Parent should be the immediate parent.
	root := event("root", "alice", 1, nil)
	parentReply := event("parent", "bob", 2, [][]string{{"e", "root", "", "root"}})
	otherTopReply := event("other-top", "erin", 3, [][]string{{"e", "root", "", "root"}})
	parentSibling := event("parent-sibling", "frank", 4, [][]string{{"e", "root", "", "root"}, {"e", "parent", "", "reply"}})
	selected := event("selected", "carol", 5, [][]string{{"e", "root", "", "root"}, {"e", "parent", "", "reply"}})
	child := event("child", "dave", 6, [][]string{{"e", "root", "", "root"}, {"e", "selected", "", "reply"}})

	view := BuildSelected(root, selected, []nostrx.Event{parentReply, otherTopReply, parentSibling, selected, child})
	if !view.FocusMode {
		t.Fatalf("focus mode = false, want true")
	}
	if view.ParentNode == nil || view.ParentNode.Event.ID != "parent" {
		t.Fatalf("parent node = %#v, want parent reply", view.ParentNode)
	}
	if view.SelectedNode == nil || view.SelectedNode.Event.ID != "selected" {
		t.Fatalf("selected node = %#v, want selected", view.SelectedNode)
	}
	if len(view.SelectedNode.Children) != 1 || view.SelectedNode.Children[0].Event.ID != "child" {
		t.Fatalf("selected children = %#v, want [child]", view.SelectedNode.Children)
	}
	// Hidden = other-top (sibling of parent), parent-sibling (sibling of selected). Parent is rendered separately so doesn't count.
	if view.HiddenNodeCount != 2 {
		t.Fatalf("hidden count = %d, want 2", view.HiddenNodeCount)
	}
}

func TestBuildSelectedFocusModeAncestorsRevealable(t *testing.T) {
	// Chain: root -> top -> middle -> parent -> selected -> child.
	// Focus shows parent above selected; "show messages above" reveals OP then
	// top then middle (everything strictly above the rendered parent).
	root := event("root", "alice", 1, nil)
	top := event("top", "bob", 2, [][]string{{"e", "root", "", "root"}})
	middle := event("middle", "carol", 3, [][]string{{"e", "root", "", "root"}, {"e", "top", "", "reply"}})
	parent := event("parent", "dave", 4, [][]string{{"e", "root", "", "root"}, {"e", "middle", "", "reply"}})
	selected := event("selected", "erin", 5, [][]string{{"e", "root", "", "root"}, {"e", "parent", "", "reply"}})
	child := event("child", "frank", 6, [][]string{{"e", "root", "", "root"}, {"e", "selected", "", "reply"}})

	view := BuildSelected(root, selected, []nostrx.Event{top, middle, parent, selected, child})
	if !view.FocusMode {
		t.Fatalf("focus mode = false, want true")
	}
	if view.ParentNode == nil || view.ParentNode.Event.ID != "parent" {
		t.Fatalf("parent node = %#v, want parent", view.ParentNode)
	}
	if len(view.HiddenAncestors) != 3 {
		t.Fatalf("hidden ancestors len = %d (%#v), want 3", len(view.HiddenAncestors), view.HiddenAncestors)
	}
	if view.HiddenAncestors[0].Event.ID != "root" || view.HiddenAncestors[1].Event.ID != "top" || view.HiddenAncestors[2].Event.ID != "middle" {
		t.Fatalf("hidden ancestors order = [%s, %s, %s], want [root, top, middle]",
			view.HiddenAncestors[0].Event.ID, view.HiddenAncestors[1].Event.ID, view.HiddenAncestors[2].Event.ID)
	}
	for i, ancestor := range view.HiddenAncestors {
		if len(ancestor.Children) != 0 {
			t.Fatalf("hidden ancestor %d has %d children, want 0 (rendered as leaf)", i, len(ancestor.Children))
		}
	}
}

func TestBuildSelectedFocusModeHiddenAncestorsIncludeRootWhenParentIsDirectReply(t *testing.T) {
	root := event("root", "alice", 1, nil)
	top := event("top", "bob", 2, [][]string{{"e", "root", "", "root"}})
	selected := event("selected", "carol", 3, [][]string{{"e", "root", "", "root"}, {"e", "top", "", "reply"}})
	view := BuildSelected(root, selected, []nostrx.Event{top, selected})
	if !view.FocusMode {
		t.Fatalf("focus mode = false, want true")
	}
	if view.ParentNode == nil || view.ParentNode.Event.ID != "top" {
		t.Fatalf("parent = %#v, want top", view.ParentNode)
	}
	if len(view.HiddenAncestors) != 1 || view.HiddenAncestors[0].Event.ID != "root" {
		t.Fatalf("hidden = %#v, want [root] only", view.HiddenAncestors)
	}
}

func TestBuildSelectedRootSelectionDisablesFocusMode(t *testing.T) {
	root := event("root", "alice", 1, nil)
	reply := event("reply", "bob", 2, [][]string{{"e", "root", "", "root"}})
	view := BuildSelected(root, root, []nostrx.Event{reply})
	if view.FocusMode {
		t.Fatalf("focus mode = true, want false for root selection")
	}
	if view.SelectedNode != nil || view.ParentNode != nil || view.HiddenNodeCount != 0 {
		t.Fatalf("unexpected focus metadata: selected=%#v parent=%#v hidden=%d", view.SelectedNode, view.ParentNode, view.HiddenNodeCount)
	}
}

func TestBuildSelectedMissingFromTreeDisablesFocusMode(t *testing.T) {
	// selected references a parent that wasn't fetched, so it can't be placed
	// in the tree. Focus mode should silently fall back to full thread view.
	root := event("root", "alice", 1, nil)
	selected := event("selected", "carol", 2, [][]string{{"e", "root", "", "root"}, {"e", "missing-parent", "", "reply"}})
	// Don't include selected in replies so it doesn't end up in the tree.
	view := BuildSelected(root, selected, []nostrx.Event{})
	if view.FocusMode {
		t.Fatalf("focus mode = true, want false when selected is not in tree")
	}
}

func event(id, pubkey string, created int64, tags [][]string) nostrx.Event {
	return nostrx.Event{
		ID:        id,
		PubKey:    pubkey,
		CreatedAt: created,
		Kind:      nostrx.KindTextNote,
		Tags:      tags,
		Content:   id,
	}
}
