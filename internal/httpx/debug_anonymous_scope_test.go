package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDebugSeedNoteRemainsReachableThroughAnonymousScope(t *testing.T) {
	srv, _ := testServer(t)
	srv.cfg.Debug = true

	pubkey := strings.Repeat("a", 64)
	noteID := strings.Repeat("b", 64)
	seed := httptest.NewRecorder()
	srv.Handler().ServeHTTP(seed, httptest.NewRequest(
		http.MethodPost,
		"/debug/seed-note?id="+noteID+"&pubkey="+pubkey,
		nil,
	))
	if seed.Code != http.StatusOK {
		t.Fatalf("seed status = %d, want 200: %s", seed.Code, seed.Body.String())
	}

	for _, path := range []string{"/thread/" + noteID + "?fragment=hydrate", "/u/" + pubkey} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200: %s", path, rec.Code, rec.Body.String())
		}
	}
}
