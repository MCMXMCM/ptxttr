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

func TestSignedInFeedDocumentUsesDurableSnapshotWhenPersonalizationCold(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	viewer := strings.Repeat("a", 64)
	relays := []string{"wss://custom.example"}
	key := signedInFeedSnapshotKey(viewer, feedSortTrend24h, webOfTrustOptions{Enabled: false, Depth: 1}, relays)
	if err := st.SetFeedSnapshot(ctx, key, testFeedSnapshotRecord(hashStringSlice(relays), "persisted personalized note")); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/feed?pubkey="+viewer+"&sort=trend24h&relay=wss://custom.example", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "persisted personalized note") {
		t.Fatalf("expected durable snapshot note in body: %s", body)
	}
	if strings.Contains(body, `data-feed-loader`) {
		t.Fatalf("unexpected loader for durable signed-in snapshot: %s", body)
	}
}

func TestSignedInFeedFragmentServesStarterSnapshotThenPersistsPersonalizedSnapshot(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	viewer := strings.Repeat("a", 64)
	author := strings.Repeat("b", 64)
	starterNoteID := strings.Repeat("c", 64)
	personalizedNoteID := strings.Repeat("d", 64)

	if err := st.SetDefaultSeedGuestFeedSnapshot(ctx, &store.DefaultSeedGuestFeedSnapshot{
		RelaysHash:       hashStringSlice(srv.canonicalDefaultLoggedOutRelays()),
		Feed:             []nostrx.Event{{ID: starterNoteID, PubKey: strings.Repeat("e", 64), CreatedAt: time.Now().Unix(), Kind: nostrx.KindTextNote, Content: "starter snapshot note", Sig: strings.Repeat("4", 128)}},
		Profiles:         map[string]store.DefaultSeedProfileSnap{},
		ReferencedEvents: map[string]nostrx.Event{},
		ReplyCounts:      map[string]int{},
		ReactionTotals:   map[string]int{},
		ReactionViewers:  map[string]string{},
		ComputedAtUnix:   time.Now().Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	for _, event := range []nostrx.Event{
		{ID: strings.Repeat("f", 64), PubKey: viewer, CreatedAt: time.Now().Unix() - 10, Kind: nostrx.KindFollowList, Tags: [][]string{{"p", author}}, Sig: strings.Repeat("5", 128)},
		{ID: personalizedNoteID, PubKey: author, CreatedAt: time.Now().Unix() - 5, Kind: nostrx.KindTextNote, Content: "personalized warm note", Sig: strings.Repeat("6", 128)},
	} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/feed?fragment=1&pubkey="+viewer+"&relay=wss://custom.example", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("X-Ptxt-Feed-Snapshot-Starter"); got != "1" {
		t.Fatalf("starter header = %q, want 1", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "starter snapshot note") {
		t.Fatalf("expected starter snapshot note in fragment: %s", body)
	}

	relays := []string{"wss://custom.example"}
	key := signedInFeedSnapshotKey(viewer, feedSortRecent, webOfTrustOptions{Enabled: false, Depth: 1}, relays)
	deadline := time.Now().Add(2 * time.Second)
	for {
		snap, ok, err := st.GetFeedSnapshot(ctx, key)
		if err == nil && ok && snap != nil && len(snap.Feed) > 0 {
			if !strings.Contains(snap.Feed[0].Content, "personalized warm note") {
				t.Fatalf("expected personalized snapshot content, got %#v", snap.Feed)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for personalized snapshot key %q", key)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestGuestTrendSnapshotSurvivesProcessRestart(t *testing.T) {
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
	if err := st1.SetFeedSnapshot(ctx, key, testFeedSnapshotRecord(hashStringSlice(srv1.canonicalDefaultLoggedOutRelays()), "restart durable trend note")); err != nil {
		t.Fatal(err)
	}
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
	if !strings.Contains(body, "restart durable trend note") {
		t.Fatalf("expected durable trend snapshot after restart: %s", body)
	}
	if strings.Contains(body, `data-feed-loader`) {
		t.Fatalf("unexpected loader for durable trend snapshot after restart: %s", body)
	}
}
