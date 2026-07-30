package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

// /thread/<id> SSR is publicly cached at the CDN (CloudFront /thread*
// behavior keys only on URL + query string). The handler must therefore NOT
// bake the requesting viewer's own reaction state into the HTML, otherwise
// the first cache fill would leak that viewer's votes to everyone who hits
// the same URL afterwards. Aggregate `data-ascii-reaction-total` is fine
// because totals are viewer-agnostic.
func TestHandleThreadDocStripsViewerReactionState(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	note := signedMutationEvent(t, nostrx.KindTextNote, "hello thread", nil)
	if err := st.SaveEvent(ctx, note); err != nil {
		t.Fatalf("save note: %v", err)
	}
	reaction := signedMutationEvent(t, nostrx.KindReaction, "+", [][]string{
		{"e", note.ID},
		{"p", note.PubKey},
	})
	if err := st.SaveEvent(ctx, reaction); err != nil {
		t.Fatalf("save reaction: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/thread/"+note.ID, nil)
	req.Header.Set(headerViewerPubkey, nostrx.EncodeNPub(reaction.PubKey))
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=\n%s", rr.Code, truncateForLog(rr.Body.String(), 600))
	}
	body := rr.Body.String()

	// Viewer-specific reaction state must NOT be baked into the cacheable
	// SSR. Empty `data-ascii-reaction-viewer=""` is fine; the client refresh
	// in thread.js will populate it after paint.
	for _, leak := range []string{
		`data-ascii-reaction-viewer="+"`,
		`data-ascii-reaction-viewer="-"`,
	} {
		if strings.Contains(body, leak) {
			t.Fatalf("thread SSR leaked viewer reaction state (%s) into cacheable HTML:\n%s",
				leak, truncateForLog(body, 1200))
		}
	}

	if !strings.Contains(body, `data-route-outlet`) {
		t.Fatalf("expected app shell route outlet in thread body")
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "private, no-store" {
		t.Fatalf("Cache-Control = %q, want private no-store for viewer-specific request", cc)
	}
}

func TestHandleThreadDocCachesCanonicalAnonymousPage(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	note := signedMutationEvent(t, nostrx.KindTextNote, "canonical thread cache", nil)
	if err := st.SaveEvent(ctx, note); err != nil {
		t.Fatalf("save note: %v", err)
	}
	allowAnonymousAuthors(t, st, note.PubKey)

	req := httptest.NewRequest(http.MethodGet, "/thread/"+note.ID, nil)
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=\n%s", rr.Code, truncateForLog(rr.Body.String(), 600))
	}
	if cc := rr.Header().Get("Cache-Control"); cc != cacheControlThreadPage {
		t.Fatalf("Cache-Control = %q, want %q", cc, cacheControlThreadPage)
	}
	if etag := rr.Header().Get("ETag"); etag == "" {
		t.Fatal("expected ETag on cacheable anonymous thread page")
	}
}

func TestCacheableAnonymousThreadDocumentRejectsPaginationCursors(t *testing.T) {
	for _, path := range []string{
		"/thread/" + strings.Repeat("a", 64) + "?cursor=1714000000",
		"/thread/" + strings.Repeat("a", 64) + "?cursor_id=" + strings.Repeat("b", 64),
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if cacheableAnonymousThreadDocument(req) {
			t.Fatalf("cacheableAnonymousThreadDocument(%q) = true, want false", path)
		}
	}
}

