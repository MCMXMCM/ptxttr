package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"ptxt-nstr/internal/nostrx"
)

func TestGetDefaultSeedGuestFeedSnapshotSkipsOversizedJSON(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	huge := strings.Repeat("x", maxDefaultSeedGuestFeedSnapshotJSONBytes+1)
	if err := st.SetAppMeta(ctx, AppMetaKeyDefaultSeedGuestFeed, huge); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.GetDefaultSeedGuestFeedSnapshot(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if ok || got != nil {
		t.Fatalf("expected oversize snapshot to be ignored, ok=%v got=%v", ok, got)
	}
}

func TestSetDefaultSeedGuestFeedSnapshotRejectsOversizedPayload(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	note := nostrx.Event{
		ID:        strings.Repeat("a", 64),
		PubKey:    strings.Repeat("b", 64),
		CreatedAt: time.Now().Unix(),
		Kind:      nostrx.KindTextNote,
		Content:   strings.Repeat("z", maxDefaultSeedGuestFeedSnapshotJSONBytes),
	}
	snap := &DefaultSeedGuestFeedSnapshot{
		Feed:           []nostrx.Event{note},
		ReferencedEvents: map[string]nostrx.Event{},
		ReplyCounts:    map[string]int{},
		ReactionTotals: map[string]int{},
		ReactionViewers: map[string]string{},
		Profiles:       map[string]DefaultSeedProfileSnap{},
	}
	if err := st.SetDefaultSeedGuestFeedSnapshot(ctx, snap); err == nil {
		t.Fatal("expected SetDefaultSeedGuestFeedSnapshot to reject oversized JSON")
	}
}
