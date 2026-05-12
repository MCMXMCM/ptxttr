package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"

	"ptxt-nstr/internal/nostrx"
)

const (
	defaultSQLiteMaxOpenConns = 10
	defaultSQLiteMaxIdleConns = 4

	// maxAuthorsRecentSummariesIN bounds pubkey IN (...) placeholders per query.
	// SQLite SQLITE_MAX_VARIABLE_NUMBER is often 999; each query also binds kinds
	// plus cursor arguments, so keep a margin below that ceiling.
	maxAuthorsRecentSummariesIN = 300
)

func defaultSQLitePool(runtimeCPUs int) (open, idle int) {
	if runtimeCPUs <= 2 {
		return 4, 2
	}
	return max(defaultSQLiteMaxOpenConns, runtimeCPUs), max(defaultSQLiteMaxIdleConns, runtimeCPUs/2)
}

// RelayHintSnapshot is a single pubkey -> relay hint set update pushed into an
// optional KV sidecar. SQLite remains authoritative and can always rebuild it.
type RelayHintSnapshot struct {
	PubKey string
	Set    RelayHintSet
}

// ProfileSummary carries the denormalized metadata needed on read paths that
// should not have to parse the original kind-0 event body every time.
type ProfileSummary struct {
	PubKey      string
	DisplayName string
	Name        string
	About       string
	Picture     string
	NIP05       string
}

type ReplyStat struct {
	DirectReplies     int
	DescendantReplies int
	LastReplyEventAt  int64
}

type Store struct {
	db      *sql.DB
	writeMu sync.Mutex

	bgCtx    context.Context
	bgCancel context.CancelFunc
	bgWG     sync.WaitGroup

	retentionMax int
	pruneEvery   int64
	writeCount   atomic.Int64
	pruning      atomic.Bool

	sidecar *sidecarCaches

	// replaceableHistory keeps all replaceable revisions in events (default).
	// When false, older rows for the same (pubkey, kind) slot are deleted after
	// a successful insert for kinds 0, 3, and 10002.
	replaceableHistory bool

	dirtyRepliesMu sync.Mutex
	dirtyReplies   map[string]struct{}
}

type CacheStats struct {
	Events      int64 `json:"events"`
	Tags        int64 `json:"tags"`
	RelayEvents int64 `json:"relay_events"`
	Relays      int64 `json:"relays"`
}

type RelayStatus struct {
	URL         string
	OK          bool
	LastError   string
	LastChecked int64
}

type TrendingItem struct {
	NoteID     string
	ReplyCount int
}

func Open(ctx context.Context, path string) (*Store, error) {
	connHookOnce.Do(registerConnectionHook)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	defaultOpenConns, defaultIdleConns := defaultSQLitePool(runtime.NumCPU())
	maxOpenConns := positiveIntEnv("PTXT_SQLITE_MAX_OPEN_CONNS", defaultOpenConns)
	maxIdleConns := positiveIntEnv("PTXT_SQLITE_MAX_IDLE_CONNS", defaultIdleConns)
	if maxIdleConns > maxOpenConns {
		maxIdleConns = maxOpenConns
	}
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxIdleConns)
	bgCtx, bgCancel := context.WithCancel(context.Background())
	sidecarSize := positiveIntEnv("PTXT_SIDECAR_LRU_SIZE", 2048)
	store := &Store{
		db:                 db,
		bgCtx:              bgCtx,
		bgCancel:           bgCancel,
		pruneEvery:         1000,
		dirtyReplies:       make(map[string]struct{}),
		replaceableHistory: true,
		sidecar:            newSidecarCaches(sidecarSize, nil),
	}
	if err := store.migrate(ctx); err != nil {
		bgCancel()
		_ = db.Close()
		return nil, err
	}
	store.startBackgroundWorkers()
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	if s.bgCancel != nil {
		s.bgCancel()
	}
	s.bgWG.Wait()
	return s.db.Close()
}

// Ping checks that the SQLite handle responds (cheap liveness for /healthz).
func (s *Store) Ping(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("store not open")
	}
	return s.db.PingContext(ctx)
}

func (s *Store) ShouldRefresh(ctx context.Context, scope, key string, ttl time.Duration) bool {
	var last int64
	err := s.db.QueryRowContext(ctx, `SELECT last_fetch FROM fetch_log WHERE scope = ? AND cache_key = ?`, scope, key).Scan(&last)
	if err == nil && time.Since(time.Unix(last, 0)) < ttl {
		return false
	}
	return true
}

func (s *Store) SetEventRetention(maxEvents int) {
	if s == nil {
		return
	}
	if maxEvents < 0 {
		maxEvents = 0
	}
	s.retentionMax = maxEvents
}

// SetReplaceableHistory controls whether multiple replaceable revisions per
// (pubkey, kind) remain in the events table. Default is true (keep history).
func (s *Store) SetReplaceableHistory(keep bool) {
	if s == nil {
		return
	}
	s.replaceableHistory = keep
}

