package nostrx

import "testing"

func TestParseNIP05Identifier(t *testing.T) {
	id, ok := ParseNIP05Identifier("Alice@Example.com")
	if !ok || id.LocalPart != "alice" || id.Domain != "example.com" {
		t.Fatalf("parsed = %#v ok=%v", id, ok)
	}
	if _, ok := ParseNIP05Identifier("bad identifier"); ok {
		t.Fatal("expected invalid identifier")
	}
}

func TestVerifyNIP05Document(t *testing.T) {
	pubkey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	doc := NIP05WellKnownDocument{Names: map[string]string{"alice": pubkey}}
	if got := VerifyNIP05Document(doc, "alice", pubkey); got != NIP05Verified {
		t.Fatalf("status = %q, want verified", got)
	}
	if got := VerifyNIP05Document(doc, "alice", "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"); got != NIP05PubkeyMismatch {
		t.Fatalf("status = %q, want pubkeyMismatch", got)
	}
	if got := VerifyNIP05Document(doc, "bob", pubkey); got != NIP05NameNotFound {
		t.Fatalf("status = %q, want nameNotFound", got)
	}
}

func TestNIP05WellKnownURL(t *testing.T) {
	id, ok := ParseNIP05Identifier("bob@example.org")
	if !ok {
		t.Fatal("parse failed")
	}
	u, ok := id.WellKnownURL()
	if !ok || u.String() != "https://example.org/.well-known/nostr.json?name=bob" {
		t.Fatalf("url = %q ok=%v", u, ok)
	}
}
