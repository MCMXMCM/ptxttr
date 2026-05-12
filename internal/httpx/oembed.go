package httpx

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ptxt-nstr/internal/nostrx"
)

// oEmbedResponse implements the oEmbed 1.0 spec
// (https://oembed.com/) for ptxt-nstr's thread / event pages. The Type field
// is always "rich" because the embed wraps user-generated text content; we
// don't generate "photo" or "video" responses for now.
type oEmbedResponse struct {
	XMLName         xml.Name `json:"-" xml:"oembed"`
	Type            string   `json:"type" xml:"type"`
	Version         string   `json:"version" xml:"version"`
	Title           string   `json:"title,omitempty" xml:"title,omitempty"`
	AuthorName      string   `json:"author_name,omitempty" xml:"author_name,omitempty"`
	AuthorURL       string   `json:"author_url,omitempty" xml:"author_url,omitempty"`
	ProviderName    string   `json:"provider_name" xml:"provider_name"`
	ProviderURL     string   `json:"provider_url" xml:"provider_url"`
	CacheAge        int      `json:"cache_age,omitempty" xml:"cache_age,omitempty"`
	ThumbnailURL    string   `json:"thumbnail_url,omitempty" xml:"thumbnail_url,omitempty"`
	ThumbnailWidth  int      `json:"thumbnail_width,omitempty" xml:"thumbnail_width,omitempty"`
	ThumbnailHeight int      `json:"thumbnail_height,omitempty" xml:"thumbnail_height,omitempty"`
	HTML            string   `json:"html,omitempty" xml:"html,omitempty"`
	Width           int      `json:"width,omitempty" xml:"width,omitempty"`
	Height          int      `json:"height,omitempty" xml:"height,omitempty"`
}

const (
	oEmbedDefaultWidth = 480
	oEmbedDefaultHeight = 200
	oEmbedCacheSeconds = 86400
	oEmbedFormatJSON   = "json"
	oEmbedFormatXML    = "xml"
	oEmbedRequestBudget = 3 * time.Second
)

// handleOEmbed serves /services/oembed?url=<rendered-url>[&format=json|xml].
// Per oEmbed convention we accept the consumer-supplied URL, parse the path
// to identify the event, and return a JSON or XML payload describing how
// to embed it. The handler is cache-only (no relay fan-out) so it can be
// hit by any consumer without becoming a relay-DDoS vector.
func (s *Server) handleOEmbed(w http.ResponseWriter, r *http.Request) {
	defer s.observe("handler.oembed", time.Now())
	target := strings.TrimSpace(r.URL.Query().Get("url"))
	if target == "" {
		setNegativeCache(w)
		http.Error(w, "missing url parameter", http.StatusBadRequest)
		return
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Path == "" {
		setNegativeCache(w)
		http.Error(w, "invalid url parameter", http.StatusBadRequest)
		return
	}
	id := oEmbedEventIDFromPath(parsed.Path)
	if id == "" {
		setNegativeCache(w)
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), oEmbedRequestBudget)
	defer cancel()
	event := s.eventFromStore(ctx, id)
	if event == nil {
		setNegativeCache(w)
		http.NotFound(w, r)
		return
	}
	profile := s.profile(ctx, event.PubKey)
	resp := buildOEmbedResponse(r, *event, profile)
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = oEmbedFormatJSON
	}
	setShortCache(w, oEmbedCacheSeconds)
	switch format {
	case oEmbedFormatXML:
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		_, _ = w.Write([]byte(xml.Header))
		_ = xml.NewEncoder(w).Encode(resp)
	default:
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// oEmbedEventIDFromPath extracts the canonical hex event id from a thread or
// shortlink URL path. Supports /thread/<id|nevent|note> and /e/<...>. Returns
// "" when the path doesn't reference an event we can serve.
func oEmbedEventIDFromPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	for _, prefix := range []string{"/thread/", "/e/"} {
		if strings.HasPrefix(path, prefix) {
			return resolveOGEventID(strings.TrimPrefix(path, prefix))
		}
	}
	if strings.HasPrefix(path, "/") {
		return resolveOGEventID(strings.TrimPrefix(path, "/"))
	}
	return ""
}

// buildOEmbedResponse assembles the oEmbed payload for a resolved event.
// The HTML is intentionally simple and self-contained (no external CSS or
// JS) so that consumer sandboxes display it predictably.
func buildOEmbedResponse(r *http.Request, event nostrx.Event, profile nostrx.Profile) oEmbedResponse {
	authorName := authorLabel(map[string]nostrx.Profile{event.PubKey: profile}, event.PubKey)
	threadURL := absoluteURL(r, "/thread/"+event.ID)
	authorURL := absoluteURL(r, "/u/"+event.PubKey)
	embedHTML := buildOEmbedHTML(event, authorName, threadURL, authorURL)
	return oEmbedResponse{
		Type:         "rich",
		Version:      "1.0",
		Title:        firstNonEmpty(shortenForOG(event.Content, ogTitleMax), authorName+" on "+ogSiteName),
		AuthorName:   authorName,
		AuthorURL:    authorURL,
		ProviderName: ogSiteName,
		ProviderURL:  requestOrigin(r),
		CacheAge:     oEmbedCacheSeconds,
		ThumbnailURL: absoluteURL(r, "/og/"+event.ID+".png"),
		HTML:         embedHTML,
		Width:        oEmbedDefaultWidth,
		Height:       oEmbedDefaultHeight,
	}
}

// buildOEmbedHTML returns a small, self-contained HTML snippet suitable for
// dropping into a third-party page. We escape user input (content, names)
// and link to the canonical thread / author URLs.
func buildOEmbedHTML(event nostrx.Event, authorName, threadURL, authorURL string) string {
	excerpt := shortenForOG(event.Content, ogDescriptionMax*2)
	var b strings.Builder
	b.WriteString(`<blockquote class="ptxt-nstr-embed" style="border-left:3px solid #e32a6d;padding:0.5em 0.75em;margin:0;font-family:ui-monospace,monospace;background:#fafafa;color:#222;">`)
	b.WriteString(`<p style="margin:0 0 0.5em 0;">`)
	b.WriteString(html.EscapeString(excerpt))
	b.WriteString(`</p>`)
	b.WriteString(`<footer style="font-size:0.85em;color:#555;">— <a href="`)
	b.WriteString(html.EscapeString(authorURL))
	b.WriteString(`" rel="noopener">`)
	b.WriteString(html.EscapeString(authorName))
	b.WriteString(`</a> · <a href="`)
	b.WriteString(html.EscapeString(threadURL))
	b.WriteString(`" rel="noopener">view on `)
	b.WriteString(ogSiteName)
	b.WriteString(`</a></footer></blockquote>`)
	return b.String()
}

// emitOEmbedDiscoveryHeaders adds the two Link rel=alternate headers oEmbed
// consumers look for (JSON + XML) so a downstream renderer can negotiate
// the format it prefers. Called from handleThread on the success path.
func emitOEmbedDiscoveryHeaders(w http.ResponseWriter, r *http.Request) {
	if w == nil || r == nil {
		return
	}
	page := canonicalRequestURL(r)
	if page == "" {
		return
	}
	base := absoluteURL(r, "/services/oembed?url="+url.QueryEscape(page))
	if base == "" {
		return
	}
	w.Header().Add("Link", "<"+base+"&format=json>; rel=\"alternate\"; type=\"application/json+oembed\"")
	w.Header().Add("Link", "<"+base+"&format=xml>; rel=\"alternate\"; type=\"text/xml+oembed\"")
}