// SetSidecarMetricSink wires cache hit/miss counters into application metrics
// (optional). Safe to call more than once (e.g. from httpx.New).
func (s *Store) SetSidecarMetricSink(fn sidecarMetricSink) {
	if s == nil || s.sidecar == nil {
		return
	}
	s.sidecar.SetSink(fn)
}

// PutProfileSidecarForTesting seeds the in-memory profile LRU (httpx avatar tests).
func (s *Store) PutProfileSidecarForTesting(pub string, summary ProfileSummary) {
	if s == nil || s.sidecar == nil || pub == "" {
		return
	}
	summary.PubKey = pub
	s.sidecar.putProfileMulti(map[string]ProfileSummary{pub: summary})
}

func (s *Store) startBackgroundWorkers() {
	if s == nil || s.bgCtx == nil {
		return
	}
	s.runTicker(15*time.Second, s.recomputeDirtyReplyStats)
	s.runTicker(10*time.Minute, func() {
		ctx, cancel := context.WithTimeout(s.bgCtx, 5*time.Second)
		defer cancel()
		if err := s.CheckpointWAL(ctx); err != nil {
			slog.Debug("wal checkpoint failed", "err", err)
		}
	})
	// Reclaim freed pages back to the filesystem on a slow cadence. Plain
	// DELETE only releases pages to the SQLite freelist, so without this the
	// .sqlite file grows unboundedly even when retention pruning is keeping
	// the row count bounded. incremental_vacuum is cheap when there is
	// nothing to reclaim and only does work after auto_vacuum=INCREMENTAL was
	// set before the first VACUUM on the DB.
	s.runTicker(30*time.Minute, func() {
		ctx, cancel := context.WithTimeout(s.bgCtx, 30*time.Second)
		defer cancel()
		if err := s.ReclaimFreePages(ctx); err != nil {
			slog.Debug("incremental vacuum failed", "err", err)
		}
	})
	// Backstop full VACUUM on a slow cadence. Existing production DBs that
	// were created before auto_vacuum=INCREMENTAL was set will not honor
	// incremental_vacuum, so we still need a periodic full VACUUM to actually
	// shrink the file. VACUUM rewrites the whole DB and is expensive, so we
	// run it daily and only when retention is active (otherwise there is no
	// recurring source of free pages to recover).
	s.runTicker(24*time.Hour, func() {
		if s.retentionMax <= 0 {
			return
		}
		ctx, cancel := context.WithTimeout(s.bgCtx, 20*time.Minute)
		defer cancel()
		if err := s.VacuumFull(ctx); err != nil {
			slog.Warn("periodic vacuum failed", "err", err)
		}
	})
}

// runTicker spawns a tracked background goroutine that invokes fn on every
// tick of interval, exiting when bgCtx is cancelled.
func (s *Store) runTicker(interval time.Duration, fn func()) {
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-s.bgCtx.Done():
				return
			case <-ticker.C:
				fn()
			}
		}
	}()
}

func (s *Store) CheckpointWAL(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

// ReclaimFreePages truncates the WAL and runs an incremental vacuum to
// release pages on the SQLite freelist back to the filesystem. It is safe to
// call on every cadence; it is a no-op when there is nothing to reclaim or
// when the DB was created without auto_vacuum=INCREMENTAL.
func (s *Store) ReclaimFreePages(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA incremental_vacuum`); err != nil {
		return err
	}
	return nil
}

// VacuumFull rewrites the entire database file, returning all freelist pages
// to the filesystem. It is the only way to shrink a DB whose auto_vacuum was
// not set to INCREMENTAL before its first VACUUM. VACUUM holds an exclusive
// write lock for the duration of the rewrite, so callers should treat this as
// a heavy maintenance operation.
func (s *Store) VacuumFull(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM`); err != nil {
		return err
	}
	return nil
}

func (s *Store) Compact(ctx context.Context, maxEvents int) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	deleted := int64(0)
	if maxEvents > 0 {
		result, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE id IN (
			SELECT id FROM events ORDER BY inserted_at ASC, created_at ASC, id ASC
			LIMIT (
				SELECT CASE WHEN COUNT(*) > ? THEN COUNT(*) - ? ELSE 0 END FROM events
			)
		)`, maxEvents, maxEvents)
		if err != nil {
			return 0, err
		}
		deleted, _ = result.RowsAffected()
	}
	if deleted > 0 && s.sidecar != nil {
		// Compact does not enumerate the deleted ids, and note_stats rows
		// survive (they're cleaned up by the next PruneEvents). Purge the
		// reply LRU defensively so cached counts cannot outlive the events
		// they describe even when descendants disappear here.
		s.sidecar.purgeReply()
	}
	if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return deleted, err
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM`); err != nil {
		return deleted, err
	}
	return deleted, nil
}

