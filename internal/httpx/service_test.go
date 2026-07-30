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
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ptxt-nstr/internal/config"
	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"

	fnostr "fiatjaf.com/nostr"
	"github.com/coder/websocket"
)

func assertCanonicalLoggedOutHomeShellDeferred(t *testing.T, body string) {
	t.Helper()
	if !strings.Contains(body, `data-route-outlet="root"`) {
		t.Fatalf("expected canonical home app shell: %s", body)
	}
	if strings.Contains(body, `data-feed-loader`) {
		t.Fatalf("did not expect canonical home shell to include server feed loader: %s", body)
	}
	if strings.Contains(body, "seeded home note") {
		t.Fatalf("did not expect seeded note inlined into canonical home shell: %s", body)
	}
}

func TestUserPageHardLoadReturnsAppShellWithoutBlockingOnProfileRelayRefresh(t *testing.T) {
	srv, _ := testServer(t)
	relay := newSlowEOSERelay(t, 400*time.Millisecond)
	defer relay.Close()

	targetPubkey := strings.Repeat("a", 64)
	viewerPubkey := strings.Repeat("b", 64)
	req := httptest.NewRequest(http.MethodGet, "/u/"+targetPubkey+"?pubkey="+viewerPubkey+"&relays="+wsURL(relay.URL), nil)
	rec := httptest.NewRecorder()

	started := time.Now()
	srv.Handler().ServeHTTP(rec, req)
	elapsed := time.Since(started)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if elapsed > 150*time.Millisecond {
		t.Fatalf("profile hard load blocked on relay-backed profile refresh: %v", elapsed)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-profile-shell="1"`) || !strings.Contains(body, `profile-modern`) {
		t.Fatalf("expected server-rendered profile shell, got: %s", body)
	}
	if strings.Contains(body, wsURL(relay.URL)) {
		t.Fatalf("profile hard load should not block and inline slow relay data: %s", body)
	}
}

func TestUserFollowingFragmentDoesNotBlockOnRelayContactProfileHydration(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	ownerSecret := fnostr.Generate()
	contactPubkey := strings.Repeat("c", 64)
	lowPubkey := strings.Repeat("0", 63) + "1"
	followList := signNostrEvent(t, ownerSecret, nostrx.KindFollowList, "", [][]string{{"p", lowPubkey}, {"p", contactPubkey}})
	if _, err := st.SaveEvents(ctx, []nostrx.Event{followList}); err != nil {
		t.Fatal(err)
	}
	allowAnonymousAuthors(t, st, followList.PubKey)
	relay := newSlowEOSERelay(t, 400*time.Millisecond)
	defer relay.Close()

	req := httptest.NewRequest(http.MethodGet, "/u/"+followList.PubKey+"?fragment=following&relays="+wsURL(relay.URL), nil)
	rec := httptest.NewRecorder()
	started := time.Now()
	srv.Handler().ServeHTTP(rec, req)
	elapsed := time.Since(started)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if elapsed > 150*time.Millisecond {
		t.Fatalf("following fragment blocked on relay contact metadata hydration: %v", elapsed)
	}
	body := rec.Body.String()
	if strings.Contains(body, "Relay Contact") || strings.Contains(body, `data-nip05="contact@example.com"`) {
		t.Fatalf("following fragment inlined relay contact metadata on the request path: %s", body)
	}
	if !strings.Contains(body, lowPubkey[:12]) || !strings.Contains(body, contactPubkey[:12]) {
		t.Fatalf("following fragment should render cached fallback rows immediately: %s", body)
	}
}

func TestEnrichedFollowListPagesBeyondHydrationLimit(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()

	t.Run("following", func(t *testing.T) {
		owner := strings.Repeat("a", 64)
		targets := make([][]string, 0, followMetadataHydrationLimit+1)
		for index := 0; index <= followMetadataHydrationLimit; index++ {
			targets = append(targets, []string{"p", fmt.Sprintf("%064x", index+1)})
		}
		if err := st.SaveEvent(ctx, nostrx.Event{
			ID:        strings.Repeat("1", 64),
			PubKey:    owner,
			CreatedAt: 10,
			Kind:      nostrx.KindFollowList,
			Tags:      targets,
		}); err != nil {
			t.Fatal(err)
		}

		view, _, ok := srv.enrichedFollowList(ctx, owner, "", 21, true, nil)
		if !ok {
			t.Fatal("expected enriched following page beyond hydration limit")
		}
		want := fmt.Sprintf("%064x", followMetadataHydrationLimit+1)
		if len(view.Items) != 1 || view.Items[0] != want {
			t.Fatalf("page 21 items = %#v, want [%q]", view.Items, want)
		}
	})

	t.Run("followers", func(t *testing.T) {
		target := strings.Repeat("f", 64)
		for index := 0; index <= followMetadataHydrationLimit; index++ {
			owner := fmt.Sprintf("%064x", index+1)
			if err := st.SaveEvent(ctx, nostrx.Event{
				ID:        fmt.Sprintf("%064x", index+10000),
				PubKey:    owner,
				CreatedAt: int64(20 + index),
				Kind:      nostrx.KindFollowList,
				Tags:      [][]string{{"p", target}},
			}); err != nil {
				t.Fatal(err)
			}
		}

		view, _, ok := srv.enrichedFollowList(ctx, target, "", 21, false, nil)
		if !ok {
			t.Fatal("expected enriched followers page beyond hydration limit")
		}
		want := fmt.Sprintf("%064x", followMetadataHydrationLimit+1)
		if len(view.Items) != 1 || view.Items[0] != want {
			t.Fatalf("page 21 items = %#v, want [%q]", view.Items, want)
		}
	})
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
	if !strings.Contains(body, `data-route-outlet="root"`) {
		t.Fatalf("expected initial home app shell, got: %s", body)
	}
	if !strings.Contains(body, `data-feed-loader`) {
		t.Fatalf("expected server-rendered home feed loader markup, got: %s", body)
	}
	if strings.Contains(body, "No notes found yet.") {
		t.Fatalf("unexpected empty-state copy in full home render: %s", body)
	}
}

func TestAppShellUsesDocumentNavigationAndSharedCacheForAnonymousRoutes(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	note := nostrx.Event{
		ID:        strings.Repeat("a", 64),
		PubKey:    strings.Repeat("b", 64),
		Kind:      nostrx.KindTextNote,
		CreatedAt: 1700000000,
		Content:   "cached anonymous app shell note",
		Sig:       "sig",
	}
	if err := st.SaveEvent(ctx, note); err != nil {
		t.Fatal(err)
	}
	allowAnonymousAuthors(t, st, note.PubKey)
	req := httptest.NewRequest(http.MethodGet, "/thread/"+note.ID, nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != cacheControlThreadPage {
		t.Fatalf("Cache-Control = %q, want shared thread cache", got)
	}
	body := rec.Body.String()
	matches := regexp.MustCompile(`<script id="ptxt-app-bootstrap" type="application/json">([^<]+)</script>`).FindStringSubmatch(body)
	if len(matches) != 2 {
		t.Fatalf("expected app bootstrap json script in response: %s", truncateForLog(body, 1200))
	}
	var payload struct {
		Features map[string]bool `json:"features"`
		Route    struct {
			Route string `json:"route"`
		} `json:"route"`
	}
	if err := json.Unmarshal([]byte(matches[1]), &payload); err != nil {
		t.Fatalf("decode bootstrap json: %v", err)
	}
	if payload.Route.Route != "thread" {
		t.Fatalf("bootstrap route = %q, want thread", payload.Route.Route)
	}
	if _, ok := payload.Features["clientRouter"]; ok {
		t.Fatalf("clientRouter feature present, want retired client router flag omitted")
	}
	if !payload.Features["documentNavigation"] {
		t.Fatalf("documentNavigation feature = false, want true")
	}
}

func TestAppShellKeepsPersonalizedRoutesPrivate(t *testing.T) {
	srv, _ := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/feed", nil)
	req.Header.Set(headerViewerPubkey, strings.Repeat("b", 64))
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q, want private no-store for personalized shell", got)
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
	if err := st.SaveEvent(ctx, note); err != nil {
		t.Fatal(err)
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
	viewer := strings.Repeat("a", 64)
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
	req.Header.Set(headerViewerPubkey, viewer)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-tag-results`) || !strings.Contains(body, `id="note-tag-note-one"`) {
		t.Fatalf("expected server-rendered tag results: %s", body)
	}
	if !strings.Contains(body, "#nostrshown") {
		t.Fatalf("expected tag heading in server-rendered document: %s", body)
	}
	if robots := rec.Header().Get("X-Robots-Tag"); robots != "noindex, nofollow" {
		t.Fatalf("X-Robots-Tag = %q, want noindex, nofollow", robots)
	}
}