func TestHandleThreadDocAnonymousHTMLCacheServesRepeatHit(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	root := signedMutationEvent(t, nostrx.KindTextNote, "cached anonymous html root", nil)
	root.CreatedAt = 1714000000
	if err := st.SaveEvent(ctx, root); err != nil {
		t.Fatalf("save root: %v", err)
	}
	allowAnonymousAuthors(t, st, root.PubKey)

	req := httptest.NewRequest(http.MethodGet, "/thread/"+root.ID, nil)
	first := httptest.NewRecorder()
	srv.handleThread(first, req)
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200; body=\n%s", first.Code, truncateForLog(first.Body.String(), 600))
	}
	if !strings.Contains(first.Body.String(), "cached anonymous html root") {
		t.Fatalf("expected root content in first body")
	}

	reply := signedMutationEvent(t, nostrx.KindTextNote, "reply added after cache fill", [][]string{
		{"e", root.ID, "", "root"},
		{"p", root.PubKey},
	})
	reply.CreatedAt = root.CreatedAt + 1
	if err := st.SaveEvent(ctx, reply); err != nil {
		t.Fatalf("save reply: %v", err)
	}

	second := httptest.NewRecorder()
	srv.handleThread(second, httptest.NewRequest(http.MethodGet, "/thread/"+root.ID, nil))
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200; body=\n%s", second.Code, truncateForLog(second.Body.String(), 600))
	}
	if second.Body.String() != first.Body.String() {
		t.Fatalf("second body was recomputed instead of served from anonymous HTML cache")
	}
	if strings.Contains(second.Body.String(), "reply added after cache fill") {
		t.Fatalf("cached second body unexpectedly included post-cache reply")
	}
	counters := srv.metrics.Snapshot()["counters"].(map[string]int64)
	if counters["thread.anonymous_html_cache_hit"] != 1 {
		t.Fatalf("anonymous HTML cache hit counter = %d, want 1; counters=%v", counters["thread.anonymous_html_cache_hit"], counters)
	}
}

func TestHandleThreadDocAnonymousHTMLCacheHonorsIfNoneMatch(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	note := signedMutationEvent(t, nostrx.KindTextNote, "cached anonymous etag", nil)
	if err := st.SaveEvent(ctx, note); err != nil {
		t.Fatalf("save note: %v", err)
	}
	allowAnonymousAuthors(t, st, note.PubKey)

	first := httptest.NewRecorder()
	srv.handleThread(first, httptest.NewRequest(http.MethodGet, "/thread/"+note.ID, nil))
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("expected ETag on first anonymous thread response")
	}

	req := httptest.NewRequest(http.MethodGet, "/thread/"+note.ID, nil)
	req.Header.Set("If-None-Match", etag)
	second := httptest.NewRecorder()
	srv.handleThread(second, req)
	if second.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304; body=%q", second.Code, second.Body.String())
	}
	if second.Body.Len() != 0 {
		t.Fatalf("304 body length = %d, want 0", second.Body.Len())
	}
}

func TestHandleThreadDocAnonymousBrowserMissReturnsNotFoundWithoutWarming(t *testing.T) {
	srv, _ := newTestServer(t, testServerOptions{relayTimeout: 500 * time.Millisecond})
	srv.warmer = nil
	note := signedMutationEvent(t, nostrx.KindTextNote, "relay backed anonymous SSR note", nil)
	relay := newRelayWithEvents(t, []nostrx.Event{note})
	defer relay.Close()
	srv.cfg.DefaultRelays = []string{wsURL(relay.URL)}

	req := httptest.NewRequest(http.MethodGet, "/thread/"+note.ID, nil)
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=\n%s", rr.Code, truncateForLog(rr.Body.String(), 600))
	}
	body := rr.Body.String()
	if strings.Contains(body, "relay backed anonymous SSR note") {
		t.Fatalf("anonymous cache miss unexpectedly relay-fetched note in SSR body:\n%s", truncateForLog(body, 1200))
	}
	if !strings.Contains(body, `class="route-error-panel"`) {
		t.Fatalf("expected not-found panel for anonymous cache miss:\n%s", truncateForLog(body, 1200))
	}
	counters := srv.metrics.Snapshot()["counters"].(map[string]int64)
	if counters["event.sync_fetch"] != 0 {
		t.Fatalf("anonymous cache miss should not foreground relay fetch, counters=%v", counters)
	}
	if counters["thread.anonymous_miss.warm_enqueued"] != 0 {
		t.Fatalf("anonymous cache miss should not enqueue warm work, counters=%v", counters)
	}
}

