package httpx

import (
	"context"
	"net/http"
	"runtime"
	"strings"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
	"ptxt-nstr/internal/thread"
)

func (s *Server) handleDebugCache(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.Stats(r.Context())
	writeJSON(w, stats, err)
}

func (s *Server) handleDebugMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now().Unix()
	staleNoteReplies, _ := s.store.CountStaleHydration(ctx, "noteReplies", now)
	staleSeedContact, _ := s.store.CountStaleHydration(ctx, store.EntityTypeSeedContact, now)
	staleKnownViewer, _ := s.store.CountStaleHydration(ctx, store.EntityTypeKnownViewer, now)
	warmDepth := 0
	warmPending := 0
	if s.warmer != nil {
		warmDepth = s.warmer.depth()
		warmPending = s.warmer.pendingCount()
	}
	writeJSON(w, map[string]any{
		"relay_queries": s.nostr.Metrics(),
		"app":           s.metrics.Snapshot(),
		"health":        s.healthSnapshot(),
		"gauges": map[string]any{
			"warm.queue_depth":                    warmDepth,
			"warm.pending_jobs":                   warmPending,
			"hydration_state.stale.noteReplies":   staleNoteReplies,
			"hydration_state.stale.seedContact":   staleSeedContact,
			"hydration_state.stale.knownViewer":   staleKnownViewer,
			"active_viewers.len":                  s.activeViewers.Len(),
		},
	}, nil)
}

func (s *Server) handleDebugRuntime(w http.ResponseWriter, _ *http.Request) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	writeJSON(w, map[string]any{
		"runtime": map[string]any{
			"goroutines":        runtime.NumGoroutine(),
			"cgo_calls":         runtime.NumCgoCall(),
			"heap_alloc":        mem.HeapAlloc,
			"heap_inuse":        mem.HeapInuse,
			"heap_idle":         mem.HeapIdle,
			"heap_released":     mem.HeapReleased,
			"heap_objects":      mem.HeapObjects,
			"stack_inuse":       mem.StackInuse,
			"stack_sys":         mem.StackSys,
			"mspan_inuse":       mem.MSpanInuse,
			"mcache_inuse":      mem.MCacheInuse,
			"buck_hash_sys":     mem.BuckHashSys,
			"gc_sys":            mem.GCSys,
			"other_sys":         mem.OtherSys,
			"sys":               mem.Sys,
			"total_alloc":       mem.TotalAlloc,
			"next_gc":           mem.NextGC,
			"last_gc_unix_nano": mem.LastGC,
			"pause_total_ns":    mem.PauseTotalNs,
			"num_gc":            mem.NumGC,
			"forced_gc":         mem.NumForcedGC,
			"gccpu_fraction":    mem.GCCPUFraction,
		},
		"db": map[string]any{
			"max_open_connections": s.store.DBStats().MaxOpenConnections,
			"open_connections":     s.store.DBStats().OpenConnections,
			"in_use":               s.store.DBStats().InUse,
			"idle":                 s.store.DBStats().Idle,
			"wait_count":           s.store.DBStats().WaitCount,
			"wait_duration":        s.store.DBStats().WaitDuration.String(),
			"max_idle_closed":      s.store.DBStats().MaxIdleClosed,
			"max_idle_time_closed": s.store.DBStats().MaxIdleTimeClosed,
			"max_lifetime_closed":  s.store.DBStats().MaxLifetimeClosed,
		},
		"store": map[string]any{
			"dirty_reply_stats_pending": s.store.DirtyReplyStatsPending(),
			"sidecar_lru":               s.store.SidecarLRUStats(),
		},
	}, nil)
}

type debugFirehoseSample struct {
	ID        string `json:"id"`
	PubKey    string `json:"pubkey"`
	CreatedAt int64  `json:"created_at"`
	RelayURL  string `json:"relay_url"`
	Source    string `json:"source"`
}

