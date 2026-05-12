package httpx

import (
	"container/list"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"ptxt-nstr/internal/nostrx"
)

const (
	avatarCacheCapacity     = 512
	avatarMaxBodyBytes      = 2 << 20 // 2 MiB
	avatarFetchTimeout      = 10 * time.Second
	avatarFetchMaxAttempts  = 3
	avatarFetchRetryBackoff = 200 * time.Millisecond
	avatarMaxRedirects      = 3
	avatarSuccessMaxAge     = 31536000 // one year, paired with content-fingerprinted URLs
	avatarPathPrefix        = "/avatar/"
	avatarFingerprintSize   = 12 // hex chars, ~48 bits of entropy on the URL
	// avatarFetchUserAgent is browser-like so CDNs and hotlink guards that block unknown bots still serve images.
	avatarFetchUserAgent = "Mozilla/5.0 (compatible; ptxt-nstr/1; +https://example.com) AppleWebKit/537.36 (KHTML, like Gecko) avatar-proxy"
)

var avatarAllowedTypes = map[string]bool{
	"image/jpeg":    true,
	"image/jpg":     true,
	"image/png":     true,
	"image/gif":     true,
	"image/webp":    true,
	"image/avif":    true,
	"image/svg+xml": true,
}

// avatarEntry holds a cached upstream avatar response.
type avatarEntry struct {
	bodyHash    string // strong fingerprint of the response body
	contentType string
	body        []byte
}

// avatarCache is a tiny mutex-protected LRU keyed by the upstream URL.
// We key on the URL (not pubkey) so multiple pubkeys that happen to share a
// picture host the same bytes, and so cache invalidation just falls out of
// the URL changing when a profile updates its metadata.
type avatarCache struct {
	mu       sync.Mutex
	capacity int
	order    *list.List
	items    map[string]*list.Element
}

type avatarCacheItem struct {
	url   string
	entry avatarEntry
}

func newAvatarCache(capacity int) *avatarCache {
	if capacity <= 0 {
		capacity = avatarCacheCapacity
	}
	return &avatarCache{
		capacity: capacity,
		order:    list.New(),
		items:    make(map[string]*list.Element, capacity),
	}
}

func (c *avatarCache) get(url string) (avatarEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.items[url]; ok {
		c.order.MoveToFront(elem)
		return elem.Value.(*avatarCacheItem).entry, true
	}
	return avatarEntry{}, false
}

func (c *avatarCache) put(url string, entry avatarEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.items[url]; ok {
		elem.Value.(*avatarCacheItem).entry = entry
		c.order.MoveToFront(elem)
		return
	}
	elem := c.order.PushFront(&avatarCacheItem{url: url, entry: entry})
	c.items[url] = elem
	for c.order.Len() > c.capacity {
		oldest := c.order.Back()
		if oldest == nil {
			return
		}
		c.order.Remove(oldest)
		delete(c.items, oldest.Value.(*avatarCacheItem).url)
	}
}

// avatarFingerprint returns a short stable hash of an upstream URL. We use
// it as a cache-busting query param on the proxied URL: when the upstream
// URL changes (because the user updated their profile metadata), the
// fingerprint changes, and the browser cache treats it as a new resource
// regardless of any aggressive Cache-Control we set on the previous one.
func avatarFingerprint(url string) string {
	if url == "" {
		return ""
	}
	sum := sha1.Sum([]byte(url))
	encoded := hex.EncodeToString(sum[:])
	if len(encoded) > avatarFingerprintSize {
		encoded = encoded[:avatarFingerprintSize]
	}
	return encoded
}

// avatarSrcFor returns the proxied URL the browser should request for a
// pubkey, or empty string when there's no upstream picture to serve.
func avatarSrcFor(pubkey, upstream string) string {
	if pubkey == "" || upstream == "" {
		return ""
	}
	return fmt.Sprintf("%s%s?v=%s", avatarPathPrefix, pubkey, avatarFingerprint(upstream))
}

