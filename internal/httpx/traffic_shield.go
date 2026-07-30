package httpx

import (
	"net"
	"net/http"
	"strings"
	"time"
)

var expensiveAnonymousPrefixes = []string{
	"/feed",
	"/reads",
	"/bookmarks",
	"/notifications",
	"/trending",
	"/search",
	"/tag/",
	"/u/",
	"/thread/",
	"/og/",
	"/services/oembed",
	"/api/feed-notes",
	"/api/guest-feed-status",
	"/api/search-notes",
	"/api/tag-notes",
	"/api/profiles",
	"/api/thread-preview",
	"/api/thread-telemetry",
	"/api/reply-counts",
	"/api/reaction-stats",
	"/api/reactions",
	"/api/avatar-meta",
	"/api/events",
	"/api/relay-insight",
	"/api/share-preview",
}

var optionalAnonymousPrefixes = []string{
	"/api/guest-feed-status",
	"/api/avatar-meta",
	"/api/thread-telemetry",
	"/api/thread-preview",
	"/api/profiles",
	"/api/reply-counts",
	"/api/reaction-stats",
	"/api/reactions",
	"/api/share-preview",
}

var botUserAgentNeedles = []string{
	"bot",
	"crawler",
	"spider",
	"scraper",
	"curl/",
	"wget/",
	"python-requests",
	"httpclient",
	"headlesschrome",
	"facebookexternalhit",
	"meta-externalagent",
	"telegrambot",
	"twitterbot",
	"slackbot",
	"discordbot",
}

func (s *Server) trafficShield(next http.Handler) http.Handler {
	if s == nil || s.cfg.DesktopMode {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !shouldShieldRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		key := clientRateKey(r)
		if key == "" {
			key = "unknown"
		}
		if isBotLikeRequest(r) && !s.botLimiter.allow(time.Now(), "bot:"+key) {
			s.writeShielded(w, "bot")
			return
		}
		// The legacy `?pubkey=` parameter is also used as a resource identifier
		// by APIs such as avatar-meta. Only the explicit viewer header proves a
		// signed-in request at the abuse-protection boundary.
		viewer := strings.TrimSpace(r.Header.Get(headerViewerPubkey))
		if viewer == "" {
			limiter := s.anonymousLimiter
			scope := "anonymous"
			if optionalAnonymousRequest(r) && s.anonymousOptionalLimiter != nil {
				limiter = s.anonymousOptionalLimiter
				scope = "anonymous_optional"
			}
			if limiter != nil && !limiter.allow(time.Now(), "anon:"+key) {
				s.writeShielded(w, scope)
				return
			}
		}
		if viewer != "" {
			viewerKey := normalizeViewerKey(viewer)
			if viewerKey == "" {
				viewerKey = viewer
			}
			if !s.viewerLimiter.allow(time.Now(), "viewer:"+viewerKey) {
				s.writeShielded(w, "viewer")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func optionalAnonymousRequest(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	for _, prefix := range optionalAnonymousPrefixes {
		if r.URL.Path == prefix || strings.HasPrefix(r.URL.Path, prefix+"/") {
			return true
		}
	}
	return false
}

func shouldShieldRequest(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	path := r.URL.Path
	if path == "/healthz" || strings.HasPrefix(path, "/static/") || strings.HasPrefix(path, avatarPathPrefix) {
		return false
	}
	if path == "/" {
		return true
	}
	for _, prefix := range expensiveAnonymousPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func anonymousRequest(r *http.Request) bool {
	return strings.TrimSpace(viewerFromRequest(r)) == ""
}

func isBotLikeRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	if detectPreviewStyle(r) != styleNormal {
		return true
	}
	ua := strings.ToLower(strings.TrimSpace(r.Header.Get("User-Agent")))
	if ua == "" {
		return true
	}
	for _, needle := range botUserAgentNeedles {
		if strings.Contains(ua, needle) {
			return true
		}
	}
	return false
}

func clientRateKey(r *http.Request) string {
	if r == nil {
		return ""
	}
	// CloudFront's viewer-request function overwrites this header from
	// event.viewer.ip. Unlike conventional forwarding headers, a viewer cannot
	// rotate it to evade the per-client buckets.
	if ip := cleanClientIP(r.Header.Get("X-Ptxt-Client-IP")); ip != "" {
		return ip
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		// Proxies append the address they observed. Prefer the right-most valid
		// value so a viewer-supplied left-most entry cannot mint limiter keys.
		for i := len(parts) - 1; i >= 0; i-- {
			if ip := cleanClientIP(parts[i]); ip != "" {
				return ip
			}
		}
	}
	if host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr)); err == nil {
		return host
	}
	return cleanClientIP(r.RemoteAddr)
}

func cleanClientIP(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if ip := net.ParseIP(raw); ip != nil {
		return ip.String()
	}
	return ""
}

func (s *Server) writeShielded(w http.ResponseWriter, scope string) {
	if s != nil && s.metrics != nil {
		s.metrics.Add("traffic_shield."+scope, 1)
	}
	w.Header().Set("Cache-Control", "public, max-age=30, s-maxage=60")
	w.Header().Set("Retry-After", "10")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte("server is busy, please retry in a moment\n"))
}
