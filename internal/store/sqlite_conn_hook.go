package store

import (
	"context"
	"fmt"
	"sync"

	"modernc.org/sqlite"
)

// connHookOnce guards a single registration of the package-level connection
// hook. database/sql opens new pool connections lazily under contention; without
// this hook, modernc.org/sqlite gives each new connection SQLite defaults
// (busy_timeout=0, synchronous=FULL, foreign_keys=OFF, etc.), so PRAGMAs run
// once during migrate would only affect that single migrate-time connection.
var connHookOnce sync.Once

// perConnectionPragmas returns the PRAGMAs that must be set on every pooled
// SQLite connection. Database-wide settings (journal_mode=WAL,
// auto_vacuum=INCREMENTAL) are intentionally left in migrate.
//
// Env values are read on every connection so operators can tune the live
// process by editing the env and forcing a pool reconnect (e.g. via
// PTXT_SQLITE_MAX_OPEN_CONNS churn) without restarting.
func perConnectionPragmas() []string {
	cacheKiB := positiveIntEnv("PTXT_SQLITE_CACHE_KIB", 32768)
	mmapBytes := positiveInt64Env("PTXT_SQLITE_MMAP_BYTES", 134217728)
	walAutoCheckpointPages := positiveIntEnv("PTXT_SQLITE_WAL_AUTOCHECKPOINT_PAGES", 800)
	busyTimeoutMS := positiveIntEnv("PTXT_SQLITE_BUSY_TIMEOUT_MS", 5000)
	return []string{
		`PRAGMA foreign_keys=ON`,
		`PRAGMA synchronous=NORMAL`,
		fmt.Sprintf(`PRAGMA cache_size=-%d`, cacheKiB),
		`PRAGMA temp_store=MEMORY`,
		fmt.Sprintf(`PRAGMA mmap_size=%d`, mmapBytes),
		fmt.Sprintf(`PRAGMA wal_autocheckpoint=%d`, walAutoCheckpointPages),
		fmt.Sprintf(`PRAGMA busy_timeout=%d`, busyTimeoutMS),
	}
}

// registerConnectionHook wires perConnectionPragmas into modernc's connection
// pipeline. Called once per process from Open via connHookOnce; the hook itself
// is package-global, so every sql.Open("sqlite", ...) in this binary (incl.
// tests) gets the same per-connection setup.
func registerConnectionHook() {
	sqlite.RegisterConnectionHook(func(conn sqlite.ExecQuerierContext, _ string) error {
		for _, stmt := range perConnectionPragmas() {
			if _, err := conn.ExecContext(context.Background(), stmt, nil); err != nil {
				return fmt.Errorf("apply %q: %w", stmt, err)
			}
		}
		return nil
	})
}
