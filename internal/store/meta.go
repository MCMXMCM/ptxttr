package store

import (
	"context"
	"database/sql"
	"log/slog"
)

const trendingCacheSchemaVersionKey = "trending_cache.schema_version"
const trendingCacheSchemaVersion = "2"

func (s *Store) AppMeta(ctx context.Context, key string) (string, bool, error) {
	if s == nil || s.db == nil || key == "" {
		return "", false, nil
	}
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_meta WHERE key = ?`, key).Scan(&value)
	if err == nil {
		return value, true, nil
	}
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return "", false, err
}

func (s *Store) SetAppMeta(ctx context.Context, key string, value string) error {
	if s == nil || s.db == nil || key == "" {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `INSERT INTO app_meta(key, value)
		VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (s *Store) SetAppMetaBatch(ctx context.Context, values map[string]string) error {
	if s == nil || s.db == nil || len(values) == 0 {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO app_meta(key, value)
		VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer func() { _ = stmt.Close() }()
	for key, value := range values {
		if key == "" {
			continue
		}
		if _, err := stmt.ExecContext(ctx, key, value); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// EnsureTrendingCacheSchemaVersion clears persisted trending rows when the stored
// schema version disagrees with this binary, then records the current version.
func (s *Store) EnsureTrendingCacheSchemaVersion(ctx context.Context) {
	if s == nil {
		return
	}
	version, ok, err := s.AppMeta(ctx, trendingCacheSchemaVersionKey)
	if err != nil {
		slog.Warn("startup trending cache version check failed", "err", err)
		return
	}
	if ok && version == trendingCacheSchemaVersion {
		return
	}
	if ok && version != "" {
		if err := s.ClearTrendingCache(ctx, "", ""); err != nil {
			slog.Warn("startup trending cache clear failed; continuing", "err", err)
			return
		}
	}
	if err := s.SetAppMeta(ctx, trendingCacheSchemaVersionKey, trendingCacheSchemaVersion); err != nil {
		slog.Warn("startup trending cache version write failed", "err", err)
	}
}
