package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"ptxt-nstr/internal/config"
)

func TestDesktopLoopbackAuthRequiresLaunchToken(t *testing.T) {
	s := &Server{cfg: config.Config{DesktopMode: true, DesktopSessionToken: "launch-token"}}
	handler := s.desktopLoopbackAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, tc := range []struct {
		name    string
		request *http.Request
		want    int
	}{
		{name: "missing", request: httptest.NewRequest(http.MethodGet, "/feed", nil), want: http.StatusForbidden},
		{name: "wrong cookie", request: httptest.NewRequest(http.MethodGet, "/feed", nil), want: http.StatusForbidden},
		{name: "health is public", request: httptest.NewRequest(http.MethodGet, "/healthz", nil), want: http.StatusNoContent},
		{name: "valid cookie", request: httptest.NewRequest(http.MethodGet, "/feed", nil), want: http.StatusNoContent},
		{name: "main process header", request: httptest.NewRequest(http.MethodPost, "/__ptxt/desktop/activity", nil), want: http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			switch tc.name {
			case "wrong cookie":
				tc.request.AddCookie(&http.Cookie{Name: desktopSessionCookie, Value: "wrong"})
			case "valid cookie":
				tc.request.AddCookie(&http.Cookie{Name: desktopSessionCookie, Value: "launch-token"})
			case "main process header":
				tc.request.Header.Set("X-Ptxt-Desktop-Token", "launch-token")
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, tc.request)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestDesktopLoopbackAuthWithoutConfiguredTokenIsTestFriendly(t *testing.T) {
	s := &Server{cfg: config.Config{DesktopMode: true}}
	handler := s.desktopLoopbackAuth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/feed", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}
