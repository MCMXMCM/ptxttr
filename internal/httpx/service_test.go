package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"ptxt-nstr/internal/config"
	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"

	fnostr "fiatjaf.com/nostr"
	"github.com/coder/websocket"
)

func assertThreadSummaryOPLink(t *testing.T, html, rootID string) {
	t.Helper()
	if !strings.Contains(html, `class="thread-op-link" href="/thread/`+rootID+`"`) {
		t.Fatalf("expected [OP] link to thread root %s: %s", rootID, html)
	}
}

func assertThreadSummaryDepth(t *testing.T, html, wantDepth string) {
	t.Helper()
	if !strings.Contains(html, `class="thread-header-op-depth">`+wantDepth) {
		t.Fatalf("expected thread header depth %s: %s", wantDepth, html)
	}
}

func assertCanonicalLoggedOutHomeShellDeferred(t *testing.T, body string) {
	t.Helper()
	if !strings.Contains(body, `data-feed-loader`) {
		t.Fatalf("expected canonical home shell loader: %s", body)
	}
	if strings.Contains(body, "seeded home note") {
		t.Fatalf("did not expect seeded note inlined into canonical home shell: %s", body)
	}
}

func TestUserNotesFragmentUsesCursorPagination(t *testing.T) {
	srv, st := testServer(t)
	pubkey := strings.Repeat("a", 64)
	for index := 0; index < 32; index++ {
		event := nostrx.Event{
			ID:        fmt.Sprintf("%064x", index+1),
			PubKey:    pubkey,
			CreatedAt: int64(1000 - index),
			Kind:      nostrx.KindTextNote,
			Content:   "note",
		}
		if err := st.SaveEvent(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/u/"+pubkey+"?fragment=notes", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("X-Ptxt-Has-More"); got != "1" {
		t.Fatalf("X-Ptxt-Has-More = %q, want 1", got)
	}
	if count := strings.Count(rec.Body.String(), `<article class="note`); count != 30 {
		t.Fatalf("rendered notes = %d, want 30", count)
	}
}

func TestAnonymousUserNotesFragmentDoesNotBlockOnProfileRefresh(t *testing.T) {
	srv, _ := testServer(t)
	relay := newSlowEOSERelay(t, 400*time.Millisecond)
	defer relay.Close()

	pubkey := strings.Repeat("a", 64)
	req := httptest.NewRequest(http.MethodGet, "/u/"+pubkey+"?fragment=notes&relays="+wsURL(relay.URL), nil)
	rec := httptest.NewRecorder()

	started := time.Now()
	srv.Handler().ServeHTTP(rec, req)
	elapsed := time.Since(started)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if elapsed > 150*time.Millisecond {
		t.Fatalf("anonymous user notes fragment blocked on slow relay: %v", elapsed)
	}
}

func TestHomeRendersFeedLoaderWhenFirstPageIsEmpty(t *testing.T) {
	srv, _ := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-feed-loader`) {
		t.Fatalf("expected initial home loader, got: %s", body)
	}
	if strings.Contains(body, "No notes found yet.") {
		t.Fatalf("unexpected empty-state copy in full home render: %s", body)
	}
}

func TestFeedItemsFragmentKeepsEmptyStateCopy(t *testing.T) {
	srv, _ := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/?fragment=1", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "No notes found yet.") {
		t.Fatalf("expected fragment empty-state copy, got: %s", body)
	}
	if strings.Contains(body, `data-feed-loader`) {
		t.Fatalf("unexpected loader markup in feed items fragment: %s", body)
	}
}

func TestHomeUsesLoggedOutSeededDefaults(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	seed, err := nostrx.DecodeIdentifier(defaultLoggedOutWOTSeedNPub)
	if err != nil {
		t.Fatalf("decode default seed: %v", err)
	}
	firstHop := strings.Repeat("b", 64)
	seededNoteID := strings.Repeat("1", 64)
	for _, event := range []nostrx.Event{
		{
			ID:        strings.Repeat("9", 64),
			PubKey:    seed,
			CreatedAt: time.Now().Unix() - 10,
			Kind:      nostrx.KindFollowList,
			Tags:      [][]string{{"p", firstHop}},
		},
		{
			ID:        seededNoteID,
			PubKey:    firstHop,
			CreatedAt: time.Now().Unix() - 120,
			Kind:      nostrx.KindTextNote,
			Content:   "seeded home note",
		},
		{
			ID:        strings.Repeat("8", 64),
			PubKey:    seed,
			CreatedAt: time.Now().Unix() - 90,
			Kind:      nostrx.KindTextNote,
			Tags:      [][]string{{"e", seededNoteID, "", "root"}, {"e", seededNoteID, "", "reply"}},
			Content:   "seeded home reply",
		},
	} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	// Canonical guest fragment path is snapshot-only on the request; prime the
	// durable default-seed snapshot like production warmers would.
	canonical := srv.canonicalDefaultLoggedOutGuestFeedRequest()
	fd := srv.feedPageDataEx(ctx, canonical, false, feedPageDataOptions{lightStatsOnly: true})
	if len(fd.Feed) == 0 {
		t.Fatal("expected non-empty canonical guest feed for snapshot prime")
	}
	if err := srv.persistDefaultSeedGuestFeedSnapshot(ctx, canonical, &fd); err != nil {
		t.Fatalf("persist snapshot: %v", err)
	}
	srv.resetGuestFeedCacheForTest()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	assertCanonicalLoggedOutHomeShellDeferred(t, body)
	if !strings.Contains(body, "Jack Dorsey") {
		t.Fatalf("expected default logged-out seed summary in body: %s", body)
	}

	frag := httptest.NewRequest(http.MethodGet, "/?fragment=1", nil)
	fragRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(fragRec, frag)
	if fragRec.Code != http.StatusOK {
		t.Fatalf("fragment status = %d, want 200", fragRec.Code)
	}
	fragBody := fragRec.Body.String()
	if !strings.Contains(fragBody, "seeded home note") {
		t.Fatalf("expected seeded home note in fragment body: %s", fragBody)
	}
}

func TestHomeDeferredGuestShellIncludesGuestTTLCacheAfterWarm(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	seed, err := nostrx.DecodeIdentifier(defaultLoggedOutWOTSeedNPub)
	if err != nil {
		t.Fatalf("decode default seed: %v", err)
	}
	firstHop := strings.Repeat("b", 64)
	seededNoteID := strings.Repeat("1", 64)
	for _, event := range []nostrx.Event{
		{
			ID:        strings.Repeat("9", 64),
			PubKey:    seed,
			CreatedAt: time.Now().Unix() - 10,
			Kind:      nostrx.KindFollowList,
			Tags:      [][]string{{"p", firstHop}},
		},
		{
			ID:        seededNoteID,
			PubKey:    firstHop,
			CreatedAt: time.Now().Unix() - 120,
			Kind:      nostrx.KindTextNote,
			Content:   "seeded home note",
		},
	} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	warmCtx, warmCancel := context.WithTimeout(ctx, 30*time.Second)
	defer warmCancel()
	srv.tryWarmDeferredGuestFeedFragmentIfCold(warmCtx, srv.canonicalDefaultLoggedOutGuestFeedRequest())

	warm := httptest.NewRequest(http.MethodGet, "/?fragment=1", nil)
	warmRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(warmRec, warm)
	if warmRec.Code != http.StatusOK {
		t.Fatalf("warm fragment status = %d, want 200", warmRec.Code)
	}

	home := httptest.NewRequest(http.MethodGet, "/", nil)
	homeRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(homeRec, home)
	if homeRec.Code != http.StatusOK {
		t.Fatalf("home status = %d, want 200", homeRec.Code)
	}
	body := homeRec.Body.String()
	assertCanonicalLoggedOutHomeShellDeferred(t, body)
}

func TestHomeDeferredGuestShellUsesPersistedSnapshotWhenGuestCacheCold(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	seed, err := nostrx.DecodeIdentifier(defaultLoggedOutWOTSeedNPub)
	if err != nil {
		t.Fatalf("decode default seed: %v", err)
	}
	firstHop := strings.Repeat("b", 64)
	seededNoteID := strings.Repeat("1", 64)
	for _, event := range []nostrx.Event{
		{
			ID:        strings.Repeat("9", 64),
			PubKey:    seed,
			CreatedAt: time.Now().Unix() - 10,
			Kind:      nostrx.KindFollowList,
			Tags:      [][]string{{"p", firstHop}},
		},
		{
			ID:        seededNoteID,
			PubKey:    firstHop,
			CreatedAt: time.Now().Unix() - 120,
			Kind:      nostrx.KindTextNote,
			Content:   "seeded home note",
		},
	} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	canonical := srv.canonicalDefaultLoggedOutGuestFeedRequest()
	data := srv.feedPageDataEx(ctx, canonical, false, feedPageDataOptions{lightStatsOnly: true})
	if len(data.Feed) == 0 {
		t.Fatalf("expected non-empty canonical guest feed, got 0 notes")
	}
	if err := srv.persistDefaultSeedGuestFeedSnapshot(ctx, canonical, &data); err != nil {
		t.Fatalf("persist snapshot: %v", err)
	}
	srv.resetGuestFeedCacheForTest()

	home := httptest.NewRequest(http.MethodGet, "/", nil)
	homeRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(homeRec, home)
	if homeRec.Code != http.StatusOK {
		t.Fatalf("home status = %d, want 200", homeRec.Code)
	}
	body := homeRec.Body.String()
	assertCanonicalLoggedOutHomeShellDeferred(t, body)
	frag := httptest.NewRequest(http.MethodGet, "/?fragment=1", nil)
	fragRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(fragRec, frag)
	if fragRec.Code != http.StatusOK {
		t.Fatalf("fragment status = %d, want 200", fragRec.Code)
	}
	if !strings.Contains(fragRec.Body.String(), "seeded home note") {
		t.Fatalf("expected persisted snapshot in fragment response: %s", fragRec.Body.String())
	}
}

func TestFeedItemsFragmentUsesPersistedSnapshotWhenGuestCacheCold(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	seed, err := nostrx.DecodeIdentifier(defaultLoggedOutWOTSeedNPub)
	if err != nil {
		t.Fatalf("decode default seed: %v", err)
	}
	firstHop := strings.Repeat("b", 64)
	seededNoteID := strings.Repeat("1", 64)
	for _, event := range []nostrx.Event{
		{
			ID:        strings.Repeat("9", 64),
			PubKey:    seed,
			CreatedAt: time.Now().Unix() - 10,
			Kind:      nostrx.KindFollowList,
			Tags:      [][]string{{"p", firstHop}},
		},
		{
			ID:        seededNoteID,
			PubKey:    firstHop,
			CreatedAt: time.Now().Unix() - 120,
			Kind:      nostrx.KindTextNote,
			Content:   "seeded home note",
		},
	} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	canonical := srv.canonicalDefaultLoggedOutGuestFeedRequest()
	data := srv.feedPageDataEx(ctx, canonical, false, feedPageDataOptions{lightStatsOnly: true})
	if len(data.Feed) == 0 {
		t.Fatalf("expected non-empty canonical guest feed, got 0 notes")
	}
	if err := srv.persistDefaultSeedGuestFeedSnapshot(ctx, canonical, &data); err != nil {
		t.Fatalf("persist snapshot: %v", err)
	}
	srv.resetGuestFeedCacheForTest()

	frag := httptest.NewRequest(http.MethodGet, "/?fragment=1", nil)
	fragRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(fragRec, frag)
	if fragRec.Code != http.StatusOK {
		t.Fatalf("fragment status = %d, want 200", fragRec.Code)
	}
	body := fragRec.Body.String()
	if !strings.Contains(body, "seeded home note") {
		t.Fatalf("expected snapshot note in fragment render: %s", body)
	}
	if strings.Contains(body, "No notes found yet.") {
		t.Fatalf("unexpected empty-state copy in persisted fragment render: %s", body)
	}
}

func TestDefaultSeedSnapshotSurvivesEmptyPeriodicRefresh(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	note := nostrx.Event{
		ID:        strings.Repeat("c", 64),
		PubKey:    strings.Repeat("d", 64),
		CreatedAt: time.Now().Unix() - 60,
		Kind:      nostrx.KindTextNote,
		Content:   "snapshot-only note",
	}
	snap := &store.DefaultSeedGuestFeedSnapshot{
		RelaysHash:       "",
		Feed:             []nostrx.Event{note},
		ReferencedEvents: map[string]nostrx.Event{},
		ReplyCounts:      map[string]int{},
		ReactionTotals:   map[string]int{},
		ReactionViewers:  map[string]string{},
		Profiles:         map[string]store.DefaultSeedProfileSnap{},
		Cursor:           0,
		CursorID:         "",
		HasMore:          false,
		ComputedAtUnix:   time.Now().Unix(),
	}
	if err := st.SetDefaultSeedGuestFeedSnapshot(ctx, snap); err != nil {
		t.Fatalf("set snapshot: %v", err)
	}
	srv.tryPeriodicCanonicalDefaultSeedGuestFeed(ctx)
	got, ok, err := st.GetDefaultSeedGuestFeedSnapshot(ctx)
	if err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	if !ok || len(got.Feed) != 1 || got.Feed[0].ID != note.ID {
		t.Fatalf("expected snapshot to survive empty periodic refresh, got ok=%v feed=%v", ok, got)
	}
}

func TestGuestFeedCacheKeyLoggedOutFirehose(t *testing.T) {
	srv, _ := testServer(t)
	ctx := context.Background()
	req := feedRequest{
		Limit:     20,
		SortMode:  "recent",
		Timeframe: "24h",
		WoT:       webOfTrustOptions{Enabled: false, Depth: 1},
	}
	resolved := srv.resolveRequestAuthors(ctx, "", "", nil, req.WoT)
	if !resolved.loggedOut || resolved.wotEnabled {
		t.Fatalf("unexpected resolved state: loggedOut=%v wot=%v", resolved.loggedOut, resolved.wotEnabled)
	}
	key, ok := srv.guestFeedCacheKey(req, resolved, feedSortRecent, "24h", false)
	if !ok || !strings.Contains(key, "|wot:0|") {
		t.Fatalf("expected firehose guest cache key, ok=%v key=%q", ok, key)
	}
}

func TestGuestFeedItemsCacheHitOnSecondDeferredFragment(t *testing.T) {
	srv, _ := testServer(t)
	ctx := context.Background()
	req := feedRequest{
		Limit:     30,
		SortMode:  "recent",
		Timeframe: "24h",
		WoT:       webOfTrustOptions{Enabled: false, Depth: 1},
	}
	_ = srv.feedItemsData(ctx, req)
	_ = srv.feedItemsData(ctx, req)
	snap := srv.metrics.Snapshot()
	counters, ok := snap["counters"].(map[string]int64)
	if !ok {
		t.Fatalf("expected counters map in snapshot %#v", snap)
	}
	if counters["feed.guest_items_cache_hit"] < 1 {
		t.Fatalf("expected guest items cache hit on second call, counters=%v", counters)
	}
}

func TestHandleReactionStatsBatch(t *testing.T) {
	srv, _ := testServer(t)
	id := strings.Repeat("f", 64)
	req := httptest.NewRequest(http.MethodGet, "/api/reaction-stats?id="+id, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var payload map[string]reactionStatsRow
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	row, ok := payload[id]
	if !ok {
		t.Fatalf("expected row for id, payload=%#v", payload)
	}
	if row.Total != 0 {
		t.Fatalf("total = %d, want 0", row.Total)
	}
}

func TestHandleTagPageShowsTaggedNote(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	note := nostrx.Event{
		ID:        "tag-note-one",
		PubKey:    strings.Repeat("c", 64),
		CreatedAt: 1714000000,
		Kind:      nostrx.KindTextNote,
		Content:   "hello #nostrshown",
		Tags:      [][]string{{"t", "nostrshown"}},
	}
	if err := st.SaveEvent(ctx, note); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/tag/nostrshown", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "note-tag-note-one") {
		t.Fatalf("expected note in tag body: %s", body)
	}
	if !strings.Contains(body, "NIP-12") {
		t.Fatalf("expected disclaimer: %s", body)
	}
}

func TestHandleTagInvalidPath404(t *testing.T) {
	srv, _ := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/tag/foo/extra", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestSearchLoggedOutUsesAllCachedNotes(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	notes := []nostrx.Event{
		{
			ID:        "search-alice",
			PubKey:    strings.Repeat("a", 64),
			CreatedAt: 1713000000,
			Kind:      nostrx.KindTextNote,
			Content:   "nostr search alpha",
		},
		{
			ID:        "search-bob",
			PubKey:    strings.Repeat("b", 64),
			CreatedAt: 1713000100,
			Kind:      nostrx.KindTextNote,
			Content:   "nostr search beta",
		},
	}
	for _, note := range notes {
		if err := st.SaveEvent(ctx, note); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/search?q=nostr", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "note-search-alice") || !strings.Contains(body, "note-search-bob") {
		t.Fatalf("expected both cached notes in logged-out search: %s", body)
	}
	if !strings.Contains(body, "Best effort search from this server's local event cache") {
		t.Fatalf("expected cache disclaimer in search output: %s", body)
	}
}

func TestSearchSecondIdenticalRequestHitsCache(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        "search-cache-hit",
		PubKey:    strings.Repeat("a", 64),
		CreatedAt: 1713000200,
		Kind:      nostrx.KindTextNote,
		Content:   "nostr cache verification token",
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/search?q=nostr", nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200", i+1, rec.Code)
		}
	}
	snapshot := srv.metrics.Snapshot()
	counters, _ := snapshot["counters"].(map[string]int64)
	if counters["search.cache.store.miss"] != 1 {
		t.Fatalf("search.cache.store.miss = %d, want 1", counters["search.cache.store.miss"])
	}
	if counters["search.cache.page.hit"] < 1 {
		t.Fatalf("search.cache.page.hit = %d, want >= 1", counters["search.cache.page.hit"])
	}
}

func TestSearchWoTDefaultsToNetworkAndCanExpandToAll(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	viewer := strings.Repeat("f", 64)
	alice := strings.Repeat("a", 64)
	bob := strings.Repeat("b", 64)
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        "viewer-follows",
		PubKey:    viewer,
		CreatedAt: 1713100000,
		Kind:      nostrx.KindFollowList,
		Tags:      [][]string{{"p", alice}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        "alice-hit",
		PubKey:    alice,
		CreatedAt: 1713100010,
		Kind:      nostrx.KindTextNote,
		Content:   "zebra search token",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        "bob-hit",
		PubKey:    bob,
		CreatedAt: 1713100020,
		Kind:      nostrx.KindTextNote,
		Content:   "zebra search token",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkCacheEvent(ctx, "search", "all", "alice-hit"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkCacheEvent(ctx, "search", "all", "bob-hit"); err != nil {
		t.Fatal(err)
	}

	networkReq := httptest.NewRequest(http.MethodGet, "/search?pubkey="+viewer+"&wot=1&wot_depth=1&q=zebra", nil)
	networkRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(networkRec, networkReq)
	if networkRec.Code != http.StatusOK {
		t.Fatalf("network status = %d, want 200", networkRec.Code)
	}
	networkBody := networkRec.Body.String()
	if !strings.Contains(networkBody, "note-alice-hit") {
		t.Fatalf("expected alice hit in network scope: %s", networkBody)
	}
	if strings.Contains(networkBody, "note-bob-hit") {
		t.Fatalf("did not expect bob hit in default network scope: %s", networkBody)
	}
	if !strings.Contains(networkBody, "expand to all cached notes") {
		t.Fatalf("expected expand control in network scope: %s", networkBody)
	}
	if !strings.Contains(networkBody, "Cache window for this scope") {
		t.Fatalf("expected cache window copy in network scope: %s", networkBody)
	}

	allReq := httptest.NewRequest(http.MethodGet, "/search?pubkey="+viewer+"&wot=1&wot_depth=1&q=zebra&scope=all", nil)
	allRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(allRec, allReq)
	if allRec.Code != http.StatusOK {
		t.Fatalf("all status = %d, want 200", allRec.Code)
	}
	allBody := allRec.Body.String()
	if !strings.Contains(allBody, "note-alice-hit") || !strings.Contains(allBody, "note-bob-hit") {
		t.Fatalf("expected both hits in all scope: %s", allBody)
	}
}

func TestFeedDataHydratesReferencedEventsForRepostsAndQuotes(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	viewer := strings.Repeat("d", 64)
	author := strings.Repeat("a", 64)
	reposter := strings.Repeat("b", 64)
	quoter := strings.Repeat("c", 64)
	now := time.Now().Unix()
	originalID := strings.Repeat("1", 64)
	repostID := strings.Repeat("2", 64)
	quoteID := strings.Repeat("3", 64)
	original := nostrx.Event{
		ID:        originalID,
		PubKey:    author,
		CreatedAt: now - 20,
		Kind:      nostrx.KindTextNote,
		Content:   "original note",
	}
	repost := nostrx.Event{
		ID:        repostID,
		PubKey:    reposter,
		CreatedAt: now - 5,
		Kind:      nostrx.KindRepost,
		Content:   "",
		Tags: [][]string{
			{"e", originalID, "wss://relay.example"},
			{"p", author},
		},
	}
	quote := nostrx.Event{
		ID:        quoteID,
		PubKey:    quoter,
		CreatedAt: now - 10,
		Kind:      nostrx.KindTextNote,
		Content:   "quote comment",
		Tags: [][]string{
			{"q", originalID, "wss://relay.example", author},
		},
	}
	for _, event := range []nostrx.Event{original, repost, quote} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        strings.Repeat("4", 64),
		PubKey:    viewer,
		CreatedAt: now - 1,
		Kind:      nostrx.KindFollowList,
		Tags:      [][]string{{"p", reposter}, {"p", quoter}},
	}); err != nil {
		t.Fatal(err)
	}

	data := srv.feedData(ctx, feedRequest{Pubkey: viewer, Limit: 20, Timeframe: "24h", SortMode: "recent"})
	if len(data.Feed) == 0 {
		t.Fatalf("feed should not be empty")
	}
	if _, ok := data.ReferencedEvents[originalID]; !ok {
		t.Fatalf("referenced events missing %s", originalID)
	}
	if got := data.ReferencedEvents[originalID].Content; got != "original note" {
		t.Fatalf("referenced content = %q, want %q", got, "original note")
	}
}

func TestReferencedHydrationFetchesFromRelaysWhenMissingFromStore(t *testing.T) {
	srv, st := newTestServer(t, testServerOptions{
		prefix:         "ref-hydrate-relay",
		requestTimeout: time.Second,
		relayTimeout:   200 * time.Millisecond,
	})
	ctx := context.Background()

	original := fnostr.Event{
		CreatedAt: fnostr.Timestamp(1000),
		Kind:      fnostr.Kind(nostrx.KindTextNote),
		Content:   "only on relay",
	}
	if err := original.Sign(fnostr.Generate()); err != nil {
		t.Fatalf("Sign() original: %v", err)
	}
	originalID := original.ID.Hex()
	authorHex := original.PubKey.Hex()

	relay := newTestRelayREQEventWhenIDsContain(ctx, originalID, original)
	defer relay.Close()

	relayWS := wsURL(relay.URL)
	repost := nostrx.Event{
		ID:        strings.Repeat("2", 64),
		PubKey:    strings.Repeat("b", 64),
		CreatedAt: 1001,
		Kind:      nostrx.KindRepost,
		Content:   "",
		Tags: [][]string{
			{"e", originalID, relayWS},
			{"p", authorHex},
		},
	}
	if err := st.SaveEvent(ctx, repost); err != nil {
		t.Fatal(err)
	}

	ref, _ := srv.referencedHydration(ctx, []nostrx.Event{repost}, []string{relayWS})
	got, ok := ref[originalID]
	if !ok {
		t.Fatalf("referenced events missing %q (map keys)", originalID)
	}
	if got.Content != "only on relay" {
		t.Fatalf("referenced content = %q, want %q", got.Content, "only on relay")
	}
}

func TestUserHeaderFragmentRendersProfileHeader(t *testing.T) {
	srv, st := testServer(t)
	pubkey := strings.Repeat("c", 64)
	if err := st.SaveEvent(context.Background(), nostrx.Event{
		ID:        strings.Repeat("9", 64),
		PubKey:    pubkey,
		CreatedAt: 100,
		Kind:      nostrx.KindProfileMetadata,
		Content:   `{"name":"fragment-user"}`,
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/u/"+pubkey+"?fragment=header", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "fragment-user") || !strings.Contains(body, `class="profile profile-modern"`) {
		t.Fatalf("unexpected header fragment body: %s", body)
	}
}

func TestUserStatsFragmentShowsExactCachedFollowCounts(t *testing.T) {
	srv, st := testServer(t)
	target := strings.Repeat("e", 64)
	followTags := make([][]string, 0, 125)
	for index := 0; index < 125; index++ {
		followTags = append(followTags, []string{"p", fmt.Sprintf("%064x", index+1)})
	}
	if err := st.SaveEvent(context.Background(), nostrx.Event{
		ID:        strings.Repeat("a", 64),
		PubKey:    target,
		CreatedAt: 10,
		Kind:      nostrx.KindFollowList,
		Tags:      followTags,
		Content:   "{}",
	}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 132; index++ {
		owner := fmt.Sprintf("%064x", 1000+index)
		if err := st.SaveEvent(context.Background(), nostrx.Event{
			ID:        fmt.Sprintf("%064x", 2000+index),
			PubKey:    owner,
			CreatedAt: int64(20 + index),
			Kind:      nostrx.KindFollowList,
			Tags:      [][]string{{"p", target}},
			Content:   "{}",
		}); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/u/"+target+"?fragment=stats", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Following") || !strings.Contains(body, "(125)") {
		t.Fatalf("expected exact cached following count, got: %s", body)
	}
	if !strings.Contains(body, "Followed") || !strings.Contains(body, "(132)") {
		t.Fatalf("expected exact cached follower count, got: %s", body)
	}
}

func TestUserFollowingFragmentSupportsSearchAndPagination(t *testing.T) {
	srv, st := testServer(t)
	owner := strings.Repeat("f", 64)
	tags := make([][]string, 0, 130)
	for index := 0; index < 130; index++ {
		target := fmt.Sprintf("%064x", 3000+index)
		tags = append(tags, []string{"p", target})
		if err := st.SaveEvent(context.Background(), nostrx.Event{
			ID:        fmt.Sprintf("%064x", 5000+index),
			PubKey:    target,
			CreatedAt: int64(50 + index),
			Kind:      nostrx.KindProfileMetadata,
			Content:   fmt.Sprintf(`{"display_name":"person-%d"}`, index),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SaveEvent(context.Background(), nostrx.Event{
		ID:        strings.Repeat("b", 64),
		PubKey:    owner,
		CreatedAt: 100,
		Kind:      nostrx.KindFollowList,
		Tags:      tags,
		Content:   "{}",
	}); err != nil {
		t.Fatal(err)
	}

	pageReq := httptest.NewRequest(http.MethodGet, "/u/"+owner+"?fragment=following&following_page=2", nil)
	pageRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(pageRec, pageReq)
	if pageRec.Code != http.StatusOK {
		t.Fatalf("page status = %d, want 200", pageRec.Code)
	}
	body := pageRec.Body.String()
	if !strings.Contains(body, "Page 2") {
		t.Fatalf("expected page indicator, got: %s", body)
	}
	if count := strings.Count(body, `<li><a href="/u/`); count != 30 {
		t.Fatalf("page 2 rendered items = %d, want 30", count)
	}

	searchReq := httptest.NewRequest(http.MethodGet, "/u/"+owner+"?fragment=following&following_q=person-129", nil)
	searchRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(searchRec, searchReq)
	if searchRec.Code != http.StatusOK {
		t.Fatalf("search status = %d, want 200", searchRec.Code)
	}
	searchBody := searchRec.Body.String()
	if !strings.Contains(searchBody, "person-129") {
		t.Fatalf("expected searched profile in body, got: %s", searchBody)
	}
	if count := strings.Count(searchBody, `<li><a href="/u/`); count != 1 {
		t.Fatalf("search rendered items = %d, want 1", count)
	}
}

