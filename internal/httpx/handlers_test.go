package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
	"ptxt-nstr/internal/thread"
)

func saveLongFormRead(t *testing.T, st *store.Store, readID, pubkey string) {
	t.Helper()
	ctx := context.Background()
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        readID,
		PubKey:    pubkey,
		CreatedAt: time.Now().Unix(),
		Kind:      nostrx.KindLongForm,
		Content:   "# Title\n\nHello",
		Tags:      [][]string{{"title", "Test Read"}},
		Sig:       "00",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestThreadParticipantsCountsSortAndCap(t *testing.T) {
	events := []nostrx.Event{
		{ID: "root", PubKey: "a", CreatedAt: 10},
		{ID: "selected", PubKey: "b", CreatedAt: 12},
		{ID: "r1", PubKey: "b", CreatedAt: 13},
		{ID: "r2", PubKey: "c", CreatedAt: 9},
		{ID: "r3", PubKey: "c", CreatedAt: 14},
		{ID: "r4", PubKey: "d", CreatedAt: 15},
		{ID: "r5", PubKey: "e", CreatedAt: 16},
		{ID: "r6", PubKey: "f", CreatedAt: 17},
		{ID: "r7", PubKey: "g", CreatedAt: 18},
		{ID: "r8", PubKey: "h", CreatedAt: 19},
		{ID: "r9", PubKey: "i", CreatedAt: 20},
		{ID: "r10", PubKey: "j", CreatedAt: 21},
		{ID: "r11", PubKey: "k", CreatedAt: 22},
		{ID: "r12", PubKey: "l", CreatedAt: 23},
		// Duplicate event ID should not be double-counted.
		{ID: "r2", PubKey: "c", CreatedAt: 24},
	}
	profiles := map[string]nostrx.Profile{
		"a": {PubKey: "a", Name: "Alice"},
		"b": {PubKey: "b", Name: "Bob"},
		"c": {PubKey: "c", Name: "Carol"},
	}

	participants := threadParticipants(events, profiles, 8)
	if len(participants) != 8 {
		t.Fatalf("len(threadParticipants()) = %d, want 8", len(participants))
	}
	if participants[0].PubKey != "c" || participants[0].Posts != 2 {
		t.Fatalf("participants[0] = %#v, want c with 2 posts", participants[0])
	}
	if participants[1].PubKey != "b" || participants[1].Posts != 2 {
		t.Fatalf("participants[1] = %#v, want b with 2 posts", participants[1])
	}
	if participants[2].PubKey != "a" || participants[2].Posts != 1 {
		t.Fatalf("participants[2] = %#v, want a with 1 post", participants[2])
	}
	if participants[0].Profile.Name != "Carol" {
		t.Fatalf("participants[0] profile = %#v, want Carol profile hydrated", participants[0].Profile)
	}
}

func TestBuildThreadViewRepliesIncludesAncestorChainForDeepSelection(t *testing.T) {
	root := testEvent("root", "alice", 1, nil)
	a := testEvent("a", "bob", 2, [][]string{{"e", "root", "", "root"}})
	b := testEvent("b", "carol", 3, [][]string{{"e", "root", "", "root"}, {"e", "a", "", "reply"}})
	c := testEvent("c", "dave", 4, [][]string{{"e", "root", "", "root"}, {"e", "b", "", "reply"}})
	selected := testEvent("selected", "erin", 5, [][]string{{"e", "root", "", "root"}, {"e", "c", "", "reply"}})
	child := testEvent("child", "frank", 6, [][]string{{"e", "root", "", "root"}, {"e", "selected", "", "reply"}})
	lookup := map[string]*nostrx.Event{
		"a": &a,
		"b": &b,
		"c": &c,
	}

	viewReplies := buildThreadViewReplies(root, selected, []nostrx.Event{child}, func(id string) *nostrx.Event {
		return lookup[id]
	})
	view := thread.BuildSelected(root, selected, viewReplies)
	if !view.FocusMode {
		t.Fatalf("focus mode = false, want true for deep selected reply")
	}
	if view.ParentNode == nil || view.ParentNode.Event.ID != "c" {
		t.Fatalf("parent node = %#v, want c", view.ParentNode)
	}
	if view.SelectedNode == nil || view.SelectedNode.Event.ID != "selected" {
		t.Fatalf("selected node = %#v, want selected", view.SelectedNode)
	}
	if len(view.SelectedNode.Children) != 1 || view.SelectedNode.Children[0].Event.ID != "child" {
		t.Fatalf("selected children = %#v, want [child]", view.SelectedNode.Children)
	}
}

func TestMergeThreadReplyPagesDedupesAndSorts(t *testing.T) {
	a := testEvent("a", "p", 10, nil)
	b := testEvent("b", "p", 20, nil)
	c := testEvent("c", "p", 30, nil)
	got := mergeThreadReplyPages([]nostrx.Event{a, b}, []nostrx.Event{b, c})
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].ID != "a" || got[1].ID != "b" || got[2].ID != "c" {
		t.Fatalf("ids = %v %v %v, want a b c", got[0].ID, got[1].ID, got[2].ID)
	}
}

