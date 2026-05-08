package nostrx

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCanonicalHex64(t *testing.T) {
	lower := strings.Repeat("a", 64)
	upper := strings.ToUpper(lower)
	if got := CanonicalHex64(upper); got != lower {
		t.Fatalf("CanonicalHex64(upper) = %q, want %q", got, lower)
	}
	if got := CanonicalHex64("  " + lower + "  "); got != lower {
		t.Fatalf("CanonicalHex64(trimmed) = %q, want %q", got, lower)
	}
	if got := CanonicalHex64("not-hex"); got != "not-hex" {
		t.Fatalf("CanonicalHex64(non-hex) = %q", got)
	}
}

func TestNormalizePubKey(t *testing.T) {
	pubkey := "FA984BD7DBB282F07E16E7AE87B26A2A7B9B90B7246A44771F0CF5AE58018F52"
	got, err := NormalizePubKey(pubkey)
	if err != nil {
		t.Fatal(err)
	}
	if got != "fa984bd7dbb282f07e16e7ae87b26a2a7b9b90b7246a44771f0cf5ae58018f52" {
		t.Fatalf("unexpected pubkey: %s", got)
	}
}

func TestNormalizeRelayURL(t *testing.T) {
	got, err := NormalizeRelayURL("wss://relay.primal.net/")
	if err != nil {
		t.Fatal(err)
	}
	if got != "wss://relay.primal.net" {
		t.Fatalf("unexpected relay URL: %s", got)
	}
}

func TestNormalizeRelayURLRejectsNonWebsocketURLs(t *testing.T) {
	for _, raw := range []string{"", "https://relay.primal.net", "relay.primal.net"} {
		if _, err := NormalizeRelayURL(raw); err == nil {
			t.Fatalf("expected %q to be rejected", raw)
		}
	}
}

func TestNormalizeRelayList(t *testing.T) {
	got := NormalizeRelayList([]string{
		"wss://relay.primal.net/",
		"not-a-relay",
		"wss://relay.primal.net",
		"wss://relay.nostr.wine",
	}, 2)
	if len(got) != 2 {
		t.Fatalf("expected two relays, got %#v", got)
	}
	if got[0] != "wss://relay.primal.net" || got[1] != "wss://relay.nostr.wine" {
		t.Fatalf("unexpected normalized relays: %#v", got)
	}
}

func TestParseRelayParamsSplitsRepeatedAndCommaValues(t *testing.T) {
	got := ParseRelayParams([]string{" wss://one.example, wss://two.example ", "", "wss://three.example"})
	want := []string{"wss://one.example", "wss://two.example", "wss://three.example"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("ParseRelayParams() = %#v, want %#v", got, want)
	}
}

func TestDedupeEventsSortsNewestBeforeLimit(t *testing.T) {
	got := DedupeEvents([]Event{
		{ID: "old", CreatedAt: 10, RelayURL: "wss://one.example"},
		{ID: "new", CreatedAt: 30, RelayURL: "wss://one.example"},
		{ID: "new", CreatedAt: 30, RelayURL: "wss://two.example"},
		{ID: ""},
		{ID: "middle", CreatedAt: 20, RelayURL: "wss://three.example"},
	}, 2)
	if len(got) != 2 {
		t.Fatalf("expected limit to keep two events, got %#v", got)
	}
	if got[0].ID != "new" || got[0].RelayURL != "wss://one.example" || got[1].ID != "middle" {
		t.Fatalf("unexpected dedupe result: %#v", got)
	}
}

func TestFollowPubkeysExtractsUniqueValidTags(t *testing.T) {
	alice := strings.Repeat("a", 64)
	bobUpper := strings.Repeat("B", 64)
	bob := strings.ToLower(bobUpper)
	event := Event{
		Kind: KindFollowList,
		Tags: [][]string{
			{"p", alice},
			{"p", alice, "wss://relay.example"},
			{"e", strings.Repeat("c", 64)},
			{"p", "not-a-pubkey"},
			{"p", bobUpper},
		},
	}
	got := FollowPubkeys(&event)
	if len(got) != 2 || got[0] != alice || got[1] != bob {
		t.Fatalf("unexpected follow list: %#v", got)
	}
}

