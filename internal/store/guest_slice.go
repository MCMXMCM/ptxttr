package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"ptxt-nstr/internal/nostrx"
)

const (
	GuestSliceDefaultKey       = "gigi-default"
	GuestSliceRoleCohort       = "cohort"
	GuestSliceRoleTrust        = "trust"
	GuestSliceStatusBuilding   = "building"
	GuestSliceStatusReady      = "ready"
	GuestSlicePinTTL           = 2 * time.Hour
	guestSliceFirstReplyPage   = 25
	guestSliceMaxStateJSONSize = 8 << 20
)

var ErrGuestSliceNotReady = errors.New("guest slice snapshot is not ready")

// GuestSliceState is the durable publication boundary for an anonymous feed.
// Only Status=ready generations may be served as v2 state.
type GuestSliceState struct {
	Key          string           `json:"key"`
	Generation   int64            `json:"generation"`
	Status       string           `json:"status"`
	SeedPubKey   string           `json:"seed_pubkey"`
	Cohort       []string         `json:"cohort"`
	Trust        []string         `json:"trust"`
	Cursors      map[string]int64 `json:"cursors,omitempty"`
	ComputedAt   int64            `json:"computed_at"`
	TopCreatedAt int64            `json:"top_created_at"`
	TopID        string           `json:"top_id"`
	LastError    string           `json:"last_error,omitempty"`
}

type GuestSliceMember struct {
	PubKey            string `json:"pubkey"`
	Role              string `json:"role"`
	LatestActivityAt  int64  `json:"latest_activity_at"`
	MetadataCheckedAt int64  `json:"metadata_checked_at"`
	MetadataFound     bool   `json:"metadata_found"`
}

type GuestSliceReadiness struct {
	Ready               bool     `json:"ready"`
	Dependencies        []string `json:"dependencies,omitempty"`
	MissingEvents       []string `json:"missing_events,omitempty"`
	MissingRoots        []string `json:"missing_roots,omitempty"`
	MissingParents      []string `json:"missing_parents,omitempty"`
	MissingReplyPages   []string `json:"missing_reply_pages,omitempty"`
	MissingParticipants []string `json:"missing_participants,omitempty"`
}

type NIP05VerificationRecord struct {
	Identifier  string
	PubKey      string
	Status      string
	CheckedAt   int64
	NextRetryAt int64
}

// ActivityRankedDirectFollows selects only direct follows and ranks them by
// locally observed note activity. Lexicographic pubkeys are used solely as a
// deterministic tie-breaker.
func (s *Store) ActivityRankedDirectFollows(ctx context.Context, owner string, since int64, limit int) ([]GuestSliceMember, error) {
	owner = strings.TrimSpace(owner)
	if s == nil || s.db == nil || owner == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 5000 {
		limit = 600
	}
	rows, err := s.db.QueryContext(ctx, `SELECT f.target_pubkey,
			COALESCE(MAX(CASE WHEN e.kind IN (?, ?, ?, ?) THEN e.created_at ELSE 0 END), 0),
			COALESCE(p.last_metadata_fetch_at, 0),
			CASE WHEN p.pubkey IS NULL THEN 0 ELSE 1 END
		FROM follow_edges f
		LEFT JOIN events e ON e.pubkey = f.target_pubkey AND e.created_at >= ?
		LEFT JOIN profiles_cache p ON p.pubkey = f.target_pubkey
		WHERE f.owner_pubkey = ?
		GROUP BY f.target_pubkey
		HAVING COALESCE(MAX(CASE WHEN e.kind IN (?, ?, ?, ?) THEN e.created_at ELSE 0 END), 0) >= ?
		ORDER BY 2 DESC, f.target_pubkey ASC
		LIMIT ?`,
		nostrx.KindTextNote, nostrx.KindComment, nostrx.KindRepost, nostrx.KindLongForm,
		since, owner,
		nostrx.KindTextNote, nostrx.KindComment, nostrx.KindRepost, nostrx.KindLongForm,
		since, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]GuestSliceMember, 0, limit)
	for rows.Next() {
		var member GuestSliceMember
		var found int
		if err := rows.Scan(&member.PubKey, &member.LatestActivityAt, &member.MetadataCheckedAt, &found); err != nil {
			return nil, err
		}
		member.Role = GuestSliceRoleCohort
		member.MetadataFound = found != 0
		out = append(out, member)
	}
	return out, rows.Err()
}

