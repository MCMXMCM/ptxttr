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

	fnostr "fiatjaf.com/nostr"
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
	}, nil)
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

func TestBuildThreadViewRepliesSkipsMutedSelectedAndStopsAtMutedAncestor(t *testing.T) {
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
	mutedErin := map[string]struct{}{authorPubkeyForMuteLookup(selected.PubKey): {}}
	viewRepliesMutedSelected := buildThreadViewReplies(root, selected, []nostrx.Event{child}, func(id string) *nostrx.Event {
		return lookup[id]
	}, mutedErin)
	for _, ev := range viewRepliesMutedSelected {
		if ev.ID == "selected" {
			t.Fatalf("muted selected should not appear in view replies, got %#v", viewRepliesMutedSelected)
		}
	}
	foundChild := false
	for _, ev := range viewRepliesMutedSelected {
		if ev.ID == "child" {
			foundChild = true
		}
	}
	if !foundChild {
		t.Fatalf("direct reply child should remain, got %#v", viewRepliesMutedSelected)
	}

	mutedCarol := map[string]struct{}{authorPubkeyForMuteLookup(b.PubKey): {}}
	viewRepliesMutedAncestor := buildThreadViewReplies(root, selected, []nostrx.Event{child}, func(id string) *nostrx.Event {
		return lookup[id]
	}, mutedCarol)
	for _, ev := range viewRepliesMutedAncestor {
		if ev.ID == "b" || ev.ID == "a" {
			t.Fatalf("ancestors at or above muted author should be omitted, got %#v", viewRepliesMutedAncestor)
		}
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

func TestFeedItemTemplateSkipsInlineReferencesForQuoteTags(t *testing.T) {
	srv, _ := testServer(t)
	firstID := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	secondID := "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	author := strings.Repeat("a", 64)
	quote := nostrx.Event{
		ID:        strings.Repeat("b", 64),
		PubKey:    author,
		CreatedAt: time.Now().Unix(),
		Kind:      nostrx.KindTextNote,
		Content:   "comment nostr:" + nostrx.EncodeNEvent(firstID, "") + " and nostr:" + nostrx.EncodeNEvent(secondID, ""),
		Tags: [][]string{
			{"q", firstID},
			{"q", secondID},
		},
	}
	data := FeedPageData{
		BasePageData: BasePageData{AsciiWidth: 120},
		Feed:         []nostrx.Event{quote},
		ReferencedEvents: map[string]nostrx.Event{
			firstID:  {ID: firstID, PubKey: author, CreatedAt: quote.CreatedAt - 1, Kind: nostrx.KindTextNote, Content: "first quoted note"},
			secondID: {ID: secondID, PubKey: author, CreatedAt: quote.CreatedAt - 2, Kind: nostrx.KindTextNote, Content: "second quoted note"},
		},
		ReplyCounts:     map[string]int{},
		ReactionTotals:  map[string]int{},
		ReactionViewers: map[string]string{},
		Profiles:        map[string]nostrx.Profile{},
	}
	rec := httptest.NewRecorder()
	srv.render(rec, "feed_items", data)
	body := rec.Body.String()
	if strings.Contains(body, "ascii-inline-reference-source") {
		t.Fatalf("quote-tagged references should not render duplicate inline reference templates: %s", body)
	}
	if strings.Contains(body, "note:"+short(firstID)) || strings.Contains(body, "note:"+short(secondID)) {
		t.Fatalf("quote-tagged note links should not remain in the displayed body: %s", body)
	}
	if !strings.Contains(body, "ascii-reference-source") {
		t.Fatalf("primary quoted note reference should still render: %s", body)
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

func TestThreadSelectedExpectsFocusView(t *testing.T) {
	root := testEvent("root", "alice", 1, nil)
	if threadSelectedExpectsFocusView(root) {
		t.Fatal("root note should not expect focus view")
	}
	reply := testEvent("reply", "bob", 2, [][]string{{"e", "root", "", "root"}, {"e", "root", "", "reply"}})
	if !threadSelectedExpectsFocusView(reply) {
		t.Fatal("reply marker should expect focus view")
	}
	legacy := testEvent("legacy", "carol", 3, [][]string{{"e", "root"}, {"e", "parent"}})
	if !threadSelectedExpectsFocusView(legacy) {
		t.Fatal("legacy e-tag reply should expect focus view")
	}
	comment := testEvent("comment", "dawn", 4, [][]string{{"E", "root"}, {"e", "parent"}})
	comment.Kind = nostrx.KindComment
	if !threadSelectedExpectsFocusView(comment) {
		t.Fatal("NIP-22 comment should expect focus view")
	}
}

func TestCollectThreadChainCandidatesIncludesSelectedTags(t *testing.T) {
	rootID := strings.Repeat("a", 64)
	parentID := strings.Repeat("b", 64)
	selected := testEvent("sel", "bob", 2, [][]string{{"e", parentID}, {"e", rootID, "", "root"}})
	got := collectThreadChainCandidates(rootID, selected, nil)
	seen := make(map[string]bool, len(got))
	for _, id := range got {
		seen[id] = true
	}
	if !seen[parentID] {
		t.Fatalf("expected parent from selected tags, got %v", got)
	}
}

func TestBuildThreadViewRepliesStopsWhenAncestorMissing(t *testing.T) {
	root := testEvent("root", "alice", 1, nil)
	selected := testEvent("selected", "erin", 5, [][]string{{"e", "root", "", "root"}, {"e", "missing", "", "reply"}})
	child := testEvent("child", "frank", 6, [][]string{{"e", "root", "", "root"}, {"e", "selected", "", "reply"}})

	viewReplies := buildThreadViewReplies(root, selected, []nostrx.Event{child}, func(string) *nostrx.Event {
		return nil
	}, nil)
	view := thread.BuildSelected(root, selected, viewReplies)
	if view.FocusMode {
		t.Fatalf("focus mode = true, want false when selected ancestor chain cannot be resolved")
	}
}

func TestFocusOtherReplyNodesExcludeSelectedBranchButKeepSiblingReplies(t *testing.T) {
	root := testEvent("root", "alice", 1, nil)
	parent := testEvent("parent", "bob", 2, [][]string{{"e", "root", "", "root"}})
	selected := testEvent("selected", "carol", 3, [][]string{{"e", "root", "", "root"}, {"e", "parent", "", "reply"}})
	siblingUnderParent := testEvent("sibling-under-parent", "dave", 4, [][]string{{"e", "root", "", "root"}, {"e", "parent", "", "reply"}})
	rootSibling := testEvent("root-sibling", "erin", 5, [][]string{{"e", "root", "", "root"}})

	view := thread.BuildSelected(root, selected, []nostrx.Event{parent, selected, siblingUnderParent, rootSibling})
	if !view.FocusMode {
		t.Fatal("focus mode = false, want true")
	}
	other := focusOtherReplyNodesFromView(view)
	if len(other) != 2 {
		t.Fatalf("len(other) = %d, want 2; nodes=%#v", len(other), other)
	}
	if other[0].Event.ID != "parent" || len(other[0].Children) != 1 || other[0].Children[0].Event.ID != "sibling-under-parent" {
		t.Fatalf("parent branch = %#v, want parent with sibling-under-parent child", other[0])
	}
	if other[1].Event.ID != "root-sibling" {
		t.Fatalf("other[1] = %q, want root-sibling", other[1].Event.ID)
	}
}

func TestLinearThreadReplyNodesFocusShowsOnlySelectedDescendants(t *testing.T) {
	root := testEvent("root", "alice", 1, nil)
	selected := testEvent("selected", "bob", 2, [][]string{{"e", "root", "", "root"}})
	selectedChild := testEvent("selected-child", "carol", 3, [][]string{{"e", "root", "", "root"}, {"e", "selected", "", "reply"}})
	selectedGrandchild := testEvent("selected-grandchild", "dave", 4, [][]string{{"e", "root", "", "root"}, {"e", "selected-child", "", "reply"}})
	rootSibling := testEvent("root-sibling", "erin", 5, [][]string{{"e", "root", "", "root"}})
	rootSiblingChild := testEvent("root-sibling-child", "frank", 6, [][]string{{"e", "root", "", "root"}, {"e", "root-sibling", "", "reply"}})

	view := thread.BuildSelected(root, selected, []nostrx.Event{selected, selectedChild, selectedGrandchild, rootSibling, rootSiblingChild})
	if !view.FocusMode {
		t.Fatal("focus mode = false, want true")
	}
	other := linearThreadOtherReplyNodes(view)
	linear := linearThreadReplyNodes(view)
	if len(linear) != 1 || linear[0].Event.ID != "selected-child" {
		t.Fatalf("linear focus replies = %#v, want selected child only", linear)
	}
	if len(linear[0].Children) != 0 {
		t.Fatalf("linear child has %d children, want one-depth projection", len(linear[0].Children))
	}
	if len(other) != 1 || other[0].Event.ID != "root-sibling" {
		t.Fatalf("other focus replies = %#v, want root sibling separately", other)
	}
	if len(other[0].Children) != 0 {
		t.Fatalf("other reply has %d children, want one-depth projection", len(other[0].Children))
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
	author := strings.Repeat("a", 64)
	saveLongFormRead(t, st, readID, author)
	allowAnonymousAuthors(t, st, author)
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

func TestHandleThreadLongFormWithBackReadDoesNotRedirect(t *testing.T) {
	srv, st := testServer(t)
	readID := strings.Repeat("d", 64)
	author := strings.Repeat("c", 64)
	saveLongFormRead(t, st, readID, author)
	allowAnonymousAuthors(t, st, author)
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
	if !strings.Contains(body, `data-route-outlet`) {
		t.Fatalf("expected thread shell markup, got:\n%s", truncateForLog(body, 800))
	}
}

func TestHandleThreadAppShellCarriesNoteOGMeta(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	noteID := strings.Repeat("7", 64)
	author := strings.Repeat("8", 64)
	imageURL := "https://cdn.example.com/note-photo.jpg"
	content := "look at this\n" + imageURL
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        noteID,
		PubKey:    author,
		Kind:      nostrx.KindTextNote,
		CreatedAt: 1700000000,
		Content:   content,
		Sig:       "sig",
	}); err != nil {
		t.Fatal(err)
	}
	allowAnonymousAuthors(t, st, author)

	req := httptest.NewRequest(http.MethodGet, "/thread/"+noteID, nil)
	req.Host = "example.test"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `id="thread-focus"`) || !strings.Contains(body, "data-thread-view-toggle") {
		t.Fatalf("expected SSR thread markup, got:\n%s", truncateForLog(body, 800))
	}
	if !strings.Contains(body, imageURL) {
		t.Fatalf("expected note image metadata/body in SSR document, got:\n%s", truncateForLog(body, 1200))
	}
	if !strings.Contains(body, `<script id="ptxt-route-context"`) || !strings.Contains(body, `"route":"thread"`) {
		t.Fatalf("expected route context in SSR thread document, got:\n%s", truncateForLog(body, 1200))
	}
}

func TestHandleThreadBrowserDocumentReturnsAuthoritativeSSR(t *testing.T) {
	srv, st := testServer(t)
	noteID := strings.Repeat("9", 64)
	if err := st.SaveEvent(context.Background(), nostrx.Event{
		ID: noteID, PubKey: strings.Repeat("8", 64), CreatedAt: time.Now().Unix(), Kind: nostrx.KindTextNote, Content: "authoritative guest thread",
	}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/thread/"+noteID, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `id="thread-focus"`) || !strings.Contains(body, "authoritative guest thread") {
		t.Fatalf("expected authoritative SSR thread document, got:\n%s", truncateForLog(body, 1200))
	}
	if strings.Contains(body, `data-thread-route-pending`) || strings.Contains(body, `thread-telemetry-loader`) {
		t.Fatalf("guest document must not depend on a pending hydrate shell, got:\n%s", truncateForLog(body, 1200))
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
	allowAnonymousAuthors(t, st, pkRoot, pkSel)
	req := httptest.NewRequest(http.MethodGet, "/thread/"+selID, nil)
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `data-route-outlet`) {
		t.Fatalf("expected app shell route outlet, got body:\n%s", truncateForLog(body, 1200))
	}
	if !strings.Contains(body, `"path":"/thread/`+selID+`"`) {
		t.Fatalf("expected thread path route context, got body:\n%s", truncateForLog(body, 1200))
	}
	if !strings.Contains(body, "/og/"+selID+".png") {
		t.Fatalf("expected SSR thread document with server OG body markup:\n%s", truncateForLog(body, 1200))
	}
}

func TestHandleThreadFetchesMissingDirectParentFromIndexerRelays(t *testing.T) {
	srv, st := newTestServer(t, testServerOptions{relayTimeout: 50 * time.Millisecond})
	ctx := context.Background()

	parentExternal := fnostr.Event{
		CreatedAt: fnostr.Timestamp(1700000000),
		Kind:      fnostr.Kind(nostrx.KindTextNote),
		Content:   "parent from indexer relay",
	}
	if err := parentExternal.Sign(fnostr.Generate()); err != nil {
		t.Fatalf("Sign() parent error = %v", err)
	}
	parent := fnostrToNostrxEvent(parentExternal)
	selected := nostrx.Event{
		ID:        strings.Repeat("b", 64),
		PubKey:    strings.Repeat("2", 64),
		Kind:      nostrx.KindTextNote,
		CreatedAt: 1700000001,
		Content:   "selected reply",
		Sig:       "sig",
		Tags: [][]string{
			{"e", parent.ID, "", "reply"},
		},
	}
	if err := st.SaveEvent(ctx, selected); err != nil {
		t.Fatal(err)
	}

	relay := newTestRelayREQEventWhenIDsContain(ctx, parent.ID, parentExternal)
	defer relay.Close()
	srv.cfg.IndexerRelays = []string{wsURL(relay.URL)}

	req := httptest.NewRequest(http.MethodGet, "/thread/"+selected.ID, nil)
	markTestRequestLoggedIn(req)
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `data-route-outlet`) {
		t.Fatalf("expected app shell route outlet, got body:\n%s", truncateForLog(body, 1200))
	}
}

func TestHandleThreadSelectedQueryUsesRootPathWithFocusedReply(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	rootID := strings.Repeat("a", 64)
	selectedID := strings.Repeat("b", 64)
	pk := strings.Repeat("1", 64)
	root := nostrx.Event{
		ID:        rootID,
		PubKey:    pk,
		Kind:      nostrx.KindTextNote,
		CreatedAt: 1000,
		Content:   "root",
		Sig:       "sig",
	}
	selected := nostrx.Event{
		ID:        selectedID,
		PubKey:    pk,
		Kind:      nostrx.KindTextNote,
		CreatedAt: 1001,
		Content:   "reply",
		Sig:       "sig",
		Tags: [][]string{
			{"e", rootID, "", "root"},
			{"e", rootID, "", "reply"},
		},
	}
	for _, ev := range []nostrx.Event{root, selected} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	allowAnonymousAuthors(t, st, pk)

	req := httptest.NewRequest(http.MethodGet, "/thread/"+rootID+"?selected="+selectedID, nil)
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"path":"/thread/`+rootID+`"`) {
		t.Fatalf("expected root thread path in route context, got body:\n%s", truncateForLog(body, 1200))
	}
	if !strings.Contains(body, `selected=`+selectedID) {
		t.Fatalf("expected selected reply id in route context search, got body:\n%s", truncateForLog(body, 1200))
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
