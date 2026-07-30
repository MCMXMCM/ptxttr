package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ptxt-nstr/internal/config"
)

func TestTrafficShieldLimitsAnonymousExpensiveRoutes(t *testing.T) {
	srv := &Server{
		cfg:              config.Config{},
		anonymousLimiter: newSearchLimiter(1, 0.001),
		botLimiter:       newSearchLimiter(100, 100),
		metrics:          newAppMetrics(),
	}
	handler := srv.trafficShield(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req1 := httptest.NewRequest(http.MethodGet, "/thread/"+strings.Repeat("a", 64)+"?fragment=replies&cursor=10", nil)
	req1.RemoteAddr = "203.0.113.10:1234"
	req1.Header.Set("User-Agent", "Mozilla/5.0")
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusNoContent {
		t.Fatalf("first anonymous request status = %d, want 204", rec1.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/thread/"+strings.Repeat("b", 64)+"?fragment=replies&cursor=20", nil)
	req2.RemoteAddr = "203.0.113.10:2345"
	req2.Header.Set("User-Agent", "Mozilla/5.0")
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second anonymous request status = %d, want 429", rec2.Code)
	}
	if rec2.Header().Get("Retry-After") == "" {
		t.Fatal("429 response missing Retry-After")
	}
}

func TestTrafficShieldIsDisabledForDesktop(t *testing.T) {
	srv := &Server{
		cfg:              config.Config{DesktopMode: true},
		anonymousLimiter: newSearchLimiter(1, 0.001),
		botLimiter:       newSearchLimiter(1, 0.001),
		viewerLimiter:    newSearchLimiter(1, 0.001),
		metrics:          newAppMetrics(),
	}
	handler := srv.trafficShield(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for i := 0; i < 4; i++ {
		req := httptest.NewRequest(http.MethodGet, "/thread/"+strings.Repeat("a", 64), nil)
		req.RemoteAddr = "203.0.113.10:1234"
		req.Header.Set("User-Agent", "Twitterbot/1.0")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("desktop request %d status = %d, want 204", i+1, rec.Code)
		}
	}
}

func TestTrafficShieldUsesStricterBotBucket(t *testing.T) {
	srv := &Server{
		cfg:              config.Config{},
		anonymousLimiter: newSearchLimiter(100, 100),
		botLimiter:       newSearchLimiter(1, 0.001),
		metrics:          newAppMetrics(),
	}
	handler := srv.trafficShield(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for i, want := range []int{http.StatusNoContent, http.StatusTooManyRequests} {
		req := httptest.NewRequest(http.MethodGet, "/u/"+strings.Repeat("a", 64), nil)
		req.RemoteAddr = "203.0.113.11:1234"
		req.Header.Set("User-Agent", "Twitterbot/1.0")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("bot request %d status = %d, want %d", i+1, rec.Code, want)
		}
	}
}

func TestTrafficShieldBypassesSignedInAndStaticRequests(t *testing.T) {
	srv := &Server{
		cfg:              config.Config{},
		anonymousLimiter: newSearchLimiter(1, 0.001),
		botLimiter:       newSearchLimiter(1, 0.001),
		metrics:          newAppMetrics(),
	}
	handler := srv.trafficShield(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, path := range []string{"/thread/" + strings.Repeat("a", 64), "/thread/" + strings.Repeat("b", 64)} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "203.0.113.12:1234"
		req.Header.Set("User-Agent", "Mozilla/5.0")
		req.Header.Set(headerViewerPubkey, strings.Repeat("f", 64))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("signed-in request %s status = %d, want 204", path, rec.Code)
		}
	}

	for _, path := range []string{"/static/app.js", avatarPathPrefix + "abc"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "203.0.113.12:1234"
		req.Header.Set("User-Agent", "Twitterbot/1.0")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("static-ish request %s status = %d, want 204", path, rec.Code)
		}
	}
}

