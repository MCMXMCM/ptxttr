package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/thread"
)

type NoteLink struct {
	NoteID       string
	AuthorPubKey string
	RootID       string
	ParentID     string
	CreatedAt    int64
}

type RelayHintSet struct {
	Write []string
	Read  []string
	All   []string
}

type HydrationTarget struct {
	EntityType string
	EntityID   string
	Priority   int
}

// EntityTypeSeedContact queues pubkeys for background WoT seed graph BFS.
const EntityTypeSeedContact = "seedContact"

// HydrationTargetKey is the composite key used when deduping targets in memory and in the httpx touch debouncer.
func HydrationTargetKey(t HydrationTarget) string {
	return t.EntityType + "\x00" + t.EntityID
}

// NormalizeHydrationTargets merges rows that share (entity_type, entity_id) by max priority
// and returns them in deterministic sort order.
func NormalizeHydrationTargets(targets []HydrationTarget) []HydrationTarget {
	if len(targets) == 0 {
		return nil
	}
	merged := make(map[string]HydrationTarget, len(targets))
	for _, target := range targets {
		if target.EntityType == "" || target.EntityID == "" {
			continue
		}
		key := HydrationTargetKey(target)
		current, ok := merged[key]
		if !ok || target.Priority > current.Priority {
			merged[key] = target
		}
	}
	if len(merged) == 0 {
		return nil
	}
	out := make([]HydrationTarget, 0, len(merged))
	for _, target := range merged {
		out = append(out, target)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].EntityType == out[j].EntityType {
			return out[i].EntityID < out[j].EntityID
		}
		return out[i].EntityType < out[j].EntityType
	})
	return out
}

type FollowPageResult struct {
	Pubkeys       []string
	FilteredTotal int
	CachedTotal   int
}

func (s *Store) projectEventTx(ctx context.Context, tx *sql.Tx, event nostrx.Event, now int64) error {
	switch event.Kind {
	case nostrx.KindProfileMetadata:
		return upsertProfileProjectionTx(ctx, tx, event, now)
	case nostrx.KindTextNote, nostrx.KindComment:
		return upsertNoteProjectionTx(ctx, tx, event, now)
	case nostrx.KindFollowList:
		return replaceFollowEdgesTx(ctx, tx, event)
	case nostrx.KindRelayListMetadata:
		return upsertRelayHintsProjectionTx(ctx, tx, event, now)
	case nostrx.KindReaction:
		return upsertNoteReactionLatestTx(ctx, tx, event)
	default:
		return nil
	}
}

func (s *Store) RebuildProjections(ctx context.Context) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM profiles_cache`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM note_links`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM note_stats`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM note_reaction_latest`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM follow_edges`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM relay_hints_cache`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM relay_hints_ranked`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM contact_relay_hints`); err != nil {
		return err
	}

	rows, err := tx.QueryContext(ctx, `SELECT raw_json FROM events WHERE kind IN (?, ?, ?, ?, ?, ?) ORDER BY created_at ASC, id ASC`,
		nostrx.KindProfileMetadata, nostrx.KindTextNote, nostrx.KindComment, nostrx.KindFollowList, nostrx.KindRelayListMetadata, nostrx.KindReaction)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	now := time.Now().Unix()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		event, err := decodeEvent(raw)
		if err != nil {
			return err
		}
		if err := s.projectEventTx(ctx, tx, *event, now); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.clearRelayHintsBestEffort()
	s.clearProfileSummariesBestEffort()
	s.clearReplyStatsBestEffort()
	if s.sidecar != nil {
		s.sidecar.purgeAll()
	}
	return nil
}

func (s *Store) ProfileSummariesByPubkeys(ctx context.Context, pubkeys []string) (map[string]ProfileSummary, error) {
	out := make(map[string]ProfileSummary, len(pubkeys))
	keys := uniqueNonEmpty(pubkeys)
	if len(keys) == 0 {
		return out, nil
	}
	missing := append([]string(nil), keys...)
	if s.sidecar != nil {
		cached, miss := s.sidecar.getProfile(keys)
		for k, v := range cached {
			out[k] = v
		}
		missing = miss
	}
	if len(missing) == 0 {
		return out, nil
	}
	fresh, err := s.profileSummariesFromSQLite(ctx, missing)
	if err != nil {
		return nil, err
	}
	toCache := make([]ProfileSummary, 0, len(fresh))
	for _, key := range missing {
		summary, ok := fresh[key]
		if !ok {
			continue
		}
		out[key] = summary
		toCache = append(toCache, summary)
	}
	s.putProfileSummariesBestEffort(toCache)
	return out, nil
}

func (s *Store) profileSummariesFromSQLite(ctx context.Context, pubkeys []string) (map[string]ProfileSummary, error) {
	out := make(map[string]ProfileSummary, len(pubkeys))
	keys := uniqueNonEmpty(pubkeys)
	if len(keys) == 0 {
		return out, nil
	}
	query := fmt.Sprintf(`SELECT pubkey, display_name, name, about, picture, nip05 FROM profiles_cache WHERE pubkey IN (%s)`, placeholders(len(keys)))
	args := make([]any, 0, len(keys))
	for _, key := range keys {
		args = append(args, key)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var item ProfileSummary
		if err := rows.Scan(&item.PubKey, &item.DisplayName, &item.Name, &item.About, &item.Picture, &item.NIP05); err != nil {
			return nil, err
		}
		out[item.PubKey] = item
	}
	return out, rows.Err()
}

func (s *Store) putProfileSummariesBestEffort(summaries []ProfileSummary) {
	if s == nil || s.sidecar == nil || len(summaries) == 0 {
		return
	}
	m := make(map[string]ProfileSummary, len(summaries))
	for _, sum := range summaries {
		if sum.PubKey == "" {
			continue
		}
		m[sum.PubKey] = sum
	}
	s.sidecar.putProfileMulti(m)
}

