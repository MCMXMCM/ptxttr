package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ptxt-nstr/internal/nostrx"
)

// EventsMentioningPubkey returns distinct events of the given kinds that include
// a `p` tag equal to taggedPubkey, ordered newest-first. Events authored by
// taggedPubkey are excluded (no self-notifications). Duplicate `p` tags on the
// same event are collapsed via GROUP BY.
func (s *Store) EventsMentioningPubkey(ctx context.Context, taggedPubkey string, kinds []int, before int64, beforeID string, limit int) ([]nostrx.Event, error) {
	taggedPubkey = strings.TrimSpace(taggedPubkey)
	if taggedPubkey == "" || len(kinds) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > nostrx.MaxRelayQueryLimit {
		limit = nostrx.DefaultRelayQueryLimit
	}
	if before <= 0 {
		before = time.Now().Unix() + 1
	}
	query := fmt.Sprintf(`SELECT e.raw_json FROM events e
		WHERE e.id IN (
			SELECT t.event_id FROM tags t
			WHERE t.name = 'p' AND t.value = ?
			GROUP BY t.event_id
		)
		AND e.pubkey <> ? AND e.kind IN (%s)
		AND (e.created_at < ? OR (e.created_at = ? AND e.id < ?))
		ORDER BY e.created_at DESC, e.id DESC LIMIT ?`,
		placeholders(len(kinds)))
	args := make([]any, 0, 3+len(kinds)+4)
	args = append(args, taggedPubkey, taggedPubkey)
	for _, k := range kinds {
		args = append(args, k)
	}
	args = append(args, before, before, beforeID, limit)
	return s.queryEvents(ctx, query, args...)
}