func (s *Store) DirectFollowMembers(ctx context.Context, owner string, limit int) ([]GuestSliceMember, error) {
	owner = strings.TrimSpace(owner)
	if s == nil || s.db == nil || owner == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	rows, err := s.db.QueryContext(ctx, `SELECT f.target_pubkey,
			COALESCE(MAX(e.created_at), 0),
			MAX(COALESCE(p.last_metadata_fetch_at, 0), COALESCE(h.last_success_at, 0)),
			CASE WHEN p.pubkey IS NULL THEN 0 ELSE 1 END
		FROM follow_edges f
		LEFT JOIN events e ON e.pubkey = f.target_pubkey AND e.kind IN (?, ?, ?, ?)
		LEFT JOIN profiles_cache p ON p.pubkey = f.target_pubkey
		LEFT JOIN hydration_state h ON h.entity_type = 'guestProfile' AND h.entity_id = f.target_pubkey
		WHERE f.owner_pubkey = ?
		GROUP BY f.target_pubkey
		ORDER BY f.target_pubkey ASC LIMIT ?`, nostrx.KindTextNote, nostrx.KindComment,
		nostrx.KindRepost, nostrx.KindLongForm, owner, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]GuestSliceMember, 0, limit)
	for rows.Next() {
		var member GuestSliceMember
		var found int
		if err := rows.Scan(&member.PubKey, &member.LatestActivityAt, &member.MetadataCheckedAt, &found); err != nil {
			return nil, err
		}
		member.Role = GuestSliceRoleCohort
		member.MetadataFound = found != 0
		out = append(out, member)
	}
	return out, rows.Err()
}

