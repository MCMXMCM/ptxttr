package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ptxt-nstr/internal/config"
	"ptxt-nstr/internal/nostrx"
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
	cfg := config.Config{DesktopMode: true, DesktopSessionToken: "launch-token"}
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

func TestHandleDesktopActivityAcceptsForegroundReducedAndPausedModes(t *testing.T) {
	s := &Server{cfg: config.Config{DesktopMode: true, DesktopSessionToken: "launch-token"}}
	s.setDesktopBackgroundMode(desktopBackgroundForeground)
	for _, tc := range []struct {
		mode   string
		want   desktopBackgroundMode
		active bool
	}{
		{mode: "reduced", want: desktopBackgroundReduced, active: true},
		{mode: "paused", want: desktopBackgroundPaused, active: false},
		{mode: "foreground", want: desktopBackgroundForeground, active: true},
	} {
		req := httptest.NewRequest(http.MethodPost, desktopActivityPath, bytes.NewBufferString(`{"mode":"`+tc.mode+`"}`))
		req.Header.Set("X-Ptxt-Desktop-Token", "launch-token")
		rec := httptest.NewRecorder()
		s.handleDesktopActivity(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("mode %s status = %d, want 204", tc.mode, rec.Code)
		}
		if got := s.desktopBackgroundMode(); got != tc.want {
			t.Fatalf("mode %s stored as %v, want %v", tc.mode, got, tc.want)
		}
		if got := s.backgroundActive.Load(); got != tc.active {
			t.Fatalf("mode %s active = %v, want %v", tc.mode, got, tc.active)
		}
	}
}

func TestDesktopAppShellKeepsTheSidecarAuthoritative(t *testing.T) {
	s := &Server{cfg: config.Config{DesktopMode: true}}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	var payload struct {
		Features map[string]bool `json:"features"`
	}
	if err := json.Unmarshal([]byte(s.appShellBootstrapJSON(req, BasePageData{})), &payload); err != nil {
		t.Fatalf("decode app bootstrap: %v", err)
	}
	if payload.Features["directRelayReads"] {
		t.Fatal("desktop app bootstrap directRelayReads = true, want false")
	}
	if payload.Features["relayNativeRoutesPrimary"] {
		t.Fatal("desktop app bootstrap relayNativeRoutesPrimary = true, want false")
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

func TestDesktopRelayQueryAcceptsOnlyBoundedConstrainedFilters(t *testing.T) {
	query, err := desktopRelayQuery(desktopRelayFilter{
		Authors: []string{strings.Repeat("a", 64)},
		Kinds:   []int{nostrx.KindTextNote, nostrx.KindTextNote},
		ETags:   []string{strings.Repeat("b", 64)},
		Limit:   500,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(query.Authors) != 1 || len(query.Kinds) != 1 || len(query.Tags["e"]) != 1 {
		t.Fatalf("unexpected normalized query: %#v", query)
	}
	if query.Limit != nostrx.MaxRelayQueryLimit {
		t.Fatalf("query limit = %d, want clamped maximum %d", query.Limit, nostrx.MaxRelayQueryLimit)
	}
	if _, err := desktopRelayQuery(desktopRelayFilter{Limit: 10}); err == nil {
		t.Fatal("unconstrained desktop relay query was accepted")
	}
}

func TestDesktopAllowsAnonymousThreadRelayFetch(t *testing.T) {
	s := &Server{cfg: config.Config{DesktopMode: true, ServerMode: "share"}}
	if !s.allowThreadRelayFetch("", true, "") {
		t.Fatal("desktop anonymous thread relay fetch = false, want true")
	}
}

func TestDesktopAppShellDisablesSharedCacheHeaders(t *testing.T) {
	s := &Server{cfg: config.Config{DesktopMode: true}}
	rec := httptest.NewRecorder()
	s.setAppShellCache(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q, want private, no-store", got)
	}
}

func TestHandleDesktopReplaceableUsesSQLiteAsTheRendererSource(t *testing.T) {
	srv, st := testServer(t)
	srv.cfg.DesktopMode = true
	srv.nostr = nil
	pubkey := strings.Repeat("a", 64)
	event := nostrx.Event{
		ID:        strings.Repeat("b", 64),
		PubKey:    pubkey,
		CreatedAt: 123,
		Kind:      nostrx.KindBookmarkList,
		Tags:      [][]string{{"e", strings.Repeat("c", 64)}},
	}
	if err := st.SaveEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, desktopReplaceablePath+"?pubkey="+pubkey+"&kind=10003", nil)
	rec := httptest.NewRecorder()

	srv.handleDesktopReplaceable(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%q want 200", rec.Code, rec.Body.String())
	}
	var got nostrx.Event
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != event.ID || got.PubKey != pubkey || got.Kind != nostrx.KindBookmarkList {
		t.Fatalf("event = %#v, want %#v", got, event)
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
