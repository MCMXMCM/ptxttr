package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ptxt-nstr/internal/nostrx"
)

func TestAnonymousUserPageRequiresCachedGigiScope(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	pubkey := strings.Repeat("a", 64)
	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        testEventID("profile", pubkey),
		PubKey:    pubkey,
		Kind:      nostrx.KindProfileMetadata,
		CreatedAt: 1700000000,
		Content:   `{"name":"outside"}`,
		Sig:       strings.Repeat("1", 128),
	}); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/u/"+pubkey, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("anonymous status outside Gigi scope = %d, want 404", rec.Code)
	}

	allowAnonymousAuthors(t, st, pubkey)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/u/"+pubkey, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("anonymous status inside cached Gigi scope = %d, want 200", rec.Code)
	}
}

func TestDesktopUserPageBypassesHostedGuestSliceAdmission(t *testing.T) {
	srv, _ := testServer(t)
	srv.cfg.DesktopMode = true
	pubkey := strings.Repeat("d", 64)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/u/"+pubkey, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("desktop uncached profile status = %d, want 200: %s", rec.Code, truncateForLog(rec.Body.String(), 800))
	}
	if strings.Contains(rec.Body.String(), "cached guest slice") {
		t.Fatalf("desktop profile rendered hosted guest-slice rejection: %s", truncateForLog(rec.Body.String(), 800))
	}
	if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("desktop profile Cache-Control = %q, want private, no-store", got)
	}
}

func TestAnonymousUserPageAllowsCachedPinnedProfile(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	pubkey := defaultLoggedOutPinnedProfilePubkey

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/u/"+pubkey, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("anonymous pinned profile without cache = %d, want 404", rec.Code)
	}

	if err := st.SaveEvent(ctx, nostrx.Event{
		ID:        testEventID("pinned-profile", pubkey),
		PubKey:    pubkey,
		Kind:      nostrx.KindProfileMetadata,
		CreatedAt: 1700000000,
		Content:   `{"name":"Pinned Profile"}`,
		Sig:       strings.Repeat("1", 128),
	}); err != nil {
		t.Fatal(err)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/u/"+pubkey, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("anonymous cached pinned profile = %d, want 200", rec.Code)
	}
}

func TestAnonymousUserPageAllowsCachedTimelineAuthorOutsideWoT(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	pubkey := strings.Repeat("c", 64)
	note := nostrx.Event{
		ID:        testEventID("outside-timeline", pubkey),
		PubKey:    pubkey,
		Kind:      nostrx.KindTextNote,
		CreatedAt: 1700000000,
		Content:   "cached thread participant profile post",
		Sig:       strings.Repeat("1", 128),
	}
	if err := st.SaveEvent(ctx, note); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/u/"+pubkey, nil)
	req.Header.Set(headerRelays, "wss://attacker-selected.example")
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("anonymous cached timeline profile = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), note.Content) {
		t.Fatalf("anonymous cached timeline profile missing cached note: %s", truncateForLog(rec.Body.String(), 1200))
	}
	if strings.Contains(rec.Body.String(), `data-retro-loader-type="profile-posts"`) {
		t.Fatalf("anonymous cached timeline profile retained a pending posts loader")
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "s-maxage=") {
		t.Fatalf("anonymous profile Cache-Control = %q, want shared cache policy", got)
	}
}

func TestDefaultLoggedOutAuthorResolutionIncludesPinnedProfile(t *testing.T) {
	srv, _ := testServer(t)
	authors, _, loggedOut := srv.resolveAuthorsAll(context.Background(), defaultLoggedOutWOTSeedNPub, nil, webOfTrustOptions{Enabled: true, Depth: defaultLoggedOutWOTDepth})
	if loggedOut {
		t.Fatal("default seed resolved as logged out")
	}
	if !newAuthorMembership(authors).Contains(defaultLoggedOutPinnedProfilePubkey) {
		t.Fatalf("resolved default logged-out authors missing pinned profile: %v", authors)
	}
}

