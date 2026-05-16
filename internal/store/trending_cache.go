package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ptxt-nstr/internal/nostrx"
)

func (s *Store) TrendingNoteIDs(ctx context.Context, since int64, limit int) ([]TrendingItem, error) {
	if limit <= 0 {
		return []TrendingItem{}, nil
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT nl.parent_id, COUNT(*) AS reply_count
		FROM note_links nl
		JOIN events e ON e.id = nl.parent_id AND e.kind = 1
		WHERE nl.parent_id != '' AND nl.parent_id != nl.note_id AND nl.created_at >= ?
		GROUP BY nl.parent_id
		ORDER BY reply_count DESC, nl.parent_id ASC
		LIMIT ?`, since, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]TrendingItem, 0, limit)
	for rows.Next() {
		var item TrendingItem
		if err := rows.Scan(&item.NoteID, &item.ReplyCount); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) TrendingSummariesByKinds(ctx context.Context, kinds []int, since int64, authors []string, offset int, limit int) ([]nostrx.Event, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > nostrx.MaxRelayQueryLimit {
		limit = nostrx.DefaultRelayQueryLimit
	}
	if offset < 0 {
		offset = 0
	}
	query := fmt.Sprintf(`SELECT e.id, e.pubkey, e.created_at, e.kind, e.content, COUNT(*) AS reply_count
		FROM note_links nl
		JOIN events e ON e.id = nl.parent_id
		WHERE nl.parent_id != '' AND nl.parent_id != nl.note_id AND nl.created_at >= ?
		AND e.kind IN (%s)`, placeholders(len(kinds)))
	args := make([]any, 0, 1+len(kinds)+len(authors)+2)
	args = append(args, since)
	for _, kind := range kinds {
		args = append(args, kind)
	}
	if len(authors) > 0 {
		query += fmt.Sprintf(" AND e.pubkey IN (%s)", placeholders(len(authors)))
		for _, author := range authors {
			args = append(args, author)
		}
	}
	query += `
		GROUP BY e.id, e.pubkey, e.created_at, e.kind, e.content
		ORDER BY reply_count DESC, e.created_at DESC, e.id DESC
		LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	events := make([]nostrx.Event, 0, limit)
	for rows.Next() {
		var event nostrx.Event
		var replyCount int
		if err := rows.Scan(&event.ID, &event.PubKey, &event.CreatedAt, &event.Kind, &event.Content, &replyCount); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// TrendingSummariesByKindsAfter returns notes ranked by direct reply count using keyset
// pagination (score, created_at, id). Pass empty afterID for the first page.
func (s *Store) TrendingSummariesByKindsAfter(ctx context.Context, kinds []int, since int64, authors []string, afterScore int, afterCreated int64, afterID string, limit int) ([]nostrx.Event, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > nostrx.MaxRelayQueryLimit {
		limit = nostrx.DefaultRelayQueryLimit
	}
	query := fmt.Sprintf(`SELECT e.id, e.pubkey, e.created_at, e.kind, e.content, COUNT(*) AS reply_count
		FROM note_links nl
		JOIN events e ON e.id = nl.parent_id
		WHERE nl.parent_id != '' AND nl.parent_id != nl.note_id AND nl.created_at >= ?
		AND e.kind IN (%s)`, placeholders(len(kinds)))
	args := make([]any, 0, 1+len(kinds)+len(authors)+8)
	args = append(args, since)
	for _, kind := range kinds {
		args = append(args, kind)
	}
	if len(authors) > 0 {
		query += fmt.Sprintf(" AND e.pubkey IN (%s)", placeholders(len(authors)))
		for _, author := range authors {
			args = append(args, author)
		}
	}
	query += `
		GROUP BY e.id, e.pubkey, e.created_at, e.kind, e.content`
	if afterID != "" {
		query += `
		HAVING (
			COUNT(*) < ? OR
			(COUNT(*) = ? AND e.created_at < ?) OR
			(COUNT(*) = ? AND e.created_at = ? AND e.id < ?)
		)`
		args = append(args, afterScore, afterScore, afterCreated, afterScore, afterCreated, afterID)
	}
	query += `
		ORDER BY reply_count DESC, e.created_at DESC, e.id DESC
		LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	events := make([]nostrx.Event, 0, limit)
	for rows.Next() {
		var event nostrx.Event
		var replyCount int
		if err := rows.Scan(&event.ID, &event.PubKey, &event.CreatedAt, &event.Kind, &event.Content, &replyCount); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) ReadTrendingCache(ctx context.Context, timeframe string, cohortKey string) ([]TrendingItem, int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT note_id, reply_count, computed_at
		FROM trending_cache
		WHERE timeframe = ? AND cohort_key = ?
		ORDER BY position ASC`, timeframe, cohortKey)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]TrendingItem, 0, 16)
	var computedAt int64
	for rows.Next() {
		var item TrendingItem
		var rowComputedAt int64
		if err := rows.Scan(&item.NoteID, &item.ReplyCount, &rowComputedAt); err != nil {
			return nil, 0, err
		}
		if rowComputedAt > computedAt {
			computedAt = rowComputedAt
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, computedAt, nil
}

func (s *Store) WriteTrendingCache(ctx context.Context, timeframe string, cohortKey string, items []TrendingItem, computedAt int64) error {
	if timeframe == "" {
		return errors.New("timeframe is required")
	}
	if computedAt <= 0 {
		computedAt = time.Now().Unix()
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM trending_cache WHERE timeframe = ? AND cohort_key = ?`, timeframe, cohortKey); err != nil {
		return err
	}
	for pos, item := range items {
		if item.NoteID == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO trending_cache(timeframe, cohort_key, position, note_id, reply_count, computed_at)
			VALUES (?, ?, ?, ?, ?, ?)`, timeframe, cohortKey, pos, item.NoteID, item.ReplyCount, computedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ClearTrendingCache(ctx context.Context, timeframe string, cohortKey string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	switch {
	case timeframe == "" && cohortKey == "":
		_, err := s.db.ExecContext(ctx, `DELETE FROM trending_cache`)
		return err
	case timeframe != "" && cohortKey == "":
		_, err := s.db.ExecContext(ctx, `DELETE FROM trending_cache WHERE timeframe = ?`, timeframe)
		return err
	case timeframe == "" && cohortKey != "":
		_, err := s.db.ExecContext(ctx, `DELETE FROM trending_cache WHERE cohort_key = ?`, cohortKey)
		return err
	default:
		_, err := s.db.ExecContext(ctx, `DELETE FROM trending_cache WHERE timeframe = ? AND cohort_key = ?`, timeframe, cohortKey)
		return err
	}
}
