package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ptxt-nstr/internal/nostrx"

	fnostr "fiatjaf.com/nostr"
	"github.com/coder/websocket"
)

func TestRotateHotFeedAuthorsWrapsLargeCohorts(t *testing.T) {
	authors := []string{"a", "b", "c", "d", "e"}

	got := rotateHotFeedAuthors(authors, 2, 1)
	if strings.Join(got, ",") != "c,d" {
		t.Fatalf("first rotation = %#v, want c,d", got)
	}

	got = rotateHotFeedAuthors(authors, 2, 2)
	if strings.Join(got, ",") != "e,a" {
		t.Fatalf("wrapped rotation = %#v, want e,a", got)
	}

	got = rotateHotFeedAuthors(authors, 10, 3)
	if strings.Join(got, ",") != strings.Join(authors, ",") {
		t.Fatalf("small cohort should be copied whole: %#v", got)
	}
}

func TestHotFeedCohortsIncludeDefaultAndActiveViewersWithinLimit(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	viewer := strings.Repeat("a", 64)
	author := strings.Repeat("b", 64)
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        strings.Repeat("1", 64),
		PubKey:    viewer,
		CreatedAt: time.Now().Unix(),
		Kind:      nostrx.KindFollowList,
		Tags:      [][]string{{"p", author}},
		Sig:       strings.Repeat("2", 128),
	}); err != nil {
		t.Fatal(err)
	}
	srv.resolvedAuthors.put(resolvedAuthorsCacheKey(viewer, webOfTrustOptions{Enabled: false, Depth: 1}), []string{author, viewer}, time.Now())
	srv.activeViewers.Touch(viewer, webOfTrustOptions{Enabled: false, Depth: 1}, time.Now())

	srv.cfg.HotFeedCrawlerCohortLimit = 1
	cohorts := srv.hotFeedCohorts(ctx, time.Now())
	if len(cohorts) != 1 || cohorts[0].name != "default_seed" {
		t.Fatalf("cohorts with limit 1 = %#v, want only default seed", cohorts)
	}

	srv.cfg.HotFeedCrawlerCohortLimit = 2
	cohorts = srv.hotFeedCohorts(ctx, time.Now())
	if len(cohorts) != 2 {
		t.Fatalf("cohort count = %d, want 2", len(cohorts))
	}
	if cohorts[1].resolved.userPubkey != viewer {
		t.Fatalf("active viewer cohort = %q, want %q", cohorts[1].resolved.userPubkey, viewer)
	}
}

func TestRefreshHotFeedSnapshotsPersistsSignedInFirstPage(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	viewer := strings.Repeat("a", 64)
	author := strings.Repeat("b", 64)
	relays := []string{"wss://custom.example"}
	for _, event := range []nostrx.Event{
		{ID: strings.Repeat("1", 64), PubKey: viewer, CreatedAt: time.Now().Unix() - 10, Kind: nostrx.KindFollowList, Tags: [][]string{{"p", author}}, Sig: strings.Repeat("2", 128)},
		{ID: strings.Repeat("3", 64), PubKey: author, CreatedAt: time.Now().Unix() - 5, Kind: nostrx.KindTextNote, Content: "hot snapshot note", Sig: strings.Repeat("4", 128)},
	} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	cohort, ok := srv.hotFeedCohortFromRequest(ctx, "viewer", feedRequest{
		Pubkey:   viewer,
		Limit:    30,
		Relays:   relays,
		SortMode: feedSortRecent,
		WoT:      webOfTrustOptions{Enabled: false, Depth: 1},
	})
	if !ok {
		t.Fatal("expected hot feed cohort")
	}

	srv.refreshHotFeedSnapshots(ctx, cohort)

	key := signedInFeedSnapshotKey(viewer, feedSortRecent, webOfTrustOptions{Enabled: false, Depth: 1}, relays)
	snap, ok, err := st.GetFeedSnapshot(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || snap == nil || len(snap.Feed) == 0 {
		t.Fatalf("missing signed-in hot feed snapshot for key %q", key)
	}
	if snap.Feed[0].Content != "hot snapshot note" {
		t.Fatalf("snapshot feed[0] content = %q, want hot snapshot note", snap.Feed[0].Content)
	}
}

func TestHotFeedCrawlerSuppliesStoreForNewerCount(t *testing.T) {
	srv, st := newTestServer(t, testServerOptions{relayTimeout: 50 * time.Millisecond})
	ctx := context.Background()
	viewer := strings.Repeat("a", 64)

	secret := fnostr.Generate()
	note := fnostr.Event{
		CreatedAt: fnostr.Timestamp(time.Now().Unix()),
		Kind:      fnostr.Kind(nostrx.KindTextNote),
		Content:   "relay hot note",
	}
	if err := note.Sign(secret); err != nil {
		t.Fatalf("Sign() note error = %v", err)
	}
	author := note.PubKey.Hex()
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        strings.Repeat("1", 64),
		PubKey:    viewer,
		CreatedAt: time.Now().Unix() - 60,
		Kind:      nostrx.KindFollowList,
		Tags:      [][]string{{"p", author}},
		Sig:       strings.Repeat("2", 128),
	}); err != nil {
		t.Fatal(err)
	}
	relay := newRelayWithEvents(t, []nostrx.Event{fnostrToNostrxEvent(note)})
	defer relay.Close()
	relayURL := wsURL(relay.URL)
	srv.cfg.DefaultRelays = []string{relayURL}
	srv.cfg.HotFeedCrawlerCohortLimit = 2
	srv.cfg.HotFeedCrawlerAuthorLimit = 4
	srv.cfg.HotFeedCrawlerFetchLimit = 10
	srv.resolvedAuthors.put(resolvedAuthorsCacheKey(viewer, webOfTrustOptions{Enabled: false, Depth: 1}), []string{author, viewer}, time.Now())
	srv.activeViewers.Touch(viewer, webOfTrustOptions{Enabled: false, Depth: 1}, time.Now())

	before := srv.feedNewerCount(ctx, feedRequest{
		Pubkey:   viewer,
		Limit:    30,
		Relays:   []string{relayURL},
		SortMode: feedSortRecent,
		WoT:      webOfTrustOptions{Enabled: false, Depth: 1},
	})
	if before != 0 {
		t.Fatalf("newer count before hot crawl = %d, want 0", before)
	}

	srv.warmHotFeedTickBody(ctx)

	after := srv.feedNewerCount(ctx, feedRequest{
		Pubkey:   viewer,
		Limit:    30,
		Relays:   []string{relayURL},
		SortMode: feedSortRecent,
		WoT:      webOfTrustOptions{Enabled: false, Depth: 1},
	})
	if after != 1 {
		t.Fatalf("newer count after hot crawl = %d, want 1", after)
	}
}

