package httpx

import (
	"ptxt-nstr/internal/nostrx"
	"testing"
	"time"
)

func TestLimitEventsPerAuthorPrefersDiversity(t *testing.T) {
	events := []nostrx.Event{
		{ID: "a1", PubKey: "a"},
		{ID: "b1", PubKey: "b"},
		{ID: "c1", PubKey: "c"},
		{ID: "a2", PubKey: "a"},
		{ID: "b2", PubKey: "b"},
		{ID: "c2", PubKey: "c"},
		{ID: "a3", PubKey: "a"},
	}
	got := limitEventsPerAuthor(events, 2, 6)
	if len(got) != 6 {
		t.Fatalf("len(limitEventsPerAuthor()) = %d, want 6", len(got))
	}
	counts := map[string]int{}
	for _, event := range got {
		counts[event.PubKey]++
	}
	if counts["a"] != 2 || counts["b"] != 2 || counts["c"] != 2 {
		t.Fatalf("unexpected distribution: %#v", counts)
	}
}

func TestLimitEventsPerAuthorDoesNotFillFromOneAuthor(t *testing.T) {
	events := []nostrx.Event{
		{ID: "a1", PubKey: "a"},
		{ID: "a2", PubKey: "a"},
		{ID: "a3", PubKey: "a"},
		{ID: "a4", PubKey: "a"},
		{ID: "b1", PubKey: "b"},
	}
	got := limitEventsPerAuthor(events, 2, 4)
	if len(got) != 3 {
		t.Fatalf("len(limitEventsPerAuthor()) = %d, want 3", len(got))
	}
	want := []string{"a1", "a2", "b1"}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("event %d = %q, want %q", i, got[i].ID, id)
		}
	}
}

func TestLimitEventsPerAuthorNoCapUsesWindow(t *testing.T) {
	events := []nostrx.Event{
		{ID: "a1", PubKey: "a"},
		{ID: "a2", PubKey: "a"},
		{ID: "a3", PubKey: "a"},
	}
	got := limitEventsPerAuthor(events, 0, 2)
	if len(got) != 2 || got[0].ID != "a1" || got[1].ID != "a2" {
		t.Fatalf("unexpected events: %#v", got)
	}
}

func TestRequestTimeoutTracksRelayTimeoutWithSmallBuffer(t *testing.T) {
	if got := requestTimeout(2 * time.Second); got != 5*time.Second {
		t.Fatalf("requestTimeout short = %s, want 5s floor", got)
	}
	if got := requestTimeout(5 * time.Second); got != 7*time.Second {
		t.Fatalf("requestTimeout long = %s, want 7s", got)
	}
}
