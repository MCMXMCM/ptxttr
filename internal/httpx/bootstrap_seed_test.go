package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	fnostr "fiatjaf.com/nostr"
	"github.com/coder/websocket"

	"ptxt-nstr/internal/nostrx"
)

func TestSeedCrawlerPerTickTimeout(t *testing.T) {
	t.Parallel()
	if got := seedCrawlerPerTickTimeout(3500*time.Millisecond, 0); got != 2*time.Minute {
		t.Fatalf("seedCrawlerPerTickTimeout(3.5s,0) = %v, want 2m", got)
	}
	long := 3 * time.Minute
	got := seedCrawlerPerTickTimeout(long, 0)
	want := requestTimeout(long)
	if got != want {
		t.Fatalf("seedCrawlerPerTickTimeout(3m,0) = %v, want %v (no floor when above 2m)", got, want)
	}
	// Larger batches need extra budget so the tick does not cancel mid-loop.
	withAuthors := seedCrawlerPerTickTimeout(3500*time.Millisecond, 20)
	wantFloor := 2*time.Minute + 20*seedCrawlerPerAuthorTimeoutBudget
	if withAuthors != wantFloor {
		t.Fatalf("seedCrawlerPerTickTimeout(3.5s,20) = %v, want %v", withAuthors, wantFloor)
	}
}

func TestPrewarmLoggedOutSeedNowEnqueuesContactsWithoutTimelineNotes(t *testing.T) {
	srv, _ := newTestServer(t, testServerOptions{relayTimeout: 50 * time.Millisecond})
	ctx := context.Background()

	firstHopSecret := fnostr.Generate()
	firstHopNote := fnostr.Event{
		CreatedAt: fnostr.Now(),
		Kind:      fnostr.Kind(nostrx.KindTextNote),
		Content:   "not on relay",
	}
	if err := firstHopNote.Sign(firstHopSecret); err != nil {
		t.Fatalf("Sign() first hop note error = %v", err)
	}

	seedSecret := fnostr.Generate()
	seedFollow := fnostr.Event{
		CreatedAt: fnostr.Now(),
		Kind:      fnostr.Kind(nostrx.KindFollowList),
		Tags:      fnostr.Tags{fnostr.Tag{"p", firstHopNote.PubKey.Hex()}},
	}
	if err := seedFollow.Sign(seedSecret); err != nil {
		t.Fatalf("Sign() seed follow error = %v", err)
	}

	// Relay only serves kind-3; followed author has no kind-1 on this relay.
	relay := newRelayWithEvents(t, []nostrx.Event{fnostrToNostrxEvent(seedFollow)})
	defer relay.Close()
	srv.cfg.DefaultRelays = []string{wsURL(relay.URL)}
	srv.cfg.MetadataRelays = []string{wsURL(relay.URL)}

	seedNPub := nostrx.EncodeNPub(seedFollow.PubKey.Hex())
	if err := srv.prewarmLoggedOutSeedNow(ctx, seedNPub, defaultLoggedOutWOTDepth); err != nil {
		t.Fatalf("prewarmLoggedOutSeedNow() error = %v (bootstrap should not require timeline notes)", err)
	}
	maxFail := srv.cfg.SeedContactMaxFailCount
	if maxFail <= 0 {
		maxFail = 12
	}
	stale, err := srv.store.StaleSeedContactBatch(ctx, time.Now().Unix(), 50, maxFail)
	if err != nil {
		t.Fatalf("StaleSeedContactBatch() error = %v", err)
	}
	if len(stale) == 0 {
		t.Fatal("expected at least one enqueued seedContact for followed pubkey")
	}
}

func TestPrewarmLoggedOutSeedNowEnqueuesAllSeedFollowsAcrossPages(t *testing.T) {
	srv, _ := newTestServer(t, testServerOptions{relayTimeout: 50 * time.Millisecond})
	ctx := context.Background()
	srv.cfg.SeedBootstrapFollowEnqueueLimit = 2

	tags := make(fnostr.Tags, 0, 5)
	for i := 0; i < 5; i++ {
		tags = append(tags, fnostr.Tag{"p", fmt.Sprintf("%064x", i+1)})
	}

	seedSecret := fnostr.Generate()
	seedFollow := fnostr.Event{
		CreatedAt: fnostr.Now(),
		Kind:      fnostr.Kind(nostrx.KindFollowList),
		Tags:      tags,
	}
	if err := seedFollow.Sign(seedSecret); err != nil {
		t.Fatalf("Sign() seed follow error = %v", err)
	}

	relay := newRelayWithEvents(t, []nostrx.Event{fnostrToNostrxEvent(seedFollow)})
	defer relay.Close()
	srv.cfg.DefaultRelays = []string{wsURL(relay.URL)}
	srv.cfg.MetadataRelays = []string{wsURL(relay.URL)}

	seedNPub := nostrx.EncodeNPub(seedFollow.PubKey.Hex())
	if err := srv.prewarmLoggedOutSeedNow(ctx, seedNPub, defaultLoggedOutWOTDepth); err != nil {
		t.Fatalf("prewarmLoggedOutSeedNow() error = %v", err)
	}
	maxFail := srv.cfg.SeedContactMaxFailCount
	if maxFail <= 0 {
		maxFail = 12
	}
	stale, err := srv.store.StaleSeedContactBatch(ctx, time.Now().Unix(), 10, maxFail)
	if err != nil {
		t.Fatalf("StaleSeedContactBatch() error = %v", err)
	}
	if len(stale) != 5 {
		t.Fatalf("expected all 5 follows enqueued across pages, got %d", len(stale))
	}
}