func TestUserFollowersFragmentUsesCacheScopedCopy(t *testing.T) {
	srv, st := testServer(t)
	target := strings.Repeat("9", 64)
	for index := 0; index < 2; index++ {
		owner := fmt.Sprintf("%064x", 8000+index)
		if err := st.SaveEvent(context.Background(), nostrx.Event{
			ID:        fmt.Sprintf("%064x", 9000+index),
			PubKey:    owner,
			CreatedAt: int64(200 + index),
			Kind:      nostrx.KindFollowList,
			Tags:      [][]string{{"p", target}},
			Content:   "{}",
		}); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/u/"+target+"?fragment=followers", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "cached followers discovered from kind 3 events") {
		t.Fatalf("expected cache-scoped follower copy, got: %s", body)
	}
	if !strings.Contains(body, "Nostr does not provide a reliable network-wide follower total.") {
		t.Fatalf("expected nostr precision caveat, got: %s", body)
	}
}

func TestThreadRepliesFragmentPaginatesAtTwentyFive(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	rootPubkey := strings.Repeat("d", 64)
	rootID := strings.Repeat("1", 64)
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        rootID,
		PubKey:    rootPubkey,
		CreatedAt: 1000,
		Kind:      nostrx.KindTextNote,
		Content:   "root",
	}); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 30; index++ {
		event := nostrx.Event{
			ID:        fmt.Sprintf("%064x", index+10),
			PubKey:    strings.Repeat(fmt.Sprintf("%x", (index%5)+2), 64),
			CreatedAt: int64(1001 + index),
			Kind:      nostrx.KindTextNote,
			Content:   "reply",
			Tags: [][]string{
				{"e", rootID, "", "root"},
				{"e", rootID, "", "reply"},
			},
		}
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	firstReq := httptest.NewRequest(http.MethodGet, "/thread/"+rootID+"?fragment=replies", nil)
	firstRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(firstRec, firstReq)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200", firstRec.Code)
	}
	if got := firstRec.Header().Get("X-Ptxt-Has-More"); got != "1" {
		t.Fatalf("first X-Ptxt-Has-More = %q, want 1", got)
	}
	if count := strings.Count(firstRec.Body.String(), `id="note-`); count != 25 {
		t.Fatalf("first rendered comments = %d, want 25", count)
	}
	cursor := firstRec.Header().Get("X-Ptxt-Cursor")
	cursorID := firstRec.Header().Get("X-Ptxt-Cursor-Id")
	if cursor == "" || cursorID == "" {
		t.Fatalf("expected cursor headers, got cursor=%q cursor_id=%q", cursor, cursorID)
	}

	secondReq := httptest.NewRequest(http.MethodGet, "/thread/"+rootID+"?fragment=replies&cursor="+cursor+"&cursor_id="+cursorID, nil)
	secondRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(secondRec, secondReq)
	if secondRec.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200", secondRec.Code)
	}
	if got := secondRec.Header().Get("X-Ptxt-Has-More"); got != "0" {
		t.Fatalf("second X-Ptxt-Has-More = %q, want 0", got)
	}
	if count := strings.Count(secondRec.Body.String(), `id="note-`); count != 5 {
		t.Fatalf("second rendered comments = %d, want 5", count)
	}
}

