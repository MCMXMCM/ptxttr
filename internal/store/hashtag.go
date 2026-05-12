package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ptxt-nstr/internal/nostrx"
)

// HashtagNotesQuery lists note summaries that carry a NIP-12 style "t" tag
// (hashtag) matching Tag (case-insensitive, value without leading '#').
type HashtagNotesQuery struct {
	Tag      string
	Authors  []string
	Kinds    []int
	Before   int64
	BeforeID string
	Limit    int
}

// HashtagNoteSummaries returns events whose tags include ["t", Tag] (any case
// for the value), ordered by recency with keyset pagination like search.
func (s *Store) HashtagNoteSummaries(ctx context.Context, query HashtagNotesQuery) (SearchNotesResult, error) {
	result := SearchNotesResult{}
	tag := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(query.Tag), "#"))
	if tag == "" {
		return result, nil
	}
	kinds := uniqueKinds(query.Kinds)
	if len(kinds) == 0 {
		return result, nil
	}
	authors := uniqueNonEmpty(query.Authors)
	oldest, latest, err := s.searchCacheWindow(ctx, kinds, authors)
	if err != nil {
		return result, err
	}
	result.OldestCachedAt = oldest
	result.LatestCachedAt = latest

	limit := nostrx.ClampRelayQueryLimit(query.Limit)
	target := limit + 1
	before := query.Before
	if before <= 0 {
		before = time.Now().Unix() + 1
	}
	sqlQuery := fmt.Sprintf(`SELECT e.id, e.pubkey, e.created_at, e.kind, e.content FROM events e
		WHERE e.id IN (SELECT event_id FROM tags WHERE name = 't' AND lower(value) = lower(?))
		AND e.kind IN (%s)
		%s
		AND (e.created_at < ? OR (e.created_at = ? AND e.id < ?))
		ORDER BY e.created_at DESC, e.id DESC
		LIMIT ?`, placeholders(len(kinds)), authorFilterClause(authors))
	args := make([]any, 0, 1+len(kinds)+len(authors)+4)
	args = append(args, tag)
	for _, kind := range kinds {
		args = append(args, kind)
	}
	for _, author := range authors {
		args = append(args, author)
	}
	args = append(args, before, before, query.BeforeID, target)
	events, err := s.queryEventSummaries(ctx, sqlQuery, args...)
	if err != nil {
		return result, err
	}
	if len(events) > limit {
		result.HasMore = true
		events = events[:limit]
	}
	result.Events = events
	if len(events) > 0 {
		last := events[len(events)-1]
		result.NextCreatedAt = last.CreatedAt
		result.NextID = last.ID
	}
	return result, nil
}
