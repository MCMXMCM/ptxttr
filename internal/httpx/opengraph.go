package httpx

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"ptxt-nstr/internal/nostrx"
)

const (
	ogSiteName        = "Plain Text Nostr"
	ogDefaultImage    = "/static/img/ascritch.png"
	ogDescriptionMax  = 240
	ogTitleMax        = 110
	ogTypeArticle     = "article"
	ogTypeProfile     = "profile"
	ogTypeWebsite     = "website"
	defaultPageDescr = "A small Nostr web app: server-rendered HTML, vanilla JS, and a SQLite event cache."
	defaultPageTitle = "Plain Text Nostr"
)

// requestSchemeHost returns the proxy-aware scheme and host (no path).
func requestSchemeHost(r *http.Request) (scheme, host string) {
	if r == nil {
		return "", ""
	}
	scheme = "https"
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))) {
	case "http":
		scheme = "http"
	case "https":
		scheme = "https"
	default:
		if r.TLS == nil {
			scheme = "http"
		}
	}
	host = strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	return scheme, host
}

// canonicalRequestURL returns an absolute URL for the current request,
// honouring proxy headers (X-Forwarded-Proto, X-Forwarded-Host) so the value
// is correct when ptxt-nstr runs behind a CDN. Path comes from r.URL only;
// query is intentionally omitted because canonical URLs in OpenGraph
// shouldn't include pagination/fragment parameters.
func canonicalRequestURL(r *http.Request) string {
	if r == nil {
		return ""
	}
	scheme, host := requestSchemeHost(r)
	if host == "" {
		return ""
	}
	return scheme + "://" + host + r.URL.Path
}

// absoluteURL turns an internal path (e.g. "/u/abc") into a fully-qualified
// URL using the same proxy-aware host detection as canonicalRequestURL. If
// raw is already absolute (http:// or https://) it's returned as-is.
func absoluteURL(r *http.Request, raw string) string {
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	scheme, host := requestSchemeHost(r)
	if host == "" {
		return raw
	}
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	return scheme + "://" + host + raw
}

// requestOrigin returns scheme://host for the current request (no path),
// using the same proxy headers as canonicalRequestURL.
func requestOrigin(r *http.Request) string {
	scheme, host := requestSchemeHost(r)
	if host == "" {
		return ""
	}
	return scheme + "://" + host
}

// firstNonEmpty returns the first non-empty string from the inputs.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// shortenForOG flattens content to a single line and truncates on a word
// boundary to fit within max runes. Empty input returns empty string.
func shortenForOG(content string, max int) string {
	if max <= 0 {
		max = ogDescriptionMax
	}
	content = strings.ReplaceAll(content, "\r\n", " ")
	content = strings.ReplaceAll(content, "\n", " ")
	content = strings.ReplaceAll(content, "\r", " ")
	content = strings.ReplaceAll(content, "\t", " ")
	for strings.Contains(content, "  ") {
		content = strings.ReplaceAll(content, "  ", " ")
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if utf8.RuneCountInString(content) <= max {
		return content
	}
	cut := 0
	count := 0
	for i := range content {
		if count >= max {
			cut = i
			break
		}
		count++
	}
	if cut == 0 {
		cut = len(content)
	}
	truncated := content[:cut]
	if idx := strings.LastIndexAny(truncated, " \t"); idx > max/2 {
		truncated = truncated[:idx]
	}
	return strings.TrimSpace(truncated) + "…"
}

// ogImageForProfile returns an absolute image URL for a profile, falling back
// to the site default when no avatar is available. Routes the picture through
// the avatar proxy so the URL is content-fingerprinted and cacheable.
func ogImageForProfile(r *http.Request, pubkey string, profile nostrx.Profile) string {
	picture := strings.TrimSpace(profile.Picture)
	if pubkey != "" && picture != "" {
		if proxied := avatarSrcFor(pubkey, picture); proxied != "" {
			return absoluteURL(r, proxied)
		}
	}
	return absoluteURL(r, ogDefaultImage)
}

// homeOG returns the generic site card for / and similar non-content routes.
func homeOG(r *http.Request) OpenGraphMeta {
	return OpenGraphMeta{
		Type:        ogTypeWebsite,
		Title:       defaultPageTitle,
		Description: defaultPageDescr,
		URL:         canonicalRequestURL(r),
		Image:       absoluteURL(r, ogDefaultImage),
		SiteName:    ogSiteName,
	}
}
