package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRouteNotFoundPagesReturn404(t *testing.T) {
	srv, _ := testServer(t)
	cases := []struct {
		path        string
		wantSnippet string
	}{
		{"/definitely-not-a-route", "Page not found"},
		{"/u/not-valid-npub", "User not found"},
		{"/reads/not-a-hex-note-id/extra", "That read URL is not valid"},
		{"/e/", "Missing note id"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
			body := rec.Body.String()
			if !strings.Contains(body, `class="route-error-panel"`) {
				t.Fatalf("missing route-error-panel in body")
			}
			if !strings.Contains(body, tc.wantSnippet) {
				t.Fatalf("body missing %q: %s", tc.wantSnippet, truncateBody(body, 800))
			}
		})
	}
}

func TestReadAndThreadHumanRoutesReturnServerDocuments(t *testing.T) {
	srv, _ := testServer(t)
	cases := []struct {
		path       string
		statusCode int
		snippet    string
		loginHint  bool
	}{
		{"/reads/" + strings.Repeat("0", 64), http.StatusNotFound, "Read not found", false},
		{"/thread/" + strings.Repeat("f", 64), http.StatusNotFound, "Note not found", true},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.statusCode {
				t.Fatalf("status = %d, want %d", rec.Code, tc.statusCode)
			}
			body := rec.Body.String()
			if !strings.Contains(body, tc.snippet) {
				t.Fatalf("body missing %q: %s", tc.snippet, truncateBody(body, 800))
			}
			if tc.statusCode == http.StatusOK && strings.Contains(body, `class="route-error-panel"`) {
				t.Fatalf("unexpected route-error-panel in app shell body")
			}
			hasLoginHint := strings.Contains(body, `href="/login" data-relay-aware>Login</a> to look for notes using your own Web of Trust.`)
			if hasLoginHint != tc.loginHint {
				t.Fatalf("login hint present = %t, want %t: %s", hasLoginHint, tc.loginHint, truncateBody(body, 800))
			}
		})
	}
}

func truncateBody(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
