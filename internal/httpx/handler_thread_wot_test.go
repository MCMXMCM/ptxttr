package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ptxt-nstr/internal/nostrx"
)

func TestHandlePersonalizedThreadDocumentAppliesWoTFiltering(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	viewerPub := strings.Repeat("a", 64)
	strangerPub := strings.Repeat("b", 64)
	root := nostrx.Event{
		ID:        strings.Repeat("c", 64),
		PubKey:    viewerPub,
		CreatedAt: 100,
		Kind:      nostrx.KindTextNote,
		Content:   "root",
	}
	trustedReply := nostrx.Event{
		ID:        strings.Repeat("d", 64),
		PubKey:    viewerPub,
		CreatedAt: 101,
		Kind:      nostrx.KindTextNote,
		Content:   "trusted",
		Tags:      [][]string{{"e", root.ID, "", "root"}, {"e", root.ID, "", "reply"}, {"p", viewerPub}},
	}
	filteredReply := nostrx.Event{
		ID:        strings.Repeat("e", 64),
		PubKey:    strangerPub,
		CreatedAt: 102,
		Kind:      nostrx.KindTextNote,
		Content:   "stranger",
		Tags:      [][]string{{"e", root.ID, "", "root"}, {"e", root.ID, "", "reply"}, {"p", viewerPub}},
	}
	for _, event := range []nostrx.Event{root, trustedReply, filteredReply} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/thread/"+root.ID, nil)
	req.Header.Set(headerViewerPubkey, nostrx.EncodeNPub(viewerPub))
	req.Header.Set(headerWotEnabled, "1")
	req.Header.Set(headerWotDepth, "1")
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, truncateForLog(rr.Body.String(), 600))
	}
	body := rr.Body.String()
	if !strings.Contains(body, "data-thread-filtered-replies") {
		t.Fatalf("personalized thread document did not render filtered-replies disclosure:\n%s", truncateForLog(body, 1200))
	}
	if !strings.Contains(body, `data-route-outlet`) {
		t.Fatalf("expected app shell route outlet in thread body")
	}
}

func TestHandleThreadHydrateAppliesWoTFilteringForPersonalizedRequest(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	viewerPub := strings.Repeat("a", 64)
	strangerPub := strings.Repeat("b", 64)
	root := nostrx.Event{
		ID:        strings.Repeat("c", 64),
		PubKey:    viewerPub,
		CreatedAt: 100,
		Kind:      nostrx.KindTextNote,
		Content:   "root",
	}
	reply := nostrx.Event{
		ID:        strings.Repeat("d", 64),
		PubKey:    strangerPub,
		CreatedAt: 101,
		Kind:      nostrx.KindTextNote,
		Content:   "visible before trust filtering",
		Tags:      [][]string{{"e", root.ID, "", "root"}, {"e", root.ID, "", "reply"}, {"p", viewerPub}},
	}
	for _, event := range []nostrx.Event{root, reply} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/thread/"+root.ID+"?fragment=hydrate", nil)
	req.Header.Set(headerViewerPubkey, nostrx.EncodeNPub(viewerPub))
	req.Header.Set(headerWotEnabled, "1")
	req.Header.Set(headerWotDepth, "2")
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, truncateForLog(rr.Body.String(), 600))
	}
	body := rr.Body.String()
	if strings.Contains(body, `data-thread-wot-deferred="1"`) {
		t.Fatalf("personalized hydrate unexpectedly deferred WoT filtering:\n%s", truncateForLog(body, 1200))
	}
	if strings.Contains(body, "visible before trust filtering") && !strings.Contains(body, `data-thread-filtered-replies`) {
		t.Fatalf("personalized hydrate rendered an untrusted reply without disclosure:\n%s", truncateForLog(body, 1200))
	}
	if !strings.Contains(body, `data-thread-filtered-replies`) {
		t.Fatalf("personalized hydrate did not render the filtered-replies disclosure:\n%s", truncateForLog(body, 1200))
	}
}