// avatarPathPubkey normalizes a path segment the same way as /u/ routes (npub
// or hex). Unknown shapes fall back to the raw value so tests and legacy
// single-segment keys keep working.
func avatarPathPubkey(raw string) string {
	raw = strings.Trim(strings.TrimSpace(raw), "/")
	if raw == "" || strings.ContainsAny(raw, "/?#") {
		return raw
	}
	if decoded, err := nostrx.DecodeIdentifier(raw); err == nil {
		return decoded
	}
	return raw
}

func (s *Server) handleAvatar(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	pubkey := strings.TrimPrefix(r.URL.Path, avatarPathPrefix)
	pubkey = avatarPathPubkey(pubkey)
	if pubkey == "" || strings.ContainsAny(pubkey, "/?#") {
		http.NotFound(w, r)
		return
	}
	profile := s.profile(r.Context(), pubkey)
	upstream := strings.TrimSpace(profile.Picture)
	if upstream == "" {
		// No-store so a previously cached 404/502 for a bare /avatar/ path does
		// not outlive metadata filling in; fingerprinted /avatar/..?v= URLs
		// (used in feeds/profile) are a different cache key.
		w.Header().Set("Cache-Control", "no-store")
		http.NotFound(w, r)
		return
	}

	// All successful loads use the same fingerprinted URL as templates (e.g.
	// user profile) so the browser shares one cache entry. Requests without a v
	// param, or with a stale v, redirect to the canonical URL.
	requested := r.URL.Query().Get("v")
	expected := avatarFingerprint(upstream)
	if requested != expected {
		http.Redirect(w, r, avatarSrcFor(pubkey, upstream), http.StatusTemporaryRedirect)
		return
	}

	entry, ok := s.avatarCache.get(upstream)
	if !ok {
		fetched, err := s.fetchAvatar(r.Context(), upstream)
		if err != nil {
			host := ""
			if u, parseErr := url.Parse(upstream); parseErr == nil {
				host = u.Host
			}
			slog.Warn("avatar fetch failed", "host", host, "class", avatarFetchErrClass(err))
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, "avatar unavailable", http.StatusBadGateway)
			return
		}
		s.avatarCache.put(upstream, fetched)
		entry = fetched
	}

	if match := r.Header.Get("If-None-Match"); match != "" && match == quotedETag(entry.bodyHash) {
		w.Header().Set("ETag", quotedETag(entry.bodyHash))
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d, immutable", avatarSuccessMaxAge))
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", entry.contentType)
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d, immutable", avatarSuccessMaxAge))
	w.Header().Set("ETag", quotedETag(entry.bodyHash))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(entry.body)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(entry.body)
}

func quotedETag(hash string) string {
	return `"` + hash + `"`
}

// fetchAvatar pulls an avatar from upstream with retries on transient errors,
// browser-like User-Agent, and magic-byte sniffing when Content-Type is wrong
// or missing. No cookies; no Referer (many CDNs still accept this UA).
func (s *Server) fetchAvatar(ctx context.Context, upstream string) (avatarEntry, error) {
	if !strings.HasPrefix(upstream, "http://") && !strings.HasPrefix(upstream, "https://") {
		return avatarEntry{}, errors.New("avatar: unsupported scheme")
	}
	var lastErr error
	for attempt := 0; attempt < avatarFetchMaxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return avatarEntry{}, ctx.Err()
			case <-time.After(avatarFetchRetryBackoff):
			}
		}
		entry, err := s.fetchAvatarOnce(ctx, upstream)
		if err == nil {
			return entry, nil
		}
		lastErr = err
		if !isAvatarFetchRetryable(err) {
			break
		}
	}
	return avatarEntry{}, lastErr
}