func (s *Store) syncProfileSummariesBestEffort(ctx context.Context, pubkeys []string) {
	if s == nil || s.sidecar == nil {
		return
	}
	keys := uniqueNonEmpty(pubkeys)
	if len(keys) == 0 {
		return
	}
	fresh, err := s.profileSummariesFromSQLite(ctx, keys)
	if err != nil {
		slog.Debug("profile sidecar sync query failed", "count", len(keys), "err", err)
		return
	}
	summaries := make([]ProfileSummary, 0, len(fresh))
	for _, key := range keys {
		summary, ok := fresh[key]
		if !ok {
			continue
		}
		summaries = append(summaries, summary)
	}
	s.putProfileSummariesBestEffort(summaries)
}

func (s *Store) clearProfileSummariesBestEffort() {
	if s == nil || s.sidecar == nil {
		return
	}
	s.sidecar.purgeProfile()
}

func (s *Store) ReplyStatsByNoteIDs(ctx context.Context, ids []string) (map[string]ReplyStat, error) {
	out := make(map[string]ReplyStat, len(ids))
	keys := uniqueNonEmpty(ids)
	if len(keys) == 0 {
		return out, nil
	}
	missing := append([]string(nil), keys...)
	if s.sidecar != nil {
		cached, miss := s.sidecar.getReply(keys)
		for k, v := range cached {
			out[k] = v
		}
		missing = miss
	}
	if len(missing) == 0 {
		return out, nil
	}
	fresh, err := s.replyStatsFromSQLite(ctx, missing)
	if err != nil {
		return nil, err
	}
	toCache := make(map[string]ReplyStat, len(missing))
	for _, id := range missing {
		stat, ok := fresh[id]
		if !ok {
			out[id] = ReplyStat{}
			continue
		}
		out[id] = stat
		toCache[id] = stat
	}
	s.putReplyStatsBestEffort(toCache)
	return out, nil
}

func (s *Store) replyStatsFromSQLite(ctx context.Context, ids []string) (map[string]ReplyStat, error) {
	out := make(map[string]ReplyStat, len(ids))
	keys := uniqueNonEmpty(ids)
	if len(keys) == 0 {
		return out, nil
	}
	query := fmt.Sprintf(`SELECT note_id, direct_reply_count, descendant_reply_count, last_reply_event_at FROM note_stats WHERE note_id IN (%s)`, placeholders(len(keys)))
	args := make([]any, 0, len(keys))
	for _, id := range keys {
		args = append(args, id)
		out[id] = ReplyStat{}
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var stat ReplyStat
		if err := rows.Scan(&id, &stat.DirectReplies, &stat.DescendantReplies, &stat.LastReplyEventAt); err != nil {
			return nil, err
		}
		out[id] = stat
	}
	return out, rows.Err()
}

func (s *Store) putReplyStatsBestEffort(stats map[string]ReplyStat) {
	if s == nil || s.sidecar == nil || len(stats) == 0 {
		return
	}
	s.sidecar.putReplyMulti(stats)
}

func (s *Store) syncReplyStatsBestEffort(ctx context.Context, noteIDs []string) {
	if s == nil || s.sidecar == nil {
		return
	}
	keys := uniqueNonEmpty(noteIDs)
	if len(keys) == 0 {
		return
	}
	fresh, err := s.replyStatsFromSQLite(ctx, keys)
	if err != nil {
		slog.Debug("reply stats sidecar sync query failed", "count", len(keys), "err", err)
		return
	}
	for _, key := range keys {
		if _, ok := fresh[key]; !ok {
			fresh[key] = ReplyStat{}
		}
	}
	s.putReplyStatsBestEffort(fresh)
}

func (s *Store) clearReplyStatsBestEffort() {
	if s == nil || s.sidecar == nil {
		return
	}
	s.sidecar.purgeReply()
}

func (s *Store) ThreadEdges(ctx context.Context, parentIDs []string, limit int) ([]NoteLink, error) {
	return s.ThreadEdgesCursor(ctx, parentIDs, 0, "", limit)
}

func (s *Store) ThreadEdgesCursor(ctx context.Context, parentIDs []string, cursor int64, cursorID string, limit int) ([]NoteLink, error) {
	parents := uniqueNonEmpty(parentIDs)
	if len(parents) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 500
	}
	query := fmt.Sprintf(`SELECT note_id, author_pubkey, root_id, parent_id, created_at
		FROM note_links
		WHERE parent_id IN (%s) AND note_id != parent_id`, placeholders(len(parents)))
	args := make([]any, 0, len(parents)+4)
	for _, parent := range parents {
		args = append(args, parent)
	}
	if cursor > 0 {
		query += ` AND (created_at > ? OR (created_at = ? AND note_id > ?))`
		args = append(args, cursor, cursor, cursorID)
	}
	query += `
		ORDER BY created_at ASC, note_id ASC
		LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []NoteLink
	for rows.Next() {
		var link NoteLink
		if err := rows.Scan(&link.NoteID, &link.AuthorPubKey, &link.RootID, &link.ParentID, &link.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, link)
	}
	return out, rows.Err()
}

// DistinctAuthorsUnderRoot returns up to `limit` distinct author pubkeys for
// notes that share the given root_id (the root itself plus any direct or
// nested replies). Backed by the existing idx_note_links_root_created index.
func (s *Store) DistinctAuthorsUnderRoot(ctx context.Context, rootID string, limit int) ([]string, error) {
	if rootID == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 1000 {
		limit = 250
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT author_pubkey FROM note_links WHERE root_id = ? LIMIT ?`, rootID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]string, 0, limit)
	for rows.Next() {
		var pubkey string
		if err := rows.Scan(&pubkey); err != nil {
			return nil, err
		}
		if pubkey != "" {
			out = append(out, pubkey)
		}
	}
	return out, rows.Err()
}

