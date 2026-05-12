package store

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"ptxt-nstr/internal/nostrx"
)

type SearchNotesQuery struct {
	Text     PreparedSearch
	Authors  []string
	Kinds    []int
	Before   int64
	BeforeID string
	Limit    int
}

type PreparedSearch struct {
	Normalized string
	Match      string
}

func (q PreparedSearch) Empty() bool {
	return q.Match == ""
}

type SearchNotesResult struct {
	Events         []nostrx.Event
	HasMore        bool
	NextCreatedAt  int64
	NextID         string
	OldestCachedAt int64
	LatestCachedAt int64
}

const (
	// SearchMaxQueryRunes bounds user-provided query size before tokenization.
	SearchMaxQueryRunes = 128
	// SearchMaxTokens bounds the number of distinct terms in an FTS query.
	SearchMaxTokens = 6
	// SearchMinTokenRunes drops short prefixes that tend to fan out heavily.
	SearchMinTokenRunes = 2
	// SearchMaxTokenRunes bounds the length of an individual term.
	SearchMaxTokenRunes = 32
)

// SearchNoteSummaries ranks matches by FTS bm25 then recency with tuple paging.
func (s *Store) SearchNoteSummaries(ctx context.Context, query SearchNotesQuery) (SearchNotesResult, error) {
	result := SearchNotesResult{}
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

	if query.Text.Empty() {
		return result, nil
	}
	limit := nostrx.ClampRelayQueryLimit(query.Limit)
	target := limit + 1
	before := query.Before
	if before <= 0 {
		before = time.Now().Unix() + 1
	}
	sqlQuery := fmt.Sprintf(`SELECT e.id, e.pubkey, e.created_at, e.kind, e.content FROM events_fts
		JOIN events e ON e.rowid = events_fts.rowid
		WHERE events_fts.content MATCH ?
		AND e.kind IN (%s)
		%s
		AND (e.created_at < ? OR (e.created_at = ? AND e.id < ?))
		ORDER BY bm25(events_fts), e.created_at DESC, e.id DESC
		LIMIT ?`, placeholders(len(kinds)), authorFilterClause(authors))
	args := make([]any, 0, 1+len(kinds)+len(authors)+4)
	args = append(args, query.Text.Match)
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

// searchCacheWindow reports the oldest/newest seen_at recorded for events that
// match the kind+author filter. The cache_events join is scoped to the
// "search" cache so unrelated scopes (e.g. "reads_authors") cannot bleed into
// the search window. Multiple search cache_keys (e.g. "all" and "authors")
// still feed into the same window: a network-scoped search can surface "the
// cache last warmed at X" even when only the broader "all" sweep has run, and
// vice versa, since the kind+author filter on events ultimately decides what
// can match the query.
func (s *Store) searchCacheWindow(ctx context.Context, kinds []int, authors []string) (int64, int64, error) {
	query := fmt.Sprintf(`SELECT COALESCE(MIN(ce.seen_at), 0), COALESCE(MAX(ce.seen_at), 0) FROM cache_events ce
		JOIN events e ON e.id = ce.event_id
		WHERE ce.scope = 'search'
		AND e.kind IN (%s)
		%s`, placeholders(len(kinds)), authorFilterClause(authors))
	args := make([]any, 0, len(kinds)+len(authors))
	for _, kind := range kinds {
		args = append(args, kind)
	}
	for _, author := range authors {
		args = append(args, author)
	}
	var oldest int64
	var latest int64
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&oldest, &latest); err != nil {
		return 0, 0, err
	}
	return oldest, latest, nil
}

func authorFilterClause(authors []string) string {
	if len(authors) == 0 {
		return ""
	}
	return fmt.Sprintf("AND e.pubkey IN (%s)", placeholders(len(authors)))
}

func uniqueKinds(kinds []int) []int {
	if len(kinds) == 0 {
		return nil
	}
	seen := make(map[int]struct{}, len(kinds))
	out := make([]int, 0, len(kinds))
	for _, kind := range kinds {
		if _, ok := seen[kind]; ok {
			continue
		}
		seen[kind] = struct{}{}
		out = append(out, kind)
	}
	return out
}

func buildFTSQuery(tokens []string) string {
	if len(tokens) == 0 {
		return ""
	}
	parts := make([]string, 0, len(tokens))
	for _, token := range tokens {
		parts = append(parts, token+"*")
	}
	return strings.Join(parts, " AND ")
}

func tokenizeSearch(raw string) []string {
	cleaned := strings.TrimSpace(strings.ToLower(raw))
	if cleaned == "" {
		return nil
	}
	if utf8.RuneCountInString(cleaned) > SearchMaxQueryRunes {
		runes := []rune(cleaned)
		cleaned = string(runes[:SearchMaxQueryRunes])
	}
	parts := strings.FieldsFunc(cleaned, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	if len(parts) == 0 {
		return nil
	}
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		runeCount := utf8.RuneCountInString(part)
		if runeCount < SearchMinTokenRunes {
			continue
		}
		if runeCount > SearchMaxTokenRunes {
			part = string([]rune(part)[:SearchMaxTokenRunes])
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
		if len(out) >= SearchMaxTokens {
			break
		}
	}
	return out
}

// PrepareSearch canonicalizes text query input once so callers can reuse the
// stable display/cache text and the FTS match string.
func PrepareSearch(raw string) PreparedSearch {
	tokens := tokenizeSearch(raw)
	if len(tokens) == 0 {
		return PreparedSearch{}
	}
	return PreparedSearch{
		Normalized: strings.Join(tokens, " "),
		Match:      buildFTSQuery(tokens),
	}
}
