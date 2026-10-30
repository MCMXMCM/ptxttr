package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

// The edge shares a response across every request with the same URL, so a
// route may only send public cache headers when nothing about the requester
// can change the bytes. These cases cover the identity and preference inputs
// the server reads; requests that carry them must stay uncached.
var personalizedRouteRequests = map[string]func(*http.Request){
	"viewer":     markTestRequestLoggedIn,
	"preference": func(r *http.Request) { r.Header.Set(headerWotEnabled, "0") },
	"query pref": func(r *http.Request) { r.URL.RawQuery = joinTestQuery(r.URL.RawQuery, "sort=trend24h") },
}

func joinTestQuery(raw, extra string) string {
	if raw == "" {
		return extra
	}
	return raw + "&" + extra
}

// newRouteCacheTestServer lifts the per-IP shield so many requests from the
// test address reach the handlers instead of the shield's 429.
func newRouteCacheTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	srv, st := testServer(t)
	for _, limiter := range []**searchLimiter{&srv.anonymousLimiter, &srv.anonymousOptionalLimiter, &srv.botLimiter, &srv.viewerLimiter, &srv.searchLimiter} {
		*limiter = newSearchLimiter(10000, 10000)
	}
	return srv, st
}

func serveTestRoute(srv *Server, target string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func assertAnonymousRouteShared(t *testing.T, srv *Server, target string) {
	t.Helper()
	anonymous := serveTestRoute(srv, target, nil)
	if anonymous.Code != http.StatusOK {
		t.Fatalf("%s status = %d", target, anonymous.Code)
	}
	if got := anonymous.Header().Get("Cache-Control"); got != cacheControlContentAddressed {
		t.Fatalf("%s anonymous Cache-Control = %q, want %q", target, got, cacheControlContentAddressed)
	}
	other := serveTestRoute(srv, target, func(r *http.Request) {
		r.RemoteAddr = "203.0.113.9:4000"
		r.Header.Set("X-Ptxt-Client-IP", "203.0.113.9")
		r.AddCookie(&http.Cookie{Name: "unrelated", Value: "1"})
	})
	if other.Body.String() != anonymous.Body.String() {
		t.Fatalf("%s anonymous body changed with the requester's address or cookies", target)
	}
	for name, mutate := range personalizedRouteRequests {
		rec := serveTestRoute(srv, target, mutate)
		if got := rec.Header().Get("Cache-Control"); strings.Contains(got, "public") {
			t.Fatalf("%s %s request got shared Cache-Control %q", target, name, got)
		}
	}
}

func TestAnonymousThreadFragmentsShareEdgeCache(t *testing.T) {
	srv, st := newRouteCacheTestServer(t)
	ctx := context.Background()
	root := signedMutationEvent(t, nostrx.KindTextNote, "fragment root", nil)
	reply := signedMutationEvent(t, nostrx.KindTextNote, "fragment reply", [][]string{
		{"e", root.ID, "", "root"},
		{"p", root.PubKey},
	})
	for _, ev := range []nostrx.Event{root, reply} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	allowAnonymousAuthors(t, st, root.PubKey, reply.PubKey)
	for _, fragment := range []string{"summary", "tree", "focus", "ancestors", "replies", "hydrate", "participants"} {
		assertAnonymousRouteShared(t, srv, "/thread/"+root.ID+"?fragment="+fragment+"&selected="+reply.ID)
	}
}

func TestAnonymousTagAndReadsShareEdgeCache(t *testing.T) {
	srv, st := newRouteCacheTestServer(t)
	read := signedMutationEvent(t, nostrx.KindLongForm, "a long read", [][]string{
		{"d", "long-read"},
		{"title", "A long read"},
	})
	if err := st.SaveEvent(context.Background(), read); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/tag/nostr", "/tag/nostr?fragment=1", "/reads", "/reads?fragment=1", "/reads/" + read.ID} {
		assertAnonymousRouteShared(t, srv, target)
	}

	srv.searchLimiter = newSearchLimiter(1, 0.001)
	_ = serveTestRoute(srv, "/tag/limited", nil)
	limited := serveTestRoute(srv, "/tag/limited", nil)
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("rate-limited tag status = %d, want 429", limited.Code)
	}
	if got := limited.Header().Get("Cache-Control"); strings.Contains(got, "public") {
		t.Fatalf("rate-limited tag response got shared Cache-Control %q", got)
	}
}
