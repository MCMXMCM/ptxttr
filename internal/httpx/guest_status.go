package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

type guestFeedStatus struct {
	Generation   int64  `json:"generation"`
	ComputedAt   int64  `json:"computed_at"`
	TopCreatedAt int64  `json:"top_created_at"`
	TopID        string `json:"top_id"`
	NewCount     int    `json:"new_count"`
}

func (s *Server) guestFeedNewCount(ctx context.Context, since int64, sinceID string) int {
	if s == nil || s.store == nil || since <= 0 {
		return 0
	}
	sinceID = nostrx.CanonicalHex64(sinceID)
	snap, ok, err := s.store.GetDefaultSeedGuestFeedSnapshot(ctx)
	if err != nil || !ok || snap == nil {
		return 0
	}
	count := 0
	for _, event := range snap.Feed {
		id := strings.ToLower(strings.TrimSpace(event.ID))
		if event.CreatedAt > since || (event.CreatedAt == since && sinceID != "" && id > sinceID) {
			count++
		}
	}
	return count
}

func (s *Server) currentGuestGeneration(ctx context.Context) int64 {
	// Kept as a small adapter so BasePageData and middleware share the same
	// single-row projection read without exposing guest state to templates.
	if s == nil || s.store == nil {
		return 0
	}
	state, found, err := s.store.GetGuestSliceState(ctx, store.GuestSliceDefaultKey)
	if err != nil || !found || state.Status != store.GuestSliceStatusReady {
		return 0
	}
	return state.Generation
}

func (s *Server) guestRouteHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if asciiWidthDocumentPath(r.URL.Path) {
			// The measured mobile width cookie changes the response bytes at one
			// URL. CloudFront projects it into its cache key; Vary also keeps the
			// browser from reusing a fresh pre-measurement document after the
			// cookie changes.
			w.Header().Add("Vary", "Cookie")
		}
		guestV2Document := s.cfg.GuestSliceV2Enabled && anonymousRequestFromHTTP(r) && guestDocumentPath(r.URL.Path)
		if guestV2Document {
			if generation := s.currentGuestGeneration(r.Context()); generation > 0 {
				w.Header().Set("X-Ptxt-Guest-Generation", strconv.FormatInt(generation, 10))
			}
			w.Header().Set("X-Ptxt-Route-Status", "ready")
		}
		prefetchRequest := r.Header.Get("X-Ptxt-Prefetch") == "1" ||
			strings.Contains(strings.ToLower(r.Header.Get("Sec-Purpose")), "prefetch") ||
			strings.Contains(strings.ToLower(r.Header.Get("Purpose")), "prefetch")
		if prefetchRequest && guestV2Document {
			next.ServeHTTP(&guestPrefetchResponseWriter{ResponseWriter: w}, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func asciiWidthDocumentPath(path string) bool {
	return path == "/" || path == "/feed" || path == "/reads" || path == "/bookmarks" ||
		path == "/search" || path == "/notifications" || strings.HasPrefix(path, "/thread/") ||
		strings.HasPrefix(path, "/u/") || strings.HasPrefix(path, "/tag/")
}

type guestPrefetchResponseWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *guestPrefetchResponseWriter) applyBrowserTTL() {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	cacheControl := w.Header().Get("Cache-Control")
	if strings.Contains(cacheControl, "max-age=0") {
		w.Header().Set("Cache-Control", strings.Replace(cacheControl, "max-age=0", "max-age=60", 1))
	}
}

func (w *guestPrefetchResponseWriter) WriteHeader(status int) {
	w.applyBrowserTTL()
	w.ResponseWriter.WriteHeader(status)
}

func (w *guestPrefetchResponseWriter) Write(body []byte) (int, error) {
	w.applyBrowserTTL()
	return w.ResponseWriter.Write(body)
}

func guestDocumentPath(path string) bool {
	return path == "/" || path == "/feed" || strings.HasPrefix(path, "/thread/") || strings.HasPrefix(path, "/u/")
}

func (s *Server) handleGuestFeedStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	since, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("since")), 10, 64)
	sinceID := nostrx.CanonicalHex64(r.URL.Query().Get("since_id"))
	status := guestFeedStatus{NewCount: s.guestFeedNewCount(r.Context(), since, sinceID)}
	if state, ok, err := s.store.GetGuestSliceState(r.Context(), store.GuestSliceDefaultKey); err == nil && ok && state.Status == store.GuestSliceStatusReady {
		status.Generation = state.Generation
		status.ComputedAt = state.ComputedAt
		status.TopCreatedAt = state.TopCreatedAt
		status.TopID = state.TopID
	} else if snap, ok, err := s.store.GetDefaultSeedGuestFeedSnapshot(r.Context()); err == nil && ok && snap != nil && len(snap.Feed) > 0 {
		status.ComputedAt = snap.ComputedAtUnix
		status.TopCreatedAt = snap.Feed[0].CreatedAt
		status.TopID = snap.Feed[0].ID
	}
	etag := "guest-feed-" + strconv.FormatInt(status.Generation, 10) + "-" + status.TopID + "-" +
		strconv.FormatInt(since, 10) + "-" + sinceID + "-" + strconv.Itoa(status.NewCount)
	w.Header().Set("Cache-Control", "public, max-age=60, s-maxage=60, stale-while-revalidate=300")
	w.Header().Set("ETag", quotedETag(etag))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if matchesETag(r, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if r.Method == http.MethodHead {
		return
	}
	_ = json.NewEncoder(w).Encode(status)
}