func TestPrewarmDefaultLoggedOutSeedFailsWithoutFollowMaterialization(t *testing.T) {
	srv, _ := newTestServer(t, testServerOptions{relayTimeout: 50 * time.Millisecond})
	ctx := context.Background()
	// No relays configured in test New() — Jack follow list cannot load.
	err := srv.prewarmBootstrapLoggedOutSeed(ctx, defaultLoggedOutWOTSeedNPub, defaultLoggedOutWOTDepth)
	if err == nil {
		t.Fatal("expected error when follow list cannot be materialized")
	}
	if !srv.store.ShouldRefresh(ctx, "bootstrap", defaultLoggedOutSeedBootstrapKey, defaultLoggedOutSeedBootstrapTTL) {
		t.Fatal("bootstrap scope should still need refresh after failed prewarm")
	}
}

func TestCrawlSeedTickHydratesSeedContact(t *testing.T) {
	t.Parallel()
	srv, _ := newTestServer(t, testServerOptions{relayTimeout: 50 * time.Millisecond})
	ctx := context.Background()

	firstHopSecret := fnostr.Generate()
	firstHopNote := fnostr.Event{
		CreatedAt: fnostr.Now(),
		Kind:      fnostr.Kind(nostrx.KindTextNote),
		Content:   "crawler note",
	}
	if err := firstHopNote.Sign(firstHopSecret); err != nil {
		t.Fatalf("Sign() first hop note error = %v", err)
	}

	seedSecret := fnostr.Generate()
	seedFollow := fnostr.Event{
		CreatedAt: fnostr.Now(),
		Kind:      fnostr.Kind(nostrx.KindFollowList),
		Tags:      fnostr.Tags{fnostr.Tag{"p", firstHopNote.PubKey.Hex()}},
	}
	if err := seedFollow.Sign(seedSecret); err != nil {
		t.Fatalf("Sign() seed follow error = %v", err)
	}

	relay := newRelayWithEvents(t, []nostrx.Event{fnostrToNostrxEvent(seedFollow), fnostrToNostrxEvent(firstHopNote)})
	defer relay.Close()
	srv.cfg.DefaultRelays = []string{wsURL(relay.URL)}
	srv.cfg.MetadataRelays = []string{wsURL(relay.URL)}

	seedNPub := nostrx.EncodeNPub(seedFollow.PubKey.Hex())
	if err := srv.prewarmLoggedOutSeedNow(ctx, seedNPub, defaultLoggedOutWOTDepth); err != nil {
		t.Fatalf("prewarmLoggedOutSeedNow() error = %v", err)
	}
	srv.crawlSeedTick()
	got, err := srv.store.RecentSummariesByAuthorsCursor(ctx, []string{firstHopNote.PubKey.Hex()}, []int{nostrx.KindTextNote}, 0, "", 5)
	if err != nil {
		t.Fatalf("RecentSummariesByAuthorsCursor() error = %v", err)
	}
	if len(got) == 0 || got[0].Content != "crawler note" {
		t.Fatalf("expected hydrated note, got %#v", got)
	}
	maxFail := srv.cfg.SeedContactMaxFailCount
	if maxFail <= 0 {
		maxFail = 12
	}
	stale, err := srv.store.StaleSeedContactBatch(ctx, time.Now().Unix(), 10, maxFail)
	if err != nil {
		t.Fatalf("StaleSeedContactBatch() error = %v", err)
	}
	for _, target := range stale {
		if target.EntityID == firstHopNote.PubKey.Hex() {
			t.Fatal("successful seed contact should remain deduped instead of being re-queued")
		}
	}
}

