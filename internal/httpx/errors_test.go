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
		{"/reads/" + strings.Repeat("0", 64), "Read not found"},
		{"/thread/" + strings.Repeat("f", 64), "Note not found"},
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

func truncateBody(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
