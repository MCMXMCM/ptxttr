package store

import (
	"context"
	"encoding/json"
	"strings"

	"ptxt-nstr/internal/nostrx"
)

func (s *Store) queryEvents(ctx context.Context, query string, args ...any) ([]nostrx.Event, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var events []nostrx.Event
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		event, err := decodeEvent(raw)
		if err != nil {
			return nil, err
		}
		events = append(events, *event)
	}
	return events, rows.Err()
}

func (s *Store) queryEventSummaries(ctx context.Context, query string, args ...any) ([]nostrx.Event, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var events []nostrx.Event
	for rows.Next() {
		var event nostrx.Event
		if err := rows.Scan(&event.ID, &event.PubKey, &event.CreatedAt, &event.Kind, &event.Content); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func decodeEvent(raw string) (*nostrx.Event, error) {
	var event nostrx.Event
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		return nil, err
	}
	return &event, nil
}

func kindCursorArgs(kinds []int, cursor int64, cursorID string, limit int) []any {
	args := make([]any, 0, len(kinds)+4)
	for _, kind := range kinds {
		args = append(args, kind)
	}
	args = append(args, cursor, cursor, cursorID, limit)
	return args
}

func authorKindCursorArgs(authors []string, kinds []int, cursor int64, cursorID string, limit int) []any {
	args := make([]any, 0, len(authors)+len(kinds)+4)
	for _, author := range authors {
		args = append(args, author)
	}
	args = append(args, kindCursorArgs(kinds, cursor, cursorID, limit)...)
	return args
}

func placeholders(n int) string {
	if n <= 0 {
		panic("placeholders requires n >= 1")
	}
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}
