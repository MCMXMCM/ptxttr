package httpx

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

func TestFetchNIP05DocumentVerifiesPubkey(t *testing.T) {
	pubkey := strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/nostr.json" || r.URL.Query().Get("name") != "matt" {
			t.Fatalf("unexpected request URL: %s", r.URL.String())
		}
		_, _ = fmt.Fprintf(w, `{"names":{"matt":"%s"}}`, pubkey)
	}))
	defer server.Close()

	doc, status := fetchNIP05Document(context.Background(), server.Client(), server.URL+"/.well-known/nostr.json?name=matt")
	if status != "" {
		t.Fatalf("status = %q, want empty fetch status", status)
	}
	if got := nostrx.VerifyNIP05Document(doc, "matt", pubkey); got != nostrx.NIP05Verified {
		t.Fatalf("verification status = %q, want verified", got)
	}
}

func TestFetchNIP05DocumentRejectsRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	defer server.Close()
	client := server.Client()
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	_, status := fetchNIP05Document(context.Background(), client, server.URL+"/.well-known/nostr.json?name=matt")
	if status != nostrx.NIP05Unreachable {
		t.Fatalf("status = %q, want unreachable for non-2xx redirect response", status)
	}
}

func TestVerifyNIP05CachedUsesServerLookupAndCache(t *testing.T) {
	pubkey := strings.Repeat("b", 64)
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/.well-known/nostr.json" || r.URL.Query().Get("name") != "matt" {
			t.Fatalf("unexpected request URL: %s", r.URL.String())
		}
		_, _ = fmt.Fprintf(w, `{"names":{"matt":"%s"}}`, pubkey)
	}))
	defer server.Close()

	originalTransport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	t.Cleanup(func() {
		http.DefaultTransport = originalTransport
	})

	srv, _ := testServer(t)
	identifier := "matt@" + strings.TrimPrefix(server.URL, "https://")
	if got := srv.verifyNIP05Cached(context.Background(), identifier, pubkey); got != nostrx.NIP05Verified {
		t.Fatalf("first status = %q, want verified", got)
	}
	if got := srv.verifyNIP05Cached(context.Background(), identifier, pubkey); got != nostrx.NIP05Verified {
		t.Fatalf("cached status = %q, want verified", got)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}
