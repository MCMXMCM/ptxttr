package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ptxt-nstr/internal/config"
	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

func testFeedSnapshotRecord(relaysHash string, content string) *store.FeedSnapshotRecord {
	return &store.FeedSnapshotRecord{
		Version:        feedSnapshotRecordVersion,
		RelaysHash:     relaysHash,
		Feed:           []nostrx.Event{{ID: strings.Repeat("1", 64), PubKey: strings.Repeat("2", 64), CreatedAt: time.Now().Unix(), Kind: nostrx.KindTextNote, Content: content, Sig: strings.Repeat("3", 128)}},
		Profiles:       map[string]store.DefaultSeedProfileSnap{},
		ComputedAtUnix: time.Now().Unix(),
	}
}

func persistTestFeedSnapshot(t *testing.T, ctx context.Context, st *store.Store, key string, rec *store.FeedSnapshotRecord) {
	t.Helper()
	for _, event := range rec.Feed {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetFeedSnapshot(ctx, key, rec); err != nil {
		t.Fatal(err)
	}
}

func assertClientShellFeedDocument(t *testing.T, body, pathSnippet string) {
	t.Helper()
	if !strings.Contains(body, "ptxt-route-context") {
		t.Fatalf("expected route context in shell document: %s", body)
	}
	if !strings.Contains(body, `data-route-outlet="root"`) {
		t.Fatalf("expected app shell root outlet in shell document: %s", body)
	}
	if strings.Contains(body, `data-feed-loader`) {
		t.Fatalf("expected feed document to avoid server-rendered feed loader markup: %s", body)
	}
	if pathSnippet != "" && !strings.Contains(body, pathSnippet) {
		t.Fatalf("expected route context path snippet %q in shell document: %s", pathSnippet, body)
	}
}

func TestSignedInFeedDocumentRendersPersonalizedSnapshot(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	viewer := strings.Repeat("a", 64)
	relays := []string{"wss://custom.example"}
	key := signedInFeedSnapshotKey(viewer, feedSortTrend24h, webOfTrustOptions{Enabled: false, Depth: 1}, relays)
	persistTestFeedSnapshot(t, ctx, st, key, testFeedSnapshotRecord(hashStringSlice(relays), "persisted personalized note"))

	req := httptest.NewRequest(http.MethodGet, "/feed?pubkey="+viewer+"&sort=trend24h&relay=wss://custom.example", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "persisted personalized note") {
		t.Fatalf("expected signed-in feed document to render cached server note, got %s", body)
	}
}

func TestSignedInFeedDocumentIgnoresStalePersonalizedSnapshot(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	viewer := strings.Repeat("a", 64)
	author := strings.Repeat("b", 64)
	relays := []string{"wss://custom.example"}
	key := signedInFeedSnapshotKey(viewer, feedSortRecent, webOfTrustOptions{Enabled: false, Depth: 1}, relays)
	rec := testFeedSnapshotRecord(hashStringSlice(relays), "stale personalized note")
	rec.ComputedAtUnix = time.Now().Add(-signedInFeedSnapshotMaxAge - time.Minute).Unix()
	persistTestFeedSnapshot(t, ctx, st, key, rec)
	for _, event := range []nostrx.Event{
		{ID: strings.Repeat("4", 64), PubKey: viewer, CreatedAt: time.Now().Unix() - 10, Kind: nostrx.KindFollowList, Tags: [][]string{{"p", author}}, Sig: strings.Repeat("5", 128)},
		{ID: strings.Repeat("6", 64), PubKey: author, CreatedAt: time.Now().Unix() - 5, Kind: nostrx.KindTextNote, Content: "fresh live note", Sig: strings.Repeat("7", 128)},
	} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/feed?pubkey="+viewer+"&relay=wss://custom.example", nil)
	recorder := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `data-feed`) || !strings.Contains(body, `data-load-more`) {
		t.Fatalf("expected server-rendered feed document chrome: %s", body)
	}
	if strings.Contains(body, "stale personalized note") {
		t.Fatalf("stale snapshot should have been bypassed: %s", body)
	}
	if !strings.Contains(body, "fresh live note") {
		t.Fatalf("expected stale snapshot bypass to render fresh server note: %s", body)
	}
}

