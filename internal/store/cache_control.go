package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"ptxt-nstr/internal/nostrx"
)

type CacheCategoryUsage struct {
	Events int64 `json:"events"`
	Bytes  int64 `json:"bytes"`
}

type CacheUsage struct {
	DiskBytes int64              `json:"disk_bytes"`
	Notes     CacheCategoryUsage `json:"notes"`
	Metadata  CacheCategoryUsage `json:"metadata"`
	UserData  CacheCategoryUsage `json:"user_data"`
	Other     CacheCategoryUsage `json:"other"`
}

type CacheClearResult struct {
	Scope         string `json:"scope"`
	DeletedEvents int64  `json:"deleted_events"`
	Warning       string `json:"warning,omitempty"`
}

var cacheNoteKinds = []int{
	nostrx.KindTextNote,
	nostrx.KindRepost,
	nostrx.KindReaction,
	nostrx.KindComment,
	nostrx.KindLongForm,
	9735, // NIP-57 zap receipt
	1068, // poll
	1018, // poll response
}

var cacheMetadataKinds = []int{nostrx.KindProfileMetadata}

var cacheUserDataKinds = []int{
	nostrx.KindFollowList,
	nostrx.KindMuteList,
	nostrx.KindRelayListMetadata,
	nostrx.KindBookmarkList,
}

func (s *Store) CacheUsage(ctx context.Context) (CacheUsage, error) {
	var usage CacheUsage
	if s == nil || s.db == nil {
		return usage, nil
	}
	query := fmt.Sprintf(`
		SELECT CASE
			WHEN kind IN (%s) THEN 'notes'
			WHEN kind IN (%s) THEN 'metadata'
			WHEN kind IN (%s) THEN 'user_data'
			ELSE 'other'
		END AS category,
		COUNT(*),
		COALESCE(SUM(LENGTH(raw_json)), 0)
		FROM events
		GROUP BY category`,
		placeholders(len(cacheNoteKinds)),
		placeholders(len(cacheMetadataKinds)),
		placeholders(len(cacheUserDataKinds)),
	)
	allKinds := append(append(append([]int{}, cacheNoteKinds...), cacheMetadataKinds...), cacheUserDataKinds...)
	args := make([]any, 0, len(allKinds))
	for _, kind := range allKinds {
		args = append(args, kind)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return CacheUsage{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var category string
		var categoryUsage CacheCategoryUsage
		if err := rows.Scan(&category, &categoryUsage.Events, &categoryUsage.Bytes); err != nil {
			return CacheUsage{}, err
		}
		switch category {
		case "notes":
			usage.Notes = categoryUsage
		case "metadata":
			usage.Metadata = categoryUsage
		case "user_data":
			usage.UserData = categoryUsage
		case "other":
			usage.Other = categoryUsage
		}
	}
	if err := rows.Err(); err != nil {
		return CacheUsage{}, err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, statErr := os.Stat(s.dbPath + suffix)
		if statErr == nil {
			usage.DiskBytes += info.Size()
		} else if !os.IsNotExist(statErr) {
			return CacheUsage{}, statErr
		}
	}
	return usage, nil
}

// ClearCache removes public Nostr cache data while deliberately preserving
// browser-owned accounts, keys, sessions, preferences, and the SQLite app_meta
// table. Derived projections are rebuilt from whatever event categories remain.
func (s *Store) ClearCache(ctx context.Context, scope string) (CacheClearResult, error) {
	result := CacheClearResult{Scope: scope}
	if s == nil || s.db == nil {
		return result, nil
	}
	var kinds []int
	switch scope {
	case "notes":
		kinds = cacheNoteKinds
	case "metadata":
		kinds = cacheMetadataKinds
	case "user_data":
		kinds = cacheUserDataKinds
	case "all":
	default:
		return CacheClearResult{}, fmt.Errorf("unknown cache scope %q", scope)
	}

	s.writeMu.Lock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.writeMu.Unlock()
		return CacheClearResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var deleteResult sql.Result
	if scope == "all" {
		deleteResult, err = tx.ExecContext(ctx, `DELETE FROM events`)
	} else {
		query := fmt.Sprintf(`DELETE FROM events WHERE kind IN (%s)`, placeholders(len(kinds)))
		args := make([]any, 0, len(kinds))
		for _, kind := range kinds {
			args = append(args, kind)
		}
		deleteResult, err = tx.ExecContext(ctx, query, args...)
	}
	if err != nil {
		s.writeMu.Unlock()
		return CacheClearResult{}, err
	}
	result.DeletedEvents, _ = deleteResult.RowsAffected()
	for _, stmt := range []string{
		`DELETE FROM relay_events WHERE event_id NOT IN (SELECT id FROM events)`,
		`DELETE FROM relay_status`,
		`DELETE FROM fetch_log`,
		`DELETE FROM trending_cache`,
		`DELETE FROM hydration_state`,
		`DELETE FROM feed_snapshots`,
		`DELETE FROM guest_slice_state`,
		`DELETE FROM guest_slice_members`,
		`DELETE FROM guest_slice_progress`,
		`DELETE FROM guest_slice_build_state`,
		`DELETE FROM event_pins`,
		`DELETE FROM nip05_verifications`,
		`DELETE FROM resolved_authors_durable`,
		`DELETE FROM share_artifacts`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			s.writeMu.Unlock()
			return CacheClearResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		s.writeMu.Unlock()
		return CacheClearResult{}, err
	}
	s.writeMu.Unlock()

	if err := s.RebuildProjections(ctx); err != nil {
		return CacheClearResult{}, err
	}
	if err := s.ReclaimFreePages(ctx); err != nil {
		result.Warning = "Cached data was cleared, but free disk pages could not be reclaimed."
		return result, nil
	}
	// Databases created before incremental vacuum was enabled retain deleted
	// pages in the file. A user-initiated clear is the right time to pay the
	// one-off cost of rewriting that older database and return space to disk.
	if ratio, err := s.FreelistRatio(ctx); err == nil && ratio >= 0.05 {
		if err := s.VacuumFull(ctx); err != nil {
			result.Warning = "Cached data was cleared, but the database file could not be compacted."
		}
	}
	return result, nil
}