// MarkGuestMetadataChecked records both positive and negative metadata probes.
// Keeping this outside the published generation lets an incomplete first build
// resume through the entire direct-follow set instead of retrying one prefix.
func (s *Store) MarkGuestMetadataChecked(ctx context.Context, pubkeys []string, checkedAt int64, retryAfter time.Duration) error {
	if s == nil || s.db == nil || len(pubkeys) == 0 {
		return nil
	}
	if checkedAt <= 0 {
		checkedAt = time.Now().Unix()
	}
	if retryAfter <= 0 {
		retryAfter = 24 * time.Hour
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
	) VALUES('guestProfile', ?, 'ok', ?, ?, ?, 0, 0)
	ON CONFLICT(entity_type, entity_id) DO UPDATE SET
		status = 'ok', last_attempt_at = excluded.last_attempt_at,
		last_success_at = excluded.last_success_at, next_retry_at = excluded.next_retry_at,
		fail_count = 0`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	nextRetry := time.Unix(checkedAt, 0).Add(retryAfter).Unix()
	seen := make(map[string]struct{}, len(pubkeys))
	for _, pubkey := range pubkeys {
		pubkey = strings.TrimSpace(pubkey)
		if pubkey == "" {
			continue
		}
		if _, ok := seen[pubkey]; ok {
			continue
		}
		seen[pubkey] = struct{}{}
		if _, err := stmt.ExecContext(ctx, pubkey, checkedAt, checkedAt, nextRetry); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GuestSliceProgress(ctx context.Context, key string) (map[string]int64, error) {
	if s == nil || s.db == nil {
		return map[string]int64{}, nil
	}
	if key == "" {
		key = GuestSliceDefaultKey
	}
	rows, err := s.db.QueryContext(ctx, `SELECT cursor_key, value FROM guest_slice_progress WHERE slice_key = ?`, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]int64)
	for rows.Next() {
		var cursor string
		var value int64
		if err := rows.Scan(&cursor, &value); err != nil {
			return nil, err
		}
		out[cursor] = value
	}
	return out, rows.Err()
}

func (s *Store) SetGuestSliceProgress(ctx context.Context, key string, values map[string]int64) error {
	if s == nil || s.db == nil || len(values) == 0 {
		return nil
	}
	if key == "" {
		key = GuestSliceDefaultKey
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO guest_slice_progress(slice_key, cursor_key, value, updated_at)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(slice_key, cursor_key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	now := time.Now().Unix()
	for cursor, value := range values {
		if strings.TrimSpace(cursor) == "" {
			continue
		}
		if _, err := stmt.ExecContext(ctx, key, cursor, value, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GuestSliceBuildTrust(ctx context.Context, key string) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if key == "" {
		key = GuestSliceDefaultKey
	}
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT trust_json FROM guest_slice_build_state WHERE slice_key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > guestSliceMaxStateJSONSize {
		return nil, fmt.Errorf("guest slice build trust JSON exceeds safety limit")
	}
	var trust []string
	if err := json.Unmarshal([]byte(raw), &trust); err != nil {
		return nil, err
	}
	return trust, nil
}

func (s *Store) SetGuestSliceBuildTrust(ctx context.Context, key string, trust []string) error {
	if s == nil || s.db == nil {
		return nil
	}
	if key == "" {
		key = GuestSliceDefaultKey
	}
	raw, err := json.Marshal(trust)
	if err != nil {
		return err
	}
	if len(raw) > guestSliceMaxStateJSONSize {
		return fmt.Errorf("guest slice build trust JSON exceeds safety limit")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.db.ExecContext(ctx, `INSERT INTO guest_slice_build_state(slice_key, trust_json, updated_at)
		VALUES(?, ?, ?)
		ON CONFLICT(slice_key) DO UPDATE SET trust_json = excluded.trust_json, updated_at = excluded.updated_at`,
		key, string(raw), time.Now().Unix())
	return err
}

func (s *Store) GetGuestSliceState(ctx context.Context, key string) (GuestSliceState, bool, error) {
	if s == nil || s.db == nil {
		return GuestSliceState{}, false, nil
	}
	if key == "" {
		key = GuestSliceDefaultKey
	}
	var state GuestSliceState
	var cohortJSON, trustJSON, cursorJSON string
	err := s.db.QueryRowContext(ctx, `SELECT slice_key, generation, status, seed_pubkey,
		cohort_json, trust_json, cursor_json, computed_at, top_created_at, top_id, last_error
		FROM guest_slice_state WHERE slice_key = ?`, key).Scan(
		&state.Key, &state.Generation, &state.Status, &state.SeedPubKey,
		&cohortJSON, &trustJSON, &cursorJSON, &state.ComputedAt, &state.TopCreatedAt, &state.TopID, &state.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		return GuestSliceState{}, false, nil
	}
	if err != nil {
		return GuestSliceState{}, false, err
	}
	if len(cohortJSON) > guestSliceMaxStateJSONSize || len(trustJSON) > guestSliceMaxStateJSONSize || len(cursorJSON) > guestSliceMaxStateJSONSize {
		return GuestSliceState{}, false, fmt.Errorf("guest slice state JSON exceeds safety limit")
	}
	if err := json.Unmarshal([]byte(cohortJSON), &state.Cohort); err != nil {
		return GuestSliceState{}, false, err
	}
	if err := json.Unmarshal([]byte(trustJSON), &state.Trust); err != nil {
		return GuestSliceState{}, false, err
	}
	if err := json.Unmarshal([]byte(cursorJSON), &state.Cursors); err != nil {
		return GuestSliceState{}, false, err
	}
	return state, true, nil
}

func (s *Store) GuestSliceMembers(ctx context.Context, generation int64, role string) ([]GuestSliceMember, error) {
	if s == nil || s.db == nil || generation <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT pubkey, role, latest_activity_at, metadata_checked_at, metadata_found
		FROM guest_slice_members WHERE generation = ? AND role = ?
		ORDER BY latest_activity_at DESC, pubkey ASC`, generation, role)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []GuestSliceMember
	for rows.Next() {
		var member GuestSliceMember
		var found int
		if err := rows.Scan(&member.PubKey, &member.Role, &member.LatestActivityAt, &member.MetadataCheckedAt, &found); err != nil {
			return nil, err
		}
		member.MetadataFound = found != 0
		out = append(out, member)
	}
	return out, rows.Err()
}

