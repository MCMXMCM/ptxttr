package httpx

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

type warmJob struct {
	enqueuedAt time.Time
	key        string
	kind       string
	viewer     string
	pubkey     string
	pubkeys    []string
	authors    []string
	before     int64
	limit      int
	relays     []string
	eventIDs   []string
}

type warmQueue struct {
	server      *Server
	ch          chan warmJob
	mu          sync.Mutex
	pending     map[string]struct{}
	wg          sync.WaitGroup
	interactive bool
}

func newWarmQueue(server *Server, workers, capacity int) *warmQueue {
	if workers <= 0 {
		workers = 1
	}
	if capacity <= 0 {
		capacity = 128
	}
	queue := &warmQueue{
		server:  server,
		ch:      make(chan warmJob, capacity),
		pending: make(map[string]struct{}),
	}
	for range workers {
		queue.wg.Add(1)
		go queue.worker()
	}
	return queue
}

func (q *warmQueue) enqueue(job warmJob) bool {
	if q == nil || job.key == "" || job.kind == "" {
		return false
	}
	select {
	case <-q.server.ctx.Done():
		return false
	default:
	}
	q.mu.Lock()
	if _, exists := q.pending[job.key]; exists {
		q.mu.Unlock()
		q.server.metrics.Add("warm.deduped", 1)
		return true
	}
	q.pending[job.key] = struct{}{}
	q.mu.Unlock()
	if job.enqueuedAt.IsZero() {
		job.enqueuedAt = time.Now()
	}

	select {
	case q.ch <- job:
		q.server.metrics.Add("warm.enqueued", 1)
		return true
	case <-q.server.ctx.Done():
		q.mu.Lock()
		delete(q.pending, job.key)
		q.mu.Unlock()
		return false
	default:
		q.mu.Lock()
		delete(q.pending, job.key)
		q.mu.Unlock()
		q.server.metrics.Add("warm.dropped", 1)
		return false
	}
}

func (q *warmQueue) worker() {
	defer q.wg.Done()
	for {
		select {
		case <-q.server.ctx.Done():
			return
		default:
		}

		select {
		case <-q.server.ctx.Done():
			return
		case job := <-q.ch:
			select {
			case <-q.server.ctx.Done():
				q.finish(job.key)
				return
			default:
			}
			q.run(job)
		}
	}
}

func (q *warmQueue) run(job warmJob) {
	defer q.finish(job.key)
	if !job.enqueuedAt.IsZero() {
		q.server.metrics.Observe("warm.queue_wait", time.Since(job.enqueuedAt))
	}
	if !q.server.waitForBackgroundActive(q.server.ctx) {
		return
	}
	timeout := q.server.cfg.WarmJobTimeout
	if q.interactive {
		timeout = 8 * time.Second
	}
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	jobCtx, cancel := context.WithTimeout(q.server.ctx, timeout)
	defer cancel()
	started := time.Now()
	run := q.server.runWithRelayWriteBudget
	if q.interactive {
		run = q.server.runWithInteractiveRelayBudget
	}
	run(jobCtx, "warm."+job.kind, func() { q.server.handleWarmJob(jobCtx, job) })
	q.server.metrics.Observe("warm."+job.kind, time.Since(started))
	if jobCtx.Err() == context.DeadlineExceeded {
		q.server.metrics.Add("warm."+job.kind+".timeout", 1)
		slog.Warn("warm job timed out", "kind", job.kind, "items", warmJobItemCount(job), "timeout", timeout)
	}
}

