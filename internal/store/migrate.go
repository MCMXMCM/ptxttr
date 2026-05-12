package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"ptxt-nstr/internal/nostrx"
)

// migrate applies schema pragmas and DDL. Add new indexes here only when a
// query path is proven hot (see README “Denormalization and query planning”).
//
// Per-connection PRAGMAs (busy_timeout, synchronous, foreign_keys, cache_size,
// temp_store, mmap_size, wal_autocheckpoint) live in sqlite_conn_hook.go so
// every pooled connection picks them up; only database-wide / one-shot
// settings stay here.
func (s *Store) migrate(ctx context.Context) error {
	statements := []string{
		// foreign_keys is per-connection but kept here defensively so the
		// migrate connection enforces FKs even if the connection hook ever
		// fails to register (e.g. test harness overrides).
		`PRAGMA foreign_keys=ON`,
		`PRAGMA journal_mode=WAL`,
		// auto_vacuum has to be set before any table exists, otherwise SQLite
		// silently keeps the DB in MODE 0 (NONE) until the next VACUUM. When
		// VACUUM eventually runs on an empty schema it locks in INCREMENTAL,
		// which is what later PRAGMA incremental_vacuum calls rely on to
		// actually return free pages to the OS instead of just to the freelist.
		`PRAGMA auto_vacuum=INCREMENTAL`,
		`CREATE TABLE IF NOT EXISTS events (
			id TEXT PRIMARY KEY,
			pubkey TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			kind INTEGER NOT NULL,
			content TEXT NOT NULL,
			sig TEXT NOT NULL,
			raw_json TEXT NOT NULL,
			inserted_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_events_pubkey_kind_created ON events(pubkey, kind, created_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_events_kind_created ON events(kind, created_at DESC, id DESC)`,
		// Covers LatestReplaceablesByKind's `WHERE kind=? ORDER BY pubkey, created_at DESC, id DESC`
		// scans over kind-3 partitions; without (kind, pubkey, ...) leading columns
		// SQLite would fall back to a sort step over the whole kind partition.
		`CREATE INDEX IF NOT EXISTS idx_events_kind_pubkey_created ON events(kind, pubkey, created_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_events_inserted_at ON events(inserted_at ASC)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS events_fts USING fts5(content)`,
		fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS trg_events_fts_insert AFTER INSERT ON events
		WHEN NEW.kind IN (%d, %d)
		BEGIN
			INSERT INTO events_fts(rowid, content) VALUES (NEW.rowid, NEW.content);
		END`, nostrx.KindTextNote, nostrx.KindRepost),
		fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS trg_events_fts_delete AFTER DELETE ON events
		WHEN OLD.kind IN (%d, %d)
		BEGIN
			DELETE FROM events_fts WHERE rowid = OLD.rowid;
		END`, nostrx.KindTextNote, nostrx.KindRepost),
		fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS trg_events_fts_update AFTER UPDATE OF kind, content ON events
		BEGIN
			DELETE FROM events_fts WHERE rowid = OLD.rowid;
			INSERT INTO events_fts(rowid, content) SELECT NEW.rowid, NEW.content
			WHERE NEW.kind IN (%d, %d);
		END`, nostrx.KindTextNote, nostrx.KindRepost),
		`CREATE TABLE IF NOT EXISTS tags (
			event_id TEXT NOT NULL,
			idx INTEGER NOT NULL,
			name TEXT NOT NULL,
			value TEXT NOT NULL,
			extra TEXT NOT NULL DEFAULT '',
			PRIMARY KEY(event_id, idx),
			FOREIGN KEY(event_id) REFERENCES events(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tags_name_value ON tags(name, value, event_id)`,
		`CREATE TABLE IF NOT EXISTS relay_events (
			event_id TEXT NOT NULL,
			relay_url TEXT NOT NULL,
			first_seen INTEGER NOT NULL,
			last_seen INTEGER NOT NULL,
			PRIMARY KEY(event_id, relay_url)
		)`,
		`CREATE TABLE IF NOT EXISTS relay_status (
			relay_url TEXT PRIMARY KEY,
			ok INTEGER NOT NULL DEFAULT 0,
			last_error TEXT NOT NULL DEFAULT '',
			last_checked INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS fetch_log (
			scope TEXT NOT NULL,
			cache_key TEXT NOT NULL,
			last_fetch INTEGER NOT NULL,
			PRIMARY KEY(scope, cache_key)
		)`,
		`CREATE TABLE IF NOT EXISTS cache_events (
			scope TEXT NOT NULL,
			cache_key TEXT NOT NULL,
			event_id TEXT NOT NULL,
			seen_at INTEGER NOT NULL,
			PRIMARY KEY(scope, cache_key, event_id),
			FOREIGN KEY(event_id) REFERENCES events(id) ON DELETE CASCADE
		)`,
		// Search cache window and cached feed reads join by scope + event_id while
		// aggregating seen_at, so keep a narrower covering index than the primary
		// key's (scope, cache_key, event_id) layout.
		`CREATE INDEX IF NOT EXISTS idx_cache_events_scope_event_seen ON cache_events(scope, event_id, seen_at)`,
		`CREATE TABLE IF NOT EXISTS profiles_cache (
			pubkey TEXT PRIMARY KEY,
			profile_event_id TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			about TEXT NOT NULL DEFAULT '',
			picture TEXT NOT NULL DEFAULT '',
			nip05 TEXT NOT NULL DEFAULT '',
			last_metadata_fetch_at INTEGER NOT NULL DEFAULT 0,
			last_seen_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_profiles_cache_last_seen ON profiles_cache(last_seen_at DESC)`,
		`CREATE TABLE IF NOT EXISTS note_links (
			note_id TEXT PRIMARY KEY,
			author_pubkey TEXT NOT NULL,
			root_id TEXT NOT NULL,
			parent_id TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			last_seen_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_note_links_root_created ON note_links(root_id, created_at DESC, note_id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_note_links_parent_created ON note_links(parent_id, created_at DESC, note_id DESC)`,
		`CREATE TABLE IF NOT EXISTS note_stats (
			note_id TEXT PRIMARY KEY,
			direct_reply_count INTEGER NOT NULL DEFAULT 0,
			descendant_reply_count INTEGER NOT NULL DEFAULT 0,
			last_reply_event_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS note_reaction_latest (
			note_id TEXT NOT NULL,
			reactor_pubkey TEXT NOT NULL,
			polarity INTEGER NOT NULL,
			reaction_created_at INTEGER NOT NULL,
			reaction_event_id TEXT NOT NULL,
			PRIMARY KEY(note_id, reactor_pubkey),
			FOREIGN KEY(note_id) REFERENCES events(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_note_reaction_latest_note ON note_reaction_latest(note_id)`,
		`CREATE TABLE IF NOT EXISTS trending_cache (
			timeframe TEXT NOT NULL,
			cohort_key TEXT NOT NULL DEFAULT '',
			position INTEGER NOT NULL,
			note_id TEXT NOT NULL,
			reply_count INTEGER NOT NULL,
			computed_at INTEGER NOT NULL,
			PRIMARY KEY(timeframe, cohort_key, position)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_trending_cache_timeframe ON trending_cache(timeframe, cohort_key, position)`,
		`CREATE TABLE IF NOT EXISTS follow_edges (
			owner_pubkey TEXT NOT NULL,
			target_pubkey TEXT NOT NULL,
			follow_event_id TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY(owner_pubkey, target_pubkey)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_follow_edges_target ON follow_edges(target_pubkey, owner_pubkey)`,
		`CREATE TABLE IF NOT EXISTS relay_hints_cache (
			pubkey TEXT PRIMARY KEY,
			relay_event_id TEXT NOT NULL,
			relays_json TEXT NOT NULL DEFAULT '[]',
			created_at INTEGER NOT NULL,
			last_fetch_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS relay_hints_ranked (
			pubkey TEXT PRIMARY KEY,
			relay_event_id TEXT NOT NULL,
			write_relays_json TEXT NOT NULL DEFAULT '[]',
			read_relays_json TEXT NOT NULL DEFAULT '[]',
			all_relays_json TEXT NOT NULL DEFAULT '[]',
			created_at INTEGER NOT NULL,
			last_fetch_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS contact_relay_hints (
			owner_pubkey TEXT NOT NULL,
			target_pubkey TEXT NOT NULL,
			relay_url TEXT NOT NULL,
			follow_event_id TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY(owner_pubkey, target_pubkey)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_contact_relay_hints_target ON contact_relay_hints(target_pubkey, owner_pubkey)`,
		`CREATE TABLE IF NOT EXISTS hydration_state (
			entity_type TEXT NOT NULL,
			entity_id TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			last_attempt_at INTEGER NOT NULL DEFAULT 0,
			last_success_at INTEGER NOT NULL DEFAULT 0,
			next_retry_at INTEGER NOT NULL DEFAULT 0,
			fail_count INTEGER NOT NULL DEFAULT 0,
			priority INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY(entity_type, entity_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_hydration_state_scan ON hydration_state(entity_type, next_retry_at, priority DESC, last_success_at, last_attempt_at)`,
		`CREATE TABLE IF NOT EXISTS app_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS feed_snapshots (
			snapshot_key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			computed_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS resolved_authors_durable (
			cache_key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			computed_at INTEGER NOT NULL
		)`,
		// One-shot FTS backfill: only scan events on the very first migrate
		// after FTS was added. After that, triggers keep events_fts in sync,
		// so re-running the SELECT on every startup is wasted I/O.
		fmt.Sprintf(`INSERT OR IGNORE INTO events_fts(rowid, content)
		SELECT rowid, content FROM events
		WHERE kind IN (%d, %d)
		AND NOT EXISTS (SELECT 1 FROM events_fts LIMIT 1)`, nostrx.KindTextNote, nostrx.KindRepost),
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	if err := s.dropLegacyNoteStatsColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureTrendingCacheCohorts(ctx); err != nil {
		return err
	}
	if err := s.maybeAnalyze(ctx); err != nil {
		slog.Warn("sqlite analyze failed", "err", err)
	}
	if err := s.maybeBootstrapNoteReactions(ctx); err != nil {
		slog.Warn("note reaction aggregates bootstrap failed", "err", err)
	}
	return nil
}

func (s *Store) ensureTrendingCacheCohorts(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(trending_cache)`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	hasCohortKey := false
	for rows.Next() {
		var (
			cid       int
			name      string
			colType   string
			notNull   int
			dfltValue any
			pk        int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return err
		}
		if name == "cohort_key" {
			hasCohortKey = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasCohortKey {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE trending_cache_migrated (
		timeframe TEXT NOT NULL,
		cohort_key TEXT NOT NULL DEFAULT '',
		position INTEGER NOT NULL,
		note_id TEXT NOT NULL,
		reply_count INTEGER NOT NULL,
		computed_at INTEGER NOT NULL,
		PRIMARY KEY(timeframe, cohort_key, position)
	)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO trending_cache_migrated(timeframe, cohort_key, position, note_id, reply_count, computed_at)
		SELECT timeframe, '', position, note_id, reply_count, computed_at
		FROM trending_cache`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DROP TABLE trending_cache`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE trending_cache_migrated RENAME TO trending_cache`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_trending_cache_timeframe ON trending_cache(timeframe, cohort_key, position)`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) dropLegacyNoteStatsColumns(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(note_stats)`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	hasLastReplyFetch := false
	hasHotScore := false
	for rows.Next() {
		var (
			cid       int
			name      string
			colType   string
			notNull   int
			dfltValue any
			pk        int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return err
		}
		switch name {
		case "last_reply_fetch_at":
			hasLastReplyFetch = true
		case "hot_score":
			hasHotScore = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if hasLastReplyFetch {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE note_stats DROP COLUMN last_reply_fetch_at`); err != nil {
			return err
		}
	}
	if hasHotScore {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE note_stats DROP COLUMN hot_score`); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) maybeAnalyze(ctx context.Context) error {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_meta WHERE key = 'analyzed_at'`).Scan(&value)
	if err == nil && value != "" {
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `ANALYZE`); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO app_meta(key, value)
		VALUES('analyzed_at', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, time.Now().UTC().Format(time.RFC3339))
	return err
}