func TestHandleThreadDocCrawlerMissDoesNotRelayFetch(t *testing.T) {
	srv, _ := newTestServer(t, testServerOptions{relayTimeout: 500 * time.Millisecond})
	note := signedMutationEvent(t, nostrx.KindTextNote, "crawler should not fetch this", nil)
	relay := newRelayWithEvents(t, []nostrx.Event{note})
	defer relay.Close()
	srv.cfg.DefaultRelays = []string{wsURL(relay.URL)}

	req := httptest.NewRequest(http.MethodGet, "/thread/"+note.ID, nil)
	req.Header.Set("User-Agent", "Twitterbot/1.0")
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=\n%s", rr.Code, truncateForLog(rr.Body.String(), 600))
	}
	body := rr.Body.String()
	if strings.Contains(body, "crawler should not fetch this") {
		t.Fatalf("crawler request unexpectedly fetched/rendered relay note:\n%s", truncateForLog(body, 1200))
	}
	counters := srv.metrics.Snapshot()["counters"].(map[string]int64)
	if counters["event.sync_fetch"] != 0 {
		t.Fatalf("crawler miss should not relay fetch, counters=%v", counters)
	}
}

func TestHandleThreadDocAnonymousPageUsesBoundedStorePreview(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	root := signedMutationEvent(t, nostrx.KindTextNote, "anonymous preview root", nil)
	root.CreatedAt = 1714000000
	if err := st.SaveEvent(ctx, root); err != nil {
		t.Fatalf("save root: %v", err)
	}
	authors := []string{root.PubKey}
	for i := 0; i < anonymousThreadPreviewReplyLimit+5; i++ {
		reply := signedMutationEvent(t, nostrx.KindTextNote, "anonymous preview reply "+strconv.Itoa(i), [][]string{
			{"e", root.ID, "", "root"},
			{"p", root.PubKey},
		})
		reply.CreatedAt = root.CreatedAt + int64(i+1)
		if err := st.SaveEvent(ctx, reply); err != nil {
			t.Fatalf("save reply %d: %v", i, err)
		}
		authors = append(authors, reply.PubKey)
	}
	allowAnonymousAuthors(t, st, authors...)

	req := httptest.NewRequest(http.MethodGet, "/thread/"+root.ID, nil)
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=\n%s", rr.Code, truncateForLog(rr.Body.String(), 600))
	}
	body := rr.Body.String()
	if !strings.Contains(body, "anonymous preview reply 0") {
		t.Fatalf("expected first preview reply in body:\n%s", truncateForLog(body, 1200))
	}
	if strings.Contains(body, "anonymous preview reply "+strconv.Itoa(anonymousThreadPreviewReplyLimit+4)) {
		t.Fatalf("anonymous preview rendered beyond bounded first page:\n%s", truncateForLog(body, 1200))
	}
	if strings.Contains(body, `data-thread-tree-view`) {
		t.Fatalf("anonymous preview should not render full tree markup:\n%s", truncateForLog(body, 1200))
	}
	counters := srv.metrics.Snapshot()["counters"].(map[string]int64)
	if counters["event.sync_fetch"] != 0 {
		t.Fatalf("anonymous preview should be store-only, event.sync_fetch=%d", counters["event.sync_fetch"])
	}
	if counters["thread.relay_pass.outbox"] != 0 || counters["thread.relay_pass.indexer"] != 0 {
		t.Fatalf("anonymous preview should not run reply relay passes, counters=%v", counters)
	}
}

func TestHandleThreadDocDoesNotSharedCacheSelectedVariant(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	root := signedMutationEvent(t, nostrx.KindTextNote, "root", nil)
	reply := signedMutationEvent(t, nostrx.KindTextNote, "selected reply", [][]string{
		{"e", root.ID, "", "root"},
		{"p", root.PubKey},
	})
	for _, ev := range []nostrx.Event{root, reply} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatalf("save event: %v", err)
		}
	}
	allowAnonymousAuthors(t, st, root.PubKey, reply.PubKey)

	req := httptest.NewRequest(http.MethodGet, "/thread/"+root.ID+"?selected="+reply.ID, nil)
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=\n%s", rr.Code, truncateForLog(rr.Body.String(), 600))
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "private, no-store" {
		t.Fatalf("Cache-Control = %q, want private no-store for selected variant", cc)
	}
}

