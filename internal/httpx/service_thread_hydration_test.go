package httpx

import (
	"context"
	"strings"
	"testing"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

func testMetricCounter(srv *Server, name string) int64 {
	snap := srv.metrics.Snapshot()
	counters, ok := snap["counters"].(map[string]int64)
	if !ok {
		return 0
	}
	return counters[name]
}

func TestThreadStoredReplyCount(t *testing.T) {
	srv, st := newTestServer(t, testServerOptions{})
	ctx := context.Background()
	rootID := strings.Repeat("a", 64)
	replyID := strings.Repeat("b", 64)
	root := nostrx.Event{
		ID: rootID, PubKey: strings.Repeat("c", 64), CreatedAt: 100,
		Kind: nostrx.KindTextNote, Content: "root",
	}
	reply := nostrx.Event{
		ID: replyID, PubKey: strings.Repeat("d", 64), CreatedAt: 101,
		Kind: nostrx.KindTextNote, Content: "reply",
		Tags:    [][]string{{"e", rootID, "", "reply"}},
	}
	for _, event := range []nostrx.Event{root, reply} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	if got := srv.threadStoredReplyCount(ctx, rootID); got != 1 {
		t.Fatalf("threadStoredReplyCount = %d, want 1", got)
	}
}

func TestRefreshRepliesSkipsIndexerWhenStoreHasEnoughReplies(t *testing.T) {
	srv, st := newTestServer(t, testServerOptions{relayTimeout: 50 * time.Millisecond})
	ctx := context.Background()
	rootID := strings.Repeat("1", 64)
	for i := 0; i < threadMinRepliesBeforeIndexer; i++ {
		replyID := strings.Repeat(string(rune('a'+i)), 64)
		reply := nostrx.Event{
			ID: replyID, PubKey: strings.Repeat("f", 64), CreatedAt: int64(100 + i),
			Kind: nostrx.KindTextNote, Content: "r",
			Tags: [][]string{{"e", rootID, "", "reply"}},
		}
		if err := st.SaveEvent(ctx, reply); err != nil {
			t.Fatal(err)
		}
	}
	before := testMetricCounter(srv, "thread.relay_pass.indexer")
	srv.refreshRepliesMode(ctx, rootID, nil, "", nil, nil, refreshRepliesSync)
	after := testMetricCounter(srv, "thread.relay_pass.indexer")
	if after != before {
		t.Fatalf("indexer pass ran with sufficient stored replies: before=%d after=%d", before, after)
	}
}

func TestRefreshRepliesSkipsIndexerWhenThreadCacheFresh(t *testing.T) {
	srv, st := newTestServer(t, testServerOptions{relayTimeout: 50 * time.Millisecond})
	ctx := context.Background()
	rootID := strings.Repeat("2", 64)
	reply := nostrx.Event{
		ID: strings.Repeat("3", 64), PubKey: strings.Repeat("f", 64), CreatedAt: 101,
		Kind: nostrx.KindTextNote, Content: "r",
		Tags: [][]string{{"e", rootID, "", "reply"}},
	}
	if err := st.SaveEvent(ctx, reply); err != nil {
		t.Fatal(err)
	}
	srv.store.MarkRefreshed(ctx, "thread", rootID)
	before := testMetricCounter(srv, "thread.relay_pass.indexer")
	srv.refreshRepliesMode(ctx, rootID, []string{"wss://example.invalid"}, "", nil, nil, refreshRepliesSync)
	after := testMetricCounter(srv, "thread.relay_pass.indexer")
	if after != before {
		t.Fatalf("indexer pass ran while thread scope fresh: before=%d after=%d", before, after)
	}
	if !srv.threadHydrateRepliesReady(ctx, rootID) {
		t.Fatal("expected replies marked ready after fresh cache with stored reply")
	}
}

func TestWarmHydrationThreadsMergesRelaysForBatch(t *testing.T) {
	srv, st := newTestServer(t, testServerOptions{})
	ctx := context.Background()
	authorA := strings.Repeat("a", 64)
	authorB := strings.Repeat("b", 64)
	noteA := strings.Repeat("1", 64)
	noteB := strings.Repeat("2", 64)
	for _, spec := range []struct {
		id, author, relay string
	}{
		{noteA, authorA, "wss://relay-a.example"},
		{noteB, authorB, "wss://relay-b.example"},
	} {
		event := nostrx.Event{
			ID: spec.id, PubKey: spec.author, CreatedAt: 100,
			Kind: nostrx.KindTextNote,
			Tags: [][]string{{"r", spec.relay}},
		}
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	srv.cfg.ThreadMaxRelays = 16
	before := testMetricCounter(srv, "warm.enqueued")
	srv.warmHydrationThreads(ctx, []store.HydrationTarget{
		{EntityType: "noteReplies", EntityID: noteA},
		{EntityType: "noteReplies", EntityID: noteB},
	}, []string{"wss://base.example"})
	deadline := time.Now().Add(2 * time.Second)
	for testMetricCounter(srv, "warm.enqueued") == before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if testMetricCounter(srv, "warm.enqueued") == before {
		t.Fatal("expected warm job enqueued for hydration thread batch")
	}
}
