package httpx

import (
	"maps"
	"net/http"
	"net/url"
	"sort"
	"strconv"
)

type anonymousHTMLDocument struct {
	Body        []byte
	ETag        string
	ContentType string
	Headers     http.Header
}

func newAnonymousHTMLCache() *ttlCache[anonymousHTMLDocument] {
	return newTTLCache(anonymousHTMLCacheTTL, anonymousHTMLCacheMaxLen, cloneAnonymousHTMLDocument)
}

func cloneAnonymousHTMLDocument(in anonymousHTMLDocument) anonymousHTMLDocument {
	out := in
	if in.Body != nil {
		out.Body = append([]byte(nil), in.Body...)
	}
	if in.Headers != nil {
		out.Headers = maps.Clone(in.Headers)
	}
	return out
}

func anonymousHTMLCacheKey(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	path := r.URL.EscapedPath()
	if path == "" {
		path = r.URL.Path
	}
	values := normalizedCacheQuery(r.URL.Query())
	key := path
	if encoded := values.Encode(); encoded != "" {
		key += "?" + encoded
	}
	// SSR contains width-dependent wrapping and ASCII chrome. Keep the origin
	// document cache partitioned even when the visible URL is identical.
	return key + "|ascii_w=" + strconv.Itoa(requestASCIIWidthWithQuery(r))
}

func normalizedCacheQuery(values url.Values) url.Values {
	out := make(url.Values, len(values))
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		copied := append([]string(nil), values[key]...)
		sort.Strings(copied)
		for _, value := range copied {
			out.Add(key, value)
		}
	}
	return out
}

func writeAnonymousHTMLDocument(w http.ResponseWriter, doc anonymousHTMLDocument) {
	if w == nil {
		return
	}
	for key, value := range doc.Headers {
		w.Header()[key] = append([]string(nil), value...)
	}
	if doc.ContentType != "" {
		w.Header().Set("Content-Type", doc.ContentType)
	} else {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	if doc.ETag != "" {
		setThreadPageCache(w, doc.ETag)
	}
	_, _ = w.Write(doc.Body)
}

type headerCapture struct {
	HeaderMap http.Header
}

func (h headerCapture) Header() http.Header {
	if h.HeaderMap == nil {
		return http.Header{}
	}
	return h.HeaderMap
}

func (h headerCapture) Write([]byte) (int, error) {
	return 0, nil
}

func (h headerCapture) WriteHeader(int) {}
