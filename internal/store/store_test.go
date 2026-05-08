package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/thread"
)

func TestDefaultSQLitePoolSmallHost(t *testing.T) {
	open, idle := defaultSQLitePool(2)
	if open != 4 || idle != 2 {
		t.Fatalf("small-host pool = (%d,%d), want (4,2)", open, idle)
	}
}

func TestDefaultSQLitePoolLargerHost(t *testing.T) {
	open, idle := defaultSQLitePool(8)
	if open != 10 || idle != 4 {
		t.Fatalf("larger-host pool = (%d,%d), want (10,4)", open, idle)
	}
}

// Matches httpx.noteTimelineKinds (notes + reposts) for mention-notification tests.
var mentionNotificationKinds = []int{nostrx.KindTextNote, nostrx.KindRepost}

func TestStoreEventsAndDerivedQueries(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	profileOld := event("profile-old", "alice", 1, nostrx.KindProfileMetadata, nil)
	profileNew := event("profile-new", "alice", 2, nostrx.KindProfileMetadata, nil)
	follow := event("follow", "alice", 3, nostrx.KindFollowList, [][]string{{"p", "bob"}})
	note := event("note", "bob", 4, nostrx.KindTextNote, nil)
	reply := event("reply", "carol", 5, nostrx.KindTextNote, [][]string{{"e", "note", "", "reply"}})

	for _, ev := range []nostrx.Event{profileOld, profileNew, follow, note, reply, note} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	latest, err := st.LatestReplaceable(ctx, "alice", nostrx.KindProfileMetadata)
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.ID != "profile-new" {
		t.Fatalf("expected latest profile-new, got %#v", latest)
	}

	recent, err := st.RecentByAuthors(ctx, []string{"bob"}, []int{nostrx.KindTextNote}, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || recent[0].ID != "note" {
		t.Fatalf("unexpected recent events: %#v", recent)
	}

	replies, err := st.RepliesTo(ctx, "note", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(replies) != 1 || replies[0].ID != "reply" {
		t.Fatalf("unexpected replies: %#v", replies)
	}

	relays, err := st.RelaySources(ctx, "note")
	if err != nil {
		t.Fatal(err)
	}
	if len(relays) != 1 || relays[0] != "wss://relay.example" {
		t.Fatalf("unexpected relay sources: %#v", relays)
	}

	stats, err := st.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Events != 5 || stats.Tags != 2 || stats.RelayEvents != 5 {
		t.Fatalf("unexpected stats: %#v", stats)
	}
}

func TestPruneEventsKeepsNewestFIFO(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	for i := 0; i < 10; i++ {
		ev := event(string(rune('a'+i)), "alice", int64(i), nostrx.KindTextNote, nil)
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := st.PruneEvents(ctx, 4)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 6 {
		t.Fatalf("deleted = %d, want 6", deleted)
	}
	recent, err := st.RecentByAuthors(ctx, []string{"alice"}, []int{nostrx.KindTextNote}, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 4 {
		t.Fatalf("remaining events = %d, want 4", len(recent))
	}
	var linkCount int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM note_links`).Scan(&linkCount); err != nil {
		t.Fatal(err)
	}
	if linkCount != 4 {
		t.Fatalf("remaining note_links = %d, want 4", linkCount)
	}
	var statsCount int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM note_stats`).Scan(&statsCount); err != nil {
		t.Fatal(err)
	}
	if statsCount != 4 {
		t.Fatalf("remaining note_stats = %d, want 4", statsCount)
	}
}

func TestReplaceableHistoryPruneRemovesOlderProfiles(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	st.SetReplaceableHistory(false)

	old := event("profile-old", "alice", 1, nostrx.KindProfileMetadata, nil)
	new := event("profile-new", "alice", 2, nostrx.KindProfileMetadata, nil)
	if err := st.SaveEvent(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveEvent(ctx, new); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE pubkey = ? AND kind = ?`, "alice", nostrx.KindProfileMetadata).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("profile rows = %d, want 1 after prune", n)
	}
	latest, err := st.LatestReplaceable(ctx, "alice", nostrx.KindProfileMetadata)
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.ID != "profile-new" {
		t.Fatalf("latest = %#v", latest)
	}
}

func TestReplaceableHistoryPruneSkipsStaleProfilesArrivingLate(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	st.SetReplaceableHistory(false)

	newer := event("profile-new", "alice", 20, nostrx.KindProfileMetadata, nil)
	older := event("profile-old", "alice", 10, nostrx.KindProfileMetadata, nil)
	if err := st.SaveEvent(ctx, newer); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveEvent(ctx, older); err != nil {
		t.Fatal(err)
	}
	var ids []string
	rows, err := st.db.QueryContext(ctx, `SELECT id FROM events WHERE pubkey = ? AND kind = ? ORDER BY created_at DESC, id DESC`,
		"alice", nostrx.KindProfileMetadata)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != "profile-new" {
		t.Fatalf("stored ids = %#v, want only newest profile", ids)
	}
}

func TestMaybePruneAsyncReconcilesOverflowOnFirstWrite(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	// Seed an oversized DB before retention is enabled, mirroring an existing
	// deployment that starts enforcing a cap after data has already accumulated.
	for i := 0; i < 6; i++ {
		ev := event(fmt.Sprintf("seed-%d", i), "alice", int64(i), nostrx.KindTextNote, nil)
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	st.SetEventRetention(4)
	st.pruneEvery = 1000
	if err := st.SaveEvent(ctx, event("trigger", "alice", 100, nostrx.KindTextNote, nil)); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		var count int
		if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("events count = %d, want 4 after async prune", count)
		}
		time.Sleep(10 * time.Millisecond)
	}

	recent, err := st.RecentByAuthors(ctx, []string{"alice"}, []int{nostrx.KindTextNote}, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 4 {
		t.Fatalf("remaining events = %d, want 4", len(recent))
	}
	got := []string{recent[0].ID, recent[1].ID, recent[2].ID, recent[3].ID}
	want := []string{"trigger", "seed-5", "seed-4", "seed-3"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("remaining IDs = %#v, want %#v", got, want)
		}
	}
}

func TestRecentByKindsAppliesTimeWindow(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	for _, ev := range []nostrx.Event{
		event("fresh-1", "alice", 100, nostrx.KindTextNote, nil),
		event("fresh-2", "bob", 99, nostrx.KindTextNote, nil),
		event("stale", "alice", 10, nostrx.KindTextNote, nil),
		event("profile", "alice", 100, nostrx.KindProfileMetadata, nil),
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.RecentByKinds(ctx, []int{nostrx.KindTextNote}, 50, 0, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "fresh-1" || got[1].ID != "fresh-2" {
		t.Fatalf("unexpected events: %#v", got)
	}
}

func TestRecentByAuthorsCursorUsesIDTieBreaker(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	for _, ev := range []nostrx.Event{
		event("ccc", "alice", 10, nostrx.KindTextNote, nil),
		event("bbb", "alice", 10, nostrx.KindTextNote, nil),
		event("aaa", "alice", 10, nostrx.KindTextNote, nil),
		event("zzz", "alice", 9, nostrx.KindTextNote, nil),
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	first, err := st.RecentByAuthorsCursor(ctx, []string{"alice"}, []int{nostrx.KindTextNote}, 0, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{first[0].ID, first[1].ID}; got[0] != "ccc" || got[1] != "bbb" {
		t.Fatalf("unexpected first page: %#v", got)
	}

	next, err := st.RecentByAuthorsCursor(ctx, []string{"alice"}, []int{nostrx.KindTextNote}, first[1].CreatedAt, first[1].ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{next[0].ID, next[1].ID}; got[0] != "aaa" || got[1] != "zzz" {
		t.Fatalf("unexpected next page: %#v", got)
	}
}

func TestNewerByAuthorsCursorUsesSinceTuple(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	for _, ev := range []nostrx.Event{
		event("ccc", "alice", 10, nostrx.KindTextNote, nil),
		event("bbb", "alice", 10, nostrx.KindTextNote, nil),
		event("aaa", "alice", 10, nostrx.KindTextNote, nil),
		event("ddd", "alice", 11, nostrx.KindTextNote, nil),
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	newer, err := st.NewerByAuthorsCursor(ctx, []string{"alice"}, []int{nostrx.KindTextNote}, 10, "bbb", 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{newer[0].ID, newer[1].ID}; got[0] != "ddd" || got[1] != "ccc" {
		t.Fatalf("unexpected newer page: %#v", got)
	}
}

func TestRecentByCacheCursorOnlyReturnsScopedEvents(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	for _, ev := range []nostrx.Event{
		event("feed-note", "alice", 10, nostrx.KindTextNote, nil),
		event("profile-note", "alice", 9, nostrx.KindTextNote, nil),
		event("other-author", "bob", 8, nostrx.KindTextNote, nil),
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.MarkCacheEvent(ctx, "feed", "curated", "feed-note"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkCacheEvent(ctx, "recent", "alice", "profile-note"); err != nil {
		t.Fatal(err)
	}

	got, err := st.RecentByCacheCursor(ctx, "feed", "curated", []string{"alice"}, []int{nostrx.KindTextNote}, 0, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "feed-note" {
		t.Fatalf("unexpected feed cache events: %#v", got)
	}
}

func TestReplyCountsBatchesAcrossEvents(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	for _, ev := range []nostrx.Event{
		event("note-a", "alice", 10, nostrx.KindTextNote, nil),
		event("note-b", "bob", 9, nostrx.KindTextNote, nil),
		event("reply-a1", "carol", 8, nostrx.KindTextNote, [][]string{{"e", "note-a"}}),
		event("reply-a2", "dan", 7, nostrx.KindTextNote, [][]string{{"e", "note-a"}}),
		event("reply-b1", "erin", 6, nostrx.KindTextNote, [][]string{{"e", "note-b"}}),
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	counts, err := st.ReplyCounts(ctx, []string{"note-a", "note-b", "note-c", "note-a"})
	if err != nil {
		t.Fatal(err)
	}
	if counts["note-a"] != 2 || counts["note-b"] != 1 || counts["note-c"] != 0 {
		t.Fatalf("unexpected reply counts: %#v", counts)
	}
}

func TestTrendingNoteIDsUsesWindowAndSortOrder(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	for _, ev := range []nostrx.Event{
		event("note-a", "alice", 100, nostrx.KindTextNote, nil),
		event("note-b", "bob", 100, nostrx.KindTextNote, nil),
		event("note-c", "carol", 100, nostrx.KindTextNote, nil),
		event("reply-a-1", "x", 220, nostrx.KindTextNote, [][]string{{"e", "note-a", "", "root"}, {"e", "note-a", "", "reply"}}),
		event("reply-a-2", "y", 225, nostrx.KindTextNote, [][]string{{"e", "note-a", "", "root"}, {"e", "note-a", "", "reply"}}),
		event("reply-b-1", "z", 230, nostrx.KindTextNote, [][]string{{"e", "note-b", "", "root"}, {"e", "note-b", "", "reply"}}),
		event("reply-b-2", "w", 235, nostrx.KindTextNote, [][]string{{"e", "note-b", "", "root"}, {"e", "note-b", "", "reply"}}),
		event("reply-c-1", "v", 240, nostrx.KindTextNote, [][]string{{"e", "note-c", "", "root"}, {"e", "note-c", "", "reply"}}),
		event("reply-old", "u", 150, nostrx.KindTextNote, [][]string{{"e", "note-c", "", "root"}, {"e", "note-c", "", "reply"}}),
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	items, err := st.TrendingNoteIDs(ctx, 200, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].NoteID != "note-a" || items[0].ReplyCount != 2 {
		t.Fatalf("unexpected first item: %#v", items[0])
	}
	if items[1].NoteID != "note-b" || items[1].ReplyCount != 2 {
		t.Fatalf("unexpected second item: %#v", items[1])
	}
}

func TestTrendingNoteIDsExcludesParentsMissingFromEvents(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	for _, ev := range []nostrx.Event{
		event("note-present", "alice", 100, nostrx.KindTextNote, nil),
		event("present-reply-1", "bob", 230, nostrx.KindTextNote, [][]string{{"e", "note-present", "", "root"}, {"e", "note-present", "", "reply"}}),
		event("orphan-reply-1", "carol", 240, nostrx.KindTextNote, [][]string{{"e", "missing-parent", "", "root"}, {"e", "missing-parent", "", "reply"}}),
		event("orphan-reply-2", "dan", 250, nostrx.KindTextNote, [][]string{{"e", "missing-parent", "", "root"}, {"e", "missing-parent", "", "reply"}}),
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	items, err := st.TrendingNoteIDs(ctx, 200, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 trending item, got %d (%#v)", len(items), items)
	}
	if items[0].NoteID != "note-present" || items[0].ReplyCount != 1 {
		t.Fatalf("unexpected trending item: %#v", items[0])
	}
}

func TestTrendingSummariesByKindsSupportsAuthorFilterAndOffset(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	for _, ev := range []nostrx.Event{
		event("note-a", "alice", 300, nostrx.KindTextNote, nil),
		event("note-b", "bob", 290, nostrx.KindTextNote, nil),
		event("note-c", "carol", 280, nostrx.KindTextNote, nil),
		event("a-r1", "r1", 320, nostrx.KindTextNote, [][]string{{"e", "note-a", "", "root"}, {"e", "note-a", "", "reply"}}),
		event("a-r2", "r2", 321, nostrx.KindTextNote, [][]string{{"e", "note-a", "", "root"}, {"e", "note-a", "", "reply"}}),
		event("a-r3", "r3", 322, nostrx.KindTextNote, [][]string{{"e", "note-a", "", "root"}, {"e", "note-a", "", "reply"}}),
		event("b-r1", "r4", 323, nostrx.KindTextNote, [][]string{{"e", "note-b", "", "root"}, {"e", "note-b", "", "reply"}}),
		event("b-r2", "r5", 324, nostrx.KindTextNote, [][]string{{"e", "note-b", "", "root"}, {"e", "note-b", "", "reply"}}),
		event("c-r1", "r6", 325, nostrx.KindTextNote, [][]string{{"e", "note-c", "", "root"}, {"e", "note-c", "", "reply"}}),
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	filtered, err := st.TrendingSummariesByKinds(ctx, []int{nostrx.KindTextNote}, 200, []string{"bob", "carol"}, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 2 {
		t.Fatalf("expected 2 filtered items, got %d", len(filtered))
	}
	if filtered[0].ID != "note-b" || filtered[1].ID != "note-c" {
		t.Fatalf("unexpected filtered order: %#v", filtered)
	}

	paged, err := st.TrendingSummariesByKinds(ctx, []int{nostrx.KindTextNote}, 200, nil, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(paged) != 1 || paged[0].ID != "note-b" {
		t.Fatalf("unexpected paged result: %#v", paged)
	}
}

func TestTrendingCacheRoundTripAndOverwrite(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	if err := st.WriteTrendingCache(ctx, "24h", "", []TrendingItem{
		{NoteID: "note-a", ReplyCount: 3},
		{NoteID: "note-b", ReplyCount: 2},
	}, 100); err != nil {
		t.Fatal(err)
	}

	items, computedAt, err := st.ReadTrendingCache(ctx, "24h", "")
	if err != nil {
		t.Fatal(err)
	}
	if computedAt != 100 {
		t.Fatalf("expected computedAt 100, got %d", computedAt)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].NoteID != "note-a" || items[0].ReplyCount != 3 {
		t.Fatalf("unexpected first cached item: %#v", items[0])
	}
	if items[1].NoteID != "note-b" || items[1].ReplyCount != 2 {
		t.Fatalf("unexpected second cached item: %#v", items[1])
	}

	if err := st.WriteTrendingCache(ctx, "24h", "", []TrendingItem{
		{NoteID: "note-c", ReplyCount: 8},
	}, 200); err != nil {
		t.Fatal(err)
	}

	items, computedAt, err = st.ReadTrendingCache(ctx, "24h", "")
	if err != nil {
		t.Fatal(err)
	}
	if computedAt != 200 {
		t.Fatalf("expected computedAt 200, got %d", computedAt)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item after overwrite, got %d", len(items))
	}
	if items[0].NoteID != "note-c" || items[0].ReplyCount != 8 {
		t.Fatalf("unexpected overwritten cached item: %#v", items[0])
	}
}

func TestSearchNoteSummariesSupportsAllCacheAndWindow(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	alpha := event("alpha", "alice", 100, nostrx.KindTextNote, nil)
	alpha.Content = "bitcoin lightning wallet"
	beta := event("beta", "bob", 120, nostrx.KindTextNote, nil)
	beta.Content = "nostr bitcoin relay"
	repost := event("repost", "carol", 130, nostrx.KindRepost, [][]string{{"e", "alpha"}})
	repost.Content = "bitcoin signal boost"
	for _, ev := range []nostrx.Event{alpha, beta, repost} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	for eventID, seenAt := range map[string]int64{
		"alpha":  200,
		"beta":   220,
		"repost": 250,
	} {
		if err := st.MarkCacheEvent(ctx, "search", "all", eventID); err != nil {
			t.Fatal(err)
		}
		if _, err := st.db.ExecContext(ctx, `UPDATE cache_events SET seen_at = ? WHERE scope = ? AND cache_key = ? AND event_id = ?`,
			seenAt, "search", "all", eventID); err != nil {
			t.Fatal(err)
		}
	}

	result, err := st.SearchNoteSummaries(ctx, SearchNotesQuery{
		Text:  PrepareSearch("bitcoin"),
		Kinds: []int{nostrx.KindTextNote, nostrx.KindRepost},
		Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.OldestCachedAt != 200 || result.LatestCachedAt != 250 {
		t.Fatalf("unexpected cache window: oldest=%d latest=%d", result.OldestCachedAt, result.LatestCachedAt)
	}
	if len(result.Events) != 2 {
		t.Fatalf("events len = %d, want 2", len(result.Events))
	}
	if result.Events[0].ID != "repost" {
		t.Fatalf("first event = %q, want repost", result.Events[0].ID)
	}
	if !result.HasMore {
		t.Fatalf("expected hasMore when page limit is 2")
	}
	if result.NextID == "" || result.NextCreatedAt == 0 {
		t.Fatalf("expected next cursor, got id=%q ts=%d", result.NextID, result.NextCreatedAt)
	}
}

func TestSearchNoteSummariesSupportsAuthorScope(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	alice := event("alice-note", "alice", 140, nostrx.KindTextNote, nil)
	alice.Content = "nostr search garden"
	bob := event("bob-note", "bob", 141, nostrx.KindTextNote, nil)
	bob.Content = "nostr search ocean"
	for _, ev := range []nostrx.Event{alice, bob} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.MarkCacheEvent(ctx, "search", "authors", "alice-note"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE cache_events SET seen_at = ? WHERE scope = ? AND cache_key = ? AND event_id = ?`,
		300, "search", "authors", "alice-note"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkCacheEvent(ctx, "search", "authors", "bob-note"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE cache_events SET seen_at = ? WHERE scope = ? AND cache_key = ? AND event_id = ?`,
		310, "search", "authors", "bob-note"); err != nil {
		t.Fatal(err)
	}

	scoped, err := st.SearchNoteSummaries(ctx, SearchNotesQuery{
		Text:    PrepareSearch("nostr"),
		Authors: []string{"alice"},
		Kinds:   []int{nostrx.KindTextNote, nostrx.KindRepost},
		Limit:   10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped.Events) != 1 || scoped.Events[0].ID != "alice-note" {
		t.Fatalf("unexpected scoped result: %#v", scoped.Events)
	}
	if scoped.OldestCachedAt != 300 || scoped.LatestCachedAt != 300 {
		t.Fatalf("unexpected scoped window oldest=%d latest=%d", scoped.OldestCachedAt, scoped.LatestCachedAt)
	}

	emptyQuery, err := st.SearchNoteSummaries(ctx, SearchNotesQuery{
		Text:    PrepareSearch(""),
		Authors: []string{"alice"},
		Kinds:   []int{nostrx.KindTextNote, nostrx.KindRepost},
		Limit:   10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(emptyQuery.Events) != 0 {
		t.Fatalf("blank query should not return events: %#v", emptyQuery.Events)
	}
	if emptyQuery.OldestCachedAt != 300 || emptyQuery.LatestCachedAt != 300 {
		t.Fatalf("blank query should still report window oldest=%d latest=%d", emptyQuery.OldestCachedAt, emptyQuery.LatestCachedAt)
	}
}

func TestMigrateAddsCacheEventsScopeEventSeenIndex(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	rows, err := st.db.QueryContext(ctx, `PRAGMA index_list('cache_events')`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	found := false
	for rows.Next() {
		var seq int
		var name string
		var unique int
		var origin string
		var partial int
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			t.Fatal(err)
		}
		if name == "idx_cache_events_scope_event_seen" {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected idx_cache_events_scope_event_seen to exist")
	}
}

func TestLatestReplaceableByPubkeysReturnsNewestPerPubkey(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	for _, ev := range []nostrx.Event{
		event("alice-old", "alice", 1, nostrx.KindProfileMetadata, nil),
		event("alice-new", "alice", 2, nostrx.KindProfileMetadata, nil),
		event("bob-profile", "bob", 1, nostrx.KindProfileMetadata, nil),
		event("alice-note", "alice", 3, nostrx.KindTextNote, nil),
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	latest, err := st.LatestReplaceableByPubkeys(ctx, []string{"alice", "bob", "carol", "alice"}, nostrx.KindProfileMetadata)
	if err != nil {
		t.Fatal(err)
	}
	if latest["alice"] == nil || latest["alice"].ID != "alice-new" {
		t.Fatalf("unexpected alice profile: %#v", latest["alice"])
	}
	if latest["bob"] == nil || latest["bob"].ID != "bob-profile" {
		t.Fatalf("unexpected bob profile: %#v", latest["bob"])
	}
	if latest["carol"] != nil {
		t.Fatalf("expected nil carol profile, got %#v", latest["carol"])
	}
}

func TestRelayStatusesReturnsPersistedStatus(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	if err := st.SetRelayStatus(ctx, "wss://relay.example", false, "dial failed"); err != nil {
		t.Fatal(err)
	}
	statuses, err := st.RelayStatuses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	status := statuses["wss://relay.example"]
	if status.OK || status.LastError != "dial failed" || status.LastChecked == 0 {
		t.Fatalf("unexpected relay status: %#v", status)
	}
}

func TestSaveEventBuildsProjections(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	profileContent, _ := json.Marshal(map[string]string{
		"name":         "alice_name",
		"display_name": "Alice",
		"about":        "hello",
	})
	profile := event("alice-profile", "alice", 10, nostrx.KindProfileMetadata, nil)
	profile.Content = string(profileContent)
	follows := event("alice-follows", "alice", 11, nostrx.KindFollowList, [][]string{{"p", "bob"}, {"p", "carol"}})
	relayHints := event("alice-relays", "alice", 12, nostrx.KindRelayListMetadata, [][]string{{"r", "wss://relay.one"}, {"r", "wss://relay.two"}})
	root := event("root", "bob", 20, nostrx.KindTextNote, nil)
	reply := event("reply", "carol", 21, nostrx.KindTextNote, [][]string{{"e", "root", "", "root"}, {"e", "root", "", "reply"}})
	replyChild := event("reply-child", "dan", 22, nostrx.KindTextNote, [][]string{{"e", "root", "", "root"}, {"e", "reply", "", "reply"}})

	for _, ev := range []nostrx.Event{profile, follows, relayHints, root, reply, replyChild} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	st.recomputeDirtyReplyStats()

	summaries, err := st.ProfileSummariesByPubkeys(ctx, []string{"alice"})
	if err != nil {
		t.Fatal(err)
	}
	if summaries["alice"].DisplayName != "Alice" {
		t.Fatalf("unexpected profile summary: %#v", summaries["alice"])
	}

	edges, err := st.FollowingPubkeys(ctx, "alice", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 2 || edges[0] != "bob" || edges[1] != "carol" {
		t.Fatalf("unexpected follow edges: %#v", edges)
	}

	relays, err := st.RelayHintsForPubkey(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(relays) != 2 {
		t.Fatalf("unexpected relay hints: %#v", relays)
	}

	stats, err := st.ReplyStatsByNoteIDs(ctx, []string{"root"})
	if err != nil {
		t.Fatal(err)
	}
	if stats["root"].DirectReplies != 1 || stats["root"].DescendantReplies != 2 {
		t.Fatalf("unexpected note stats: %#v", stats["root"])
	}

	links, err := st.ThreadEdges(ctx, []string{"root"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].NoteID != "reply" {
		t.Fatalf("unexpected thread edges: %#v", links)
	}

	cursorLinks, err := st.ThreadEdgesCursor(ctx, []string{"root"}, links[0].CreatedAt, links[0].NoteID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(cursorLinks) != 0 {
		t.Fatalf("expected no cursor links after last edge, got %#v", cursorLinks)
	}
}

func TestNoteLinksMatchThreadWhenNIP10MarkersWrongCase(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	root := event("root", "alice", 20, nostrx.KindTextNote, nil)
	mid := event("mid", "alice", 25, nostrx.KindTextNote, [][]string{
		{"e", "root", "", "Root"},
		{"e", "root", "", "Reply"},
	})
	reply := event("reply", "bob", 30, nostrx.KindTextNote, [][]string{
		{"e", "mid", "", "Reply"},
		{"e", "root", "", "Root"},
	})
	for _, ev := range []nostrx.Event{root, mid, reply} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	wantRoot := thread.RootID(reply)
	wantParent := thread.ParentID(wantRoot, reply)
	var gotRoot, gotParent string
	if err := st.db.QueryRowContext(ctx, `SELECT root_id, parent_id FROM note_links WHERE note_id = ?`, reply.ID).Scan(&gotRoot, &gotParent); err != nil {
		t.Fatal(err)
	}
	if gotRoot != wantRoot || gotParent != wantParent {
		t.Fatalf("note_links = root %q parent %q, want root %q parent %q", gotRoot, gotParent, wantRoot, wantParent)
	}
	edges, err := st.ThreadEdges(ctx, []string{"mid"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 || edges[0].NoteID != "reply" {
		t.Fatalf("expected mid->reply edge, got %#v", edges)
	}
}

func TestSaveEventCanonicalizesReplyRootFromParentProjection(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	root := event("root", "alice", 20, nostrx.KindTextNote, nil)
	reply := event("reply", "bob", 21, nostrx.KindTextNote, [][]string{{"e", "root", "", "root"}})
	nested := event("nested", "carol", 22, nostrx.KindTextNote, [][]string{{"e", "reply", "", "root"}})

	for _, ev := range []nostrx.Event{root, reply, nested} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	var rootID, parentID string
	if err := st.db.QueryRowContext(ctx, `SELECT root_id, parent_id FROM note_links WHERE note_id = ?`, nested.ID).Scan(&rootID, &parentID); err != nil {
		t.Fatal(err)
	}
	if rootID != root.ID {
		t.Fatalf("nested root_id = %q, want %q", rootID, root.ID)
	}
	if parentID != reply.ID {
		t.Fatalf("nested parent_id = %q, want %q", parentID, reply.ID)
	}
}

func TestSaveEventResolvesLongFormParentFromAddressTagComment(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	read := event("read-v1", "alice", 20, nostrx.KindLongForm, [][]string{{"d", "intro"}})
	comment := event("comment", "bob", 21, nostrx.KindComment, [][]string{{"a", "30023:alice:intro", "", "reply"}})

	for _, ev := range []nostrx.Event{read, comment} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	var rootID, parentID string
	if err := st.db.QueryRowContext(ctx, `SELECT root_id, parent_id FROM note_links WHERE note_id = ?`, comment.ID).Scan(&rootID, &parentID); err != nil {
		t.Fatal(err)
	}
	if rootID != read.ID {
		t.Fatalf("comment root_id = %q, want %q", rootID, read.ID)
	}
	if parentID != read.ID {
		t.Fatalf("comment parent_id = %q, want %q", parentID, read.ID)
	}
}

func TestTrendingSummariesByKindsIncludesLongFormAddressComments(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	read := event("read-v1", "alice", 300, nostrx.KindLongForm, [][]string{{"d", "intro"}})
	comment1 := event("comment-1", "bob", 320, nostrx.KindComment, [][]string{{"a", "30023:alice:intro", "", "reply"}})
	comment2 := event("comment-2", "carol", 321, nostrx.KindComment, [][]string{{"a", "30023:alice:intro", "", "reply"}})
	for _, ev := range []nostrx.Event{read, comment1, comment2} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	items, err := st.TrendingSummariesByKinds(ctx, []int{nostrx.KindLongForm}, 200, nil, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one long-form trending item, got %d (%#v)", len(items), items)
	}
	if items[0].ID != read.ID {
		t.Fatalf("unexpected long-form trending item %q, want %q", items[0].ID, read.ID)
	}
}

func TestSaveEventIgnoresStaleFollowListForProjection(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	owner := strings.Repeat("a", 64)
	newer := event("newer-follow", owner, 20, nostrx.KindFollowList, [][]string{{"p", strings.Repeat("b", 64)}})
	older := event("older-follow", owner, 10, nostrx.KindFollowList, [][]string{{"p", strings.Repeat("c", 64)}})

	if err := st.SaveEvent(ctx, newer); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveEvent(ctx, older); err != nil {
		t.Fatal(err)
	}

	got, err := st.FollowingPubkeys(ctx, owner, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != strings.Repeat("b", 64) {
		t.Fatalf("following = %#v, want newest projection only", got)
	}
}

func TestRelayHintsByUsageAndContactHints(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	owner := strings.Repeat("1", 64)
	target := strings.Repeat("2", 64)
	relayList := event("relay-list", target, 10, nostrx.KindRelayListMetadata, [][]string{
		{"r", "wss://write.example", "write"},
		{"r", "wss://read.example", "read"},
		{"r", "wss://both.example"},
	})
	contacts := event("contacts", owner, 12, nostrx.KindFollowList, [][]string{
		{"p", target, "wss://contact.example"},
	})
	for _, ev := range []nostrx.Event{relayList, contacts} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	writeHints, err := st.RelayHintsForPubkeyByUsage(ctx, target, nostrx.RelayUsageWrite)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(writeHints, ",") != "wss://write.example,wss://both.example" {
		t.Fatalf("write hints = %#v", writeHints)
	}
	readHints, err := st.RelayHintsForPubkeyByUsage(ctx, target, nostrx.RelayUsageRead)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(readHints, ",") != "wss://read.example,wss://both.example" {
		t.Fatalf("read hints = %#v", readHints)
	}
	contactHints, err := st.ContactRelayHintsForOwner(ctx, owner, []string{target}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(contactHints[target], ",") != "wss://contact.example" {
		t.Fatalf("contact hints = %#v", contactHints)
	}
}

func TestRelayHintsCanReadThroughLRUSidecar(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	target := strings.Repeat("2", 64)
	relayList := event("relay-list", target, 10, nostrx.KindRelayListMetadata, [][]string{
		{"r", "wss://write.example", "write"},
		{"r", "wss://read.example", "read"},
		{"r", "wss://both.example"},
	})
	if err := st.SaveEvent(ctx, relayList); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `DELETE FROM relay_hints_ranked`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `DELETE FROM relay_hints_cache`); err != nil {
		t.Fatal(err)
	}

	writeHints, err := st.RelayHintsForPubkeyByUsage(ctx, target, nostrx.RelayUsageWrite)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(writeHints, ",") != "wss://write.example,wss://both.example" {
		t.Fatalf("write hints = %#v", writeHints)
	}
	allHints, err := st.RelayHintsForPubkey(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(allHints, ",") != "wss://write.example,wss://read.example,wss://both.example" {
		t.Fatalf("all hints = %#v", allHints)
	}
}

func TestProfileSummariesCanReadThroughLRUSidecar(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	profile := event("alice-profile", "alice", 10, nostrx.KindProfileMetadata, nil)
	profile.Content = `{"display_name":"Alice","name":"alice","about":"hello","picture":"https://example.com/a.png","nip05":"alice@example.com"}`
	if err := st.SaveEvent(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `DELETE FROM profiles_cache`); err != nil {
		t.Fatal(err)
	}

	summaries, err := st.ProfileSummariesByPubkeys(ctx, []string{"alice"})
	if err != nil {
		t.Fatal(err)
	}
	summary, ok := summaries["alice"]
	if !ok {
		t.Fatalf("expected alice profile summary")
	}
	if summary.DisplayName != "Alice" || summary.NIP05 != "alice@example.com" {
		t.Fatalf("summary = %#v", summary)
	}
}

func TestReplyStatsCanReadThroughLRUSidecar(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	root := event("root", "alice", 10, nostrx.KindTextNote, nil)
	reply := event("reply", "bob", 11, nostrx.KindTextNote, [][]string{{"e", "root", "", "root"}, {"e", "root", "", "reply"}})
	if err := st.SaveEvent(ctx, root); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveEvent(ctx, reply); err != nil {
		t.Fatal(err)
	}
	st.recomputeDirtyReplyStats()
	if _, err := st.db.ExecContext(ctx, `DELETE FROM note_stats`); err != nil {
		t.Fatal(err)
	}

	stats, err := st.ReplyStatsByNoteIDs(ctx, []string{"root", "reply"})
	if err != nil {
		t.Fatal(err)
	}
	if stats["root"].DirectReplies != 1 || stats["root"].DescendantReplies != 1 {
		t.Fatalf("root stats = %#v", stats["root"])
	}
	if stats["reply"].LastReplyEventAt != 11 {
		t.Fatalf("reply stats = %#v", stats["reply"])
	}
}

func TestSecondHopFollowingPubkeys(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	owner := strings.Repeat("a", 64)
	firstHop := strings.Repeat("b", 64)
	direct := strings.Repeat("c", 64)
	secondHop := strings.Repeat("d", 64)
	for _, ev := range []nostrx.Event{
		event("owner-follow", owner, 1, nostrx.KindFollowList, [][]string{{"p", firstHop}, {"p", direct}}),
		event("firsthop-follow", firstHop, 2, nostrx.KindFollowList, [][]string{{"p", secondHop}}),
		event("direct-follow", direct, 3, nostrx.KindFollowList, [][]string{{"p", secondHop}}),
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	got, err := st.SecondHopFollowingPubkeys(ctx, owner, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != secondHop {
		t.Fatalf("second hop = %#v, want [%q]", got, secondHop)
	}
}

func TestFollowingPubkeysPageSupportsTotalsPaginationAndSearch(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	owner := strings.Repeat("a", 64)
	targets := []string{
		strings.Repeat("b", 64),
		strings.Repeat("c", 64),
		strings.Repeat("d", 64),
	}
	for index, target := range targets {
		profile := event(fmt.Sprintf("profile-%d", index), target, int64(10+index), nostrx.KindProfileMetadata, nil)
		profile.Content = fmt.Sprintf(`{"display_name":"user-%d","name":"person-%d","about":"about-%d","nip05":"user-%d@example.com"}`, index, index, index, index)
		if err := st.SaveEvent(ctx, profile); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SaveEvent(ctx, event("owner-follows", owner, 20, nostrx.KindFollowList, [][]string{{"p", targets[0]}, {"p", targets[1]}, {"p", targets[2]}})); err != nil {
		t.Fatal(err)
	}
	page, err := st.FollowingPubkeysPage(ctx, owner, "", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.CachedTotal != 3 || page.FilteredTotal != 3 {
		t.Fatalf("unexpected totals: %#v", page)
	}
	if len(page.Pubkeys) != 2 {
		t.Fatalf("len(page.Pubkeys) = %d, want 2", len(page.Pubkeys))
	}
	nextPage, err := st.FollowingPubkeysPage(ctx, owner, "", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(nextPage.Pubkeys) != 1 || nextPage.Pubkeys[0] != targets[2] {
		t.Fatalf("unexpected second page: %#v", nextPage.Pubkeys)
	}
	search, err := st.FollowingPubkeysPage(ctx, owner, "user-1", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if search.CachedTotal != 3 || search.FilteredTotal != 1 || len(search.Pubkeys) != 1 || search.Pubkeys[0] != targets[1] {
		t.Fatalf("unexpected search results: %#v", search)
	}
}

func TestFollowerPubkeysPageSupportsTotalsPaginationAndSearch(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	target := strings.Repeat("f", 64)
	owners := []string{
		strings.Repeat("1", 64),
		strings.Repeat("2", 64),
		strings.Repeat("3", 64),
	}
	for index, owner := range owners {
		profile := event(fmt.Sprintf("owner-profile-%d", index), owner, int64(30+index), nostrx.KindProfileMetadata, nil)
		profile.Content = fmt.Sprintf(`{"display_name":"follower-%d","name":"reader-%d","about":"bio-%d"}`, index, index, index)
		if err := st.SaveEvent(ctx, profile); err != nil {
			t.Fatal(err)
		}
		if err := st.SaveEvent(ctx, event(fmt.Sprintf("follow-%d", index), owner, int64(40+index), nostrx.KindFollowList, [][]string{{"p", target}})); err != nil {
			t.Fatal(err)
		}
	}
	page, err := st.FollowerPubkeysPage(ctx, target, "", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.CachedTotal != 3 || page.FilteredTotal != 3 || len(page.Pubkeys) != 2 {
		t.Fatalf("unexpected follower page: %#v", page)
	}
	search, err := st.FollowerPubkeysPage(ctx, target, "reader-2", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if search.CachedTotal != 3 || search.FilteredTotal != 1 || len(search.Pubkeys) != 1 || search.Pubkeys[0] != owners[2] {
		t.Fatalf("unexpected follower search: %#v", search)
	}
}

func TestHydrationStateScheduling(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	if err := st.TouchHydrationTarget(ctx, "profile", "alice", 3); err != nil {
		t.Fatal(err)
	}
	initial, err := st.StaleHydrationBatch(ctx, "profile", time.Now().Unix(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(initial) != 1 || initial[0].EntityID != "alice" {
		t.Fatalf("unexpected initial hydration targets: %#v", initial)
	}

	if err := st.MarkHydrationAttempt(ctx, "profile", "alice", true, 120*time.Second); err != nil {
		t.Fatal(err)
	}
	afterSuccess, err := st.StaleHydrationBatch(ctx, "profile", time.Now().Unix(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterSuccess) != 0 {
		t.Fatalf("expected no immediate stale targets after success, got %#v", afterSuccess)
	}

	later, err := st.StaleHydrationBatch(ctx, "profile", time.Now().Unix()+180, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(later) != 1 || later[0].EntityID != "alice" {
		t.Fatalf("expected target to become stale later, got %#v", later)
	}
}

func TestTouchHydrationTargetsBatchCoalescesPriority(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	if err := st.TouchHydrationTargetsBatch(ctx, []HydrationTarget{
		{EntityType: "profile", EntityID: "alice", Priority: 1},
		{EntityType: "profile", EntityID: "alice", Priority: 3},
		{EntityType: "relayHints", EntityID: "alice", Priority: 1},
	}); err != nil {
		t.Fatal(err)
	}

	profiles, err := st.StaleHydrationBatch(ctx, "profile", time.Now().Unix(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].EntityID != "alice" || profiles[0].Priority != 3 {
		t.Fatalf("unexpected coalesced profile hydration targets: %#v", profiles)
	}

	relayHints, err := st.StaleHydrationBatch(ctx, "relayHints", time.Now().Unix(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(relayHints) != 1 || relayHints[0].EntityID != "alice" || relayHints[0].Priority != 1 {
		t.Fatalf("unexpected relay hint hydration targets: %#v", relayHints)
	}
}

func TestRecentSummariesByAuthorsCursorBatchesLargeAuthorList(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	const n = 450
	authors := make([]string, n)
	for i := 0; i < n; i++ {
		authors[i] = fmt.Sprintf("%064x", int64(i+1))
	}
	notePub := authors[340]
	if err := st.SaveEvent(ctx, event("heavy-batch-note-1", notePub, 2000, nostrx.KindTextNote, nil)); err != nil {
		t.Fatal(err)
	}
	out, err := st.RecentSummariesByAuthorsCursor(ctx, authors, []int{nostrx.KindTextNote}, 3000, "", 5)
	if err != nil {
		t.Fatalf("RecentSummariesByAuthorsCursor: %v", err)
	}
	if len(out) != 1 || out[0].ID != "heavy-batch-note-1" || out[0].PubKey != notePub {
		t.Fatalf("unexpected summaries: %#v", out)
	}
}

func TestSeedContactFrontierTouchAndStaleBatch(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	pk := strings.Repeat("ab", 32)
	if err := st.TouchSeedContactFrontier(ctx, []string{pk}, 5); err != nil {
		t.Fatal(err)
	}
	batch, err := st.StaleSeedContactBatch(ctx, time.Now().Unix(), 10, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 1 || batch[0].EntityType != EntityTypeSeedContact || batch[0].EntityID != pk {
		t.Fatalf("unexpected batch: %#v", batch)
	}
	if err := st.DeleteHydrationState(ctx, EntityTypeSeedContact, pk); err != nil {
		t.Fatal(err)
	}
	batch2, err := st.StaleSeedContactBatch(ctx, time.Now().Unix(), 10, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch2) != 0 {
		t.Fatalf("expected empty batch after delete, got %#v", batch2)
	}
}

func TestSeedContactTemporaryGiveUpAndReTouch(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	pk := strings.Repeat("cd", 32)
	if err := st.TouchSeedContactFrontier(ctx, []string{pk}, 1); err != nil {
		t.Fatal(err)
	}
	maxFail := 3
	for i := 0; i < maxFail; i++ {
		if err := st.MarkHydrationAttempt(ctx, EntityTypeSeedContact, pk, false, time.Millisecond); err != nil {
			t.Fatal(err)
		}
	}
	batch, err := st.StaleSeedContactBatch(ctx, time.Now().Add(24*time.Hour).Unix(), 10, maxFail)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 0 {
		t.Fatalf("expected give-up (fail_count >= max), got %#v", batch)
	}
	if err := st.TouchSeedContactFrontier(ctx, []string{pk}, 4); err != nil {
		t.Fatal(err)
	}
	batch2, err := st.StaleSeedContactBatch(ctx, time.Now().Unix(), 10, maxFail)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch2) != 1 || batch2[0].EntityID != pk {
		t.Fatalf("expected re-touch to revive row, got %#v", batch2)
	}
}

func TestSeedContactSuccessLeavesDurableDedupeUntilReTouched(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	pk := strings.Repeat("ef", 32)
	if err := st.TouchSeedContactFrontier(ctx, []string{pk}, 2); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkHydrationAttempt(ctx, EntityTypeSeedContact, pk, true, time.Hour); err != nil {
		t.Fatal(err)
	}
	batch, err := st.StaleSeedContactBatch(ctx, time.Now().Add(24*time.Hour).Unix(), 10, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 0 {
		t.Fatalf("expected successful contact to stay deduped, got %#v", batch)
	}
	if err := st.TouchSeedContactFrontier(ctx, []string{pk}, 4); err != nil {
		t.Fatal(err)
	}
	batch2, err := st.StaleSeedContactBatch(ctx, time.Now().Unix(), 10, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch2) != 1 || batch2[0].EntityID != pk {
		t.Fatalf("expected re-touch to reactivate successful contact, got %#v", batch2)
	}
}

func TestEventsMentioningPubkeyExcludesSelfAndDedupesPTags(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	view := strings.Repeat("aa", 32)
	other := strings.Repeat("bb", 32)
	dupPTags := [][]string{{"p", view}, {"p", view}, {"e", "rootid", "", "reply"}}
	n1 := event("m1", other, 100, nostrx.KindTextNote, [][]string{{"p", view}})
	n2 := event("m2", view, 101, nostrx.KindTextNote, [][]string{{"p", view}})
	n3 := event("m3", other, 102, nostrx.KindTextNote, dupPTags)
	for _, ev := range []nostrx.Event{n1, n2, n3} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	out, err := st.EventsMentioningPubkey(ctx, view, mentionNotificationKinds, 1000, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("len=%d want 2 (self excluded, dup p collapsed), got %#v", len(out), out)
	}
	seen := map[string]bool{}
	for _, e := range out {
		seen[e.ID] = true
	}
	if !seen["m1"] || !seen["m3"] {
		t.Fatalf("unexpected ids: %v", seen)
	}
}

func TestEventsMentioningPubkeyCursorPagination(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	view := strings.Repeat("cc", 32)
	other := strings.Repeat("dd", 32)
	// Newest first: ids ordered so lex tie-break is stable with same created_at if needed.
	a := event("zzz", other, 300, nostrx.KindTextNote, [][]string{{"p", view}})
	b := event("yyy", other, 200, nostrx.KindTextNote, [][]string{{"p", view}})
	c := event("xxx", other, 100, nostrx.KindTextNote, [][]string{{"p", view}})
	for _, ev := range []nostrx.Event{a, b, c} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	page1, err := st.EventsMentioningPubkey(ctx, view, mentionNotificationKinds, 400, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 || page1[0].ID != "zzz" || page1[1].ID != "yyy" {
		t.Fatalf("page1 = %#v", page1)
	}
	last := page1[len(page1)-1]
	page2, err := st.EventsMentioningPubkey(ctx, view, mentionNotificationKinds, last.CreatedAt, last.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 1 || page2[0].ID != "xxx" {
		t.Fatalf("page2 = %#v", page2)
	}
}

func event(id, pubkey string, created int64, kind int, tags [][]string) nostrx.Event {
	return nostrx.Event{
		ID:        id,
		PubKey:    pubkey,
		CreatedAt: created,
		Kind:      kind,
		Tags:      tags,
		Content:   "{}",
		Sig:       "sig",
		RelayURL:  "wss://relay.example",
	}
}
