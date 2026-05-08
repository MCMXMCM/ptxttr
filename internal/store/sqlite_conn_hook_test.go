package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

// TestPerConnectionPragmasApplyOnAllPoolConnections forces multiple pool
// connections to be live at once and asserts each saw the connection-hook
// PRAGMAs. Without the hook, only the migrate connection would have
// busy_timeout=5000 / foreign_keys=1; the rest would silently fall back to
// SQLite defaults (0 / OFF) — exactly the regression the High finding flagged.
func TestPerConnectionPragmasApplyOnAllPoolConnections(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)
	st.db.SetMaxOpenConns(8)
	st.db.SetMaxIdleConns(8)

	const n = 4
	conns := make([]*sql.Conn, 0, n)
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()
	for i := 0; i < n; i++ {
		c, err := st.db.Conn(ctx)
		if err != nil {
			t.Fatalf("Conn[%d]: %v", i, err)
		}
		conns = append(conns, c)
	}

	for i, c := range conns {
		var busy int
		if err := c.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busy); err != nil {
			t.Fatalf("conn[%d] PRAGMA busy_timeout: %v", i, err)
		}
		if busy != 5000 {
			t.Errorf("conn[%d] busy_timeout = %d, want 5000", i, busy)
		}
		var fk int
		if err := c.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
			t.Fatalf("conn[%d] PRAGMA foreign_keys: %v", i, err)
		}
		if fk != 1 {
			t.Errorf("conn[%d] foreign_keys = %d, want 1", i, fk)
		}
		var syncMode int
		if err := c.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&syncMode); err != nil {
			t.Fatalf("conn[%d] PRAGMA synchronous: %v", i, err)
		}
		// synchronous=NORMAL is mode 1.
		if syncMode != 1 {
			t.Errorf("conn[%d] synchronous = %d, want 1 (NORMAL)", i, syncMode)
		}
	}
}

// TestForeignKeyCascadeOnFreshPoolConnection deletes an event from a freshly
// opened (non-migrate) pool connection and asserts the FK cascade to tags
// fires. Before the connection hook this would silently leave orphan rows
// because foreign_keys defaults to OFF on each new connection. Forcing the
// fresh connection: SetMaxIdleConns(0) evicts any idle conn that was the
// migrate connection; the next db.Conn() therefore opens a brand-new SQLite
// connection that must go through the registered hook.
func TestForeignKeyCascadeOnFreshPoolConnection(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, ctx)

	ev := event("fk-cascade", "alice", 100, nostrx.KindTextNote, [][]string{{"e", "parent-id"}})
	mustSaveEvent(t, ctx, st, ev)

	var tagCount int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tags WHERE event_id = ?`, ev.ID).Scan(&tagCount); err != nil {
		t.Fatalf("count tags pre-delete: %v", err)
	}
	if tagCount != 1 {
		t.Fatalf("precondition: expected 1 tag for event %q, got %d", ev.ID, tagCount)
	}

	// Force any cached idle connections (including the one migrate ran on) to
	// close, so the next Conn() opens a fresh driver connection that flows
	// through the registered hook.
	st.db.SetMaxIdleConns(0)
	st.db.SetMaxOpenConns(4)
	st.db.SetMaxIdleConns(4)

	deleter, err := st.db.Conn(ctx)
	if err != nil {
		t.Fatalf("deleter Conn: %v", err)
	}

	var fk int
	if err := deleter.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("PRAGMA foreign_keys on fresh conn: %v", err)
	}
	if fk != 1 {
		t.Fatalf("fresh pool conn has foreign_keys=%d, want 1 (hook should have applied)", fk)
	}

	if _, err := deleter.ExecContext(ctx, `DELETE FROM events WHERE id = ?`, ev.ID); err != nil {
		t.Fatalf("delete event: %v", err)
	}
	if err := deleter.Close(); err != nil {
		t.Fatalf("close deleter: %v", err)
	}

	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tags WHERE event_id = ?`, ev.ID).Scan(&tagCount); err != nil {
		t.Fatalf("count tags post-delete: %v", err)
	}
	if tagCount != 0 {
		t.Fatalf("FK cascade did not fire on fresh pool connection: %d tag rows remain", tagCount)
	}
}

// TestRegisterConnectionHookIsIdempotent ensures opening multiple stores in the
// same process does not panic and that every store still gets the per-connection
// PRAGMAs. The connection hook is package-global; this test exercises the
// sync.Once guard from concurrent goroutines.
func TestRegisterConnectionHookIsIdempotent(t *testing.T) {
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := filepath.Join(t.TempDir(), "hook.sqlite")
			st, err := Open(ctx, path)
			if err != nil {
				t.Errorf("open[%d]: %v", i, err)
				return
			}
			defer func() { _ = st.Close() }()
			var busy int
			if err := st.db.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busy); err != nil {
				t.Errorf("pragma[%d]: %v", i, err)
				return
			}
			if busy != 5000 {
				t.Errorf("busy_timeout[%d] = %d, want 5000", i, busy)
			}
		}(i)
	}
	wg.Wait()
}
