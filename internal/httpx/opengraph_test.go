package httpx

import (
	"crypto/tls"
	"net/http/httptest"
	"strings"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

func TestCanonicalRequestURLDefaultsToHTTP(t *testing.T) {
	r := httptest.NewRequest("GET", "/thread/abc", nil)
	r.Host = "example.test"
	got := canonicalRequestURL(r)
	if got != "http://example.test/thread/abc" {
		t.Fatalf("canonicalRequestURL = %q", got)
	}
}

func TestCanonicalRequestURLHonoursForwardedProto(t *testing.T) {
	r := httptest.NewRequest("GET", "/thread/abc", nil)
	r.Host = "example.test"
	r.Header.Set("X-Forwarded-Proto", "https")
	got := canonicalRequestURL(r)
	if got != "https://example.test/thread/abc" {
		t.Fatalf("canonicalRequestURL = %q", got)
	}
}

func TestCanonicalRequestURLHonoursForwardedHost(t *testing.T) {
	r := httptest.NewRequest("GET", "/u/abc", nil)
	r.Host = "internal.local"
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "public.example.com")
	got := canonicalRequestURL(r)
	if got != "https://public.example.com/u/abc" {
		t.Fatalf("canonicalRequestURL = %q", got)
	}
}

func TestCanonicalRequestURLHTTPSWhenTLSPresent(t *testing.T) {
	r := httptest.NewRequest("GET", "/thread/abc", nil)
	r.Host = "example.test"
	r.TLS = &tls.ConnectionState{} // mark request as TLS-terminated locally
	got := canonicalRequestURL(r)
	if got != "https://example.test/thread/abc" {
		t.Fatalf("canonicalRequestURL = %q", got)
	}
}

func TestAbsoluteURLPassesThroughExternal(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "example.test"
	if got := absoluteURL(r, "https://cdn.example.com/foo.png"); got != "https://cdn.example.com/foo.png" {
		t.Fatalf("absoluteURL = %q", got)
	}
}

func TestAbsoluteURLJoinsRelativePath(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "example.test"
	r.Header.Set("X-Forwarded-Proto", "https")
	if got := absoluteURL(r, "/static/img/og.png"); got != "https://example.test/static/img/og.png" {
		t.Fatalf("absoluteURL = %q", got)
	}
}

func TestShortenForOGCollapsesWhitespace(t *testing.T) {
	got := shortenForOG("hello\n\nworld\t\twith   spaces", 100)
	if got != "hello world with spaces" {
		t.Fatalf("shortenForOG = %q", got)
	}
}

func TestShortenForOGTruncatesOnWordBoundary(t *testing.T) {
	in := strings.Repeat("hello world ", 30) // > 240 chars
	got := shortenForOG(in, 50)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected ellipsis suffix, got %q", got)
	}
	// The truncated body should be <= 50 runes plus the ellipsis.
	if len(got) > 60 {
		t.Fatalf("shortenForOG length = %d, want <= 60", len(got))
	}
	if strings.Contains(got, "  ") {
		t.Fatalf("expected single-spaces, got %q", got)
	}
}

func TestShortenForOGEmpty(t *testing.T) {
	if got := shortenForOG("   \n\t  ", 100); got != "" {
		t.Fatalf("shortenForOG(blank) = %q", got)
	}
}

func TestHomeOGFields(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "example.test"
	og := homeOG(r)
	if og.Type != ogTypeWebsite {
		t.Fatalf("og.Type = %q", og.Type)
	}
	if og.Title == "" || og.Description == "" || og.Image == "" {
		t.Fatalf("homeOG should populate Title, Description, Image: %+v", og)
	}
	if !strings.HasPrefix(og.Image, "http://example.test/") {
		t.Fatalf("og.Image = %q, want absolute on host", og.Image)
	}
}

func TestThreadOGUsesAuthorAndContent(t *testing.T) {
	r := httptest.NewRequest("GET", "/thread/abc", nil)
	r.Host = "example.test"
	event := nostrx.Event{
		ID:      "abc",
		PubKey:  testHexPubkey,
		Content: "Hello world from a Nostr note that should appear in the description.",
	}
	profiles := map[string]nostrx.Profile{
		testHexPubkey: {
			PubKey:  testHexPubkey,
			Display: "Alice",
			Picture: "https://cdn.example.com/alice.png",
		},
	}
	og := threadOG(r, event, profiles)
	if og.Type != ogTypeArticle {
		t.Fatalf("Type = %q", og.Type)
	}
	if !strings.Contains(og.Title, "Alice") {
		t.Fatalf("Title %q missing author", og.Title)
	}
	if !strings.Contains(og.Description, "Hello world") {
		t.Fatalf("Description %q missing content excerpt", og.Description)
	}
	if og.Image == "" {
		t.Fatalf("Image empty")
	}
	// The thread OG card should be served from the dynamic /og/<id>.png
	// renderer so the preview reflects the actual note text, not just
	// the author's avatar.
	if !strings.Contains(og.Image, "/og/"+event.ID+".png") {
		t.Fatalf("expected /og/<id>.png URL, got %q", og.Image)
	}
	if og.Author != "Alice" {
		t.Fatalf("Author = %q", og.Author)
	}
}

