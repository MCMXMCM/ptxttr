package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"ptxt-nstr/internal/nostrx"
)

// ReactionStat holds deduped reaction totals for a note (latest vote per reactor).
type ReactionStat struct {
	Up    int
	Down  int
	Total int
}

const reactionAggBootstrapKey = "reaction_agg_bootstrap_v1"

func (s *Store) maybeBootstrapNoteReactions(ctx context.Context) error {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_meta WHERE key = ?`, reactionAggBootstrapKey).Scan(&v)
	if err == nil && v != "" {
		return nil
	}
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err := s.RebuildNoteReactionAggregates(ctx); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO app_meta(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, reactionAggBootstrapKey, "1")
	return err
}

// RebuildNoteReactionAggregates repopulates note_reaction_latest from cached kind-7 events.
func (s *Store) RebuildNoteReactionAggregates(ctx context.Context) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM note_reaction_latest`); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT raw_json FROM events WHERE kind = ? ORDER BY created_at ASC, id ASC`, nostrx.KindReaction)
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
		if err := upsertNoteReactionLatestTx(ctx, tx, *ev); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

func upsertNoteReactionLatestTx(ctx context.Context, tx *sql.Tx, event nostrx.Event) error {
	if event.Kind != nostrx.KindReaction {
		return nil
	}
	pol := nostrx.ReactionPolarity(event.Content)
	if pol == 0 {
		return nil
	}
	noteID := nostrx.CanonicalHex64(nostrx.ReactionLastETagID(event))
	if noteID == "" {
		return nil
	}
	var targetKind int
	err := tx.QueryRowContext(ctx, `SELECT kind FROM events WHERE id = ?`, noteID).Scan(&targetKind)
	if err != nil {
		return nil
	}
	if targetKind != nostrx.KindTextNote && targetKind != nostrx.KindComment {
		return nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO note_reaction_latest(note_id, reactor_pubkey, polarity, reaction_created_at, reaction_event_id)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(note_id, reactor_pubkey) DO UPDATE SET
			polarity = excluded.polarity,
			reaction_created_at = excluded.reaction_created_at,
			reaction_event_id = excluded.reaction_event_id
		WHERE excluded.reaction_created_at > note_reaction_latest.reaction_created_at
			OR (excluded.reaction_created_at = note_reaction_latest.reaction_created_at AND excluded.reaction_event_id > note_reaction_latest.reaction_event_id)`,
		noteID, event.PubKey, pol, event.CreatedAt, event.ID)
	return err
}