func (s *Store) MarkRefreshed(ctx context.Context, scope, key string) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, _ = s.db.ExecContext(ctx, `INSERT INTO fetch_log(scope, cache_key, last_fetch)
		VALUES (?, ?, ?)
		ON CONFLICT(scope, cache_key) DO UPDATE SET last_fetch = excluded.last_fetch`,
		scope, key, time.Now().Unix())
}

func (s *Store) SaveEvent(ctx context.Context, event nostrx.Event) error {
	_, err := s.SaveEvents(ctx, []nostrx.Event{event})
	return err
}

func (s *Store) SaveEvents(ctx context.Context, events []nostrx.Event) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}
	type preparedEvent struct {
		event nostrx.Event
		raw   string
	}
	prepared := make([]preparedEvent, 0, len(events))
	for _, event := range events {
		if event.ID == "" {
			return 0, errors.New("event id is required")
		}
		raw, err := json.Marshal(event)
		if err != nil {
			return 0, err
		}
		prepared = append(prepared, preparedEvent{event: event, raw: string(raw)})
	}
	var dirtyReplyRefs []string
	var dirtyRelayHintPubkeys []string
	var dirtyProfilePubkeys []string
	var dirtyReplyStatNoteIDs []string
	s.writeMu.Lock()
	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		s.writeMu.Unlock()
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	saved := 0
	for _, item := range prepared {
		inserted, referenced, err := s.savePreparedEventTx(ctx, tx, item.event, item.raw, now)
		if err != nil {
			s.writeMu.Unlock()
			return saved, err
		}
		if inserted {
			saved++
			dirtyReplyRefs = append(dirtyReplyRefs, referenced...)
			if isThreadingNoteKind(item.event.Kind) {
				dirtyReplyStatNoteIDs = append(dirtyReplyStatNoteIDs, immediateReplyStatNoteIDs(item.event)...)
			} else if item.event.Kind == nostrx.KindRelayListMetadata {
				dirtyRelayHintPubkeys = append(dirtyRelayHintPubkeys, item.event.PubKey)
			} else if item.event.Kind == nostrx.KindProfileMetadata {
				dirtyProfilePubkeys = append(dirtyProfilePubkeys, item.event.PubKey)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		s.writeMu.Unlock()
		return saved, err
	}
	s.writeMu.Unlock()
	if len(dirtyReplyRefs) > 0 {
		s.queueDirtyReplyStats(dirtyReplyRefs)
	}
	if saved > 0 {
		if s.sidecar != nil {
			s.sidecar.invalidateRelayKeys(dirtyRelayHintPubkeys)
			s.sidecar.invalidateProfileKeys(dirtyProfilePubkeys)
			s.sidecar.invalidateReplyKeys(dirtyReplyStatNoteIDs)
		}
		s.syncRelayHintsBestEffort(ctx, dirtyRelayHintPubkeys)
		s.syncProfileSummariesBestEffort(ctx, dirtyProfilePubkeys)
		s.syncReplyStatsBestEffort(ctx, dirtyReplyStatNoteIDs)
		s.maybePruneAsync()
	}
	return saved, nil
}

func immediateReplyStatNoteIDs(event nostrx.Event) []string {
	if !isThreadingNoteKind(event.Kind) || event.ID == "" {
		return nil
	}
	out := []string{event.ID}
	rootID, parentID := noteRootParent(event)
	if rootID != "" && rootID != event.ID {
		out = append(out, rootID)
	}
	if parentID != "" && parentID != event.ID && parentID != rootID {
		out = append(out, parentID)
	}
	return out
}

func compareReplaceableOrder(createdAtA int64, idA string, createdAtB int64, idB string) int {
	switch {
	case createdAtA > createdAtB:
		return 1
	case createdAtA < createdAtB:
		return -1
	case idA > idB:
		return 1
	case idA < idB:
		return -1
	default:
		return 0
	}
}

func latestReplaceableSlotTx(ctx context.Context, tx *sql.Tx, pubkey string, kind int) (string, int64, error) {
	var id string
	var createdAt int64
	err := tx.QueryRowContext(ctx, `SELECT id, created_at FROM events
		WHERE pubkey = ? AND kind = ?
		ORDER BY created_at DESC, id DESC LIMIT 1`, pubkey, kind).Scan(&id, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", 0, nil
		}
		return "", 0, err
	}
	return id, createdAt, nil
}

