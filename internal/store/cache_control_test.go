package store

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

func TestCacheUsageAndScopedClearPreserveAppMeta(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	for _, ev := range []nostrx.Event{
		event("note-cache", "alice", 1, nostrx.KindTextNote, nil),
		event("profile-cache", "alice", 2, nostrx.KindProfileMetadata, nil),
		event("follow-cache", "alice", 3, nostrx.KindFollowList, [][]string{{"p", "bob"}}),
		event("other-cache", "alice", 4, 42, nil),
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.db.ExecContext(ctx, `INSERT INTO app_meta(key, value) VALUES('preserve-me', 'yes')`); err != nil {
		t.Fatal(err)
	}

	usage, err := st.CacheUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Notes.Events != 1 || usage.Metadata.Events != 1 || usage.UserData.Events != 1 || usage.Other.Events != 1 {
		t.Fatalf("unexpected category usage: %#v", usage)
	}
	if usage.DiskBytes <= 0 {
		t.Fatalf("disk bytes = %d, want > 0", usage.DiskBytes)
	}

	result, err := st.ClearCache(ctx, "metadata")
	if err != nil {
		t.Fatal(err)
	}
	if result.DeletedEvents != 1 {
		t.Fatalf("deleted events = %d, want 1", result.DeletedEvents)
	}
	var profileCount, noteCount int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE id='profile-cache'`).Scan(&profileCount); err != nil {
		t.Fatal(err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE id='note-cache'`).Scan(&noteCount); err != nil {
		t.Fatal(err)
	}
	if profileCount != 0 {
		t.Fatalf("profile event count after metadata clear = %d, want 0", profileCount)
	}
	if noteCount != 1 {
		t.Fatalf("note event count after metadata clear = %d, want 1", noteCount)
	}
	var preserved string
	if err := st.db.QueryRowContext(ctx, `SELECT value FROM app_meta WHERE key='preserve-me'`).Scan(&preserved); err != nil {
		t.Fatal(err)
	}
	if preserved != "yes" {
		t.Fatalf("app_meta value = %q, want yes", preserved)
	}

	if _, err := st.ClearCache(ctx, "all"); err != nil {
		t.Fatal(err)
	}
	finalUsage, err := st.CacheUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if finalUsage.Notes.Events+finalUsage.Metadata.Events+finalUsage.UserData.Events+finalUsage.Other.Events != 0 {
		t.Fatalf("events remain after all clear: %#v", finalUsage)
	}
}

func TestClearCacheRejectsUnknownScope(t *testing.T) {
	st := openTestStore(t, context.Background())
	if _, err := st.ClearCache(context.Background(), "accounts"); err == nil {
		t.Fatal("expected unknown scope error")
	}
}

func TestClearCacheFinishesUnderConfiguredByteLimit(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	st.SetRetentionPolicy(true)
	if _, err := st.db.ExecContext(ctx, `PRAGMA auto_vacuum=NONE`); err != nil {
		t.Fatal(err)
	}
	if err := st.VacuumFull(ctx); err != nil {
		t.Fatal(err)
	}

	note := event("clear-budget-note", "alice", 1, nostrx.KindTextNote, nil)
	note.Content = "clear me"
	if err := st.SaveEvent(ctx, note); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		ev := event(fmt.Sprintf("clear-budget-profile-%02d", i), fmt.Sprintf("author-%02d", i), int64(i+2), nostrx.KindProfileMetadata, nil)
		ev.Content = fmt.Sprintf(`{"name":"profile-%02d","about":"%s"}`, i, strings.Repeat(string(rune('a'+i)), 128<<10))
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.ReclaimFreePages(ctx); err != nil {
		t.Fatal(err)
	}
	before := DBFileBytes(st.dbPath)
	maxBytes := before * 3 / 4
	st.SetDiskByteRetentionPolicy(maxBytes, maxBytes*9/10)

	if _, err := st.ClearCache(ctx, "notes"); err != nil {
		t.Fatal(err)
	}
	if after := DBFileBytes(st.dbPath); after >= maxBytes {
		t.Fatalf("database bytes after clear = %d, want below configured max %d (before %d)", after, maxBytes, before)
	}
}
