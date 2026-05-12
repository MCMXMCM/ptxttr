package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Each `*FromRequest` helper goes through the same `header beats trimmed
// query` precedence (via `headerOrQuery`), so we drive every string-returning
// helper from one table. New helpers should add a row here rather than a
// dedicated `Test*` function.
func TestStringHeadersFromRequest(t *testing.T) {
	cases := []struct {
		name        string
		fn          func(*http.Request) string
		header      string // header name to set (or "" to skip)
		rawURL      string
		headerValue string
		want        string
	}{
		{"viewer prefers header", viewerFromRequest, headerViewerPubkey, "/thread/abc?pubkey=fromquery", "fromheader", "fromheader"},
		{"viewer falls back to query", viewerFromRequest, "", "/thread/abc?pubkey=fromquery", "", "fromquery"},
		{"viewer whitespace header falls through", viewerFromRequest, headerViewerPubkey, "/thread/abc?pubkey=fromquery", "   ", "fromquery"},
		{"viewer no sources is empty", viewerFromRequest, "", "/thread/abc", "", ""},

		{"seed prefers header", seedPubkeyFromRequest, headerWotSeed, "/feed?seed_pubkey=fromquery", "fromheader", "fromheader"},
		{"seed falls back to query", seedPubkeyFromRequest, "", "/feed?seed_pubkey=fromquery", "", "fromquery"},

		{"feed sort prefers header", feedSortFromRequest, headerFeedSort, "/feed?sort=fromquery", "fromheader", "fromheader"},
		{"feed sort falls back to query", feedSortFromRequest, "", "/feed?sort=trend7d", "", "trend7d"},

		{"feed tf prefers header", feedTrendingTfFromRequest, headerFeedTrendingTf, "/?tf=24h", "1w", "1w"},
		{"feed tf falls back to query", feedTrendingTfFromRequest, "", "/?tf=24h", "", "24h"},

		{"reads tf prefers header", readsTrendingTfFromRequest, headerReadsTrendingTf, "/reads?reads_tf=24h", "1w", "1w"},
		{"reads tf falls back to query", readsTrendingTfFromRequest, "", "/reads?reads_tf=24h", "", "24h"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.rawURL, nil)
			if tc.header != "" {
				req.Header.Set(tc.header, tc.headerValue)
			}
			if got := tc.fn(req); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// wotEnabledFromRequest and wotDepthFromRequest share a tri-state contract:
// header beats query, but absence is observable as `set=false` so callers can
// apply route-specific defaults (logged-out users default WoT on for `/`,
// `/reads` only turns WoT on when explicitly requested).
func TestTriStateHeadersFromRequest(t *testing.T) {
	cases := []struct {
		name        string
		fn          func(*http.Request) (bool, string)
		header      string
		rawURL      string
		headerValue string
		wantSet     bool
		wantValue   string
	}{
		{"wot prefers header", wotEnabledFromRequest, headerWotEnabled, "/?wot=0", "1", true, "1"},
		{"wot falls back to query", wotEnabledFromRequest, "", "/?wot=0", "", true, "0"},
		{"wot unset returns false", wotEnabledFromRequest, "", "/", "", false, ""},

		{"wot_depth prefers header", wotDepthFromRequest, headerWotDepth, "/?wot_depth=1", "3", true, "3"},
		{"wot_depth falls back to query", wotDepthFromRequest, "", "/?wot_depth=2", "", true, "2"},
		{"wot_depth unset returns false", wotDepthFromRequest, "", "/", "", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.rawURL, nil)
			if tc.header != "" {
				req.Header.Set(tc.header, tc.headerValue)
			}
			set, value := tc.fn(req)
			if set != tc.wantSet || value != tc.wantValue {
				t.Fatalf("got (%t, %q), want (%t, %q)", set, value, tc.wantSet, tc.wantValue)
			}
		})
	}
}

// relayParamsFromRequest takes its own test because its return shape
// (multi-value slice; comma-joined header passthrough; concat of `?relays=`
// and `?relay=` for the query fallback) differs from the simple string
// helpers above.
func TestRelayParamsFromRequest(t *testing.T) {
	t.Run("prefers header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/feed?relays=wss://from.query/&relay=wss://other.query/", nil)
		req.Header.Set(headerRelays, "wss://from.header/")
		got := relayParamsFromRequest(req)
		if len(got) != 1 || got[0] != "wss://from.header/" {
			t.Fatalf("got %v, want [wss://from.header/]", got)
		}
	})
	t.Run("falls back to query (relays then relay)", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/feed?relays=wss://a/&relay=wss://b/", nil)
		got := relayParamsFromRequest(req)
		if len(got) != 2 || got[0] != "wss://a/" || got[1] != "wss://b/" {
			t.Fatalf("got %v, want [wss://a/ wss://b/]", got)
		}
	})
}
