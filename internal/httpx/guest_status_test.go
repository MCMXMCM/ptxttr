package httpx

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

func publishTestGuestSlice(t *testing.T, st *store.Store, author, nip05 string, generation int64) nostrx.Event {
	t.Helper()
	ctx := context.Background()
	profile := nostrx.Event{
		ID:        testEventID("guest-profile", author),
		PubKey:    author,
		CreatedAt: 100,
		Kind:      nostrx.KindProfileMetadata,
		Content:   fmt.Sprintf(`{"name":"Guest Profile","nip05":%q}`, nip05),
		Sig:       strings.Repeat("1", 128),
	}
	note := nostrx.Event{
		ID:        testEventID("guest-note", author),
		PubKey:    author,
		CreatedAt: 101,
		Kind:      nostrx.KindTextNote,
		Content:   "complete cached note",
		Sig:       strings.Repeat("2", 128),
	}
	for _, event := range []nostrx.Event{profile, note} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.MarkHydrationAttempt(ctx, "noteReplies", note.ID, true, time.Hour); err != nil {
		t.Fatal(err)
	}
	_, err := st.PublishGuestSlice(ctx, store.GuestSliceState{
		Key: store.GuestSliceDefaultKey, Generation: generation, SeedPubKey: author,
		Cohort: []string{author}, Trust: []string{author},
	}, []store.GuestSliceMember{{
		PubKey: author, Role: store.GuestSliceRoleCohort,
		MetadataCheckedAt: 100, MetadataFound: true,
	}}, &store.DefaultSeedGuestFeedSnapshot{Feed: []nostrx.Event{note}}, time.Hour)
	if err != nil {
		t.Fatalf("publish guest slice: %v", err)
	}
	return note
}