func TestLinearFirstPageFromFullRepliesMatchesShallowMerge(t *testing.T) {
	root := testEvent("root", "alice", 1, nil)
	a := testEvent("a", "bob", 2, [][]string{{"e", "root", "", "root"}})
	b := testEvent("b", "carol", 3, [][]string{{"e", "root", "", "root"}, {"e", "a", "", "reply"}})
	c := testEvent("c", "dave", 4, [][]string{{"e", "root", "", "root"}, {"e", "root", "", "reply"}})
	full := []nostrx.Event{a, b, c}
	got, _, _, _ := linearFirstPageFromFullReplies(full, root, root)
	if len(got) != 2 {
		t.Fatalf("OP first page len = %d, want 2 (direct to root only)", len(got))
	}
	if got[0].ID != "a" || got[1].ID != "c" {
		t.Fatalf("got ids %v %v, want a c (nested b excluded)", got[0].ID, got[1].ID)
	}
}

func TestBuildThreadTreeDataFromRepliesMatchesBuildSelectedForOP(t *testing.T) {
	root := testEvent("root", "alice", 1, nil)
	a := testEvent("a", "bob", 2, [][]string{{"e", "root", "", "root"}})
	b := testEvent("b", "carol", 3, [][]string{{"e", "root", "", "root"}, {"e", "a", "", "reply"}})
	replies := []nostrx.Event{a, b}
	td := buildThreadTreeDataFromReplies(root, replies)
	view := thread.BuildSelected(root, root, replies)
	if len(td.Nodes) != len(view.Nodes) {
		t.Fatalf("tree node count %d vs linear view %d", len(td.Nodes), len(view.Nodes))
	}
	for i := range td.Nodes {
		if td.Nodes[i].Event.ID != view.Nodes[i].Event.ID {
			t.Fatalf("node %d: tree %q vs view %q", i, td.Nodes[i].Event.ID, view.Nodes[i].Event.ID)
		}
	}
}

func TestBuildThreadViewRepliesStopsWhenAncestorMissing(t *testing.T) {
	root := testEvent("root", "alice", 1, nil)
	selected := testEvent("selected", "erin", 5, [][]string{{"e", "root", "", "root"}, {"e", "missing", "", "reply"}})
	child := testEvent("child", "frank", 6, [][]string{{"e", "root", "", "root"}, {"e", "selected", "", "reply"}})

	viewReplies := buildThreadViewReplies(root, selected, []nostrx.Event{child}, func(string) *nostrx.Event {
		return nil
	})
	view := thread.BuildSelected(root, selected, viewReplies)
	if view.FocusMode {
		t.Fatalf("focus mode = true, want false when selected ancestor chain cannot be resolved")
	}
}

func TestResolveThreadRootIDWalksAncestorChainWithoutExplicitRootOnSelected(t *testing.T) {
	root := testEvent("root", "alice", 1, nil)
	a := testEvent("a", "bob", 2, [][]string{{"e", "root", "", "root"}})
	b := testEvent("b", "carol", 3, [][]string{{"e", "a", "", "reply"}})
	selected := testEvent("selected", "dave", 4, [][]string{{"e", "b", "", "reply"}})

	lookup := map[string]*nostrx.Event{
		"a": &a,
		"b": &b,
	}
	if got := resolveThreadRootID(selected, func(id string) *nostrx.Event {
		return lookup[id]
	}); got != root.ID {
		t.Fatalf("resolveThreadRootID() = %q, want %q", got, root.ID)
	}
}