func TestThreadFocusUsesDirectChildReplyCounts(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	rootID := strings.Repeat("1", 64)
	selectedID := strings.Repeat("2", 64)
	childID := strings.Repeat("3", 64)
	siblingID := strings.Repeat("4", 64)
	events := []nostrx.Event{
		{
			ID:        rootID,
			PubKey:    strings.Repeat("a", 64),
			CreatedAt: 1000,
			Kind:      nostrx.KindTextNote,
			Content:   "root",
		},
		{
			ID:        selectedID,
			PubKey:    strings.Repeat("b", 64),
			CreatedAt: 1001,
			Kind:      nostrx.KindTextNote,
			Content:   "selected",
			Tags:      [][]string{{"e", rootID, "", "root"}, {"e", rootID, "", "reply"}},
		},
		{
			ID:        childID,
			PubKey:    strings.Repeat("c", 64),
			CreatedAt: 1002,
			Kind:      nostrx.KindTextNote,
			Content:   "selected child",
			Tags:      [][]string{{"e", rootID, "", "root"}, {"e", selectedID, "", "reply"}},
		},
		{
			ID:        siblingID,
			PubKey:    strings.Repeat("d", 64),
			CreatedAt: 1003,
			Kind:      nostrx.KindTextNote,
			Content:   "root sibling",
			Tags:      [][]string{{"e", rootID, "", "root"}, {"e", rootID, "", "reply"}},
		},
	}
	for _, event := range events {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	focusReq := httptest.NewRequest(http.MethodGet, "/thread/"+selectedID+"?fragment=focus", nil)
	focusRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(focusRec, focusReq)
	if focusRec.Code != http.StatusOK {
		t.Fatalf("focus status = %d, want 200", focusRec.Code)
	}
	body := focusRec.Body.String()
	selectedPattern := regexp.MustCompile(`id="note-` + selectedID + `"[^>]*data-ascii-kind="selected"[^>]*data-ascii-reply-count="1"`)
	if !selectedPattern.MatchString(body) {
		t.Fatalf("selected note should show direct child count (1): %s", body)
	}
	rootPattern := regexp.MustCompile(`id="note-` + rootID + `"[^>]*data-ascii-reply-count="2"`)
	if !rootPattern.MatchString(body) {
		t.Fatalf("focused parent should show root direct child count (2): %s", body)
	}

	summaryReq := httptest.NewRequest(http.MethodGet, "/thread/"+selectedID+"?fragment=summary", nil)
	summaryRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(summaryRec, summaryReq)
	if summaryRec.Code != http.StatusOK {
		t.Fatalf("summary status = %d, want 200", summaryRec.Code)
	}
	summaryBody := summaryRec.Body.String()
	assertThreadSummaryOPLink(t, summaryBody, rootID)
	assertThreadSummaryDepth(t, summaryBody, "2")
	if !strings.Contains(summaryBody, "data-thread-tree-toggle") {
		t.Fatalf("summary should include thread tree toggle: %s", summaryBody)
	}
	if !strings.Contains(summaryBody, `data-expanded-label="thread view"`) {
		t.Fatalf("tree toggle should switch back to thread view: %s", summaryBody)
	}
}

func TestThreadPageRendersHiddenAncestorsAboveFocusedNote(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	rootID := strings.Repeat("a", 64)
	topID := strings.Repeat("b", 64)
	parentID := strings.Repeat("c", 64)
	selectedID := strings.Repeat("d", 64)
	childID := strings.Repeat("e", 64)
	events := []nostrx.Event{
		{
			ID:        rootID,
			PubKey:    strings.Repeat("1", 64),
			CreatedAt: 1000,
			Kind:      nostrx.KindTextNote,
			Content:   "root",
		},
		{
			ID:        topID,
			PubKey:    strings.Repeat("2", 64),
			CreatedAt: 1001,
			Kind:      nostrx.KindTextNote,
			Content:   "top",
			Tags:      [][]string{{"e", rootID, "", "root"}, {"e", rootID, "", "reply"}},
		},
		{
			ID:        parentID,
			PubKey:    strings.Repeat("3", 64),
			CreatedAt: 1002,
			Kind:      nostrx.KindTextNote,
			Content:   "parent",
			Tags:      [][]string{{"e", rootID, "", "root"}, {"e", topID, "", "reply"}},
		},
		{
			ID:        selectedID,
			PubKey:    strings.Repeat("4", 64),
			CreatedAt: 1003,
			Kind:      nostrx.KindTextNote,
			Content:   "selected",
			Tags:      [][]string{{"e", rootID, "", "root"}, {"e", parentID, "", "reply"}},
		},
		{
			ID:        childID,
			PubKey:    strings.Repeat("5", 64),
			CreatedAt: 1004,
			Kind:      nostrx.KindTextNote,
			Content:   "selected child",
			Tags:      [][]string{{"e", rootID, "", "root"}, {"e", selectedID, "", "reply"}},
		},
	}
	for _, event := range events {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/thread/"+selectedID, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "show messages above (2)") {
		t.Fatalf("focused thread should include hidden ancestor toggle for OP + chain: %s", body)
	}
	rootIdx := strings.Index(body, `id="note-`+rootID+`"`)
	topIdx := strings.Index(body, `id="note-`+topID+`"`)
	parentIdx := strings.Index(body, `id="note-`+parentID+`"`)
	selectedIdx := strings.Index(body, `id="note-`+selectedID+`"`)
	if rootIdx == -1 || topIdx == -1 || parentIdx == -1 || selectedIdx == -1 {
		t.Fatalf("expected root, top, parent, and selected notes in thread output: %s", body)
	}
	if rootIdx >= topIdx || topIdx >= parentIdx || parentIdx >= selectedIdx {
		t.Fatalf("want DOM order hidden [root,top], then focus parent, then selected")
	}
	if !strings.Contains(body, `data-thread-tree`) {
		t.Fatalf("thread summary should render hidden traversal tree container: %s", body)
	}
	if !strings.Contains(body, `data-thread-tree-note="note-`+topID+`"`) {
		t.Fatalf("thread tree should render clickable lineage notes: %s", body)
	}
}

func TestThreadPageHydratesAvatarForHiddenAncestor(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	rootID := strings.Repeat("a", 64)
	topID := strings.Repeat("b", 64)
	parentID := strings.Repeat("c", 64)
	selectedID := strings.Repeat("d", 64)
	topPubKey := strings.Repeat("2", 64)
	topPicture := "https://example.com/top.png"
	events := []nostrx.Event{
		{
			ID:        rootID,
			PubKey:    strings.Repeat("1", 64),
			CreatedAt: 1000,
			Kind:      nostrx.KindTextNote,
			Content:   "root",
		},
		{
			ID:        topID,
			PubKey:    topPubKey,
			CreatedAt: 1001,
			Kind:      nostrx.KindTextNote,
			Content:   "top",
			Tags:      [][]string{{"e", rootID, "", "root"}, {"e", rootID, "", "reply"}},
		},
		{
			ID:        parentID,
			PubKey:    strings.Repeat("3", 64),
			CreatedAt: 1002,
			Kind:      nostrx.KindTextNote,
			Content:   "parent",
			Tags:      [][]string{{"e", rootID, "", "root"}, {"e", topID, "", "reply"}},
		},
		{
			ID:        selectedID,
			PubKey:    strings.Repeat("4", 64),
			CreatedAt: 1003,
			Kind:      nostrx.KindTextNote,
			Content:   "selected",
			Tags:      [][]string{{"e", rootID, "", "root"}, {"e", parentID, "", "reply"}},
		},
		{
			ID:        "top-profile",
			PubKey:    topPubKey,
			CreatedAt: 999,
			Kind:      nostrx.KindProfileMetadata,
			Content:   `{"name":"jb55","picture":"` + topPicture + `"}`,
		},
	}
	for _, event := range events {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/thread/"+selectedID, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	hiddenAncestorPattern := regexp.MustCompile(`(?s)id="note-` + topID + `".*?<a class="comment-avatar"[^>]*><img src="/avatar/` + topPubKey)
	if !hiddenAncestorPattern.MatchString(body) {
		t.Fatalf("hidden ancestor should render hydrated avatar: %s", body)
	}
}

func TestThreadSummaryKeepsOriginalRootForNestedReplyWithoutExplicitRootTag(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	rootID := strings.Repeat("1", 64)
	aID := strings.Repeat("2", 64)
	bID := strings.Repeat("3", 64)
	cID := strings.Repeat("4", 64)
	events := []nostrx.Event{
		{
			ID:        rootID,
			PubKey:    strings.Repeat("a", 64),
			CreatedAt: 1000,
			Kind:      nostrx.KindTextNote,
			Content:   "root",
		},
		{
			ID:        aID,
			PubKey:    strings.Repeat("b", 64),
			CreatedAt: 1001,
			Kind:      nostrx.KindTextNote,
			Content:   "a",
			Tags:      [][]string{{"e", rootID, "", "root"}},
		},
		{
			ID:        bID,
			PubKey:    strings.Repeat("c", 64),
			CreatedAt: 1002,
			Kind:      nostrx.KindTextNote,
			Content:   "b",
			Tags:      [][]string{{"e", rootID, "", "root"}, {"e", aID, "", "reply"}},
		},
		{
			ID:        cID,
			PubKey:    strings.Repeat("d", 64),
			CreatedAt: 1003,
			Kind:      nostrx.KindTextNote,
			Content:   "c",
			Tags:      [][]string{{"e", bID, "", "reply"}},
		},
	}
	for _, event := range events {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/thread/"+cID+"?fragment=summary", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	assertThreadSummaryOPLink(t, body, rootID)
	assertThreadSummaryDepth(t, body, "4")
	for _, noteID := range []string{rootID, aID, bID, cID} {
		if !strings.Contains(body, `data-thread-tree-note="note-`+noteID+`"`) {
			t.Fatalf("summary missing traversal note %s: %s", noteID, body)
		}
	}
}

func TestThreadSummaryBackParamDoesNotRerootNestedReply(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	rootID := strings.Repeat("5", 64)
	aID := strings.Repeat("6", 64)
	bID := strings.Repeat("7", 64)
	cID := strings.Repeat("8", 64)
	events := []nostrx.Event{
		{
			ID:        rootID,
			PubKey:    strings.Repeat("a", 64),
			CreatedAt: 1000,
			Kind:      nostrx.KindTextNote,
			Content:   "root",
		},
		{
			ID:        aID,
			PubKey:    strings.Repeat("b", 64),
			CreatedAt: 1001,
			Kind:      nostrx.KindTextNote,
			Content:   "a",
			Tags:      [][]string{{"e", rootID, "", "root"}},
		},
		{
			ID:        bID,
			PubKey:    strings.Repeat("c", 64),
			CreatedAt: 1002,
			Kind:      nostrx.KindTextNote,
			Content:   "b",
			Tags:      [][]string{{"e", rootID, "", "root"}, {"e", aID, "", "reply"}},
		},
		{
			ID:        cID,
			PubKey:    strings.Repeat("d", 64),
			CreatedAt: 1003,
			Kind:      nostrx.KindTextNote,
			Content:   "c",
			Tags:      [][]string{{"e", bID, "", "reply"}},
		},
	}
	for _, event := range events {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/thread/"+cID+"?fragment=summary&back="+bID+"&back_note="+cID, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	assertThreadSummaryOPLink(t, body, rootID)
	assertThreadSummaryDepth(t, body, "4")
	if !strings.Contains(body, `/thread/`+bID+`#note-`+cID) {
		t.Fatalf("summary should keep back-to-original-thread link: %s", body)
	}
}

func TestThreadSummaryRepairsBogusExplicitRootMarkerFromAncestorChain(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	rootID := strings.Repeat("9", 64)
	aID := strings.Repeat("a", 64)
	bID := strings.Repeat("b", 64)
	cID := strings.Repeat("c", 64)
	events := []nostrx.Event{
		{
			ID:        rootID,
			PubKey:    strings.Repeat("1", 64),
			CreatedAt: 1000,
			Kind:      nostrx.KindTextNote,
			Content:   "root",
		},
		{
			ID:        aID,
			PubKey:    strings.Repeat("2", 64),
			CreatedAt: 1001,
			Kind:      nostrx.KindTextNote,
			Content:   "a",
			Tags:      [][]string{{"e", rootID, "", "root"}},
		},
		{
			ID:        bID,
			PubKey:    strings.Repeat("3", 64),
			CreatedAt: 1002,
			Kind:      nostrx.KindTextNote,
			Content:   "b",
			Tags:      [][]string{{"e", rootID, "", "root"}, {"e", aID, "", "reply"}},
		},
		{
			ID:        cID,
			PubKey:    strings.Repeat("4", 64),
			CreatedAt: 1003,
			Kind:      nostrx.KindTextNote,
			Content:   "c",
			Tags:      [][]string{{"e", bID, "", "root"}},
		},
	}
	for _, event := range events {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/thread/"+cID+"?fragment=summary", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	assertThreadSummaryOPLink(t, body, rootID)
	assertThreadSummaryDepth(t, body, "4")
	for _, noteID := range []string{rootID, aID, bID, cID} {
		if !strings.Contains(body, `data-thread-tree-note="note-`+noteID+`"`) {
			t.Fatalf("summary missing traversal note %s: %s", noteID, body)
		}
	}
}

func TestThreadRepliesFragmentScopesToSelectedBranch(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	rootID := strings.Repeat("a", 64)
	selectedID := strings.Repeat("b", 64)
	selectedChildID := strings.Repeat("c", 64)
	rootSiblingID := strings.Repeat("d", 64)
	events := []nostrx.Event{
		{
			ID:        rootID,
			PubKey:    strings.Repeat("1", 64),
			CreatedAt: 1000,
			Kind:      nostrx.KindTextNote,
			Content:   "root",
		},
		{
			ID:        selectedID,
			PubKey:    strings.Repeat("2", 64),
			CreatedAt: 1001,
			Kind:      nostrx.KindTextNote,
			Content:   "selected",
			Tags:      [][]string{{"e", rootID, "", "root"}, {"e", rootID, "", "reply"}},
		},
		{
			ID:        selectedChildID,
			PubKey:    strings.Repeat("3", 64),
			CreatedAt: 1002,
			Kind:      nostrx.KindTextNote,
			Content:   "selected child",
			Tags:      [][]string{{"e", rootID, "", "root"}, {"e", selectedID, "", "reply"}},
		},
		{
			ID:        rootSiblingID,
			PubKey:    strings.Repeat("4", 64),
			CreatedAt: 1003,
			Kind:      nostrx.KindTextNote,
			Content:   "root sibling",
			Tags:      [][]string{{"e", rootID, "", "root"}, {"e", rootID, "", "reply"}},
		},
	}
	for _, event := range events {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/thread/"+selectedID+"?fragment=replies", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="note-`+selectedChildID+`"`) {
		t.Fatalf("expected selected branch child in replies fragment: %s", body)
	}
	if strings.Contains(body, `id="note-`+rootSiblingID+`"`) {
		t.Fatalf("replies fragment should not include root siblings in focused branch: %s", body)
	}
}

// Regression: full-page thread runs one BFS for the Reddit-style tree; the linear
// column stays a shallow merged first page (Twitter-style), while the tree still
// shows the full branch including a reply under the URL-selected note.
func TestThreadFullPageSharedReplyWalkIncludesGrandchild(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	rootID := strings.Repeat("1", 64)
	midID := strings.Repeat("2", 64)
	selectedID := strings.Repeat("3", 64)
	childID := strings.Repeat("4", 64)
	pk := strings.Repeat("a", 64)
	events := []nostrx.Event{
		{ID: rootID, PubKey: pk, CreatedAt: 1000, Kind: nostrx.KindTextNote, Content: "root", Sig: "s"},
		{
			ID: midID, PubKey: pk, CreatedAt: 1001, Kind: nostrx.KindTextNote, Content: "mid", Sig: "s",
			Tags: [][]string{{"e", rootID, "", "root"}, {"e", rootID, "", "reply"}},
		},
		{
			ID: selectedID, PubKey: pk, CreatedAt: 1002, Kind: nostrx.KindTextNote, Content: "selected", Sig: "s",
			Tags: [][]string{{"e", rootID, "", "root"}, {"e", midID, "", "reply"}},
		},
		{
			ID: childID, PubKey: pk, CreatedAt: 1003, Kind: nostrx.KindTextNote, Content: "child", Sig: "s",
			Tags: [][]string{{"e", rootID, "", "root"}, {"e", selectedID, "", "reply"}},
		},
	}
	for _, ev := range events {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	fullReq := httptest.NewRequest(http.MethodGet, "/thread/"+selectedID, nil)
	fullRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(fullRec, fullReq)
	if fullRec.Code != http.StatusOK {
		t.Fatalf("full page status = %d, want 200", fullRec.Code)
	}
	fullBody := fullRec.Body.String()
	if !strings.Contains(fullBody, `data-thread-tree-note="note-`+childID+`"`) {
		t.Fatalf("full page should embed tree markup for descendant: %s", fullBody)
	}
	// Child is a direct reply to the URL-selected note; focus mode may show it in
	// #thread-replies (Twitter-style), while nested replies to the OP alone stay shallow.
	if !strings.Contains(fullBody, `id="note-`+childID+`"`) {
		t.Fatalf("expected direct reply to selected in full page: %s", fullBody)
	}

	treeReq := httptest.NewRequest(http.MethodGet, "/thread/"+selectedID+"?fragment=tree", nil)
	treeRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(treeRec, treeReq)
	if treeRec.Code != http.StatusOK {
		t.Fatalf("tree fragment status = %d, want 200", treeRec.Code)
	}
	treeBody := treeRec.Body.String()
	if !strings.Contains(treeBody, `data-thread-tree-note="note-`+childID+`"`) {
		t.Fatalf("tree fragment should include same descendant: %s", treeBody)
	}
}

func TestRelaySuggestionsFragmentUsesCachedNIP65Event(t *testing.T) {
	srv, st := testServer(t)
	pubkey := strings.Repeat("b", 64)
	if err := st.SaveEvent(context.Background(), nostrx.Event{
		ID:        strings.Repeat("1", 64),
		PubKey:    pubkey,
		CreatedAt: 100,
		Kind:      nostrx.KindRelayListMetadata,
		Tags:      [][]string{{"r", "wss://relay.example"}},
		Content:   "",
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/relays?fragment=suggestions&pubkey="+pubkey, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "wss://relay.example") {
		t.Fatalf("suggestions did not include cached relay: %s", rec.Body.String())
	}
}

func TestFeedHeadingFragmentRendersHeadingOnly(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        strings.Repeat("a", 64),
		PubKey:    strings.Repeat("b", 64),
		CreatedAt: time.Now().Unix(),
		Kind:      nostrx.KindTextNote,
		Content:   "note",
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/feed?fragment=heading", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<section class="page-heading">`) {
		t.Fatalf("expected page heading fragment, got: %s", body)
	}
	if strings.Contains(body, `class="note`) {
		t.Fatalf("heading fragment should not include feed note items: %s", body)
	}
}

func TestFeedNewerFragmentReturnsItemsAndCountHeader(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	viewer := strings.Repeat("e", 64)
	author := strings.Repeat("d", 64)
	event := nostrx.Event{
		ID:        strings.Repeat("c", 64),
		PubKey:    author,
		CreatedAt: time.Now().Unix(),
		Kind:      nostrx.KindTextNote,
		Content:   "new note",
	}
	if err := st.SaveEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        strings.Repeat("f", 64),
		PubKey:    viewer,
		CreatedAt: time.Now().Unix(),
		Kind:      nostrx.KindFollowList,
		Tags:      [][]string{{"p", author}},
	}); err != nil {
		t.Fatal(err)
	}

	// Default poll path: count-only response with a 204 so we don't pay the
	// templating cost for the every-30-seconds background ping.
	pollReq := httptest.NewRequest(http.MethodGet, "/feed?fragment=newer&since=0&since_id=&pubkey="+viewer, nil)
	pollRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(pollRec, pollReq)
	if pollRec.Code != http.StatusNoContent {
		t.Fatalf("poll status = %d, want 204", pollRec.Code)
	}
	if got := pollRec.Header().Get("X-Ptxt-New-Count"); got == "" {
		t.Fatalf("missing X-Ptxt-New-Count header on poll response")
	}
	if pollRec.Body.Len() != 0 {
		t.Fatalf("poll body should be empty, got %d bytes", pollRec.Body.Len())
	}

	// Explicit body request (the "Show new notes" click): the rendered HTML
	// is returned alongside the count header.
	bodyReq := httptest.NewRequest(http.MethodGet, "/feed?fragment=newer&since=0&since_id=&body=1&pubkey="+viewer, nil)
	bodyRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(bodyRec, bodyReq)
	if bodyRec.Code != http.StatusOK {
		t.Fatalf("body status = %d, want 200", bodyRec.Code)
	}
	if got := bodyRec.Header().Get("X-Ptxt-New-Count"); got == "" {
		t.Fatalf("missing X-Ptxt-New-Count header on body response")
	}
	if !strings.Contains(bodyRec.Body.String(), event.ID[:12]) {
		t.Fatalf("newer fragment missing expected note content")
	}
}

func TestThreadParticipantsFragmentRendersRail(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	rootID := strings.Repeat("1", 64)
	for _, ev := range []nostrx.Event{
		{
			ID:        rootID,
			PubKey:    strings.Repeat("2", 64),
			CreatedAt: 100,
			Kind:      nostrx.KindTextNote,
			Content:   "root",
		},
		{
			ID:        strings.Repeat("3", 64),
			PubKey:    strings.Repeat("4", 64),
			CreatedAt: 101,
			Kind:      nostrx.KindTextNote,
			Content:   "reply",
			Tags: [][]string{
				{"e", rootID, "", "root"},
				{"e", rootID, "", "reply"},
			},
		},
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/thread/"+rootID+"?fragment=participants", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "People in this thread") {
		t.Fatalf("participants fragment missing expected heading: %s", body)
	}
	if strings.Contains(body, "thread-summary") {
		t.Fatalf("participants fragment should not include thread summary markup: %s", body)
	}
}

func TestThreadHydrateUsesStoreOnlyContext(t *testing.T) {
	srv, st := newTestServer(t, testServerOptions{
		prefix:         "thread-hydrate-root",
		requestTimeout: time.Second,
		relayTimeout:   200 * time.Millisecond,
	})
	ctx := context.Background()

	root := fnostr.Event{
		CreatedAt: fnostr.Timestamp(1000),
		Kind:      fnostr.Kind(nostrx.KindTextNote),
		Content:   "root from relay",
	}
	if err := root.Sign(fnostr.Generate()); err != nil {
		t.Fatalf("Sign() root error = %v", err)
	}

	// SQLite copy of root so hydrate prefers cache over relay body ("root from relay").
	rootCached := nostrx.Event{
		ID:        root.ID.Hex(),
		PubKey:    strings.Repeat("1", 64),
		CreatedAt: 1000,
		Kind:      nostrx.KindTextNote,
		Content:   "root from cache",
		Sig:       "sig",
	}
	if err := st.SaveEvent(ctx, rootCached); err != nil {
		t.Fatal(err)
	}

	replyID := strings.Repeat("b", 64)
	reply := nostrx.Event{
		ID:        replyID,
		PubKey:    strings.Repeat("2", 64),
		CreatedAt: 1001,
		Kind:      nostrx.KindTextNote,
		Content:   "reply from store",
		Tags: [][]string{
			{"e", root.ID.Hex(), "", "root"},
			{"e", root.ID.Hex(), "", "reply"},
		},
	}
	if err := st.SaveEvent(ctx, reply); err != nil {
		t.Fatal(err)
	}

	relay := newTestRelayREQEventWhenIDsContain(ctx, root.ID.Hex(), root)
	defer relay.Close()

	req := httptest.NewRequest(http.MethodGet, "/thread/"+replyID+"?fragment=hydrate&relays="+wsURL(relay.URL), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "reply from store") {
		t.Fatalf("hydrate should still render selected reply content: %s", body)
	}
	if !strings.Contains(body, "root from cache") {
		t.Fatalf("hydrate should render cached root context: %s", body)
	}
	if strings.Contains(body, "root from relay") {
		t.Fatalf("hydrate should prefer SQLite root over relay when both exist: %s", body)
	}
}

func TestThreadPageFetchesMissingRootContext(t *testing.T) {
	srv, st := newTestServer(t, testServerOptions{
		prefix:         "thread-page-root",
		requestTimeout: time.Second,
		relayTimeout:   200 * time.Millisecond,
	})
	ctx := context.Background()

	root := fnostr.Event{
		CreatedAt: fnostr.Timestamp(1000),
		Kind:      fnostr.Kind(nostrx.KindTextNote),
		Content:   "root from relay",
	}
	if err := root.Sign(fnostr.Generate()); err != nil {
		t.Fatalf("Sign() root error = %v", err)
	}

	replyID := strings.Repeat("c", 64)
	reply := nostrx.Event{
		ID:        replyID,
		PubKey:    strings.Repeat("3", 64),
		CreatedAt: 1001,
		Kind:      nostrx.KindTextNote,
		Content:   "reply from store",
		Tags: [][]string{
			{"e", root.ID.Hex(), "", "root"},
			{"e", root.ID.Hex(), "", "reply"},
		},
	}
	if err := st.SaveEvent(ctx, reply); err != nil {
		t.Fatal(err)
	}

	relay := newTestRelayREQEventWhenIDsContain(ctx, root.ID.Hex(), root)
	defer relay.Close()

	req := httptest.NewRequest(http.MethodGet, "/thread/"+replyID+"?relays="+wsURL(relay.URL), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	assertThreadSummaryOPLink(t, body, root.ID.Hex())
	if !strings.Contains(body, "root from relay") {
		t.Fatalf("thread page should render fetched root note content: %s", body)
	}
	if !strings.Contains(body, "reply from store") {
		t.Fatalf("thread page should still render selected reply content: %s", body)
	}
}

// Regression: anonymous ?fragment=hydrate must fetch missing root so focus mode shows parent above reply.
func TestThreadHydrateFetchesMissingRootContext(t *testing.T) {
	srv, st := newTestServer(t, testServerOptions{
		prefix:         "thread-hydrate-missing-root",
		requestTimeout: time.Second,
		relayTimeout:   200 * time.Millisecond,
	})
	ctx := context.Background()

	root := fnostr.Event{
		CreatedAt: fnostr.Timestamp(1000),
		Kind:      fnostr.Kind(nostrx.KindTextNote),
		Content:   "root from relay hydrate",
	}
	if err := root.Sign(fnostr.Generate()); err != nil {
		t.Fatalf("Sign() root error = %v", err)
	}

	replyID := strings.Repeat("d", 64)
	reply := nostrx.Event{
		ID:        replyID,
		PubKey:    strings.Repeat("4", 64),
		CreatedAt: 1001,
		Kind:      nostrx.KindTextNote,
		Content:   "reply hydrate store only",
		Tags: [][]string{
			{"e", root.ID.Hex(), "", "root"},
			{"e", root.ID.Hex(), "", "reply"},
		},
	}
	if err := st.SaveEvent(ctx, reply); err != nil {
		t.Fatal(err)
	}

	relay := newTestRelayREQEventWhenIDsContain(ctx, root.ID.Hex(), root)
	defer relay.Close()

	req := httptest.NewRequest(http.MethodGet, "/thread/"+replyID+"?fragment=hydrate&relays="+wsURL(relay.URL), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("hydrate status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `class="thread-focus"`) {
		t.Fatalf("hydrate should use focused thread layout: %s", body)
	}
	if !strings.Contains(body, "thread-focus-parent") || !strings.Contains(body, "thread-focus-selected") {
		t.Fatalf("hydrate should render parent above selected reply: %s", body)
	}
	if !strings.Contains(body, `data-ascii-kind="selected"`) {
		t.Fatalf("hydrate should mark focused selected note: %s", body)
	}
	if !strings.Contains(body, "root from relay hydrate") {
		t.Fatalf("hydrate should render root fetched from relay: %s", body)
	}
	if !strings.Contains(body, "reply hydrate store only") {
		t.Fatalf("hydrate should render reply from store: %s", body)
	}
}

// Regression: note_links without events rows — hydrate must refetch reply bodies from relays.
func TestThreadHydrateFetchesDirectRepliesMissingFromStore(t *testing.T) {
	srv, st := newTestServer(t, testServerOptions{
		prefix:         "thread-hydrate-relay-replies",
		requestTimeout: time.Second,
		relayTimeout:   200 * time.Millisecond,
	})
	ctx := context.Background()

	rootID := strings.Repeat("a", 64)
	midID := strings.Repeat("b", 64)
	selID := strings.Repeat("c", 64)
	pk := strings.Repeat("1", 64)

	rootEv := nostrx.Event{
		ID:        rootID,
		PubKey:    pk,
		CreatedAt: 1000,
		Kind:      nostrx.KindTextNote,
		Content:   "nested thread root",
		Sig:       "sig",
	}
	midEv := nostrx.Event{
		ID:        midID,
		PubKey:    pk,
		CreatedAt: 1001,
		Kind:      nostrx.KindTextNote,
		Content:   "nested thread mid",
		Sig:       "sig",
		Tags: [][]string{
			{"e", rootID, "", "root"},
			{"e", rootID, "", "reply"},
		},
	}
	selEv := nostrx.Event{
		ID:        selID,
		PubKey:    pk,
		CreatedAt: 1002,
		Kind:      nostrx.KindTextNote,
		Content:   "nested thread selected",
		Sig:       "sig",
		Tags: [][]string{
			{"e", rootID, "", "root"},
			{"e", midID, "", "reply"},
		},
	}
	childTags := fnostr.Tags{
		fnostr.Tag{"e", rootID, "", "root"},
		fnostr.Tag{"e", selID, "", "reply"},
	}
	childContents := []string{"hydrate relay child one", "hydrate relay child two"}
	childTs := []fnostr.Timestamp{1003, 1004}
	relayByID := make(map[string]fnostr.Event, 2)
	var toSave []nostrx.Event
	toSave = append(toSave, rootEv, midEv, selEv)
	for i := range 2 {
		ev := fnostr.Event{
			CreatedAt: childTs[i],
			Kind:      fnostr.Kind(nostrx.KindTextNote),
			Content:   childContents[i],
			Tags:      childTags,
		}
		if err := ev.Sign(fnostr.Generate()); err != nil {
			t.Fatal(err)
		}
		idHex := ev.ID.Hex()
		relayByID[idHex] = ev
		toSave = append(toSave, fnostrToNostrxEvent(ev))
	}
	for _, ev := range toSave {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	childIDs := make([]string, 0, len(relayByID))
	for id := range relayByID {
		childIDs = append(childIDs, id)
	}
	if err := st.DeleteEventsForTesting(ctx, childIDs); err != nil {
		t.Fatal(err)
	}

	relay := newTestRelayREQEventsByIDs(ctx, relayByID)
	defer relay.Close()

	req := httptest.NewRequest(http.MethodGet, "/thread/"+selID+"?fragment=hydrate&relays="+wsURL(relay.URL), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("hydrate status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "hydrate relay child one") || !strings.Contains(body, "hydrate relay child two") {
		t.Fatalf("hydrate should render direct replies fetched from relay: %s", body)
	}
}

func TestFeedDataKeepsHasMoreForThinFreshCache(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	pubkey := strings.Repeat("a", 64)

	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        fmt.Sprintf("%064x", 1),
		PubKey:    pubkey,
		CreatedAt: 100,
		Kind:      nostrx.KindTextNote,
		Content:   "note",
	}); err != nil {
		t.Fatal(err)
	}
	st.MarkRefreshed(ctx, "feed", authorsCacheKey([]string{pubkey}))

	data := srv.feedData(ctx, feedRequest{Pubkey: pubkey, Limit: 30, SortMode: "recent"})
	if len(data.Feed) != 1 {
		t.Fatalf("feed length = %d, want 1", len(data.Feed))
	}
	if !data.HasMore {
		t.Fatalf("hasMore = false, want true for thin cached page")
	}
}

func TestFeedDataNoEventsHasNoMore(t *testing.T) {
	srv, _ := testServer(t)
	pubkey := strings.Repeat("b", 64)

	data := srv.feedData(context.Background(), feedRequest{Pubkey: pubkey, Limit: 30, SortMode: "recent"})
	if data.HasMore {
		t.Fatalf("hasMore = true, want false when feed is empty")
	}
}

func TestAuthorsCacheKeyIsStableHash(t *testing.T) {
	authors := []string{strings.Repeat("b", 64), strings.Repeat("a", 64)}
	got := authorsCacheKey(authors)
	reordered := authorsCacheKey([]string{authors[1], authors[0]})
	if got != reordered {
		t.Fatalf("authorsCacheKey should ignore author order: %q != %q", got, reordered)
	}
	if !strings.HasPrefix(got, "authors:") || len(got) != len("authors:")+64 {
		t.Fatalf("authorsCacheKey = %q, want sha256 key", got)
	}
}

func TestGroupAuthorsForOutboxUsesAuthorHints(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	viewer := strings.Repeat("1", 64)
	authorA := strings.Repeat("2", 64)
	authorB := strings.Repeat("3", 64)
	for _, ev := range []nostrx.Event{
		{
			ID:        strings.Repeat("a", 64),
			PubKey:    authorA,
			CreatedAt: 10,
			Kind:      nostrx.KindRelayListMetadata,
			Tags:      [][]string{{"r", "wss://author-a-write.example", "write"}},
			Content:   "",
			Sig:       "sig",
		},
		{
			ID:        strings.Repeat("b", 64),
			PubKey:    authorB,
			CreatedAt: 11,
			Kind:      nostrx.KindRelayListMetadata,
			Tags:      [][]string{{"r", "wss://author-b-read.example", "read"}},
			Content:   "",
			Sig:       "sig",
		},
		{
			ID:        strings.Repeat("c", 64),
			PubKey:    viewer,
			CreatedAt: 12,
			Kind:      nostrx.KindFollowList,
			Tags:      [][]string{{"p", authorB, "wss://contact-b.example"}},
			Content:   "",
			Sig:       "sig",
		},
		{
			ID:        strings.Repeat("d", 64),
			PubKey:    authorB,
			CreatedAt: 13,
			Kind:      nostrx.KindTextNote,
			Content:   "hello",
			Sig:       "sig",
			RelayURL:  "wss://observed-b.example",
		},
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	groups := srv.groupAuthorsForOutbox(ctx, viewer, []string{authorA, authorB}, []string{"wss://default.example"})
	if len(groups) == 0 {
		t.Fatal("expected outbox groups")
	}
	relaysA := relaysForAuthor(groups, authorA)
	if len(relaysA) == 0 || relaysA[0] != "wss://author-a-write.example" {
		t.Fatalf("authorA relays = %#v", relaysA)
	}
	relaysB := relaysForAuthor(groups, authorB)
	if len(relaysB) == 0 {
		t.Fatalf("authorB relays missing")
	}
	joinedB := strings.Join(relaysB, ",")
	if !strings.Contains(joinedB, "wss://contact-b.example") || !strings.Contains(joinedB, "wss://observed-b.example") {
		t.Fatalf("authorB relays missing contact/observed hints: %#v", relaysB)
	}
}

func TestGroupAuthorsForOutboxAnonymousUsesAuthorWriteHints(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	author := strings.Repeat("4", 64)
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        strings.Repeat("d", 64),
		PubKey:    author,
		CreatedAt: 10,
		Kind:      nostrx.KindRelayListMetadata,
		Tags:      [][]string{{"r", "wss://author-write.example", "write"}},
		Content:   "",
		Sig:       "sig",
	}); err != nil {
		t.Fatal(err)
	}

	groups := srv.groupAuthorsForOutbox(ctx, "", []string{author}, []string{"wss://default.example"})
	relays := relaysForAuthor(groups, author)
	if len(relays) == 0 {
		t.Fatalf("expected relays for anonymous profile refresh")
	}
	if relays[0] != "wss://author-write.example" {
		t.Fatalf("expected author write relay priority, got %#v", relays)
	}
}

func TestPlanPublishRelaysPrioritizesExplicitAndAuthorWriteHints(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	author := strings.Repeat("2", 64)
	rootAuthor := strings.Repeat("3", 64)
	srv.cfg.DefaultRelays = []string{"wss://relay.primal.net", "wss://relay.damus.io"}
	srv.cfg.MetadataRelays = []string{"wss://nos.lol"}
	for _, ev := range []nostrx.Event{
		{
			ID:        strings.Repeat("a", 64),
			PubKey:    author,
			CreatedAt: 10,
			Kind:      nostrx.KindRelayListMetadata,
			Tags:      [][]string{{"r", "wss://author-write.example", "write"}},
		},
		{
			ID:        strings.Repeat("b", 64),
			PubKey:    rootAuthor,
			CreatedAt: 9,
			Kind:      nostrx.KindRelayListMetadata,
			Tags:      [][]string{{"r", "wss://root-write.example", "write"}},
		},
		{
			ID:        strings.Repeat("c", 64),
			PubKey:    rootAuthor,
			CreatedAt: 8,
			Kind:      nostrx.KindTextNote,
			Content:   "root",
		},
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	event := nostrx.Event{
		ID:        strings.Repeat("d", 64),
		PubKey:    author,
		CreatedAt: 11,
		Kind:      nostrx.KindTextNote,
		Tags:      [][]string{{"e", strings.Repeat("c", 64), "", "root"}, {"p", rootAuthor}},
		Content:   "reply",
	}
	req := httptest.NewRequest(http.MethodPost, "/api/events", nil)
	planned := srv.planPublishRelays(ctx, req, event, []string{"wss://explicit.example"})
	if len(planned) == 0 {
		t.Fatal("expected planned relays")
	}
	if planned[0] != "wss://explicit.example" {
		t.Fatalf("planned[0] = %q, want explicit relay first", planned[0])
	}
	joined := strings.Join(planned, ",")
	if !strings.Contains(joined, "wss://author-write.example") {
		t.Fatalf("author write relay missing from plan: %#v", planned)
	}
	if !strings.Contains(joined, "wss://root-write.example") {
		t.Fatalf("thread participant relay missing from plan: %#v", planned)
	}
}

func TestLoggedOutFeedAllowsExplicitRecent(t *testing.T) {
	srv, st := testServer(t)
	srv.cfg.FeedWindow = 7 * 24 * time.Hour
	ctx := context.Background()
	now := time.Now().Unix()
	fresh := nostrx.Event{
		ID:        strings.Repeat("1", 64),
		PubKey:    strings.Repeat("a", 64),
		CreatedAt: now - 60,
		Kind:      nostrx.KindTextNote,
		Content:   "fresh note",
	}
	stale := nostrx.Event{
		ID:        strings.Repeat("2", 64),
		PubKey:    strings.Repeat("b", 64),
		CreatedAt: now - int64(30*24*time.Hour/time.Second),
		Kind:      nostrx.KindTextNote,
		Content:   "stale note",
	}
	for _, ev := range []nostrx.Event{fresh, stale} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	st.MarkRefreshed(ctx, "feed", feedRefreshKey(defaultFeedCacheKey, 0, ""))

	data := srv.feedData(ctx, feedRequest{Limit: 30, SortMode: "recent"})
	if !data.DefaultFeed {
		t.Fatalf("DefaultFeed = false, want true")
	}
	if data.FeedSort != feedSortRecent {
		t.Fatalf("FeedSort = %q, want %q", data.FeedSort, feedSortRecent)
	}
	if got := srv.feedNewerCount(ctx, feedRequest{Limit: 30, SortMode: "recent"}); got != 2 {
		t.Fatalf("feedNewerCount = %d, want 2", got)
	}
}

func TestFeedSortForPubkeyRespectsSessionState(t *testing.T) {
	loggedIn := strings.Repeat("a", 64)
	if got := feedSortForPubkey(loggedIn, ""); got != feedSortRecent {
		t.Fatalf("logged-in default sort = %q, want %q", got, feedSortRecent)
	}
	if got := feedSortForPubkey(loggedIn, "trend24h"); got != feedSortTrend24h {
		t.Fatalf("logged-in trend24h sort = %q, want %q", got, feedSortTrend24h)
	}
	if got := feedSortForPubkey("", ""); got != feedSortRecent {
		t.Fatalf("logged-out default sort = %q, want %q", got, feedSortRecent)
	}
	if got := feedSortForPubkey("", "recent"); got != feedSortRecent {
		t.Fatalf("logged-out recent sort = %q, want %q", got, feedSortRecent)
	}
	if got := feedSortForPubkey("", "trend24h"); got != feedSortTrend24h {
		t.Fatalf("logged-out trend24h sort = %q, want %q", got, feedSortTrend24h)
	}
}

func TestFeedHeadingFragmentShowsChronologicalWhenLoggedOut(t *testing.T) {
	srv, _ := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/feed?fragment=heading", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `value="recent"`) {
		t.Fatalf("logged-out heading should expose chronological option: %s", body)
	}
	if !strings.Contains(body, `value="recent" selected`) {
		t.Fatalf("logged-out heading should default to recent: %s", body)
	}
}

func TestFeedHeadingFragmentKeepsExplicitChronologicalForLoggedOut(t *testing.T) {
	srv, _ := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/feed?fragment=heading&sort=recent", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `value="recent" selected`) {
		t.Fatalf("logged-out heading should keep chronological selected: %s", body)
	}
}

func TestFeedHeadingFragmentKeepsChronologicalForLoggedIn(t *testing.T) {
	srv, _ := testServer(t)
	pubkey := strings.Repeat("a", 64)
	req := httptest.NewRequest(http.MethodGet, "/feed?fragment=heading&pubkey="+pubkey, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `value="recent" selected`) {
		t.Fatalf("logged-in heading should keep chronological selected: %s", body)
	}
}

func TestFeedCursorProgressesAcrossPages(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	pubkey := strings.Repeat("d", 64)
	for index := 0; index < 35; index++ {
		event := nostrx.Event{
			ID:        fmt.Sprintf("%064x", index+1),
			PubKey:    pubkey,
			CreatedAt: int64(1000 - index),
			Kind:      nostrx.KindTextNote,
			Content:   "note",
		}
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	first := srv.feedData(ctx, feedRequest{Pubkey: pubkey, Limit: 30, SortMode: "recent"})
	if len(first.Feed) != 30 || !first.HasMore {
		t.Fatalf("first page len=%d hasMore=%v, want 30 true", len(first.Feed), first.HasMore)
	}
	second := srv.feedData(ctx, feedRequest{Pubkey: pubkey, Cursor: first.Cursor, CursorID: first.CursorID, Limit: 30, SortMode: "recent"})
	if len(second.Feed) != 5 {
		t.Fatalf("second page len=%d, want 5", len(second.Feed))
	}
	if second.Feed[0].ID == first.Feed[len(first.Feed)-1].ID {
		t.Fatalf("cursor did not advance past first page")
	}
}

func TestFeedDataTrendSortRespectsFollowScope(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	now := time.Now().Unix()
	viewer := strings.Repeat("f", 64)
	followed := strings.Repeat("a", 64)
	outsider := strings.Repeat("b", 64)
	followList := nostrx.Event{
		ID:        strings.Repeat("9", 64),
		PubKey:    viewer,
		CreatedAt: now - 10,
		Kind:      nostrx.KindFollowList,
		Tags:      [][]string{{"p", followed}},
	}
	followedNote := nostrx.Event{
		ID:        strings.Repeat("1", 64),
		PubKey:    followed,
		CreatedAt: now - 120,
		Kind:      nostrx.KindTextNote,
		Content:   "followed note",
	}
	outsiderNote := nostrx.Event{
		ID:        strings.Repeat("2", 64),
		PubKey:    outsider,
		CreatedAt: now - 120,
		Kind:      nostrx.KindTextNote,
		Content:   "outsider note",
	}
	for _, event := range []nostrx.Event{
		followList,
		followedNote,
		outsiderNote,
		{
			ID:        strings.Repeat("3", 64),
			PubKey:    strings.Repeat("3", 64),
			CreatedAt: now - 90,
			Kind:      nostrx.KindTextNote,
			Tags:      [][]string{{"e", followedNote.ID, "", "root"}, {"e", followedNote.ID, "", "reply"}},
			Content:   "reply",
		},
		{
			ID:        strings.Repeat("4", 64),
			PubKey:    strings.Repeat("4", 64),
			CreatedAt: now - 80,
			Kind:      nostrx.KindTextNote,
			Tags:      [][]string{{"e", followedNote.ID, "", "root"}, {"e", followedNote.ID, "", "reply"}},
			Content:   "reply",
		},
		{
			ID:        strings.Repeat("5", 64),
			PubKey:    strings.Repeat("5", 64),
			CreatedAt: now - 70,
			Kind:      nostrx.KindTextNote,
			Tags:      [][]string{{"e", outsiderNote.ID, "", "root"}, {"e", outsiderNote.ID, "", "reply"}},
			Content:   "reply",
		},
		{
			ID:        strings.Repeat("6", 64),
			PubKey:    strings.Repeat("6", 64),
			CreatedAt: now - 60,
			Kind:      nostrx.KindTextNote,
			Tags:      [][]string{{"e", outsiderNote.ID, "", "root"}, {"e", outsiderNote.ID, "", "reply"}},
			Content:   "reply",
		},
		{
			ID:        strings.Repeat("7", 64),
			PubKey:    strings.Repeat("7", 64),
			CreatedAt: now - 50,
			Kind:      nostrx.KindTextNote,
			Tags:      [][]string{{"e", outsiderNote.ID, "", "root"}, {"e", outsiderNote.ID, "", "reply"}},
			Content:   "reply",
		},
	} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	data := srv.feedData(ctx, feedRequest{Pubkey: viewer, Limit: 30, Timeframe: "24h", SortMode: "trend24h"})
	if data.FeedSort != "trend24h" {
		t.Fatalf("feed sort = %q, want trend24h", data.FeedSort)
	}
	if len(data.Feed) != 1 {
		t.Fatalf("expected one follow-scoped trend note, got %d", len(data.Feed))
	}
	if data.Feed[0].ID != followedNote.ID {
		t.Fatalf("unexpected trend note id = %q", data.Feed[0].ID)
	}
}

func TestFeedDataLoggedOutSeededTrendSortRespectsScope(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	now := time.Now().Unix()
	seed := strings.Repeat("a", 64)
	firstHop := strings.Repeat("b", 64)
	outsider := strings.Repeat("c", 64)
	for _, event := range []nostrx.Event{
		{
			ID:        strings.Repeat("1", 64),
			PubKey:    seed,
			CreatedAt: now - 10,
			Kind:      nostrx.KindFollowList,
			Tags:      [][]string{{"p", firstHop}},
		},
		{
			ID:        strings.Repeat("2", 64),
			PubKey:    firstHop,
			CreatedAt: now - 120,
			Kind:      nostrx.KindTextNote,
			Content:   "seeded note",
		},
		{
			ID:        strings.Repeat("3", 64),
			PubKey:    outsider,
			CreatedAt: now - 120,
			Kind:      nostrx.KindTextNote,
			Content:   "outsider note",
		},
		{
			ID:        strings.Repeat("4", 64),
			PubKey:    seed,
			CreatedAt: now - 90,
			Kind:      nostrx.KindTextNote,
			Tags:      [][]string{{"e", strings.Repeat("2", 64), "", "root"}, {"e", strings.Repeat("2", 64), "", "reply"}},
			Content:   "seeded reply",
		},
		{
			ID:        strings.Repeat("5", 64),
			PubKey:    outsider,
			CreatedAt: now - 80,
			Kind:      nostrx.KindTextNote,
			Tags:      [][]string{{"e", strings.Repeat("3", 64), "", "root"}, {"e", strings.Repeat("3", 64), "", "reply"}},
			Content:   "outsider reply 1",
		},
		{
			ID:        strings.Repeat("6", 64),
			PubKey:    outsider,
			CreatedAt: now - 70,
			Kind:      nostrx.KindTextNote,
			Tags:      [][]string{{"e", strings.Repeat("3", 64), "", "root"}, {"e", strings.Repeat("3", 64), "", "reply"}},
			Content:   "outsider reply 2",
		},
	} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	data := srv.feedData(ctx, feedRequest{
		SeedPubkey: seed,
		Limit:      30,
		Timeframe:  "24h",
		SortMode:   "trend24h",
		WoT:        webOfTrustOptions{Enabled: true, Depth: 1},
	})
	if len(data.Feed) != 1 {
		t.Fatalf("expected one seeded trend note, got %d", len(data.Feed))
	}
	if data.Feed[0].Content != "seeded note" {
		t.Fatalf("unexpected seeded trend note: %#v", data.Feed[0])
	}
}

func TestFeedDataLoggedOutSeededTrendColdCacheReturnsEmptyWithoutBlocking(t *testing.T) {
	// Anonymous WoT requests must not block on relay round-trips. With a
	// cold trending cache, the request returns whatever the local store
	// has (empty) and the background warmer/crawler is responsible for
	// filling cache for subsequent requests.
	srv, st := newTestServer(t, testServerOptions{relayTimeout: 50 * time.Millisecond})
	ctx := context.Background()

	firstHopSecret := fnostr.Generate()
	firstHopNote := fnostr.Event{
		CreatedAt: fnostr.Now(),
		Kind:      fnostr.Kind(nostrx.KindTextNote),
		Content:   "relay seeded note",
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

	seedEvent := fnostrToNostrxEvent(seedFollow)
	seed := seedEvent.PubKey
	if err := st.SaveEvent(ctx, seedEvent); err != nil {
		t.Fatal(err)
	}
	relay := newRelayWithEvents(t, []nostrx.Event{fnostrToNostrxEvent(firstHopNote)})
	defer relay.Close()

	started := time.Now()
	data := srv.feedData(ctx, feedRequest{
		SeedPubkey: seed,
		Limit:      20,
		Relays:     []string{wsURL(relay.URL)},
		Timeframe:  "24h",
		SortMode:   "trend24h",
		WoT:        webOfTrustOptions{Enabled: true, Depth: 1},
	})
	elapsed := time.Since(started)
	if len(data.Feed) != 0 {
		t.Fatalf("expected empty feed on cold cache, got %d notes", len(data.Feed))
	}
	// Sanity: must not be anywhere near the prior 2s synchronous timeout.
	if elapsed > 250*time.Millisecond {
		t.Fatalf("cold cache request blocked too long: %s", elapsed)
	}
}

func TestFeedDataLoggedOutSeededTrendMissingFollowGraphReturnsEmptyFast(t *testing.T) {
	// With no follow list cached for the seed, the request must still
	// return immediately without performing a synchronous relay fetch or
	// graph sync. The seed crawler/bootstrap path is responsible for
	// filling the follow graph in the background.
	srv, _ := newTestServer(t, testServerOptions{relayTimeout: 50 * time.Millisecond})
	ctx := context.Background()

	firstHopSecret := fnostr.Generate()
	firstHopNote := fnostr.Event{
		CreatedAt: fnostr.Now(),
		Kind:      fnostr.Kind(nostrx.KindTextNote),
		Content:   "relay cold-start note",
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

	relay := newRelayWithEvents(t, []nostrx.Event{
		fnostrToNostrxEvent(seedFollow),
		fnostrToNostrxEvent(firstHopNote),
	})
	defer relay.Close()

	started := time.Now()
	data := srv.feedData(ctx, feedRequest{
		SeedPubkey: seedFollow.PubKey.Hex(),
		Limit:      20,
		Relays:     []string{wsURL(relay.URL)},
		Timeframe:  "24h",
		SortMode:   "trend24h",
		WoT:        webOfTrustOptions{Enabled: true, Depth: 1},
	})
	elapsed := time.Since(started)
	if len(data.Feed) != 0 {
		t.Fatalf("expected empty feed when follow graph is missing, got %d notes", len(data.Feed))
	}
	if elapsed > 250*time.Millisecond {
		t.Fatalf("cold-start request blocked on relay: %s", elapsed)
	}
}

func TestPrewarmDefaultLoggedOutSeedPopulatesSeededTrending(t *testing.T) {
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

	replySecrets := []fnostr.SecretKey{fnostr.Generate(), fnostr.Generate()}
	replyContents := []string{"bootstrap reply one", "bootstrap reply two"}
	replyEvents := make([]nostrx.Event, 0, len(replySecrets))
	for index, secret := range replySecrets {
		reply := fnostr.Event{
			CreatedAt: fnostr.Now(),
			Kind:      fnostr.Kind(nostrx.KindTextNote),
			Tags: fnostr.Tags{
				fnostr.Tag{"e", firstHopNote.ID.Hex(), "", "root"},
				fnostr.Tag{"e", firstHopNote.ID.Hex(), "", "reply"},
			},
			Content: replyContents[index],
		}
		if err := reply.Sign(secret); err != nil {
			t.Fatalf("Sign() reply %d error = %v", index, err)
		}
		replyEvents = append(replyEvents, fnostrToNostrxEvent(reply))
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
	relayEvents = append(relayEvents, replyEvents...)
	relay := newRelayWithEvents(t, relayEvents)
	defer relay.Close()

	relayURL := wsURL(relay.URL)
	srv.cfg.DefaultRelays = []string{relayURL}
	srv.cfg.MetadataRelays = []string{relayURL}

	seedNPub := nostrx.EncodeNPub(seedFollow.PubKey.Hex())
	if err := srv.prewarmLoggedOutSeedNow(ctx, seedNPub, defaultLoggedOutWOTDepth); err != nil {
		t.Fatalf("prewarmLoggedOutSeedNow() error = %v", err)
	}
	srv.crawlSeedTick()

	seedPubkey, err := nostrx.DecodeIdentifier(seedNPub)
	if err != nil {
		t.Fatalf("DecodeIdentifier(seedNPub) error = %v", err)
	}
	follows, err := srv.store.FollowingPubkeys(ctx, seedPubkey, 10)
	if err != nil {
		t.Fatalf("FollowingPubkeys() error = %v", err)
	}
	if len(follows) == 0 {
		t.Fatal("expected prewarmed follow graph")
	}
	authors, _, loggedOut := srv.resolveAuthorsAll(ctx, seedNPub, nil, webOfTrustOptions{Enabled: true, Depth: 1})
	if loggedOut || len(authors) == 0 {
		t.Fatalf("resolveAuthorsAll() loggedOut=%v len=%d", loggedOut, len(authors))
	}
	cohortKey := authorsCacheKey(authors)
	now := time.Now()
	if _, err := srv.computeAndStoreCohortTrending(ctx, trending24h, cohortKey, authors, now); err != nil {
		t.Fatalf("computeAndStoreCohortTrending 24h error = %v", err)
	}
	if _, err := srv.computeAndStoreCohortTrending(ctx, trending1w, cohortKey, authors, now); err != nil {
		t.Fatalf("computeAndStoreCohortTrending 1w error = %v", err)
	}
	trendingRows, err := srv.store.TrendingSummariesByKinds(ctx, noteTimelineKinds, time.Now().Add(-24*time.Hour).Unix(), []string{firstHopNote.PubKey.Hex()}, 0, 10)
	if err != nil {
		t.Fatalf("TrendingSummariesByKinds() error = %v", err)
	}
	if len(trendingRows) == 0 {
		t.Fatal("expected prewarmed trending rows for seed cohort")
	}

	data := srv.feedData(ctx, feedRequest{
		SeedPubkey: seedNPub,
		Limit:      20,
		Timeframe:  "24h",
		SortMode:   "trend24h",
		WoT:        webOfTrustOptions{Enabled: true, Depth: defaultLoggedOutWOTDepth},
	})
	if len(data.Feed) != 1 {
		t.Fatalf("expected one prewarmed seeded trend note, got %d", len(data.Feed))
	}
	if data.Feed[0].Content != "bootstrap seeded note" {
		t.Fatalf("unexpected prewarmed seeded trend note: %#v", data.Feed[0])
	}
}

func TestFeedDataDefaultSeedTrendFallsBackToGlobalAndSkipsEmptyGuestCache(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	req := feedRequest{
		SeedPubkey: defaultLoggedOutWOTSeedNPub,
		Limit:      20,
		Timeframe:  "24h",
		SortMode:   "trend24h",
		WoT:        webOfTrustOptions{Enabled: true, Depth: defaultLoggedOutWOTDepth},
	}

	empty := srv.feedData(ctx, req)
	if len(empty.Feed) != 0 {
		t.Fatalf("expected empty initial feed, got %#v", empty.Feed)
	}
	resolved := srv.resolveRequestAuthors(ctx, req.Pubkey, req.SeedPubkey, req.Relays, req.WoT)
	cacheKey, ok := srv.guestFeedCacheKey(req, resolved, normalizeFeedSort(req.SortMode), normalizeTrendingTimeframe(req.Timeframe), true)
	if !ok || cacheKey == "" {
		t.Fatalf("expected guest feed cache key, got ok=%v key=%q", ok, cacheKey)
	}
	if _, hit := srv.guestFeedCache.get(cacheKey, time.Now()); hit {
		t.Fatal("expected empty cache-only guest trend result to skip guest page cache")
	}

	now := time.Now().Unix()
	note := nostrx.Event{
		ID:        strings.Repeat("a", 64),
		PubKey:    strings.Repeat("1", 64),
		CreatedAt: now - 120,
		Kind:      nostrx.KindTextNote,
		Content:   "global fallback trend note",
	}
	replyOne := nostrx.Event{
		ID:        strings.Repeat("b", 64),
		PubKey:    strings.Repeat("2", 64),
		CreatedAt: now - 60,
		Kind:      nostrx.KindTextNote,
		Tags:      [][]string{{"e", note.ID, "", "root"}, {"e", note.ID, "", "reply"}},
		Content:   "reply one",
	}
	replyTwo := nostrx.Event{
		ID:        strings.Repeat("c", 64),
		PubKey:    strings.Repeat("3", 64),
		CreatedAt: now - 30,
		Kind:      nostrx.KindTextNote,
		Tags:      [][]string{{"e", note.ID, "", "root"}, {"e", note.ID, "", "reply"}},
		Content:   "reply two",
	}
	for _, event := range []nostrx.Event{note, replyOne, replyTwo} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	fallback := srv.feedData(ctx, req)
	if len(fallback.Feed) != 1 {
		t.Fatalf("expected one global fallback trend note, got %#v", fallback.Feed)
	}
	if fallback.Feed[0].ID != note.ID {
		t.Fatalf("fallback feed note id = %q, want %q", fallback.Feed[0].ID, note.ID)
	}
}

func TestResolveAuthorsUsesWebOfTrustDepth(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	viewer := strings.Repeat("a", 64)
	firstHop := strings.Repeat("b", 64)
	secondHop := strings.Repeat("c", 64)
	thirdHop := strings.Repeat("d", 64)
	for _, ev := range []nostrx.Event{
		{ID: strings.Repeat("1", 64), PubKey: viewer, CreatedAt: 10, Kind: nostrx.KindFollowList, Tags: [][]string{{"p", firstHop}}},
		{ID: strings.Repeat("2", 64), PubKey: firstHop, CreatedAt: 11, Kind: nostrx.KindFollowList, Tags: [][]string{{"p", secondHop}}},
		{ID: strings.Repeat("3", 64), PubKey: secondHop, CreatedAt: 12, Kind: nostrx.KindFollowList, Tags: [][]string{{"p", thirdHop}}},
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	depthTwo, _, _ := srv.resolveAuthors(ctx, viewer, nil, webOfTrustOptions{Enabled: true, Depth: 2})
	gotTwo := strings.Join(depthTwo, ",")
	if !strings.Contains(gotTwo, firstHop) || !strings.Contains(gotTwo, secondHop) || !strings.Contains(gotTwo, viewer) {
		t.Fatalf("depth 2 authors = %#v", depthTwo)
	}
	if strings.Contains(gotTwo, thirdHop) {
		t.Fatalf("depth 2 authors should exclude third hop: %#v", depthTwo)
	}

	depthThree, _, _ := srv.resolveAuthors(ctx, viewer, nil, webOfTrustOptions{Enabled: true, Depth: 3})
	gotThree := strings.Join(depthThree, ",")
	if !strings.Contains(gotThree, thirdHop) {
		t.Fatalf("depth 3 authors should include third hop: %#v", depthThree)
	}
}

func TestFeedHandlerRespectsWebOfTrustParams(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	viewer := strings.Repeat("f", 64)
	firstHop := strings.Repeat("1", 64)
	secondHop := strings.Repeat("2", 64)
	outsider := strings.Repeat("3", 64)
	for _, ev := range []nostrx.Event{
		{ID: strings.Repeat("a", 64), PubKey: viewer, CreatedAt: 10, Kind: nostrx.KindFollowList, Tags: [][]string{{"p", firstHop}}},
		{ID: strings.Repeat("b", 64), PubKey: firstHop, CreatedAt: 11, Kind: nostrx.KindFollowList, Tags: [][]string{{"p", secondHop}}},
		{ID: strings.Repeat("c", 64), PubKey: firstHop, CreatedAt: 20, Kind: nostrx.KindTextNote, Content: "first hop"},
		{ID: strings.Repeat("d", 64), PubKey: secondHop, CreatedAt: 21, Kind: nostrx.KindTextNote, Content: "second hop"},
		{ID: strings.Repeat("e", 64), PubKey: outsider, CreatedAt: 22, Kind: nostrx.KindTextNote, Content: "outsider"},
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/feed?pubkey="+viewer+"&wot=1&wot_depth=2", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "first hop") || !strings.Contains(body, "second hop") {
		t.Fatalf("expected reachable notes in body: %s", body)
	}
	if strings.Contains(body, "outsider") {
		t.Fatalf("unexpected outsider note in body: %s", body)
	}
}

func TestFeedHandlerUsesFullWOTMembershipBeyondRefreshCap(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	viewer := strings.Repeat("f", 64)
	firstHop := strings.Repeat("1", 64)
	target := fmt.Sprintf("%064x", 399)
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        strings.Repeat("a", 64),
		PubKey:    viewer,
		CreatedAt: 10,
		Kind:      nostrx.KindFollowList,
		Tags:      [][]string{{"p", firstHop}},
	}); err != nil {
		t.Fatal(err)
	}
	followTags := make([][]string, 0, 300)
	for i := 0; i < 300; i++ {
		pubkey := fmt.Sprintf("%064x", i+100)
		followTags = append(followTags, []string{"p", pubkey})
	}
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        strings.Repeat("b", 64),
		PubKey:    firstHop,
		CreatedAt: 11,
		Kind:      nostrx.KindFollowList,
		Tags:      followTags,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        strings.Repeat("c", 64),
		PubKey:    target,
		CreatedAt: 20,
		Kind:      nostrx.KindTextNote,
		Content:   "beyond refresh cap",
	}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/feed?pubkey="+viewer+"&wot=1&wot_depth=2", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "beyond refresh cap") {
		t.Fatalf("expected WoT scan to include note beyond capped refresh authors: %s", rec.Body.String())
	}
}

func TestFetchScannedFeedPageFallsBackToAuthorQueryWhenScanIsEmpty(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	viewer := strings.Repeat("f", 64)
	firstHop := strings.Repeat("1", 64)
	secondHop := strings.Repeat("2", 64)
	for _, ev := range []nostrx.Event{
		{ID: strings.Repeat("a", 64), PubKey: viewer, CreatedAt: 10, Kind: nostrx.KindFollowList, Tags: [][]string{{"p", firstHop}}},
		{ID: strings.Repeat("b", 64), PubKey: firstHop, CreatedAt: 11, Kind: nostrx.KindFollowList, Tags: [][]string{{"p", secondHop}}},
		{ID: strings.Repeat("c", 64), PubKey: firstHop, CreatedAt: 20, Kind: nostrx.KindTextNote, Content: "first hop"},
		{ID: strings.Repeat("d", 64), PubKey: secondHop, CreatedAt: 21, Kind: nostrx.KindTextNote, Content: "second hop"},
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	authors, _, _ := srv.resolveAuthors(ctx, viewer, nil, webOfTrustOptions{Enabled: true, Depth: 2})
	events, hasMore := srv.fetchScannedFeedPage(ctx, viewer, authors, authorMembership{}, 0, "", 10, nil, "feed", authorsCacheKey(authors))
	if len(events) != 2 {
		t.Fatalf("expected fallback notes, got %d", len(events))
	}
	if !hasMore {
		t.Fatalf("expected fallback path to keep pagination open")
	}
	body := events[0].Content + "|" + events[1].Content
	if !strings.Contains(body, "first hop") || !strings.Contains(body, "second hop") {
		t.Fatalf("fallback missed reachable notes: %q", body)
	}
}

func TestFetchScannedFeedPageScansPastFirstStoreBatch(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	matchAuthor := strings.Repeat("a", 64)
	otherAuthor := strings.Repeat("b", 64)
	for i := 0; i < 90; i++ {
		pubkey := otherAuthor
		content := "other"
		if i >= 50 {
			pubkey = matchAuthor
			content = fmt.Sprintf("match-%d", i)
		}
		event := nostrx.Event{
			ID:        fmt.Sprintf("%064x", 1000-i),
			PubKey:    pubkey,
			CreatedAt: int64(1000 - i),
			Kind:      nostrx.KindTextNote,
			Content:   content,
		}
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	events, hasMore := srv.fetchScannedFeedPage(ctx, matchAuthor, []string{matchAuthor}, newAuthorMembership([]string{matchAuthor}), 0, "", 30, nil, "feed", authorsCacheKey([]string{matchAuthor}))
	if len(events) != 31 {
		t.Fatalf("expected 31 events from multi-batch scan, got %d", len(events))
	}
	if !hasMore {
		t.Fatalf("expected hasMore when matches continue past first batch")
	}
	if !strings.Contains(events[0].Content, "match-") {
		t.Fatalf("expected matched content, got %q", events[0].Content)
	}
}

func TestServerBootstrapsEmptyWOTGraphFromSQLiteOnStartup(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(root, "bootstrap.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	viewer := strings.Repeat("a", 64)
	firstHop := strings.Repeat("b", 64)
	secondHop := strings.Repeat("c", 64)
	for _, ev := range []nostrx.Event{
		{ID: strings.Repeat("1", 64), PubKey: viewer, CreatedAt: 10, Kind: nostrx.KindFollowList, Tags: [][]string{{"p", firstHop}}},
		{ID: strings.Repeat("2", 64), PubKey: firstHop, CreatedAt: 11, Kind: nostrx.KindFollowList, Tags: [][]string{{"p", secondHop}}},
		{ID: strings.Repeat("3", 64), PubKey: secondHop, CreatedAt: 12, Kind: nostrx.KindTextNote, Content: "second hop"},
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	srv, err := New(config.Config{RequestTimeout: time.Second, WOTMaxAuthors: 240}, st, nostrx.NewClient(nil, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	deadline := time.Now().Add(2 * time.Second)
	for {
		authors, _, _ := srv.resolveAuthors(ctx, viewer, nil, webOfTrustOptions{Enabled: true, Depth: 2})
		if strings.Contains(strings.Join(authors, ","), secondHop) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("startup bootstrap never populated second hop; authors=%#v", authors)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestWoTReachabilityUsesSQLiteFollowEdges(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(root, "wot.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	viewer := strings.Repeat("a", 64)
	firstHop := strings.Repeat("b", 64)
	secondHop := strings.Repeat("c", 64)
	for _, ev := range []nostrx.Event{
		{ID: strings.Repeat("1", 64), PubKey: viewer, CreatedAt: 10, Kind: nostrx.KindFollowList, Tags: [][]string{{"p", firstHop}}},
		{ID: strings.Repeat("2", 64), PubKey: firstHop, CreatedAt: 11, Kind: nostrx.KindFollowList, Tags: [][]string{{"p", secondHop}}},
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	srv, err := New(config.Config{RequestTimeout: time.Second, WOTMaxAuthors: 240}, st, nostrx.NewClient(nil, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	authors, _, _ := srv.resolveAuthors(ctx, viewer, nil, webOfTrustOptions{Enabled: true, Depth: 2})
	if !strings.Contains(strings.Join(authors, ","), secondHop) {
		t.Fatalf("expected WoT authors to include second hop from follow_edges, authors=%#v", authors)
	}
}

func TestReadsDataFiltersLongFormAndMetadataFallback(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	now := time.Now().Unix()
	author := strings.Repeat("a", 64)
	other := strings.Repeat("b", 64)
	withMeta := nostrx.Event{
		ID:        strings.Repeat("1", 64),
		PubKey:    author,
		CreatedAt: now - 120,
		Kind:      nostrx.KindLongForm,
		Tags: [][]string{
			{"title", "Plain Nostr Article"},
			{"published_at", "1700000000"},
		},
		Content: "# Heading\n\nBody copy",
	}
	fallback := nostrx.Event{
		ID:        strings.Repeat("2", 64),
		PubKey:    other,
		CreatedAt: now - 180,
		Kind:      nostrx.KindLongForm,
		Content:   "First line title\n\nAnother paragraph",
	}
	note := nostrx.Event{
		ID:        strings.Repeat("3", 64),
		PubKey:    author,
		CreatedAt: now - 60,
		Kind:      nostrx.KindTextNote,
		Content:   "kind 1 note",
	}
	for _, event := range []nostrx.Event{withMeta, fallback, note} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	st.MarkRefreshed(ctx, "reads", feedRefreshKey(readsCacheKey, 0, ""))

	data := srv.readsData(ctx, feedRequest{Limit: 20, SortMode: "recent"}, "24h")
	if len(data.Items) != 2 {
		t.Fatalf("len(reads items) = %d, want 2", len(data.Items))
	}
	if data.Items[0].Event.ID != withMeta.ID {
		t.Fatalf("first read id = %q, want %q", data.Items[0].Event.ID, withMeta.ID)
	}
	if data.Items[0].Title != "Plain Nostr Article" {
		t.Fatalf("tag title = %q", data.Items[0].Title)
	}
	if data.Items[0].PublishedAt != 1700000000 {
		t.Fatalf("published_at = %d, want 1700000000", data.Items[0].PublishedAt)
	}
	if data.Items[1].Title != "First line title" {
		t.Fatalf("fallback title = %q, want first content line", data.Items[1].Title)
	}
	if data.Items[1].PublishedAt != fallback.CreatedAt {
		t.Fatalf("fallback published date = %d, want created_at %d", data.Items[1].PublishedAt, fallback.CreatedAt)
	}
}

func TestReadsHandlerRespectsWebOfTrustParams(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	viewer := strings.Repeat("f", 64)
	firstHop := strings.Repeat("1", 64)
	secondHop := strings.Repeat("2", 64)
	outsider := strings.Repeat("3", 64)
	for _, ev := range []nostrx.Event{
		{ID: strings.Repeat("a", 64), PubKey: viewer, CreatedAt: 10, Kind: nostrx.KindFollowList, Tags: [][]string{{"p", firstHop}}},
		{ID: strings.Repeat("b", 64), PubKey: firstHop, CreatedAt: 11, Kind: nostrx.KindFollowList, Tags: [][]string{{"p", secondHop}}},
		{ID: strings.Repeat("c", 64), PubKey: firstHop, CreatedAt: 20, Kind: nostrx.KindLongForm, Tags: [][]string{{"title", "first hop read"}}, Content: "first hop read"},
		{ID: strings.Repeat("d", 64), PubKey: secondHop, CreatedAt: 21, Kind: nostrx.KindLongForm, Tags: [][]string{{"title", "second hop read"}}, Content: "second hop read"},
		{ID: strings.Repeat("e", 64), PubKey: outsider, CreatedAt: 22, Kind: nostrx.KindLongForm, Tags: [][]string{{"title", "outsider read"}}, Content: "outsider read"},
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/reads?pubkey="+viewer+"&wot=1&wot_depth=2", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "first hop read") || !strings.Contains(body, "second hop read") {
		t.Fatalf("expected reachable reads in body: %s", body)
	}
	if strings.Contains(body, "outsider read") {
		t.Fatalf("unexpected outsider read in body: %s", body)
	}
}

func TestReadsHandlerUsesFullWOTMembershipBeyondRefreshCap(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	viewer := strings.Repeat("f", 64)
	firstHop := strings.Repeat("1", 64)
	target := fmt.Sprintf("%064x", 399)
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        strings.Repeat("a", 64),
		PubKey:    viewer,
		CreatedAt: 10,
		Kind:      nostrx.KindFollowList,
		Tags:      [][]string{{"p", firstHop}},
	}); err != nil {
		t.Fatal(err)
	}
	followTags := make([][]string, 0, 300)
	for i := 0; i < 300; i++ {
		pubkey := fmt.Sprintf("%064x", i+100)
		followTags = append(followTags, []string{"p", pubkey})
	}
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        strings.Repeat("b", 64),
		PubKey:    firstHop,
		CreatedAt: 11,
		Kind:      nostrx.KindFollowList,
		Tags:      followTags,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        strings.Repeat("c", 64),
		PubKey:    target,
		CreatedAt: 20,
		Kind:      nostrx.KindLongForm,
		Tags:      [][]string{{"title", "beyond reads refresh cap"}},
		Content:   "beyond reads refresh cap",
	}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/reads?pubkey="+viewer+"&wot=1&wot_depth=2", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "beyond reads refresh cap") {
		t.Fatalf("expected WoT read scan to include read beyond capped refresh authors: %s", rec.Body.String())
	}
}

func TestReadsHandlerRespectsSeededLoggedOutWebOfTrustParams(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	now := time.Now().Unix()
	seed := strings.Repeat("f", 64)
	firstHop := strings.Repeat("1", 64)
	secondHop := strings.Repeat("2", 64)
	outsider := strings.Repeat("3", 64)
	for _, ev := range []nostrx.Event{
		{ID: strings.Repeat("a", 64), PubKey: seed, CreatedAt: now - 300, Kind: nostrx.KindFollowList, Tags: [][]string{{"p", firstHop}}},
		{ID: strings.Repeat("b", 64), PubKey: firstHop, CreatedAt: now - 290, Kind: nostrx.KindFollowList, Tags: [][]string{{"p", secondHop}}},
		{ID: strings.Repeat("c", 64), PubKey: firstHop, CreatedAt: now - 200, Kind: nostrx.KindLongForm, Tags: [][]string{{"title", "seed first hop read"}}, Content: "seed first hop read"},
		{ID: strings.Repeat("d", 64), PubKey: secondHop, CreatedAt: now - 190, Kind: nostrx.KindLongForm, Tags: [][]string{{"title", "seed second hop read"}}, Content: "seed second hop read"},
		{ID: strings.Repeat("e", 64), PubKey: outsider, CreatedAt: now - 180, Kind: nostrx.KindLongForm, Tags: [][]string{{"title", "seed outsider read"}}, Content: "seed outsider read"},
		{ID: strings.Repeat("f", 64), PubKey: strings.Repeat("4", 64), CreatedAt: now - 170, Kind: nostrx.KindTextNote, Tags: [][]string{{"e", strings.Repeat("c", 64), "", "root"}, {"e", strings.Repeat("c", 64), "", "reply"}}, Content: "first hop reply"},
		{ID: strings.Repeat("6", 64), PubKey: strings.Repeat("5", 64), CreatedAt: now - 160, Kind: nostrx.KindTextNote, Tags: [][]string{{"e", strings.Repeat("d", 64), "", "root"}, {"e", strings.Repeat("d", 64), "", "reply"}}, Content: "second hop reply"},
		{ID: strings.Repeat("7", 64), PubKey: strings.Repeat("6", 64), CreatedAt: now - 150, Kind: nostrx.KindTextNote, Tags: [][]string{{"e", strings.Repeat("e", 64), "", "root"}, {"e", strings.Repeat("e", 64), "", "reply"}}, Content: "outsider reply"},
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/reads?wot=1&wot_depth=2&seed_pubkey="+seed, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "seed first hop read") || !strings.Contains(body, "seed second hop read") {
		t.Fatalf("expected reachable seeded reads in body: %s", body)
	}
	if strings.Contains(body, "seed outsider read") {
		t.Fatalf("unexpected outsider seeded read in body: %s", body)
	}

	trendReq := httptest.NewRequest(http.MethodGet, "/reads?sort=trend24h&wot=1&wot_depth=2&seed_pubkey="+seed, nil)
	trendRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(trendRec, trendReq)
	if trendRec.Code != http.StatusOK {
		t.Fatalf("trend status = %d, want 200", trendRec.Code)
	}
	trendBody := trendRec.Body.String()
	if !strings.Contains(trendBody, "seed first hop read") || !strings.Contains(trendBody, "seed second hop read") {
		t.Fatalf("expected reachable seeded trend reads in body: %s", trendBody)
	}
	if strings.Contains(trendBody, "seed outsider read") {
		t.Fatalf("unexpected outsider seeded trend read in body: %s", trendBody)
	}
}

func TestReadsDataSeededLoggedOutRefreshesScopedAuthors(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	srv.nostr = nostrx.NewClient(nil, time.Second)
	seed := strings.Repeat("f", 64)
	secret := fnostr.Generate()
	externalRead := fnostr.Event{
		CreatedAt: fnostr.Timestamp(time.Now().Unix() - 60),
		Kind:      fnostr.Kind(nostrx.KindLongForm),
		Tags:      fnostr.Tags{fnostr.Tag{"title", "relay scoped read"}},
		Content:   "relay scoped read",
	}
	if err := externalRead.Sign(secret); err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	firstHop := externalRead.PubKey.Hex()
	for _, ev := range []nostrx.Event{
		{ID: strings.Repeat("a", 64), PubKey: seed, CreatedAt: time.Now().Unix() - 120, Kind: nostrx.KindFollowList, Tags: [][]string{{"p", firstHop}}},
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "done") }()
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
		}
		if err := json.Unmarshal(envelope[2], &filter); err == nil && slices.Contains(filter.Authors, firstHop) {
			encoded, err := json.Marshal(externalRead)
			if err == nil {
				message := fmt.Sprintf(`["EVENT",%q,%s]`, subID, string(encoded))
				_ = conn.Write(ctx, websocket.MessageText, []byte(message))
			}
		}
		_ = conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`["EOSE",%q]`, subID)))
	}))
	defer relay.Close()
	relayURL := "ws" + strings.TrimPrefix(relay.URL, "http")

	data := srv.readsData(ctx, feedRequest{
		SeedPubkey: seed,
		Limit:      20,
		Relays:     []string{relayURL},
		SortMode:   "recent",
		WoT:        webOfTrustOptions{Enabled: true, Depth: 1},
	}, "24h")
	if len(data.Items) != 1 {
		t.Fatalf("expected one scoped relay read, got %d", len(data.Items))
	}
	if data.Items[0].Event.ID != externalRead.ID.Hex() {
		t.Fatalf("reads item id = %q, want %q", data.Items[0].Event.ID, externalRead.ID.Hex())
	}
}

func TestReadsDataSupportsTrendSortAndTrendingTimeframe(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	now := time.Now().Unix()
	readRecent := nostrx.Event{
		ID:        strings.Repeat("a", 64),
		PubKey:    strings.Repeat("1", 64),
		CreatedAt: now - 300,
		Kind:      nostrx.KindLongForm,
		Tags:      [][]string{{"title", "Recent"}},
		Content:   "recent read",
	}
	readWeek := nostrx.Event{
		ID:        strings.Repeat("b", 64),
		PubKey:    strings.Repeat("2", 64),
		CreatedAt: now - 400,
		Kind:      nostrx.KindLongForm,
		Tags:      [][]string{{"title", "Week"}},
		Content:   "week read",
	}
	recentReply := nostrx.Event{
		ID:        strings.Repeat("c", 64),
		PubKey:    strings.Repeat("3", 64),
		CreatedAt: now - 120,
		Kind:      nostrx.KindTextNote,
		Tags:      [][]string{{"e", readRecent.ID, "", "root"}, {"e", readRecent.ID, "", "reply"}},
		Content:   "reply",
	}
	weekReply := nostrx.Event{
		ID:        strings.Repeat("d", 64),
		PubKey:    strings.Repeat("4", 64),
		CreatedAt: now - int64((48*time.Hour)/time.Second),
		Kind:      nostrx.KindTextNote,
		Tags:      [][]string{{"e", readWeek.ID, "", "root"}, {"e", readWeek.ID, "", "reply"}},
		Content:   "reply",
	}
	for _, event := range []nostrx.Event{readRecent, readWeek, recentReply, weekReply} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	trend24h := srv.readsData(ctx, feedRequest{Limit: 20, SortMode: "trend24h"}, "24h")
	if trend24h.ReadsSort != "trend24h" {
		t.Fatalf("reads sort = %q, want trend24h", trend24h.ReadsSort)
	}
	if len(trend24h.Items) != 1 || trend24h.Items[0].Event.ID != readRecent.ID {
		t.Fatalf("unexpected trend24h reads: %#v", trend24h.Items)
	}
	if trend24h.Items[0].Title != "Recent" {
		t.Fatalf("trend24h list title = %q, want tag title Recent (summaries must hydrate)", trend24h.Items[0].Title)
	}
	if len(trend24h.Trending) != 1 || trend24h.Trending[0].Event.ID != readRecent.ID {
		t.Fatalf("unexpected 24h trending reads: %#v", trend24h.Trending)
	}

	trend7d := srv.readsData(ctx, feedRequest{Limit: 20, SortMode: "trend7d"}, "1w")
	if len(trend7d.Items) != 2 {
		t.Fatalf("expected 2 trend7d reads, got %d", len(trend7d.Items))
	}
	if trend7d.Items[0].Event.ID != readRecent.ID || trend7d.Items[1].Event.ID != readWeek.ID {
		t.Fatalf("unexpected trend7d order: %#v", trend7d.Items)
	}
	if trend7d.Items[0].Title != "Recent" || trend7d.Items[1].Title != "Week" {
		t.Fatalf("trend7d titles = %q / %q, want Recent / Week", trend7d.Items[0].Title, trend7d.Items[1].Title)
	}
	if len(trend7d.Trending) != 2 {
		t.Fatalf("expected 2 trending reads in 1w, got %d", len(trend7d.Trending))
	}
}

func TestReadsFragmentPaginatesAtTwenty(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	now := time.Now().Unix()
	pubkey := strings.Repeat("c", 64)
	for index := 0; index < 22; index++ {
		event := nostrx.Event{
			ID:        fmt.Sprintf("%064x", index+1),
			PubKey:    pubkey,
			CreatedAt: now - int64(index+1),
			Kind:      nostrx.KindLongForm,
			Tags:      [][]string{{"title", fmt.Sprintf("Read %d", index+1)}},
			Content:   "body",
		}
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	st.MarkRefreshed(ctx, "reads", feedRefreshKey(readsCacheKey, 0, ""))

	req := httptest.NewRequest(http.MethodGet, "/reads?fragment=1", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("X-Ptxt-Has-More"); got != "1" {
		t.Fatalf("X-Ptxt-Has-More = %q, want 1", got)
	}
	if rec.Header().Get("X-Ptxt-Cursor") == "" || rec.Header().Get("X-Ptxt-Cursor-Id") == "" {
		t.Fatalf("expected cursor headers, got cursor=%q cursor_id=%q", rec.Header().Get("X-Ptxt-Cursor"), rec.Header().Get("X-Ptxt-Cursor-Id"))
	}
	if count := strings.Count(rec.Body.String(), `class="read-article"`); count != 20 {
		t.Fatalf("rendered reads = %d, want 20", count)
	}
}

func TestReadsRightRailFragment(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	now := time.Now().Unix()
	event := nostrx.Event{
		ID:        fmt.Sprintf("%064x", 1),
		PubKey:    strings.Repeat("c", 64),
		CreatedAt: now,
		Kind:      nostrx.KindLongForm,
		Tags:      [][]string{{"title", "A read"}},
		Content:   "body",
	}
	if err := st.SaveEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	st.MarkRefreshed(ctx, "reads", feedRefreshKey(readsCacheKey, 0, ""))

	req := httptest.NewRequest(http.MethodGet, "/reads?fragment=right-rail", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "reads-right-rail") {
		t.Fatalf("expected reads right rail aside: %s", body)
	}
	if !strings.Contains(body, "Trending Reads") {
		t.Fatalf("expected trending header: %s", body)
	}
}

func TestEventRouteRedirectsToThread(t *testing.T) {
	srv, _ := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/e/"+strings.Repeat("a", 64), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/thread/"+strings.Repeat("a", 64) {
		t.Fatalf("location = %q, want /thread/{id}", loc)
	}
}

func TestReadDetailRouteRendersMoreReads(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	pubkey := strings.Repeat("a", 64)
	first := nostrx.Event{
		ID:        strings.Repeat("1", 64),
		PubKey:    pubkey,
		CreatedAt: time.Now().Unix() - 30,
		Kind:      nostrx.KindLongForm,
		Tags:      [][]string{{"title", "First read"}},
		Content:   "# first\n\nhello",
	}
	second := nostrx.Event{
		ID:        strings.Repeat("2", 64),
		PubKey:    pubkey,
		CreatedAt: time.Now().Unix() - 60,
		Kind:      nostrx.KindLongForm,
		Tags:      [][]string{{"title", "Second read"}},
		Content:   "# second\n\nworld",
	}
	for _, event := range []nostrx.Event{first, second} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/reads/"+first.ID, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "More reads from") {
		t.Fatalf("missing right-rail more reads content: %s", body)
	}
	if !strings.Contains(body, "/reads/"+second.ID) {
		t.Fatalf("missing additional read link in right rail: %s", body)
	}
}

func TestThreadSummaryShowsBackToReadLink(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	readID := strings.Repeat("a", 64)
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        readID,
		PubKey:    strings.Repeat("b", 64),
		CreatedAt: time.Now().Unix(),
		Kind:      nostrx.KindLongForm,
		Content:   "read",
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/thread/"+readID+"?fragment=summary&back_read="+readID, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "/reads/"+readID) {
		t.Fatalf("missing back-to-read link in thread summary: %s", rec.Body.String())
	}
}

func TestRefreshRepliesMarksEmptyFetchFresh(t *testing.T) {
	srv, _ := testServer(t)
	srv.nostr = nostrx.NewClient(nil, time.Second)
	ctx := context.Background()
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close(websocket.StatusNormalClosure, "done") }()
		_, msg, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var envelope []json.RawMessage
		if err := json.Unmarshal(msg, &envelope); err != nil || len(envelope) < 2 {
			return
		}
		var subID string
		if err := json.Unmarshal(envelope[1], &subID); err != nil {
			return
		}
		_ = conn.Write(ctx, websocket.MessageText, []byte(fmt.Sprintf(`["EOSE",%q]`, subID)))
	}))
	defer relay.Close()

	relayURL := "ws" + strings.TrimPrefix(relay.URL, "http")
	eventID := strings.Repeat("e", 64)
	srv.refreshReplies(ctx, eventID, []string{relayURL})
	if srv.store.ShouldRefresh(ctx, "thread", eventID, threadTTL) {
		t.Fatalf("empty successful refresh was not marked fresh")
	}
}