func (s *Store) FollowingPubkeys(ctx context.Context, owner string, limit int) ([]string, error) {
	if owner == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT target_pubkey FROM follow_edges WHERE owner_pubkey = ? ORDER BY target_pubkey ASC LIMIT ?`, owner, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var pubkey string
		if err := rows.Scan(&pubkey); err != nil {
			return nil, err
		}
		out = append(out, pubkey)
	}
	return out, rows.Err()
}

func (s *Store) FollowingPubkeysAfter(ctx context.Context, owner string, after string, limit int) ([]string, error) {
	if owner == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT target_pubkey
		FROM follow_edges
		WHERE owner_pubkey = ? AND target_pubkey > ?
		ORDER BY target_pubkey ASC
		LIMIT ?`, owner, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var pubkey string
		if err := rows.Scan(&pubkey); err != nil {
			return nil, err
		}
		out = append(out, pubkey)
	}
	return out, rows.Err()
}

func (s *Store) DistinctFollowOwnerCount(ctx context.Context) (int, error) {
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT owner_pubkey) FROM follow_edges`)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) FollowingPubkeysPage(ctx context.Context, owner string, query string, limit int, offset int) (FollowPageResult, error) {
	return s.followPubkeysPage(ctx, owner, query, limit, offset, true)
}

func (s *Store) FollowerPubkeys(ctx context.Context, target string, limit int) ([]string, error) {
	if target == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT owner_pubkey FROM follow_edges WHERE target_pubkey = ? ORDER BY owner_pubkey ASC LIMIT ?`, target, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var pubkey string
		if err := rows.Scan(&pubkey); err != nil {
			return nil, err
		}
		out = append(out, pubkey)
	}
	return out, rows.Err()
}

func (s *Store) FollowerPubkeysPage(ctx context.Context, target string, query string, limit int, offset int) (FollowPageResult, error) {
	return s.followPubkeysPage(ctx, target, query, limit, offset, false)
}

func (s *Store) followPubkeysPage(ctx context.Context, subject string, query string, limit int, offset int, following bool) (FollowPageResult, error) {
	if subject == "" {
		return FollowPageResult{}, nil
	}
	limit, offset = sanitizeFollowPageBounds(limit, offset)
	needlePrefix, needleContains := followSearchPatterns(query)
	subjectColumn := "owner_pubkey"
	peerColumn := "target_pubkey"
	if !following {
		subjectColumn = "target_pubkey"
		peerColumn = "owner_pubkey"
	}
	var cachedTotal int
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM follow_edges WHERE %s = ?`, subjectColumn)
	if err := s.db.QueryRowContext(ctx, countQuery, subject).Scan(&cachedTotal); err != nil {
		return FollowPageResult{}, err
	}
	pageQuery := fmt.Sprintf(`SELECT fe.%s, COUNT(*) OVER() AS filtered_total
		FROM follow_edges fe
		LEFT JOIN profiles_cache pc ON pc.pubkey = fe.%s
		WHERE fe.%s = ?
			AND (? = '' OR
				fe.%s LIKE ? OR
				LOWER(COALESCE(pc.display_name, '')) LIKE ? OR
				LOWER(COALESCE(pc.name, '')) LIKE ? OR
				LOWER(COALESCE(pc.nip05, '')) LIKE ? OR
				LOWER(COALESCE(pc.about, '')) LIKE ?)
		ORDER BY fe.%s ASC
		LIMIT ? OFFSET ?`, peerColumn, peerColumn, subjectColumn, peerColumn, peerColumn)
	args := []any{subject, needleContains, needlePrefix, needleContains, needleContains, needleContains, needleContains, limit, offset}
	rows, err := s.db.QueryContext(ctx, pageQuery, args...)
	if err != nil {
		return FollowPageResult{}, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]string, 0, limit)
	filteredTotal := 0
	for rows.Next() {
		var pubkey string
		var rowFilteredTotal int
		if err := rows.Scan(&pubkey, &rowFilteredTotal); err != nil {
			return FollowPageResult{}, err
		}
		if filteredTotal == 0 {
			filteredTotal = rowFilteredTotal
		}
		out = append(out, pubkey)
	}
	if err := rows.Err(); err != nil {
		return FollowPageResult{}, err
	}
	if len(out) == 0 && needleContains == "" {
		filteredTotal = cachedTotal
	}
	return FollowPageResult{
		Pubkeys:       out,
		FilteredTotal: filteredTotal,
		CachedTotal:   cachedTotal,
	}, nil
}

func (s *Store) RelayHintsForPubkey(ctx context.Context, pubkey string) ([]string, error) {
	return s.RelayHintsForPubkeyByUsage(ctx, pubkey, nostrx.RelayUsageAny)
}

func (s *Store) RelayHintsForPubkeyByUsage(ctx context.Context, pubkey string, usage nostrx.RelayUsage) ([]string, error) {
	if pubkey == "" {
		return nil, nil
	}
	if s.sidecar != nil {
		if set, ok := s.sidecar.getRelay(pubkey); ok {
			return relayHintValues(normalizeRelayHintSet(set), usage), nil
		}
	}
	set, ok, err := s.relayHintSetFromSQLite(ctx, pubkey)
	if err != nil || !ok {
		return nil, err
	}
	s.putRelayHintsBestEffort([]RelayHintSnapshot{{PubKey: pubkey, Set: set}})
	return relayHintValues(set, usage), nil
}

func (s *Store) RelayHintsByUsageForPubkeys(ctx context.Context, pubkeys []string) (map[string]RelayHintSet, error) {
	out := make(map[string]RelayHintSet, len(pubkeys))
	keys := uniqueNonEmpty(pubkeys)
	if len(keys) == 0 {
		return out, nil
	}
	missing := append([]string(nil), keys...)
	if s.sidecar != nil {
		cached, miss := s.sidecar.getRelayMulti(keys)
		for k, v := range cached {
			out[k] = normalizeRelayHintSet(v)
		}
		missing = miss
	}
	if len(missing) == 0 {
		return out, nil
	}
	fresh, err := s.relayHintSetsFromSQLite(ctx, missing)
	if err != nil {
		return nil, err
	}
	snapshots := make([]RelayHintSnapshot, 0, len(fresh))
	for _, key := range missing {
		set, ok := fresh[key]
		if !ok {
			continue
		}
		out[key] = set
		snapshots = append(snapshots, RelayHintSnapshot{PubKey: key, Set: set})
	}
	s.putRelayHintsBestEffort(snapshots)
	return out, nil
}

func relayHintValues(set RelayHintSet, usage nostrx.RelayUsage) []string {
	switch usage {
	case nostrx.RelayUsageRead:
		return append([]string(nil), set.Read...)
	case nostrx.RelayUsageWrite:
		return append([]string(nil), set.Write...)
	default:
		return append([]string(nil), set.All...)
	}
}

func normalizeRelayHintSet(set RelayHintSet) RelayHintSet {
	set.Write = normalizeRelayHintValues(set.Write)
	set.Read = normalizeRelayHintValues(set.Read)
	set.All = normalizeRelayHintValues(set.All)
	return set
}

func normalizeRelayHintValues(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func decodeRelayHintSet(rawWrite, rawRead, rawAll string) (RelayHintSet, error) {
	set := RelayHintSet{}
	if rawWrite != "" {
		if err := json.Unmarshal([]byte(rawWrite), &set.Write); err != nil {
			return RelayHintSet{}, err
		}
	}
	if rawRead != "" {
		if err := json.Unmarshal([]byte(rawRead), &set.Read); err != nil {
			return RelayHintSet{}, err
		}
	}
	if rawAll != "" {
		if err := json.Unmarshal([]byte(rawAll), &set.All); err != nil {
			return RelayHintSet{}, err
		}
	}
	return normalizeRelayHintSet(set), nil
}

func (s *Store) relayHintSetFromSQLite(ctx context.Context, pubkey string) (RelayHintSet, bool, error) {
	if pubkey == "" {
		return RelayHintSet{}, false, nil
	}
	var rawWrite string
	var rawRead string
	var rawAll string
	err := s.db.QueryRowContext(ctx, `SELECT write_relays_json, read_relays_json, all_relays_json
		FROM relay_hints_ranked
		WHERE pubkey = ?`, pubkey).Scan(&rawWrite, &rawRead, &rawAll)
	if err == nil {
		set, decodeErr := decodeRelayHintSet(rawWrite, rawRead, rawAll)
		return set, true, decodeErr
	}
	if err != sql.ErrNoRows {
		return RelayHintSet{}, false, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT relays_json FROM relay_hints_cache WHERE pubkey = ?`, pubkey).Scan(&rawAll)
	if err != nil {
		if err == sql.ErrNoRows {
			return RelayHintSet{}, false, nil
		}
		return RelayHintSet{}, false, err
	}
	set, decodeErr := decodeRelayHintSet("", "", rawAll)
	return set, true, decodeErr
}

