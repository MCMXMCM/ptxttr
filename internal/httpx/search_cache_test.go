package httpx

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

func TestSearchStoreCacheHitMissAndExpiry(t *testing.T) {
	cache := newSearchStoreCache()
	now := time.Unix(100, 0)
	key := "k"
	if _, ok := cache.get(key, now); ok {
		t.Fatal("cache hit before put")
	}
	cache.put(key, store.SearchNotesResult{NextID: "a"}, now)
	hit, ok := cache.get(key, now.Add(5*time.Second))
	if !ok {
		t.Fatal("expected hit")
	}
	if hit.NextID != "a" {
		t.Fatalf("NextID = %q, want a", hit.NextID)
	}
	if _, ok := cache.get(key, now.Add(searchStoreCacheTTL+time.Second)); ok {
		t.Fatal("expected expired entry to miss")
	}
}

func TestSearchPageCacheKeySeparation(t *testing.T) {
	req := searchRequest{
		Pubkey: strings.Repeat("a", 64),
		Query:  "nostr",
		Scope:  searchScopeAll,
		Cursor: 10,
		Limit:  30,
		WoT:    webOfTrustOptions{Enabled: true, Depth: 2},
		Relays: []string{"wss://a.example"},
	}
	srv, _ := testServer(t)
	query := store.PrepareSearch(req.Query)
	planAll := srv.newSearchPlan(context.Background(), req, query)
	req.Scope = searchScopeNetwork
	planNetwork := srv.newSearchPlan(context.Background(), req, query)
	if planAll.storeKey == planNetwork.storeKey {
		t.Fatal("store key should differ by scope/authors")
	}
	if planAll.pageKey == planNetwork.pageKey {
		t.Fatal("page key should differ by scope/authors")
	}
}

func TestSearchPageCacheCloneOnRead(t *testing.T) {
	cache := newSearchPageCache()
	now := time.Unix(200, 0)
	key := "page"
	cache.put(key, SearchPageData{
		Feed: []nostrx.Event{{ID: "1"}},
		ReplyCounts: map[string]int{
			"1": 3,
		},
	}, now)
	first, ok := cache.get(key, now)
	if !ok {
		t.Fatal("expected hit")
	}
	first.Feed[0].ID = "changed"
	first.ReplyCounts["1"] = 10
	second, ok := cache.get(key, now)
	if !ok {
		t.Fatal("expected second hit")
	}
	if second.Feed[0].ID != "1" {
		t.Fatalf("cached feed mutated: %q", second.Feed[0].ID)
	}
	if second.ReplyCounts["1"] != 3 {
		t.Fatalf("cached reply count mutated: %d", second.ReplyCounts["1"])
	}
}

func TestSearchStoreCacheEvictsWhenOverCap(t *testing.T) {
	cache := newSearchStoreCache()
	now := time.Unix(300, 0)
	for i := 0; i < searchStoreCacheMaxLen+25; i++ {
		cache.put(fmt.Sprintf("k-%d", i), store.SearchNotesResult{NextID: fmt.Sprintf("%d", i)}, now)
	}
	if len(cache.entries) > searchStoreCacheMaxLen {
		t.Fatalf("entries = %d, want <= %d", len(cache.entries), searchStoreCacheMaxLen)
	}
}
