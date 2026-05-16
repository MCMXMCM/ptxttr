package httpx

import (
	"context"
	"fmt"
	"hash/fnv"
	"maps"
	"strconv"
	"sync"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

const (
	searchStoreCacheTTL      = 30 * time.Second
	searchStoreCacheMaxLen   = 512
	searchPageCacheTTL       = 15 * time.Second
	searchPageCacheMaxLen    = 256
	guestFeedPageCacheTTL    = 5 * time.Minute
	guestFeedPageCacheMaxLen = 256
)

var searchKindsKey = hashIntSlice(noteTimelineKinds)

type ttlCacheEntry[T any] struct {
	value     T
	expiresAt time.Time
}

type ttlCache[T any] struct {
	mu      sync.Mutex
	entries map[string]ttlCacheEntry[T]
	ttl     time.Duration
	maxLen  int
	clone   func(T) T
}

func newTTLCache[T any](ttl time.Duration, maxLen int, clone func(T) T) *ttlCache[T] {
	return &ttlCache[T]{
		entries: make(map[string]ttlCacheEntry[T]),
		ttl:     ttl,
		maxLen:  maxLen,
		clone:   clone,
	}
}

func newSearchStoreCache() *ttlCache[store.SearchNotesResult] {
	return newTTLCache(searchStoreCacheTTL, searchStoreCacheMaxLen, cloneSearchNotesResult)
}

func newSearchPageCache() *ttlCache[SearchPageData] {
	return newTTLCache(searchPageCacheTTL, searchPageCacheMaxLen, cloneSearchPageData)
}

func newTagStoreCache() *ttlCache[store.SearchNotesResult] {
	return newTTLCache(searchStoreCacheTTL, searchStoreCacheMaxLen, cloneSearchNotesResult)
}

func newTagPageCache() *ttlCache[TagPageData] {
	return newTTLCache(searchPageCacheTTL, searchPageCacheMaxLen, cloneTagPageData)
}

func newGuestFeedPageCache() *ttlCache[FeedPageData] {
	return newTTLCache(guestFeedPageCacheTTL, guestFeedPageCacheMaxLen, cloneFeedPageData)
}

func (c *ttlCache[T]) get(key string, now time.Time) (T, bool) {
	var zero T
	if c == nil || key == "" {
		return zero, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || now.After(entry.expiresAt) {
		if ok {
			delete(c.entries, key)
		}
		return zero, false
	}
	if c.clone != nil {
		return c.clone(entry.value), true
	}
	return entry.value, true
}

func (c *ttlCache[T]) put(key string, value T, now time.Time) {
	if c == nil || key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = ttlCacheEntry[T]{
		value:     value,
		expiresAt: now.Add(c.ttl),
	}
	if len(c.entries) <= c.maxLen {
		return
	}
	for k, v := range c.entries {
		if now.After(v.expiresAt) {
			delete(c.entries, k)
		}
	}
	// Hard cap: if expiry sweep didn't free enough, drop arbitrary entries.
	// Map iteration order is randomized so this approximates random eviction
	// without the bookkeeping cost of a true LRU.
	for k := range c.entries {
		if len(c.entries) <= c.maxLen {
			break
		}
		delete(c.entries, k)
	}
}

// reset clears all entries (used by tests to simulate a cold in-memory guest cache).
func (c *ttlCache[T]) reset() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]ttlCacheEntry[T])
}

type searchPlan struct {
	req           searchRequest
	resolved      requestAuthors
	query         store.PreparedSearch
	scope         string
	scopedAuthors []string
	storeKey      string
	pageKey       string
}

