package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"ptxt-nstr/internal/nostrx"
)

const (
	// AppMetaKeyDefaultSeedGuestFeed stores JSON of DefaultSeedGuestFeedSnapshot
	// for the canonical logged-out default-seed first page (fragment-shaped payload).
	AppMetaKeyDefaultSeedGuestFeed      = "default_seed_guest_feed_snapshot_v1"
	defaultSeedGuestFeedSnapshotVersion = 1

	// maxDefaultSeedGuestFeedSnapshotJSONBytes caps guest snapshot JSON so a
	// corrupt or oversized app_meta row cannot stall json.Unmarshal.
	maxDefaultSeedGuestFeedSnapshotJSONBytes = 4 << 20
)

// DefaultSeedGuestFeedSnapshot is a durable first-page payload for the anonymous
// default-seed home feed. It mirrors the fields merged into the deferred SSR shell
// and fragment=1 light-stats path.
type DefaultSeedGuestFeedSnapshot struct {
	Version          int                               `json:"version"`
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

// DefaultSeedProfileSnap is JSON-friendly nostrx.Profile without the nested Event pointer.
type DefaultSeedProfileSnap struct {
	PubKey  string `json:"pubkey"`
	Name    string `json:"name"`
	Display string `json:"display"`
	About   string `json:"about"`
	Picture string `json:"picture"`
	Website string `json:"website"`
	NIP05   string `json:"nip05"`
}

// GetDefaultSeedGuestFeedSnapshot returns the last persisted default-seed guest
// first page, if any.
func (s *Store) GetDefaultSeedGuestFeedSnapshot(ctx context.Context) (*DefaultSeedGuestFeedSnapshot, bool, error) {
	if s == nil {
		return nil, false, nil
	}
	raw, ok, err := s.AppMeta(ctx, AppMetaKeyDefaultSeedGuestFeed)
	if err != nil || !ok || raw == "" {
		return nil, false, err
	}
	if len(raw) > maxDefaultSeedGuestFeedSnapshotJSONBytes {
		slog.Warn("default seed guest feed snapshot JSON exceeds max size; ignoring",
			"bytes", len(raw), "max", maxDefaultSeedGuestFeedSnapshotJSONBytes)
		return nil, false, nil
	}
	var snap DefaultSeedGuestFeedSnapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		return nil, false, err
	}
	if snap.Version != defaultSeedGuestFeedSnapshotVersion || len(snap.Feed) == 0 {
		return nil, false, nil
	}
	return &snap, true, nil
}

// SetDefaultSeedGuestFeedSnapshot replaces the persisted snapshot. Callers must
// only invoke this when len(snap.Feed) > 0 so a cold transient build never wipes
// a usable last-known-good page.
func (s *Store) SetDefaultSeedGuestFeedSnapshot(ctx context.Context, snap *DefaultSeedGuestFeedSnapshot) error {
	if s == nil || snap == nil || len(snap.Feed) == 0 {
		return nil
	}
	snap.Version = defaultSeedGuestFeedSnapshotVersion
	b, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return errors.New("empty default seed guest feed snapshot json")
	}
	if len(b) > maxDefaultSeedGuestFeedSnapshotJSONBytes {
		return fmt.Errorf("default seed guest feed snapshot json exceeds maximum size (%d bytes, max %d)",
			len(b), maxDefaultSeedGuestFeedSnapshotJSONBytes)
	}
	return s.SetAppMeta(ctx, AppMetaKeyDefaultSeedGuestFeed, string(b))
}
