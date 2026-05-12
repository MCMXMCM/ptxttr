package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

const tinyPNG = "\x89PNG\r\n\x1a\n" + "00000000000"

func writeProfilePicture(t *testing.T, srv *Server, pubkey, pictureURL string) {
	t.Helper()
	body := map[string]string{"picture": pictureURL, "name": "alice"}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	event := nostrx.Event{
		ID:        pubkey + "-profile",
		PubKey:    pubkey,
		Kind:      nostrx.KindProfileMetadata,
		CreatedAt: 1,
		Content:   string(raw),
	}
	if err := srv.store.SaveEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
}

func TestHandleAvatarServesUpstreamBytesWithCacheHeaders(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte(tinyPNG))
	}))
	t.Cleanup(upstream.Close)

	srv, _ := testServer(t)
	pubkey := "alice"
	pictureURL := upstream.URL + "/pic.png"
	writeProfilePicture(t, srv, pubkey, pictureURL)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/avatar/"+pubkey+"?v="+avatarFingerprint(pictureURL), nil)
	srv.handleAvatar(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%q", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("content-type = %q, want image/png", got)
	}
	if !strings.Contains(rr.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("cache-control should be immutable, got %q", rr.Header().Get("Cache-Control"))
	}
	if rr.Header().Get("ETag") == "" {
		t.Fatal("ETag should be set")
	}
	if rr.Body.String() != tinyPNG {
		t.Fatalf("body = %q, want passthrough", rr.Body.String())
	}

	// Second request should hit the in-memory cache, not upstream.
	rr2 := httptest.NewRecorder()
	srv.handleAvatar(rr2, httptest.NewRequest(http.MethodGet, "/avatar/"+pubkey+"?v="+avatarFingerprint(pictureURL), nil))
	if rr2.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200", rr2.Code)
	}
	if hits.Load() != 1 {
		t.Fatalf("upstream hit %d times, want 1 (second request should be cache hit)", hits.Load())
	}
}

func TestHandleAvatarRespectsIfNoneMatch(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte(tinyPNG))
	}))
	t.Cleanup(upstream.Close)

	srv, _ := testServer(t)
	pubkey := "alice"
	pictureURL := upstream.URL + "/pic.png"
	writeProfilePicture(t, srv, pubkey, pictureURL)

	rr := httptest.NewRecorder()
	srv.handleAvatar(rr, httptest.NewRequest(http.MethodGet, "/avatar/"+pubkey+"?v="+avatarFingerprint(pictureURL), nil))
	etag := rr.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag should be set on first response")
	}

	rr2 := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/avatar/"+pubkey+"?v="+avatarFingerprint(pictureURL), nil)
	req.Header.Set("If-None-Match", etag)
	srv.handleAvatar(rr2, req)
	if rr2.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304 when If-None-Match matches", rr2.Code)
	}
	if rr2.Body.Len() != 0 {
		t.Fatalf("304 body should be empty, got %d bytes", rr2.Body.Len())
	}
}

func TestHandleAvatarMissingPictureReturns404(t *testing.T) {
	srv, _ := testServer(t)
	pubkey := "ghost"

	rr := httptest.NewRecorder()
	srv.handleAvatar(rr, httptest.NewRequest(http.MethodGet, "/avatar/"+pubkey, nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when no picture is on file", rr.Code)
	}
	if rr.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache-control = %q, want no-store on missing avatar", rr.Header().Get("Cache-Control"))
	}
}

func TestHandleAvatarUnfingerprintedPathRedirectsToCanonical(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte(tinyPNG))
	}))
	t.Cleanup(upstream.Close)

	srv, _ := testServer(t)
	pubkey := "alice"
	pictureURL := upstream.URL + "/pic.png"
	writeProfilePicture(t, srv, pubkey, pictureURL)

	rr := httptest.NewRecorder()
	srv.handleAvatar(rr, httptest.NewRequest(http.MethodGet, "/avatar/"+pubkey, nil))
	if rr.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307 to canonical v= URL when v is absent", rr.Code)
	}
	loc := rr.Header().Get("Location")
	wantPrefix := "/avatar/" + pubkey + "?v=" + avatarFingerprint(pictureURL)
	if loc != wantPrefix {
		t.Fatalf("Location = %q, want %q", loc, wantPrefix)
	}
}

func TestHandleAvatarRedirectsOnFingerprintMismatch(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte(tinyPNG))
	}))
	t.Cleanup(upstream.Close)

	srv, _ := testServer(t)
	pubkey := "alice"
	writeProfilePicture(t, srv, pubkey, upstream.URL+"/pic.png")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/avatar/"+pubkey+"?v=stale", nil)
	srv.handleAvatar(rr, req)
	if rr.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307 when fingerprint is stale", rr.Code)
	}
	loc := rr.Header().Get("Location")
	if !strings.HasPrefix(loc, "/avatar/"+pubkey+"?v=") || strings.Contains(loc, "stale") {
		t.Fatalf("redirect location = %q, want canonical fingerprinted URL", loc)
	}
}

