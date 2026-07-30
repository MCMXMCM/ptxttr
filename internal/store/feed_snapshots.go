package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"ptxt-nstr/internal/nostrx"
)

const feedSnapshotJSONVersion = 2

var ErrFeedSnapshotMissingCanonicalEvent = errors.New("feed snapshot references an event missing from the canonical store")

type snapshotQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func feedSnapshotEventIDs(events []nostrx.Event) []string {
	ids := make([]string, 0, len(events))
	seen := make(map[string]struct{}, len(events))
	for _, event := range events {
		id := nostrx.CanonicalHex64(event.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func canonicalFeedEventsPresent(ctx context.Context, q snapshotQueryer, events []nostrx.Event) (bool, error) {
	ids := feedSnapshotEventIDs(events)
	if len(ids) != len(events) || len(ids) == 0 {
		return false, nil
	}
	query := fmt.Sprintf(`SELECT id FROM events WHERE id IN (%s)`, placeholders(len(ids)))
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	found := 0
	for rows.Next() {
		found++
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return found == len(ids), nil
}

func (s *Store) deleteFeedSnapshot(ctx context.Context, key string) error {
	if s == nil || s.db == nil || key == "" {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `DELETE FROM feed_snapshots WHERE snapshot_key = ?`, key)
	return err
}

func invalidateFeedSnapshotsTx(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM feed_snapshots`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM app_meta WHERE key = ?`, AppMetaKeyDefaultSeedGuestFeed)
	return err
}

func (s *Store) invalidateFeedSnapshotsLocked(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM feed_snapshots`); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM app_meta WHERE key = ?`, AppMetaKeyDefaultSeedGuestFeed)
	return err
}

// FeedSnapshotRecord is a durable first-page feed payload (guest canonical or
// signed-in personalized). Shape matches DefaultSeedGuestFeedSnapshot plus
// IsStarter for signed-in placeholder content.
type FeedSnapshotRecord struct {
	Version          int                               `json:"version"`
	IsStarter        bool                              `json:"is_starter,omitempty"`
	RelaysHash       string                            `json:"relays_hash"`
	Feed             []nostrx.Event                    `json:"feed"`
	ReferencedEvents map[string]nostrx.Event           `json:"referenced_events,omitempty"`
	ReplyCounts      map[string]int                    `json:"reply_counts,omitempty"`
	ReactionTotals   map[string]int                    `json:"reaction_totals,omitempty"`
	ReactionViewers  map[string]string                 `json:"reaction_viewers,omitempty"`
	Profiles         map[string]DefaultSeedProfileSnap `json:"profiles,omitempty"`
	Cursor           int64                             `json:"cursor"`
	CursorID         string                            `json:"cursor_id"`
	HasMore          bool                              `json:"has_more"`
	ComputedAtUnix   int64                             `json:"computed_at_unix"`
}

// GetFeedSnapshot returns a persisted feed snapshot by primary key, if any.
func (s *Store) GetFeedSnapshot(ctx context.Context, key string) (*FeedSnapshotRecord, bool, error) {
	if s == nil || s.db == nil || key == "" {
		return nil, false, nil
	}
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM feed_snapshots WHERE snapshot_key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var rec FeedSnapshotRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, false, err
	}
	if rec.Version != feedSnapshotJSONVersion || len(rec.Feed) == 0 {
		return nil, false, nil
	}
	present, err := canonicalFeedEventsPresent(ctx, s.db, rec.Feed)
	if err != nil {
		return nil, false, err
	}
	if !present {
		_ = s.deleteFeedSnapshot(ctx, key)
		return nil, false, nil
	}
	return &rec, true, nil
}

// SetFeedSnapshot persists a feed snapshot. Callers must only write non-empty feeds.
func (s *Store) SetFeedSnapshot(ctx context.Context, key string, rec *FeedSnapshotRecord) error {
	if s == nil || s.db == nil || key == "" || rec == nil || len(rec.Feed) == 0 {
		return nil
	}
	rec.Version = feedSnapshotJSONVersion
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return errors.New("empty feed snapshot json")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	present, err := canonicalFeedEventsPresent(ctx, tx, rec.Feed)
	if err != nil {
		return err
	}
	if !present {
		return ErrFeedSnapshotMissingCanonicalEvent
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO feed_snapshots(snapshot_key, value, computed_at)
		VALUES(?, ?, ?)
		ON CONFLICT(snapshot_key) DO UPDATE SET value = excluded.value, computed_at = excluded.computed_at`,
		key, string(b), rec.ComputedAtUnix)
	if err != nil {
		return err
	}
	return tx.Commit()
}
