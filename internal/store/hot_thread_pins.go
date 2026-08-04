package store

import (
	"context"
	"time"
)

const hotThreadPinReasonPrefix = "viewer-hot-thread:"

const hotThreadMaxEvents = 100

// PinHotThread protects a bounded ready-to-view thread working set from LRU
// pruning. Pins are refreshed on feed/intent materialization and oldest roots
// are released when the root cap is exceeded.
func (s *Store) PinHotThread(ctx context.Context, rootID string, eventIDs []string, ttl time.Duration, maxRoots int) error {
	if s == nil || s.db == nil || rootID == "" || len(eventIDs) == 0 {
		return nil
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if maxRoots <= 0 {
		maxRoots = 50
	}
	now := time.Now().Unix()
	expiresAt := time.Now().Add(ttl).Unix()
	reason := hotThreadPinReasonPrefix + rootID
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO hot_thread_pins(root_id, pinned_at, expires_at)
		VALUES(?, ?, ?) ON CONFLICT(root_id) DO UPDATE SET pinned_at = excluded.pinned_at, expires_at = excluded.expires_at`,
		rootID, now, expiresAt); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO event_pins(event_id, generation, reason, expires_at)
		VALUES(?, 0, ?, ?) ON CONFLICT(event_id, generation, reason) DO UPDATE SET expires_at = excluded.expires_at`)
	if err != nil {
		return err
	}
	uniqueEventIDs := uniqueNonEmpty(eventIDs)
	if len(uniqueEventIDs) > hotThreadMaxEvents {
		uniqueEventIDs = uniqueEventIDs[:hotThreadMaxEvents]
	}
	for _, eventID := range uniqueEventIDs {
		if _, err := stmt.ExecContext(ctx, eventID, reason, expiresAt); err != nil {
			_ = stmt.Close()
			return err
		}
	}
	_ = stmt.Close()
	if _, err := tx.ExecContext(ctx, `DELETE FROM event_pins WHERE reason LIKE ? AND expires_at <= ?`, hotThreadPinReasonPrefix+"%", now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM hot_thread_pins WHERE expires_at <= ?`, now); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT root_id FROM hot_thread_pins ORDER BY pinned_at DESC, root_id DESC LIMIT -1 OFFSET ?`, maxRoots)
	if err != nil {
		return err
	}
	var evict []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		evict = append(evict, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range evict {
		if _, err := tx.ExecContext(ctx, `DELETE FROM event_pins WHERE reason = ?`, hotThreadPinReasonPrefix+id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM hot_thread_pins WHERE root_id = ?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