func TestHandleAvatarRejectsDisallowedContentType(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>nope</html>"))
	}))
	t.Cleanup(upstream.Close)

	srv, _ := testServer(t)
	pubkey := "alice"
	pictureURL := upstream.URL + "/pic.html"
	writeProfilePicture(t, srv, pubkey, pictureURL)

	rr := httptest.NewRecorder()
	srv.handleAvatar(rr, httptest.NewRequest(http.MethodGet, "/avatar/"+pubkey+"?v="+avatarFingerprint(pictureURL), nil))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 for disallowed content-type", rr.Code)
	}
}

func TestHandleAvatarAcceptsOctetStreamWithPNGBytes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte(tinyPNG))
	}))
	t.Cleanup(upstream.Close)

	srv, _ := testServer(t)
	pubkey := "alice"
	pictureURL := upstream.URL + "/pic.bin"
	writeProfilePicture(t, srv, pubkey, pictureURL)

	rr := httptest.NewRecorder()
	srv.handleAvatar(rr, httptest.NewRequest(http.MethodGet, "/avatar/"+pubkey+"?v="+avatarFingerprint(pictureURL), nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for octet-stream body with PNG magic", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("content-type = %q, want image/png from sniff", got)
	}
}

func TestHandleAvatarRetriesUpstream503ThenSucceeds(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := hits.Add(1)
		if n < 3 {
			http.Error(w, "temp", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte(tinyPNG))
	}))
	t.Cleanup(upstream.Close)

	srv, _ := testServer(t)
	pubkey := "alice"
	pictureURL := upstream.URL + "/pic.png"
	writeProfilePicture(t, srv, pubkey, pictureURL)

	rr := httptest.NewRecorder()
	srv.handleAvatar(rr, httptest.NewRequest(http.MethodGet, "/avatar/"+pubkey+"?v="+avatarFingerprint(pictureURL), nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 after retries", rr.Code)
	}
	if hits.Load() != 3 {
		t.Fatalf("upstream hits = %d, want 3 (two 503 then 200)", hits.Load())
	}
}

func TestHandleAvatarUpstream403NoRetry(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	t.Cleanup(upstream.Close)

	srv, _ := testServer(t)
	pubkey := "alice"
	pictureURL := upstream.URL + "/pic.png"
	writeProfilePicture(t, srv, pubkey, pictureURL)

	rr := httptest.NewRecorder()
	srv.handleAvatar(rr, httptest.NewRequest(http.MethodGet, "/avatar/"+pubkey+"?v="+avatarFingerprint(pictureURL), nil))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rr.Code)
	}
	if hits.Load() != 1 {
		t.Fatalf("upstream hits = %d, want 1 (no retry on 403)", hits.Load())
	}
}

func TestHandleAvatarStaleGraphEmptyPictureUsesKind0(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte(tinyPNG))
	}))
	t.Cleanup(upstream.Close)

	srv, _ := testServer(t)
	pubkey := "alice"
	pictureURL := upstream.URL + "/pic.png"
	writeProfilePicture(t, srv, pubkey, pictureURL)

	srv.store.PutProfileSidecarForTesting(pubkey, store.ProfileSummary{
		PubKey:      pubkey,
		DisplayName: "Alice",
		Picture:     "",
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/avatar/"+pubkey+"?v="+avatarFingerprint(pictureURL), nil)
	srv.handleAvatar(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 when graph picture empty but kind-0 has URL", rr.Code)
	}
}

func TestAvatarFingerprintStableAndDistinct(t *testing.T) {
	a := avatarFingerprint("https://example.com/a.png")
	b := avatarFingerprint("https://example.com/b.png")
	again := avatarFingerprint("https://example.com/a.png")
	if a != again {
		t.Fatalf("fingerprint not stable: %q vs %q", a, again)
	}
	if a == b {
		t.Fatalf("different URLs should produce different fingerprints, both got %q", a)
	}
	if avatarFingerprint("") != "" {
		t.Fatal("empty URL should produce empty fingerprint")
	}
}

func TestAvatarSrcForReturnsEmptyWhenInputsMissing(t *testing.T) {
	if got := avatarSrcFor("", "https://example.com/a.png"); got != "" {
		t.Fatalf("avatarSrcFor empty pubkey = %q, want empty", got)
	}
	if got := avatarSrcFor("alice", ""); got != "" {
		t.Fatalf("avatarSrcFor empty picture = %q, want empty", got)
	}
	got := avatarSrcFor("alice", "https://example.com/a.png")
	if !strings.HasPrefix(got, "/avatar/alice?v=") {
		t.Fatalf("avatarSrcFor = %q, want canonical proxy URL", got)
	}
}
