package httpx

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestThreadTelemetrySSEHeadersAndFlush(t *testing.T) {
	srv, _ := testServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/thread-telemetry?id=testthread01", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	if got := resp.Header.Get("Cache-Control"); !strings.Contains(got, "no-transform") || !strings.Contains(got, "no-store") {
		t.Fatalf("Cache-Control = %q, want no-store and no-transform", got)
	}
	if got := resp.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Fatalf("X-Accel-Buffering = %q, want no", got)
	}

	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if line != ": connected\n" {
		t.Fatalf("first flushed line = %q, want connected comment", line)
	}

	srv.publishThreadTelemetry("testthread01", "relay_fetch", "asking relays", 18)
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for streamed status event")
		default:
		}
		line, err = reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "data: ") {
			if !strings.Contains(line, `"message":"asking relays"`) {
				t.Fatalf("data line = %q, want asking relays payload", line)
			}
			return
		}
	}
}

func TestThreadTelemetryReplaysEarlyEvents(t *testing.T) {
	srv, _ := testServer(t)
	srv.publishThreadTelemetry("testthread02", "cache", "checking cache", 10)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/thread-telemetry?id=testthread02", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	deadline := time.After(500 * time.Millisecond)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for replayed status event")
		default:
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "data: ") {
			if !strings.Contains(line, `"message":"checking cache"`) {
				t.Fatalf("data line = %q, want checking cache payload", line)
			}
			return
		}
	}
}

func TestThreadTelemetryBypassesRequestTimeout(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(30 * time.Millisecond):
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
			t.Fatalf("telemetry request context was unexpectedly cancelled: %v", r.Context().Err())
		}
	})
	handler := withTimeout(5*time.Millisecond, inner)
	req := httptest.NewRequest(http.MethodGet, "/api/thread-telemetry?id=testthread03", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rr.Code)
	}
}
