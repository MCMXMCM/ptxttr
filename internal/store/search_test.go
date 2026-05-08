package store

import (
	"strings"
	"testing"
)

func TestPrepareSearch(t *testing.T) {
	query := PrepareSearch("  Nostr!!! lightning, wallet  ")
	if query.Empty() {
		t.Fatal("query.Empty() = true, want false")
	}
	if query.Normalized != "nostr lightning wallet" {
		t.Fatalf("normalized = %q, want %q", query.Normalized, "nostr lightning wallet")
	}
	if query.Match != "nostr* AND lightning* AND wallet*" {
		t.Fatalf("match = %q, want %q", query.Match, "nostr* AND lightning* AND wallet*")
	}
}

func TestPrepareSearchRejectsEmptyAfterFiltering(t *testing.T) {
	query := PrepareSearch(" a # % !")
	if !query.Empty() {
		t.Fatalf("query.Empty() = false, want true (normalized=%q)", query.Normalized)
	}
}

func TestPrepareSearchCapsLengthAndTokenCount(t *testing.T) {
	raw := strings.Repeat("abcd ", 20)
	query := PrepareSearch(raw)
	if query.Empty() {
		t.Fatal("query.Empty() = true, want false")
	}
	parts := strings.Fields(query.Normalized)
	if len(parts) != 1 {
		t.Fatalf("token dedupe mismatch: got %d tokens (%q), want 1 token", len(parts), query.Normalized)
	}

	raw = "one two three four five six seven eight nine ten"
	query = PrepareSearch(raw)
	if query.Empty() {
		t.Fatal("query.Empty() = true, want false")
	}
	parts = strings.Fields(query.Normalized)
	if len(parts) != 6 {
		t.Fatalf("token cap = %d, want 6 (%q)", len(parts), query.Normalized)
	}
}

func TestPrepareSearchSupportsUnicodeLetters(t *testing.T) {
	query := PrepareSearch("  Café 日本語 cafe  ")
	if query.Empty() {
		t.Fatal("query.Empty() = true, want false")
	}
	if query.Normalized != "café 日本語 cafe" {
		t.Fatalf("normalized = %q, want %q", query.Normalized, "café 日本語 cafe")
	}
}
