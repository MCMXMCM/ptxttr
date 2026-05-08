package httpx

import (
	"fmt"
	"sync"
	"time"
)

// resolvedAuthorsTTL bounds how long resolveAuthors results stay cached.
// The 30-second poll interval that drives feedNewerCount is the dominant
// hot path. Following lists and WoT graph reach do not change quickly for
// the default guest cohorts, so keeping this warm for minutes avoids
// redundant WoT reachability + SQLite follow-list scans on repeated requests.
const (
	resolvedAuthorsTTL    = 5 * time.Minute
	resolvedAuthorsMaxLen = 256
)

type resolvedAuthorsEntry struct {
	authors   []string
	expiresAt time.Time
}

type resolvedAuthorsCache struct {
	mu      sync.Mutex
	entries map[string]resolvedAuthorsEntry
}

func newResolvedAuthorsCache() *resolvedAuthorsCache {
	return &resolvedAuthorsCache{entries: make(map[string]resolvedAuthorsEntry)}
}

func resolvedAuthorsCacheKey(viewer string, wot webOfTrustOptions) string {
	if !wot.Enabled {
		return viewer + "|0|0"
	}
	return fmt.Sprintf("%s|1|%d", viewer, wot.Depth)
}

func (c *resolvedAuthorsCache) get(key string, now time.Time) ([]string, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || now.After(entry.expiresAt) {
		if ok {
			delete(c.entries, key)
		}
		return nil, false
	}
	return entry.authors, true
}

func (c *resolvedAuthorsCache) put(key string, authors []string, now time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = resolvedAuthorsEntry{
		authors:   authors,
		expiresAt: now.Add(resolvedAuthorsTTL),
	}
	if len(c.entries) <= resolvedAuthorsMaxLen {
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
		if len(c.entries) <= resolvedAuthorsMaxLen {
			break
		}
		delete(c.entries, k)
	}
}

func (c *resolvedAuthorsCache) deletePrefix(prefix string) {
	if c == nil || prefix == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.entries {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			delete(c.entries, key)
		}
	}
}
