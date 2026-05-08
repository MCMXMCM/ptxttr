package httpx

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"ptxt-nstr/internal/store"
)

type warmJob struct {
	key      string
	kind     string
	viewer   string
	pubkey   string
	pubkeys  []string
	authors  []string
	before   int64
	limit    int
	relays   []string
	eventIDs []string
}

type warmQueue struct {
	server  *Server
	ch      chan warmJob
	mu      sync.Mutex
	pending map[string]struct{}
	wg      sync.WaitGroup
}

func newWarmQueue(server *Server, workers int) *warmQueue {
	if workers <= 0 {
		workers = 1
	}
	queue := &warmQueue{
		server:  server,
		ch:      make(chan warmJob, 128),
		pending: make(map[string]struct{}),
	}
	for range workers {
		queue.wg.Add(1)
		go queue.worker()
	}
	return queue
}

func (q *warmQueue) enqueue(job warmJob) {
	if q == nil || job.key == "" || job.kind == "" {
		return
	}
	q.mu.Lock()
	if _, exists := q.pending[job.key]; exists {
		q.mu.Unlock()
		q.server.metrics.Add("warm.deduped", 1)
		return
	}
	q.pending[job.key] = struct{}{}
	q.mu.Unlock()

	select {
	case q.ch <- job:
		q.server.metrics.Add("warm.enqueued", 1)
	default:
		q.mu.Lock()
		delete(q.pending, job.key)
		q.mu.Unlock()
		q.server.metrics.Add("warm.dropped", 1)
	}
}

func (q *warmQueue) worker() {
	defer q.wg.Done()
	for {
		select {
		case <-q.server.ctx.Done():
			return
		case job := <-q.ch:
			func() {
				timeout := q.server.cfg.WarmJobTimeout
				if timeout <= 0 {
					timeout = 45 * time.Second
				}
				jobCtx, cancel := context.WithTimeout(q.server.ctx, timeout)
				defer cancel()
				defer func() {
					q.mu.Lock()
					delete(q.pending, job.key)
					q.mu.Unlock()
				}()
				started := time.Now()
				q.server.runWithRelayWriteBudget(jobCtx, "warm."+job.kind, func() {
					q.server.handleWarmJob(jobCtx, job)
				})
				q.server.metrics.Observe("warm."+job.kind, time.Since(started))
				if jobCtx.Err() == context.DeadlineExceeded {
					q.server.metrics.Add("warm."+job.kind+".timeout", 1)
					slog.Warn("warm job timed out", "kind", job.kind, "key", job.key, "timeout", timeout)
				}
			}()
		}
	}
}

func (q *warmQueue) close() {
	if q == nil {
		return
	}
	q.wg.Wait()
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
				s.refreshReplies(ctx, eventID, job.relays)
			}
			s.enqueueWarmNotes("noteReplies", tail, job.relays)
			s.metrics.Add("warm.noteReplies.chunked", 1)
			return
		}
		for _, eventID := range ids {
			if ctx.Err() != nil {
				return
			}
			s.refreshReplies(ctx, eventID, job.relays)
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
	}
}

func (s *Server) enqueueWarmNotes(kind string, ids []string, relays []string) {
	if s == nil || s.warmer == nil || len(ids) == 0 {
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
	s.warmAuthors(ids, relays)
	s.metrics.Add("warm.authors.requeued_timeout", 1)
}

func (s *Server) requeueWarmNotesOnTimeout(ctx context.Context, kind string, ids []string, relays []string) {
	if ctx.Err() != context.DeadlineExceeded {
		return
	}
	s.enqueueWarmNotes(kind, ids, relays)
	s.metrics.Add("warm."+kind+".requeued_timeout", 1)
}

func (s *Server) warmAuthor(pubkey string, relays []string) {
	if s == nil || s.warmer == nil || pubkey == "" {
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
	if s == nil || s.warmer == nil || len(pubkeys) == 0 {
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
	if s == nil || s.warmer == nil || len(authors) == 0 {
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
	if s == nil || s.warmer == nil {
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
	s.touchHydrationTargets(s.ctx, noteReplyWarmTargets(ids))
	s.touchHydrationTargets(s.ctx, noteReactionWarmTargets(ids))
	s.enqueueWarmNotes("noteReplies", ids, relays)
	s.enqueueWarmNotes("noteReactions", ids, relays)
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