func TestHandleThreadRedirectsLongFormToReads(t *testing.T) {
	srv, st := testServer(t)
	readID := strings.Repeat("e", 64)
	saveLongFormRead(t, st, readID, strings.Repeat("a", 64))
	req := httptest.NewRequest(http.MethodGet, "/thread/"+readID, nil)
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)
	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/reads/"+readID {
		t.Fatalf("Location = %q, want /reads/%s", loc, readID)
	}
}

func TestHandleThreadLongFormHydrateSendsNavigateHeader(t *testing.T) {
	srv, st := testServer(t)
	readID := strings.Repeat("f", 64)
	saveLongFormRead(t, st, readID, strings.Repeat("b", 64))
	req := httptest.NewRequest(http.MethodGet, "/thread/"+readID+"?fragment=hydrate", nil)
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rr.Code)
	}
	if got := rr.Header().Get("X-Ptxt-Navigate"); got != "/reads/"+readID {
		t.Fatalf("X-Ptxt-Navigate = %q, want /reads/%s", got, readID)
	}
}

func TestHandleThreadLongFormWithBackReadDoesNotRedirect(t *testing.T) {
	srv, st := testServer(t)
	readID := strings.Repeat("d", 64)
	saveLongFormRead(t, st, readID, strings.Repeat("c", 64))
	req := httptest.NewRequest(http.MethodGet, "/thread/"+readID+"?back_read="+readID, nil)
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "" {
		t.Fatalf("unexpected Location %q", loc)
	}
	if nav := rr.Header().Get("X-Ptxt-Navigate"); nav != "" {
		t.Fatalf("unexpected X-Ptxt-Navigate %q", nav)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "thread-header") {
		t.Fatalf("expected thread shell markup, got:\n%s", truncateForLog(body, 800))
	}
}

// When the parent chain stops on a missing note, handleThread must still
// anchor the tree on the NIP-10 "root" tag if that event is in the store.
func TestHandleThreadUsesExplicitRootWhenParentChainMissing(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	fullRoot := strings.Repeat("a", 64)
	missingMid := strings.Repeat("b", 64)
	selID := strings.Repeat("c", 64)
	pkRoot := strings.Repeat("1", 64)
	pkSel := strings.Repeat("2", 64)
	rootEv := nostrx.Event{
		ID:        fullRoot,
		PubKey:    pkRoot,
		Kind:      nostrx.KindTextNote,
		CreatedAt: 1700000000,
		Content:   "conversation root",
		Sig:       "sig",
	}
	selEv := nostrx.Event{
		ID:        selID,
		PubKey:    pkSel,
		Kind:      nostrx.KindTextNote,
		CreatedAt: 1700000001,
		Content:   "reply text",
		Sig:       "sig",
		Tags: [][]string{
			{"e", fullRoot, "wss://example.invalid/", "root", pkRoot},
			{"e", missingMid, "wss://example.invalid/", "reply", pkSel},
		},
	}
	if err := st.SaveEvent(ctx, rootEv); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveEvent(ctx, selEv); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/thread/"+selID, nil)
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "#note-"+fullRoot) {
		t.Fatalf("expected root link to explicit thread root id, got body:\n%s", truncateForLog(body, 1200))
	}
	if !strings.Contains(body, "/og/"+selID+".png") {
		t.Fatalf("expected OpenGraph image for selected note id:\n%s", truncateForLog(body, 1200))
	}
}

func TestResolveThreadRootIDOverridesBogusExplicitRootOnSelected(t *testing.T) {
	root := testEvent("root", "alice", 1, nil)
	a := testEvent("a", "bob", 2, [][]string{{"e", "root", "", "root"}})
	b := testEvent("b", "carol", 3, [][]string{{"e", "root", "", "root"}, {"e", "a", "", "reply"}})
	selected := testEvent("selected", "dave", 4, [][]string{{"e", "b", "", "root"}})

	lookup := map[string]*nostrx.Event{
		"a": &a,
		"b": &b,
	}
	if got := resolveThreadRootID(selected, func(id string) *nostrx.Event {
		return lookup[id]
	}); got != root.ID {
		t.Fatalf("resolveThreadRootID() = %q, want %q", got, root.ID)
	}
}

func testEvent(id, pubkey string, created int64, tags [][]string) nostrx.Event {
	return nostrx.Event{
		ID:        id,
		PubKey:    pubkey,
		CreatedAt: created,
		Kind:      nostrx.KindTextNote,
		Tags:      tags,
		Content:   id,
	}
}