func (s *Server) fetchAvatarOnce(ctx context.Context, upstream string) (avatarEntry, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, avatarFetchTimeout)
	defer cancel()
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= avatarMaxRedirects {
				return errors.New("avatar: too many redirects")
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, upstream, nil)
	if err != nil {
		return avatarEntry{}, err
	}
	req.Header.Set("Accept", "image/*,*/*;q=0.8")
	req.Header.Set("User-Agent", avatarFetchUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return avatarEntry{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return avatarEntry{}, &avatarUpstreamHTTPError{Code: resp.StatusCode}
	}
	limited := io.LimitReader(resp.Body, avatarMaxBodyBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return avatarEntry{}, err
	}
	if len(body) > avatarMaxBodyBytes {
		return avatarEntry{}, errors.New("avatar: upstream body too large")
	}
	contentType := normalizeContentType(resp.Header.Get("Content-Type"))
	if !avatarAllowedTypes[contentType] {
		if sniffed := sniffImageMIME(body); sniffed != "" {
			contentType = sniffed
		}
	}
	if !avatarAllowedTypes[contentType] {
		return avatarEntry{}, fmt.Errorf("avatar: disallowed content-type %q", contentType)
	}
	hash := sha1.Sum(body)
	return avatarEntry{
		bodyHash:    hex.EncodeToString(hash[:]),
		contentType: contentType,
		body:        body,
	}, nil
}

type avatarUpstreamHTTPError struct {
	Code int
}

func (e *avatarUpstreamHTTPError) Error() string {
	return fmt.Sprintf("avatar: upstream status %d", e.Code)
}

func isAvatarFetchRetryable(err error) bool {
	var up *avatarUpstreamHTTPError
	if errors.As(err, &up) {
		return up.Code == http.StatusTooManyRequests || (up.Code >= 500 && up.Code <= 504)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	var opErr *net.OpError
	return errors.As(err, &opErr)
}

func avatarFetchErrClass(err error) string {
	if err == nil {
		return "none"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var up *avatarUpstreamHTTPError
	if errors.As(err, &up) {
		if up.Code >= 500 {
			return "upstream_5xx"
		}
		if up.Code == http.StatusTooManyRequests {
			return "upstream_429"
		}
		if up.Code >= 400 {
			return "upstream_4xx"
		}
		return "upstream_other"
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return "network"
	}
	if strings.Contains(err.Error(), "avatar: unsupported scheme") {
		return "unsupported_scheme"
	}
	if strings.Contains(err.Error(), "avatar: disallowed content-type") {
		return "validation_content_type"
	}
	if strings.Contains(err.Error(), "avatar: upstream body too large") {
		return "validation_size"
	}
	if strings.Contains(err.Error(), "too many redirects") {
		return "redirects"
	}
	return "other"
}

// sniffImageMIME returns a canonical image/* type when magic bytes match, else "".
func sniffImageMIME(b []byte) string {
	if len(b) < 12 {
		return ""
	}
	switch {
	case len(b) >= 8 && string(b[0:8]) == "\x89PNG\r\n\x1a\n":
		return "image/png"
	case len(b) >= 3 && b[0] == 0xff && b[1] == 0xd8 && b[2] == 0xff:
		return "image/jpeg"
	case len(b) >= 6 && (string(b[0:6]) == "GIF87a" || string(b[0:6]) == "GIF89a"):
		return "image/gif"
	case len(b) >= 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "image/webp"
	case len(b) >= 12 && string(b[4:8]) == "ftyp":
		// ISO BMFF: AVIF often uses brands avif, mif1, msf1.
		br := string(b[8:min(32, len(b))])
		if strings.Contains(br, "avif") || strings.Contains(br, "mif1") || strings.Contains(br, "msf1") {
			return "image/avif"
		}
	}
	return ""
}

func normalizeContentType(raw string) string {
	if idx := strings.IndexByte(raw, ';'); idx >= 0 {
		raw = raw[:idx]
	}
	return strings.ToLower(strings.TrimSpace(raw))
}
