package httpx

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ptxt-nstr/internal/nostrx"
)

func TestHandleOGImageReturnsPNGOnHit(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	author := strings.Repeat("a", 64)
	id := strings.Repeat("0", 64)
	ev := nostrx.Event{
		ID:        id,
		PubKey:    author,
		Kind:      nostrx.KindTextNote,
		CreatedAt: time.Now().Unix(),
		Content:   "Hello from a Nostr OG card test.",
		Sig:       "sig",
	}
	if err := st.SaveEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/og/"+id+".png", nil)
	rr := httptest.NewRecorder()
	srv.handleOGImage(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %q", got)
	}
	if !bytes.HasPrefix(rr.Body.Bytes(), []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("body is not a PNG")
	}
	if got := rr.Header().Get("ETag"); got != `"`+id+`"` {
		t.Fatalf("ETag = %q", got)
	}
	if cc := rr.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=300") {
		t.Fatalf("Cache-Control = %q", cc)
	}
}

func TestHandleOGImage304OnMatchingETag(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	author := strings.Repeat("a", 64)
	id := strings.Repeat("0", 64)
	ev := nostrx.Event{
		ID:        id,
		PubKey:    author,
		Kind:      nostrx.KindTextNote,
		CreatedAt: time.Now().Unix(),
		Content:   "OG card etag test",
		Sig:       "sig",
	}
	if err := st.SaveEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/og/"+id+".png", nil)
	req.Header.Set("If-None-Match", `"`+id+`"`)
	rr := httptest.NewRecorder()
	srv.handleOGImage(rr, req)
	if rr.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", rr.Code)
	}
	if rr.Body.Len() != 0 {
		t.Fatalf("304 body = %d bytes, want 0", rr.Body.Len())
	}
}

func TestHandleOGImage404OnMiss(t *testing.T) {
	srv, _ := testServer(t)
	id := strings.Repeat("f", 64)
	req := httptest.NewRequest(http.MethodGet, "/og/"+id+".png", nil)
	rr := httptest.NewRecorder()
	srv.handleOGImage(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	if cc := rr.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=") {
		t.Fatalf("expected negative cache header on miss, got %q", cc)
	}
}

func TestHandleOGImage404OnGarbagePath(t *testing.T) {
	srv, _ := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/og/garbage.png", nil)
	rr := httptest.NewRecorder()
	srv.handleOGImage(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}