func TestFetchDefaultFeedPageReturnsCachedResultsWithoutWaitingForRefresh(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	now := time.Now().Unix()
	for index := 0; index < 3; index++ {
		if err := st.SaveEvent(ctx, nostrx.Event{
			ID:        fmt.Sprintf("%064x", index+1),
			PubKey:    strings.Repeat(fmt.Sprintf("%x", index+1), 64),
			CreatedAt: now - int64(index+1),
			Kind:      nostrx.KindTextNote,
			Content:   "cached",
		}); err != nil {
			t.Fatal(err)
		}
	}
	srv.nostr = nostrx.NewClient(nil, 2*time.Second)
	relay := newSlowEOSERelay(t, 1200*time.Millisecond)
	defer relay.Close()
	relayURL := "ws" + strings.TrimPrefix(relay.URL, "http")

	start := time.Now()
	events, _ := srv.fetchDefaultFeedPage(ctx, 0, "", 1, []string{relayURL})
	if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
		t.Fatalf("fetchDefaultFeedPage blocked for %s; expected cached fast return", elapsed)
	}
	if len(events) == 0 {
		t.Fatalf("expected cached events, got none")
	}
}

func TestFetchDefaultFeedPageEmptyCacheReturnsQuickly(t *testing.T) {
	srv, _ := testServer(t)
	srv.nostr = nostrx.NewClient(nil, 2*time.Second)
	relay := newSlowEOSERelay(t, 1200*time.Millisecond)
	defer relay.Close()
	relayURL := "ws" + strings.TrimPrefix(relay.URL, "http")

	start := time.Now()
	events, hasMore := srv.fetchDefaultFeedPage(context.Background(), 0, "", 1, []string{relayURL})
	if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
		t.Fatalf("empty cache path blocked for %s; expected async refresh", elapsed)
	}
	if len(events) != 0 || hasMore {
		t.Fatalf("expected empty immediate result, got len=%d hasMore=%v", len(events), hasMore)
	}
}

