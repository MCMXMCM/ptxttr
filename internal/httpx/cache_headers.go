package httpx

import (
	"fmt"
	"net/http"
	"strings"
)

// HTTP cache headers for HTML, images, JSON, and similar response surfaces.
//
// Presets:
//
//   - Content-addressed (HTML): the URL plus the supplied etag uniquely
//     identify the rendered bytes for the moment, but new replies on a thread
//     or new posts on a profile invalidate it. s-maxage is kept short (5min)
//     so anonymous edge cache staleness after a publish is bounded.
//   - Content-addressed (long, e.g. /og/<id>): the response is fully derived
//     from an immutable event, so a 24h s-maxage is safe.
//   - Immutable (e.g. /api/event/<id>): signed Nostr events are
//     content-addressed forever; max-age and s-maxage are set to one year.
//   - Short: the response can stale-cache briefly (e.g. pagination fragments).
//   - Negative: a 404 / soft-miss; cache long enough to absorb crawler retry
//     storms without keeping the renderer hot for the same bad URL.
//
// Header values are aligned with njump's `render_event.go` so a CDN tuned for
// njump-style traffic behaves consistently in front of ptxt-nstr.
const (
	cacheControlContentAddressed     = "public, max-age=300, s-maxage=300, stale-while-revalidate=604800"
	cacheControlContentAddressedLong = "public, max-age=300, s-maxage=86400, stale-while-revalidate=604800"
	cacheControlImmutable            = "public, max-age=31536000, s-maxage=31536000, immutable"
	cacheControlShortFmt             = "public, max-age=%d, s-maxage=%d"
	cacheControlNegative             = "public, max-age=300, s-maxage=1200"
)

// setContentAddressedCache marks a response as cacheable (short s-maxage to
// bound staleness on new-reply/new-post invalidations) and attaches a quoted
// ETag derived from etag (typically a Nostr event id). Returns silently when
// etag is empty so callers can stay branch-free.
func setContentAddressedCache(w http.ResponseWriter, etag string) {
	if w == nil || etag == "" {
		return
	}
	w.Header().Set("Cache-Control", cacheControlContentAddressed)
	w.Header().Set("ETag", quotedETag(etag))
}

// setContentAddressedCacheLong is the same as setContentAddressedCache but
// with a 24-hour shared cache TTL. Use only when the response is fully
// derived from an immutable event (e.g. /og/<id> rendering).
func setContentAddressedCacheLong(w http.ResponseWriter, etag string) {
	if w == nil || etag == "" {
		return
	}
	w.Header().Set("Cache-Control", cacheControlContentAddressedLong)
	w.Header().Set("ETag", quotedETag(etag))
}

// setImmutableCache marks a response as immutable (1y max-age) for true
// content-addressed payloads like raw Nostr events fetched by id.
func setImmutableCache(w http.ResponseWriter, etag string) {
	if w == nil {
		return
	}
	w.Header().Set("Cache-Control", cacheControlImmutable)
	if etag != "" {
		w.Header().Set("ETag", quotedETag(etag))
	}
}

// setShortCache marks a response as cacheable for `seconds` in the browser and
// 5x that at the shared cache. seconds <= 0 falls back to 60s.
func setShortCache(w http.ResponseWriter, seconds int) {
	if w == nil {
		return
	}
	if seconds <= 0 {
		seconds = 60
	}
	w.Header().Set("Cache-Control", fmt.Sprintf(cacheControlShortFmt, seconds, seconds*5))
}

// setNegativeCache attaches a short cache window suited for 404 / soft-miss
// responses. It is defensive: if the caller has already set Cache-Control we
// leave their value alone (e.g. an upstream timeout that already opted into
// no-store).
func setNegativeCache(w http.ResponseWriter) {
	if w == nil {
		return
	}
	if w.Header().Get("Cache-Control") != "" {
		return
	}
	w.Header().Set("Cache-Control", cacheControlNegative)
}

// matchesETag returns true when If-None-Match on the request matches etag
// (either bare or quoted). Callers that get true should respond with 304 and
// the same cache shape as the corresponding 200: use writeNotModified,
// writeNotModifiedLong, or writeNotModifiedImmutable as appropriate.
func matchesETag(r *http.Request, etag string) bool {
	if r == nil || etag == "" {
		return false
	}
	header := r.Header.Get("If-None-Match")
	if header == "" {
		return false
	}
	quoted := quotedETag(etag)
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if candidate == quoted || candidate == etag || candidate == "*" {
			return true
		}
	}
	return false
}

// writeNotModified writes a 304 with the short Cache-Control + ETag. Callers
// that found a matching If-None-Match should call this and return.
func writeNotModified(w http.ResponseWriter, etag string) {
	if w == nil || etag == "" {
		return
	}
	setContentAddressedCache(w, etag)
	w.WriteHeader(http.StatusNotModified)
}

// writeNotModifiedLong writes a 304 with the long Cache-Control + ETag. Use
// when the underlying response is fully derived from an immutable event.
func writeNotModifiedLong(w http.ResponseWriter, etag string) {
	if w == nil || etag == "" {
		return
	}
	setContentAddressedCacheLong(w, etag)
	w.WriteHeader(http.StatusNotModified)
}

// writeNotModifiedImmutable writes a 304 with immutable Cache-Control + ETag.
// Use for content-addressed payloads that never change (e.g. raw events by id).
func writeNotModifiedImmutable(w http.ResponseWriter, etag string) {
	if w == nil || etag == "" {
		return
	}
	setImmutableCache(w, etag)
	w.WriteHeader(http.StatusNotModified)
}
