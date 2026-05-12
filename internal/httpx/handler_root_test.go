package httpx

import (
	"strings"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

const (
	testHexPubkey  = "fa984bd7dbb282f07e16e7ae87b26a2a7b9b90b7246a44771f0cf5ae58018f52"
	testHexEventID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

func TestTryNip19RedirectNPub(t *testing.T) {
	npub := nostrx.EncodeNPub(testHexPubkey)
	if npub == "" {
		t.Fatal("EncodeNPub returned empty")
	}
	got, ok := tryNip19Redirect(npub)
	if !ok {
		t.Fatalf("tryNip19Redirect(%q) ok = false", npub)
	}
	want := "/u/" + testHexPubkey
	if got != want {
		t.Fatalf("tryNip19Redirect(npub) = %q, want %q", got, want)
	}
}

func TestTryNip19RedirectNProfile(t *testing.T) {
	nprofile := nostrx.EncodeNProfile(testHexPubkey, []string{"wss://relay.primal.net"})
	if nprofile == "" {
		t.Fatal("EncodeNProfile returned empty")
	}
	got, ok := tryNip19Redirect(nprofile)
	if !ok {
		t.Fatalf("tryNip19Redirect(%q) ok = false", nprofile)
	}
	want := "/u/" + testHexPubkey
	if got != want {
		t.Fatalf("tryNip19Redirect(nprofile) = %q, want %q", got, want)
	}
}

func TestTryNip19RedirectNEvent(t *testing.T) {
	nevent := nostrx.EncodeNEvent(testHexEventID, testHexPubkey)
	if nevent == "" || nevent == testHexEventID {
		t.Fatalf("EncodeNEvent returned %q", nevent)
	}
	got, ok := tryNip19Redirect(nevent)
	if !ok {
		t.Fatalf("tryNip19Redirect(%q) ok = false", nevent)
	}
	want := "/thread/" + testHexEventID
	if got != want {
		t.Fatalf("tryNip19Redirect(nevent) = %q, want %q", got, want)
	}
}

func TestTryNip19RedirectNeventLongFormKind(t *testing.T) {
	nevent := nostrx.EncodeNEventWithKind(testHexEventID, testHexPubkey, nostrx.KindLongForm)
	if nevent == "" {
		t.Fatal("EncodeNEventWithKind returned empty")
	}
	got, ok := tryNip19Redirect(nevent)
	if !ok {
		t.Fatalf("tryNip19Redirect(nevent+kind) ok = false")
	}
	want := "/reads/" + testHexEventID
	if got != want {
		t.Fatalf("tryNip19Redirect(nevent long-form) = %q, want %q", got, want)
	}
}

func TestTryNip19RedirectStripNostrPrefix(t *testing.T) {
	npub := nostrx.EncodeNPub(testHexPubkey)
	got, ok := tryNip19Redirect("nostr:" + npub)
	if !ok {
		t.Fatalf("tryNip19Redirect(nostr:npub) ok = false")
	}
	if got != "/u/"+testHexPubkey {
		t.Fatalf("tryNip19Redirect(nostr:npub) = %q", got)
	}
}

func TestTryNip19RedirectBareHexAsEventID(t *testing.T) {
	got, ok := tryNip19Redirect(testHexEventID)
	if !ok {
		t.Fatalf("tryNip19Redirect(64-hex) ok = false")
	}
	want := "/thread/" + testHexEventID
	if got != want {
		t.Fatalf("tryNip19Redirect(64-hex) = %q, want %q", got, want)
	}
}

func TestTryNip19RedirectMixedCaseHex(t *testing.T) {
	upper := strings.ToUpper(testHexEventID)
	got, ok := tryNip19Redirect(upper)
	if !ok {
		t.Fatalf("tryNip19Redirect(upper-hex) ok = false")
	}
	if got != "/thread/"+testHexEventID {
		t.Fatalf("tryNip19Redirect(upper-hex) = %q, want lowercased path", got)
	}
}

func TestTryNip19RedirectRejectsUnknownSegment(t *testing.T) {
	rejects := []string{
		"",
		"feed",
		"login",
		"foo bar",
		"npub-not-a-real-code",
		"abc.css",
		"abc/def",
		"abc?x=1",
		"favicon.ico",
		strings.Repeat("g", 64), // hex-length but non-hex char
		strings.Repeat("a", 63), // not 64
	}
	for _, in := range rejects {
		t.Run(in, func(t *testing.T) {
			if _, ok := tryNip19Redirect(in); ok {
				t.Fatalf("tryNip19Redirect(%q) ok = true, want false", in)
			}
		})
	}
}
