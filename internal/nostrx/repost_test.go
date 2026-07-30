package nostrx

import (
	"strings"
	"testing"
)

func TestParseEmbeddedRepost(t *testing.T) {
	noteID := "fdffc1e0f60c1cfd45356bc5a95f5308184430a5b76a2f71f2e30978250a4260"
	pubkey := "14b55cd017eb033127ab4d0c8a50cd3d80dbaf4085e2ef3f13da9b1bf44831e6"
	content := `{"kind":1,"created_at":1781459759,"pubkey":"` + pubkey + `","tags":[["imeta","url https://example.com/a.jpg","m jpeg"]],"content":"Approaching 250 years of freedom","id":"` + noteID + `"}`

	got, ok := ParseEmbeddedRepost(content, noteID)
	if !ok {
		t.Fatal("expected embedded repost to parse")
	}
	if got.ID != noteID {
		t.Fatalf("id = %q, want %q", got.ID, noteID)
	}
	if got.PubKey != pubkey {
		t.Fatalf("pubkey = %q, want %q", got.PubKey, pubkey)
	}
	if got.Content != "Approaching 250 years of freedom" {
		t.Fatalf("content = %q", got.Content)
	}

	if _, ok := ParseEmbeddedRepost(content, strings.Repeat("a", 64)); ok {
		t.Fatal("expected id mismatch to fail")
	}
	if _, ok := ParseEmbeddedRepost("", noteID); ok {
		t.Fatal("expected empty content to fail")
	}
	if _, ok := ParseEmbeddedRepost(`{"kind":6,"id":"`+noteID+`","content":""}`, noteID); ok {
		t.Fatal("expected kind 6 embedded event to fail")
	}
}