func TestGuestFeedStatusETagAndDocumentGenerationHeaders(t *testing.T) {
	srv, st := testServer(t)
	srv.cfg.GuestSliceV2Enabled = true
	author := strings.Repeat("a", 64)
	note := publishTestGuestSlice(t, st, author, "nobody@example.invalid", 7)

	req := httptest.NewRequest(http.MethodGet, "/api/guest-feed-status", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status endpoint code = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"generation":7`) || !strings.Contains(rec.Body.String(), note.ID) {
		t.Fatalf("unexpected status body: %s", rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "s-maxage=60") {
		t.Fatalf("status cache-control = %q", got)
	}
	etag := rec.Header().Get("ETag")
	conditional := httptest.NewRequest(http.MethodGet, "/api/guest-feed-status", nil)
	conditional.Header.Set("If-None-Match", etag)
	conditionalRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(conditionalRec, conditional)
	if conditionalRec.Code != http.StatusNotModified {
		t.Fatalf("conditional status = %d, want 304", conditionalRec.Code)
	}

	profileReq := httptest.NewRequest(http.MethodGet, "/u/"+author, nil)
	profileRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(profileRec, profileReq)
	if profileRec.Code != http.StatusOK {
		t.Fatalf("profile status = %d", profileRec.Code)
	}
	if profileRec.Header().Get("X-Ptxt-Guest-Generation") != "7" || profileRec.Header().Get("X-Ptxt-Route-Status") != "ready" {
		t.Fatalf("profile guest headers = %#v", profileRec.Header())
	}
	if got := profileRec.Header().Get("Vary"); !strings.Contains(got, "Cookie") {
		t.Fatalf("profile Vary = %q, want Cookie", got)
	}
	if !strings.Contains(profileRec.Body.String(), `data-guest-v2="1" data-guest-generation="7"`) {
		t.Fatalf("profile does not use v2 document: %s", truncateForLog(profileRec.Body.String(), 800))
	}
	prefetchReq := httptest.NewRequest(http.MethodGet, "/u/"+author, nil)
	prefetchReq.Header.Set("Sec-Purpose", "prefetch")
	prefetchRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(prefetchRec, prefetchReq)
	if got := prefetchRec.Header().Get("Cache-Control"); !strings.Contains(got, "max-age=60") {
		t.Fatalf("prefetched document cache-control = %q", got)
	}
}

func TestGuestSliceV2KeepsSignedInDocumentsOnFullAppBundle(t *testing.T) {
	srv, st := testServer(t)
	srv.cfg.GuestSliceV2Enabled = true
	author := strings.Repeat("d", 64)
	publishTestGuestSlice(t, st, author, "", 9)

	req := httptest.NewRequest(http.MethodGet, "/u/"+author, nil)
	req.Header.Set(headerViewerPubkey, strings.Repeat("e", 64))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("signed-in profile status = %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, `data-guest-v2="1"`) || strings.Contains(body, "/build/guest.js") {
		t.Fatalf("signed-in profile incorrectly used guest document: %s", truncateForLog(body, 800))
	}
	if !strings.Contains(body, "/build/entry.js") {
		t.Fatalf("signed-in profile did not use full app bundle: %s", truncateForLog(body, 800))
	}
	if got := rec.Header().Get("X-Ptxt-Guest-Generation"); got != "" {
		t.Fatalf("signed-in profile guest generation header = %q", got)
	}
}

func TestGuestSliceV2KeepsNonGuestRoutesOnFullAppBundle(t *testing.T) {
	srv, _ := testServer(t)
	srv.cfg.GuestSliceV2Enabled = true

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, `data-guest-v2="1"`) || strings.Contains(body, "/build/guest.js") {
		t.Fatalf("login incorrectly used guest document: %s", truncateForLog(body, 800))
	}
	if !strings.Contains(body, "/build/entry.js") {
		t.Fatalf("login did not use full app bundle: %s", truncateForLog(body, 800))
	}
}

func TestGuestProfileDoesNotPerformNIP05NetworkLookup(t *testing.T) {
	requests := 0
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()
	originalTransport := http.DefaultTransport
	http.DefaultTransport = upstream.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	srv, st := testServer(t)
	srv.cfg.GuestSliceV2Enabled = true
	author := strings.Repeat("b", 64)
	identifier := "guest@" + strings.TrimPrefix(upstream.URL, "https://")
	publishTestGuestSlice(t, st, author, identifier, 3)

	req := httptest.NewRequest(http.MethodGet, "/u/"+author, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("profile status = %d", rec.Code)
	}
	if requests != 0 {
		t.Fatalf("guest profile made %d NIP-05 network requests", requests)
	}
}

func TestGuestProfileDoesNotScheduleRelayWork(t *testing.T) {
	var relayRequests atomic.Int64
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relayRequests.Add(1)
		http.Error(w, "unexpected relay request", http.StatusBadRequest)
	}))
	defer relay.Close()

	srv, st := testServer(t)
	srv.cfg.GuestSliceV2Enabled = true
	relayURL := "ws" + strings.TrimPrefix(relay.URL, "http")
	srv.cfg.DefaultRelays = []string{relayURL}
	srv.cfg.MetadataRelays = []string{relayURL}
	author := strings.Repeat("c", 64)
	publishTestGuestSlice(t, st, author, "", 5)
	quote := nostrx.Event{
		ID:        testEventID("guest-profile-quote", author),
		PubKey:    author,
		CreatedAt: 102,
		Kind:      nostrx.KindTextNote,
		Content:   "cached quote with a missing referenced event",
		Tags:      [][]string{{"q", testEventID("missing-quoted-note", author)}},
		Sig:       strings.Repeat("3", 128),
	}
	if err := st.SaveEvent(context.Background(), quote); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/u/"+author, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("profile status = %d", rec.Code)
	}
	time.Sleep(150 * time.Millisecond)
	if got := relayRequests.Load(); got != 0 {
		t.Fatalf("guest profile scheduled %d relay requests", got)
	}
}