func (s *Store) savePreparedEventTx(ctx context.Context, tx *sql.Tx, event nostrx.Event, raw string, now int64) (bool, []string, error) {
	if event.ID == "" {
		return false, nil, errors.New("event id is required")
	}
	var referenced []string
	if !s.replaceableHistory && nostrx.IsReplaceablePruneSlotKind(event.Kind) {
		latestID, latestCreatedAt, err := latestReplaceableSlotTx(ctx, tx, event.PubKey, event.Kind)
		if err != nil {
			return false, nil, err
		}
		if latestID != "" && latestID != event.ID && compareReplaceableOrder(latestCreatedAt, latestID, event.CreatedAt, event.ID) >= 0 {
			return false, nil, nil
		}
	}
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO events
		(id, pubkey, created_at, kind, content, sig, raw_json, inserted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, event.PubKey, event.CreatedAt, event.Kind, event.Content, event.Sig, raw, now)
	if err != nil {
		return false, nil, err
	}
	inserted := true
	if rowsAffected, rowsErr := result.RowsAffected(); rowsErr == nil {
		inserted = rowsAffected > 0
	}
	if inserted {
		if _, err := tx.ExecContext(ctx, `DELETE FROM tags WHERE event_id = ?`, event.ID); err != nil {
			return false, nil, err
		}
		for idx, tag := range event.Tags {
			if len(tag) < 2 {
				continue
			}
			extra := ""
			if len(tag) > 2 {
				encoded, _ := json.Marshal(tag[2:])
				extra = string(encoded)
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO tags(event_id, idx, name, value, extra) VALUES (?, ?, ?, ?, ?)`,
				event.ID, idx, tag[0], tag[1], extra); err != nil {
				return false, nil, err
			}
		}
		if err := s.projectEventTx(ctx, tx, event, now); err != nil {
			return false, nil, err
		}
		if !s.replaceableHistory && nostrx.IsReplaceablePruneSlotKind(event.Kind) {
			if _, err := tx.ExecContext(ctx, `DELETE FROM relay_events WHERE event_id IN (
				SELECT id FROM events WHERE pubkey = ? AND kind = ? AND id != ?
			)`,
				event.PubKey, event.Kind, event.ID,
			); err != nil {
				return false, nil, err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE pubkey = ? AND kind = ? AND id != ?`,
				event.PubKey, event.Kind, event.ID,
			); err != nil {
				return false, nil, err
			}
		}
		if isThreadingNoteKind(event.Kind) {
			referenced = noteReferenceIDs(event)
		}
	}
	if event.RelayURL != "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO relay_events(event_id, relay_url, first_seen, last_seen)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(event_id, relay_url) DO UPDATE SET last_seen = excluded.last_seen`,
			event.ID, event.RelayURL, now, now)
		if err != nil {
			return false, nil, err
		}
	}
	return inserted, referenced, nil
}

func (s *Store) MarkCacheEvent(ctx context.Context, scope, key, eventID string) error {
	if scope == "" || key == "" || eventID == "" {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `INSERT INTO cache_events(scope, cache_key, event_id, seen_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(scope, cache_key, event_id) DO UPDATE SET seen_at = excluded.seen_at`,
		scope, key, eventID, time.Now().Unix())
	return err
}

func (s *Store) GetEvent(ctx context.Context, id string) (*nostrx.Event, error) {
	id = nostrx.CanonicalHex64(id)
	if id == "" {
		return nil, nil
	}
	row := s.db.QueryRowContext(ctx, `SELECT raw_json FROM events WHERE id = ?`, id)
	var raw string
	if err := row.Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return decodeEvent(raw)
}

func (s *Store) GetEvents(ctx context.Context, ids []string) (map[string]*nostrx.Event, error) {
	out := make(map[string]*nostrx.Event, len(ids))
	keys := uniqueNonEmpty(ids)
	if len(keys) == 0 {
		return out, nil
	}
	query := fmt.Sprintf(`SELECT raw_json FROM events WHERE id IN (%s)`, placeholders(len(keys)))
	args := make([]any, 0, len(keys))
	for _, id := range keys {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		event, err := decodeEvent(raw)
		if err != nil {
			return nil, err
		}
		out[event.ID] = event
	}
	return out, rows.Err()
}

func (s *Store) EventSummariesByIDs(ctx context.Context, ids []string) (map[string]*nostrx.Event, error) {
	out := make(map[string]*nostrx.Event, len(ids))
	keys := uniqueNonEmpty(ids)
	if len(keys) == 0 {
		return out, nil
	}
	query := fmt.Sprintf(`SELECT id, pubkey, created_at, kind, content FROM events WHERE id IN (%s)`, placeholders(len(keys)))
	args := make([]any, 0, len(keys))
	for _, id := range keys {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var event nostrx.Event
		if err := rows.Scan(&event.ID, &event.PubKey, &event.CreatedAt, &event.Kind, &event.Content); err != nil {
			return nil, err
		}
		copyEvent := event
		out[event.ID] = &copyEvent
	}
	return out, rows.Err()
}