func (s *Store) relayHintSetsFromSQLite(ctx context.Context, pubkeys []string) (map[string]RelayHintSet, error) {
	out := make(map[string]RelayHintSet, len(pubkeys))
	keys := uniqueNonEmpty(pubkeys)
	if len(keys) == 0 {
		return out, nil
	}
	query := fmt.Sprintf(`SELECT pubkey, write_relays_json, read_relays_json, all_relays_json
		FROM relay_hints_ranked
		WHERE pubkey IN (%s)`, placeholders(len(keys)))
	args := make([]any, 0, len(keys))
	for _, key := range keys {
		args = append(args, key)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var pubkey string
		var rawWrite string
		var rawRead string
		var rawAll string
		if err := rows.Scan(&pubkey, &rawWrite, &rawRead, &rawAll); err != nil {
			return nil, err
		}
		set, err := decodeRelayHintSet(rawWrite, rawRead, rawAll)
		if err != nil {
			return nil, err
		}
		out[pubkey] = set
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	missing := make([]string, 0, len(keys))
	for _, key := range keys {
		if _, ok := out[key]; !ok {
			missing = append(missing, key)
		}
	}
	if len(missing) == 0 {
		return out, nil
	}
	fallbackQuery := fmt.Sprintf(`SELECT pubkey, relays_json FROM relay_hints_cache WHERE pubkey IN (%s)`, placeholders(len(missing)))
	fallbackArgs := make([]any, 0, len(missing))
	for _, key := range missing {
		fallbackArgs = append(fallbackArgs, key)
	}
	fallbackRows, err := s.db.QueryContext(ctx, fallbackQuery, fallbackArgs...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = fallbackRows.Close() }()
	for fallbackRows.Next() {
		var pubkey string
		var rawAll string
		if err := fallbackRows.Scan(&pubkey, &rawAll); err != nil {
			return nil, err
		}
		set, err := decodeRelayHintSet("", "", rawAll)
		if err != nil {
			return nil, err
		}
		out[pubkey] = set
	}
	return out, fallbackRows.Err()
}

func (s *Store) putRelayHintsBestEffort(snapshots []RelayHintSnapshot) {
	if s == nil || s.sidecar == nil || len(snapshots) == 0 {
		return
	}
	m := make(map[string]RelayHintSet, len(snapshots))
	for _, sn := range snapshots {
		if sn.PubKey == "" {
			continue
		}
		m[sn.PubKey] = sn.Set
	}
	s.sidecar.putRelayMulti(m)
}

func (s *Store) syncRelayHintsBestEffort(ctx context.Context, pubkeys []string) {
	if s == nil || s.sidecar == nil {
		return
	}
	keys := uniqueNonEmpty(pubkeys)
	if len(keys) == 0 {
		return
	}
	fresh, err := s.relayHintSetsFromSQLite(ctx, keys)
	if err != nil {
		slog.Debug("relay hint sidecar sync query failed", "count", len(keys), "err", err)
		return
	}
	snapshots := make([]RelayHintSnapshot, 0, len(fresh))
	for _, key := range keys {
		set, ok := fresh[key]
		if !ok {
			continue
		}
		snapshots = append(snapshots, RelayHintSnapshot{PubKey: key, Set: set})
	}
	s.putRelayHintsBestEffort(snapshots)
}

func (s *Store) clearRelayHintsBestEffort() {
	if s == nil || s.sidecar == nil {
		return
	}
	s.sidecar.purgeRelay()
}

func (s *Store) ContactRelayHintsForOwner(ctx context.Context, owner string, targets []string, limit int) (map[string][]string, error) {
	out := make(map[string][]string, len(targets))
	if owner == "" {
		return out, nil
	}
	keys := uniqueNonEmpty(targets)
	if len(keys) == 0 {
		return out, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query := fmt.Sprintf(`SELECT target_pubkey, relay_url FROM contact_relay_hints
		WHERE owner_pubkey = ? AND target_pubkey IN (%s)
		ORDER BY target_pubkey ASC, created_at DESC, relay_url ASC LIMIT ?`, placeholders(len(keys)))
	args := make([]any, 0, len(keys)+2)
	args = append(args, owner)
	for _, key := range keys {
		args = append(args, key)
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var pubkey string
		var relay string
		if err := rows.Scan(&pubkey, &relay); err != nil {
			return nil, err
		}
		out[pubkey] = append(out[pubkey], relay)
	}
	return out, rows.Err()
}

func (s *Store) ObservedRelaysForAuthors(ctx context.Context, authors []string, kinds []int, limitPerAuthor int) (map[string][]string, error) {
	out := make(map[string][]string, len(authors))
	keys := uniqueNonEmpty(authors)
	if len(keys) == 0 {
		return out, nil
	}
	if len(kinds) == 0 {
		kinds = []int{nostrx.KindTextNote, nostrx.KindProfileMetadata, nostrx.KindRelayListMetadata}
	}
	if limitPerAuthor <= 0 || limitPerAuthor > 10 {
		limitPerAuthor = 3
	}

	query := fmt.Sprintf(`SELECT e.pubkey, re.relay_url, MAX(re.last_seen) AS last_seen
		FROM relay_events re
		JOIN events e ON e.id = re.event_id
		WHERE e.pubkey IN (%s) AND e.kind IN (%s)
		GROUP BY e.pubkey, re.relay_url
		ORDER BY e.pubkey ASC, last_seen DESC`, placeholders(len(keys)), placeholders(len(kinds)))
	args := make([]any, 0, len(keys)+len(kinds))
	for _, key := range keys {
		args = append(args, key)
	}
	for _, kind := range kinds {
		args = append(args, kind)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	counts := make(map[string]int, len(keys))
	for rows.Next() {
		var pubkey string
		var relay string
		var lastSeen int64
		if err := rows.Scan(&pubkey, &relay, &lastSeen); err != nil {
			return nil, err
		}
		if counts[pubkey] >= limitPerAuthor {
			continue
		}
		out[pubkey] = append(out[pubkey], relay)
		counts[pubkey]++
	}
	return out, rows.Err()
}

func (s *Store) SecondHopFollowingPubkeys(ctx context.Context, owner string, limit int) ([]string, error) {
	if owner == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT f2.target_pubkey
		FROM follow_edges f1
		JOIN follow_edges f2 ON f2.owner_pubkey = f1.target_pubkey
		LEFT JOIN follow_edges direct ON direct.owner_pubkey = f1.owner_pubkey AND direct.target_pubkey = f2.target_pubkey
		WHERE f1.owner_pubkey = ? AND f2.target_pubkey != ? AND direct.target_pubkey IS NULL
		ORDER BY f2.target_pubkey ASC
		LIMIT ?`, owner, owner, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var pubkey string
		if err := rows.Scan(&pubkey); err != nil {
			return nil, err
		}
		out = append(out, pubkey)
	}
	return out, rows.Err()
}

func (s *Store) TouchHydrationTarget(ctx context.Context, entityType, entityID string, priority int) error {
	return s.TouchHydrationTargetsBatch(ctx, []HydrationTarget{{
		EntityType: entityType,
		EntityID:   entityID,
		Priority:   priority,
	}})
}

func (s *Store) TouchHydrationTargets(ctx context.Context, entityType string, entityIDs []string, priority int) error {
	if entityType == "" || len(entityIDs) == 0 {
		return nil
	}
	targets := make([]HydrationTarget, 0, len(entityIDs))
	for _, entityID := range uniqueNonEmpty(entityIDs) {
		targets = append(targets, HydrationTarget{
			EntityType: entityType,
			EntityID:   entityID,
			Priority:   priority,
		})
	}
	return s.TouchHydrationTargetsBatch(ctx, targets)
}

func (s *Store) TouchHydrationTargetsBatch(ctx context.Context, targets []HydrationTarget) error {
	ordered := NormalizeHydrationTargets(targets)
	if len(ordered) == 0 {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO hydration_state(entity_type, entity_id, priority)
		VALUES (?, ?, ?)
		ON CONFLICT(entity_type, entity_id) DO UPDATE SET priority = MAX(priority, excluded.priority)`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, target := range ordered {
		if _, err := stmt.ExecContext(ctx, target.EntityType, target.EntityID, target.Priority); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) MarkHydrationAttempt(ctx context.Context, entityType, entityID string, success bool, backoff time.Duration) error {
	if entityType == "" || entityID == "" {
		return nil
	}
	now := time.Now().Unix()
	retryAfter := int64(backoff.Seconds())
	if retryAfter < 1 {
		retryAfter = 1
	}
	nextRetry := now + retryAfter
	status := "failed"
	failDelta := 1
	if success {
		status = "ok"
		failDelta = 0
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `INSERT INTO hydration_state(
			entity_type, entity_id, status, last_attempt_at, last_success_at, next_retry_at, fail_count
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(entity_type, entity_id) DO UPDATE SET
			status = excluded.status,
			last_attempt_at = excluded.last_attempt_at,
			last_success_at = CASE WHEN excluded.last_success_at > 0 THEN excluded.last_success_at ELSE hydration_state.last_success_at END,
			next_retry_at = excluded.next_retry_at,
			fail_count = CASE WHEN ? = 1 THEN 0 ELSE hydration_state.fail_count + 1 END`,
		entityType, entityID, status, now, ternaryInt64(success, now, 0), nextRetry, failDelta, ternaryInt(success, 1, 0))
	return err
}

func (s *Store) StaleHydrationBatch(ctx context.Context, entityType string, now int64, limit int) ([]HydrationTarget, error) {
	if entityType == "" {
		return nil, nil
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	if limit <= 0 {
		limit = 32
	}
	rows, err := s.db.QueryContext(ctx, `SELECT entity_type, entity_id, priority
		FROM hydration_state
		WHERE entity_type = ? AND (next_retry_at = 0 OR next_retry_at <= ?)
		ORDER BY priority DESC, last_success_at ASC, last_attempt_at ASC
		LIMIT ?`, entityType, now, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []HydrationTarget
	for rows.Next() {
		var target HydrationTarget
		if err := rows.Scan(&target.EntityType, &target.EntityID, &target.Priority); err != nil {
			return nil, err
		}
		out = append(out, target)
	}
	return out, rows.Err()
}

// StaleSeedContactBatch returns seedContact hydration rows that are due for
// work and have fail_count strictly less than maxFailExclusive. When
// maxFailExclusive <= 0, a default of 12 is used so repeatedly failing contacts
// are temporarily excluded until re-touched via TouchSeedContactFrontier.
func (s *Store) StaleSeedContactBatch(ctx context.Context, now int64, limit int, maxFailExclusive int) ([]HydrationTarget, error) {
	if now <= 0 {
		now = time.Now().Unix()
	}
	if limit <= 0 {
		limit = 32
	}
	if maxFailExclusive <= 0 {
		maxFailExclusive = 12
	}
	rows, err := s.db.QueryContext(ctx, `SELECT entity_type, entity_id, priority
		FROM hydration_state
		WHERE entity_type = ? AND (next_retry_at = 0 OR next_retry_at <= ?)
		AND status != 'ok'
		AND fail_count < ?
		ORDER BY priority DESC, last_success_at ASC, last_attempt_at ASC
		LIMIT ?`, EntityTypeSeedContact, now, maxFailExclusive, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []HydrationTarget
	for rows.Next() {
		var target HydrationTarget
		if err := rows.Scan(&target.EntityType, &target.EntityID, &target.Priority); err != nil {
			return nil, err
		}
		out = append(out, target)
	}
	return out, rows.Err()
}

// TouchSeedContactFrontier upserts seedContact rows, resetting retry state so
// rediscovered pubkeys are eligible for background hydration again.
func (s *Store) TouchSeedContactFrontier(ctx context.Context, pubkeys []string, priority int) error {
	if s == nil || len(pubkeys) == 0 {
		return nil
	}
	targets := make([]HydrationTarget, 0, len(pubkeys))
	seen := make(map[string]bool, len(pubkeys))
	for _, pk := range pubkeys {
		if pk == "" || seen[pk] {
			continue
		}
		seen[pk] = true
		targets = append(targets, HydrationTarget{
			EntityType: EntityTypeSeedContact,
			EntityID:   pk,
			Priority:   priority,
		})
	}
	ordered := NormalizeHydrationTargets(targets)
	if len(ordered) == 0 {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO hydration_state(
			entity_type, entity_id, status, last_attempt_at, last_success_at, next_retry_at, fail_count, priority
		) VALUES (?, ?, 'pending', 0, 0, 0, 0, ?)
		ON CONFLICT(entity_type, entity_id) DO UPDATE SET
			priority = MAX(hydration_state.priority, excluded.priority),
			fail_count = 0,
			next_retry_at = 0,
			status = 'pending'`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, target := range ordered {
		if _, err := stmt.ExecContext(ctx, target.EntityType, target.EntityID, target.Priority); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteHydrationState removes one hydration_state row.
func (s *Store) DeleteHydrationState(ctx context.Context, entityType, entityID string) error {
	if s == nil || entityType == "" || entityID == "" {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `DELETE FROM hydration_state WHERE entity_type = ? AND entity_id = ?`, entityType, entityID)
	return err
}

func upsertProfileProjectionTx(ctx context.Context, tx *sql.Tx, event nostrx.Event, now int64) error {
	profile := nostrx.ParseProfile(event.PubKey, &event)
	_, err := tx.ExecContext(ctx, `INSERT INTO profiles_cache(
			pubkey, profile_event_id, created_at, display_name, name, about, picture, nip05, last_metadata_fetch_at, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(pubkey) DO UPDATE SET
			profile_event_id = excluded.profile_event_id,
			created_at = excluded.created_at,
			display_name = excluded.display_name,
			name = excluded.name,
			about = excluded.about,
			picture = excluded.picture,
			nip05 = excluded.nip05,
			last_metadata_fetch_at = excluded.last_metadata_fetch_at,
			last_seen_at = excluded.last_seen_at
		WHERE excluded.created_at > profiles_cache.created_at
			OR (excluded.created_at = profiles_cache.created_at AND excluded.profile_event_id > profiles_cache.profile_event_id)`,
		event.PubKey, event.ID, event.CreatedAt, profile.Display, profile.Name, profile.About, profile.Picture, profile.NIP05, now, now)
	return err
}

func upsertNoteProjectionTx(ctx context.Context, tx *sql.Tx, event nostrx.Event, now int64) error {
	rootID, parentID := noteRootParent(event)
	if parentID == "" {
		resolvedParentID, err := resolveAddressParentTx(ctx, tx, event)
		if err != nil {
			return err
		}
		if resolvedParentID != "" {
			parentID = resolvedParentID
			if rootID == "" {
				rootID = resolvedParentID
			}
		}
	}
	if parentID != "" && parentID != event.ID {
		var parentRootID string
		err := tx.QueryRowContext(ctx, `SELECT root_id FROM note_links WHERE note_id = ?`, parentID).Scan(&parentRootID)
		if err == nil && parentRootID != "" {
			rootID = parentRootID
		} else if err != nil && err != sql.ErrNoRows {
			return err
		}
	}
	if rootID == "" {
		rootID = event.ID
	}
	if parentID == "" {
		parentID = rootID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO note_links(note_id, author_pubkey, root_id, parent_id, created_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(note_id) DO UPDATE SET
			author_pubkey = excluded.author_pubkey,
			root_id = excluded.root_id,
			parent_id = excluded.parent_id,
			created_at = excluded.created_at,
			last_seen_at = excluded.last_seen_at`,
		event.ID, event.PubKey, rootID, parentID, event.CreatedAt, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO note_stats(note_id, last_reply_event_at)
		VALUES (?, ?)
		ON CONFLICT(note_id) DO UPDATE SET
			last_reply_event_at = MAX(note_stats.last_reply_event_at, excluded.last_reply_event_at)`,
		event.ID, event.CreatedAt); err != nil {
		return err
	}
	if parentID != "" && parentID != event.ID {
		if _, err := tx.ExecContext(ctx, `INSERT INTO note_stats(note_id, direct_reply_count, last_reply_event_at)
			VALUES (?, 1, ?)
			ON CONFLICT(note_id) DO UPDATE SET
				direct_reply_count = note_stats.direct_reply_count + 1,
				last_reply_event_at = MAX(note_stats.last_reply_event_at, excluded.last_reply_event_at)`,
			parentID, event.CreatedAt); err != nil {
			return err
		}
	}
	if err := backfillNoteReactionLatestForTargetTx(ctx, tx, event.ID); err != nil {
		return err
	}
	return nil
}

func resolveAddressParentTx(ctx context.Context, tx *sql.Tx, event nostrx.Event) (string, error) {
	for _, tag := range event.Tags {
		if len(tag) < 2 || tag[0] != "a" {
			continue
		}
		parts := strings.SplitN(strings.TrimSpace(tag[1]), ":", 3)
		if len(parts) != 3 {
			continue
		}
		kind, err := strconv.Atoi(parts[0])
		if err != nil || kind != nostrx.KindLongForm {
			continue
		}
		author := strings.TrimSpace(parts[1])
		identifier := strings.TrimSpace(parts[2])
		if author == "" || identifier == "" {
			continue
		}
		var parentID string
		err = tx.QueryRowContext(ctx, `SELECT e.id
			FROM events e
			JOIN tags t ON t.event_id = e.id AND t.name = 'd'
			WHERE e.kind = ? AND e.pubkey = ? AND t.value = ?
			ORDER BY e.created_at DESC, e.id DESC
			LIMIT 1`, nostrx.KindLongForm, author, identifier).Scan(&parentID)
		if err == nil {
			return parentID, nil
		}
		if err != sql.ErrNoRows {
			return "", err
		}
	}
	return "", nil
}

func applyReplyStatUpdateTx(ctx context.Context, tx *sql.Tx, noteID string, directReplies int, descendantReplies int, lastReplyAt int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO note_stats(note_id, direct_reply_count, descendant_reply_count, last_reply_event_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(note_id) DO UPDATE SET
			direct_reply_count = excluded.direct_reply_count,
			descendant_reply_count = excluded.descendant_reply_count,
			last_reply_event_at = MAX(note_stats.last_reply_event_at, excluded.last_reply_event_at)`,
		noteID, directReplies, descendantReplies, lastReplyAt)
	return err
}

func (s *Store) countDirectReplies(ctx context.Context, noteID string) (int, error) {
	var directReplies int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM note_links WHERE parent_id = ? AND note_id != ?`, noteID, noteID).Scan(&directReplies)
	return directReplies, err
}

func (s *Store) descendantCount(ctx context.Context, rootID string) (int, error) {
	var total int
	err := s.db.QueryRowContext(ctx, `WITH RECURSIVE descendants(note_id) AS (
			SELECT note_id FROM note_links WHERE parent_id = ? AND note_id != parent_id
			UNION
			SELECT nl.note_id FROM note_links nl
			JOIN descendants d ON nl.parent_id = d.note_id
			WHERE nl.note_id != nl.parent_id
		)
		SELECT COUNT(DISTINCT note_id) FROM descendants`, rootID).Scan(&total)
	return total, err
}

func replaceFollowEdgesTx(ctx context.Context, tx *sql.Tx, event nostrx.Event) error {
	winner, err := isLatestReplaceableEventTx(ctx, tx, event.PubKey, nostrx.KindFollowList, event.ID)
	if err != nil {
		return err
	}
	if !winner {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM follow_edges WHERE owner_pubkey = ?`, event.PubKey); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM contact_relay_hints WHERE owner_pubkey = ?`, event.PubKey); err != nil {
		return err
	}
	targets := make([]string, 0, len(event.Tags))
	seen := make(map[string]bool, len(event.Tags))
	for _, tag := range event.Tags {
		if len(tag) < 2 || tag[0] != "p" || tag[1] == "" || seen[tag[1]] {
			continue
		}
		seen[tag[1]] = true
		targets = append(targets, tag[1])
	}
	for _, target := range targets {
		if _, err := tx.ExecContext(ctx, `INSERT INTO follow_edges(owner_pubkey, target_pubkey, follow_event_id, created_at)
			VALUES (?, ?, ?, ?)`, event.PubKey, target, event.ID, event.CreatedAt); err != nil {
			return err
		}
	}
	for _, hint := range nostrx.FollowRelayHints(&event, 256) {
		if _, err := tx.ExecContext(ctx, `INSERT INTO contact_relay_hints(owner_pubkey, target_pubkey, relay_url, follow_event_id, created_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(owner_pubkey, target_pubkey) DO UPDATE SET
				relay_url = excluded.relay_url,
				follow_event_id = excluded.follow_event_id,
				created_at = excluded.created_at
			WHERE excluded.created_at > contact_relay_hints.created_at
				OR (excluded.created_at = contact_relay_hints.created_at AND excluded.follow_event_id > contact_relay_hints.follow_event_id)`,
			event.PubKey, hint.PubKey, hint.Relay, event.ID, event.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}

func isLatestReplaceableEventTx(ctx context.Context, tx *sql.Tx, pubkey string, kind int, eventID string) (bool, error) {
	if tx == nil || pubkey == "" || eventID == "" {
		return false, nil
	}
	var latestID string
	err := tx.QueryRowContext(ctx, `SELECT id FROM events WHERE pubkey = ? AND kind = ? ORDER BY created_at DESC, id DESC LIMIT 1`, pubkey, kind).Scan(&latestID)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return latestID == eventID, nil
}

func upsertRelayHintsProjectionTx(ctx context.Context, tx *sql.Tx, event nostrx.Event, now int64) error {
	relays := nostrx.RelayURLsForUsage(&event, nostrx.MaxRelays, nostrx.RelayUsageAny)
	rawAll, err := json.Marshal(relays)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO relay_hints_cache(pubkey, relay_event_id, relays_json, created_at, last_fetch_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(pubkey) DO UPDATE SET
			relay_event_id = excluded.relay_event_id,
			relays_json = excluded.relays_json,
			created_at = excluded.created_at,
			last_fetch_at = excluded.last_fetch_at
		WHERE excluded.created_at > relay_hints_cache.created_at
			OR (excluded.created_at = relay_hints_cache.created_at AND excluded.relay_event_id > relay_hints_cache.relay_event_id)`,
		event.PubKey, event.ID, string(rawAll), event.CreatedAt, now)
	if err != nil {
		return err
	}

	writeRelays := nostrx.RelayURLsForUsage(&event, nostrx.MaxRelays, nostrx.RelayUsageWrite)
	readRelays := nostrx.RelayURLsForUsage(&event, nostrx.MaxRelays, nostrx.RelayUsageRead)
	rawWrite, err := json.Marshal(writeRelays)
	if err != nil {
		return err
	}
	rawRead, err := json.Marshal(readRelays)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO relay_hints_ranked(pubkey, relay_event_id, write_relays_json, read_relays_json, all_relays_json, created_at, last_fetch_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(pubkey) DO UPDATE SET
			relay_event_id = excluded.relay_event_id,
			write_relays_json = excluded.write_relays_json,
			read_relays_json = excluded.read_relays_json,
			all_relays_json = excluded.all_relays_json,
			created_at = excluded.created_at,
			last_fetch_at = excluded.last_fetch_at
		WHERE excluded.created_at > relay_hints_ranked.created_at
			OR (excluded.created_at = relay_hints_ranked.created_at AND excluded.relay_event_id > relay_hints_ranked.relay_event_id)`,
		event.PubKey, event.ID, string(rawWrite), string(rawRead), string(rawAll), event.CreatedAt, now)
	return err
}

// noteRootParent returns (root_id, parent_id) for note_links. Kind-1 uses
// thread.RootID / thread.ParentID so projections match the thread UI and NIP-10.
func noteRootParent(event nostrx.Event) (string, string) {
	switch event.Kind {
	case nostrx.KindTextNote:
		rootID := thread.RootID(event)
		parentID := thread.ParentID(rootID, event)
		if rootID == "" && parentID == "" {
			return "", ""
		}
		if rootID == "" {
			rootID = event.ID
		}
		if parentID == "" {
			parentID = rootID
		}
		return rootID, parentID
	case nostrx.KindComment:
		var firstE, rootE, replyE string
		for _, tag := range event.Tags {
			if len(tag) < 2 || tag[0] != "e" {
				continue
			}
			ref := thread.NormalizeHexEventID(tag[1])
			if ref == "" {
				continue
			}
			if firstE == "" {
				firstE = ref
			}
			if len(tag) >= 4 {
				switch strings.ToLower(strings.TrimSpace(tag[3])) {
				case "root":
					rootE = ref
				case "reply":
					replyE = ref
				}
			}
		}
		if rootE == "" {
			rootE = firstE
		}
		if replyE == "" {
			replyE = firstE
		}
		return rootE, replyE
	default:
		return "", ""
	}
}

func noteReferenceIDs(event nostrx.Event) []string {
	seen := make(map[string]bool, len(event.Tags))
	var refs []string
	rootID, parentID := noteRootParent(event)
	if rootID != "" {
		seen[rootID] = true
		refs = append(refs, rootID)
	}
	if parentID != "" && !seen[parentID] {
		seen[parentID] = true
		refs = append(refs, parentID)
	}
	for _, tag := range event.Tags {
		if len(tag) < 2 || tag[0] != "e" || tag[1] == "" || seen[tag[1]] {
			continue
		}
		seen[tag[1]] = true
		refs = append(refs, tag[1])
	}
	return refs
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}

func ternaryInt64(condition bool, whenTrue int64, whenFalse int64) int64 {
	if condition {
		return whenTrue
	}
	return whenFalse
}

func ternaryInt(condition bool, whenTrue int, whenFalse int) int {
	if condition {
		return whenTrue
	}
	return whenFalse
}

func sanitizeFollowPageBounds(limit int, offset int) (int, int) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func isThreadingNoteKind(kind int) bool {
	return kind == nostrx.KindTextNote || kind == nostrx.KindComment
}

func followSearchPatterns(query string) (prefix string, contains string) {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return "", ""
	}
	return needle + "%", "%" + needle + "%"
}