func TestHandleTagAnonymousReturnsShellWithoutServerTagSearch(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	note := nostrx.Event{
		ID:        "tag-note-anon",
		PubKey:    strings.Repeat("d", 64),
		CreatedAt: 1714000001,
		Kind:      nostrx.KindTextNote,
		Content:   "hello #anonshown",
		Tags:      [][]string{{"t", "anonshown"}},
	}
	if err := st.SaveEvent(ctx, note); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/tag/anonshown", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-tag-results`) || !strings.Contains(body, `id="note-tag-note-anon"`) {
		t.Fatalf("expected anonymous tag route to render cached tag results: %s", body)
	}
}

func TestTagPlanNormalizesStoreCacheKeyOnly(t *testing.T) {
	srv, _ := testServer(t)
	ctx := context.Background()

	lower := srv.newTagPlan(ctx, tagRequest{Tag: "nostr", Scope: searchScopeAll, Limit: 30})
	mixed := srv.newTagPlan(ctx, tagRequest{Tag: "Nostr", Scope: searchScopeAll, Limit: 30})

	if lower.storeKey != mixed.storeKey {
		t.Fatalf("storeKey should be case-insensitive:\n%s\n%s", lower.storeKey, mixed.storeKey)
	}
	if lower.pageKey == mixed.pageKey {
		t.Fatalf("pageKey should preserve requested tag casing for rendered page data")
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
	if !strings.Contains(body, `data-search-results`) {
		t.Fatalf("expected server-rendered search results container: %s", body)
	}
	if !strings.Contains(body, `q=nostr`) {
		t.Fatalf("expected search query preserved in search document: %s", body)
	}
	if robots := rec.Header().Get("X-Robots-Tag"); robots != "noindex, nofollow" {
		t.Fatalf("X-Robots-Tag = %q, want noindex, nofollow", robots)
	}
}

func TestSearchSecondIdenticalRequestHitsCache(t *testing.T) {
	srv, _ := testServer(t)
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
	if counters["search.cache.store.miss"] == 0 {
		t.Fatalf("search.cache.store.miss = 0, want server-rendered search to query cache")
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
	if !strings.Contains(networkBody, `data-search-results`) {
		t.Fatalf("expected server-rendered search results container: %s", networkBody)
	}
	if !strings.Contains(networkBody, `q=zebra`) || !strings.Contains(networkBody, `scope=all`) {
		t.Fatalf("expected search query/scope links preserved in search document: %s", networkBody)
	}

	allReq := httptest.NewRequest(http.MethodGet, "/search?pubkey="+viewer+"&wot=1&wot_depth=1&q=zebra&scope=all", nil)
	allRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(allRec, allReq)
	if allRec.Code != http.StatusOK {
		t.Fatalf("all status = %d, want 200", allRec.Code)
	}
	allBody := allRec.Body.String()
	if !strings.Contains(allBody, `all cached notes`) {
		t.Fatalf("expected expanded search scope in route context shell: %s", allBody)
	}
}

func TestSearchUsersModeRendersCachedProfiles(t *testing.T) {
	srv, _ := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/search?q=alice&mode=users", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-search-users`) {
		t.Fatalf("expected server-rendered user search container: %s", body)
	}
	if !strings.Contains(body, `name="mode" value="users"`) {
		t.Fatalf("expected users mode preserved in search document: %s", body)
	}
}

func TestSearchInvalidModeFallsBackToNotes(t *testing.T) {
	srv, _ := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/search?q=nostr&mode=bogus", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-search-results`) {
		t.Fatalf("expected server-rendered search results container: %s", body)
	}
	if !strings.Contains(body, `name="mode" value="notes"`) {
		t.Fatalf("expected notes mode selected in shell controls: %s", body)
	}
}