func (s *Store) ValidateGuestSliceSnapshot(ctx context.Context, snap *DefaultSeedGuestFeedSnapshot, generation int64) (GuestSliceReadiness, error) {
	if s == nil || s.db == nil {
		return GuestSliceReadiness{}, ErrGuestSliceNotReady
	}
	return validateGuestSliceSnapshot(ctx, s.db, snap, generation)
}

type guestSliceQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func validateGuestSliceSnapshot(ctx context.Context, q guestSliceQueryer, snap *DefaultSeedGuestFeedSnapshot, generation int64) (GuestSliceReadiness, error) {
	result := GuestSliceReadiness{}
	if snap == nil || len(snap.Feed) == 0 || generation <= 0 {
		return result, ErrGuestSliceNotReady
	}
	dependencies := make(map[string]struct{}, len(snap.Feed)*4)
	participants := make(map[string]struct{}, len(snap.Feed)*2)
	candidateEvents := append([]nostrx.Event(nil), snap.Feed...)
	seenCandidateIDs := make(map[string]struct{}, len(snap.Feed)+len(snap.ReferencedEvents))
	for _, event := range snap.Feed {
		if id := nostrx.CanonicalHex64(event.ID); id != "" {
			seenCandidateIDs[id] = struct{}{}
		}
	}
	for key, event := range snap.ReferencedEvents {
		if event.ID == "" {
			event.ID = key
		}
		id := nostrx.CanonicalHex64(event.ID)
		if id != "" {
			if _, seen := seenCandidateIDs[id]; seen {
				continue
			}
			seenCandidateIDs[id] = struct{}{}
		}
		candidateEvents = append(candidateEvents, event)
	}
	for _, event := range candidateEvents {
		id := nostrx.CanonicalHex64(event.ID)
		if id == "" {
			result.MissingEvents = append(result.MissingEvents, event.ID)
			continue
		}
		var pubkey string
		if err := q.QueryRowContext(ctx, `SELECT pubkey FROM events WHERE id = ?`, id).Scan(&pubkey); err != nil {
			result.MissingEvents = append(result.MissingEvents, id)
			continue
		}
		dependencies[id] = struct{}{}
		participants[pubkey] = struct{}{}
		rootID, parentID := id, id
		var linkAuthor string
		err := q.QueryRowContext(ctx, `SELECT author_pubkey, root_id, parent_id FROM note_links WHERE note_id = ?`, id).
			Scan(&linkAuthor, &rootID, &parentID)
		if err != nil && (event.Kind == nostrx.KindTextNote || event.Kind == nostrx.KindComment) {
			result.MissingRoots = append(result.MissingRoots, id)
			continue
		}
		if linkAuthor != "" {
			participants[linkAuthor] = struct{}{}
		}
		addDependency := func(dependencyID string, missing *[]string) bool {
			dependencyID = nostrx.CanonicalHex64(dependencyID)
			if dependencyID == "" {
				return false
			}
			var author string
			if err := q.QueryRowContext(ctx, `SELECT pubkey FROM events WHERE id = ?`, dependencyID).Scan(&author); err != nil {
				*missing = append(*missing, dependencyID)
				return false
			}
			dependencies[dependencyID] = struct{}{}
			participants[author] = struct{}{}
			return true
		}
		rootID = nostrx.CanonicalHex64(rootID)
		parentID = nostrx.CanonicalHex64(parentID)
		_ = addDependency(rootID, &result.MissingRoots)
		ancestorID := parentID
		seenAncestors := map[string]struct{}{id: {}}
		for depth := 0; ancestorID != "" && ancestorID != id && depth < 64; depth++ {
			if _, seen := seenAncestors[ancestorID]; seen {
				result.MissingParents = append(result.MissingParents, ancestorID)
				break
			}
			seenAncestors[ancestorID] = struct{}{}
			if !addDependency(ancestorID, &result.MissingParents) || ancestorID == rootID {
				break
			}
			var ancestorRootID, ancestorParentID string
			if err := q.QueryRowContext(ctx, `SELECT root_id, parent_id FROM note_links WHERE note_id = ?`, ancestorID).
				Scan(&ancestorRootID, &ancestorParentID); err != nil {
				result.MissingParents = append(result.MissingParents, ancestorID)
				break
			}
			ancestorRootID = nostrx.CanonicalHex64(ancestorRootID)
			if ancestorRootID != "" && rootID != "" && ancestorRootID != rootID {
				result.MissingRoots = append(result.MissingRoots, ancestorRootID)
			}
			ancestorID = nostrx.CanonicalHex64(ancestorParentID)
			if depth == 63 && ancestorID != "" && ancestorID != rootID {
				result.MissingParents = append(result.MissingParents, ancestorID)
			}
		}
		var graphJSON string
		graphErr := q.QueryRowContext(ctx, `SELECT event_ids_json FROM thread_graph_cache WHERE root_id = ?`, rootID).Scan(&graphJSON)
		if graphErr != nil {
			var hydrated int
			hydrationErr := q.QueryRowContext(ctx, `SELECT 1 FROM hydration_state
				WHERE entity_type = 'noteReplies' AND entity_id = ? AND status = 'ok' LIMIT 1`, rootID).Scan(&hydrated)
			if hydrationErr != nil {
				result.MissingReplyPages = append(result.MissingReplyPages, rootID)
			}
		} else {
			var replyIDs []string
			if json.Unmarshal([]byte(graphJSON), &replyIDs) != nil {
				result.MissingReplyPages = append(result.MissingReplyPages, rootID)
			} else {
				if len(replyIDs) > guestSliceFirstReplyPage {
					replyIDs = replyIDs[:guestSliceFirstReplyPage]
				}
				for _, replyID := range replyIDs {
					var author string
					if err := q.QueryRowContext(ctx, `SELECT pubkey FROM events WHERE id = ?`, replyID).Scan(&author); err != nil {
						result.MissingReplyPages = append(result.MissingReplyPages, replyID)
						continue
					}
					dependencies[replyID] = struct{}{}
					participants[author] = struct{}{}
				}
			}
		}
	}
	for pubkey := range participants {
		var ready int
		err := q.QueryRowContext(ctx, `SELECT 1 WHERE
			EXISTS (SELECT 1 FROM profiles_cache WHERE pubkey = ?)
			OR EXISTS (SELECT 1 FROM guest_slice_members
				WHERE generation = ? AND pubkey = ? AND metadata_checked_at > 0)
			OR EXISTS (SELECT 1 FROM hydration_state
				WHERE entity_type = 'profile' AND entity_id = ? AND last_attempt_at > 0 AND next_retry_at > unixepoch())
			LIMIT 1`, pubkey, generation, pubkey, pubkey).Scan(&ready)
		if err != nil {
			result.MissingParticipants = append(result.MissingParticipants, pubkey)
		}
	}
	for id := range dependencies {
		result.Dependencies = append(result.Dependencies, id)
	}
	sort.Strings(result.Dependencies)
	result.MissingEvents = uniqueSorted(result.MissingEvents)
	result.MissingRoots = uniqueSorted(result.MissingRoots)
	result.MissingParents = uniqueSorted(result.MissingParents)
	result.MissingReplyPages = uniqueSorted(result.MissingReplyPages)
	result.MissingParticipants = uniqueSorted(result.MissingParticipants)
	result.Ready = len(result.MissingEvents) == 0 && len(result.MissingRoots) == 0 &&
		len(result.MissingParents) == 0 && len(result.MissingReplyPages) == 0 && len(result.MissingParticipants) == 0
	if !result.Ready {
		return result, ErrGuestSliceNotReady
	}
	return result, nil
}