func (s *Server) debugFirehoseSamples(ctx context.Context, relays []string) ([]debugFirehoseSample, error) {
	seen := make(map[string]bool)
	out := make([]debugFirehoseSample, 0, 20)
	if s != nil && s.store != nil {
		stored, err := s.store.RecentByKinds(ctx, noteTimelineKinds, 0, time.Now().Unix()+1, "", 20)
		if err != nil {
			return nil, err
		}
		for _, event := range stored {
			if event.ID == "" || seen[event.ID] {
				continue
			}
			seen[event.ID] = true
			out = append(out, debugFirehoseSample{
				ID:        event.ID,
				PubKey:    event.PubKey,
				CreatedAt: event.CreatedAt,
				Source:    "store",
			})
		}
	}
	if s == nil || s.nostr == nil || len(out) >= 20 {
		return out, nil
	}
	events, err := s.nostr.FetchFrom(ctx, relays, nostrx.Query{
		Kinds: noteTimelineKinds,
		Limit: 20,
	})
	if err != nil {
		if len(out) > 0 {
			return out, nil
		}
		return nil, err
	}
	for _, event := range events {
		if event.ID == "" || seen[event.ID] {
			continue
		}
		seen[event.ID] = true
		out = append(out, debugFirehoseSample{
			ID:        event.ID,
			PubKey:    event.PubKey,
			CreatedAt: event.CreatedAt,
			RelayURL:  event.RelayURL,
			Source:    "relay",
		})
		if len(out) >= 20 {
			break
		}
	}
	return out, nil
}

func (s *Server) handleDebugFirehose(w http.ResponseWriter, r *http.Request) {
	relays := s.requestRelays(r)
	samples, err := s.debugFirehoseSamples(r.Context(), relays)
	writeJSON(w, map[string]any{
		"relays":      relays,
		"event_count": len(samples),
		"events":      samples,
		"metrics":     s.nostr.Metrics(),
	}, err)
}

// handleDebugSeedNote inserts a minimal text note into the local store for e2e
// and local debugging. POST only; optional ?id= hex event id (64 chars).
func (s *Server) handleDebugSeedNote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s == nil || s.store == nil {
		writeJSON(w, nil, httpError("store unavailable", http.StatusServiceUnavailable))
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		id = strings.Repeat("e", 64)
	} else {
		id = thread.NormalizeHexEventID(id)
		if !isBare64Hex(id) {
			writeJSON(w, nil, httpError("id must be 64 hex characters", http.StatusBadRequest))
			return
		}
	}
	pubkey := strings.TrimSpace(r.URL.Query().Get("pubkey"))
	if pubkey == "" {
		pubkey = strings.Repeat("f", 64)
	} else if decoded, err := nostrx.DecodeIdentifier(pubkey); err != nil || decoded == "" {
		writeJSON(w, nil, httpError("pubkey must be hex or npub", http.StatusBadRequest))
		return
	} else {
		pubkey = decoded
	}
	event := nostrx.Event{
		ID:        id,
		PubKey:    pubkey,
		CreatedAt: time.Now().Unix() - 60,
		Kind:      nostrx.KindTextNote,
		Content:   "e2e seeded note",
	}
	if err := s.store.SaveEvent(r.Context(), event); err != nil {
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, map[string]any{"id": id, "pubkey": pubkey}, nil)
}

func (s *Server) handleDebugEvent(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeJSON(w, nil, httpError("missing id", http.StatusBadRequest))
		return
	}
	event := s.eventByID(r.Context(), id, s.cfg.DefaultRelays)
	if event == nil {
		writeJSON(w, nil, httpError("event not found", http.StatusNotFound))
		return
	}
	relays, err := s.store.RelaySources(r.Context(), id)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, map[string]any{"event": event, "relays": relays}, nil)
}

func (s *Server) handleDebugProfile(w http.ResponseWriter, r *http.Request) {
	pubkey, err := nostrx.DecodeIdentifier(viewerFromRequest(r))
	if err != nil {
		writeJSON(w, nil, httpError(err.Error(), http.StatusBadRequest))
		return
	}
	s.refreshAuthor(r.Context(), pubkey, s.cfg.DefaultRelays)
	profile := s.profile(r.Context(), pubkey)
	following := s.following(r.Context(), pubkey, maxFeedAuthors)
	relayHints := s.userRelays(r.Context(), pubkey)
	writeJSON(w, map[string]any{
		"profile":         profile,
		"following_count": len(following),
		"relay_hints":     relayHints,
		"npub":            nostrx.EncodeNPub(pubkey),
	}, nil)
}
