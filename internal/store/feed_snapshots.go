package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"ptxt-nstr/internal/nostrx"
)

const feedSnapshotJSONVersion = 2

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
	_, err = s.db.ExecContext(ctx, `INSERT INTO feed_snapshots(snapshot_key, value, computed_at)
		VALUES(?, ?, ?)
		ON CONFLICT(snapshot_key) DO UPDATE SET value = excluded.value, computed_at = excluded.computed_at`,
		key, string(b), rec.ComputedAtUnix)
	return err
}