func TestBookmarkEventIDsExtractsUniqueHexEventIDs(t *testing.T) {
	first := strings.Repeat("a", 64)
	secondUpper := strings.Repeat("B", 64)
	second := strings.ToLower(secondUpper)
	event := Event{
		Kind: KindBookmarkList,
		Tags: [][]string{
			{"e", first},
			{"e", first, "wss://relay.one"},
			{"e", secondUpper, "wss://relay.two"},
			{"a", "30023:pubkey:identifier"},
			{"e", "not-an-event-id"},
		},
	}
	got := BookmarkEventIDs(&event, 10)
	if strings.Join(got, ",") != strings.Join([]string{first, second}, ",") {
		t.Fatalf("BookmarkEventIDs() = %#v, want %#v", got, []string{first, second})
	}
}

func TestRelayURLsExtractsNormalizedRelayList(t *testing.T) {
	event := Event{
		Kind: KindRelayListMetadata,
		Tags: [][]string{
			{"r", "wss://relay.primal.net/"},
			{"r", "wss://relay.primal.net"},
			{"r", "https://relay.invalid"},
			{"p", strings.Repeat("a", 64)},
			{"r", "wss://relay.nostr.wine"},
		},
	}
	got := RelayURLs(&event, 4)
	if len(got) != 2 || got[0] != "wss://relay.primal.net" || got[1] != "wss://relay.nostr.wine" {
		t.Fatalf("unexpected relay list: %#v", got)
	}
}

func TestRelayHintsPreserveReadWriteMarkers(t *testing.T) {
	event := Event{
		Kind: KindRelayListMetadata,
		Tags: [][]string{
			{"r", "wss://write.example", "write"},
			{"r", "wss://read.example", "read"},
			{"r", "wss://both.example"},
			{"r", "wss://write.example", "read"},
		},
	}
	hints := RelayHints(&event, 0)
	if len(hints) != 3 {
		t.Fatalf("RelayHints() len = %d, want 3", len(hints))
	}
	if !hints[0].Write || !hints[0].Read {
		t.Fatalf("expected merged read+write flags for duplicate relay, got %#v", hints[0])
	}
	if !hints[1].Read || hints[1].Write {
		t.Fatalf("expected read-only relay, got %#v", hints[1])
	}
	if !hints[2].Read || !hints[2].Write {
		t.Fatalf("expected default relay to be both read/write, got %#v", hints[2])
	}
}

func TestRelayURLsForUsageFiltersByMarker(t *testing.T) {
	event := Event{
		Kind: KindRelayListMetadata,
		Tags: [][]string{
			{"r", "wss://write.example", "write"},
			{"r", "wss://read.example", "read"},
			{"r", "wss://both.example"},
		},
	}
	writeOnly := RelayURLsForUsage(&event, 10, RelayUsageWrite)
	if strings.Join(writeOnly, ",") != "wss://write.example,wss://both.example" {
		t.Fatalf("write relays = %#v", writeOnly)
	}
	readOnly := RelayURLsForUsage(&event, 10, RelayUsageRead)
	if strings.Join(readOnly, ",") != "wss://read.example,wss://both.example" {
		t.Fatalf("read relays = %#v", readOnly)
	}
}

func TestFollowRelayHintsExtractsContactRelayHints(t *testing.T) {
	event := Event{
		Kind: KindFollowList,
		Tags: [][]string{
			{"p", strings.Repeat("a", 64), "wss://relay.one"},
			{"p", strings.Repeat("A", 64), "wss://relay.one/"},
			{"p", strings.Repeat("b", 64), "https://bad.example"},
			{"p", strings.Repeat("c", 64)},
		},
	}
	hints := FollowRelayHints(&event, 10)
	if len(hints) != 1 {
		t.Fatalf("expected one deduped hint, got %#v", hints)
	}
	if hints[0].PubKey != strings.Repeat("a", 64) || hints[0].Relay != "wss://relay.one" {
		t.Fatalf("unexpected hint value: %#v", hints[0])
	}
}

func TestNPubRoundTrip(t *testing.T) {
	pubkey := "fa984bd7dbb282f07e16e7ae87b26a2a7b9b90b7246a44771f0cf5ae58018f52"
	npub := EncodeNPub(pubkey)
	got, err := DecodeIdentifier(npub)
	if err != nil {
		t.Fatal(err)
	}
	if got != pubkey {
		t.Fatalf("got %s, want %s", got, pubkey)
	}
}

