package httpx

import (
	"hash/fnv"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"ptxt-nstr/internal/config"
)

// coalesceConfig configures the per-URL coalescing middleware. When Enabled
// is false the middleware is a no-op pass-through. Buckets and Timeout fall
// back to defaults when zero or negative.
type coalesceConfig struct {
	Enabled bool
	Buckets int
	Timeout time.Duration
}

const defaultCoalesceTimeout = 4 * time.Second

// Path prefixes that bypass coalescing entirely. These are either truly
// concurrent-safe (static assets, debug) or already deduplicated upstream
// (API endpoints) so adding the bucket lock would only hurt latency.
// Requests with a non-empty ?fragment= also bypass (see shouldCoalesce).
var coalesceBypassPrefixes = []string{
	"/static/",
	avatarPathPrefix,
	"/api/",
	"/debug/",
}

// coalesceShard tracks in-flight requests for the keys hashed to it. Each
// entry's channel closes when that request's handler returns.
type coalesceShard struct {
	mu       sync.Mutex
	inFlight map[string]chan struct{}
}

// newCoalesceMiddleware returns an HTTP middleware that holds back identical
// concurrent anonymous GETs while the first one renders. Requests are
// identical when they share the anonymous document cache key (path, query and
// ASCII width). Later arrivers wait up to cfg.Timeout and then run the handler
// themselves, which normally serves the leader's freshly cached document;
// on timeout they receive 504. Waiters never redirect, since a redirect costs
// the browser another round trip and a CDN miss for anything uncacheable.
func newCoalesceMiddleware(cfg coalesceConfig) func(http.Handler) http.Handler {
	if !cfg.Enabled {
		return func(next http.Handler) http.Handler { return next }
	}
	buckets := cfg.Buckets
	if buckets <= 0 {
		buckets = config.DefaultCoalesceBuckets
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultCoalesceTimeout
	}
	shards := make([]*coalesceShard, buckets)
	for i := range shards {
		shards[i] = &coalesceShard{inFlight: make(map[string]chan struct{})}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !shouldCoalesce(r) {
				next.ServeHTTP(w, r)
				return
			}
			key := anonymousHTMLCacheKey(r)
			shard := shards[bucketIndex(key, len(shards))]
			shard.mu.Lock()
			leaderDone, busy := shard.inFlight[key]
			if !busy {
				done := make(chan struct{})
				shard.inFlight[key] = done
				shard.mu.Unlock()
				defer func() {
					shard.mu.Lock()
					delete(shard.inFlight, key)
					shard.mu.Unlock()
					close(done)
				}()
				next.ServeHTTP(w, r)
				return
			}
			shard.mu.Unlock()
			timer := time.NewTimer(timeout)
			defer timer.Stop()
			select {
			case <-leaderDone:
				next.ServeHTTP(w, r)
			case <-timer.C:
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusGatewayTimeout)
				_, _ = w.Write([]byte("server under heavy load, please retry in a moment\n"))
			case <-r.Context().Done():
			}
		})
	}
}

// shouldCoalesce reports whether the request is eligible for coalescing:
// anonymous GETs and HEADs whose path doesn't match a bypass prefix.
// Signed-in responses are personalized, so they never wait on each other.
func shouldCoalesce(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	for _, prefix := range coalesceBypassPrefixes {
		if strings.HasPrefix(r.URL.Path, prefix) {
			return false
		}
	}
	// HTML fragments are hydration requests with no shared origin cache to
	// reuse, so waiting on an identical one would only add latency.
	if strings.TrimSpace(r.URL.Query().Get("fragment")) != "" {
		return false
	}
	return anonymousRequestFromHTTP(r)
}

// bucketIndex returns the FNV-1a shard index for a coalescing key.
func bucketIndex(key string, buckets int) int {
	if buckets <= 1 {
		return 0
	}
	h := fnv.New64a()
	_, _ = io.WriteString(h, key)
	return int(h.Sum64() % uint64(buckets))
}