func TestHandleThreadTreeRendersFilteredReplyDisclosure(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	viewerPub := strings.Repeat("a", 64)
	strangerPub := strings.Repeat("b", 64)
	root := nostrx.Event{
		ID: strings.Repeat("c", 64), PubKey: viewerPub, CreatedAt: 100,
		Kind: nostrx.KindTextNote, Content: "tree disclosure root",
	}
	filteredReply := nostrx.Event{
		ID: strings.Repeat("d", 64), PubKey: strangerPub, CreatedAt: 101,
		Kind: nostrx.KindTextNote, Content: "tree reply outside graph",
		Tags: [][]string{{"e", root.ID, "", "root"}, {"e", root.ID, "", "reply"}, {"p", viewerPub}},
	}
	for _, event := range []nostrx.Event{root, filteredReply} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/thread/"+root.ID+"?fragment=tree", nil)
	req.Header.Set(headerViewerPubkey, nostrx.EncodeNPub(viewerPub))
	req.Header.Set(headerWotEnabled, "1")
	req.Header.Set(headerWotDepth, "1")
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, truncateForLog(rr.Body.String(), 1000))
	}
	body := rr.Body.String()
	filteredStart := strings.Index(body, "data-thread-tree-filtered-replies")
	if filteredStart < 0 {
		t.Fatalf("tree fragment missing filtered reply block: %s", truncateForLog(body, 1800))
	}
	if !strings.Contains(body[filteredStart:], filteredReply.Content) {
		t.Fatalf("filtered tree block missing reply: %s", truncateForLog(body, 1800))
	}
	if !strings.Contains(body, "data-thread-tree-filtered-replies-toggle") || !strings.Contains(body, "show 1 more") {
		t.Fatalf("tree fragment missing show-more control: %s", truncateForLog(body, 1800))
	}
}

func TestAnonymousThreadTreeKeepsOutOfScopeParentOfInScopeReply(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	root := nostrx.Event{ID: strings.Repeat("a", 64), PubKey: strings.Repeat("1", 64), Kind: nostrx.KindTextNote, CreatedAt: 100, Content: "thread root", Sig: "sig"}
	bridgeParent := nostrx.Event{ID: strings.Repeat("b", 64), PubKey: strings.Repeat("2", 64), Kind: nostrx.KindTextNote, CreatedAt: 101, Content: "outside parent must remain as context", Tags: [][]string{
		{"e", root.ID, "", "root"},
		{"e", root.ID, "", "reply"},
		{"p", root.PubKey},
	}, Sig: "sig"}
	trustedChild := nostrx.Event{ID: strings.Repeat("c", 64), PubKey: strings.Repeat("3", 64), Kind: nostrx.KindTextNote, CreatedAt: 102, Content: "followed author reply", Tags: [][]string{
		{"e", root.ID, "", "root"},
		{"e", bridgeParent.ID, "", "reply"},
		{"p", root.PubKey},
		{"p", bridgeParent.PubKey},
	}, Sig: "sig"}
	for _, event := range []nostrx.Event{root, bridgeParent, trustedChild} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	allowAnonymousAuthors(t, st, root.PubKey, trustedChild.PubKey)

	req := httptest.NewRequest(http.MethodGet, "/thread/"+root.ID+"?fragment=tree", nil)
	req.Header.Set(headerWotEnabled, "1")
	req.Header.Set(headerWotDepth, "1")
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, truncateForLog(rr.Body.String(), 800))
	}
	body := rr.Body.String()
	parentPos := strings.Index(body, bridgeParent.Content)
	childPos := strings.Index(body, trustedChild.Content)
	if parentPos < 0 || childPos < 0 {
		t.Fatalf("tree omitted parent or trusted child: parent=%d child=%d body=%s", parentPos, childPos, truncateForLog(body, 1800))
	}
	if parentPos >= childPos {
		t.Fatalf("trusted child rendered before its parent: parent=%d child=%d", parentPos, childPos)
	}
	parentNodeMarker := `data-thread-tree-note="note-` + bridgeParent.ID + `"`
	if strings.Count(body, parentNodeMarker) != 1 {
		t.Fatalf("bridge parent rendered more than once: %s", truncateForLog(body, 1800))
	}

	// The visible bridge parent must remain focusable. Otherwise tapping the
	// parent shown above a trusted reply lands on an anonymous-scope 404.
	focusReq := httptest.NewRequest(http.MethodGet, "/thread/"+root.ID+"?selected="+bridgeParent.ID+"&fragment=hydrate", nil)
	focusReq.Header.Set(headerWotEnabled, "1")
	focusReq.Header.Set(headerWotDepth, "1")
	focusRR := httptest.NewRecorder()
	srv.handleThread(focusRR, focusReq)
	if focusRR.Code != http.StatusOK {
		t.Fatalf("bridge focus status = %d, want 200; body=%s", focusRR.Code, truncateForLog(focusRR.Body.String(), 1000))
	}
	focusBody := focusRR.Body.String()
	parentPos = strings.Index(focusBody, bridgeParent.Content)
	childPos = strings.Index(focusBody, trustedChild.Content)
	if parentPos < 0 || childPos < 0 || parentPos >= childPos {
		t.Fatalf("focused bridge did not preserve parent before trusted child: parent=%d child=%d body=%s", parentPos, childPos, truncateForLog(focusBody, 1800))
	}
}