func (s *Store) RelaySources(ctx context.Context, eventID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT relay_url FROM relay_events WHERE event_id = ? ORDER BY relay_url`, eventID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var relays []string
	for rows.Next() {
		var relay string
		if err := rows.Scan(&relay); err != nil {
			return nil, err
		}
		relays = append(relays, relay)
	}
	return relays, rows.Err()
}

func (s *Store) LatestReplaceable(ctx context.Context, pubkey string, kind int) (*nostrx.Event, error) {
	row := s.db.QueryRowContext(ctx, `SELECT raw_json FROM events WHERE pubkey = ? AND kind = ? ORDER BY created_at DESC, id DESC LIMIT 1`, pubkey, kind)
	var raw string
	if err := row.Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return decodeEvent(raw)
}

// RecentByKinds returns the most recent events of the given kinds created
// within [since, before). Both bounds are optional (0 disables). Results are
// ordered newest-first with (created_at, id) tuple paging.
func (s *Store) RecentByKinds(ctx context.Context, kinds []int, since int64, before int64, beforeID string, limit int) ([]nostrx.Event, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > nostrx.MaxRelayQueryLimit {
		limit = nostrx.DefaultRelayQueryLimit
	}
	if before <= 0 {
		before = time.Now().Unix() + 1
	}
	query := fmt.Sprintf(`SELECT raw_json FROM events
		WHERE kind IN (%s)
		AND (created_at < ? OR (created_at = ? AND id < ?))
		AND created_at >= ?
		ORDER BY created_at DESC, id DESC LIMIT ?`, placeholders(len(kinds)))
	args := make([]any, 0, len(kinds)+4)
	for _, kind := range kinds {
		args = append(args, kind)
	}
	args = append(args, before, before, beforeID, since, limit)
	return s.queryEvents(ctx, query, args...)
}

func (s *Store) RecentSummariesByKinds(ctx context.Context, kinds []int, since int64, before int64, beforeID string, limit int) ([]nostrx.Event, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > nostrx.MaxRelayQueryLimit {
		limit = nostrx.DefaultRelayQueryLimit
	}
	if before <= 0 {
		before = time.Now().Unix() + 1
	}
	query := fmt.Sprintf(`SELECT id, pubkey, created_at, kind, content FROM events
		WHERE kind IN (%s)
		AND (created_at < ? OR (created_at = ? AND id < ?))
		AND created_at >= ?
		ORDER BY created_at DESC, id DESC LIMIT ?`, placeholders(len(kinds)))
	args := make([]any, 0, len(kinds)+5)
	for _, kind := range kinds {
		args = append(args, kind)
	}
	args = append(args, before, before, beforeID, since, limit)
	return s.queryEventSummaries(ctx, query, args...)
}

// PruneEvents keeps at most `max` events in the store. Events are removed
// oldest-first (by inserted_at, then created_at) to bound storage FIFO-style.
// Returns the number of events deleted.
func (s *Store) PruneEvents(ctx context.Context, max int) (int64, error) {
	if max <= 0 {
		return 0, nil
	}
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events`).Scan(&total); err != nil {
		return 0, err
	}
	excess := total - int64(max)
	if excess <= 0 {
		return 0, nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `DELETE FROM events WHERE id IN (
		SELECT id FROM events ORDER BY inserted_at ASC, created_at ASC, id ASC LIMIT ?
	)`, excess)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM note_links WHERE note_id NOT IN (SELECT id FROM events)`); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM note_stats WHERE note_id NOT IN (SELECT id FROM events)`); err != nil {
		return 0, err
	}
	deleted, _ := result.RowsAffected()
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if deleted > 0 && s.sidecar != nil {
		// note_stats rows for pruned events were removed in-tx; drop the
		// reply-stat LRU so cached counts cannot outlive their underlying
		// SQLite rows. Profiles / relay hints are unaffected by event prune.
		s.sidecar.purgeReply()
	}
	if deleted > 0 {
		// Return freed pages to the OS where possible. This is a no-op on
		// older DBs that were not created with auto_vacuum=INCREMENTAL; the
		// daily VACUUM ticker covers those.
		if _, err := s.db.ExecContext(ctx, `PRAGMA incremental_vacuum`); err != nil {
			slog.Debug("incremental vacuum after prune failed", "err", err)
		}
	}
	return deleted, nil
}

func (s *Store) RecentByAuthors(ctx context.Context, authors []string, kinds []int, before int64, limit int) ([]nostrx.Event, error) {
	return s.RecentByAuthorsCursor(ctx, authors, kinds, before, "", limit)
}

func (s *Store) RecentByAuthorsCursor(ctx context.Context, authors []string, kinds []int, before int64, beforeID string, limit int) ([]nostrx.Event, error) {
	if len(authors) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > nostrx.MaxRelayQueryLimit {
		limit = nostrx.DefaultRelayQueryLimit
	}
	if before <= 0 {
		before = time.Now().Unix() + 1
	}
	query := fmt.Sprintf(`SELECT raw_json FROM events
		WHERE pubkey IN (%s) AND kind IN (%s)
		AND (created_at < ? OR (created_at = ? AND id < ?))
		ORDER BY created_at DESC, id DESC LIMIT ?`, placeholders(len(authors)), placeholders(len(kinds)))
	args := make([]any, 0, len(authors)+len(kinds)+2)
	for _, author := range authors {
		args = append(args, author)
	}
	for _, kind := range kinds {
		args = append(args, kind)
	}
	args = append(args, before, before, beforeID, limit)
	return s.queryEvents(ctx, query, args...)
}

