package httpx

import (
	"encoding/json"
	"net/http"
	"strings"

	"ptxt-nstr/internal/nostrx"
)

const threadWarmMaxBody = 2 << 10

type threadWarmRequest struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

func (s *Server) handleThreadWarm(w http.ResponseWriter, r *http.Request) {
	if s == nil || !s.runtimeCapabilities().DesktopShell || s.store == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	viewer := normalizedViewerPubkey(viewerFromRequest(r))
	if viewer == "" {
		http.Error(w, "viewer required", http.StatusUnauthorized)
		return
	}
	if !s.allowPublicAPIRequest(w, r, "thread-warm", viewer) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, threadWarmMaxBody)
	var body threadWarmRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	body.ID = nostrx.CanonicalHex64(body.ID)
	body.Reason = strings.ToLower(strings.TrimSpace(body.Reason))
	if !nostrx.IsValidPubKeyHex(body.ID) {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	switch body.Reason {
	case "hover", "pointer", "focus":
	default:
		http.Error(w, "invalid reason", http.StatusBadRequest)
		return
	}
	if s.eventFromStore(r.Context(), body.ID) == nil {
		http.Error(w, "note not found", http.StatusNotFound)
		return
	}
	state, _ := s.threadProjectionStatus(r.Context(), body.ID)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Ptxt-Thread-Cache", string(state))
	if state == threadProjectionReady {
		s.metrics.Add("thread.intent.ready", 1)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !s.enqueueInteractiveThreadMaterialization(viewer, body.ID, s.requestRelays(r)) {
		s.metrics.Add("thread.intent.saturated", 1)
		w.Header().Set("Retry-After", "1")
		http.Error(w, "thread warm queue unavailable", http.StatusServiceUnavailable)
		return
	}
	s.metrics.Add("thread.intent.enqueued", 1)
	w.WriteHeader(http.StatusAccepted)
}
