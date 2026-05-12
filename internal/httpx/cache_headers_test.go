package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSetContentAddressedCache(t *testing.T) {
	rr := httptest.NewRecorder()
	setContentAddressedCache(rr, "abc123")
	if got := rr.Header().Get("Cache-Control"); got != cacheControlContentAddressed {
		t.Fatalf("Cache-Control = %q, want %q", got, cacheControlContentAddressed)
	}
	if got := rr.Header().Get("ETag"); got != `"abc123"` {
		t.Fatalf("ETag = %q, want %q", got, `"abc123"`)
	}
}

func TestSetContentAddressedCacheEmptyEtagIsNoop(t *testing.T) {
	rr := httptest.NewRecorder()
	setContentAddressedCache(rr, "")
	if got := rr.Header().Get("Cache-Control"); got != "" {
		t.Fatalf("Cache-Control = %q, want empty", got)
	}
	if got := rr.Header().Get("ETag"); got != "" {
		t.Fatalf("ETag = %q, want empty", got)
	}
}

func TestSetShortCache(t *testing.T) {
	rr := httptest.NewRecorder()
	setShortCache(rr, 30)
	got := rr.Header().Get("Cache-Control")
	if !strings.Contains(got, "max-age=30") || !strings.Contains(got, "s-maxage=150") {
		t.Fatalf("Cache-Control = %q, want max-age=30 + s-maxage=150", got)
	}
}

func TestSetShortCacheZeroFallback(t *testing.T) {
	rr := httptest.NewRecorder()
	setShortCache(rr, 0)
	got := rr.Header().Get("Cache-Control")
	if !strings.Contains(got, "max-age=60") {
		t.Fatalf("Cache-Control = %q, want fallback max-age=60", got)
	}
}

func TestSetNegativeCacheDefault(t *testing.T) {
	rr := httptest.NewRecorder()
	setNegativeCache(rr)
	if got := rr.Header().Get("Cache-Control"); got != cacheControlNegative {
		t.Fatalf("Cache-Control = %q, want %q", got, cacheControlNegative)
	}
}

func TestSetNegativeCacheDoesNotOverridePreset(t *testing.T) {
	rr := httptest.NewRecorder()
	rr.Header().Set("Cache-Control", "no-store")
	setNegativeCache(rr)
	if got := rr.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store preserved", got)
	}
}

func TestMatchesETag(t *testing.T) {
	cases := []struct {
		name   string
		header string
		etag   string
		want   bool
	}{
		{"empty header", "", "abc", false},
		{"empty etag", `"abc"`, "", false},
		{"quoted exact", `"abc"`, "abc", true},
		{"bare exact", "abc", "abc", true},
		{"wildcard", "*", "abc", true},
		{"no match", `"xyz"`, "abc", false},
		{"comma list with match", `"foo", "abc"`, "abc", true},
		{"comma list with no match", `"foo", "bar"`, "abc", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			if c.header != "" {
				r.Header.Set("If-None-Match", c.header)
			}
			if got := matchesETag(r, c.etag); got != c.want {
				t.Fatalf("matchesETag(%q, %q) = %v, want %v", c.header, c.etag, got, c.want)
			}
		})
	}
}

func TestWriteNotModified(t *testing.T) {
	rr := httptest.NewRecorder()
	writeNotModified(rr, "abc123")
	if rr.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", rr.Code)
	}
	if got := rr.Header().Get("ETag"); got != `"abc123"` {
		t.Fatalf("ETag = %q, want %q", got, `"abc123"`)
	}
	if got := rr.Header().Get("Cache-Control"); got != cacheControlContentAddressed {
		t.Fatalf("Cache-Control = %q, want %q", got, cacheControlContentAddressed)
	}
	if rr.Body.Len() != 0 {
		t.Fatalf("body len = %d, want 0", rr.Body.Len())
	}
}

func TestThreadPageETagComposite(t *testing.T) {
	if got := threadPageETag("abc", 0); got != "abc-r0" {
		t.Fatalf("threadPageETag(abc, 0) = %q", got)
	}
	if got := threadPageETag("abc", 7); got != "abc-r7" {
		t.Fatalf("threadPageETag(abc, 7) = %q", got)
	}
	if got := threadPageETag("", 5); got != "" {
		t.Fatalf("threadPageETag(empty) = %q, want empty", got)
	}
}