func (s *Store) RecentSummariesByAuthorsCursor(ctx context.Context, authors []string, kinds []int, before int64, beforeID string, limit int) ([]nostrx.Event, error) {
	if len(authors) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > nostrx.MaxRelayQueryLimit {
		limit = nostrx.DefaultRelayQueryLimit
	}
	if before <= 0 {
		before = time.Now().Unix() + 1
	}
	if len(authors) <= maxAuthorsRecentSummariesIN {
		return s.recentSummariesByAuthorsCursorOnce(ctx, authors, kinds, before, beforeID, limit)
	}
	var merged []nostrx.Event
	for i := 0; i < len(authors); i += maxAuthorsRecentSummariesIN {
		j := i + maxAuthorsRecentSummariesIN
		if j > len(authors) {
			j = len(authors)
		}
		part, err := s.recentSummariesByAuthorsCursorOnce(ctx, authors[i:j], kinds, before, beforeID, limit)
		if err != nil {
			return nil, err
		}
		merged = append(merged, part...)
	}
	sort.SliceStable(merged, func(a, b int) bool {
		if merged[a].CreatedAt != merged[b].CreatedAt {
			return merged[a].CreatedAt > merged[b].CreatedAt
		}
		return merged[a].ID > merged[b].ID
	})
	if len(merged) > limit {
		merged = merged[:limit]
	}
	return merged, nil
}

func (s *Store) recentSummariesByAuthorsCursorOnce(ctx context.Context, authors []string, kinds []int, before int64, beforeID string, limit int) ([]nostrx.Event, error) {
	if len(authors) == 0 {
		return nil, nil
	}
	query := fmt.Sprintf(`SELECT id, pubkey, created_at, kind, content FROM events
		WHERE pubkey IN (%s) AND kind IN (%s)
		AND (created_at < ? OR (created_at = ? AND id < ?))
		ORDER BY created_at DESC, id DESC LIMIT ?`, placeholders(len(authors)), placeholders(len(kinds)))
	args := authorKindCursorArgs(authors, kinds, before, beforeID, limit)
	return s.queryEventSummaries(ctx, query, args...)
}

func (s *Store) NewerSummariesByKinds(ctx context.Context, kinds []int, since int64, sinceID string, limit int) ([]nostrx.Event, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > nostrx.MaxRelayQueryLimit {
		limit = nostrx.DefaultRelayQueryLimit
	}
	query := fmt.Sprintf(`SELECT id, pubkey, created_at, kind, content FROM events
		WHERE kind IN (%s)
		AND (created_at > ? OR (created_at = ? AND id > ?))
		ORDER BY created_at DESC, id DESC LIMIT ?`, placeholders(len(kinds)))
	args := kindCursorArgs(kinds, since, sinceID, limit)
	return s.queryEventSummaries(ctx, query, args...)
}

func (s *Store) NewerByAuthorsCursor(ctx context.Context, authors []string, kinds []int, since int64, sinceID string, limit int) ([]nostrx.Event, error) {
	if len(authors) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > nostrx.MaxRelayQueryLimit {
		limit = nostrx.DefaultRelayQueryLimit
	}
	query := fmt.Sprintf(`SELECT raw_json FROM events
		WHERE pubkey IN (%s) AND kind IN (%s)
		AND (created_at > ? OR (created_at = ? AND id > ?))
		ORDER BY created_at DESC, id DESC LIMIT ?`, placeholders(len(authors)), placeholders(len(kinds)))
	args := make([]any, 0, len(authors)+len(kinds)+4)
	for _, author := range authors {
		args = append(args, author)
	}
	for _, kind := range kinds {
		args = append(args, kind)
	}
	args = append(args, since, since, sinceID, limit)
	return s.queryEvents(ctx, query, args...)
}

func (s *Store) NewerSummariesByAuthorsCursor(ctx context.Context, authors []string, kinds []int, since int64, sinceID string, limit int) ([]nostrx.Event, error) {
	if len(authors) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > nostrx.MaxRelayQueryLimit {
		limit = nostrx.DefaultRelayQueryLimit
	}
	query := fmt.Sprintf(`SELECT id, pubkey, created_at, kind, content FROM events
		WHERE pubkey IN (%s) AND kind IN (%s)
		AND (created_at > ? OR (created_at = ? AND id > ?))
		ORDER BY created_at DESC, id DESC LIMIT ?`, placeholders(len(authors)), placeholders(len(kinds)))
	args := authorKindCursorArgs(authors, kinds, since, sinceID, limit)
	return s.queryEventSummaries(ctx, query, args...)
}

