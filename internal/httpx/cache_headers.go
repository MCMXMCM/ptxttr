package httpx

import (
	"fmt"
	"net/http"
	"strings"
)

// HTTP cache headers for the rendered HTML and image surfaces.
//
// We split caching into three presets:
//
//   - Content-addressed (long): the URL plus the supplied etag uniquely identify
//     the rendered bytes. Used on /thread/<id> and on profile-only user
//     fragments where the response only changes when the underlying replaceable
//     event changes.
//   - Short: the response can stale-cache briefly (e.g. pagination fragments).
//   - Negative: a 404 / soft-miss; cache long enough to absorb crawler retry
//     storms without keeping the renderer hot for the same bad URL.
//
// Header values are aligned with njump's `render_event.go` so a CDN tuned for
// njump-style traffic behaves consistently in front of ptxt-nstr.
const (
	cacheControlContentAddressed = "public, max-age=300, s-maxage=86400, stale-while-revalidate=604800"
	cacheControlShortFmt         = "public, max-age=%d, s-maxage=%d"
	cacheControlNegative         = "public, max-age=300, s-maxage=1200"
)

// setContentAddressedCache marks a response as long-cacheable and attaches a
// quoted ETag derived from etag (typically a Nostr event id). Returns silently
// when etag is empty so callers can stay branch-free.
func setContentAddressedCache(w http.ResponseWriter, etag string) {
	if w == nil || etag == "" {
		return
	}
	w.Header().Set("Cache-Control", cacheControlContentAddressed)
	w.Header().Set("ETag", quotedETag(etag))
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
// (either bare or quoted). Callers that get true should write the same ETag,
// the long Cache-Control, and a 304 status with no body.
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

// writeNotModified writes a 304 with the long Cache-Control + ETag. Callers
// that found a matching If-None-Match should call this and return.
func writeNotModified(w http.ResponseWriter, etag string) {
	if w == nil || etag == "" {
		return
	}
	setContentAddressedCache(w, etag)
	w.WriteHeader(http.StatusNotModified)
}
