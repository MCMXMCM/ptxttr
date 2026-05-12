package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

func TestOEmbedEventIDFromPath(t *testing.T) {
	cases := map[string]string{
		"/thread/" + testHexEventID: testHexEventID,
		"/e/" + testHexEventID:      testHexEventID,
		"/" + testHexEventID:        testHexEventID,
		"/foo":                      "",
		"":                          "",
		"/feed":                     "",
	}
	for in, want := range cases {
		if got := oEmbedEventIDFromPath(in); got != want {
			t.Fatalf("oEmbedEventIDFromPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHandleOEmbedJSON(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	author := strings.Repeat("a", 64)
	id := strings.Repeat("0", 64)
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        id,
		PubKey:    author,
		Kind:      nostrx.KindTextNote,
		CreatedAt: 1700000000,
		Content:   "embed me",
		Sig:       "sig",
	}); err != nil {
		t.Fatal(err)
	}
	target := "https://example.test/thread/" + id
	req := httptest.NewRequest(http.MethodGet, "/services/oembed?url="+url.QueryEscape(target), nil)
	rr := httptest.NewRecorder()
	srv.handleOEmbed(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type = %q", got)
	}
	var resp oEmbedResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Type != "rich" || resp.Version != "1.0" {
		t.Fatalf("type/version = %q/%q", resp.Type, resp.Version)
	}
	if resp.HTML == "" {
		t.Fatalf("expected HTML, got empty")
	}
	if !strings.Contains(resp.HTML, "embed me") {
		t.Fatalf("HTML missing content excerpt: %s", resp.HTML)
	}
	if resp.ProviderName != ogSiteName {
		t.Fatalf("ProviderName = %q", resp.ProviderName)
	}
	if resp.ThumbnailURL == "" {
		t.Fatalf("expected ThumbnailURL pointing at /og/<id>.png")
	}
}

func TestHandleOEmbedXML(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	author := strings.Repeat("a", 64)
	id := strings.Repeat("0", 64)
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        id,
		PubKey:    author,
		Kind:      nostrx.KindTextNote,
		CreatedAt: 1700000000,
		Content:   "xml-mode embed",
		Sig:       "sig",
	}); err != nil {
		t.Fatal(err)
	}
	target := "https://example.test/thread/" + id
	req := httptest.NewRequest(http.MethodGet, "/services/oembed?url="+url.QueryEscape(target)+"&format=xml", nil)
	rr := httptest.NewRecorder()
	srv.handleOEmbed(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/xml") {
		t.Fatalf("Content-Type = %q", got)
	}
	body := rr.Body.String()
	if !strings.HasPrefix(body, "<?xml") {
		t.Fatalf("expected XML header, got: %s", truncateForLog(body, 200))
	}
	if !strings.Contains(body, "<oembed>") {
		t.Fatalf("expected <oembed> root, got: %s", truncateForLog(body, 500))
	}
	if !strings.Contains(body, "xml-mode embed") {
		t.Fatalf("expected content excerpt in XML, got: %s", truncateForLog(body, 500))
	}
}

func TestHandleOEmbedMissingURL(t *testing.T) {
	srv, _ := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/services/oembed", nil)
	rr := httptest.NewRecorder()
	srv.handleOEmbed(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestHandleOEmbedNotFound(t *testing.T) {
	srv, _ := testServer(t)
	target := "https://example.test/thread/" + strings.Repeat("f", 64)
	req := httptest.NewRequest(http.MethodGet, "/services/oembed?url="+url.QueryEscape(target), nil)
	rr := httptest.NewRecorder()
	srv.handleOEmbed(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestHandleOEmbedDoesNotFanOutToRelays(t *testing.T) {
	// Verifies the cache-only constraint: an oembed for an unknown id must
	// 404 quickly without trying to fetch from configured relays.
	srv, _ := testServer(t)
	target := "https://example.test/thread/" + strings.Repeat("c", 64)
	req := httptest.NewRequest(http.MethodGet, "/services/oembed?url="+url.QueryEscape(target), nil)
	rr := httptest.NewRecorder()
	srv.handleOEmbed(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (must be cache-only, no relay fan-out)", rr.Code)
	}
}

func TestEmitOEmbedDiscoveryHeaders(t *testing.T) {
	r := httptest.NewRequest("GET", "/thread/abc", nil)
	r.Host = "example.test"
	rr := httptest.NewRecorder()
	emitOEmbedDiscoveryHeaders(rr, r)
	links := rr.Header().Values("Link")
	if len(links) != 2 {
		t.Fatalf("expected 2 Link headers, got %d", len(links))
	}
	hasJSON := false
	hasXML := false
	for _, l := range links {
		if strings.Contains(l, "application/json+oembed") {
			hasJSON = true
		}
		if strings.Contains(l, "text/xml+oembed") {
			hasXML = true
		}
		if !strings.Contains(l, "/services/oembed?url=") {
			t.Fatalf("Link missing oembed URL prefix: %s", l)
		}
	}
	if !hasJSON || !hasXML {
		t.Fatalf("expected both JSON + XML alternates, got %v", links)
	}
}

func TestHandleThreadEmitsOEmbedLinkHeaders(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	author := strings.Repeat("b", 64)
	id := strings.Repeat("9", 64)
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        id,
		PubKey:    author,
		Kind:      nostrx.KindTextNote,
		CreatedAt: 1700000000,
		Content:   "test",
		Sig:       "sig",
	}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/thread/"+id, nil)
	rr := httptest.NewRecorder()
	srv.handleThread(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if links := rr.Header().Values("Link"); len(links) != 2 {
		t.Fatalf("expected 2 Link headers, got %d: %v", len(links), links)
	}
}
