package store

import (
	"context"
	"testing"
	"time"

	"ptxt-nstr/internal/nostrx"
)

func TestFeedSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	rec := &FeedSnapshotRecord{
		Version:        feedSnapshotJSONVersion,
		RelaysHash:     "rh1",
		Feed: []nostrx.Event{{ID: "a", PubKey: "b", CreatedAt: time.Now().Unix(), Kind: nostrx.KindTextNote, Content: "x", Sig: "s"}},
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
	if !ok || got == nil || len(got.Feed) != 1 || got.Feed[0].ID != "a" {
		t.Fatalf("GetFeedSnapshot = ok=%v rec=%v", ok, got)
	}
}