func TestHotFeedCrawlerUsesDiscoveredRelayHintsForDefaultWoTNotes(t *testing.T) {
	srv, st := newTestServer(t, testServerOptions{relayTimeout: 500 * time.Millisecond})
	ctx := context.Background()
	viewer := strings.Repeat("a", 64)

	secret := fnostr.Generate()
	note := fnostr.Event{
		CreatedAt: fnostr.Timestamp(time.Now().Unix()),
		Kind:      fnostr.Kind(nostrx.KindTextNote),
		Content:   "hinted relay hot note",
	}
	if err := note.Sign(secret); err != nil {
		t.Fatalf("Sign() note error = %v", err)
	}
	author := note.PubKey.Hex()
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        strings.Repeat("1", 64),
		PubKey:    viewer,
		CreatedAt: time.Now().Unix() - 60,
		Kind:      nostrx.KindFollowList,
		Tags:      [][]string{{"p", author}},
		Sig:       strings.Repeat("2", 128),
	}); err != nil {
		t.Fatal(err)
	}

	hintedRelay := filteredRelayWithEvents(t, []nostrx.Event{fnostrToNostrxEvent(note)})
	defer hintedRelay.Close()
	hintedRelayURL := wsURL(hintedRelay.URL)
	relayList := fnostr.Event{
		CreatedAt: fnostr.Timestamp(time.Now().Unix()),
		Kind:      fnostr.Kind(nostrx.KindRelayListMetadata),
		Tags:      fnostr.Tags{{"r", hintedRelayURL, "write"}},
	}
	if err := relayList.Sign(secret); err != nil {
		t.Fatalf("Sign() relay list error = %v", err)
	}
	defaultRelay := filteredRelayWithEvents(t, []nostrx.Event{fnostrToNostrxEvent(relayList)})
	defer defaultRelay.Close()
	defaultRelayURL := wsURL(defaultRelay.URL)

	srv.cfg.DefaultRelays = []string{defaultRelayURL}
	srv.cfg.MetadataRelays = []string{defaultRelayURL}
	srv.cfg.HotFeedCrawlerCohortLimit = 2
	srv.cfg.HotFeedCrawlerAuthorLimit = 4
	srv.cfg.HotFeedCrawlerFetchLimit = 10
	srv.resolvedAuthors.put(resolvedAuthorsCacheKey(viewer, webOfTrustOptions{Enabled: false, Depth: 1}), []string{author, viewer}, time.Now())
	srv.activeViewers.Touch(viewer, webOfTrustOptions{Enabled: false, Depth: 1}, time.Now())

	srv.warmHotFeedTickBody(ctx)

	stored, err := st.GetEvent(ctx, note.ID.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if stored == nil || stored.Content != "hinted relay hot note" {
		t.Fatalf("hot crawler did not fetch note through discovered relay hint: %#v", stored)
	}
	counters := srv.metrics.Snapshot()["counters"].(map[string]int64)
	if counters["crawler.hot_feed.relay_hints"] == 0 {
		t.Fatalf("expected relay hint refresh metric, counters=%v", counters)
	}
}

func filteredRelayWithEvents(t *testing.T, events []nostrx.Event) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "done") }()
		ctx := context.Background()
		_, msg, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var envelope []json.RawMessage
		if err := json.Unmarshal(msg, &envelope); err != nil || len(envelope) < 3 {
			return
		}
		var subID string
		if err := json.Unmarshal(envelope[1], &subID); err != nil {
			return
		}
		var filter struct {
			Authors []string `json:"authors"`
			Kinds   []int    `json:"kinds"`
			Since   int64    `json:"since"`
			Until   int64    `json:"until"`
		}
		_ = json.Unmarshal(envelope[2], &filter)
		authorOK := func(pubkey string) bool {
			if len(filter.Authors) == 0 {
				return true
			}
			for _, author := range filter.Authors {
				if author == pubkey {
					return true
				}
			}
			return false
		}
		kindOK := func(kind int) bool {
			if len(filter.Kinds) == 0 {
				return true
			}
			for _, k := range filter.Kinds {
				if k == kind {
					return true
				}
			}
			return false
		}
		for _, event := range events {
			if !authorOK(event.PubKey) || !kindOK(event.Kind) {
				continue
			}
			if filter.Since > 0 && event.CreatedAt < filter.Since {
				continue
			}
			if filter.Until > 0 && event.CreatedAt > filter.Until {
				continue
			}
			encoded, err := json.Marshal(event)
			if err != nil {
				continue
			}
			message := fmt.Sprintf(`["EVENT",%q,%s]`, subID, string(encoded))
			_ = conn.Write(ctx, websocket.MessageText, []byte(message))
		}
		_ = conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`["EOSE",%q]`, subID)))
	}))
}
