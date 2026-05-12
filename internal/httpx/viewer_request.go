package httpx

import (
	"net/http"
	"strings"
)

// Viewer identity and per-viewer view preferences travel as request headers
// rather than URL query parameters so that anonymous full-page HTML
// (`/thread/<id>`, `/u/<id>`, etc.) produces a single CloudFront cache entry
// shared across all viewers. The client reads each value from localStorage and
// injects the corresponding `X-Ptxt-*` header on every fetch / SPA hydration,
// so canonical viewer URLs no longer contain `?pubkey=`, `?relays=`, `?sort=`,
// `?tf=`, `?reads_tf=`, `?wot=`, or `?wot_depth=`.
//
// The legacy query-string fallback is kept on every reader so bookmarks,
// external links, and crawlers that captured the old URLs continue to resolve
// to the same response shape.
const (
	headerViewerPubkey    = "X-Ptxt-Viewer"    // must match HEADER_VIEWER_PUBKEY in web/static/js/session.js
	headerWotSeed         = "X-Ptxt-Wot-Seed"  // must match HEADER_WOT_SEED in web/static/js/session.js
	headerRelays          = "X-Ptxt-Relays"    // comma-separated relay URL list
	headerFeedSort        = "X-Ptxt-Sort"      // recent / trend24h / trend7d
	headerFeedTrendingTf  = "X-Ptxt-Tf"        // 24h / 1w (home/feed trending sidebar)
	headerReadsTrendingTf = "X-Ptxt-Reads-Tf"  // 24h / 1w (reads trending sidebar)
	headerWotEnabled      = "X-Ptxt-Wot"       // 1 / 0 / "" (unset)
	headerWotDepth        = "X-Ptxt-Wot-Depth" // 1..MaxDepth
)

// headerOrQuery returns the trimmed value from `header`, falling back to the
// trimmed `query` value if the header is empty or absent. Caller-side
// validation (decode, normalize, ParseBool, …) runs on the returned string.
func headerOrQuery(r *http.Request, header, query string) string {
	if r == nil {
		return ""
	}
	if v := strings.TrimSpace(r.Header.Get(header)); v != "" {
		return v
	}
	return strings.TrimSpace(r.URL.Query().Get(query))
}

// triStateFromRequest is the tri-state variant of headerOrQuery: it
// distinguishes "header/query supplied" (set=true) from "absent" (set=false)
// so callers can apply route-specific defaults only when the viewer did not
// state a preference. A non-empty header always wins; an empty header lets
// `r.URL.Query().Has(query)` decide presence (which matches the old query-
// only contract for `?wot=` / `?wot_depth=`).
func triStateFromRequest(r *http.Request, header, query string) (set bool, value string) {
	if r == nil {
		return false, ""
	}
	if raw := r.Header.Get(header); raw != "" {
		return true, raw
	}
	q := r.URL.Query()
	if q.Has(query) {
		return true, q.Get(query)
	}
	return false, ""
}

// viewerFromRequest returns the viewer pubkey identifier as supplied by the
// client. Header beats query string; callers run nostrx.DecodeIdentifier when
// they need a 64-char hex value.
func viewerFromRequest(r *http.Request) string {
	return headerOrQuery(r, headerViewerPubkey, "pubkey")
}

// seedPubkeyFromRequest returns the WoT seed pubkey identifier.
func seedPubkeyFromRequest(r *http.Request) string {
	return headerOrQuery(r, headerWotSeed, "seed_pubkey")
}

// relayParamsFromRequest returns the raw relay tokens from the request, in
// the shape the existing nostrx.ParseRelayParams helper expects (a flat slice
// of values, including comma-joined entries). When the X-Ptxt-Relays header
// is present its value is used verbatim; otherwise the `?relays=` and
// `?relay=` query values are concatenated for back-compat.
func relayParamsFromRequest(r *http.Request) []string {
	if r == nil {
		return nil
	}
	if v := strings.TrimSpace(r.Header.Get(headerRelays)); v != "" {
		return []string{v}
	}
	out := append([]string(nil), r.URL.Query()["relays"]...)
	return append(out, r.URL.Query()["relay"]...)
}

// feedSortFromRequest returns the requested sort token (recent/trend24h/
// trend7d). Caller normalizes; empty means "no preference, use the route
// default".
func feedSortFromRequest(r *http.Request) string {
	return headerOrQuery(r, headerFeedSort, "sort")
}

// feedTrendingTfFromRequest returns the trending window for the home/feed
// sidebar.
func feedTrendingTfFromRequest(r *http.Request) string {
	return headerOrQuery(r, headerFeedTrendingTf, "tf")
}

// readsTrendingTfFromRequest returns the trending window for the reads
// sidebar.
func readsTrendingTfFromRequest(r *http.Request) string {
	return headerOrQuery(r, headerReadsTrendingTf, "reads_tf")
}

// wotEnabledFromRequest returns whether the WoT toggle was supplied (set) and
// its raw string value if so.
func wotEnabledFromRequest(r *http.Request) (set bool, value string) {
	return triStateFromRequest(r, headerWotEnabled, "wot")
}

// wotDepthFromRequest returns whether wot_depth was supplied and its raw
// string value if so.
func wotDepthFromRequest(r *http.Request) (set bool, value string) {
	return triStateFromRequest(r, headerWotDepth, "wot_depth")
}
