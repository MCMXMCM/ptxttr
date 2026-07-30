package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
	staticfs "ptxt-nstr/web/static"

	fnostr "fiatjaf.com/nostr"
)

func TestDeferGuestLoggedOutFeedFirstPageIncludesTrendSorts(t *testing.T) {
	req := feedRequest{
		Pubkey:     "",
		SeedPubkey: defaultLoggedOutWOTSeedNPub,
		SortMode:   feedSortTrend7d,
		WoT:        webOfTrustOptions{Enabled: true, Depth: defaultLoggedOutWOTDepth},
	}
	if !deferGuestLoggedOutFeedFirstPage(req) {
		t.Fatal("expected defer for canonical guest trend7d first page")
	}
}

func TestInvalidateResolvedSeedAuthorsClearsDurableStore(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	seed := strings.Repeat("f", 64)
	key := resolvedAuthorsCacheKey(seed, webOfTrustOptions{Enabled: true, Depth: 2})
	if err := st.SetResolvedAuthorsDurable(ctx, key, []string{strings.Repeat("a", 64)}, 1); err != nil {
		t.Fatal(err)
	}
	srv.invalidateResolvedSeedAuthors(seed)
	_, _, ok, err := st.GetResolvedAuthorsDurable(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected durable resolved authors cleared for seed prefix")
	}
}

func TestHomeFeedShellPageDataGuestWOTSkipsResolvedAuthors(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	req := feedRequest{
		Pubkey:     "",
		SeedPubkey: defaultLoggedOutWOTSeedNPub,
		SortMode:   feedSortRecent,
		WoT:        webOfTrustOptions{Enabled: true, Depth: defaultLoggedOutWOTDepth},
	}

	data := srv.homeFeedShellPageData(ctx, req)
	if data.Trending == nil {
		t.Fatal("expected initialized trending slice")
	}

	counters := metricCountersSnapshot(srv)
	if counters["authors.cache_hit"] != 0 || counters["authors.cache_miss"] != 0 || counters["authors.durable_cache_hit"] != 0 {
		t.Fatalf("expected no author-resolution counters during guest shell render, got %#v", counters)
	}

	seed, err := nostrx.DecodeIdentifier(defaultLoggedOutWOTSeedNPub)
	if err != nil || seed == "" {
		t.Fatalf("decode default seed: %v", err)
	}
	key := resolvedAuthorsCacheKey(seed, req.WoT)
	_, _, ok, err := st.GetResolvedAuthorsDurable(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected guest shell render to avoid persisting resolved seed authors")
	}
}

func TestHomeRouteGuestWOTUsesDeferredShellData(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `data-feed-loader`) {
		t.Fatalf("expected deferred feed loader in home shell, got: %s", body)
	}
	counters := metricCountersSnapshot(srv)
	if counters["authors.cache_hit"] != 0 || counters["authors.cache_miss"] != 0 || counters["authors.durable_cache_hit"] != 0 {
		t.Fatalf("expected home route to avoid author resolution during guest shell render, got %#v", counters)
	}

	seed, err := nostrx.DecodeIdentifier(defaultLoggedOutWOTSeedNPub)
	if err != nil || seed == "" {
		t.Fatalf("decode default seed: %v", err)
	}
	key := resolvedAuthorsCacheKey(seed, webOfTrustOptions{Enabled: true, Depth: defaultLoggedOutWOTDepth})
	_, _, ok, err := st.GetResolvedAuthorsDurable(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected home route shell render to avoid persisting resolved seed authors")
	}
}

func TestHomeDocumentIncludesAppOpenGraphMetadata(t *testing.T) {
	srv, _ := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "origin.internal"
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "plaintextnostr.com")
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	want := []string{
		`<meta property="og:type" content="website">`,
		`<meta property="og:title" content="Plain Text Nostr">`,
		`<meta property="og:url" content="https://plaintextnostr.com/">`,
		`<meta property="og:image" content="https://plaintextnostr.com` + staticfs.VersionedPath("img/ascritch_og_light.png") + `">`,
		`<meta name="twitter:card" content="summary_large_image">`,
		`<meta name="twitter:image" content="https://plaintextnostr.com` + staticfs.VersionedPath("img/ascritch_og_light.png") + `">`,
	}
	for _, marker := range want {
		if !strings.Contains(body, marker) {
			t.Errorf("response missing %q", marker)
		}
	}
}

