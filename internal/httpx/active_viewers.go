package httpx

import (
	"sort"
	"sync"
	"time"
)

// activeViewersMaxLen caps the LRU so a burst of one-off requests cannot
// retain unbounded memory. Mirrors resolvedAuthorsMaxLen for symmetry: the
// background trending warmer should not have a meaningfully larger working
// set than the author resolution cache it depends on.
const activeViewersMaxLen = 256

// activeViewerEntry pairs a viewer pubkey with the exact webOfTrustOptions
// they were browsing with on their last request. We keep both because the
// resolvedAuthors cache key is (viewer + WoT), so the warmer needs the same
// pair to look up the cohort the viewer would actually request.
type activeViewerEntry struct {
	Viewer string
	WoT    webOfTrustOptions
}

// activeViewers tracks the last time each (viewer, WoT) pair was seen on a
// request that resolved a WoT author cohort. The background trending hot
// loop reads this snapshot to decide whose per-cohort trending_cache rows
// to keep warm; eviction is approximate (oldest-first when over cap) since
// the warmer is opportunistic, not authoritative.
type activeViewers struct {
	mu      sync.Mutex
	entries map[string]activeViewerRecord
}

type activeViewerRecord struct {
	entry    activeViewerEntry
	lastSeen time.Time
}

func newActiveViewers() *activeViewers {
	return &activeViewers{entries: make(map[string]activeViewerRecord)}
}

// Touch records that the given viewer was just seen browsing under the
// supplied WoT options. Empty pubkeys are ignored so anonymous traffic does
// not pollute the LRU. Re-touching with different WoT options keeps both
// pairs (so a viewer who switches between depths warms both cohorts).
func (v *activeViewers) Touch(viewer string, wot webOfTrustOptions, now time.Time) {
	if v == nil || viewer == "" {
		return
	}
	key := resolvedAuthorsCacheKey(viewer, wot)
	v.mu.Lock()
	defer v.mu.Unlock()
	v.entries[key] = activeViewerRecord{
		entry:    activeViewerEntry{Viewer: viewer, WoT: wot},
		lastSeen: now,
	}
	if len(v.entries) <= activeViewersMaxLen {
		return
	}
	// Over the cap: drop the oldest entries until we are back under.
	type aged struct {
		key string
		ts  time.Time
	}
	all := make([]aged, 0, len(v.entries))
	for k, rec := range v.entries {
		all = append(all, aged{key: k, ts: rec.lastSeen})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ts.Before(all[j].ts) })
	drop := len(all) - activeViewersMaxLen
	for i := 0; i < drop; i++ {
		delete(v.entries, all[i].key)
	}
}

// Snapshot returns the entries touched within window, sorted newest-first.
// Stale entries (older than window) are evicted as a side effect so the map
// does not grow unbounded between calls.
func (v *activeViewers) Snapshot(now time.Time, window time.Duration) []activeViewerEntry {
	if v == nil {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	cutoff := now.Add(-window)
	type aged struct {
		entry activeViewerEntry
		ts    time.Time
	}
	live := make([]aged, 0, len(v.entries))
	for k, rec := range v.entries {
		if rec.lastSeen.Before(cutoff) {
			delete(v.entries, k)
			continue
		}
		live = append(live, aged{entry: rec.entry, ts: rec.lastSeen})
	}
	sort.Slice(live, func(i, j int) bool { return live[i].ts.After(live[j].ts) })
	out := make([]activeViewerEntry, len(live))
	for i, a := range live {
		out[i] = a.entry
	}
	return out
}

// Len returns the current number of tracked entries (including stale ones
// that have not yet been swept by a Snapshot call). Used by tests and the
// /debug/metrics gauge.
func (v *activeViewers) Len() int {
	if v == nil {
		return 0
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.entries)
}
