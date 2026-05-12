package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

func seedReachablePubkeysFullGraph(t *testing.T, ctx context.Context, st *Store) {
	t.Helper()
	mustSaveEvent(t, ctx, st, event("alice-follows", "alice", 10, nostrx.KindFollowList, [][]string{{"p", "bob"}, {"p", "carol"}}))
	mustSaveEvent(t, ctx, st, event("bob-follows", "bob", 11, nostrx.KindFollowList, [][]string{{"p", "dave"}, {"p", "erin"}}))
	mustSaveEvent(t, ctx, st, event("carol-follows", "carol", 12, nostrx.KindFollowList, [][]string{{"p", "erin"}, {"p", "frank"}}))
	mustSaveEvent(t, ctx, st, event("erin-follows", "erin", 13, nostrx.KindFollowList, [][]string{{"p", "alice"}, {"p", "gina"}}))
}

func TestReachablePubkeysWithinDepthThreeFullGraph(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	seedReachablePubkeysFullGraph(t, ctx, st)

	got, err := st.ReachablePubkeysWithin(ctx, "alice", 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"bob", "carol", "dave", "erin", "frank", "gina"}
	assertStringSliceEq(t, got, want)
}

func TestReachablePubkeysWithinDepthOne(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	seedReachablePubkeysFullGraph(t, ctx, st)

	got, err := st.ReachablePubkeysWithin(ctx, "alice", 1)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"bob", "carol"}
	assertStringSliceEq(t, got, want)
}

func TestReachablePubkeysWithinClampDepth(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	seedReachablePubkeysFullGraph(t, ctx, st)

	at3, err := st.ReachablePubkeysWithin(ctx, "alice", 3)
	if err != nil {
		t.Fatal(err)
	}
	at10, err := st.ReachablePubkeysWithin(ctx, "alice", 10)
	if err != nil {
		t.Fatal(err)
	}
	assertStringSliceEq(t, at10, at3)
}

func TestReachablePubkeysWithinHandlesCycles(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	mustSaveEvent(t, ctx, st, event("af", "alice", 1, nostrx.KindFollowList, [][]string{{"p", "bob"}}))
	mustSaveEvent(t, ctx, st, event("bf", "bob", 2, nostrx.KindFollowList, [][]string{{"p", "carol"}, {"p", "alice"}}))
	mustSaveEvent(t, ctx, st, event("cf", "carol", 3, nostrx.KindFollowList, [][]string{{"p", "alice"}}))

	got, err := st.ReachablePubkeysWithin(ctx, "alice", 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"bob", "carol"}
	assertStringSliceEq(t, got, want)
}

func TestReachablePubkeysWithinExcludesOwnerSelfFollow(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	mustSaveEvent(t, ctx, st, event("alice-follows", "alice", 10, nostrx.KindFollowList, [][]string{{"p", "alice"}, {"p", "bob"}}))
	mustSaveEvent(t, ctx, st, event("bob-follows", "bob", 11, nostrx.KindFollowList, [][]string{{"p", "carol"}}))

	got, err := st.ReachablePubkeysWithin(ctx, "alice", 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, pk := range got {
		if pk == "alice" {
			t.Fatalf("owner pubkey must not appear in reachable set: %#v", got)
		}
	}
	want := []string{"bob", "carol"}
	assertStringSliceEq(t, got, want)
}

func TestReachablePubkeysWithinUnknownPubkeyReturnsEmpty(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	mustSaveEvent(t, ctx, st, event("alice-follows", "alice", 10, nostrx.KindFollowList, [][]string{{"p", "bob"}}))

	got, err := st.ReachablePubkeysWithin(ctx, "lonely", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty slice, got %#v", got)
	}
}

func TestReachablePubkeysWithinDepthOrdersAlphaWithinRing(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	// alice follows zoe then anna in tag order; CTE orders ring 1 by target_pubkey ASC → anna, zoe.
	mustSaveEvent(t, ctx, st, event("af", "alice", 1, nostrx.KindFollowList, [][]string{{"p", "zoe"}, {"p", "anna"}}))
	mustSaveEvent(t, ctx, st, event("zf", "zoe", 2, nostrx.KindFollowList, [][]string{{"p", "mike"}}))
	mustSaveEvent(t, ctx, st, event("anf", "anna", 3, nostrx.KindFollowList, [][]string{{"p", "bob"}}))

	got, err := st.ReachablePubkeysWithin(ctx, "alice", 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"anna", "zoe", "bob", "mike"}
	assertStringSliceEq(t, got, want)
}

func TestReachablePubkeysWithinSnapshotConsistency(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "wot_snapshot.sqlite")

	// Reader and writer use separate *sql.DB pools so the read transaction keeps a WAL snapshot
	// while another pool commits writes (same-pool ordering made this flaky under modernc).
	stRead := openTestStoreAtPath(t, ctx, path)
	mustSaveEvent(t, ctx, stRead, event("alice-follows", "alice", 10, nostrx.KindFollowList, [][]string{{"p", "bob"}}))
	mustSaveEvent(t, ctx, stRead, event("bob-follows", "bob", 11, nostrx.KindFollowList, [][]string{{"p", "carol"}}))

	stWrite := openTestStoreAtPath(t, ctx, path)

	tx, err := stRead.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	var edgeCount int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM follow_edges WHERE owner_pubkey = ?`, "alice").Scan(&edgeCount); err != nil {
		t.Fatal(err)
	}
	if edgeCount != 1 {
		t.Fatalf("precondition: alice should have 1 follow edge, got %d", edgeCount)
	}

	mustSaveEvent(t, ctx, stWrite, event("alice-follows2", "alice", 20, nostrx.KindFollowList, [][]string{{"p", "bob"}, {"p", "carol"}}))
	mustSaveEvent(t, ctx, stWrite, event("carol-f", "carol", 21, nostrx.KindFollowList, [][]string{{"p", "zed"}}))

	// Writer expanded alice's direct follows to bob+carol; reader still sees one alice edge (bob only).
	var aliceEdgesAfterWrite int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM follow_edges WHERE owner_pubkey = ?`, "alice").Scan(&aliceEdgesAfterWrite); err != nil {
		t.Fatal(err)
	}
	if aliceEdgesAfterWrite != 1 {
		t.Fatalf("read txn should still see 1 alice edge (snapshot); got %d", aliceEdgesAfterWrite)
	}

	got, err := scanReachablePubkeysWithin(ctx, tx, "alice", 3)
	if err != nil {
		t.Fatal(err)
	}
	// Snapshot graph: alice→bob, bob→carol. carol is depth-2, not a direct follow of alice.
	want := []string{"bob", "carol"}
	assertStringSliceEq(t, got, want)

	fresh, err := stWrite.ReachablePubkeysWithin(ctx, "alice", 3)
	if err != nil {
		t.Fatal(err)
	}
	wantFresh := []string{"bob", "carol", "zed"}
	assertStringSliceEq(t, fresh, wantFresh)
}

func assertStringSliceEq(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (got %#v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("[%d] = %q, want %q (got %#v)", i, got[i], want[i], got)
		}
	}
}