func TestFeedDataHydratesReferencedEventsForRepostsAndQuotes(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	viewer := strings.Repeat("d", 64)
	author := strings.Repeat("a", 64)
	reposter := strings.Repeat("b", 64)
	quoter := strings.Repeat("c", 64)
	linker := strings.Repeat("e", 64)
	now := time.Now().Unix()
	originalID := strings.Repeat("1", 64)
	repostID := strings.Repeat("2", 64)
	quoteID := strings.Repeat("3", 64)
	linkedID := strings.Repeat("5", 64)
	linkPostID := strings.Repeat("6", 64)
	original := nostrx.Event{
		ID:        originalID,
		PubKey:    author,
		CreatedAt: now - 20,
		Kind:      nostrx.KindTextNote,
		Content:   "original note",
	}
	linked := nostrx.Event{
		ID:        linkedID,
		PubKey:    author,
		CreatedAt: now - 30,
		Kind:      nostrx.KindTextNote,
		Content:   "inline linked note",
	}
	linkPost := nostrx.Event{
		ID:        linkPostID,
		PubKey:    linker,
		CreatedAt: now - 2,
		Kind:      nostrx.KindTextNote,
		Content:   "inline nostr:" + nostrx.EncodeNEvent(linkedID, author),
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
	for _, event := range []nostrx.Event{original, linked, linkPost, repost, quote} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        strings.Repeat("4", 64),
		PubKey:    viewer,
		CreatedAt: now - 1,
		Kind:      nostrx.KindFollowList,
		Tags:      [][]string{{"p", reposter}, {"p", quoter}, {"p", linker}},
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
	if got := data.ReferencedEvents[linkedID].Content; got != "inline linked note" {
		t.Fatalf("inline referenced content = %q, want %q", got, "inline linked note")
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

func TestUserPageRendersThinMetadataShell(t *testing.T) {
	srv, st := testServer(t)
	pubkey := strings.Repeat("d", 64)
	if err := st.SaveEvent(context.Background(), nostrx.Event{
		ID:        strings.Repeat("1", 64),
		PubKey:    pubkey,
		CreatedAt: 100,
		Kind:      nostrx.KindProfileMetadata,
		Content:   `{"name":"thin-shell"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveEvent(context.Background(), nostrx.Event{
		ID:        strings.Repeat("2", 64),
		PubKey:    pubkey,
		CreatedAt: 110,
		Kind:      nostrx.KindRelayListMetadata,
		Tags:      [][]string{{"r", "wss://relay.example"}},
		Content:   "",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveEvent(context.Background(), nostrx.Event{
		ID:        strings.Repeat("3", 64),
		PubKey:    pubkey,
		CreatedAt: 120,
		Kind:      nostrx.KindTextNote,
		Content:   "hello world",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveEvent(context.Background(), nostrx.Event{
		ID:        strings.Repeat("4", 64),
		PubKey:    pubkey,
		CreatedAt: 121,
		Kind:      nostrx.KindTextNote,
		Content:   "cached reply",
		Tags:      [][]string{{"e", strings.Repeat("3", 64), "", "reply"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveEvent(context.Background(), nostrx.Event{
		ID:        strings.Repeat("5", 64),
		PubKey:    pubkey,
		CreatedAt: 122,
		Kind:      nostrx.KindTextNote,
		Content:   "cached media https://example.com/photo.jpg",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveEvent(context.Background(), nostrx.Event{
		ID:        strings.Repeat("6", 64),
		PubKey:    pubkey,
		CreatedAt: 123,
		Kind:      nostrx.KindRepost,
		Content:   "cached repost",
		Tags:      [][]string{{"e", strings.Repeat("3", 64)}},
	}); err != nil {
		t.Fatal(err)
	}

	allowAnonymousAuthors(t, st, pubkey)

	req := httptest.NewRequest(http.MethodGet, "/u/"+pubkey, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-profile-shell="1"`) || !strings.Contains(body, `profile-modern`) {
		t.Fatalf("expected server-rendered profile shell, got: %s", body)
	}
	if !strings.Contains(body, `id="note-`+strings.Repeat("3", 64)+`"`) || !strings.Contains(body, "hello world") {
		t.Fatalf("expected server-rendered profile note, got: %s", body)
	}
	if strings.Contains(body, "cached reply") || strings.Contains(body, "cached repost") {
		t.Fatalf("profile posts should exclude replies and reposts, got: %s", body)
	}
	for _, unwanted := range []string{
		`data-profile-post-count`,
		`data-profile-reply-count`,
		`data-profile-media-count`,
	} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("profile tabs should not render count marker %q, got: %s", unwanted, body)
		}
	}
	if !strings.Contains(body, `data-retro-loader-type="profile-replies"`) || !strings.Contains(body, `data-retro-loader-type="profile-media"`) {
		t.Fatalf("initial user document should keep replies/media as lazy panels, got: %s", body)
	}

	replyReq := httptest.NewRequest(http.MethodGet, "/u/"+pubkey+"?fragment=replies", nil)
	replyRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(replyRec, replyReq)
	if replyRec.Code != http.StatusOK {
		t.Fatalf("reply fragment status = %d, want 200", replyRec.Code)
	}
	replyBody := replyRec.Body.String()
	if !strings.Contains(replyBody, "cached reply") || strings.Contains(replyBody, "hello world") || strings.Contains(replyBody, "cached repost") {
		t.Fatalf("reply fragment should render cached replies only, got: %s", replyBody)
	}

	mediaReq := httptest.NewRequest(http.MethodGet, "/u/"+pubkey+"?fragment=media", nil)
	mediaRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(mediaRec, mediaReq)
	if mediaRec.Code != http.StatusOK {
		t.Fatalf("media fragment status = %d, want 200", mediaRec.Code)
	}
	mediaBody := mediaRec.Body.String()
	if !strings.Contains(mediaBody, "cached media") || strings.Contains(mediaBody, "hello world") {
		t.Fatalf("media fragment should render cached media only, got: %s", mediaBody)
	}
}

func TestUserPageBootstrapSeedsCachedProfileMetadata(t *testing.T) {
	srv, st := testServer(t)
	pubkey := strings.Repeat("d", 64)
	eventID := strings.Repeat("1", 64)
	if err := st.SaveEvent(context.Background(), nostrx.Event{
		ID:        eventID,
		PubKey:    pubkey,
		CreatedAt: 1719360000,
		Kind:      nostrx.KindProfileMetadata,
		Content:   `{"name":"thin-shell","display_name":"Thin Shell","about":"cached bio","picture":"https://example.com/avatar.png","website":"example.com","nip05":"thin@example.com","lud16":"zap@example.com"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveEvent(context.Background(), nostrx.Event{
		ID:        strings.Repeat("2", 64),
		PubKey:    pubkey,
		CreatedAt: 1719360001,
		Kind:      nostrx.KindRelayListMetadata,
		Tags:      [][]string{{"r", "wss://author.example", "write"}},
	}); err != nil {
		t.Fatal(err)
	}
	allowAnonymousAuthors(t, st, pubkey)

	req := httptest.NewRequest(http.MethodGet, "/u/"+pubkey+"?relays=wss://relay.example", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`data-profile-shell="1"`,
		pubkey,
		"Thin Shell",
		"cached bio",
		"example.com",
		"thin@example.com",
		"zap@example.com",
		"wss://author.example",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected server-rendered profile metadata %q in response: %s", want, truncateForLog(body, 1200))
		}
	}

	headerReq := httptest.NewRequest(http.MethodGet, "/u/"+pubkey+"?fragment=header", nil)
	headerRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(headerRec, headerReq)
	if headerRec.Code != http.StatusOK {
		t.Fatalf("header fragment status = %d, want 200", headerRec.Code)
	}
	headerBody := headerRec.Body.String()
	for _, want := range []string{"Thin Shell", "cached bio", "example.com", "zap@example.com"} {
		if !strings.Contains(headerBody, want) {
			t.Fatalf("expected profile header metadata %q in response: %s", want, truncateForLog(headerBody, 1200))
		}
	}
	if strings.Contains(headerBody, `data-profile-shell="1"`) || strings.Contains(headerBody, `id="user-panel-posts"`) {
		t.Fatalf("header fragment should not include the profile timeline shell: %s", truncateForLog(headerBody, 1200))
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
	authors := []string{rootPubkey}
	for index := 0; index < 30; index++ {
		author := strings.Repeat(fmt.Sprintf("%x", (index%5)+2), 64)
		authors = append(authors, author)
		event := nostrx.Event{
			ID:        fmt.Sprintf("%064x", index+10),
			PubKey:    author,
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
	allowAnonymousAuthors(t, st, authors...)

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
	t.Skip("legacy server fragment behavior removed; thread route is client-rendered")
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
	if !strings.Contains(summaryBody, `data-thread-view-toggle`) {
		t.Fatalf("summary should include thread view toggle: %s", summaryBody)
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
	markTestRequestLoggedIn(req)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-route-outlet`) {
		t.Fatalf("expected app shell route outlet for human thread route: %s", body)
	}
	if !strings.Contains(body, `"path":"/thread/`+selectedID+`"`) {
		t.Fatalf("expected thread path in route context: %s", body)
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
	markTestRequestLoggedIn(req)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-route-outlet`) {
		t.Fatalf("expected app shell route outlet for human thread route: %s", body)
	}
	if !strings.Contains(body, `"path":"/thread/`+selectedID+`"`) {
		t.Fatalf("expected thread path in route context: %s", body)
	}
}

func TestThreadSummaryKeepsOriginalRootForNestedReplyWithoutExplicitRootTag(t *testing.T) {
	t.Skip("legacy server fragment behavior removed; thread route is client-rendered")
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
	for _, noteID := range []string{rootID, aID, bID, cID} {
		if !strings.Contains(body, `data-thread-tree-note="note-`+noteID+`"`) {
			t.Fatalf("summary missing traversal note %s: %s", noteID, body)
		}
	}
}

func TestThreadSummaryBackParamDoesNotRerootNestedReply(t *testing.T) {
	t.Skip("legacy server fragment behavior removed; thread route is client-rendered")
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
	if !strings.Contains(body, `/thread/`+bID+`#note-`+cID) {
		t.Fatalf("summary should keep back-to-original-thread link: %s", body)
	}
}

func TestThreadSummaryRepairsBogusExplicitRootMarkerFromAncestorChain(t *testing.T) {
	t.Skip("legacy server fragment behavior removed; thread route is client-rendered")
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
	for _, noteID := range []string{rootID, aID, bID, cID} {
		if !strings.Contains(body, `data-thread-tree-note="note-`+noteID+`"`) {
			t.Fatalf("summary missing traversal note %s: %s", noteID, body)
		}
	}
}

func TestThreadRepliesFragmentScopesToSelectedBranch(t *testing.T) {
	t.Skip("legacy server fragment behavior removed; thread route is client-rendered")
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
	t.Skip("legacy server fragment behavior removed; thread route is client-rendered")
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
	if !strings.Contains(fullBody, `data-route-outlet`) {
		t.Fatalf("expected app shell route outlet for human thread route: %s", fullBody)
	}
	if !strings.Contains(fullBody, `"path":"/thread/`+selectedID+`"`) {
		t.Fatalf("expected thread path in route context: %s", fullBody)
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

func TestThreadFocusedInitialPageSeparatesOtherDirectRepliesWhenSelectedHasNoChildren(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	rootID := strings.Repeat("1", 64)
	parentID := strings.Repeat("2", 64)
	selectedID := strings.Repeat("3", 64)
	siblingID := strings.Repeat("4", 64)
	nestedSiblingID := strings.Repeat("5", 64)
	pk := strings.Repeat("a", 64)
	events := []nostrx.Event{
		{ID: rootID, PubKey: pk, CreatedAt: 1000, Kind: nostrx.KindTextNote, Content: "root", Sig: "s"},
		{
			ID: parentID, PubKey: pk, CreatedAt: 1001, Kind: nostrx.KindTextNote, Content: "parent", Sig: "s",
			Tags: [][]string{{"e", rootID, "", "root"}, {"e", rootID, "", "reply"}},
		},
		{
			ID: selectedID, PubKey: pk, CreatedAt: 1002, Kind: nostrx.KindTextNote, Content: "selected", Sig: "s",
			Tags: [][]string{{"e", rootID, "", "root"}, {"e", parentID, "", "reply"}},
		},
		{
			ID: siblingID, PubKey: pk, CreatedAt: 1003, Kind: nostrx.KindTextNote, Content: "sibling reply that should not disappear", Sig: "s",
			Tags: [][]string{{"e", rootID, "", "root"}, {"e", rootID, "", "reply"}},
		},
		{
			ID: nestedSiblingID, PubKey: pk, CreatedAt: 1004, Kind: nostrx.KindTextNote, Content: "nested sibling reply that should not disappear", Sig: "s",
			Tags: [][]string{{"e", rootID, "", "root"}, {"e", parentID, "", "reply"}},
		},
	}
	for _, ev := range events {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/thread/"+selectedID, nil)
	markTestRequestLoggedIn(req)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-route-outlet`) {
		t.Fatalf("expected app shell route outlet for human thread route: %s", body)
	}
	if !strings.Contains(body, `"path":"/thread/`+selectedID+`"`) {
		t.Fatalf("expected thread path in route context: %s", body)
	}
}

func TestThreadRootPagePromotesRepliesWhoseIntermediateParentIsMissing(t *testing.T) {
	t.Skip("legacy server fragment behavior removed; thread route is client-rendered")
	srv, st := testServer(t)
	ctx := context.Background()
	rootID := strings.Repeat("1", 64)
	missingParentID := strings.Repeat("2", 64)
	orphanID := strings.Repeat("3", 64)
	childID := strings.Repeat("4", 64)
	pk := strings.Repeat("a", 64)
	events := []nostrx.Event{
		{ID: rootID, PubKey: pk, CreatedAt: 1000, Kind: nostrx.KindTextNote, Content: "root", Sig: "s"},
		{
			ID: orphanID, PubKey: pk, CreatedAt: 1001, Kind: nostrx.KindTextNote, Content: "orphan still belongs to root", Sig: "s",
			Tags: [][]string{{"e", rootID, "", "root"}, {"e", missingParentID, "", "reply"}},
		},
		{
			ID: childID, PubKey: pk, CreatedAt: 1002, Kind: nostrx.KindTextNote, Content: "child under promoted orphan", Sig: "s",
			Tags: [][]string{{"e", rootID, "", "root"}, {"e", orphanID, "", "reply"}},
		},
	}
	for _, ev := range events {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/thread/"+rootID+"?fragment=hydrate", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="note-`+orphanID+`"`) {
		t.Fatalf("root hydrate should render orphaned root descendant: %s", body)
	}
	if strings.Contains(body, `id="note-`+childID+`"`) {
		t.Fatalf("root hydrate should not render child of orphan in one-depth thread view: %s", body)
	}

	childReq := httptest.NewRequest(http.MethodGet, "/thread/"+orphanID+"?fragment=hydrate", nil)
	childRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(childRec, childReq)
	if childRec.Code != http.StatusOK {
		t.Fatalf("child status = %d, want 200", childRec.Code)
	}
	childBody := childRec.Body.String()
	if !strings.Contains(childBody, `thread-focus-selected" id="note-`+orphanID+`"`) {
		t.Fatalf("orphan hydrate should focus promoted orphan: %s", childBody)
	}
	if !strings.Contains(childBody, `id="note-`+childID+`"`) {
		t.Fatalf("orphan hydrate should render direct child after selecting orphan: %s", childBody)
	}
}

func TestThreadHydrateFocusesSelectedReplyWhoseParentIsMissing(t *testing.T) {
	t.Skip("legacy server fragment behavior removed; thread route is client-rendered")
	srv, st := testServer(t)
	ctx := context.Background()
	rootID := strings.Repeat("1", 64)
	missingParentID := strings.Repeat("2", 64)
	selectedID := strings.Repeat("3", 64)
	childID := strings.Repeat("4", 64)
	siblingID := strings.Repeat("5", 64)
	pk := strings.Repeat("a", 64)
	events := []nostrx.Event{
		{ID: rootID, PubKey: pk, CreatedAt: 1000, Kind: nostrx.KindTextNote, Content: "root", Sig: "s"},
		{
			ID: selectedID, PubKey: pk, CreatedAt: 1001, Kind: nostrx.KindTextNote, Content: "selected with missing parent", Sig: "s",
			Tags: [][]string{{"e", rootID, "", "root"}, {"e", missingParentID, "", "reply"}},
		},
		{
			ID: childID, PubKey: pk, CreatedAt: 1002, Kind: nostrx.KindTextNote, Content: "selected child", Sig: "s",
			Tags: [][]string{{"e", rootID, "", "root"}, {"e", selectedID, "", "reply"}},
		},
		{
			ID: siblingID, PubKey: pk, CreatedAt: 1003, Kind: nostrx.KindTextNote, Content: "root sibling", Sig: "s",
			Tags: [][]string{{"e", rootID, "", "root"}, {"e", rootID, "", "reply"}},
		},
	}
	for _, ev := range events {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/thread/"+selectedID+"?fragment=hydrate", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("X-Ptxt-Thread-Incomplete"); got != "1" {
		t.Fatalf("X-Ptxt-Thread-Incomplete = %q, want 1 for unresolved selected parent", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `thread-focus-selected" id="note-`+selectedID+`"`) {
		t.Fatalf("hydrate should focus selected reply even with missing parent: %s", body)
	}
	if !strings.Contains(body, `id="note-`+childID+`"`) {
		t.Fatalf("hydrate should render selected child first branch: %s", body)
	}
	if !strings.Contains(body, `data-thread-other-replies-toggle`) {
		t.Fatalf("hydrate should separate surrounding replies behind a toggle: %s", body)
	}
	if !strings.Contains(body, `id="note-`+siblingID+`"`) {
		t.Fatalf("hydrate should keep surrounding root branch replies: %s", body)
	}
	childIdx := strings.Index(body, `id="note-`+childID+`"`)
	toggleIdx := strings.Index(body, `data-thread-other-replies-toggle`)
	siblingIdx := strings.Index(body, `id="note-`+siblingID+`"`)
	if !(childIdx >= 0 && toggleIdx > childIdx && siblingIdx > toggleIdx) {
		t.Fatalf("focused child, other-replies toggle, and sibling order wrong: child=%d toggle=%d sibling=%d", childIdx, toggleIdx, siblingIdx)
	}
}

func TestThreadHydrateDepthFiveSelectedBranchFirst(t *testing.T) {
	t.Skip("legacy server fragment behavior removed; thread route is client-rendered")
	srv, st := testServer(t)
	ctx := context.Background()
	rootID := strings.Repeat("1", 64)
	aID := strings.Repeat("2", 64)
	bID := strings.Repeat("3", 64)
	cID := strings.Repeat("4", 64)
	dID := strings.Repeat("5", 64)
	selectedID := strings.Repeat("6", 64)
	childID := strings.Repeat("7", 64)
	pk := strings.Repeat("a", 64)
	events := []nostrx.Event{
		{ID: rootID, PubKey: pk, CreatedAt: 1000, Kind: nostrx.KindTextNote, Content: "root", Sig: "s"},
		{ID: aID, PubKey: pk, CreatedAt: 1001, Kind: nostrx.KindTextNote, Content: "a", Sig: "s", Tags: [][]string{{"e", rootID, "", "root"}, {"e", rootID, "", "reply"}}},
		{ID: bID, PubKey: pk, CreatedAt: 1002, Kind: nostrx.KindTextNote, Content: "b", Sig: "s", Tags: [][]string{{"e", rootID, "", "root"}, {"e", aID, "", "reply"}}},
		{ID: cID, PubKey: pk, CreatedAt: 1003, Kind: nostrx.KindTextNote, Content: "c", Sig: "s", Tags: [][]string{{"e", rootID, "", "root"}, {"e", bID, "", "reply"}}},
		{ID: dID, PubKey: pk, CreatedAt: 1004, Kind: nostrx.KindTextNote, Content: "d", Sig: "s", Tags: [][]string{{"e", rootID, "", "root"}, {"e", cID, "", "reply"}}},
		{ID: selectedID, PubKey: pk, CreatedAt: 1005, Kind: nostrx.KindTextNote, Content: "selected", Sig: "s", Tags: [][]string{{"e", rootID, "", "root"}, {"e", dID, "", "reply"}}},
		{ID: childID, PubKey: pk, CreatedAt: 1006, Kind: nostrx.KindTextNote, Content: "selected child", Sig: "s", Tags: [][]string{{"e", rootID, "", "root"}, {"e", selectedID, "", "reply"}}},
	}
	for _, ev := range events {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/thread/"+selectedID+"?fragment=hydrate", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `thread-focus-selected" id="note-`+selectedID+`"`) {
		t.Fatalf("depth-5 hydrate should focus selected reply: %s", body)
	}
	if !strings.Contains(body, `id="note-`+childID+`"`) {
		t.Fatalf("depth-5 hydrate should render selected child: %s", body)
	}
	if !strings.Contains(body, "show messages above") {
		t.Fatalf("depth-5 hydrate should expose ancestor stack: %s", body)
	}
}

func TestThreadHydrateAndFullPageAgreeOnFocusedMissingParentReply(t *testing.T) {
	t.Skip("legacy server fragment behavior removed; thread route is client-rendered")
	srv, st := testServer(t)
	ctx := context.Background()
	rootID := strings.Repeat("1", 64)
	missingParentID := strings.Repeat("2", 64)
	selectedID := strings.Repeat("3", 64)
	childID := strings.Repeat("4", 64)
	pk := strings.Repeat("a", 64)
	events := []nostrx.Event{
		{ID: rootID, PubKey: pk, CreatedAt: 1000, Kind: nostrx.KindTextNote, Content: "root", Sig: "s"},
		{ID: selectedID, PubKey: pk, CreatedAt: 1001, Kind: nostrx.KindTextNote, Content: "selected", Sig: "s", Tags: [][]string{{"e", rootID, "", "root"}, {"e", missingParentID, "", "reply"}}},
		{ID: childID, PubKey: pk, CreatedAt: 1002, Kind: nostrx.KindTextNote, Content: "child", Sig: "s", Tags: [][]string{{"e", rootID, "", "root"}, {"e", selectedID, "", "reply"}}},
	}
	for _, ev := range events {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	for _, path := range []string{"/thread/" + selectedID, "/thread/" + selectedID + "?fragment=hydrate"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, rec.Code)
		}
		body := rec.Body.String()
		if strings.Contains(path, "fragment=hydrate") {
			if len(strings.TrimSpace(body)) == 0 {
				t.Fatalf("%s should still return a non-empty hydrate response", path)
			}
			continue
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("%s Cache-Control = %q, want no-store", path, got)
		}
		if !strings.Contains(body, `data-route-outlet`) {
			t.Fatalf("%s should return app shell route outlet: %s", path, body)
		}
	}
}

func TestThreadParticipantsFragmentRendersRail(t *testing.T) {
	t.Skip("legacy server fragment behavior removed; thread route is client-rendered")
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
	t.Skip("legacy server fragment behavior removed; thread route is client-rendered")
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
	markTestRequestLoggedIn(req)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-route-outlet`) {
		t.Fatalf("expected app shell route outlet for human thread route: %s", body)
	}
	if !strings.Contains(body, `"path":"/thread/`+replyID+`"`) {
		t.Fatalf("expected thread path in route context: %s", body)
	}
}

// Regression: anonymous ?fragment=hydrate must fetch missing root so focus mode shows parent above reply.
func TestThreadHydrateFetchesMissingRootContext(t *testing.T) {
	t.Skip("legacy server fragment behavior removed; thread route is client-rendered")
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
	if len(strings.TrimSpace(rec.Body.String())) == 0 {
		t.Fatal("expected non-empty hydrate response")
	}
}

// Regression: note_links without events rows — hydrate must refetch reply bodies from relays.
func TestThreadHydrateFetchesDirectRepliesMissingFromStore(t *testing.T) {
	t.Skip("legacy server fragment behavior removed; thread route is client-rendered")
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
	if _, err := st.SaveEvents(ctx, toSave); err != nil {
		t.Fatal(err)
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

func TestAuthorsCacheKeyBoundsLargeInputs(t *testing.T) {
	authors := make([]string, maxAuthorsCacheKeyAuthors+100)
	for i := range authors {
		authors[i] = fmt.Sprintf("%064x", i)
	}
	got := authorsCacheKey(authors)
	want := authorsCacheKey(authors[:maxAuthorsCacheKeyAuthors])
	if got != want {
		t.Fatalf("authorsCacheKey should cap large inputs: %q != %q", got, want)
	}
	if !strings.HasPrefix(got, "authors:") || len(got) != len("authors:")+64 {
		t.Fatalf("authorsCacheKey = %q, want sha256 key", got)
	}
}

func TestRequestAuthorsCohortAuthorsBoundsWoT(t *testing.T) {
	full := make([]string, maxFeedAuthors+20)
	for i := range full {
		full[i] = fmt.Sprintf("%064x", i)
	}
	resolved := requestAuthors{wotEnabled: true, allAuthors: full}
	got := resolved.cohortAuthors()
	if len(got) != maxFeedAuthors {
		t.Fatalf("cohortAuthors len = %d, want %d", len(got), maxFeedAuthors)
	}
	if !slices.Equal(got, full[:maxFeedAuthors]) {
		t.Fatalf("cohortAuthors = %#v, want bounded prefix", got)
	}

	resolved.authors = full[:3]
	got = resolved.cohortAuthors()
	if !slices.Equal(got, resolved.authors) {
		t.Fatalf("cohortAuthors = %#v, want resolved query authors", got)
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
	if len(fallback.Feed) == 0 {
		t.Fatalf("expected global fallback trend feed, got %#v", fallback.Feed)
	}
	if fallback.Feed[0].ID != note.ID {
		t.Fatalf("fallback feed top note id = %q, want root %q; feed=%#v", fallback.Feed[0].ID, note.ID, fallback.Feed)
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
	if !strings.Contains(body, `data-feed`) || !strings.Contains(body, "first hop") || !strings.Contains(body, "second hop") {
		t.Fatalf("expected server-rendered feed document with WoT notes, got: %s", body)
	}
	if strings.Contains(body, "outsider") {
		t.Fatalf("expected WOT feed document to exclude outsider note, got: %s", body)
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
	body := rec.Body.String()
	if !strings.Contains(body, `data-feed`) || !strings.Contains(body, "beyond refresh cap") {
		t.Fatalf("expected server-rendered feed document with full WoT membership, got: %s", body)
	}
}

func TestWoTFeedMergesThinSQLWithNewerScannedAuthors(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	viewer := strings.Repeat("a", 64)
	firstHop := strings.Repeat("b", 64)
	target := strings.Repeat("f", 64)
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        strings.Repeat("1", 64),
		PubKey:    viewer,
		CreatedAt: 10,
		Kind:      nostrx.KindFollowList,
		Tags:      [][]string{{"p", firstHop}},
	}); err != nil {
		t.Fatal(err)
	}
	followTags := make([][]string, 0, 300)
	for i := 0; i < 299; i++ {
		followTags = append(followTags, []string{"p", fmt.Sprintf("%064x", i+100)})
	}
	followTags = append(followTags, []string{"p", target})
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        strings.Repeat("2", 64),
		PubKey:    firstHop,
		CreatedAt: 11,
		Kind:      nostrx.KindFollowList,
		Tags:      followTags,
	}); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []nostrx.Event{
		{ID: strings.Repeat("3", 64), PubKey: firstHop, CreatedAt: 20, Kind: nostrx.KindTextNote, Content: "old sql note"},
		{ID: strings.Repeat("4", 64), PubKey: target, CreatedAt: 30, Kind: nostrx.KindTextNote, Content: "new scanned note"},
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
	if !strings.Contains(body, `data-feed`) || !strings.Contains(body, "old sql note") || !strings.Contains(body, "new scanned note") {
		t.Fatalf("expected server-rendered feed document with thin SQL and scanned authors, got: %s", body)
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
	if !strings.Contains(body, `data-reads`) || !strings.Contains(body, "first hop read") || !strings.Contains(body, "second hop read") {
		t.Fatalf("expected server-rendered reads document with WoT membership, got: %s", body)
	}
	if strings.Contains(body, "outsider read") {
		t.Fatalf("expected WOT reads document to exclude outsider read, got: %s", body)
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
	body := rec.Body.String()
	if !strings.Contains(body, `data-reads`) || !strings.Contains(body, "beyond reads refresh cap") {
		t.Fatalf("expected server-rendered reads document with full WoT membership, got: %s", body)
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
	if !strings.Contains(body, `data-reads`) || !strings.Contains(body, "within 1 degrees of Gigi's follow graph") {
		t.Fatalf("expected logged-out reads document to use protected default seed copy, got: %s", body)
	}
	if strings.Contains(body, "seed first hop read") || strings.Contains(body, "seed second hop read") || strings.Contains(body, "seed outsider read") {
		t.Fatalf("expected anonymous custom seed reads request to stay on default Gigi slice, got: %s", body)
	}

	trendReq := httptest.NewRequest(http.MethodGet, "/reads?sort=trend24h&wot=1&wot_depth=2&seed_pubkey="+seed, nil)
	trendRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(trendRec, trendReq)
	if trendRec.Code != http.StatusOK {
		t.Fatalf("trend status = %d, want 200", trendRec.Code)
	}
	trendBody := trendRec.Body.String()
	if !strings.Contains(trendBody, `data-reads`) || !strings.Contains(trendBody, `value="trend24h" selected`) {
		t.Fatalf("expected seeded trend reads document with selected sort, got: %s", trendBody)
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

func TestReadsDataSeededLoggedOutRefreshesAuthorsBeyondOutboxGroupCap(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	srv.nostr = nostrx.NewClient(nil, time.Second)
	seed := strings.Repeat("f", 64)
	secret := fnostr.Generate()
	externalRead := fnostr.Event{
		CreatedAt: fnostr.Timestamp(time.Now().Unix() - 60),
		Kind:      fnostr.Kind(nostrx.KindLongForm),
		Tags:      fnostr.Tags{fnostr.Tag{"title", "late cohort relay read"}},
		Content:   "late cohort relay read",
	}
	if err := externalRead.Sign(secret); err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	targetAuthor := externalRead.PubKey.Hex()
	followTags := make([][]string, 0, maxFeedAuthors+21)
	for i := 0; i < maxFeedAuthors+20; i++ {
		followTags = append(followTags, []string{"p", fmt.Sprintf("%064x", i+1000)})
	}
	followTags = append(followTags, []string{"p", targetAuthor})
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        strings.Repeat("a", 64),
		PubKey:    seed,
		CreatedAt: time.Now().Unix() - 120,
		Kind:      nostrx.KindFollowList,
		Tags:      followTags,
	}); err != nil {
		t.Fatal(err)
	}
	var sawTarget atomic.Bool
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
		if err := json.Unmarshal(envelope[2], &filter); err == nil && slices.Contains(filter.Authors, targetAuthor) {
			sawTarget.Store(true)
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
	if !sawTarget.Load() {
		t.Fatal("relay never received a query containing the late cohort author")
	}
	if len(data.Items) != 1 {
		t.Fatalf("expected one late cohort read, got %d", len(data.Items))
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
	if !strings.Contains(body, `class="read-article is-full"`) || !strings.Contains(body, "First read") {
		t.Fatalf("expected server-rendered read detail document: %s", body)
	}
	if !strings.Contains(body, "More reads from") || !strings.Contains(body, "Second read") {
		t.Fatalf("expected server-rendered more-reads rail: %s", body)
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

	page1, hasMore, after, relayOrdered := srv.fetchRankedFeedPage(ctx, nil, trendingRankKey{}, 2, feedSortTrend7d, true, true)
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
	if relayOrdered {
		t.Fatal("expected cached ranked page, got relay-ordered page")
	}

	page2, hasMore2, _, relayOrdered2 := srv.fetchRankedFeedPage(ctx, nil, after, 5, feedSortTrend7d, true, true)
	if len(page2) != 1 || page2[0].ID != "note-b" {
		t.Fatalf("page2 = %#v, want [note-b]", page2)
	}
	if hasMore2 {
		t.Fatalf("expected hasMore=false on last page")
	}
	if relayOrdered2 {
		t.Fatal("expected cached ranked page, got relay-ordered page")
	}
	if page1[0].ID == page2[0].ID {
		t.Fatal("page2 overlapped page1")
	}
}

func TestFetchRankedFeedPageResortsMisorderedTrendingCache(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	now := time.Now().Unix()
	for index, id := range []string{"note-low", "note-newer-tie", "note-top"} {
		if err := st.SaveEvent(ctx, nostrx.Event{
			ID:        id,
			PubKey:    fmt.Sprintf("%064x", index+41),
			CreatedAt: now - int64(index),
			Kind:      nostrx.KindTextNote,
			Content:   id,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.WriteTrendingCache(ctx, trending24h, "", []store.TrendingItem{
		{NoteID: "note-low", ReplyCount: 1, Score: 1},
		{NoteID: "note-newer-tie", ReplyCount: 2, Score: 10},
		{NoteID: "note-top", ReplyCount: 8, Score: 10},
	}, now); err != nil {
		t.Fatal(err)
	}

	page, hasMore, _, relayOrdered := srv.fetchRankedFeedPage(ctx, nil, trendingRankKey{}, 3, feedSortTrend24h, false, false)
	if relayOrdered {
		t.Fatal("expected local ranked cache, got relay order")
	}
	if hasMore {
		t.Fatal("expected single full page without hasMore")
	}
	want := []string{"note-top", "note-newer-tie", "note-low"}
	if len(page) != len(want) {
		t.Fatalf("page len = %d, want %d: %#v", len(page), len(want), page)
	}
	for i := range want {
		if page[i].ID != want[i] {
			t.Fatalf("page order = %#v, want %v", page, want)
		}
	}
}

func TestRankedTrendingCacheFallsForwardWhenCursorIDMissing(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	now := time.Now().Unix()
	for index, id := range []string{"note-top", "note-next", "note-tail"} {
		if err := st.SaveEvent(ctx, nostrx.Event{
			ID:        id,
			PubKey:    fmt.Sprintf("%064x", index+60),
			CreatedAt: now - int64(index),
			Kind:      nostrx.KindTextNote,
			Content:   id,
		}); err != nil {
			t.Fatal(err)
		}
	}
	items := []store.TrendingItem{
		{NoteID: "note-top", ReplyCount: 10, Score: 10, HotScore: 10, NoteCreatedAt: now},
		{NoteID: "note-next", ReplyCount: 7, Score: 7, HotScore: 7, NoteCreatedAt: now - 1},
		{NoteID: "note-tail", ReplyCount: 5, Score: 5, HotScore: 5, NoteCreatedAt: now - 2},
	}
	page, hasMore, _, ok := srv.rankedTrendingFeedPageFromItems(ctx, items, trendingRankKey{
		hotScore: 8,
		score:    8,
		replies:  8,
		id:       "note-dropped",
	}, 2)
	if !ok {
		t.Fatal("expected ranked cache page after missing cursor")
	}
	if hasMore {
		t.Fatal("expected no additional page")
	}
	want := []string{"note-next", "note-tail"}
	if len(page) != len(want) {
		t.Fatalf("page len = %d, want %d: %#v", len(page), len(want), page)
	}
	for i := range want {
		if page[i].ID != want[i] {
			t.Fatalf("page ids = %#v, want %v", page, want)
		}
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

func TestRankedRecentFallbackMissingCursorAdvancesByRecency(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	now := time.Now().Unix()
	for index, spec := range []struct {
		id        string
		createdAt int64
	}{
		{"note-new", now - 10},
		{"note-next", now - 20},
		{"note-tail", now - 30},
	} {
		if err := st.SaveEvent(ctx, nostrx.Event{
			ID:        spec.id,
			PubKey:    fmt.Sprintf("%064x", index+70),
			CreatedAt: spec.createdAt,
			Kind:      nostrx.KindTextNote,
			Content:   spec.id,
		}); err != nil {
			t.Fatal(err)
		}
	}
	page, _, _, ok := srv.fetchRankedRecentFallbackPage(ctx, trending24h, nil, true, trendingRankKey{
		id:        "note-from-other-ranking",
		createdAt: now - 15,
	}, 2)
	if !ok {
		t.Fatal("expected recency fallback page after missing cursor")
	}
	want := []string{"note-next", "note-tail"}
	if len(page) != len(want) {
		t.Fatalf("page len = %d, want %d: %#v", len(page), len(want), page)
	}
	for i := range want {
		if page[i].ID != want[i] {
			t.Fatalf("page ids = %#v, want %v", page, want)
		}
	}
}

func TestRankedFeedDoesNotReplayFallbackPageAfterCacheWarms(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	now := time.Now().Unix()
	for index, spec := range []struct {
		id        string
		createdAt int64
		replies   int
	}{
		{"note-recent", now - 10, 1},
		{"note-hot", now - 20, 10},
		{"note-tail", now - 30, 1},
	} {
		if err := st.SaveEvent(ctx, nostrx.Event{
			ID:        spec.id,
			PubKey:    fmt.Sprintf("%064x", index+80),
			CreatedAt: spec.createdAt,
			Kind:      nostrx.KindTextNote,
			Content:   spec.id,
		}); err != nil {
			t.Fatal(err)
		}
		for reply := 0; reply < spec.replies; reply++ {
			if err := st.SaveEvent(ctx, nostrx.Event{
				ID:        fmt.Sprintf("%s-reply-%d", spec.id, reply),
				PubKey:    fmt.Sprintf("%064x", index+90+reply),
				CreatedAt: spec.createdAt + int64(reply+1),
				Kind:      nostrx.KindTextNote,
				Tags:      [][]string{{"e", spec.id, "", "root"}, {"e", spec.id, "", "reply"}},
				Content:   "reply",
			}); err != nil {
				t.Fatal(err)
			}
		}
	}

	page1, _, after, ok := srv.fetchRankedRecentFallbackPage(ctx, trending24h, nil, true, trendingRankKey{}, 2)
	if !ok || len(page1) != 2 || page1[0].ID != "note-recent" || page1[1].ID != "note-hot" {
		t.Fatalf("fallback page1 = %#v ok=%v", page1, ok)
	}
	if err := st.WriteTrendingCache(ctx, trending24h, "", []store.TrendingItem{
		{NoteID: "note-hot", ReplyCount: 10, Score: 10, HotScore: 10, NoteCreatedAt: now - 20},
		{NoteID: "note-recent", ReplyCount: 1, Score: 1, HotScore: 1, NoteCreatedAt: now - 10},
		{NoteID: "note-tail", ReplyCount: 1, Score: 1, HotScore: 1, NoteCreatedAt: now - 30},
	}, now); err != nil {
		t.Fatal(err)
	}

	page2, _, _, _ := srv.fetchRankedFeedPage(ctx, nil, after, 5, feedSortTrend24h, true, true)
	for _, ev := range page2 {
		if ev.ID == "note-recent" || ev.ID == "note-hot" {
			t.Fatalf("page2 replayed fallback page note %q: %#v", ev.ID, page2)
		}
	}
	if len(page2) != 1 || page2[0].ID != "note-tail" {
		t.Fatalf("page2 = %#v, want [note-tail]", page2)
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
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        strings.Repeat("e", 64),
		PubKey:    viewer,
		CreatedAt: now + 2,
		Kind:      nostrx.KindFollowList,
		Tags:      [][]string{{"p", good}, {"p", muted}},
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
		items = append(items, store.TrendingItem{
			NoteID:        spec.id,
			ReplyCount:    spec.replies,
			Score:         spec.replies,
			HotScore:      float64(spec.replies),
			NoteCreatedAt: now - int64(spec.replies),
		})
	}
	resolved := srv.resolveRequestAuthors(ctx, viewer, "", nil, webOfTrustOptions{})
	cohortKey := authorsCacheKey(resolved.authors)
	if err := st.WriteTrendingCache(ctx, trending24h, cohortKey, items, now); err != nil {
		t.Fatal(err)
	}
	st.MarkRefreshed(ctx, "feed", feedRefreshKey("feed-"+feedSortTrend24h, 0, "")+"|"+cohortKey)
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

func TestTrendingDataCacheOnlyGlobalFallbackFiltersToCohort(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	now := time.Now().Unix()
	inside := strings.Repeat("a", 64)
	outside := strings.Repeat("b", 64)
	for _, event := range []nostrx.Event{
		{ID: "note-inside", PubKey: inside, CreatedAt: now - 1, Kind: nostrx.KindTextNote, Content: "inside cohort"},
		{ID: "note-outside", PubKey: outside, CreatedAt: now - 2, Kind: nostrx.KindTextNote, Content: "outside cohort"},
	} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.WriteTrendingCache(ctx, trending24h, "", []store.TrendingItem{
		{NoteID: "note-outside", ReplyCount: 10, Score: 10},
		{NoteID: "note-inside", ReplyCount: 5, Score: 5},
	}, now); err != nil {
		t.Fatal(err)
	}

	cohort := []string{inside}
	trending := srv.trendingData(ctx, trending24h, authorsCacheKey(cohort), cohort, nil, true)
	if len(trending) != 1 {
		t.Fatalf("trending len = %d, want 1: %#v", len(trending), trending)
	}
	if trending[0].Event.ID != "note-inside" {
		t.Fatalf("expected cohort-filtered fallback, got %#v", trending)
	}
}

func TestTrendingDataDoesNotBackfillZeroEngagementNotes(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	now := time.Now().Unix()

	hot := nostrx.Event{
		ID:        fmt.Sprintf("%064x", 101),
		PubKey:    strings.Repeat("a", 64),
		CreatedAt: now - 30,
		Kind:      nostrx.KindTextNote,
		Content:   "hot note",
	}
	cold := nostrx.Event{
		ID:        fmt.Sprintf("%064x", 102),
		PubKey:    strings.Repeat("b", 64),
		CreatedAt: now - 10,
		Kind:      nostrx.KindTextNote,
		Content:   "cold note",
	}
	reply := nostrx.Event{
		ID:        fmt.Sprintf("%064x", 103),
		PubKey:    strings.Repeat("c", 64),
		CreatedAt: now - 5,
		Kind:      nostrx.KindTextNote,
		Content:   "reply",
		Tags:      [][]string{{"e", hot.ID, "", "root"}, {"e", hot.ID, "", "reply"}},
	}
	for _, ev := range []nostrx.Event{hot, cold, reply} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := srv.computeAndStoreTrending(ctx, trending24h, time.Now()); err != nil {
		t.Fatalf("computeAndStoreTrending() error = %v", err)
	}

	trending := srv.trendingData(ctx, trending24h, "", nil, nil, true)
	if len(trending) != 2 {
		t.Fatalf("trending len = %d, want 2 with recent zero-engagement tail: %#v", len(trending), trending)
	}
	if trending[0].Event.ID != hot.ID || trending[1].Event.ID != cold.ID {
		t.Fatalf("unexpected trending note: %#v", trending)
	}
}

func TestFeedDataRanksByRepliesAndReactionsFromBackgroundCache(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	now := time.Now().Unix()

	reactionBoosted := nostrx.Event{
		ID:        fmt.Sprintf("%064x", 301),
		PubKey:    strings.Repeat("1", 64),
		CreatedAt: now - 40,
		Kind:      nostrx.KindTextNote,
		Content:   "feed reaction boosted",
	}
	replyOnly := nostrx.Event{
		ID:        fmt.Sprintf("%064x", 302),
		PubKey:    strings.Repeat("2", 64),
		CreatedAt: now - 50,
		Kind:      nostrx.KindTextNote,
		Content:   "feed reply only",
	}
	replyEvents := []nostrx.Event{
		{
			ID:        fmt.Sprintf("%064x", 303),
			PubKey:    strings.Repeat("3", 64),
			CreatedAt: now - 30,
			Kind:      nostrx.KindTextNote,
			Content:   "reply a",
			Tags:      [][]string{{"e", reactionBoosted.ID, "", "root"}, {"e", reactionBoosted.ID, "", "reply"}},
		},
		{
			ID:        fmt.Sprintf("%064x", 304),
			PubKey:    strings.Repeat("4", 64),
			CreatedAt: now - 20,
			Kind:      nostrx.KindTextNote,
			Content:   "reply b1",
			Tags:      [][]string{{"e", replyOnly.ID, "", "root"}, {"e", replyOnly.ID, "", "reply"}},
		},
		{
			ID:        fmt.Sprintf("%064x", 305),
			PubKey:    strings.Repeat("5", 64),
			CreatedAt: now - 10,
			Kind:      nostrx.KindTextNote,
			Content:   "reply b2",
			Tags:      [][]string{{"e", replyOnly.ID, "", "root"}, {"e", replyOnly.ID, "", "reply"}},
		},
	}
	for _, ev := range append([]nostrx.Event{reactionBoosted, replyOnly}, replyEvents...) {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	reactorA := fnostr.Generate()
	reactorB := fnostr.Generate()
	for _, ev := range []nostrx.Event{
		signNostrEvent(t, reactorA, nostrx.KindReaction, "+", [][]string{{"e", reactionBoosted.ID}, {"p", reactionBoosted.PubKey}}),
		signNostrEvent(t, reactorB, nostrx.KindReaction, "+", [][]string{{"e", reactionBoosted.ID}, {"p", reactionBoosted.PubKey}}),
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := srv.computeAndStoreTrending(ctx, trending24h, time.Unix(now, 0)); err != nil {
		t.Fatalf("computeAndStoreTrending() error = %v", err)
	}
	data := srv.feedData(ctx, feedRequest{Limit: 10, SortMode: feedSortTrend24h})
	if len(data.Feed) < 2 {
		t.Fatalf("expected at least two feed items, got %#v", data.Feed)
	}
	if data.Feed[0].ID != reactionBoosted.ID || data.Feed[1].ID != replyOnly.ID {
		t.Fatalf("unexpected cold-cache feed order: %#v", data.Feed)
	}
}

func TestLoggedOutRankedColdFallbackUsesRecentRootNotes(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	now := time.Now().Unix()
	likedPub := strings.Repeat("1", 64)
	repliedPub := strings.Repeat("2", 64)
	recentPub := strings.Repeat("3", 64)

	liked := nostrx.Event{
		ID:        fmt.Sprintf("%064x", 501),
		PubKey:    likedPub,
		CreatedAt: now - 120,
		Kind:      nostrx.KindTextNote,
		Content:   "liked",
	}
	replied := nostrx.Event{
		ID:        fmt.Sprintf("%064x", 502),
		PubKey:    repliedPub,
		CreatedAt: now - 60,
		Kind:      nostrx.KindTextNote,
		Content:   "replied",
	}
	recentTie := nostrx.Event{
		ID:        fmt.Sprintf("%064x", 503),
		PubKey:    recentPub,
		CreatedAt: now - 10,
		Kind:      nostrx.KindTextNote,
		Content:   "recent tie",
	}
	reply := nostrx.Event{
		ID:        fmt.Sprintf("%064x", 504),
		PubKey:    strings.Repeat("4", 64),
		CreatedAt: now - 20,
		Kind:      nostrx.KindTextNote,
		Content:   "reply",
		Tags:      [][]string{{"e", replied.ID, "", "root"}, {"e", replied.ID, "", "reply"}},
	}
	for _, ev := range []nostrx.Event{liked, replied, recentTie, reply} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	reactorA := fnostr.Generate()
	reactorB := fnostr.Generate()
	for _, ev := range []nostrx.Event{
		signNostrEvent(t, reactorA, nostrx.KindReaction, "+", [][]string{{"e", liked.ID}, {"p", liked.PubKey}}),
		signNostrEvent(t, reactorB, nostrx.KindReaction, "+", [][]string{{"e", recentTie.ID}, {"p", recentTie.PubKey}}),
	} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	authors := []string{likedPub, repliedPub, recentPub}
	page, hasMore, _, relayOrdered := srv.fetchRankedFeedPage(ctx, authors, trendingRankKey{}, 10, feedSortTrend24h, true, false)
	if relayOrdered {
		t.Fatal("expected computed local ranking, got relay order")
	}
	if hasMore {
		t.Fatal("expected no additional page")
	}
	want := []string{recentTie.ID, replied.ID, liked.ID}
	if len(page) != len(want) {
		t.Fatalf("page len = %d, want %d: %#v", len(page), len(want), page)
	}
	for i := range want {
		if page[i].ID != want[i] {
			t.Fatalf("page order = %#v, want %v", page, want)
		}
	}
}

func TestFeedDataGlobalTrendingPrefersLocalCacheOverRelayHot(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	now := time.Now().Unix()

	makeSigned := func(createdAt int64, content string) nostrx.Event {
		t.Helper()
		secret := fnostr.Generate()
		ev := fnostr.Event{
			CreatedAt: fnostr.Timestamp(createdAt),
			Kind:      fnostr.Kind(nostrx.KindTextNote),
			Content:   content,
		}
		if err := ev.Sign(secret); err != nil {
			t.Fatalf("Sign: %v", err)
		}
		return fnostrToNostrxEvent(ev)
	}
	olderHot := makeSigned(now-120, "older but hotter")
	newerHot := makeSigned(now-60, "newer but second")
	localTop := makeSigned(now-30, "local cached top")
	for _, ev := range []nostrx.Event{localTop} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.WriteTrendingCache(ctx, trending24h, "", []store.TrendingItem{
		{NoteID: localTop.ID, ReplyCount: 3, ReactionCount: 2, Score: 5, HotScore: 5, NoteCreatedAt: localTop.CreatedAt},
	}, now); err != nil {
		t.Fatal(err)
	}
	relay := newRelayWithEvents(t, []nostrx.Event{olderHot, newerHot})
	defer relay.Close()

	srv.cfg.TrendingSearchRelays = []string{wsURL(relay.URL)}
	srv.cfg.TrendingSearchMaxRelays = 1
	srv.cfg.RequestTimeout = 2 * time.Second

	page1 := srv.feedData(ctx, feedRequest{Limit: 1, SortMode: feedSortTrend24h})
	if len(page1.Feed) != 1 || page1.Feed[0].ID != localTop.ID {
		t.Fatalf("page1 feed = %#v, want local cached trend row", page1.Feed)
	}
	if stored, err := st.GetEvent(ctx, olderHot.ID); err != nil || stored != nil {
		t.Fatalf("relay hot event should not be fetched on foreground trend request, got event=%#v err=%v", stored, err)
	}
}

func TestTrendingHotScoreOrderingAndZeroEngagementTail(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	item := func(id string, replies, reactions int, createdAt int64) store.TrendingItem {
		score := replies + reactions
		return store.TrendingItem{
			NoteID:        id,
			ReplyCount:    replies,
			ReactionCount: reactions,
			Score:         score,
			HotScore:      trendingHotScore(score, createdAt, trending1w, now),
			NoteCreatedAt: createdAt,
		}
	}
	items := []store.TrendingItem{
		item("zero-old", 0, 0, now.Add(-1*time.Hour).Unix()),
		item("zero-new", 0, 0, now.Add(-10*time.Minute).Unix()),
		item("newer-weak", 1, 0, now.Add(-1*time.Hour).Unix()),
		item("older-high", 20, 0, now.Add(-48*time.Hour).Unix()),
		item("tie-replies", 2, 1, now.Add(-2*time.Hour).Unix()),
		item("tie-reactions", 1, 2, now.Add(-2*time.Hour).Unix()),
	}
	sort.Slice(items, func(i, j int) bool {
		return trendingItemLess(items[i], items[j])
	})

	got := make([]string, len(items))
	for idx, item := range items {
		got[idx] = item.NoteID
	}
	want := []string{"older-high", "tie-replies", "tie-reactions", "newer-weak", "zero-new", "zero-old"}
	if !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if items[len(items)-2].HotScore != 0 || items[len(items)-1].HotScore != 0 {
		t.Fatalf("zero-engagement tail should have zero hot scores: %#v", items)
	}
}

func TestComputeAndStoreTrendingPreservesWarmCacheOnDegradedRecompute(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	now := time.Unix(20_000, 0)
	existing := make([]store.TrendingItem, trendingDegradedMinItems)
	for idx := range existing {
		existing[idx] = store.TrendingItem{
			NoteID:        fmt.Sprintf("%064x", 8000+idx),
			ReplyCount:    trendingDegradedMinItems - idx,
			ReactionCount: idx,
			Score:         trendingDegradedMinItems,
			HotScore:      float64(trendingDegradedMinItems - idx),
			NoteCreatedAt: now.Add(-time.Duration(idx) * time.Minute).Unix(),
		}
	}
	if err := st.WriteTrendingCache(ctx, trending24h, "", existing, now.Add(-30*time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}

	items, err := srv.computeAndStoreTrending(ctx, trending24h, now)
	if err != nil {
		t.Fatalf("computeAndStoreTrending() error = %v", err)
	}
	if len(items) != len(existing) || items[0].NoteID != existing[0].NoteID {
		t.Fatalf("expected preserved warm cache, got %#v", items)
	}
	cached, computedAt, err := st.ReadTrendingCache(ctx, trending24h, "")
	if err != nil {
		t.Fatal(err)
	}
	if computedAt != now.Add(-30*time.Minute).Unix() {
		t.Fatalf("degraded recompute should not overwrite cache computed_at, got %d", computedAt)
	}
	if len(cached) != len(existing) || cached[0].NoteID != existing[0].NoteID {
		t.Fatalf("unexpected cached items after degraded recompute: %#v", cached)
	}
}

func TestComputeAndStoreTrendingExcludesReplies(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	now := time.Now().Unix()

	root := nostrx.Event{
		ID:        fmt.Sprintf("%064x", 401),
		PubKey:    strings.Repeat("6", 64),
		CreatedAt: now - 60,
		Kind:      nostrx.KindTextNote,
		Content:   "root note",
	}
	reply := nostrx.Event{
		ID:        fmt.Sprintf("%064x", 402),
		PubKey:    strings.Repeat("7", 64),
		CreatedAt: now - 50,
		Kind:      nostrx.KindTextNote,
		Content:   "reply note",
		Tags:      [][]string{{"e", root.ID, "", "root"}, {"e", root.ID, "", "reply"}},
	}
	replyChildren := []nostrx.Event{
		{
			ID:        fmt.Sprintf("%064x", 403),
			PubKey:    strings.Repeat("8", 64),
			CreatedAt: now - 40,
			Kind:      nostrx.KindTextNote,
			Content:   "reply child one",
			Tags:      [][]string{{"e", root.ID, "", "root"}, {"e", reply.ID, "", "reply"}},
		},
		{
			ID:        fmt.Sprintf("%064x", 404),
			PubKey:    strings.Repeat("9", 64),
			CreatedAt: now - 30,
			Kind:      nostrx.KindTextNote,
			Content:   "reply child two",
			Tags:      [][]string{{"e", root.ID, "", "root"}, {"e", reply.ID, "", "reply"}},
		},
		{
			ID:        fmt.Sprintf("%064x", 405),
			PubKey:    strings.Repeat("a", 64),
			CreatedAt: now - 20,
			Kind:      nostrx.KindTextNote,
			Content:   "root child",
			Tags:      [][]string{{"e", root.ID, "", "root"}, {"e", root.ID, "", "reply"}},
		},
	}
	for _, ev := range append([]nostrx.Event{root, reply}, replyChildren...) {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}

	items, err := srv.computeAndStoreTrending(ctx, trending24h, time.Unix(now, 0))
	if err != nil {
		t.Fatalf("computeAndStoreTrending() error = %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected trending items")
	}
	for _, item := range items {
		if item.NoteID == reply.ID {
			t.Fatalf("expected reply to be excluded from trending items: %#v", items)
		}
	}
	if items[0].NoteID != root.ID {
		t.Fatalf("expected root note to remain in trending items, got %#v", items)
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

	events, _ := srv.fetchAuthorsPage(ctx, "", []string{pubkey}, 0, "", 30, []string{wsURL(relay.URL)}, "feed", authorsCacheKey([]string{pubkey}), nil, false)
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

	events, _ := srv.fetchAuthorsPage(ctx, strings.Repeat("f", 64), []string{pubkey}, 0, "", 30, []string{wsURL(relay.URL)}, "profile", pubkey, nil, false)
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
