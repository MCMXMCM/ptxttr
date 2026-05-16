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