func TestDecodeIdentifierAcceptsRawHex(t *testing.T) {
	pubkey := "FA984BD7DBB282F07E16E7AE87B26A2A7B9B90B7246A44771F0CF5AE58018F52"
	got, err := DecodeIdentifier(pubkey)
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.ToLower(pubkey) {
		t.Fatalf("DecodeIdentifier(hex) = %s, want %s", got, strings.ToLower(pubkey))
	}
}

func TestDecodeIdentifierAcceptsNProfile(t *testing.T) {
	pubkey := "fa984bd7dbb282f07e16e7ae87b26a2a7b9b90b7246a44771f0cf5ae58018f52"
	nprofile := EncodeNProfile(pubkey, []string{"wss://relay.primal.net"})
	got, err := DecodeIdentifier("nostr:" + nprofile)
	if err != nil {
		t.Fatal(err)
	}
	if got != pubkey {
		t.Fatalf("DecodeIdentifier(nprofile) = %s, want %s", got, pubkey)
	}
}

func TestDecodeNIP27ReferenceNeventAndNprofile(t *testing.T) {
	pubkey := "fa984bd7dbb282f07e16e7ae87b26a2a7b9b90b7246a44771f0cf5ae58018f52"
	eventID := strings.Repeat("c", 64)
	nevent := EncodeNEvent(eventID, pubkey)
	nprofile := EncodeNProfile(pubkey, []string{"wss://relay.primal.net", "wss://relay.nostr.wine"})

	eventRef, err := DecodeNIP27Reference("nostr:" + nevent)
	if err != nil {
		t.Fatal(err)
	}
	if eventRef.Kind != NIP27KindNEvent {
		t.Fatalf("eventRef.Kind = %s, want nevent", eventRef.Kind)
	}
	if eventRef.Event != eventID {
		t.Fatalf("eventRef.Event = %s, want %s", eventRef.Event, eventID)
	}
	if eventRef.PubKey != pubkey {
		t.Fatalf("eventRef.PubKey = %s, want %s", eventRef.PubKey, pubkey)
	}

	profileRef, err := DecodeNIP27Reference("nostr:" + nprofile)
	if err != nil {
		t.Fatal(err)
	}
	if profileRef.Kind != NIP27KindNProfile {
		t.Fatalf("profileRef.Kind = %s, want nprofile", profileRef.Kind)
	}
	if profileRef.PubKey != pubkey {
		t.Fatalf("profileRef.PubKey = %s, want %s", profileRef.PubKey, pubkey)
	}
	if len(profileRef.Relays) == 0 || profileRef.Relays[0] != "wss://relay.primal.net" {
		t.Fatalf("profileRef.Relays unexpected: %#v", profileRef.Relays)
	}
}

func TestExtractMentionPubKeysFromNIP27Content(t *testing.T) {
	alice := strings.Repeat("a", 64)
	bob := "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	content := strings.Join([]string{
		"hello",
		"nostr:" + EncodeNPub(alice),
		"and",
		"nostr:" + EncodeNPub(bob),
		"plus invalid nostr:npub1xyz",
	}, " ")
	got := ExtractMentionPubKeys(content)
	if strings.Join(got, ",") != strings.Join([]string{alice, bob}, ",") {
		t.Fatalf("ExtractMentionPubKeys() = %#v, want [%s %s]", got, alice, bob)
	}
}

func TestExtractNIP27ReferencesCapturesBoundaries(t *testing.T) {
	alice := strings.Repeat("a", 64)
	content := "start (nostr:" + EncodeNPub(alice) + "), end."
	refs := ExtractNIP27References(content)
	if len(refs) != 1 {
		t.Fatalf("len(ExtractNIP27References()) = %d, want 1", len(refs))
	}
	if refs[0].Raw == "" || refs[0].Start < 0 || refs[0].End <= refs[0].Start {
		t.Fatalf("reference boundaries missing: %#v", refs[0])
	}
}

