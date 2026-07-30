package store

import (
	"context"

	"ptxt-nstr/internal/nostrx"
)

const profilePaymentBackfillKey = "profile_payment_backfill_v1"

func (s *Store) maybeBackfillProfileCachePaymentFields(ctx context.Context) error {
	if v, ok, err := s.AppMeta(ctx, profilePaymentBackfillKey); err != nil {
		return err
	} else if ok && v != "" {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `
		SELECT e.raw_json
		FROM profiles_cache pc
		INNER JOIN events e ON e.id = pc.profile_event_id
		WHERE pc.profile_event_id != ''
		  AND (pc.lud16 = '' OR pc.lud06 = '' OR pc.website = '')`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		ev, err := decodeEvent(raw)
		if err != nil || ev == nil {
			continue
		}
		profile := nostrx.ParseProfile(ev.PubKey, ev)
		if _, err := tx.ExecContext(ctx, `
			UPDATE profiles_cache
			SET lud16 = ?, lud06 = ?, website = ?
			WHERE pubkey = ?`,
			profile.Lud16, profile.Lud06, profile.Website, ev.PubKey); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO app_meta(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, profilePaymentBackfillKey, "1"); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.clearProfileSummariesBestEffort()
	return nil
}
