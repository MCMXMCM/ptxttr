package httpx

import (
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
	anonymousHTMLCacheTTL    = 30 * time.Second
	anonymousHTMLCacheMaxLen = 512
	relayTrendingCacheTTL    = 2 * time.Minute
	relayTrendingCacheMaxLen = 8
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

type searchStoreCall struct {
	wg    sync.WaitGroup
	val   store.SearchNotesResult
	panic any
}

type searchStoreSingleFlight struct {
	mu    sync.Mutex
	calls map[string]*searchStoreCall
}

func newTTLCache[T any](ttl time.Duration, maxLen int, clone func(T) T) *ttlCache[T] {
	return &ttlCache[T]{
		entries: make(map[string]ttlCacheEntry[T]),
		ttl:     ttl,
		maxLen:  maxLen,
		clone:   clone,
	}
}

func newTagStoreCache() *ttlCache[store.SearchNotesResult] {
	return newTTLCache(searchStoreCacheTTL, searchStoreCacheMaxLen, cloneSearchNotesResult)
}

func newSearchStoreCache() *ttlCache[store.SearchNotesResult] {
	return newTTLCache(searchStoreCacheTTL, searchStoreCacheMaxLen, cloneSearchNotesResult)
}

func newSearchStoreSingleFlight() *searchStoreSingleFlight {
	return &searchStoreSingleFlight{calls: make(map[string]*searchStoreCall)}
}

func (g *searchStoreSingleFlight) do(key string, fn func() store.SearchNotesResult) store.SearchNotesResult {
	if g == nil || key == "" {
		return fn()
	}
	g.mu.Lock()
	if call, ok := g.calls[key]; ok {
		g.mu.Unlock()
		call.wg.Wait()
		if call.panic != nil {
			panic(call.panic)
		}
		return cloneSearchNotesResult(call.val)
	}
	call := &searchStoreCall{}
	call.wg.Add(1)
	g.calls[key] = call
	g.mu.Unlock()

	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				call.panic = recovered
			}
			call.wg.Done()
			g.mu.Lock()
			delete(g.calls, key)
			g.mu.Unlock()
		}()
		call.val = fn()
	}()
	if call.panic != nil {
		panic(call.panic)
	}
	return cloneSearchNotesResult(call.val)
}

func newTagPageCache() *ttlCache[TagPageData] {
	return newTTLCache(searchPageCacheTTL, searchPageCacheMaxLen, cloneTagPageData)
}

func newGuestFeedPageCache() *ttlCache[FeedPageData] {
	return newTTLCache(guestFeedPageCacheTTL, guestFeedPageCacheMaxLen, cloneFeedPageData)
}

func newReadsTrendingCache() *ttlCache[[]TrendingNote] {
	return newTTLCache(relayTrendingCacheTTL, relayTrendingCacheMaxLen*16, cloneTrendingNotes)
}

type relayTrendingSnapshot struct {
	Events []nostrx.Event
}

func newRelayTrendingCache() *ttlCache[relayTrendingSnapshot] {
	return newTTLCache(relayTrendingCacheTTL, relayTrendingCacheMaxLen, cloneRelayTrendingSnapshot)
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

func cloneRelayTrendingSnapshot(in relayTrendingSnapshot) relayTrendingSnapshot {
	out := in
	if len(in.Events) > 0 {
		out.Events = append([]nostrx.Event(nil), in.Events...)
	}
	return out
}

func cloneTrendingNotes(in []TrendingNote) []TrendingNote {
	if len(in) == 0 {
		return nil
	}
	return append([]TrendingNote(nil), in...)
}