func (s *Store) RecentByCacheCursor(ctx context.Context, scope, key string, authors []string, kinds []int, before int64, beforeID string, limit int) ([]nostrx.Event, error) {
	if scope == "" || key == "" || len(authors) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > nostrx.MaxRelayQueryLimit {
		limit = nostrx.DefaultRelayQueryLimit
	}
	if before <= 0 {
		before = time.Now().Unix() + 1
	}
	query := fmt.Sprintf(`SELECT e.raw_json FROM events e
		JOIN cache_events ce ON ce.event_id = e.id
		WHERE ce.scope = ? AND ce.cache_key = ?
		AND e.pubkey IN (%s) AND e.kind IN (%s)
		AND (e.created_at < ? OR (e.created_at = ? AND e.id < ?))
		ORDER BY e.created_at DESC, e.id DESC LIMIT ?`, placeholders(len(authors)), placeholders(len(kinds)))
	args := make([]any, 0, 2+len(authors)+len(kinds)+4)
	args = append(args, scope, key)
	for _, author := range authors {
		args = append(args, author)
	}
	for _, kind := range kinds {
		args = append(args, kind)
	}
	args = append(args, before, before, beforeID, limit)
	return s.queryEvents(ctx, query, args...)
}

func (s *Store) RepliesTo(ctx context.Context, eventID string, limit int) ([]nostrx.Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	return s.queryEvents(ctx, `SELECT e.raw_json FROM events e
		JOIN tags t ON t.event_id = e.id
		WHERE t.name = 'e' AND t.value = ? AND e.kind = 1
		ORDER BY e.created_at ASC, e.id ASC LIMIT ?`, eventID, limit)
}

func (s *Store) ReplyCounts(ctx context.Context, eventIDs []string) (map[string]int, error) {
	counts := make(map[string]int, len(eventIDs))
	if len(eventIDs) == 0 {
		return counts, nil
	}

	seen := make(map[string]bool, len(eventIDs))
	ids := make([]string, 0, len(eventIDs))
	for _, id := range eventIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
		counts[id] = 0
	}
	if len(ids) == 0 {
		return counts, nil
	}

	query := fmt.Sprintf(`SELECT t.value, COUNT(DISTINCT e.id) FROM tags t
		JOIN events e ON e.id = t.event_id
		WHERE t.name = 'e' AND t.value IN (%s) AND e.kind = 1
		GROUP BY t.value`, placeholders(len(ids)))
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			return nil, err
		}
		counts[id] = count
	}
	return counts, rows.Err()
}

func (s *Store) FollowersOf(ctx context.Context, pubkey string, limit int) ([]nostrx.Event, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	return s.queryEvents(ctx, `SELECT e.raw_json FROM events e
		JOIN tags t ON t.event_id = e.id
		WHERE t.name = 'p' AND t.value = ? AND e.kind = 3
		ORDER BY e.created_at DESC LIMIT ?`, pubkey, limit)
}

func (s *Store) LatestReplaceableByPubkeys(ctx context.Context, pubkeys []string, kind int) (map[string]*nostrx.Event, error) {
	out := make(map[string]*nostrx.Event, len(pubkeys))
	if len(pubkeys) == 0 {
		return out, nil
	}
	seen := make(map[string]bool, len(pubkeys))
	keys := make([]string, 0, len(pubkeys))
	for _, pubkey := range pubkeys {
		if pubkey == "" || seen[pubkey] {
			continue
		}
		seen[pubkey] = true
		keys = append(keys, pubkey)
	}
	if len(keys) == 0 {
		return out, nil
	}

	query := fmt.Sprintf(`SELECT pubkey, raw_json FROM events
		WHERE kind = ? AND pubkey IN (%s)
		ORDER BY pubkey ASC, created_at DESC, id DESC`, placeholders(len(keys)))
	args := make([]any, 0, len(keys)+1)
	args = append(args, kind)
	for _, pubkey := range keys {
		args = append(args, pubkey)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var pubkey string
		var raw string
		if err := rows.Scan(&pubkey, &raw); err != nil {
			return nil, err
		}
		if _, exists := out[pubkey]; exists {
			continue
		}
		event, err := decodeEvent(raw)
		if err != nil {
			return nil, err
		}
		out[pubkey] = event
	}
	return out, rows.Err()
}

