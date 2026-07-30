package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"ptxt-nstr/internal/nostrx"
)

func TestFeedSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	event := nostrx.Event{ID: strings.Repeat("a", 64), PubKey: strings.Repeat("b", 64), CreatedAt: time.Now().Unix(), Kind: nostrx.KindTextNote, Content: "x", Sig: "s"}
	if err := st.SaveEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	rec := &FeedSnapshotRecord{
		Version:        feedSnapshotJSONVersion,
		RelaysHash:     "rh1",
		Feed:           []nostrx.Event{event},
		ComputedAtUnix: time.Now().Unix(),
	}
	key := "test:snap:1"
	if err := st.SetFeedSnapshot(ctx, key, rec); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.GetFeedSnapshot(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || got == nil || len(got.Feed) != 1 || got.Feed[0].ID != event.ID {
		t.Fatalf("GetFeedSnapshot = ok=%v rec=%v", ok, got)
	}
}

func TestSetFeedSnapshotRejectsMissingCanonicalEvent(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	rec := &FeedSnapshotRecord{
		Feed: []nostrx.Event{{
			ID: strings.Repeat("c", 64), PubKey: strings.Repeat("d", 64),
			CreatedAt: time.Now().Unix(), Kind: nostrx.KindTextNote,
		}},
		ComputedAtUnix: time.Now().Unix(),
	}
	if err := st.SetFeedSnapshot(ctx, "test:missing", rec); !errors.Is(err, ErrFeedSnapshotMissingCanonicalEvent) {
		t.Fatalf("SetFeedSnapshot error = %v, want ErrFeedSnapshotMissingCanonicalEvent", err)
	}
}

func TestPruneEventsInvalidatesFeedSnapshots(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	old := nostrx.Event{ID: strings.Repeat("1", 64), PubKey: strings.Repeat("a", 64), CreatedAt: 1, Kind: nostrx.KindTextNote}
	newer := nostrx.Event{ID: strings.Repeat("2", 64), PubKey: strings.Repeat("b", 64), CreatedAt: 2, Kind: nostrx.KindTextNote}
	if _, err := st.SaveEvents(ctx, []nostrx.Event{old, newer}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFeedSnapshot(ctx, "test:prune", &FeedSnapshotRecord{Feed: []nostrx.Event{old}, ComputedAtUnix: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	if deleted, err := st.PruneEvents(ctx, 1); err != nil || deleted != 1 {
		t.Fatalf("PruneEvents = deleted %d, err %v", deleted, err)
	}
	if rec, ok, err := st.GetFeedSnapshot(ctx, "test:prune"); err != nil || ok || rec != nil {
		t.Fatalf("snapshot survived canonical prune: ok=%v rec=%v err=%v", ok, rec, err)
	}
}