func TestAnonymousThreadFocusesExplicitCachedNoteOutsideWoT(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	root := nostrx.Event{ID: strings.Repeat("a", 64), PubKey: strings.Repeat("1", 64), Kind: nostrx.KindTextNote, CreatedAt: 100, Content: "trusted root", Sig: "sig"}
	untrusted := nostrx.Event{ID: strings.Repeat("b", 64), PubKey: strings.Repeat("2", 64), Kind: nostrx.KindTextNote, CreatedAt: 101, Content: "untrusted direct reply", Tags: [][]string{{"e", root.ID, "", "root"}, {"e", root.ID, "", "reply"}}, Sig: "sig"}
	for _, event := range []nostrx.Event{root, untrusted} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	allowAnonymousAuthors(t, st, root.PubKey)

	req := httptest.NewRequest(http.MethodGet, "/thread/"+root.ID+"?selected="+untrusted.ID+"&fragment=hydrate", nil)
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, truncateForLog(rr.Body.String(), 1000))
	}
	body := rr.Body.String()
	rootPos := strings.Index(body, root.Content)
	selectedPos := strings.Index(body, untrusted.Content)
	if rootPos < 0 || selectedPos < 0 || rootPos >= selectedPos {
		t.Fatalf("explicit cached selection did not render with root context: root=%d selected=%d body=%s", rootPos, selectedPos, truncateForLog(body, 1600))
	}
}

func TestGuestThreadUsesThreeHopWoTAndHidesFourthHopReply(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	seed, err := nostrx.DecodeIdentifier(defaultLoggedOutWOTSeedNPub)
	if err != nil || seed == "" {
		t.Fatalf("decode default seed: %v", err)
	}
	hop1 := strings.Repeat("1", 64)
	hop2 := strings.Repeat("2", 64)
	hop3 := strings.Repeat("3", 64)
	hop4 := strings.Repeat("4", 64)
	saveTestFollowList(t, st, seed, []string{hop1}, 100)
	saveTestFollowList(t, st, hop1, []string{hop2}, 101)
	saveTestFollowList(t, st, hop2, []string{hop3}, 102)
	saveTestFollowList(t, st, hop3, []string{hop4}, 103)

	root := nostrx.Event{ID: strings.Repeat("a", 64), PubKey: hop1, Kind: nostrx.KindTextNote, CreatedAt: 200, Content: "one-hop root", Sig: "sig"}
	reply2 := nostrx.Event{ID: strings.Repeat("b", 64), PubKey: hop2, Kind: nostrx.KindTextNote, CreatedAt: 201, Content: "two-hop visible reply", Tags: [][]string{{"e", root.ID, "", "root"}, {"e", root.ID, "", "reply"}}, Sig: "sig"}
	reply3 := nostrx.Event{ID: strings.Repeat("c", 64), PubKey: hop3, Kind: nostrx.KindTextNote, CreatedAt: 202, Content: "three-hop visible reply", Tags: [][]string{{"e", root.ID, "", "root"}, {"e", root.ID, "", "reply"}}, Sig: "sig"}
	reply4 := nostrx.Event{ID: strings.Repeat("d", 64), PubKey: hop4, Kind: nostrx.KindTextNote, CreatedAt: 203, Content: "four-hop hidden reply", Tags: [][]string{{"e", root.ID, "", "root"}, {"e", root.ID, "", "reply"}}, Sig: "sig"}
	for _, event := range []nostrx.Event{root, reply2, reply3, reply4} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	if err := srv.refreshDefaultLoggedOutAuthorMemberships(ctx); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/thread/"+root.ID+"?fragment=hydrate", nil)
	// The guest transport remains feed-aligned at one hop; the thread handler
	// must apply its own fixed three-hop policy instead of trusting this header.
	req.Header.Set(headerWotEnabled, "1")
	req.Header.Set(headerWotDepth, "1")
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, truncateForLog(rr.Body.String(), 1200))
	}
	body := rr.Body.String()
	filteredStart := strings.Index(body, "data-thread-filtered-replies")
	if filteredStart < 0 {
		t.Fatalf("missing filtered reply block: %s", truncateForLog(body, 2000))
	}
	visible := body[:filteredStart]
	for _, content := range []string{reply2.Content, reply3.Content} {
		if !strings.Contains(visible, content) {
			t.Fatalf("trusted reply %q missing before show-more: %s", content, truncateForLog(body, 2000))
		}
	}
	if strings.Contains(visible, reply4.Content) || !strings.Contains(body[filteredStart:], reply4.Content) {
		t.Fatalf("fourth-hop reply was not isolated behind show-more: %s", truncateForLog(body, 2400))
	}
	if !strings.Contains(body, "show 1 more") {
		t.Fatalf("missing show-more label: %s", truncateForLog(body, 2000))
	}
	collapsedParticipantsStart := strings.Index(body, "data-thread-collapsed-participants")
	expandedParticipantsStart := strings.Index(body, "data-thread-expanded-participants")
	if collapsedParticipantsStart < 0 || expandedParticipantsStart <= collapsedParticipantsStart {
		t.Fatalf("missing collapsed or expanded participant lists: %s", truncateForLog(body, 2400))
	}
	collapsedParticipants := body[collapsedParticipantsStart:expandedParticipantsStart]
	expandedParticipants := body[expandedParticipantsStart:]
	hop4ProfileHref := `href="/u/` + hop4 + `"`
	if strings.Contains(collapsedParticipants, hop4ProfileHref) {
		t.Fatalf("hidden reply author appeared before disclosure: %s", truncateForLog(collapsedParticipants, 1200))
	}
	if !strings.Contains(expandedParticipants, hop4ProfileHref) {
		t.Fatalf("expanded participant list missing hidden reply author: %s", truncateForLog(expandedParticipants, 1600))
	}
	counters := metricCountersSnapshot(srv)
	if counters["authors.cache_miss"] != 0 || counters["authors.cache_hit"] != 0 || counters["authors.durable_cache_hit"] != 0 {
		t.Fatalf("guest thread triggered general author resolution: %#v", counters)
	}
}