func TestCrawlSeedTickInvalidatesSeedAuthorCacheAfterGraphExpansion(t *testing.T) {
	srv, _ := newTestServer(t, testServerOptions{relayTimeout: 50 * time.Millisecond})
	ctx := context.Background()
	srv.cfg.SeedContactFollowEnqueuePerTick = 1

	firstHopSecret := fnostr.Generate()
	secondHop := fmt.Sprintf("%064x", 99)
	firstHopFollow := fnostr.Event{
		CreatedAt: fnostr.Now(),
		Kind:      fnostr.Kind(nostrx.KindFollowList),
		Tags:      fnostr.Tags{fnostr.Tag{"p", secondHop}},
	}
	if err := firstHopFollow.Sign(firstHopSecret); err != nil {
		t.Fatalf("Sign() first hop follow error = %v", err)
	}

	seedSecret := fnostr.Generate()
	seedFollow := fnostr.Event{
		CreatedAt: fnostr.Now(),
		Kind:      fnostr.Kind(nostrx.KindFollowList),
		Tags:      fnostr.Tags{fnostr.Tag{"p", firstHopFollow.PubKey.Hex()}},
	}
	if err := seedFollow.Sign(seedSecret); err != nil {
		t.Fatalf("Sign() seed follow error = %v", err)
	}

	seedFollowNx := fnostrToNostrxEvent(seedFollow)
	firstHopFollowNx := fnostrToNostrxEvent(firstHopFollow)
	// Filtered relay: naive newRelayWithEvents would echo every stored event on
	// every REQ, which would ingest the second-hop follow list during prewarm and
	// defeat the stale-cache scenario this test covers.
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		}
		if err := json.Unmarshal(envelope[2], &filter); err != nil {
			return
		}
		for _, ev := range []nostrx.Event{seedFollowNx, firstHopFollowNx} {
			if len(filter.Authors) > 0 && !slices.Contains(filter.Authors, ev.PubKey) {
				continue
			}
			if len(filter.Kinds) > 0 && !slices.Contains(filter.Kinds, int(ev.Kind)) {
				continue
			}
			encoded, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			message := fmt.Sprintf(`["EVENT",%q,%s]`, subID, string(encoded))
			_ = conn.Write(ctx, websocket.MessageText, []byte(message))
		}
		_ = conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`["EOSE",%q]`, subID)))
	}))
	defer relay.Close()
	srv.cfg.DefaultRelays = []string{wsURL(relay.URL)}
	srv.cfg.MetadataRelays = []string{wsURL(relay.URL)}

	seedNPub := nostrx.EncodeNPub(seedFollow.PubKey.Hex())
	if err := srv.prewarmLoggedOutSeedNow(ctx, seedNPub, defaultLoggedOutWOTDepth); err != nil {
		t.Fatalf("prewarmLoggedOutSeedNow() error = %v", err)
	}

	before, _, loggedOut := srv.resolveAuthorsAll(ctx, seedNPub, nil, webOfTrustOptions{Enabled: true, Depth: 2})
	if loggedOut {
		t.Fatal("resolveAuthorsAll() unexpectedly logged out")
	}
	if containsString(before, secondHop) {
		t.Fatalf("unexpected second hop before crawl: %#v", before)
	}

	srv.crawlSeedTick()

	after, _, loggedOut := srv.resolveAuthorsAll(ctx, seedNPub, nil, webOfTrustOptions{Enabled: true, Depth: 2})
	if loggedOut {
		t.Fatal("resolveAuthorsAll() unexpectedly logged out after crawl")
	}
	if !containsString(after, secondHop) {
		t.Fatalf("expected second hop after crawl invalidated cached authors, got %#v", after)
	}
}

func TestPrewarmDefaultLoggedOutSeedMarksRefreshedOnSuccess(t *testing.T) {
	srv, _ := newTestServer(t, testServerOptions{relayTimeout: 50 * time.Millisecond})
	ctx := context.Background()

	firstHopSecret := fnostr.Generate()
	firstHopNote := fnostr.Event{
		CreatedAt: fnostr.Now(),
		Kind:      fnostr.Kind(nostrx.KindTextNote),
		Content:   "bootstrap seeded note",
	}
	if err := firstHopNote.Sign(firstHopSecret); err != nil {
		t.Fatalf("Sign() first hop note error = %v", err)
	}

	seedSecret := fnostr.Generate()
	seedFollow := fnostr.Event{
		CreatedAt: fnostr.Now(),
		Kind:      fnostr.Kind(nostrx.KindFollowList),
		Tags:      fnostr.Tags{fnostr.Tag{"p", firstHopNote.PubKey.Hex()}},
	}
	if err := seedFollow.Sign(seedSecret); err != nil {
		t.Fatalf("Sign() seed follow error = %v", err)
	}

	relayEvents := []nostrx.Event{fnostrToNostrxEvent(seedFollow), fnostrToNostrxEvent(firstHopNote)}
	relay := newRelayWithEvents(t, relayEvents)
	defer relay.Close()

	relayURL := wsURL(relay.URL)
	srv.cfg.DefaultRelays = []string{relayURL}
	srv.cfg.MetadataRelays = []string{relayURL}

	seedNPub := nostrx.EncodeNPub(seedFollow.PubKey.Hex())
	if err := srv.prewarmBootstrapLoggedOutSeed(ctx, seedNPub, defaultLoggedOutWOTDepth); err != nil {
		t.Fatalf("prewarmBootstrapLoggedOutSeed() error = %v", err)
	}
	if srv.store.ShouldRefresh(ctx, "bootstrap", defaultLoggedOutSeedBootstrapKey, defaultLoggedOutSeedBootstrapTTL) {
		t.Fatal("expected bootstrap marked refreshed after successful prewarm")
	}
	// Second call within TTL should no-op without error.
	if err := srv.prewarmBootstrapLoggedOutSeed(ctx, seedNPub, defaultLoggedOutWOTDepth); err != nil {
		t.Fatalf("second prewarmBootstrapLoggedOutSeed() error = %v", err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
