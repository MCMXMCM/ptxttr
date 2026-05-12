package httpx

import (
	"context"
	"strings"
	"testing"
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
