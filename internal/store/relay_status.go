package store

import (
	"context"
	"time"
)

func (s *Store) SetRelayStatus(ctx context.Context, relayURL string, ok bool, lastErr string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	okInt := 0
	if ok {
		okInt = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO relay_status(relay_url, ok, last_error, last_checked)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(relay_url) DO UPDATE SET ok = excluded.ok, last_error = excluded.last_error, last_checked = excluded.last_checked`,
		relayURL, okInt, lastErr, time.Now().Unix())
	return err
}

func (s *Store) RelayStatuses(ctx context.Context) (map[string]RelayStatus, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT relay_url, ok, last_error, last_checked FROM relay_status`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	statuses := make(map[string]RelayStatus)
	for rows.Next() {
		var status RelayStatus
		var ok int
		if err := rows.Scan(&status.URL, &ok, &status.LastError, &status.LastChecked); err != nil {
			return nil, err
		}
		status.OK = ok == 1
		statuses[status.URL] = status
	}
	return statuses, rows.Err()
}