func (q *warmQueue) hasPending(key string) bool {
	if q == nil || key == "" {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	_, ok := q.pending[key]
	return ok
}

func warmJobItemCount(job warmJob) int {
	switch {
	case len(job.eventIDs) > 0:
		return len(job.eventIDs)
	case len(job.pubkeys) > 0:
		return len(job.pubkeys)
	case len(job.authors) > 0:
		return len(job.authors)
	case job.pubkey != "":
		return 1
	default:
		return 0
	}
}

func (q *warmQueue) finish(key string) {
	q.mu.Lock()
	delete(q.pending, key)
	q.mu.Unlock()
}

func (q *warmQueue) close() {
	if q == nil {
		return
	}
	q.wg.Wait()
}

func (q *warmQueue) depth() int {
	if q == nil {
		return 0
	}
	return len(q.ch)
}

func (q *warmQueue) pendingCount() int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

func (s *Server) handleWarmJob(ctx context.Context, job warmJob) {
	maxAuthors := s.cfg.WarmMaxAuthorsPerJob
	if maxAuthors <= 0 {
		maxAuthors = 16
	}
	maxNotes := s.cfg.WarmMaxNoteIDsPerJob
	if maxNotes <= 0 {
		maxNotes = 12
	}
	switch job.kind {
	case "author":
		s.refreshAuthor(ctx, job.pubkey, job.relays)
	case "authors":
		keys := job.pubkeys
		if len(keys) > maxAuthors {
			head, tail := keys[:maxAuthors], keys[maxAuthors:]
			for i, pubkey := range head {
				if ctx.Err() != nil {
					s.requeueWarmAuthorsOnTimeout(ctx, append(append([]string(nil), head[i:]...), tail...), job.relays)
					return
				}
				s.refreshAuthor(ctx, pubkey, job.relays)
			}
			s.warmAuthors(tail, job.relays)
			s.metrics.Add("warm.authors.chunked", 1)
			return
		}
		for _, pubkey := range keys {
			if ctx.Err() != nil {
				return
			}
			s.refreshAuthor(ctx, pubkey, job.relays)
		}
	case "recent":
		s.refreshRecent(ctx, job.viewer, job.authors, job.before, job.limit, job.relays, 0)
	case "noteReplies":
		ids := job.eventIDs
		if len(ids) > maxNotes {
			head, tail := ids[:maxNotes], ids[maxNotes:]
			for i, eventID := range head {
				if ctx.Err() != nil {
					s.requeueWarmNotesOnTimeout(ctx, "noteReplies", append(append([]string(nil), head[i:]...), tail...), job.relays)
					return
				}
				s.refreshRepliesBackground(ctx, eventID, job.viewer, nil, job.relays)
			}
			s.enqueueWarmNotesForViewer(job.viewer, "noteReplies", tail, job.relays)
			s.metrics.Add("warm.noteReplies.chunked", 1)
			return
		}
		for _, eventID := range ids {
			if ctx.Err() != nil {
				return
			}
			s.refreshRepliesBackground(ctx, eventID, job.viewer, nil, job.relays)
		}
	case "noteReactions":
		ids := job.eventIDs
		if len(ids) > maxNotes {
			head, tail := ids[:maxNotes], ids[maxNotes:]
			for i, eventID := range head {
				if ctx.Err() != nil {
					s.requeueWarmNotesOnTimeout(ctx, "noteReactions", append(append([]string(nil), head[i:]...), tail...), job.relays)
					return
				}
				s.refreshReactionsForNote(ctx, eventID, job.relays)
			}
			s.enqueueWarmNotes("noteReactions", tail, job.relays)
			s.metrics.Add("warm.noteReactions.chunked", 1)
			return
		}
		for _, eventID := range ids {
			if ctx.Err() != nil {
				return
			}
			s.refreshReactionsForNote(ctx, eventID, job.relays)
		}
	case "threadGraph":
		for _, rootID := range job.eventIDs {
			if ctx.Err() != nil {
				return
			}
			s.eventsByID(ctx, []string{rootID}, job.relays)
			s.refreshRepliesBackground(ctx, rootID, job.viewer, nil, job.relays)
			s.buildThreadGraphCache(ctx, rootID)
		}
	case "threadMaterialize":
		for _, selectedID := range job.eventIDs {
			if ctx.Err() != nil {
				return
			}
			if state, _ := s.threadProjectionStatus(ctx, selectedID); state == threadProjectionReady {
				s.metrics.Add("warm.threadMaterialize.skipped_ready", 1)
				continue
			}
			s.materializeThread(ctx, job.viewer, selectedID, job.relays)
		}
	}
}

func (s *Server) enqueueWarmNotes(kind string, ids []string, relays []string) {
	if s == nil || !s.allowLegacyWarmers() || s.warmer == nil || len(ids) == 0 {
		return
	}
	sort.Strings(ids)
	s.warmer.enqueue(warmJob{
		key:      kind + ":" + strings.Join(ids, ","),
		kind:     kind,
		eventIDs: append([]string(nil), ids...),
		relays:   append([]string(nil), relays...),
	})
}

func (s *Server) requeueWarmAuthorsOnTimeout(ctx context.Context, ids []string, relays []string) {
	if ctx.Err() != context.DeadlineExceeded {
		return
	}
	// Periodic hydration/crawler passes provide the next retry. Immediate
	// requeue under a persistently failing relay fleet turns a timeout into an
	// endless queue churn loop that competes with foreground requests.
	_ = ids
	_ = relays
	s.metrics.Add("warm.authors.abandoned_timeout", 1)
}

func (s *Server) requeueWarmNotesOnTimeout(ctx context.Context, kind string, ids []string, relays []string) {
	if ctx.Err() != context.DeadlineExceeded {
		return
	}
	_ = ids
	_ = relays
	s.metrics.Add("warm."+kind+".abandoned_timeout", 1)
}

func (s *Server) warmAuthor(pubkey string, relays []string) {
	if s == nil || !s.allowLegacyWarmers() || s.warmer == nil || pubkey == "" {
		return
	}
	s.touchHydrationTargets(s.ctx, authorWarmTargets([]string{pubkey}))
	s.warmer.enqueue(warmJob{
		key:    "author:" + pubkey,
		kind:   "author",
		pubkey: pubkey,
		relays: append([]string(nil), relays...),
	})
}

func (s *Server) warmAuthors(pubkeys []string, relays []string) {
	if s == nil || !s.allowLegacyWarmers() || s.warmer == nil || len(pubkeys) == 0 {
		return
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
		return
	}
	sort.Strings(keys)
	s.touchHydrationTargets(s.ctx, authorWarmTargets(keys))
	s.warmer.enqueue(warmJob{
		key:     "authors:" + strings.Join(keys, ","),
		kind:    "authors",
		pubkeys: keys,
		relays:  append([]string(nil), relays...),
	})
}

func (s *Server) warmRecent(viewer string, authors []string, before int64, limit int, relays []string) {
	if s == nil || !s.allowLegacyWarmers() || s.warmer == nil || len(authors) == 0 {
		return
	}
	if before <= 0 {
		before = time.Now().Unix() + 1
	}
	s.warmer.enqueue(warmJob{
		key:     "recent:" + authorsCacheKey(authors) + ":" + cacheCursorKey(before, ""),
		kind:    "recent",
		viewer:  viewer,
		authors: append([]string(nil), authors...),
		before:  before,
		limit:   limit,
		relays:  append([]string(nil), relays...),
	})
}

func (s *Server) warmThread(eventIDs []string, relays []string) {
	s.warmThreadForViewer("", eventIDs, relays)
}

func (s *Server) warmThreadGraph(rootIDs []string, relays []string) {
	s.warmThreadGraphForViewer("", rootIDs, relays)
}

func (s *Server) warmThreadGraphForViewer(viewer string, rootIDs []string, relays []string) {
	if s == nil || !s.allowLegacyWarmers() || s.warmer == nil || len(rootIDs) == 0 {
		return
	}
	ids := trimWarmStrings(rootIDs, s.cfg.WarmMaxNoteIDsPerJob)
	if len(ids) == 0 {
		return
	}
	if s.store != nil {
		if roots, err := s.store.ThreadRootIDsByNoteIDs(s.ctx, ids); err == nil {
			for i, id := range ids {
				if rootID := roots[id]; rootID != "" {
					ids[i] = rootID
				}
			}
			ids = trimWarmStrings(ids, s.cfg.WarmMaxNoteIDsPerJob)
			if len(ids) == 0 {
				return
			}
		}
	}
	sort.Strings(ids)
	s.warmer.enqueue(warmJob{
		key:      "threadGraph:" + strings.Join(ids, ","),
		kind:     "threadGraph",
		viewer:   viewer,
		eventIDs: ids,
		relays:   append([]string(nil), relays...),
	})
}

func (s *Server) warmThreadsFromRecentSummaries(viewer string, baseRelays []string, recent []nostrx.Event, limit int) {
	if s == nil || !s.allowLegacyWarmers() || len(recent) == 0 {
		return
	}
	nWarm := min(limit, len(recent))
	ids := make([]string, 0, nWarm)
	mergedRelays := append([]string(nil), baseRelays...)
	for i := 0; i < nWarm; i++ {
		if recent[i].ID == "" {
			continue
		}
		ids = append(ids, recent[i].ID)
		mergedRelays = append(mergedRelays, s.threadRelays(baseRelays, recent[i])...)
	}
	if len(ids) == 0 {
		return
	}
	maxRelays := s.cfg.ThreadMaxRelays
	if maxRelays <= 0 {
		maxRelays = 16
	}
	s.warmThreadForViewer(viewer, ids, nostrx.NormalizeRelayList(mergedRelays, maxRelays))
}

func (s *Server) warmThreadForViewer(viewer string, eventIDs []string, relays []string) {
	if s == nil || !s.allowLegacyWarmers() || s.warmer == nil {
		return
	}
	ids := make([]string, 0, len(eventIDs))
	seen := make(map[string]bool, len(eventIDs))
	for _, id := range eventIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return
	}
	sort.Strings(ids)
	s.touchHydrationTargets(s.ctx, noteReplyWarmTargetsForViewer(viewer, ids))
	s.touchHydrationTargets(s.ctx, noteReactionWarmTargets(ids))
	s.enqueueWarmNotesForViewer(viewer, "noteReplies", ids, relays)
	s.enqueueWarmNotesForViewer(viewer, "noteReactions", ids, relays)
	s.warmThreadGraphForViewer(viewer, ids, relays)
}