func TestCachedThreadAssemblyUsesStoredGraph(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	root := signedMutationEvent(t, nostrx.KindTextNote, "root cached graph", nil)
	reply := signedMutationEvent(t, nostrx.KindTextNote, "reply cached graph", [][]string{
		{"e", root.ID, "", "root"},
		{"p", root.PubKey},
	})
	nested := signedMutationEvent(t, nostrx.KindTextNote, "nested cached graph", [][]string{
		{"e", root.ID, "", "root"},
		{"e", reply.ID, "", "reply"},
		{"p", root.PubKey},
		{"p", reply.PubKey},
	})
	sibling := signedMutationEvent(t, nostrx.KindTextNote, "sibling cached graph", [][]string{
		{"e", root.ID, "", "root"},
		{"p", root.PubKey},
	})
	for _, ev := range []nostrx.Event{root, reply, nested, sibling} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.BuildThreadGraphCache(ctx, root.ID, 500); err != nil {
		t.Fatal(err)
	}

	assembly, ok := srv.cachedThreadAssembly(ctx, root, nested, true, nil)
	if !ok {
		t.Fatal("expected cached thread assembly")
	}
	if assembly.ParentByID[nested.ID] != reply.ID {
		t.Fatalf("nested parent = %q, want %q", assembly.ParentByID[nested.ID], reply.ID)
	}
	seen := map[string]bool{}
	for _, ev := range assembly.TreeReplies {
		seen[ev.ID] = true
	}
	for _, id := range []string{reply.ID, nested.ID, sibling.ID} {
		if !seen[id] {
			t.Fatalf("cached assembly missing event %s", id)
		}
	}
}

func TestCachedThreadAssemblyRejectsMissingSelectedParent(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	root := signedMutationEvent(t, nostrx.KindTextNote, "root cached graph missing parent", nil)
	parent := signedMutationEvent(t, nostrx.KindTextNote, "parent omitted from cached graph", [][]string{
		{"e", root.ID, "", "root"},
		{"p", root.PubKey},
	})
	selected := signedMutationEvent(t, nostrx.KindTextNote, "selected with omitted parent", [][]string{
		{"e", root.ID, "", "root"},
		{"e", parent.ID, "", "reply"},
		{"p", root.PubKey},
		{"p", parent.PubKey},
	})
	for _, ev := range []nostrx.Event{root, parent, selected} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SaveThreadGraphCache(ctx, store.ThreadGraphCache{
		RootID:           root.ID,
		EventIDs:         []string{selected.ID},
		ParentByID:       map[string]string{selected.ID: parent.ID},
		LastReplyEventAt: selected.CreatedAt,
		BuiltAt:          selected.CreatedAt,
	}); err != nil {
		t.Fatal(err)
	}

	if _, ok := srv.cachedThreadAssembly(ctx, root, selected, true, nil); ok {
		t.Fatal("cached assembly should be rejected when selected parent is absent from graph")
	}
	lookup := map[string]*nostrx.Event{parent.ID: &parent}
	assembly := srv.loadThreadAssembly(ctx, root, selected, true, nil, func(id string) *nostrx.Event {
		return lookup[id]
	}, false)
	seen := map[string]bool{}
	for _, ev := range assembly.TreeReplies {
		seen[ev.ID] = true
	}
	if !seen[parent.ID] || !seen[selected.ID] {
		t.Fatalf("fallback assembly missing selected path: parent=%v selected=%v", seen[parent.ID], seen[selected.ID])
	}
	if assembly.ParentByID[selected.ID] != parent.ID {
		t.Fatalf("selected parent = %q, want %q", assembly.ParentByID[selected.ID], parent.ID)
	}
}