func TestFetchRankedFeedPageUsesTrendingCacheForLoggedOut(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	for index, id := range []string{"note-a", "note-b", "note-c"} {
		if err := st.SaveEvent(ctx, nostrx.Event{
			ID:        id,
			PubKey:    fmt.Sprintf("%064x", index+1),
			CreatedAt: 100 - int64(index),
			Kind:      nostrx.KindTextNote,
			Content:   id,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.WriteTrendingCache(ctx, trending1w, "", []store.TrendingItem{
		{NoteID: "note-c", ReplyCount: 9},
		{NoteID: "note-a", ReplyCount: 8},
		{NoteID: "note-b", ReplyCount: 7},
	}, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}

	page1, hasMore, after := srv.fetchRankedFeedPage(ctx, nil, trendingRankKey{}, 2, feedSortTrend7d, true, true)
	if len(page1) != 2 {
		t.Fatalf("len(page1) = %d, want 2", len(page1))
	}
	if page1[0].ID != "note-c" || page1[1].ID != "note-a" {
		t.Fatalf("unexpected cached order: %#v", page1)
	}
	if !hasMore {
		t.Fatalf("expected hasMore=true")
	}
	if after.id != "note-a" {
		t.Fatalf("after.id = %q, want note-a", after.id)
	}

	page2, hasMore2, _ := srv.fetchRankedFeedPage(ctx, nil, after, 5, feedSortTrend7d, true, true)
	if len(page2) != 1 || page2[0].ID != "note-b" {
		t.Fatalf("page2 = %#v, want [note-b]", page2)
	}
	if hasMore2 {
		t.Fatalf("expected hasMore=false on last page")
	}
	if page1[0].ID == page2[0].ID {
		t.Fatal("page2 overlapped page1")
	}
}

func TestRankedFeedKeysetCursorFromHeaders(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	now := time.Now().Unix()
	for index, spec := range []struct {
		id      string
		replies int
	}{
		{"note-top", 5},
		{"note-mid", 3},
		{"note-low", 1},
	} {
		if err := st.SaveEvent(ctx, nostrx.Event{
			ID:        spec.id,
			PubKey:    fmt.Sprintf("%064x", index+10),
			CreatedAt: now - int64(index),
			Kind:      nostrx.KindTextNote,
			Content:   spec.id,
		}); err != nil {
			t.Fatal(err)
		}
		for r := 0; r < spec.replies; r++ {
			reply := nostrx.Event{
				ID:        fmt.Sprintf("%s-r%d", spec.id, r),
				PubKey:    fmt.Sprintf("%064x", index+100+r),
				CreatedAt: now + int64(r),
				Kind:      nostrx.KindTextNote,
				Tags:      [][]string{{"e", spec.id, "", "root"}, {"e", spec.id, "", "reply"}},
				Content:   "reply",
			}
			if err := st.SaveEvent(ctx, reply); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := st.WriteTrendingCache(ctx, trending24h, "", []store.TrendingItem{
		{NoteID: "note-top", ReplyCount: 5},
		{NoteID: "note-mid", ReplyCount: 3},
		{NoteID: "note-low", ReplyCount: 1},
	}, now); err != nil {
		t.Fatal(err)
	}

	data := srv.feedData(ctx, feedRequest{Limit: 1, SortMode: feedSortTrend24h})
	if len(data.Feed) != 1 || data.Feed[0].ID != "note-top" {
		t.Fatalf("first page = %#v, want note-top", data.Feed)
	}
	if !data.HasMore || data.CursorID != "note-top" {
		t.Fatalf("cursor = (%d, %q), hasMore=%v", data.Cursor, data.CursorID, data.HasMore)
	}

	page2 := srv.feedData(ctx, feedRequest{Limit: 10, SortMode: feedSortTrend24h, Cursor: data.Cursor, CursorID: data.CursorID})
	ids := make([]string, 0, len(page2.Feed))
	for _, ev := range page2.Feed {
		ids = append(ids, ev.ID)
	}
	if ids[0] == "note-top" {
		t.Fatalf("page2 duplicated anchor: %#v", ids)
	}
	want := []string{"note-mid", "note-low"}
	if len(ids) != len(want) {
		t.Fatalf("page2 ids = %#v, want %#v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("page2 ids = %#v, want %#v", ids, want)
		}
	}
}

func TestRankedFeedMutePaginationAdvancesPastSkippedRankedNotes(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	now := time.Now().Unix()
	viewer := strings.Repeat("a", 64)
	muted := strings.Repeat("b", 64)
	good := strings.Repeat("c", 64)

	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        strings.Repeat("d", 64),
		PubKey:    viewer,
		CreatedAt: now + 1,
		Kind:      nostrx.KindMuteList,
		Tags:      [][]string{{"p", muted}},
	}); err != nil {
		t.Fatal(err)
	}
	specs := []struct {
		id      string
		pub     string
		replies int
	}{
		{"note-muted-top", muted, 100},
		{"note-good-1", good, 90},
		{"note-good-2", good, 80},
		{"note-muted-mid", muted, 70},
		{"note-good-3", good, 60},
		{"note-good-4", good, 50},
	}
	for _, spec := range specs {
		if err := st.SaveEvent(ctx, nostrx.Event{
			ID:        spec.id,
			PubKey:    spec.pub,
			CreatedAt: now - int64(spec.replies),
			Kind:      nostrx.KindTextNote,
			Content:   spec.id,
		}); err != nil {
			t.Fatal(err)
		}
	}
	items := make([]store.TrendingItem, 0, len(specs))
	for _, spec := range specs {
		items = append(items, store.TrendingItem{NoteID: spec.id, ReplyCount: spec.replies})
	}
	if err := st.WriteTrendingCache(ctx, trending24h, "", items, now); err != nil {
		t.Fatal(err)
	}
	page1 := srv.feedPageDataEx(ctx, feedRequest{Pubkey: viewer, Limit: 2, SortMode: feedSortTrend24h}, true, feedPageDataOptions{})
	if len(page1.Feed) != 2 {
		t.Fatalf("page1 len = %d, want 2: %#v", len(page1.Feed), page1.Feed)
	}
	if page1.Feed[0].ID != "note-good-1" || page1.Feed[1].ID != "note-good-2" {
		t.Fatalf("page1 ids = [%s, %s], want [note-good-1, note-good-2]", page1.Feed[0].ID, page1.Feed[1].ID)
	}
	if !page1.HasMore {
		t.Fatal("expected page1 hasMore")
	}

	page2 := srv.feedPageDataEx(ctx, feedRequest{
		Pubkey:   viewer,
		Limit:    2,
		SortMode: feedSortTrend24h,
		Cursor:   page1.Cursor,
		CursorID: page1.CursorID,
	}, true, feedPageDataOptions{})
	if len(page2.Feed) != 2 {
		t.Fatalf("page2 len = %d, want 2: %#v", len(page2.Feed), page2.Feed)
	}
	if page2.Feed[0].ID != "note-good-3" || page2.Feed[1].ID != "note-good-4" {
		t.Fatalf("page2 ids = [%s, %s], want [note-good-3, note-good-4]", page2.Feed[0].ID, page2.Feed[1].ID)
	}
	for _, ev := range page2.Feed {
		if ev.PubKey == muted {
			t.Fatalf("page2 included muted author note %q", ev.ID)
		}
	}
}

func TestTrendingDataMissReturnsFastEmptyAndWarmsAsync(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	now := time.Now().Unix()
	parent := nostrx.Event{
		ID:        "note-parent",
		PubKey:    strings.Repeat("a", 64),
		CreatedAt: now - 3,
		Kind:      nostrx.KindTextNote,
		Content:   "parent",
	}
	replies := []nostrx.Event{
		{
			ID:        "reply-1",
			PubKey:    strings.Repeat("b", 64),
			CreatedAt: now - 2,
			Kind:      nostrx.KindTextNote,
			Content:   "reply one",
			Tags:      [][]string{{"e", "note-parent", "", "root"}, {"e", "note-parent", "", "reply"}},
		},
		{
			ID:        "reply-2",
			PubKey:    strings.Repeat("c", 64),
			CreatedAt: now - 1,
			Kind:      nostrx.KindTextNote,
			Content:   "reply two",
			Tags:      [][]string{{"e", "note-parent", "", "root"}, {"e", "note-parent", "", "reply"}},
		},
	}
	if err := st.SaveEvent(ctx, parent); err != nil {
		t.Fatal(err)
	}
	for _, reply := range replies {
		if err := st.SaveEvent(ctx, reply); err != nil {
			t.Fatal(err)
		}
	}

	trending := srv.trendingData(ctx, trending24h, "", nil, nil, true)
	if len(trending) != 0 {
		t.Fatalf("expected fast-empty on cache miss, got %#v", trending)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		items, computedAt, err := st.ReadTrendingCache(ctx, trending24h, "")
		if err != nil {
			t.Fatal(err)
		}
		if computedAt > 0 && len(items) >= 1 && items[0].NoteID == "note-parent" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for async trending warm, got items=%#v computedAt=%d", items, computedAt)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestFetchAuthorsPageFeedFullCacheSkipsRefresh(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	fresh := signedMutationEvent(t, nostrx.KindTextNote, "fresh relay note", nil)
	pubkey := fresh.PubKey
	now := fresh.CreatedAt
	for index := 0; index < 31; index++ {
		if err := st.SaveEvent(ctx, nostrx.Event{
			ID:        fmt.Sprintf("%064x", 9000+index),
			PubKey:    pubkey,
			CreatedAt: now - 1000 - int64(index),
			Kind:      nostrx.KindTextNote,
			Content:   "cached",
		}); err != nil {
			t.Fatal(err)
		}
	}
	relay := newRelayWithEvents(t, []nostrx.Event{fresh})
	defer relay.Close()

	events, _ := srv.fetchAuthorsPage(ctx, "", []string{pubkey}, 0, "", 30, []string{wsURL(relay.URL)}, "feed", authorsCacheKey([]string{pubkey}), nil)
	if len(events) == 0 {
		t.Fatalf("expected cached page")
	}
	for _, event := range events {
		if event.ID == fresh.ID {
			t.Fatalf("unexpected relay-refreshed event in feed full-cache path")
		}
	}
}

func TestFetchAuthorsPageProfileFullCacheRefreshesFirstPage(t *testing.T) {
	srv, st := newTestServer(t, testServerOptions{relayTimeout: 50 * time.Millisecond})
	ctx := context.Background()
	fresh := signedMutationEvent(t, nostrx.KindTextNote, "fresh relay note", nil)
	pubkey := fresh.PubKey
	now := fresh.CreatedAt
	for index := 0; index < 31; index++ {
		if err := st.SaveEvent(ctx, nostrx.Event{
			ID:        fmt.Sprintf("%064x", 12000+index),
			PubKey:    pubkey,
			CreatedAt: now - 1000 - int64(index),
			Kind:      nostrx.KindTextNote,
			Content:   "cached",
		}); err != nil {
			t.Fatal(err)
		}
	}
	relay := newRelayWithEvents(t, []nostrx.Event{fresh})
	defer relay.Close()

	events, _ := srv.fetchAuthorsPage(ctx, strings.Repeat("f", 64), []string{pubkey}, 0, "", 30, []string{wsURL(relay.URL)}, "profile", pubkey, nil)
	if len(events) == 0 {
		t.Fatalf("expected profile page results")
	}
	if events[0].ID != fresh.ID {
		t.Fatalf("expected refreshed latest profile note %s at top, got %s", fresh.ID, events[0].ID)
	}
}

func TestDefaultFeedRefreshGuardDeduplicatesInFlight(t *testing.T) {
	srv, _ := testServer(t)
	if !srv.beginRefresh("feed:key") {
		t.Fatalf("first beginRefresh should acquire lock")
	}
	if srv.beginRefresh("feed:key") {
		t.Fatalf("second beginRefresh should be deduplicated")
	}
	srv.endRefresh("feed:key")
	if !srv.beginRefresh("feed:key") {
		t.Fatalf("beginRefresh should acquire after endRefresh")
	}
}

func newRelayWithEvents(t *testing.T, events []nostrx.Event) *httptest.Server {
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
		if err := json.Unmarshal(msg, &envelope); err != nil || len(envelope) < 2 {
			return
		}
		var subID string
		if err := json.Unmarshal(envelope[1], &subID); err != nil {
			return
		}
		for _, event := range events {
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
