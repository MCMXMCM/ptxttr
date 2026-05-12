package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Durable resolved-authors cache survives process restarts and long idle periods
// so the request path can avoid cold BFS + follow scans when a recent memo exists.

const resolvedAuthorsDurableJSONVersion = 1

type resolvedAuthorsDurableRecord struct {
	Version        int      `json:"version"`
	Authors        []string `json:"authors"`
	ComputedAtUnix int64    `json:"computed_at_unix"`
}

// GetResolvedAuthorsDurable returns memoized authors for cache_key if present.
func (s *Store) GetResolvedAuthorsDurable(ctx context.Context, cacheKey string) ([]string, int64, bool, error) {
	if s == nil || s.db == nil || cacheKey == "" {
		return nil, 0, false, nil
	}
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM resolved_authors_durable WHERE cache_key = ?`, cacheKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	var rec resolvedAuthorsDurableRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, 0, false, err
	}
	if rec.Version != resolvedAuthorsDurableJSONVersion || len(rec.Authors) == 0 {
		return nil, 0, false, nil
	}
	return rec.Authors, rec.ComputedAtUnix, true, nil
}

// SetResolvedAuthorsDurable persists a resolved author list for cache_key.
func (s *Store) SetResolvedAuthorsDurable(ctx context.Context, cacheKey string, authors []string, computedAtUnix int64) error {
	if s == nil || s.db == nil || cacheKey == "" || len(authors) == 0 {
		return nil
	}
	rec := resolvedAuthorsDurableRecord{
		Version:        resolvedAuthorsDurableJSONVersion,
		Authors:        authors,
		ComputedAtUnix: computedAtUnix,
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return errors.New("empty resolved authors durable json")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.db.ExecContext(ctx, `INSERT INTO resolved_authors_durable(cache_key, value, computed_at)
		VALUES(?, ?, ?)
		ON CONFLICT(cache_key) DO UPDATE SET value = excluded.value, computed_at = excluded.computed_at`,
		cacheKey, string(b), computedAtUnix)
	return err
}

// DeleteResolvedAuthorsDurablePrefix removes durable rows whose cache_key has
// the given prefix (e.g. seed hex + "|1|" when WoT seed graph expands).
func (s *Store) DeleteResolvedAuthorsDurablePrefix(ctx context.Context, prefix string) error {
	if s == nil || s.db == nil || prefix == "" {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `DELETE FROM resolved_authors_durable WHERE cache_key LIKE ?`, prefix+"%")
	return err
}
