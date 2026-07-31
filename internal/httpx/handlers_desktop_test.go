package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ptxt-nstr/internal/config"
	"ptxt-nstr/internal/store"
)

func TestHandleDesktopActivityNotRegisteredOff(t *testing.T) {
	cfg := config.Config{DesktopMode: false}
	s := &Server{cfg: cfg}
	req := httptest.NewRequest(http.MethodPost, desktopActivityPath, bytes.NewReader([]byte(`{"active":false}`)))
	rec := httptest.NewRecorder()
	s.handleDesktopActivity(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code=%d want 404", rec.Code)
	}
}

func TestHandleDesktopActivityRequiresLaunchToken(t *testing.T) {
	cfg := config.Config{DesktopMode: true, DesktopActivityToken: "launch-token"}
	s := &Server{cfg: cfg}
	s.backgroundActive.Store(true)
	req := httptest.NewRequest(http.MethodPost, desktopActivityPath, bytes.NewReader([]byte(`{"active":false}`)))
	rec := httptest.NewRecorder()
	s.handleDesktopActivity(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("code=%d want 403", rec.Code)
	}
	if !s.backgroundActive.Load() {
		t.Fatal("unauthorized request changed background activity")
	}

	req = httptest.NewRequest(http.MethodPost, desktopActivityPath, bytes.NewReader([]byte(`{"active":false}`)))
	req.Header.Set("X-Ptxt-Desktop-Token", "launch-token")
	rec = httptest.NewRecorder()
	s.handleDesktopActivity(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("code=%d want 204", rec.Code)
	}
	if s.backgroundActive.Load() {
		t.Fatal("authorized activity update did not pause background work")
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
	for _, feature := range []string{"localFirst", "desktopShell", "storageControls"} {
		if !payload.Features[feature] {
			t.Fatalf("desktop app bootstrap %s = false, want true", feature)
		}
	}
	if payload.Features["browserExtensionSigner"] || payload.Features["hostedGuestAdmission"] {
		t.Fatal("desktop bootstrap enabled a hosted/browser-only capability")
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

func TestHandleDesktopStorageUpdatesPersistentLRUCacheLimit(t *testing.T) {
	srv, st := testServer(t)
	srv.cfg.DesktopMode = true
	const maxBytes = int64(3 * 1024 * 1024 * 1024)
	req := httptest.NewRequest(http.MethodPut, desktopStoragePath, bytes.NewReader([]byte(`{"max_bytes":3221225472}`)))
	rec := httptest.NewRecorder()

	srv.handleDesktopStorage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%q want 200", rec.Code, rec.Body.String())
	}
	var usage store.CacheUsage
	if err := json.Unmarshal(rec.Body.Bytes(), &usage); err != nil {
		t.Fatal(err)
	}
	if usage.MaxBytes != maxBytes || usage.TargetBytes != maxBytes*9/10 {
		t.Fatalf("cache policy = (%d, %d), want (%d, %d)", usage.MaxBytes, usage.TargetBytes, maxBytes, maxBytes*9/10)
	}
	value, ok, err := st.AppMeta(context.Background(), store.AppMetaKeyCacheMaxBytes)
	if err != nil || !ok || value != "3221225472" {
		t.Fatalf("saved cache preference = %q, %v, %v", value, ok, err)
	}
}

func TestHandleDesktopStorageRejectsTooSmallCacheLimit(t *testing.T) {
	srv, _ := testServer(t)
	srv.cfg.DesktopMode = true
	req := httptest.NewRequest(http.MethodPut, desktopStoragePath, bytes.NewReader([]byte(`{"max_bytes":1024}`)))
	rec := httptest.NewRecorder()

	srv.handleDesktopStorage(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code=%d want 400", rec.Code)
	}
}
