package httpx

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestGuestSliceViewIsReplacedByNewGeneration(t *testing.T) {
	srv, st := testServer(t)
	srv.cfg.GuestSliceV2Enabled = true
	ctx := context.Background()
	first := strings.Repeat("a", 64)
	second := strings.Repeat("b", 64)
	publishTestGuestSlice(t, st, first, "", 7)

	view := srv.currentGuestSliceView(ctx)
	if view == nil || view.state.Generation != 7 {
		t.Fatalf("view = %#v, want generation 7", view)
	}
	if again := srv.currentGuestSliceView(ctx); again != view {
		t.Fatal("an unchanged generation rebuilt the cached view")
	}
	if thread := srv.defaultLoggedOutThreadAuthorMembership(ctx); !thread.Contains(first) || thread.Contains(second) {
		t.Fatalf("generation 7 thread scope = %v", thread.Authors())
	}

	publishTestGuestSlice(t, st, second, "", 8)
	next := srv.currentGuestSliceView(ctx)
	if next == nil || next == view || next.state.Generation != 8 {
		t.Fatalf("view after publish = %#v, want a new generation 8 view", next)
	}
	for name, membership := range map[string]authorMembership{
		"feed":   srv.defaultLoggedOutAuthorMembership(ctx),
		"thread": srv.defaultLoggedOutThreadAuthorMembership(ctx),
	} {
		if !membership.Contains(second) || membership.Contains(first) {
			t.Fatalf("generation 8 %s scope = %v", name, membership.Authors())
		}
	}
	if got := srv.currentGuestGeneration(ctx); got != 8 {
		t.Fatalf("current guest generation = %d, want 8", got)
	}
	if cohort, _, ok := srv.resolveAuthorsForSeed(ctx, defaultLoggedOutWOTSeedNPub, nil, webOfTrustOptions{Enabled: true, Depth: defaultLoggedOutWOTDepth}); !ok || len(cohort) != 1 || cohort[0] != second {
		t.Fatalf("seed cohort = %v ok=%v, want generation 8 cohort", cohort, ok)
	}
}

func TestGuestSliceViewPicksUpDebugAuthors(t *testing.T) {
	srv, st := testServer(t)
	srv.cfg.GuestSliceV2Enabled = true
	srv.cfg.Debug = true
	ctx := context.Background()
	publishTestGuestSlice(t, st, strings.Repeat("a", 64), "", 7)
	fixture := strings.Repeat("c", 64)
	if srv.defaultLoggedOutThreadAuthorMembership(ctx).Contains(fixture) {
		t.Fatal("fixture author was in the guest scope before seeding")
	}
	srv.addDebugAnonymousAuthor(fixture)
	if !srv.defaultLoggedOutThreadAuthorMembership(ctx).Contains(fixture) {
		t.Fatal("cached guest scope missed a newly seeded debug author")
	}
}

func TestGuestSliceViewKeepsThreadScopeForUnchangedTrust(t *testing.T) {
	srv, st := testServer(t)
	srv.cfg.GuestSliceV2Enabled = true
	ctx := context.Background()
	author := strings.Repeat("a", 64)
	publishTestGuestSlice(t, st, author, "", 7)
	first := srv.currentGuestSliceView(ctx)
	publishTestGuestSlice(t, st, author, "", 8)
	second := srv.currentGuestSliceView(ctx)
	if second == nil || second == first || second.state.Generation != 8 {
		t.Fatalf("view after publish = %#v, want a new generation 8 view", second)
	}
	if second.thread.filter != first.thread.filter {
		t.Fatal("an unchanged trust list rebuilt the thread scope")
	}
	publishTestGuestSlice(t, st, strings.Repeat("b", 64), "", 9)
	if third := srv.currentGuestSliceView(ctx); third == nil || third.thread.filter == second.thread.filter {
		t.Fatal("a changed trust list kept the previous thread scope")
	}
}

func TestFinishGuestSlicePublishFlushesOriginCachesOnlyForNewTrust(t *testing.T) {
	srv, _ := testServer(t)
	ctx := context.Background()
	seed := strings.Repeat("a", 64)
	fill := func() {
		srv.anonymousHTMLCache.put("/thread/x|ascii_w=52", anonymousHTMLDocument{Body: []byte("cached")}, time.Now())
		srv.guestFeedCache.put("guest_feed|test", FeedPageData{}, time.Now())
	}
	cached := func() (bool, bool) {
		_, html := srv.anonymousHTMLCache.get("/thread/x|ascii_w=52", time.Now())
		_, feed := srv.guestFeedCache.get("guest_feed|test", time.Now())
		return html, feed
	}

	fill()
	srv.finishGuestSlicePublish(ctx, seed, false)
	if html, feed := cached(); !html || !feed {
		t.Fatalf("unchanged trust flushed origin caches: html=%v feed=%v", html, feed)
	}
	srv.finishGuestSlicePublish(ctx, seed, true)
	if html, feed := cached(); html || feed {
		t.Fatalf("new trust kept origin caches: html=%v feed=%v", html, feed)
	}
}
