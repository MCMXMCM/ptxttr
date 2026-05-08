package nostrx

import (
	"fmt"
	"strings"
	"testing"

	fnostr "fiatjaf.com/nostr"
)

func TestValidateIngestEventHTTPAPI_AcceptsAllowedKinds(t *testing.T) {
	ev := signedTestEvent(t, KindTextNote, "hello", nil)
	if err := ValidateIngestEvent(IngestFromHTTPAPI, ev); err != nil {
		t.Fatalf("ValidateIngestEvent() = %v", err)
	}
}

func TestValidateIngestEventHTTPAPI_RejectsDisallowedKind(t *testing.T) {
	ev := signedTestEvent(t, 9999, "x", nil)
	err := ValidateIngestEvent(IngestFromHTTPAPI, ev)
	if err == nil {
		t.Fatal("expected error for disallowed kind")
	}
	if !strings.Contains(err.Error(), "not accepted") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateIngestEventHTTPAPI_RejectsOversizedContent(t *testing.T) {
	ev := signedTestEvent(t, KindTextNote, strings.Repeat("a", publishMaxContentBytes+1), nil)
	if err := ValidateIngestEvent(IngestFromHTTPAPI, ev); err == nil {
		t.Fatal("expected error for oversized content")
	}
}

func TestValidateIngestEventHTTPAPI_RejectsInvalidRelayListMetadata(t *testing.T) {
	ev := signedTestEvent(t, KindRelayListMetadata, "not-empty", [][]string{{"r", "wss://relay.example"}})
	if err := ValidateIngestEvent(IngestFromHTTPAPI, ev); err == nil {
		t.Fatal("expected error for invalid relay list metadata")
	}
}

func TestValidateIngestEventRelay_SkipsKindAllowlist(t *testing.T) {
	ev := signedTestEvent(t, 9999, "x", nil)
	ev.RelayURL = "wss://relay.example.com"
	if err := ValidateIngestEvent(IngestFromRelay, ev); err != nil {
		t.Fatalf("relay ingest should allow any kind when signed: %v", err)
	}
}

func TestIngestSourceForStoreSave(t *testing.T) {
	a := Event{RelayURL: "wss://x"}
	if IngestSourceForStoreSave(a) != IngestFromRelay {
		t.Fatalf("expected relay source")
	}
	b := Event{}
	if IngestSourceForStoreSave(b) != IngestPersisted {
		t.Fatalf("expected persisted source")
	}
}

func TestVerifyIngestEventsBeforeSave_Sequential(t *testing.T) {
	ev := signedTestEvent(t, KindTextNote, "ok", nil)
	if err := VerifyIngestEventsBeforeSave(0, []Event{ev}); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyIngestEventsBeforeSave_Parallel(t *testing.T) {
	const n = 8
	list := make([]Event, n)
	for i := 0; i < n; i++ {
		list[i] = signedTestEvent(t, KindTextNote, "m", [][]string{{"i", fmt.Sprintf("%d", i)}})
	}
	if err := VerifyIngestEventsBeforeSave(4, list); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyIngestEventsBeforeSave_ParallelFirstError(t *testing.T) {
	good := signedTestEvent(t, KindTextNote, "ok", nil)
	bad := signedTestEvent(t, KindTextNote, "tampered", nil)
	bad.Content = "nope"
	if err := VerifyIngestEventsBeforeSave(4, []Event{good, bad}); err == nil {
		t.Fatal("expected verification error")
	}
}

func TestValidateRelayIngestBatch_PreservesOrderAndDropsInvalid(t *testing.T) {
	first := signedTestEvent(t, KindTextNote, "first", nil)
	second := signedTestEvent(t, KindTextNote, "second", nil)
	bad := signedTestEvent(t, KindTextNote, "bad", nil)
	bad.Content = "tampered"

	rawFirst, err := toExternalEvent(first)
	if err != nil {
		t.Fatal(err)
	}
	rawBad, err := toExternalEvent(bad)
	if err != nil {
		t.Fatal(err)
	}
	rawSecond, err := toExternalEvent(second)
	if err != nil {
		t.Fatal(err)
	}
	got := ValidateRelayIngestBatch("wss://relay.example", []fnostr.Event{rawFirst, rawBad, rawSecond}, 4)
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].ID != first.ID || got[1].ID != second.ID {
		t.Fatalf("unexpected order after batch validate: %#v", got)
	}
	if got[0].RelayURL != "wss://relay.example" || got[1].RelayURL != "wss://relay.example" {
		t.Fatalf("expected relay url to be attached: %#v", got)
	}
}