func (s *Server) newSearchPlan(ctx context.Context, req searchRequest, query store.PreparedSearch) searchPlan {
	resolved := s.resolveRequestAuthors(ctx, req.Pubkey, req.SeedPubkey, req.Relays, req.WoT)
	scope := normalizeSearchScope(req.Scope, resolved.loggedOut && !resolved.seedWOTEnabled, resolved.wotEnabled)
	var scopedAuthors []string
	if scope == searchScopeNetwork {
		scopedAuthors = resolved.authors
	}
	viewer := resolved.viewerForMuteFilter()
	storeKey := fmt.Sprintf("%s|%s|%s|%s|%s|%d|%s|%d",
		viewer,
		scope,
		searchKindsKey,
		authorsCacheKey(scopedAuthors),
		query.Normalized,
		req.Cursor,
		req.CursorID,
		req.Limit,
	)
	pageKey := storeKey + "|" + strconv.FormatBool(req.WoT.Enabled) + "|" + strconv.Itoa(req.WoT.Depth) + "|" + hashStringSlice(req.Relays)
	return searchPlan{
		req:           req,
		resolved:      resolved,
		query:         query,
		scope:         scope,
		scopedAuthors: scopedAuthors,
		storeKey:      storeKey,
		pageKey:       pageKey,
	}
}

func hashStringSlice(values []string) string {
	if len(values) == 0 {
		return "0"
	}
	h := fnv.New64a()
	for _, value := range values {
		_, _ = h.Write([]byte(value))
		_, _ = h.Write([]byte{0})
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

func hashIntSlice(values []int) string {
	if len(values) == 0 {
		return "0"
	}
	h := fnv.New64a()
	var buf [20]byte
	for _, value := range values {
		n := copy(buf[:], strconv.Itoa(value))
		_, _ = h.Write(buf[:n])
		_, _ = h.Write([]byte{0})
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

func cloneSearchNotesResult(in store.SearchNotesResult) store.SearchNotesResult {
	out := in
	if len(in.Events) > 0 {
		out.Events = append([]nostrx.Event(nil), in.Events...)
	}
	return out
}

func cloneSearchPageData(in SearchPageData) SearchPageData {
	out := in
	if len(in.Feed) > 0 {
		out.Feed = append([]nostrx.Event(nil), in.Feed...)
	}
	if in.ReferencedEvents != nil {
		out.ReferencedEvents = maps.Clone(in.ReferencedEvents)
	}
	if in.ReplyCounts != nil {
		out.ReplyCounts = maps.Clone(in.ReplyCounts)
	}
	if in.ReactionTotals != nil {
		out.ReactionTotals = maps.Clone(in.ReactionTotals)
	}
	if in.ReactionViewers != nil {
		out.ReactionViewers = maps.Clone(in.ReactionViewers)
	}
	if in.Profiles != nil {
		out.Profiles = maps.Clone(in.Profiles)
	}
	return out
}

func cloneTagPageData(in TagPageData) TagPageData {
	out := in
	if len(in.Feed) > 0 {
		out.Feed = append([]nostrx.Event(nil), in.Feed...)
	}
	if in.ReferencedEvents != nil {
		out.ReferencedEvents = maps.Clone(in.ReferencedEvents)
	}
	if in.ReplyCounts != nil {
		out.ReplyCounts = maps.Clone(in.ReplyCounts)
	}
	if in.ReactionTotals != nil {
		out.ReactionTotals = maps.Clone(in.ReactionTotals)
	}
	if in.ReactionViewers != nil {
		out.ReactionViewers = maps.Clone(in.ReactionViewers)
	}
	if in.Profiles != nil {
		out.Profiles = maps.Clone(in.Profiles)
	}
	return out
}

func cloneFeedPageData(in FeedPageData) FeedPageData {
	out := in
	if len(in.Feed) > 0 {
		out.Feed = append([]nostrx.Event(nil), in.Feed...)
	}
	if in.ReferencedEvents != nil {
		out.ReferencedEvents = maps.Clone(in.ReferencedEvents)
	}
	if in.ReplyCounts != nil {
		out.ReplyCounts = maps.Clone(in.ReplyCounts)
	}
	if in.ReactionTotals != nil {
		out.ReactionTotals = maps.Clone(in.ReactionTotals)
	}
	if in.ReactionViewers != nil {
		out.ReactionViewers = maps.Clone(in.ReactionViewers)
	}
	if in.Profiles != nil {
		out.Profiles = maps.Clone(in.Profiles)
	}
	if len(in.Trending) > 0 {
		out.Trending = append([]TrendingNote(nil), in.Trending...)
	}
	if len(in.Relays) > 0 {
		out.Relays = append([]string(nil), in.Relays...)
	}
	return out
}
