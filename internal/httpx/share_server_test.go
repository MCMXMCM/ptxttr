package httpx

import (
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ptxt-nstr/internal/config"
	"ptxt-nstr/internal/nostrx"

	fnostr "fiatjaf.com/nostr"
)

func TestShareServerModeDisablesWarmQueue(t *testing.T) {
	srv, _ := newTestServer(t, testServerOptions{serverMode: config.ServerModeShare})
	if srv.warmer != nil {
		t.Fatal("warmer != nil in share server mode")
	}
}

func TestShareServerModeSignedInThreadIsStoreOnly(t *testing.T) {
	srv, _ := newTestServer(t, testServerOptions{serverMode: config.ServerModeShare})
	event := signedMutationEvent(t, 1, "relay only thread note", nil)

	req := httptest.NewRequest(http.MethodGet, "/thread/"+event.ID, nil)
	req.Header.Set("X-Ptxt-Viewer", strings.Repeat("a", 64))
	req.Header.Set("User-Agent", "Twitterbot/1.0")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-route-outlet="root"`) {
		t.Fatalf("expected shell-only thread document, got %s", body)
	}
	if strings.Contains(body, "Note not found") {
		t.Fatalf("expected share-mode thread request to avoid server miss rendering, got %s", body)
	}
}

func TestShareServerModeAnonymousThreadMissStaysCheap(t *testing.T) {
	srv, _ := newTestServer(t, testServerOptions{serverMode: config.ServerModeShare})
	event := signedMutationEvent(t, 1, "relay only anonymous thread note", nil)

	req := httptest.NewRequest(http.MethodGet, "/thread/"+event.ID, nil)
	req.Header.Set("User-Agent", "Twitterbot/1.0")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Note not found") {
		t.Fatalf("expected anonymous cache miss to render not found, got %s", body)
	}
}

func TestShareServerModeOGMissStaysCacheOnly(t *testing.T) {
	srv, st := newTestServer(t, testServerOptions{serverMode: config.ServerModeShare})
	ctx := context.Background()
	event := signedMutationEvent(t, 1, "relay only og note", nil)
	wire := nostrxEventToFnostrEvent(t, event)
	relay := newTestRelayREQEventWhenIDsContain(ctx, event.ID, wire)
	defer relay.Close()
	srv.cfg.DefaultRelays = []string{wsURL(relay.URL)}

	req := httptest.NewRequest(http.MethodGet, "/og/"+event.ID+".png", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if cached := srv.eventFromStore(ctx, event.ID); cached == nil {
		if stats, err := st.Stats(ctx); err == nil && stats.Events != 0 {
			t.Fatalf("expected og miss to stay cache-only, stats=%+v", stats)
		}
	}
}

func TestShareServerModeFeedDocumentDoesNotServerRenderNotes(t *testing.T) {
	srv, st := newTestServer(t, testServerOptions{serverMode: config.ServerModeShare})
	ctx := context.Background()
	note := signedMutationEvent(t, nostrx.KindTextNote, "server rendered feed note", nil)
	if err := st.SaveEvent(ctx, note); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/feed", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "server rendered feed note") {
		t.Fatalf("expected share-mode feed document to stay shell-only, got %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `data-feed`) || !strings.Contains(rec.Body.String(), `data-feed-loader`) {
		t.Fatalf("expected server-rendered empty feed shell, got %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `<script id="ptxt-route-context" type="application/json">"{`) {
		t.Fatalf("expected raw JSON route context, got %s", rec.Body.String())
	}
}

func TestShareServerModeSearchDocumentOmitsLegacyCacheCopy(t *testing.T) {
	srv, _ := newTestServer(t, testServerOptions{serverMode: config.ServerModeShare})

	req := httptest.NewRequest(http.MethodGet, "/search?q=nostr", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "Best effort search from this server's local event cache") {
		t.Fatalf("unexpected legacy cache search copy in share mode: %s", body)
	}
	if !strings.Contains(body, `data-search-results`) {
		t.Fatalf("expected server-rendered search results container, got %s", body)
	}
}

func TestShareServerModeLoginDocumentIncludesStubRouteContext(t *testing.T) {
	srv, _ := newTestServer(t, testServerOptions{serverMode: config.ServerModeShare})

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "\"path\":\"/login\"") {
		t.Fatalf("expected login path route context, got %s", body)
	}
	if !strings.Contains(body, "\"route\":\"stub\"") {
		t.Fatalf("expected login to map to stub route context, got %s", body)
	}
}

func TestShareServerModeRelaysDocumentStaysShellOnly(t *testing.T) {
	srv, st := newTestServer(t, testServerOptions{serverMode: config.ServerModeShare})
	ctx := context.Background()
	secret := fnostr.Generate()
	relayList := signNostrEvent(t, secret, nostrx.KindRelayListMetadata, "", [][]string{
		{"r", "wss://hint.example"},
	})
	if err := st.SaveEvent(ctx, relayList); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRelayStatus(ctx, "wss://relay.primal.net", true, ""); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/relays?pubkey="+relayList.PubKey, nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "wss://hint.example") {
		t.Fatalf("expected share-mode relays document to omit suggested relays, got %s", body)
	}
	if strings.Contains(body, "status: ok") {
		t.Fatalf("expected share-mode relays document to omit relay status copy, got %s", body)
	}
	if !strings.Contains(body, "\"route\":\"relays\"") {
		t.Fatalf("expected app-shell relays route context, got %s", body)
	}
}

func nostrxEventToFnostrEvent(t *testing.T, event nostrx.Event) fnostr.Event {
	t.Helper()
	id, err := fnostr.IDFromHex(event.ID)
	if err != nil {
		t.Fatalf("IDFromHex() error = %v", err)
	}
	pubkey, err := fnostr.PubKeyFromHex(event.PubKey)
	if err != nil {
		t.Fatalf("PubKeyFromHex() error = %v", err)
	}
	sigBytes, err := hex.DecodeString(event.Sig)
	if err != nil || len(sigBytes) != 64 {
		t.Fatalf("DecodeString(sig) error = %v len=%d", err, len(sigBytes))
	}
	var sig [64]byte
	copy(sig[:], sigBytes)
	tags := make(fnostr.Tags, 0, len(event.Tags))
	for _, tag := range event.Tags {
		tags = append(tags, fnostr.Tag(tag))
	}
	return fnostr.Event{
		ID:        id,
		PubKey:    pubkey,
		CreatedAt: fnostr.Timestamp(event.CreatedAt),
		Kind:      fnostr.Kind(event.Kind),
		Tags:      tags,
		Content:   event.Content,
		Sig:       sig,
	}
}