func TestSeedCrawlerTargetsIncludePinnedProfile(t *testing.T) {
	targets := appendPinnedSeedContactTargets(nil)
	if len(targets) != 1 {
		t.Fatalf("targets len = %d, want 1", len(targets))
	}
	if targets[0].EntityID != defaultLoggedOutPinnedProfilePubkey {
		t.Fatalf("target = %+v, want pinned profile", targets[0])
	}
	targets = appendPinnedSeedContactTargets(targets)
	if len(targets) != 1 {
		t.Fatalf("duplicate pinned target appended, len = %d", len(targets))
	}
}

func TestAnonymousThreadRendersCachedRootAndFiltersReplies(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	root := nostrx.Event{ID: strings.Repeat("a", 64), PubKey: strings.Repeat("1", 64), Kind: nostrx.KindTextNote, CreatedAt: 100, Content: "cached Gigi root", Sig: "sig"}
	reply := nostrx.Event{ID: strings.Repeat("b", 64), PubKey: strings.Repeat("2", 64), Kind: nostrx.KindTextNote, CreatedAt: 101, Content: "outside Gigi reply", Tags: [][]string{
		{"e", root.ID, "", "root"},
		{"p", root.PubKey},
	}, Sig: "sig"}
	for _, event := range []nostrx.Event{root, reply} {
		if err := st.SaveEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/thread/"+root.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("anonymous cached thread status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "cached Gigi root") {
		t.Fatalf("expected cached root in anonymous body: %s", truncateForLog(body, 800))
	}
	filteredStart := strings.Index(body, "data-thread-filtered-replies")
	if filteredStart < 0 || !strings.Contains(body[filteredStart:], "outside Gigi reply") {
		t.Fatalf("anonymous body did not place out-of-scope reply behind show-more: %s", truncateForLog(body, 1600))
	}
	if strings.Contains(body[:filteredStart], "outside Gigi reply") {
		t.Fatalf("anonymous body rendered out-of-scope reply before show-more: %s", truncateForLog(body, 1600))
	}
	if !strings.Contains(body, "show 1 more") {
		t.Fatalf("anonymous body missing show-more control: %s", truncateForLog(body, 1600))
	}
}

func TestGuestFeedAndThreadUseSeparateWoTDepths(t *testing.T) {
	srv, st := testServer(t)
	ctx := context.Background()
	seed, err := nostrx.DecodeIdentifier(defaultLoggedOutWOTSeedNPub)
	if err != nil || seed == "" {
		t.Fatalf("decode default seed: %v", err)
	}
	hop1 := strings.Repeat("1", 64)
	hop2 := strings.Repeat("2", 64)
	hop3 := strings.Repeat("3", 64)
	hop4 := strings.Repeat("4", 64)
	saveTestFollowList(t, st, seed, []string{hop1}, 100)
	saveTestFollowList(t, st, hop1, []string{hop2}, 101)
	saveTestFollowList(t, st, hop2, []string{hop3}, 102)
	saveTestFollowList(t, st, hop3, []string{hop4}, 103)
	if err := srv.refreshDefaultLoggedOutAuthorMemberships(ctx); err != nil {
		t.Fatal(err)
	}

	feedMembership := srv.defaultLoggedOutAuthorMembership(ctx)
	if !feedMembership.Contains(hop1) || feedMembership.Contains(hop2) {
		t.Fatalf("1-hop feed membership = %#v, want hop1 only", feedMembership.Authors())
	}
	threadMembership := srv.defaultLoggedOutThreadAuthorMembership(ctx)
	for _, pubkey := range []string{hop1, hop2, hop3} {
		if !threadMembership.Contains(pubkey) {
			t.Fatalf("3-hop thread membership missing %s: %#v", pubkey, threadMembership.Authors())
		}
	}
	if threadMembership.Contains(hop4) {
		t.Fatalf("3-hop thread membership included fourth hop: %#v", threadMembership.Authors())
	}
}