func TestGuestThreadFragmentsDoNotContactRelaysOrScheduleRelayWarm(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	root := nostrx.Event{ID: strings.Repeat("a", 64), PubKey: strings.Repeat("1", 64), Kind: nostrx.KindTextNote, CreatedAt: 100, Content: "cache-only guest thread", Sig: "sig"}
	if err := st.SaveEvent(ctx, root); err != nil {
		t.Fatal(err)
	}

	var relayRequests atomic.Int64
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		relayRequests.Add(1)
		http.Error(w, "guest request reached relay", http.StatusServiceUnavailable)
	}))
	defer relay.Close()

	for _, fragment := range []string{"hydrate", "replies"} {
		req := httptest.NewRequest(http.MethodGet, "/thread/"+root.ID+"?fragment="+fragment+"&relays="+wsURL(relay.URL), nil)
		req.Header.Set(headerWotEnabled, "1")
		req.Header.Set(headerWotDepth, "1")
		rr := httptest.NewRecorder()
		srv.handleThread(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("fragment %s status = %d, want 200; body=%s", fragment, rr.Code, truncateForLog(rr.Body.String(), 1000))
		}
	}
	// A leaked background context warmer would connect immediately after the
	// response, so give it a bounded window to make the regression observable.
	time.Sleep(100 * time.Millisecond)
	if got := relayRequests.Load(); got != 0 {
		t.Fatalf("anonymous thread fragments contacted relay %d times", got)
	}
}

func TestCanonicalThreadFragmentsDeferWoTFiltering(t *testing.T) {
	for _, fragment := range []string{"", "hydrate", "summary", "tree", "focus", "ancestors", "replies", "participants"} {
		t.Run(fragment, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/thread/x?fragment="+fragment, nil)
			if !threadRequestDefersWoT(req, fragment) {
				t.Fatalf("expected fragment %q to defer WoT filtering", fragment)
			}
		})
	}
}

func TestPersonalizedThreadFragmentsDoNotDeferWoTFiltering(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/thread/x?fragment=hydrate", nil)
	req.Header.Set(headerViewerPubkey, strings.Repeat("a", 64))
	if threadRequestDefersWoT(req, "hydrate") {
		t.Fatal("personalized hydrate should apply WoT filtering before render")
	}
}

func TestEffectiveThreadWoTEnabled(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/thread/x", nil)
	if !effectiveThreadWoTEnabled(req, true) {
		t.Fatal("expected global on")
	}
	if effectiveThreadWoTEnabled(req, false) {
		t.Fatal("expected global off")
	}
}
