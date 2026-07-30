package httpx

import (
	"strings"
	"testing"
)

func TestTreeMediaFieldsImetaMerge(t *testing.T) {
	content := "see attached"
	tags := [][]string{
		{"imeta", "url https://cdn.example.com/abc123.png", "m image/png"},
	}
	info := treeMediaFields(content, tags)
	if info.Label == "" {
		t.Fatal("expected media label")
	}
	if !strings.Contains(info.ItemsJSON, "https://cdn.example.com/abc123.png") {
		t.Fatalf("ItemsJSON missing url: %q", info.ItemsJSON)
	}
	if !strings.Contains(strings.ToLower(info.ItemsJSON), `"type":"image"`) {
		t.Fatalf("ItemsJSON missing image type: %q", info.ItemsJSON)
	}
}

func TestImetaMediaItemsJSON(t *testing.T) {
	tags := [][]string{
		{"p", "abc"},
		{"imeta", "url https://x.test/h.jpg", "m image/jpeg"},
	}
	s := imetaMediaItemsJSON(tags)
	if !strings.Contains(s, "https://x.test/h.jpg") {
		t.Fatalf("got %q", s)
	}
}

func TestImetaMediaItemsJSONAcceptsShorthandMime(t *testing.T) {
	tags := [][]string{
		{"imeta", "url https://x.test/h.jpg", "m jpeg"},
		{"imeta", "url https://x.test/v.mp4", "m mp4"},
	}
	s := imetaMediaItemsJSON(tags)
	if !strings.Contains(s, `"type":"image"`) || !strings.Contains(s, `"type":"video"`) {
		t.Fatalf("expected shorthand mime types to be recognized, got %q", s)
	}
}

func TestImetaMediaItemsJSONIncludesValidDimensions(t *testing.T) {
	tags := [][]string{
		{"imeta", "url https://x.test/photo.jpg", "m image/jpeg", "dim 1200x800"},
	}
	s := imetaMediaItemsJSON(tags)
	if !strings.Contains(s, `"width":1200`) || !strings.Contains(s, `"height":800`) {
		t.Fatalf("expected dimensions in media JSON, got %q", s)
	}
}

func TestImetaMediaItemsJSONIgnoresInvalidDimensions(t *testing.T) {
	tags := [][]string{
		{"imeta", "url https://x.test/photo.jpg", "m image/jpeg", "dim 0x800"},
	}
	s := imetaMediaItemsJSON(tags)
	if strings.Contains(s, `"width"`) || strings.Contains(s, `"height"`) {
		t.Fatalf("expected invalid dimensions to be omitted, got %q", s)
	}
}

func TestMediaGridHelpersMatchClientContract(t *testing.T) {
	items := []treeMediaItem{
		{URL: "https://x.test/a.jpg", Type: "image", Width: 1200, Height: 800},
		{URL: "https://x.test/b.mp4", Type: "video"},
	}
	if got, want := mediaGridClass(items), "note-media-grid note-media-grid-2"; got != want {
		t.Fatalf("mediaGridClass() = %q, want %q", got, want)
	}
	if got, want := mediaGridSignature(items), "image:https://x.test/a.jpg|video:https://x.test/b.mp4"; got != want {
		t.Fatalf("mediaGridSignature() = %q, want %q", got, want)
	}
	if got := mediaGridAspectRatio(items); got != "" {
		t.Fatalf("multi-item aspect ratio = %q, want empty", got)
	}
	if got, want := mediaGridAspectRatio(items[:1]), "1200 / 800"; got != want {
		t.Fatalf("single-item aspect ratio = %q, want %q", got, want)
	}

	seven := append(append([]treeMediaItem(nil), items...), items...)
	seven = append(seven, items...)
	seven = append(seven, treeMediaItem{URL: "https://x.test/c.jpg", Type: "image"})
	if got := len(mediaGridVisibleItems(seven)); got != 5 {
		t.Fatalf("visible item count = %d, want 5 for overflow grid", got)
	}
}

func TestTreeMediaFieldsPrefersCanonicalImetaBlossomURL(t *testing.T) {
	content := "https://@Karnage.blossom.band/15441956fd94a71f03ddc36744910607b8326256b04c4f4f185ad7a5e7ad56d0.png"
	tags := [][]string{
		{"imeta", "url https://npub1example.blossom.band/15441956fd94a71f03ddc36744910607b8326256b04c4f4f185ad7a5e7ad56d0.png", "m image/png"},
	}
	info := treeMediaFields(content, tags)
	if !strings.Contains(info.ItemsJSON, "https://npub1example.blossom.band/15441956fd94a71f03ddc36744910607b8326256b04c4f4f185ad7a5e7ad56d0.png") {
		t.Fatalf("expected canonical imeta blossom url, got %q", info.ItemsJSON)
	}
	if strings.Contains(info.ItemsJSON, "https://@Karnage.blossom.band/15441956fd94a71f03ddc36744910607b8326256b04c4f4f185ad7a5e7ad56d0.png") {
		t.Fatalf("expected display-only blossom url to be replaced, got %q", info.ItemsJSON)
	}
}

func TestTreeMediaFieldsTagsNil(t *testing.T) {
	info := treeMediaFields("https://z/z.png", nil)
	if info.Label == "" {
		t.Fatal("expected label for url in content")
	}
}

func TestImetaMediaItemsJSONIgnoresNonHTTPURL(t *testing.T) {
	tags := [][]string{
		{"imeta", "url javascript:alert(1)", "m image/png"},
	}
	if imetaMediaItemsJSON(tags) != "" {
		t.Fatal("expected non-http(s) imeta url to be ignored")
	}
}
