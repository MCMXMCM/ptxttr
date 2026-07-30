package httpx

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"time"
)

const threadTelemetryHeader = "X-Ptxt-Thread-Request"

var threadTelemetryIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,80}$`)

type threadTelemetryEvent struct {
	Stage   string `json:"stage"`
	Message string `json:"message"`
	Percent int    `json:"percent,omitempty"`
	Done    bool   `json:"done,omitempty"`
	At      int64  `json:"at"`
}

type threadTelemetryHub struct {
	mu          sync.Mutex
	subscribers map[string]map[chan threadTelemetryEvent]struct{}
	recent      map[string][]threadTelemetryEvent
	recentAt    map[string]time.Time
}

func newThreadTelemetryHub() *threadTelemetryHub {
	return &threadTelemetryHub{
		subscribers: make(map[string]map[chan threadTelemetryEvent]struct{}),
		recent:      make(map[string][]threadTelemetryEvent),
		recentAt:    make(map[string]time.Time),
	}
}

func validThreadTelemetryID(id string) bool {
	return threadTelemetryIDPattern.MatchString(id)
}

func threadTelemetryIDFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	id := r.Header.Get(threadTelemetryHeader)
	if id == "" {
		id = r.URL.Query().Get("telemetry")
	}
	if !validThreadTelemetryID(id) {
		return ""
	}
	return id
}

func (h *threadTelemetryHub) subscribe(id string) (<-chan threadTelemetryEvent, func(), bool) {
	if h == nil || !validThreadTelemetryID(id) {
		return nil, func() {}, false
	}
	ch := make(chan threadTelemetryEvent, 16)
	h.mu.Lock()
	if h.subscribers[id] == nil {
		h.subscribers[id] = make(map[chan threadTelemetryEvent]struct{})
	}
	h.subscribers[id][ch] = struct{}{}
	replay := append([]threadTelemetryEvent(nil), h.recent[id]...)
	h.mu.Unlock()
	for _, event := range replay {
		ch <- event
	}
	unsubscribe := func() {
		h.mu.Lock()
		if subs := h.subscribers[id]; subs != nil {
			delete(subs, ch)
			if len(subs) == 0 {
				delete(h.subscribers, id)
			}
		}
		h.mu.Unlock()
		close(ch)
	}
	return ch, unsubscribe, true
}

func (h *threadTelemetryHub) publish(id, stage, message string, percent int, done bool) {
	if h == nil || !validThreadTelemetryID(id) || message == "" {
		return
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	event := threadTelemetryEvent{
		Stage:   stage,
		Message: message,
		Percent: percent,
		Done:    done,
		At:      time.Now().UnixMilli(),
	}
	h.mu.Lock()
	now := time.Now()
	h.pruneLocked(now)
	h.recent[id] = append(h.recent[id], event)
	if len(h.recent[id]) > 16 {
		h.recent[id] = h.recent[id][len(h.recent[id])-16:]
	}
	h.recentAt[id] = now
	subs := h.subscribers[id]
	for ch := range subs {
		select {
		case ch <- event:
		default:
		}
	}
	h.mu.Unlock()
}

func (h *threadTelemetryHub) pruneLocked(now time.Time) {
	for id, at := range h.recentAt {
		if now.Sub(at) <= 2*time.Minute {
			continue
		}
		delete(h.recentAt, id)
		delete(h.recent, id)
	}
	if len(h.recentAt) <= 256 {
		return
	}
	for id := range h.recentAt {
		delete(h.recentAt, id)
		delete(h.recent, id)
		if len(h.recentAt) <= 192 {
			return
		}
	}
}

func (s *Server) publishThreadTelemetry(id, stage, message string, percent int) {
	if s == nil || s.threadTelemetry == nil {
		return
	}
	s.threadTelemetry.publish(id, stage, message, percent, false)
	if s.metrics != nil {
		s.metrics.Add("thread.telemetry.publish", 1)
	}
}

func (s *Server) completeThreadTelemetry(id, message string) {
	if s == nil || s.threadTelemetry == nil {
		return
	}
	s.threadTelemetry.publish(id, "done", message, 100, true)
}

func (s *Server) handleThreadTelemetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := r.URL.Query().Get("id")
	if !validThreadTelemetryID(id) {
		http.Error(w, "invalid telemetry id", http.StatusBadRequest)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	events, unsubscribe, ok := s.threadTelemetry.subscribe(id)
	if !ok {
		http.Error(w, "invalid telemetry id", http.StatusBadRequest)
		return
	}
	defer unsubscribe()

	header := w.Header()
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-cache, no-store, no-transform")
	header.Set("Connection", "keep-alive")
	header.Set("X-Accel-Buffering", "no")
	header.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(": connected\n\n"))
	flusher.Flush()
	if s.metrics != nil {
		s.metrics.Add("thread.telemetry.connect", 1)
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			_, _ = w.Write([]byte(": keep-alive\n\n"))
			flusher.Flush()
		case event, ok := <-events:
			if !ok {
				return
			}
			payload, err := json.Marshal(event)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "event: status\ndata: %s\n\n", payload)
			flusher.Flush()
			if event.Done {
				return
			}
		}
	}
}