func TestSignedInFeedSnapshotMutedItemsTopUpFromLiveFeed(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	viewer := strings.Repeat("a", 64)
	muted := strings.Repeat("b", 64)
	author := strings.Repeat("c", 64)
	relays := []string{"wss://custom.example"}
	key := signedInFeedSnapshotKey(viewer, feedSortRecent, webOfTrustOptions{Enabled: false, Depth: 1}, relays)
	mutedSnapshot := &store.FeedSnapshotRecord{
		Version:        feedSnapshotRecordVersion,
		RelaysHash:     hashStringSlice(relays),
		Feed:           []nostrx.Event{{ID: strings.Repeat("8", 64), PubKey: muted, CreatedAt: time.Now().Unix(), Kind: nostrx.KindTextNote, Content: "muted snapshot note", Sig: strings.Repeat("9", 128)}},
		Profiles:       map[string]store.DefaultSeedProfileSnap{},
		HasMore:        true,
		ComputedAtUnix: time.Now().Unix(),
	}
	persistTestFeedSnapshot(t, ctx, st, key, mutedSnapshot)
	for _, event := range []nostrx.Event{
		{ID: strings.Repeat("d", 64), PubKey: viewer, CreatedAt: time.Now().Unix() - 20, Kind: nostrx.KindFollowList, Tags: [][]string{{"p", author}}, Sig: strings.Repeat("1", 128)},
		{ID: strings.Repeat("e", 64), PubKey: viewer, CreatedAt: time.Now().Unix() - 19, Kind: nostrx.KindMuteList, Tags: [][]string{{"p", muted}}, Sig: strings.Repeat("2", 128)},
		{ID: strings.Repeat("f", 64), PubKey: author, CreatedAt: time.Now().Unix() - 5, Kind: nostrx.KindTextNote, Content: "top up live note", Sig: strings.Repeat("3", 128)},
	} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/feed?pubkey="+viewer+"&relay=wss://custom.example", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-feed`) || !strings.Contains(body, `data-load-more`) {
		t.Fatalf("expected server-rendered feed document chrome: %s", body)
	}
	if strings.Contains(body, "muted snapshot note") {
		t.Fatalf("muted snapshot note should not be inlined into server feed document: %s", body)
	}
	if !strings.Contains(body, "top up live note") {
		t.Fatalf("expected server feed document to top up muted snapshot from live cache: %s", body)
	}
}

func TestGuestTrendDocumentStaysShellFirstAfterProcessRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(root, "snapshot.sqlite")

	st1, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	srv1, err := New(config.Config{RequestTimeout: time.Second, WOTMaxAuthors: 240}, st1, nostrx.NewClient(nil, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	key := guestCanonicalFeedSnapshotKey(feedSortTrend7d, srv1.canonicalDefaultLoggedOutRelays())
	persistTestFeedSnapshot(t, ctx, st1, key, testFeedSnapshotRecord(hashStringSlice(srv1.canonicalDefaultLoggedOutRelays()), "restart durable trend note"))
	srv1.Close()
	_ = st1.Close()

	st2, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st2.Close() })
	srv2, err := New(config.Config{RequestTimeout: time.Second, WOTMaxAuthors: 240}, st2, nostrx.NewClient(nil, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/?sort=trend7d", nil)
	rec := httptest.NewRecorder()
	srv2.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-feed`) || !strings.Contains(body, `data-feed-snapshot-starter="1"`) {
		t.Fatalf("expected server-rendered guest trend feed document: %s", body)
	}
	if !strings.Contains(body, "restart durable trend note") {
		t.Fatalf("expected guest trend document to render durable snapshot after restart: %s", body)
	}
}
