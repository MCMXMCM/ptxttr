package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

func healthProbeBaseURL(addr string) (string, bool) {
	addr = strings.TrimSpace(addr)
	if addr == "" || addr == ":0" {
		return "", false
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		if strings.HasPrefix(addr, ":") {
			return "http://127.0.0.1" + addr, true
		}
		return "", false
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	if host == "[::]" {
		host = "[::1]"
	}
	return "http://" + net.JoinHostPort(host, port), true
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.store == nil {
		http.Error(w, `{"ok":false,"error":"server not ready"}`, http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	dbErr := s.store.Ping(pingCtx)
	degraded := s.healthDegraded.Load()
	lastOK := s.healthLastOK.Load()
	lastMS := s.healthLastProbeMS.Load()
	relay := s.nostr.OutboundMetrics()

	body := map[string]any{
		"ok":                    dbErr == nil,
		"db_ok":                 dbErr == nil,
		"degraded":              degraded,
		"health_probe_last_ok":  lastOK,
		"health_probe_last_ms":  lastMS,
		"relay_outbound_slots":  relay.MaxSlots,
		"relay_outbound_waited": relay.AcquireWaited,
	}
	w.Header().Set("Content-Type", "application/json")
	if dbErr != nil {
		body["db_error"] = dbErr.Error()
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	_ = json.NewEncoder(w).Encode(body)
}

func (s *Server) runHealthProbeLoop(baseURL string) {
	if s == nil {
		return
	}
	path := strings.TrimSpace(s.cfg.HealthProbePath)
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	interval := s.cfg.HealthProbeInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	timeout := s.cfg.HealthProbeTimeout
	if timeout <= 0 {
		timeout = 12 * time.Second
	}
	threshold := s.cfg.HealthProbeDegradedThreshold
	if threshold <= 0 {
		threshold = 3
	}
	client := &http.Client{Transport: http.DefaultTransport}
	// First tick after `interval` so the HTTP listener is usually up (avoids
	// startup connection-refused noise when main starts the probe before Listen).
	next := time.NewTimer(interval)
	defer next.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-next.C:
			s.runOneHealthProbe(client, baseURL+path, timeout, threshold)
			next.Reset(interval)
		}
	}
}

func (s *Server) runOneHealthProbe(client *http.Client, fullURL string, timeout time.Duration, threshold int) {
	reqCtx, cancel := context.WithTimeout(s.ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, fullURL, nil)
	if err != nil {
		s.recordHealthProbeFailure(threshold, err)
		return
	}
	req.Header.Set("User-Agent", "ptxt-nstr-health-probe/1")
	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start)
	if err != nil {
		s.recordHealthProbeFailure(threshold, err)
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode >= http.StatusInternalServerError {
		s.recordHealthProbeFailure(threshold, fmt.Errorf("health probe status %s", resp.Status))
		return
	}
	s.healthProbeFails.Store(0)
	s.healthLastOK.Store(true)
	s.healthLastProbeMS.Store(latency.Milliseconds())
	s.healthDegraded.Store(false)
	if s.metrics != nil {
		s.metrics.Add("health.probe_ok", 1)
		s.metrics.Observe("health.probe_latency", latency)
	}
}

func (s *Server) recordHealthProbeFailure(threshold int, probeErr error) {
	fails := s.healthProbeFails.Add(1)
	s.healthLastOK.Store(false)
	if int(fails) >= threshold {
		s.healthDegraded.Store(true)
	}
	if s.metrics != nil {
		s.metrics.Add("health.probe_fail", 1)
	}
	slog.Warn("health self-probe failed", "fails", fails, "threshold", threshold, "err", probeErr)
}

func (s *Server) healthSnapshot() map[string]any {
	if s == nil {
		return nil
	}
	return map[string]any{
		"probe_last_ok":     s.healthLastOK.Load(),
		"probe_last_ms":     s.healthLastProbeMS.Load(),
		"degraded":          s.healthDegraded.Load(),
		"consecutive_fails": s.healthProbeFails.Load(),
	}
}
