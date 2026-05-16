package httpx

import (
	"context"
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

func TestMuteListSaveProjectsMutedPubkeys(t *testing.T) {
	_, st := testServer(t)
	ctx := context.Background()
	viewer := strings.Repeat("a", 64)
	muted := strings.Repeat("b", 64)
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        strings.Repeat("c", 64),
		PubKey:    viewer,
		CreatedAt: 100,
		Kind:      nostrx.KindMuteList,
		Tags:      [][]string{{"p", muted}},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := st.MutedPubkeys(ctx, viewer, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != muted {
		t.Fatalf("muted pubkeys = %#v, want [%q]", got, muted)
	}
}

func TestAuthorPubkeyForMuteLookup(t *testing.T) {
	const hex = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if got := authorPubkeyForMuteLookup("  " + strings.ToUpper(hex) + "  "); got != hex {
		t.Fatalf("got %q want %q", got, hex)
	}
}