func TestRelayMetricsTrackPerRelayCounters(t *testing.T) {
	client := NewClient([]string{"wss://relay.example"}, 0)
	client.recordRelayAttempt("wss://relay.example")
	client.recordRelayFailure("wss://relay.example")
	client.recordRelayEvents("wss://relay.example", 3)

	metrics := client.Metrics()
	relay := metrics.Relays["wss://relay.example"]
	if relay.RelayAttempts != 1 || relay.RelayFailures != 1 || relay.EventsSeen != 3 {
		t.Fatalf("unexpected relay metrics: %#v", relay)
	}
}

func TestFilterConversionDropsInvalidValues(t *testing.T) {
	if got := idsFromHex([]string{"not-an-id"}); len(got) != 0 {
		t.Fatalf("expected invalid ids to be dropped, got %d", len(got))
	}
	if got := pubkeysFromHex([]string{"not-a-pubkey"}); len(got) != 0 {
		t.Fatalf("expected invalid pubkeys to be dropped, got %d", len(got))
	}
	if got := kindsFromInts([]int{0, 1, 10002}); len(got) != 3 {
		t.Fatalf("expected all kinds to convert, got %d", len(got))
	}
}

func TestClassifyClosedPolicyReason(t *testing.T) {
	tests := []struct {
		reason string
		kind   string
		ok     bool
	}{
		{reason: "blocked: Request rejected", kind: "blocked", ok: true},
		{reason: "requires auth", kind: "auth", ok: true},
		{reason: "too many requests", kind: "", ok: false},
	}
	for _, test := range tests {
		kind, ok := classifyClosedPolicyReason(test.reason)
		if kind != test.kind || ok != test.ok {
			t.Fatalf("classifyClosedPolicyReason(%q) = (%q,%v), want (%q,%v)", test.reason, kind, ok, test.kind, test.ok)
		}
	}
}

func TestRelayPolicyBackoffCaps(t *testing.T) {
	if got := relayPolicyBackoff(1); got != 2*time.Minute {
		t.Fatalf("relayPolicyBackoff(1) = %s, want 2m", got)
	}
	if got := relayPolicyBackoff(3); got != 8*time.Minute {
		t.Fatalf("relayPolicyBackoff(3) = %s, want 8m", got)
	}
	if got := relayPolicyBackoff(10); got != 30*time.Minute {
		t.Fatalf("relayPolicyBackoff(10) = %s, want 30m cap", got)
	}
}

func TestRelayPolicyStateExpiresAndClears(t *testing.T) {
	client := NewClient([]string{"wss://relay.example"}, time.Second)
	now := time.Now()
	client.recordRelayPolicyRejection("wss://relay.example", "auth", "challenge", now)
	if !client.relayPolicyBlocked("wss://relay.example", now.Add(time.Minute)) {
		t.Fatal("expected relay to be blocked during backoff window")
	}
	if client.relayPolicyBlocked("wss://relay.example", now.Add(3*time.Minute)) {
		t.Fatal("expected relay to unblock after backoff expires")
	}
}

func BenchmarkDedupeEventsSorted(b *testing.B) {
	events := make([]Event, 0, 400)
	for index := 0; index < 400; index++ {
		id := strings.Repeat("a", 63) + string(rune('a'+index%26))
		events = append(events, Event{ID: id, CreatedAt: int64(1000 - index)})
		if index%4 == 0 {
			events = append(events, Event{ID: id, CreatedAt: int64(1000 - index)})
		}
	}
	b.ReportAllocs()
	for range b.N {
		input := append([]Event(nil), events...)
		_ = DedupeEvents(input, 200)
	}
}

func BenchmarkRelayEnvelopeTypeParse(b *testing.B) {
	msg := []byte(`["EVENT","subid",{"id":"` + strings.Repeat("a", 64) + `","pubkey":"` + strings.Repeat("b", 64) + `","created_at":123,"kind":1,"tags":[],"content":"hello","sig":"` + strings.Repeat("c", 128) + `"}]`)
	b.ReportAllocs()
	for range b.N {
		var envelope []json.RawMessage
		if err := json.Unmarshal(msg, &envelope); err != nil {
			b.Fatal(err)
		}
		var typ string
		if err := json.Unmarshal(envelope[0], &typ); err != nil {
			b.Fatal(err)
		}
	}
}
