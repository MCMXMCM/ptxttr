package httpx

import (
	"context"
	"sync"
	"time"

	"ptxt-nstr/internal/store"
)

const (
	hydrationTouchDebounceTTL       = 2 * time.Minute
	hydrationTouchCacheMaxLen       = 32768
	maxWarmFeedAuthors              = 16
	maxWarmFeedThreads              = 24
	maxWarmThreadProfileAuthors     = 24
	maxWarmUserContactAuthors       = 24
	recentProfileSeedScanLimit      = 64
	recentProfileSeedQuietWindow    = 2 * time.Minute
	noteRepliesHydrationRetryWindow   = 2 * time.Minute
	noteReactionsHydrationRetryWindow = 5 * time.Minute
)

type hydrationTouchEntry struct {
	expiresAt time.Time
	priority  int
}

type hydrationTouchCache struct {
	mu      sync.Mutex
	entries map[string]hydrationTouchEntry
	ttl     time.Duration
	maxLen  int
}

func newHydrationTouchCache(ttl time.Duration, maxLen int) *hydrationTouchCache {
	return &hydrationTouchCache{
		entries: make(map[string]hydrationTouchEntry),
		ttl:     ttl,
		maxLen:  maxLen,
	}
}

func (c *hydrationTouchCache) filter(targets []store.HydrationTarget, now time.Time) []store.HydrationTarget {
	if len(targets) == 0 {
		return nil
	}
	deduped := store.NormalizeHydrationTargets(targets)
	if c == nil {
		return deduped
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]store.HydrationTarget, 0, len(deduped))
	for _, target := range deduped {
		key := store.HydrationTargetKey(target)
		entry, ok := c.entries[key]
		if ok && now.Before(entry.expiresAt) && entry.priority >= target.Priority {
			continue
		}
		out = append(out, target)
		c.entries[key] = hydrationTouchEntry{
			expiresAt: now.Add(c.ttl),
			priority:  target.Priority,
		}
	}
	if len(c.entries) <= c.maxLen {
		return out
	}
	for key, entry := range c.entries {
		if !now.Before(entry.expiresAt) {
			delete(c.entries, key)
		}
	}
	// Best-effort size cap: map iteration order is undefined, so we may drop arbitrary valid entries.
	for key := range c.entries {
		if len(c.entries) <= c.maxLen {
			break
		}
		delete(c.entries, key)
	}
	return out
}

func (s *Server) touchHydrationTargets(ctx context.Context, targets []store.HydrationTarget) {
	if s == nil || s.store == nil || len(targets) == 0 {
		return
	}
	filtered := targets
	if s.hydrationTouches != nil {
		filtered = s.hydrationTouches.filter(targets, time.Now())
	}
	if len(filtered) == 0 {
		return
	}
	_ = s.store.TouchHydrationTargetsBatch(ctx, filtered)
}

func authorWarmTargets(pubkeys []string) []store.HydrationTarget {
	keys := uniqueNonEmptyStable(pubkeys)
	if len(keys) == 0 {
		return nil
	}
	targets := make([]store.HydrationTarget, 0, len(keys)*3)
	for _, pubkey := range keys {
		targets = append(targets,
			store.HydrationTarget{EntityType: "profile", EntityID: pubkey, Priority: 3},
			store.HydrationTarget{EntityType: "followGraph", EntityID: pubkey, Priority: 2},
			store.HydrationTarget{EntityType: "relayHints", EntityID: pubkey, Priority: 1},
		)
	}
	return targets
}

func noteReplyWarmTargets(eventIDs []string) []store.HydrationTarget {
	return noteEntityWarmTargets("noteReplies", 3, eventIDs)
}

func noteReactionWarmTargets(eventIDs []string) []store.HydrationTarget {
	return noteEntityWarmTargets("noteReactions", 1, eventIDs)
}

func noteEntityWarmTargets(entityType string, priority int, eventIDs []string) []store.HydrationTarget {
	ids := uniqueNonEmptyStable(eventIDs)
	if len(ids) == 0 {
		return nil
	}
	targets := make([]store.HydrationTarget, 0, len(ids))
	for _, eventID := range ids {
		targets = append(targets, store.HydrationTarget{
			EntityType: entityType,
			EntityID:   eventID,
			Priority:   priority,
		})
	}
	return targets
}
