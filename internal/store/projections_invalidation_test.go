package store

import (
	"context"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

// TestPruneEventsInvalidatesReplyStatLRU exercises the invalidation checklist:
// after PruneEvents removes events whose note_stats rows are wiped
// in the same transaction, the in-memory reply LRU must not still serve
// counts for those vanished note ids.
func TestPruneEventsInvalidatesReplyStatLRU(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	// Build a small thread plus extra padding events so PruneEvents has
	// candidates to drop. Inserted_at advances per save, so the thread
	// (saved first) is the FIFO-oldest cohort and gets pruned.
	root := event("root", "alice", 10, nostrx.KindTextNote, nil)
	reply := event("reply", "bob", 11, nostrx.KindTextNote, [][]string{{"e", "root", "", "root"}, {"e", "root", "", "reply"}})
	mustSaveEvent(t, ctx, st, root)
	mustSaveEvent(t, ctx, st, reply)
	st.recomputeDirtyReplyStats()

	stats, err := st.ReplyStatsByNoteIDs(ctx, []string{"root"})
	if err != nil {
		t.Fatal(err)
	}
	if stats["root"].DirectReplies != 1 {
		t.Fatalf("precondition: root direct replies = %d, want 1", stats["root"].DirectReplies)
	}
	// Confirm the LRU now holds the entry; otherwise the assertion below is
	// vacuous (an empty LRU trivially "no longer holds it").
	if !st.sidecar.reply.Contains("root") {
		t.Fatal("precondition: reply LRU should contain root after ReplyStatsByNoteIDs")
	}

	// Push the thread out via FIFO prune. Save N padding events so PruneEvents
	// trims the thread plus its note_stats rows.
	for i := 0; i < 6; i++ {
		mustSaveEvent(t, ctx, st, event(string(rune('a'+i)), "padder", int64(100+i), nostrx.KindTextNote, nil))
	}
	deleted, err := st.PruneEvents(ctx, 4)
	if err != nil {
		t.Fatal(err)
	}
	if deleted < 1 {
		t.Fatalf("expected PruneEvents to delete at least 1 row, got %d", deleted)
	}

	if st.sidecar.reply.Contains("root") {
		t.Fatal("reply LRU still serves stats for pruned note: PruneEvents must purgeReply()")
	}

	// And the next read must produce zeros, not the old cached count.
	postStats, err := st.ReplyStatsByNoteIDs(ctx, []string{"root"})
	if err != nil {
		t.Fatal(err)
	}
	if got := postStats["root"]; got.DirectReplies != 0 || got.DescendantReplies != 0 {
		t.Fatalf("post-prune root stats = %#v, want zero (note_stats row was pruned)", got)
	}
}

// TestCompactInvalidatesReplyStatLRU mirrors the prune test for the Compact
// path. Compact does not enumerate deleted ids and does not clean note_stats,
// but the LRU is purged defensively so cached counts cannot describe a thread
// whose root event has been compacted away.
func TestCompactInvalidatesReplyStatLRU(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	root := event("root-c", "alice", 10, nostrx.KindTextNote, nil)
	reply := event("reply-c", "bob", 11, nostrx.KindTextNote, [][]string{{"e", "root-c", "", "root"}, {"e", "root-c", "", "reply"}})
	mustSaveEvent(t, ctx, st, root)
	mustSaveEvent(t, ctx, st, reply)
	st.recomputeDirtyReplyStats()

	if _, err := st.ReplyStatsByNoteIDs(ctx, []string{"root-c"}); err != nil {
		t.Fatal(err)
	}
	if !st.sidecar.reply.Contains("root-c") {
		t.Fatal("precondition: reply LRU should contain root-c after ReplyStatsByNoteIDs")
	}

	for i := 0; i < 4; i++ {
		mustSaveEvent(t, ctx, st, event(string(rune('p'+i)), "padder", int64(200+i), nostrx.KindTextNote, nil))
	}
	deleted, err := st.Compact(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if deleted < 1 {
		t.Fatalf("precondition: Compact should delete at least 1 event, got %d", deleted)
	}
	if st.sidecar.reply.Contains("root-c") {
		t.Fatal("reply LRU still serves stats after Compact: should be purged")
	}
}