func (s *Server) enqueueWarmNotesForViewer(viewer, kind string, ids []string, relays []string) {
	if s == nil || !s.allowLegacyWarmers() || s.warmer == nil || len(ids) == 0 {
		return
	}
	sort.Strings(ids)
	s.warmer.enqueue(warmJob{
		key:      kind + ":" + strings.Join(ids, ","),
		kind:     kind,
		viewer:   viewer,
		eventIDs: append([]string(nil), ids...),
		relays:   append([]string(nil), relays...),
	})
}

func (s *Server) buildThreadGraphCache(ctx context.Context, rootID string) {
	if s == nil || s.store == nil || rootID == "" {
		return
	}
	if _, err := s.store.BuildThreadGraphCache(ctx, rootID, threadTreeFetchLimit); err != nil {
		s.metrics.Add("thread.graph_cache.build_error", 1)
		slog.Debug("thread graph cache build failed", "root", short(rootID), "err", err)
		return
	}
	s.metrics.Add("thread.graph_cache.built", 1)
}

func trimWarmStrings(values []string, limit int) []string {
	return limitedStrings(uniqueNonEmptyStable(values), limit)
}

func profileTouchTargets(pubkeys []string, priority int) []store.HydrationTarget {
	keys := uniqueNonEmptyStable(pubkeys)
	if len(keys) == 0 {
		return nil
	}
	targets := make([]store.HydrationTarget, 0, len(keys))
	for _, pubkey := range keys {
		targets = append(targets, store.HydrationTarget{
			EntityType: "profile",
			EntityID:   pubkey,
			Priority:   priority,
		})
	}
	return targets
}
