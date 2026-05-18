package httpx

import (
	"context"
	"strings"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

func TestThreadHydrateContextWarmed(t *testing.T) {
	srv, _ := newTestServer(t, testServerOptions{})
	ctx := context.Background()
	rootID := strings.Repeat("a", 64)

	if srv.threadHydrateContextReady(ctx, rootID) {
		t.Fatal("expected cold before mark")
	}
	srv.markThreadHydrateContextWarmed(ctx, rootID)
	if !srv.threadHydrateContextReady(ctx, rootID) {
		t.Fatal("expected warm immediately after mark")
	}
}

func TestThreadHydrateStoreFirstRequiresContextAndReplies(t *testing.T) {
	srv, _ := newTestServer(t, testServerOptions{})
	ctx := context.Background()
	rootID := strings.Repeat("b", 64)

	if srv.threadHydrateStoreFirst(ctx, "hydrate", rootID) {
		t.Fatal("expected store-first false when both cold")
	}
	srv.markThreadHydrateContextWarmed(ctx, rootID)
	if srv.threadHydrateStoreFirst(ctx, "hydrate", rootID) {
		t.Fatal("expected store-first false with only context warm")
	}
	srv.markThreadHydrateRepliesReady(ctx, rootID)
	if !srv.threadHydrateStoreFirst(ctx, "hydrate", rootID) {
		t.Fatal("expected store-first true when context and replies are warm")
	}
	if srv.threadHydrateStoreFirst(ctx, "tree", rootID) {
		t.Fatal("expected store-first false for non-hydrate fragments")
	}
}

func TestThreadHydrateWarmIDsComplete(t *testing.T) {
	rootID := strings.Repeat("a", 64)
	childID := strings.Repeat("b", 64)
	root := nostrx.Event{
		ID:        rootID,
		PubKey:    strings.Repeat("c", 64),
		CreatedAt: 100,
		Kind:      nostrx.KindTextNote,
		Content:   "root",
	}
	loaded := map[string]*nostrx.Event{rootID: &root}
	if !threadHydrateWarmIDsComplete([]string{rootID}, loaded) {
		t.Fatal("expected complete when all ids are present")
	}
	if threadHydrateWarmIDsComplete([]string{rootID, childID}, loaded) {
		t.Fatal("expected incomplete when any id is missing")
	}
}
