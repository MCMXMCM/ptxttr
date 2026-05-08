package store

import (
	"sync"
	"sync/atomic"

	lru "github.com/hashicorp/golang-lru/v2"
)

// sidecarMetricSink records cache hit/miss events (e.g. httpx appMetrics.Add).
type sidecarMetricSink func(name string, delta int64)

// sidecarCaches holds bounded in-memory read-through caches for hot projection
// paths. SQLite remains authoritative; entries are
// invalidated on explicit checklist paths (purge* / invalidate*).
type sidecarCaches struct {
	mu sync.Mutex

	relay   *lru.Cache[string, RelayHintSet]
	profile *lru.Cache[string, ProfileSummary]
	reply   *lru.Cache[string, ReplyStat]

	sink sidecarMetricSink

	relayHits     atomic.Uint64
	relayMisses   atomic.Uint64
	profileHits   atomic.Uint64
	profileMisses atomic.Uint64
	replyHits     atomic.Uint64
	replyMisses   atomic.Uint64
}

func newSidecarCaches(size int, sink sidecarMetricSink) *sidecarCaches {
	if size < 8 {
		size = 8
	}
	relay, _ := lru.New[string, RelayHintSet](size)
	profile, _ := lru.New[string, ProfileSummary](size)
	reply, _ := lru.New[string, ReplyStat](size)
	return &sidecarCaches{
		relay:   relay,
		profile: profile,
		reply:   reply,
		sink:    sink,
	}
}

func (c *sidecarCaches) SetSink(sink sidecarMetricSink) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sink = sink
}

func (c *sidecarCaches) emit(name string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	fn := c.sink
	c.mu.Unlock()
	if fn != nil {
		fn(name, 1)
	}
}

func (c *sidecarCaches) getRelay(pubkey string) (RelayHintSet, bool) {
	if c == nil {
		return RelayHintSet{}, false
	}
	c.mu.Lock()
	v, ok := c.relay.Get(pubkey)
	c.mu.Unlock()
	if ok {
		c.relayHits.Add(1)
		c.emit("sidecar.relay_hint.hit")
		return v, true
	}
	c.relayMisses.Add(1)
	c.emit("sidecar.relay_hint.miss")
	return RelayHintSet{}, false
}

func (c *sidecarCaches) getRelayMulti(keys []string) (map[string]RelayHintSet, []string) {
	out := make(map[string]RelayHintSet, len(keys))
	var missing []string
	if c == nil {
		for _, k := range keys {
			if k != "" {
				missing = append(missing, k)
			}
		}
		return out, missing
	}
	for _, k := range keys {
		if k == "" {
			continue
		}
		c.mu.Lock()
		v, ok := c.relay.Get(k)
		c.mu.Unlock()
		if ok {
			c.relayHits.Add(1)
			c.emit("sidecar.relay_hint.hit")
			out[k] = v
		} else {
			c.relayMisses.Add(1)
			c.emit("sidecar.relay_hint.miss")
			missing = append(missing, k)
		}
	}
	return out, missing
}

func (c *sidecarCaches) putRelayMulti(m map[string]RelayHintSet) {
	if c == nil || len(m) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range m {
		if k == "" {
			continue
		}
		_ = c.relay.Add(k, v)
	}
}

func (c *sidecarCaches) getProfile(keys []string) (map[string]ProfileSummary, []string) {
	out := make(map[string]ProfileSummary, len(keys))
	var missing []string
	if c == nil {
		for _, k := range keys {
			if k != "" {
				missing = append(missing, k)
			}
		}
		return out, missing
	}
	for _, k := range keys {
		if k == "" {
			continue
		}
		c.mu.Lock()
		v, ok := c.profile.Get(k)
		c.mu.Unlock()
		if ok {
			c.profileHits.Add(1)
			c.emit("sidecar.profile.hit")
			out[k] = v
		} else {
			c.profileMisses.Add(1)
			c.emit("sidecar.profile.miss")
			missing = append(missing, k)
		}
	}
	return out, missing
}

func (c *sidecarCaches) putProfileMulti(m map[string]ProfileSummary) {
	if c == nil || len(m) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range m {
		if k == "" {
			continue
		}
		_ = c.profile.Add(k, v)
	}
}

func (c *sidecarCaches) getReply(keys []string) (map[string]ReplyStat, []string) {
	out := make(map[string]ReplyStat, len(keys))
	var missing []string
	if c == nil {
		for _, k := range keys {
			if k != "" {
				missing = append(missing, k)
			}
		}
		return out, missing
	}
	for _, k := range keys {
		if k == "" {
			continue
		}
		c.mu.Lock()
		v, ok := c.reply.Get(k)
		c.mu.Unlock()
		if ok {
			c.replyHits.Add(1)
			c.emit("sidecar.reply_stat.hit")
			out[k] = v
		} else {
			c.replyMisses.Add(1)
			c.emit("sidecar.reply_stat.miss")
			missing = append(missing, k)
		}
	}
	return out, missing
}

func (c *sidecarCaches) putReplyMulti(m map[string]ReplyStat) {
	if c == nil || len(m) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range m {
		if k == "" {
			continue
		}
		_ = c.reply.Add(k, v)
	}
}

func (c *sidecarCaches) purgeRelay() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.relay.Purge()
}

func (c *sidecarCaches) purgeProfile() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.profile.Purge()
}

func (c *sidecarCaches) purgeReply() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reply.Purge()
}

func (c *sidecarCaches) purgeAll() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.relay.Purge()
	c.profile.Purge()
	c.reply.Purge()
}

func (c *sidecarCaches) invalidateRelayKeys(pubkeys []string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range pubkeys {
		if k == "" {
			continue
		}
		c.relay.Remove(k)
	}
}

func (c *sidecarCaches) invalidateProfileKeys(pubkeys []string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range pubkeys {
		if k == "" {
			continue
		}
		c.profile.Remove(k)
	}
}

func (c *sidecarCaches) invalidateReplyKeys(noteIDs []string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range noteIDs {
		if k == "" {
			continue
		}
		c.reply.Remove(k)
	}
}

// SidecarLRUStats returns hit/miss counters for /debug observability.
func (s *Store) SidecarLRUStats() map[string]int64 {
	if s == nil || s.sidecar == nil {
		return map[string]int64{}
	}
	sc := s.sidecar
	return map[string]int64{
		"sidecar.relay_hint.hit":  int64(sc.relayHits.Load()),
		"sidecar.relay_hint.miss": int64(sc.relayMisses.Load()),
		"sidecar.profile.hit":     int64(sc.profileHits.Load()),
		"sidecar.profile.miss":    int64(sc.profileMisses.Load()),
		"sidecar.reply_stat.hit":  int64(sc.replyHits.Load()),
		"sidecar.reply_stat.miss": int64(sc.replyMisses.Load()),
	}
}