func (s *Store) LatestReplaceablesByKind(ctx context.Context, kind int) ([]nostrx.Event, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT pubkey, raw_json FROM events WHERE kind = ? ORDER BY pubkey ASC, created_at DESC, id DESC`, kind)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]nostrx.Event, 0, 128)
	seen := make(map[string]struct{})
	for rows.Next() {
		var pubkey string
		var raw string
		if err := rows.Scan(&pubkey, &raw); err != nil {
			return nil, err
		}
		if _, ok := seen[pubkey]; ok {
			continue
		}
		seen[pubkey] = struct{}{}
		event, err := decodeEvent(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, *event)
	}
	return out, rows.Err()
}

func (s *Store) Stats(ctx context.Context) (CacheStats, error) {
	var stats CacheStats
	queries := []struct {
		value *int64
		sql   string
	}{
		{&stats.Events, `SELECT COUNT(*) FROM events`},
		{&stats.Tags, `SELECT COUNT(*) FROM tags`},
		{&stats.RelayEvents, `SELECT COUNT(*) FROM relay_events`},
		{&stats.Relays, `SELECT COUNT(*) FROM relay_status`},
	}
	for _, query := range queries {
		if err := s.db.QueryRowContext(ctx, query.sql).Scan(query.value); err != nil {
			return CacheStats{}, err
		}
	}
	return stats, nil
}

func (s *Store) queueDirtyReplyStats(noteIDs []string) {
	if len(noteIDs) == 0 {
		return
	}
	s.dirtyRepliesMu.Lock()
	for _, noteID := range noteIDs {
		if noteID == "" {
			continue
		}
		s.dirtyReplies[noteID] = struct{}{}
	}
	s.dirtyRepliesMu.Unlock()
}

func (s *Store) takeDirtyReplyStats(limit int) []string {
	if limit <= 0 {
		limit = 128
	}
	s.dirtyRepliesMu.Lock()
	defer s.dirtyRepliesMu.Unlock()
	if len(s.dirtyReplies) == 0 {
		return nil
	}
	out := make([]string, 0, min(limit, len(s.dirtyReplies)))
	for noteID := range s.dirtyReplies {
		out = append(out, noteID)
		delete(s.dirtyReplies, noteID)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func (s *Store) recomputeDirtyReplyStats() {
	ids := s.takeDirtyReplyStats(32)
	if len(ids) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(s.bgCtx, 4*time.Second)
	defer cancel()
	type replyStatUpdate struct {
		noteID      string
		direct      int
		descendants int
		lastReplyAt int64
	}
	updates := make([]replyStatUpdate, 0, len(ids))
	retry := make([]string, 0, len(ids))
	now := time.Now().Unix()
	for _, noteID := range ids {
		directReplies, err := s.countDirectReplies(ctx, noteID)
		if err != nil {
			retry = append(retry, noteID)
			continue
		}
		descendantReplies, err := s.descendantCount(ctx, noteID)
		if err != nil {
			retry = append(retry, noteID)
			continue
		}
		updates = append(updates, replyStatUpdate{
			noteID:      noteID,
			direct:      directReplies,
			descendants: descendantReplies,
			lastReplyAt: now,
		})
	}
	if len(retry) > 0 {
		s.queueDirtyReplyStats(retry)
	}
	if len(updates) == 0 {
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback() }()
	for _, update := range updates {
		if err := applyReplyStatUpdateTx(ctx, tx, update.noteID, update.direct, update.descendants, update.lastReplyAt); err != nil {
			retry = append(retry, update.noteID)
		}
	}
	if err := tx.Commit(); err != nil {
		for _, update := range updates {
			retry = append(retry, update.noteID)
		}
	} else {
		noteIDs := make([]string, 0, len(updates))
		for _, update := range updates {
			noteIDs = append(noteIDs, update.noteID)
		}
		s.syncReplyStatsBestEffort(ctx, noteIDs)
	}
	if len(retry) > 0 {
		s.queueDirtyReplyStats(retry)
	}
}

func (s *Store) maybePruneAsync() {
	if s.retentionMax <= 0 {
		return
	}
	interval := s.pruneEvery
	if interval <= 0 {
		interval = 1000
	}
	writes := s.writeCount.Add(1)
	// The first post-start write should reconcile any preexisting overflow
	// (e.g. after enabling retention on an already-large DB). After that we
	// fall back to the regular periodic cadence to avoid counting on every save.
	if writes != 1 && writes%interval != 0 {
		return
	}
	if !s.pruning.CompareAndSwap(false, true) {
		return
	}
	s.bgWG.Add(1)
	go func() {
		defer s.bgWG.Done()
		defer s.pruning.Store(false)
		ctx, cancel := context.WithTimeout(s.bgCtx, 5*time.Second)
		defer cancel()
		if _, err := s.PruneEvents(ctx, s.retentionMax); err != nil {
			slog.Debug("periodic prune failed", "err", err)
		}
	}()
}

// DeleteEventsForTesting removes events by id (tags cascade on FK). note_links
// rows are not removed; used by integration tests where reply edges exist before
// bodies are refetched from relays.
func (s *Store) DeleteEventsForTesting(ctx context.Context, noteIDs []string) error {
	if s == nil || s.db == nil {
		return nil
	}
	ids := uniqueNonEmpty(noteIDs)
	if len(ids) == 0 {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	ph := placeholders(len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE id IN (`+ph+`)`, args...)
	return err
}

func (s *Store) DBStats() sql.DBStats {
	if s == nil || s.db == nil {
		return sql.DBStats{}
	}
	return s.db.Stats()
}

func (s *Store) DirtyReplyStatsPending() int {
	s.dirtyRepliesMu.Lock()
	defer s.dirtyRepliesMu.Unlock()
	return len(s.dirtyReplies)
}

