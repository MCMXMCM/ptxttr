package httpx

import (
	"strings"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

func TestFilterEventsByViewerMutedSet_caseInsensitiveHex(t *testing.T) {
	const lower = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	upper := strings.ToUpper(lower)
	srv := &Server{}
	muted := map[string]struct{}{lower: {}}
	ev := nostrx.Event{PubKey: upper, ID: "1", Kind: nostrx.KindTextNote}
	out := srv.filterEventsByViewerMutedSet([]nostrx.Event{ev}, muted)
	if len(out) != 0 {
		t.Fatalf("expected muted author filtered out, got %d events", len(out))
	}
}

func TestAuthorPubkeyForMuteLookup(t *testing.T) {
	const hex = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if got := authorPubkeyForMuteLookup("  " + strings.ToUpper(hex) + "  "); got != hex {
		t.Fatalf("got %q want %q", got, hex)
	}
}
