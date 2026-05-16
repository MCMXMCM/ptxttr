package httpx

import (
	"net/http"
	"runtime"
	"strings"

	"ptxt-nstr/internal/nostrx"
)

func (s *Server) handleDebugCache(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.Stats(r.Context())
	writeJSON(w, stats, err)
}

func (s *Server) handleDebugMetrics(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"relay_queries": s.nostr.Metrics(),
		"app":           s.metrics.Snapshot(),
		"health":        s.healthSnapshot(),
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

func (s *Server) handleDebugFirehose(w http.ResponseWriter, r *http.Request) {
	relays := s.requestRelays(r)
	events, err := s.nostr.FetchFrom(r.Context(), relays, nostrx.Query{
		Kinds: noteTimelineKinds,
		Limit: 20,
	})
	type sample struct {
		ID        string `json:"id"`
		PubKey    string `json:"pubkey"`
		CreatedAt int64  `json:"created_at"`
		RelayURL  string `json:"relay_url"`
	}
	samples := make([]sample, 0, len(events))
	for _, event := range events {
		samples = append(samples, sample{ID: event.ID, PubKey: event.PubKey, CreatedAt: event.CreatedAt, RelayURL: event.RelayURL})
	}
	writeJSON(w, map[string]any{
		"relays":      relays,
		"event_count": len(events),
		"events":      samples,
		"metrics":     s.nostr.Metrics(),
	}, err)
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
