package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	staticfs "ptxt-nstr/web/static"
)

func TestStaticAssetName(t *testing.T) {
	version := staticfs.ReleaseVersion()
	name, versioned := staticAssetName("/static/" + version + "/js/app/document-router.js")
	if !versioned {
		t.Fatalf("expected versioned asset path")
	}
	if name != "js/app/document-router.js" {
		t.Fatalf("name = %q, want js/app/document-router.js", name)
	}

	name, versioned = staticAssetName("/static/012345abcdef/js/app/document-router.js")
	if !versioned {
		t.Fatalf("expected stale versioned asset path")
	}
	if name != "js/app/document-router.js" {
		t.Fatalf("stale versioned name = %q, want js/app/document-router.js", name)
	}

	name, versioned = staticAssetName("/static/js/app/document-router.js")
	if versioned {
		t.Fatalf("legacy static path should not be versioned")
	}
	if name != "js/app/document-router.js" {
		t.Fatalf("legacy name = %q, want js/app/document-router.js", name)
	}
}

func TestHandleStaticAssetVersionedCacheControl(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/static/"+staticfs.ReleaseVersion()+"/js/app/document-router.js", nil)
	rr := httptest.NewRecorder()

	var srv Server
	srv.handleStaticAsset(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestHandleStaticAssetStaleVersionStillServesEmbeddedAsset(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/static/012345abcdef/css/app.css", nil)
	rr := httptest.NewRecorder()

	var srv Server
	srv.handleStaticAsset(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "text/css; charset=utf-8" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := rr.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q", got)
	}
}

func TestHandleStaticAssetLegacyCacheControl(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/static/js/app/document-router.js", nil)
	rr := httptest.NewRecorder()

	var srv Server
	srv.handleStaticAsset(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q", got)
	}
}