func backfillNoteReactionLatestForTargetTx(ctx context.Context, tx *sql.Tx, noteID string) error {
	noteID = nostrx.CanonicalHex64(strings.TrimSpace(noteID))
	if noteID == "" {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT e.raw_json
		FROM events e
		WHERE e.kind = ?
			AND e.id IN (
				SELECT t.event_id
				FROM tags t
				WHERE t.name = 'e' AND t.value = ?
				GROUP BY t.event_id
			)
		ORDER BY e.created_at ASC, e.id ASC`, nostrx.KindReaction, noteID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		event, err := decodeEvent(raw)
		if err != nil || event == nil {
			continue
		}
		if err := upsertNoteReactionLatestTx(ctx, tx, *event); err != nil {
			return err
		}
	}
	return rows.Err()
}

// canonicalDistinctNoteIDs dedupes note ids as lowercase 64-hex for reaction SQL and map keys.
func canonicalDistinctNoteIDs(noteIDs []string) []string {
	seen := make(map[string]struct{}, len(noteIDs))
	out := make([]string, 0, len(noteIDs))
	for _, id := range noteIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		c := nostrx.CanonicalHex64(id)
		if c == "" {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// ReactionStatsByNoteIDs returns Up, Down, Total (=Up+Down), and optional viewer polarity (+ / - / "").
func (s *Store) ReactionStatsByNoteIDs(ctx context.Context, noteIDs []string, viewerPubkey string) (map[string]ReactionStat, map[string]string, error) {
	out := make(map[string]ReactionStat)
	viewer := make(map[string]string)
	keys := canonicalDistinctNoteIDs(noteIDs)
	if len(keys) == 0 {
		return out, viewer, nil
	}
	viewerPubkey = strings.TrimSpace(viewerPubkey)
	if pk, err := nostrx.NormalizePubKey(viewerPubkey); err == nil {
		viewerPubkey = pk
	}
	q := fmt.Sprintf(`SELECT n.note_id,
			SUM(CASE WHEN n.polarity = 1 THEN 1 ELSE 0 END) AS up_cnt,
			SUM(CASE WHEN n.polarity = -1 THEN 1 ELSE 0 END) AS down_cnt
		FROM note_reaction_latest n
		WHERE n.note_id IN (%s)
		GROUP BY n.note_id`, placeholders(len(keys)))
	args := make([]any, 0, len(keys))
	for _, id := range keys {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var noteID string
		var up, down int
		if err := rows.Scan(&noteID, &up, &down); err != nil {
			return nil, nil, err
		}
		noteID = nostrx.CanonicalHex64(noteID)
		out[noteID] = ReactionStat{Up: up, Down: down, Total: up + down}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if viewerPubkey != "" {
		vq := fmt.Sprintf(`SELECT note_id, polarity FROM note_reaction_latest
			WHERE reactor_pubkey = ? AND note_id IN (%s)`, placeholders(len(keys)))
		vargs := make([]any, 0, 1+len(keys))
		vargs = append(vargs, viewerPubkey)
		for _, id := range keys {
			vargs = append(vargs, id)
		}
		vrows, err := s.db.QueryContext(ctx, vq, vargs...)
		if err != nil {
			return nil, nil, err
		}
		defer func() { _ = vrows.Close() }()
		for vrows.Next() {
			var nid string
			var pol int
			if err := vrows.Scan(&nid, &pol); err != nil {
				return nil, nil, err
			}
			nid = nostrx.CanonicalHex64(nid)
			switch pol {
			case 1:
				viewer[nid] = "+"
			case -1:
				viewer[nid] = "-"
			default:
				viewer[nid] = ""
			}
		}
		if err := vrows.Err(); err != nil {
			return nil, nil, err
		}
	}
	return out, viewer, nil
}

// MaxReactionReactorsList caps ReactionReactorsByNoteID fetches (API list modal).
const MaxReactionReactorsList = 500

// ReactionReactorRow is one reactor's latest vote on a single note.
type ReactionReactorRow struct {
	ReactorPubkey string
	Polarity      int // 1 up, -1 down
}

// ReactionReactorsByNoteID returns latest reactor rows for noteID (canonical hex), ordered
// upvotes first then pubkey, capped at limit (default MaxReactionReactorsList). truncated
// is true when more than cap rows exist in the aggregate table.
func (s *Store) ReactionReactorsByNoteID(ctx context.Context, noteID string, limit int) ([]ReactionReactorRow, bool, error) {
	noteID = nostrx.CanonicalHex64(strings.TrimSpace(noteID))
	if noteID == "" {
		return nil, false, nil
	}
	if limit <= 0 || limit > MaxReactionReactorsList {
		limit = MaxReactionReactorsList
	}
	fetch := limit + 1
	rows, err := s.db.QueryContext(ctx, `SELECT reactor_pubkey, polarity FROM note_reaction_latest
		WHERE note_id = ?
		ORDER BY polarity DESC, reactor_pubkey ASC
		LIMIT ?`, noteID, fetch)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	var out []ReactionReactorRow
	for rows.Next() {
		var pk string
		var pol int
		if err := rows.Scan(&pk, &pol); err != nil {
			return nil, false, err
		}
		out = append(out, ReactionReactorRow{
			ReactorPubkey: nostrx.CanonicalHex64(pk),
			Polarity:      pol,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	truncated := len(out) > limit
	if truncated {
		out = out[:limit]
	}
	return out, truncated, nil
}

// ReactionRollupRow is one target note with deduped reactor count for notification rollups.
type ReactionRollupRow struct {
	NoteID       string
	ReactorCount int
	LastAt       int64
	LastEventID  string
}

// ReactionRollupsForNoteAuthor returns reaction activity on notes authored by authorPubkey.
func (s *Store) ReactionRollupsForNoteAuthor(ctx context.Context, authorPubkey string, before int64, beforeID string, limit int) ([]ReactionRollupRow, error) {
	authorPubkey = strings.TrimSpace(authorPubkey)
	if authorPubkey == "" || limit <= 0 {
		return nil, nil
	}
	if limit > 100 {
		limit = 100
	}
	if before <= 0 {
		before = 1 << 62
	}
	if beforeID == "" {
		beforeID = "~"
	}
	q := `SELECT roll.note_id, roll.cnt, roll.last_at, roll.last_eid FROM (
			SELECT nrl.note_id AS note_id,
				COUNT(*) AS cnt,
				MAX(nrl.reaction_created_at) AS last_at,
				MAX(nrl.reaction_event_id) AS last_eid
			FROM note_reaction_latest nrl
			INNER JOIN events target ON target.id = nrl.note_id
			WHERE target.pubkey = ? AND target.kind IN (?, ?) AND nrl.reactor_pubkey <> ?
			GROUP BY nrl.note_id
		) AS roll
		WHERE roll.last_at < ? OR (roll.last_at = ? AND roll.note_id < ?)
		ORDER BY roll.last_at DESC, roll.note_id DESC
		LIMIT ?`
	rows, err := s.db.QueryContext(ctx, q, authorPubkey, nostrx.KindTextNote, nostrx.KindComment, authorPubkey, before, before, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ReactionRollupRow
	for rows.Next() {
		var row ReactionRollupRow
		if err := rows.Scan(&row.NoteID, &row.ReactorCount, &row.LastAt, &row.LastEventID); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