func TestTrafficShieldLimitsSignedInExpensiveRoutesByViewer(t *testing.T) {
	srv := &Server{
		cfg:              config.Config{},
		anonymousLimiter: newSearchLimiter(100, 100),
		botLimiter:       newSearchLimiter(100, 100),
		viewerLimiter:    newSearchLimiter(1, 0.001),
		metrics:          newAppMetrics(),
	}
	handler := srv.trafficShield(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	viewer := strings.Repeat("f", 64)
	for i, want := range []int{http.StatusNoContent, http.StatusTooManyRequests} {
		req := httptest.NewRequest(http.MethodGet, "/thread/"+strings.Repeat("a", 64), nil)
		req.RemoteAddr = "203.0.113.12:1234"
		req.Header.Set("User-Agent", "Mozilla/5.0")
		req.Header.Set(headerViewerPubkey, viewer)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("signed-in request %d status = %d, want %d", i+1, rec.Code, want)
		}
	}
}

func TestTrafficShieldOptionalTrafficCannotConsumeRouteCapacity(t *testing.T) {
	srv := &Server{
		cfg:                      config.Config{},
		anonymousLimiter:         newSearchLimiter(1, 0.001),
		anonymousOptionalLimiter: newSearchLimiter(1, 0.001),
		botLimiter:               newSearchLimiter(100, 100),
		metrics:                  newAppMetrics(),
	}
	handler := srv.trafficShield(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := func(path string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "203.0.113.25:1234"
		req.Header.Set("User-Agent", "Mozilla/5.0")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := request("/api/avatar-meta?pubkey=" + strings.Repeat("a", 64)); got != http.StatusNoContent {
		t.Fatalf("first optional request status = %d, want 204", got)
	}
	if got := request("/api/avatar-meta?pubkey=" + strings.Repeat("b", 64)); got != http.StatusTooManyRequests {
		t.Fatalf("second optional request status = %d, want 429", got)
	}
	if got := request("/thread/" + strings.Repeat("c", 64)); got != http.StatusNoContent {
		t.Fatalf("route request status = %d, want independent 204", got)
	}
}

func TestTrafficShieldLimitsGuestFeedStatusInOptionalLane(t *testing.T) {
	srv := &Server{
		cfg:                      config.Config{},
		anonymousLimiter:         newSearchLimiter(1, 0.001),
		anonymousOptionalLimiter: newSearchLimiter(1, 0.001),
		botLimiter:               newSearchLimiter(100, 100),
		metrics:                  newAppMetrics(),
	}
	handler := srv.trafficShield(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := func(path string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "203.0.113.30:1234"
		req.Header.Set("User-Agent", "Mozilla/5.0")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := request("/api/guest-feed-status?since=1"); got != http.StatusNoContent {
		t.Fatalf("first guest status request = %d, want 204", got)
	}
	if got := request("/api/guest-feed-status?since=2"); got != http.StatusTooManyRequests {
		t.Fatalf("second guest status request = %d, want 429", got)
	}
	if got := request("/thread/" + strings.Repeat("d", 64)); got != http.StatusNoContent {
		t.Fatalf("document route request = %d, want independent 204", got)
	}
}

func TestClientRateKeyUsesEdgeViewerIPAndRejectsForwardedSpoofing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/feed", nil)
	req.RemoteAddr = "198.51.100.20:443"
	req.Header.Set("X-Ptxt-Client-IP", "203.0.113.40")
	req.Header.Set("X-Forwarded-For", "192.0.2.123, 198.51.100.20")
	if got := clientRateKey(req); got != "203.0.113.40" {
		t.Fatalf("edge viewer key = %q, want 203.0.113.40", got)
	}

	req.Header.Del("X-Ptxt-Client-IP")
	if got := clientRateKey(req); got != "198.51.100.20" {
		t.Fatalf("forwarded fallback key = %q, want right-most proxy-observed address", got)
	}
}