func TestHomeFeedShellPageDataUsesCanonicalRecentSnapshotWithoutResolvingAuthors(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	req := feedRequest{
		Pubkey:     "",
		SeedPubkey: defaultLoggedOutWOTSeedNPub,
		SortMode:   feedSortTrend24h,
		Relays:     srv.canonicalDefaultLoggedOutRelays(),
		WoT:        webOfTrustOptions{Enabled: true, Depth: defaultLoggedOutWOTDepth},
	}
	event := signedMutationEvent(t, nostrx.KindTextNote, "cached anonymous first page", nil)
	reply := signedMutationEvent(t, nostrx.KindTextNote, "cached anonymous reply", [][]string{{"e", event.ID, "", "root"}, {"e", event.ID, "", "reply"}})
	reaction := signNostrEvent(t, fnostr.Generate(), nostrx.KindReaction, "+", [][]string{{"e", event.ID}, {"p", event.PubKey}})
	for _, ev := range []nostrx.Event{event, reply, reaction} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	key := guestCanonicalFeedSnapshotKey(feedSortTrend24h, req.Relays)
	if err := st.SetFeedSnapshot(ctx, key, &store.FeedSnapshotRecord{
		Version:        feedSnapshotRecordVersion,
		RelaysHash:     hashStringSlice(req.Relays),
		Feed:           []nostrx.Event{event},
		Cursor:         event.CreatedAt,
		CursorID:       event.ID,
		HasMore:        true,
		ComputedAtUnix: time.Now().Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	data := srv.homeFeedShellPageData(ctx, req)
	if len(data.Feed) != 1 || data.Feed[0].ID != event.ID {
		t.Fatalf("expected recent snapshot feed in shell, got %d items", len(data.Feed))
	}
	if !data.HasMore || data.CursorID != event.ID {
		t.Fatalf("snapshot pagination not restored: has_more=%v cursor_id=%q", data.HasMore, data.CursorID)
	}
	if data.ReplyCounts[event.ID] != 1 || data.ReactionTotals[event.ID] != 1 {
		t.Fatalf("expected trend snapshot stats to hydrate, replies=%v reactions=%v", data.ReplyCounts, data.ReactionTotals)
	}

	counters := metricCountersSnapshot(srv)
	if counters["authors.cache_hit"] != 0 || counters["authors.cache_miss"] != 0 || counters["authors.durable_cache_hit"] != 0 {
		t.Fatalf("expected no author-resolution counters during snapshot shell render, got %#v", counters)
	}
}

func TestHandleWoTAuthorsServesDurableCache(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	seed, err := nostrx.DecodeIdentifier(defaultLoggedOutWOTSeedNPub)
	if err != nil {
		t.Fatal(err)
	}
	authors := []string{seed, strings.Repeat("d", 64)}
	key := resolvedAuthorsCacheKey(seed, webOfTrustOptions{Enabled: true, Depth: 2})
	computedAt := time.Now().Unix()
	if err := st.SetResolvedAuthorsDurable(ctx, key, authors, computedAt); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/wot-authors?seed="+defaultLoggedOutWOTSeedNPub+"&depth=2", nil)
	rr := httptest.NewRecorder()
	srv.handleWoTAuthors(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Authors        []string `json:"authors"`
		Cached         bool     `json:"cached"`
		ComputedAtUnix int64    `json:"computed_at"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Cached || payload.ComputedAtUnix != computedAt || len(payload.Authors) != len(authors) {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestHandleWoTAuthorsMissDoesNotResolveWithoutRefresh(t *testing.T) {
	srv, _ := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/wot-authors?seed="+defaultLoggedOutWOTSeedNPub+"&depth=2", nil)
	rr := httptest.NewRecorder()
	srv.handleWoTAuthors(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	counters := metricCountersSnapshot(srv)
	if counters["authors.cache_miss"] != 0 {
		t.Fatalf("expected cache-only miss to avoid resolving authors, got %#v", counters)
	}
}

func TestHandleWoTAuthorsRejectsCustomAnonymousSeed(t *testing.T) {
	srv, _ := testServer(t)
	seed := strings.Repeat("e", 64)
	req := httptest.NewRequest(http.MethodGet, "/api/wot-authors?seed="+seed+"&depth=2&refresh=1", nil)
	rr := httptest.NewRecorder()
	srv.handleWoTAuthors(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", rr.Code, rr.Body.String())
	}
	counters := metricCountersSnapshot(srv)
	if counters["authors.cache_miss"] != 0 {
		t.Fatalf("custom seed should not resolve authors, got %#v", counters)
	}
}

func TestAppShellInlinesCachedGuestFeedWhenSnapshotExists(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	req := feedRequest{
		Pubkey:     "",
		SeedPubkey: defaultLoggedOutWOTSeedNPub,
		SortMode:   feedSortTrend24h,
		Relays:     srv.canonicalDefaultLoggedOutRelays(),
		WoT:        webOfTrustOptions{Enabled: true, Depth: defaultLoggedOutWOTDepth},
	}
	event := signedMutationEvent(t, nostrx.KindTextNote, "cached anonymous first page", nil)
	reply := signedMutationEvent(t, nostrx.KindTextNote, "cached anonymous reply", [][]string{{"e", event.ID, "", "root"}, {"e", event.ID, "", "reply"}})
	reaction := signNostrEvent(t, fnostr.Generate(), nostrx.KindReaction, "+", [][]string{{"e", event.ID}, {"p", event.PubKey}})
	for _, ev := range []nostrx.Event{event, reply, reaction} {
		if err := st.SaveEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	key := guestCanonicalFeedSnapshotKey(feedSortTrend24h, req.Relays)
	if err := st.SetFeedSnapshot(ctx, key, &store.FeedSnapshotRecord{
		Version:        feedSnapshotRecordVersion,
		RelaysHash:     hashStringSlice(req.Relays),
		Feed:           []nostrx.Event{event},
		Cursor:         event.CreatedAt,
		CursorID:       event.ID,
		HasMore:        true,
		ComputedAtUnix: time.Now().Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	httpReq := httptest.NewRequest(http.MethodGet, "/?sort=trend24h", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httpReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="note-`+event.ID+`"`) {
		t.Fatalf("expected cached note inlined in app shell, got: %s", truncateForLog(body, 1200))
	}
	if strings.Contains(body, `data-feed-loader`) {
		t.Fatalf("did not expect loader when snapshot notes are present: %s", truncateForLog(body, 800))
	}
	if !strings.Contains(body, `data-ascii-reply-count="1"`) || !strings.Contains(body, `data-ascii-reaction-total="1"`) {
		t.Fatalf("expected cached trend note stats to be hydrated, got: %s", truncateForLog(body, 1200))
	}
}

func TestHandleFeedNotesAPIServesSnapshot(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	relays := srv.canonicalDefaultLoggedOutRelays()
	event := signedMutationEvent(t, nostrx.KindTextNote, "feed api note", nil)
	if err := st.SaveEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	key := guestCanonicalFeedSnapshotKey(feedSortRecent, relays)
	if err := st.SetFeedSnapshot(ctx, key, &store.FeedSnapshotRecord{
		Version:        feedSnapshotRecordVersion,
		RelaysHash:     hashStringSlice(relays),
		Feed:           []nostrx.Event{event},
		Cursor:         event.CreatedAt,
		CursorID:       event.ID,
		HasMore:        false,
		ComputedAtUnix: time.Now().Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/feed-notes?limit=30", nil)
	rec := httptest.NewRecorder()
	srv.handleFeedNotesAPI(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Notes []nostrx.Event `json:"notes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Notes) != 1 || payload.Notes[0].ID != event.ID {
		t.Fatalf("unexpected notes payload: %#v", payload.Notes)
	}
}

func TestHandleThreadPreviewCapsGraphBundle(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	root := signedMutationEvent(t, nostrx.KindTextNote, "preview root", nil)
	if err := st.SaveEvent(ctx, root); err != nil {
		t.Fatal(err)
	}
	var selected nostrx.Event
	for i := 0; i < 80; i++ {
		reply := signedMutationEvent(t, nostrx.KindTextNote, "preview reply", [][]string{
			{"e", root.ID, "", "root"},
			{"e", root.ID, "", "reply"},
		})
		if i == 0 {
			selected = reply
		}
		if err := st.SaveEvent(ctx, reply); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.BuildThreadGraphCache(ctx, root.ID, 500); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/thread-preview?id="+selected.ID, nil)
	rr := httptest.NewRecorder()
	srv.handleThreadPreviewAPI(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Events []nostrx.Event `json:"events"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Events) > 50 {
		t.Fatalf("thread preview returned %d events, want <= 50", len(payload.Events))
	}
	if cc := rr.Header().Get("Cache-Control"); cc == "" {
		t.Fatal("expected cache header on bounded thread preview")
	}
}

func metricCountersSnapshot(srv *Server) map[string]int64 {
	if srv == nil || srv.metrics == nil {
		return map[string]int64{}
	}
	snapshot := srv.metrics.Snapshot()
	raw, _ := snapshot["counters"].(map[string]int64)
	if raw != nil {
		return raw
	}
	return map[string]int64{}
}
