package httpx

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ptxt-nstr/internal/config"
)

func TestHandleDesktopOpenExternalNotRegisteredOff(t *testing.T) {
	cfg := config.Config{DesktopMode: false}
	s := &Server{cfg: cfg}
	req := httptest.NewRequest(http.MethodPost, desktopOpenExternalPath, bytes.NewReader([]byte(`{"url":"https://example.com/"}`)))
	rec := httptest.NewRecorder()
	s.handleDesktopOpenExternal(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code=%d want 404", rec.Code)
	}
}

func TestHandleDesktopOpenExternalRejectsNonHTTP(t *testing.T) {
	cfg := config.Config{DesktopMode: true}
	s := &Server{cfg: cfg}
	req := httptest.NewRequest(http.MethodPost, desktopOpenExternalPath, bytes.NewReader([]byte(`{"url":"javascript:alert(1)"}`)))
	rec := httptest.NewRecorder()
	s.handleDesktopOpenExternal(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d want 400", rec.Code)
	}
}

func TestDesktopAppShellEnablesDirectRelayReads(t *testing.T) {
	s := &Server{cfg: config.Config{DesktopMode: true}}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	var payload struct {
		Features map[string]bool `json:"features"`
	}
	if err := json.Unmarshal([]byte(s.appShellBootstrapJSON(req, BasePageData{})), &payload); err != nil {
		t.Fatalf("decode app bootstrap: %v", err)
	}
	if !payload.Features["directRelayReads"] {
		t.Fatal("desktop app bootstrap directRelayReads = false, want true")
	}
	if !payload.Features["relayNativeRoutesPrimary"] {
		t.Fatal("desktop app bootstrap relayNativeRoutesPrimary = false, want true")
	}
}

func TestDesktopAllowsAnonymousThreadRelayFetch(t *testing.T) {
	s := &Server{cfg: config.Config{DesktopMode: true, ServerMode: "share"}}
	if !s.allowThreadRelayFetch("", true, "") {
		t.Fatal("desktop anonymous thread relay fetch = false, want true")
	}
}

func TestServerAppShellKeepsDirectRelayReadsDisabledByDefault(t *testing.T) {
	s := &Server{cfg: config.Config{}}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	var payload struct {
		Features map[string]bool `json:"features"`
	}
	if err := json.Unmarshal([]byte(s.appShellBootstrapJSON(req, BasePageData{})), &payload); err != nil {
		t.Fatalf("decode app bootstrap: %v", err)
	}
	if payload.Features["directRelayReads"] {
		t.Fatal("server app bootstrap directRelayReads = true, want false")
	}
	if payload.Features["relayNativeRoutesPrimary"] {
		t.Fatal("server app bootstrap relayNativeRoutesPrimary = true, want false")
	}
}

func TestDesktopStorageEndpointsBypassRequestTimeout(t *testing.T) {
	for _, path := range []string{desktopStoragePath, desktopStorageClearPath} {
		t.Run(path, func(t *testing.T) {
			inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-time.After(30 * time.Millisecond):
					w.WriteHeader(http.StatusNoContent)
				case <-r.Context().Done():
					t.Fatalf("desktop storage request context was unexpectedly cancelled: %v", r.Context().Err())
				}
			})
			handler := withTimeout(5*time.Millisecond, inner)
			req := httptest.NewRequest(http.MethodGet, path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204", rec.Code)
			}
		})
	}
}
