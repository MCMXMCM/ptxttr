package httpx

import (
	"hash/fnv"
	"io"
	"net/http"
	"strings"
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

// newCoalesceMiddleware returns an HTTP middleware that serializes
// concurrent GETs for the same URL.path through one of N buckets. The lead
// arriver runs the handler; later arrivers wait up to cfg.Timeout, and on
// success 302-redirect to the same URL so a CDN in front of ptxt-nstr can
// serve a freshly-warm response. On timeout late arrivers receive 504.
//
// The redirect-on-contention pattern only helps with a shared cache in
// front; without one, the late arrivers will simply re-acquire the bucket
// on the next request. The middleware therefore defaults to disabled
// (PTXT_COALESCE_ENABLED=false) and is meant to be turned on once a CDN is
// confirmed.
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
	semaphores := make([]chan struct{}, buckets)
	for i := range semaphores {
		semaphores[i] = make(chan struct{}, 1)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !shouldCoalesce(r) {
				next.ServeHTTP(w, r)
				return
			}
			idx := bucketIndex(r.URL.Path, len(semaphores))
			sem := semaphores[idx]
			select {
			case sem <- struct{}{}:
				// Lead arriver: run the handler, release on done so late
				// arrivers waiting on this bucket can wake up.
				defer func() { <-sem }()
				next.ServeHTTP(w, r)
			default:
				// Already locked. Wait for either the lead arriver to release
				// (then redirect, hoping a CDN now serves the warm copy), our
				// own deadline (504), or a client cancel (no body).
				timer := time.NewTimer(timeout)
				defer timer.Stop()
				select {
				case sem <- struct{}{}:
					// Don't actually run the handler; immediately release and
					// redirect so the upstream cache can serve.
					<-sem
					redirect := r.URL.Path
					if r.URL.RawQuery != "" {
						redirect += "?" + r.URL.RawQuery
					}
					w.Header().Set("Cache-Control", "no-store")
					http.Redirect(w, r, redirect, http.StatusFound)
				case <-timer.C:
					w.Header().Set("Cache-Control", "no-store")
					w.Header().Set("Content-Type", "text/plain; charset=utf-8")
					w.WriteHeader(http.StatusGatewayTimeout)
					_, _ = w.Write([]byte("server under heavy load, please retry in a moment\n"))
				case <-r.Context().Done():
					return
				}
			}
		})
	}
}

// shouldCoalesce reports whether the request is eligible for coalescing.
// Only GETs, and only request paths that don't match a bypass prefix.
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
	// Bucketing uses path only, but HTML fragments (?fragment=…) return a
	// different body than the full document for the same path. Coalescing
	// them with the full page can starve hydrations or time out waiters.
	if strings.TrimSpace(r.URL.Query().Get("fragment")) != "" {
		return false
	}
	return true
}

// bucketIndex returns the FNV-1a bucket index for path. Stable across runs
// so that a CDN-fronted host coalesces consistently for the same URL.
func bucketIndex(path string, buckets int) int {
	if buckets <= 1 {
		return 0
	}
	h := fnv.New64a()
	_, _ = io.WriteString(h, path)
	return int(h.Sum64() % uint64(buckets))
}
