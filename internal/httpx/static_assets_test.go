package httpx

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
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

func TestDocumentsLoadSmallBrandImages(t *testing.T) {
	srv, _ := testServer(t)
	logoTag := regexp.MustCompile(`<img [^>]*class="about-logo[^"]*"[^>]*>`)
	iconHref := regexp.MustCompile(`<link rel="(?:icon" type="image/png|apple-touch-icon)" href="([^"]+)"`)
	imageAsset := regexp.MustCompile(`/static/[0-9a-f]{12}/(img/[^"]+)`)
	for _, path := range []string{"/", "/about", "/terms"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, rec.Code)
		}
		body := rec.Body.String()
		logos := logoTag.FindAllString(body, -1)
		if len(logos) == 0 {
			t.Fatalf("%s rendered no brand logo", path)
		}
		// Each document carries a light and a dark logo, and the mobile menu
		// stays hidden on desktop: lazy loading fetches only the visible one.
		for _, logo := range logos {
			if !strings.Contains(logo, `loading="lazy"`) || !strings.Contains(logo, `_256.png"`) {
				t.Errorf("%s logo = %s, want a lazy 256 px image", path, logo)
			}
		}
		icons := iconHref.FindAllStringSubmatch(body, -1)
		if len(icons) == 0 {
			t.Fatalf("%s rendered no PNG or touch icon", path)
		}
		for _, icon := range icons {
			if !strings.HasSuffix(icon[1], "_180.png") {
				t.Errorf("%s icon = %s, want the 180 px image", path, icon[1])
			}
		}
		for _, match := range imageAsset.FindAllStringSubmatch(body, -1) {
			if _, err := fs.Stat(staticfs.FS, match[1]); err != nil {
				t.Errorf("%s references missing static image %s", path, match[1])
			}
		}
	}
}

func TestDocumentsPreloadTheStylesheetFont(t *testing.T) {
	css, err := fs.ReadFile(staticfs.FS, "css/app.css")
	if err != nil {
		t.Fatal(err)
	}
	// The preload must request the exact URL app.css resolves for its
	// @font-face, or browsers fetch the font twice.
	fonts := regexp.MustCompile(`url\("\.\./(fonts/[^"]+\.woff2)"\)`).FindAllStringSubmatch(string(css), -1)
	if len(fonts) != 1 {
		t.Fatalf("app.css font urls = %v, want exactly one woff2", fonts)
	}
	// Only the URL matters here: the desktop fork's public tree ships its
	// font separately, so the file itself may be absent from the embed.
	font := fonts[0][1]
	want := `<link rel="preload" href="` + staticfs.VersionedPath(font) + `" as="font" type="font/woff2" crossorigin>`
	srv, _ := testServer(t)
	for _, path := range []string{"/", "/about"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s head is missing %s", path, want)
		}
	}
}