func TestThreadOGUsesDirectImageForImageOnlyNote(t *testing.T) {
	r := httptest.NewRequest("GET", "/thread/abc", nil)
	r.Host = "example.test"
	imageURL := "https://cdn.example.com/photo.jpg?name=hello"
	event := nostrx.Event{
		ID:      "abc",
		PubKey:  testHexPubkey,
		Content: imageURL,
	}
	profiles := map[string]nostrx.Profile{
		testHexPubkey: {
			PubKey:  testHexPubkey,
			Display: "Alice",
		},
	}

	og := threadOG(r, event, profiles)

	if og.Image != imageURL {
		t.Fatalf("Image = %q, want direct media URL %q", og.Image, imageURL)
	}
	if strings.Contains(og.Title, imageURL) {
		t.Fatalf("Title should not include raw media URL: %q", og.Title)
	}
	if strings.Contains(og.Description, imageURL) {
		t.Fatalf("Description should not include raw media URL: %q", og.Description)
	}
}

func TestThreadOGUsesDirectImageForTextWithImage(t *testing.T) {
	r := httptest.NewRequest("GET", "/thread/abc", nil)
	r.Host = "example.test"
	imageURL := "https://cdn.example.com/photo.jpg"
	event := nostrx.Event{
		ID:      "abc",
		PubKey:  testHexPubkey,
		Content: "look at this\n" + imageURL,
	}

	og := threadOG(r, event, nil)

	if og.Image != imageURL {
		t.Fatalf("Image = %q, want direct media URL %q", og.Image, imageURL)
	}
	if !strings.Contains(og.Title, "look at this") {
		t.Fatalf("Title should include visible note text, got %q", og.Title)
	}
	if og.Description != "look at this" {
		t.Fatalf("Description = %q, want visible note text", og.Description)
	}
	if strings.Contains(og.Title, imageURL) {
		t.Fatalf("Title should not include raw media URL: %q", og.Title)
	}
	if strings.Contains(og.Description, imageURL) {
		t.Fatalf("Description should not include raw media URL: %q", og.Description)
	}
}

func TestThreadOGTruncatesLongTextWithImage(t *testing.T) {
	r := httptest.NewRequest("GET", "/thread/abc", nil)
	r.Host = "example.test"
	imageURL := "https://cdn.example.com/photo.jpg"
	event := nostrx.Event{
		ID:      "abc",
		PubKey:  testHexPubkey,
		Content: strings.Repeat("hello world ", 40) + imageURL,
	}

	og := threadOG(r, event, nil)

	if og.Image != imageURL {
		t.Fatalf("Image = %q, want direct media URL %q", og.Image, imageURL)
	}
	if strings.Contains(og.Description, imageURL) {
		t.Fatalf("Description should not include raw media URL: %q", og.Description)
	}
	if !strings.HasSuffix(og.Description, "…") {
		t.Fatalf("Description should be truncated with ellipsis, got %q", og.Description)
	}
}

func TestThreadOGUsesImetaImageForEmptyImageOnlyNote(t *testing.T) {
	r := httptest.NewRequest("GET", "/thread/abc", nil)
	r.Host = "example.test"
	imageURL := "https://cdn.example.com/photo.png"
	event := nostrx.Event{
		ID:      "abc",
		PubKey:  testHexPubkey,
		Content: "",
		Tags: [][]string{
			{"imeta", "url " + imageURL, "m image/png"},
		},
	}

	og := threadOG(r, event, nil)

	if og.Image != imageURL {
		t.Fatalf("Image = %q, want imeta image URL %q", og.Image, imageURL)
	}
}

func TestThreadOGFallsBackWithoutProfile(t *testing.T) {
	r := httptest.NewRequest("GET", "/thread/abc", nil)
	r.Host = "example.test"
	event := nostrx.Event{
		ID:     "abc",
		PubKey: testHexPubkey,
	}
	og := threadOG(r, event, nil)
	if og.Title == "" {
		t.Fatalf("Title empty")
	}
	if og.Image == "" {
		t.Fatalf("Image empty (default should be applied)")
	}
}