func uniqueSorted(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	sort.Strings(values)
	out := values[:0]
	for _, value := range values {
		if value == "" || (len(out) > 0 && out[len(out)-1] == value) {
			continue
		}
		out = append(out, value)
	}
	return out
}

// PublishGuestSlice atomically publishes membership, the canonical recent
// snapshot, generation metadata, and all retention pins. A failed validation
// leaves the previously ready generation untouched.
func (s *Store) PublishGuestSlice(ctx context.Context, state GuestSliceState, members []GuestSliceMember, snap *DefaultSeedGuestFeedSnapshot, pinTTL time.Duration) (GuestSliceReadiness, error) {
	return s.PublishGuestSliceSnapshots(ctx, state, members, snap, nil, pinTTL)
}

// PublishGuestSliceSnapshots extends PublishGuestSlice with the sort-specific
// first pages served by the feed snapshot path. They validate, pin, and commit
// with the canonical recent page and generation row.
func (s *Store) PublishGuestSliceSnapshots(ctx context.Context, state GuestSliceState, members []GuestSliceMember, snap *DefaultSeedGuestFeedSnapshot, snapshots map[string]*FeedSnapshotRecord, pinTTL time.Duration) (GuestSliceReadiness, error) {
	if s == nil || s.db == nil || snap == nil || len(snap.Feed) == 0 {
		return GuestSliceReadiness{}, ErrGuestSliceNotReady
	}
	if state.Key == "" {
		state.Key = GuestSliceDefaultKey
	}
	if state.Generation <= 0 {
		state.Generation = time.Now().UnixNano()
	}
	if pinTTL <= 0 {
		pinTTL = GuestSlicePinTTL
	}
	state.Status = GuestSliceStatusReady
	state.ComputedAt = time.Now().Unix()
	state.TopID = snap.Feed[0].ID
	state.TopCreatedAt = snap.Feed[0].CreatedAt
	snap.Version = defaultSeedGuestFeedSnapshotVersion
	snap.ComputedAtUnix = state.ComputedAt
	cohortJSON, err := json.Marshal(state.Cohort)
	if err != nil {
		return GuestSliceReadiness{}, err
	}
	trustJSON, err := json.Marshal(state.Trust)
	if err != nil {
		return GuestSliceReadiness{}, err
	}
	cursorJSON, err := json.Marshal(state.Cursors)
	if err != nil {
		return GuestSliceReadiness{}, err
	}
	snapshotJSON, err := json.Marshal(snap)
	if err != nil {
		return GuestSliceReadiness{}, err
	}
	if len(snapshotJSON) > maxDefaultSeedGuestFeedSnapshotJSONBytes {
		return GuestSliceReadiness{}, fmt.Errorf("guest snapshot exceeds maximum size")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GuestSliceReadiness{}, err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO guest_slice_members(
		generation, pubkey, role, latest_activity_at, metadata_checked_at, metadata_found
	) VALUES (?, ?, ?, ?, ?, ?)
	ON CONFLICT(generation, pubkey, role) DO UPDATE SET
		latest_activity_at = excluded.latest_activity_at,
		metadata_checked_at = excluded.metadata_checked_at,
		metadata_found = excluded.metadata_found`)
	if err != nil {
		return GuestSliceReadiness{}, err
	}
	for _, member := range members {
		if member.PubKey == "" || member.Role == "" {
			continue
		}
		if _, err := stmt.ExecContext(ctx, state.Generation, member.PubKey, member.Role,
			member.LatestActivityAt, member.MetadataCheckedAt, ternaryInt(member.MetadataFound, 1, 0)); err != nil {
			_ = stmt.Close()
			return GuestSliceReadiness{}, err
		}
	}
	_ = stmt.Close()
	readiness, err := validateGuestSliceSnapshot(ctx, tx, snap, state.Generation)
	if err != nil {
		return readiness, err
	}
	for key, rec := range snapshots {
		if strings.TrimSpace(key) == "" || rec == nil || len(rec.Feed) == 0 {
			return readiness, ErrGuestSliceNotReady
		}
		rec.Version = feedSnapshotJSONVersion
		rec.ComputedAtUnix = state.ComputedAt
		extra, err := validateGuestSliceSnapshot(ctx, tx, defaultSeedSnapshotFromFeedRecord(rec), state.Generation)
		readiness = mergeGuestSliceReadiness(readiness, extra)
		if err != nil {
			return readiness, err
		}
		present, err := canonicalFeedEventsPresent(ctx, tx, rec.Feed)
		if err != nil {
			return readiness, err
		}
		if !present {
			return readiness, ErrFeedSnapshotMissingCanonicalEvent
		}
		raw, err := json.Marshal(rec)
		if err != nil {
			return readiness, err
		}
		if len(raw) > maxDefaultSeedGuestFeedSnapshotJSONBytes {
			return readiness, fmt.Errorf("guest feed snapshot %q exceeds maximum size", key)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO feed_snapshots(snapshot_key, value, computed_at)
			VALUES(?, ?, ?)
			ON CONFLICT(snapshot_key) DO UPDATE SET value = excluded.value, computed_at = excluded.computed_at`,
			key, string(raw), state.ComputedAt); err != nil {
			return readiness, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO guest_slice_state(
		slice_key, generation, status, seed_pubkey, cohort_json, trust_json, cursor_json,
		computed_at, top_created_at, top_id, last_error
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '')
	ON CONFLICT(slice_key) DO UPDATE SET
		generation = excluded.generation, status = excluded.status, seed_pubkey = excluded.seed_pubkey,
		cohort_json = excluded.cohort_json, trust_json = excluded.trust_json, cursor_json = excluded.cursor_json,
		computed_at = excluded.computed_at, top_created_at = excluded.top_created_at,
		top_id = excluded.top_id, last_error = ''`, state.Key, state.Generation, state.Status,
		state.SeedPubKey, string(cohortJSON), string(trustJSON), string(cursorJSON), state.ComputedAt,
		state.TopCreatedAt, state.TopID); err != nil {
		return readiness, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO app_meta(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, AppMetaKeyDefaultSeedGuestFeed, string(snapshotJSON)); err != nil {
		return readiness, err
	}
	expiresAt := time.Now().Add(pinTTL).Unix()
	pinStmt, err := tx.PrepareContext(ctx, `INSERT INTO event_pins(event_id, generation, reason, expires_at)
		VALUES (?, ?, 'guest-snapshot', ?)
		ON CONFLICT(event_id, generation, reason) DO UPDATE SET expires_at = excluded.expires_at`)
	if err != nil {
		return readiness, err
	}
	for _, id := range readiness.Dependencies {
		if _, err := pinStmt.ExecContext(ctx, id, state.Generation, expiresAt); err != nil {
			_ = pinStmt.Close()
			return readiness, err
		}
	}
	_ = pinStmt.Close()
	_, _ = tx.ExecContext(ctx, `DELETE FROM event_pins WHERE expires_at <= ?`, time.Now().Unix())
	_, _ = tx.ExecContext(ctx, `DELETE FROM guest_slice_members WHERE generation < ?`, state.Generation-1)
	if err := tx.Commit(); err != nil {
		return readiness, err
	}
	return readiness, nil
}

func defaultSeedSnapshotFromFeedRecord(rec *FeedSnapshotRecord) *DefaultSeedGuestFeedSnapshot {
	if rec == nil {
		return nil
	}
	return &DefaultSeedGuestFeedSnapshot{
		Version: rec.Version, RelaysHash: rec.RelaysHash, Feed: rec.Feed,
		ReferencedEvents: rec.ReferencedEvents, ReplyCounts: rec.ReplyCounts,
		ReactionTotals: rec.ReactionTotals, ReactionViewers: rec.ReactionViewers,
		Profiles: rec.Profiles, Cursor: rec.Cursor, CursorID: rec.CursorID,
		HasMore: rec.HasMore, ComputedAtUnix: rec.ComputedAtUnix,
	}
}

func mergeGuestSliceReadiness(a, b GuestSliceReadiness) GuestSliceReadiness {
	a.Dependencies = uniqueSorted(append(a.Dependencies, b.Dependencies...))
	a.MissingEvents = uniqueSorted(append(a.MissingEvents, b.MissingEvents...))
	a.MissingRoots = uniqueSorted(append(a.MissingRoots, b.MissingRoots...))
	a.MissingParents = uniqueSorted(append(a.MissingParents, b.MissingParents...))
	a.MissingReplyPages = uniqueSorted(append(a.MissingReplyPages, b.MissingReplyPages...))
	a.MissingParticipants = uniqueSorted(append(a.MissingParticipants, b.MissingParticipants...))
	a.Ready = len(a.MissingEvents) == 0 && len(a.MissingRoots) == 0 && len(a.MissingParents) == 0 &&
		len(a.MissingReplyPages) == 0 && len(a.MissingParticipants) == 0
	return a
}

func (s *Store) PutNIP05Verification(ctx context.Context, record NIP05VerificationRecord) error {
	if s == nil || s.db == nil || record.Identifier == "" || record.PubKey == "" || record.Status == "" {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `INSERT INTO nip05_verifications(identifier, pubkey, status, checked_at, next_retry_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(identifier, pubkey) DO UPDATE SET status = excluded.status,
		checked_at = excluded.checked_at, next_retry_at = excluded.next_retry_at`,
		strings.ToLower(strings.TrimSpace(record.Identifier)), record.PubKey, record.Status, record.CheckedAt, record.NextRetryAt)
	return err
}

func (s *Store) GetNIP05Verification(ctx context.Context, identifier, pubkey string) (NIP05VerificationRecord, bool, error) {
	if s == nil || s.db == nil || identifier == "" || pubkey == "" {
		return NIP05VerificationRecord{}, false, nil
	}
	var record NIP05VerificationRecord
	err := s.db.QueryRowContext(ctx, `SELECT identifier, pubkey, status, checked_at, next_retry_at
		FROM nip05_verifications WHERE identifier = ? AND pubkey = ?`,
		strings.ToLower(strings.TrimSpace(identifier)), pubkey).Scan(
		&record.Identifier, &record.PubKey, &record.Status, &record.CheckedAt, &record.NextRetryAt)
	if errors.Is(err, sql.ErrNoRows) {
		return NIP05VerificationRecord{}, false, nil
	}
	return record, err == nil, err
}

func (s *Store) DeleteNonDirectSeedContacts(ctx context.Context, seed string, pinned []string) (int64, error) {
	if s == nil || s.db == nil || seed == "" {
		return 0, nil
	}
	args := []any{EntityTypeSeedContact, seed}
	extra := ""
	if clean := uniqueNonEmpty(pinned); len(clean) > 0 {
		extra = ` AND entity_id NOT IN (` + placeholders(len(clean)) + `)`
		for _, value := range clean {
			args = append(args, value)
		}
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	result, err := s.db.ExecContext(ctx, `DELETE FROM hydration_state
		WHERE entity_type = ? AND entity_id NOT IN (
			SELECT target_pubkey FROM follow_edges WHERE owner_pubkey = ?
		)`+extra, args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) FollowCounts(ctx context.Context, pubkey string) (following, followers int, err error) {
	if s == nil || s.db == nil || pubkey == "" {
		return 0, 0, nil
	}
	err = s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM follow_edges WHERE owner_pubkey = ?),
		(SELECT COUNT(*) FROM follow_edges WHERE target_pubkey = ?)`, pubkey, pubkey).Scan(&following, &followers)
	return
}

func (s *Store) WALSizeBytes() int64 {
	if s == nil || s.dbPath == "" || s.dbPath == ":memory:" {
		return 0
	}
	info, err := os.Stat(s.dbPath + "-wal")
	if err != nil {
		return 0
	}
	return info.Size()
}
