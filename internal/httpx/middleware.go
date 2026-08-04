package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

type statusError struct {
	message string
	status  int
}

func (e statusError) Error() string {
	return e.message
}

func httpError(message string, status int) error {
	return statusError{message: message, status: status}
}

func writeJSON(w http.ResponseWriter, value any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		status := http.StatusInternalServerError
		var typed statusError
		if errors.As(err, &typed) {
			status = typed.status
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.wroteHeader = true
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(body []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(body)
}

func (r *statusRecorder) Flush() {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

var requestSequence atomic.Uint64

func requestCorrelationID(r *http.Request) string {
	if r != nil {
		for _, header := range []string{"X-Ptxt-Request-ID", "X-Request-ID"} {
			if id := strings.TrimSpace(r.Header.Get(header)); id != "" && len(id) <= 80 {
				return id
			}
		}
		if id := threadTelemetryIDFromRequest(r); id != "" {
			return id
		}
	}
	return fmt.Sprintf("r%x-%x", time.Now().UnixMilli(), requestSequence.Add(1))
}

func requestVariant(r *http.Request) string {
	if r == nil || r.URL == nil {
		return "unknown"
	}
	if fragment := strings.TrimSpace(r.URL.Query().Get("fragment")); fragment != "" {
		return "fragment:" + fragment
	}
	if strings.HasPrefix(r.URL.Path, "/thread/") || strings.HasPrefix(r.URL.Path, "/u/") {
		return "document"
	}
	return "request"
}

func logging(server *Server, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := requestCorrelationID(r)
		w.Header().Set("X-Ptxt-Request-ID", requestID)
		if server != nil {
			server.markRequestStart(start)
			defer server.markRequestDone()
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if server != nil && server.metrics != nil {
			if errors.Is(r.Context().Err(), context.DeadlineExceeded) {
				server.metrics.Add("request.deadline_exceeded", 1)
				slog.Warn("request context deadline exceeded", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
			}
		}
		noisyPath := strings.HasPrefix(r.URL.Path, "/static/") || strings.HasPrefix(r.URL.Path, avatarPathPrefix)
		noisyStatus := rec.status == http.StatusNotModified
		noisy := noisyPath || noisyStatus
		if noisy && (server == nil || !server.cfg.Debug) {
			return
		}
		logFn := slog.Info
		if noisy {
			logFn = slog.Debug
		}
		logFn(
			"request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration", time.Since(start),
			"request_id", requestID,
			"variant", requestVariant(r),
			"anonymous", strings.TrimSpace(r.Header.Get(headerViewerPubkey)) == "",
		)
	})
}

func withTimeout(timeout time.Duration, next http.Handler) http.Handler {
	if timeout <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r != nil && r.URL != nil {
			switch r.URL.Path {
			case "/api/thread-telemetry", desktopStoragePath, desktopStorageClearPath, desktopFollowGraphPath,
				desktopReplaceablePath, desktopRelayFetchPath:
				next.ServeHTTP(w, r)
				return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requestTimeout(relayTimeout time.Duration) time.Duration {
	if relayTimeout <= 0 {
		return 0
	}
	timeout := relayTimeout + 2*time.Second
	if timeout < 5*time.Second {
		return 5 * time.Second
	}
	return timeout
}
