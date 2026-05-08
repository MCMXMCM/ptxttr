package httpx

import (
	"strings"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

func TestRewriteASCIIMentionsReplacesNProfileWithDisplayName(t *testing.T) {
	pubkey := "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	encoded := nostrx.EncodeNProfile(pubkey, []string{"wss://relay.example"})
	if encoded == "" {
		t.Fatalf("EncodeNProfile() returned empty string")
	}
	content := "hi nostr:" + encoded + " welcome"
	profiles := map[string]nostrx.Profile{
		pubkey: {PubKey: pubkey, Display: "Paul Keating"},
	}
	rewritten, mentions := RewriteASCIIMentions(content, profiles)
	want := "hi @Paul Keating welcome"
	if rewritten != want {
		t.Fatalf("rewritten = %q, want %q", rewritten, want)
	}
	if len(mentions) != 1 {
		t.Fatalf("len(mentions) = %d, want 1", len(mentions))
	}
	if mentions[0].Label != "@Paul Keating" || mentions[0].Href != "/u/"+pubkey {
		t.Fatalf("mention = %+v, want label=@Paul Keating href=/u/%s", mentions[0], pubkey)
	}
}

func TestRewriteASCIIMentionsShortensNeventToNoteShort(t *testing.T) {
	eventID := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	encoded := nostrx.EncodeNEvent(eventID, "")
	if encoded == "" {
		t.Fatalf("EncodeNEvent returned empty")
	}
	content := "see nostr:" + encoded
	rewritten, mentions := RewriteASCIIMentions(content, nil)
	if !strings.HasPrefix(rewritten, "see note:") {
		t.Fatalf("rewritten = %q, want prefix 'see note:'", rewritten)
	}
	if len(mentions) != 1 {
		t.Fatalf("len(mentions) = %d, want 1", len(mentions))
	}
	if mentions[0].Href != "/thread/"+eventID {
		t.Fatalf("href = %s, want /thread/%s", mentions[0].Href, eventID)
	}
	if !strings.HasPrefix(mentions[0].Label, "note:") {
		t.Fatalf("label = %q, want prefix 'note:'", mentions[0].Label)
	}
	// The shortened token must be substantially shorter than the original
	// so column-wrapped clients can render it without splitting.
	if len(mentions[0].Label) > 24 {
		t.Fatalf("label too long: %q", mentions[0].Label)
	}
}

func TestRewriteASCIIMentionsNeventLongFormLinksToReads(t *testing.T) {
	eventID := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	pk := "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	encoded := nostrx.EncodeNEventWithKind(eventID, pk, nostrx.KindLongForm)
	if encoded == "" {
		t.Fatal("EncodeNEventWithKind returned empty")
	}
	content := "see nostr:" + encoded
	_, mentions := RewriteASCIIMentions(content, nil)
	if len(mentions) != 1 {
		t.Fatalf("len(mentions) = %d, want 1", len(mentions))
	}
	if mentions[0].Href != "/reads/"+eventID {
		t.Fatalf("href = %s, want /reads/%s", mentions[0].Href, eventID)
	}
	if !strings.HasPrefix(mentions[0].Label, "note:") {
		t.Fatalf("label = %q, want prefix 'note:'", mentions[0].Label)
	}
	if len(mentions[0].Label) > 24 {
		t.Fatalf("label too long: %q", mentions[0].Label)
	}
}

func TestRewriteASCIIMentionsLeavesContentWithoutRefsUntouched(t *testing.T) {
	got, mentions := RewriteASCIIMentions("nothing to see here", nil)
	if got != "nothing to see here" {
		t.Fatalf("rewritten = %q", got)
	}
	if mentions != nil {
		t.Fatalf("mentions = %+v, want nil", mentions)
	}
}
