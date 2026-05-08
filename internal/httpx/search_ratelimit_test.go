package httpx

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestSearchLimiterBurstThenRefill(t *testing.T) {
	limiter := newSearchLimiter(2, 1)
	now := time.Unix(1000, 0)
	if !limiter.allow(now, "ip:1.2.3.4") {
		t.Fatal("first allow denied")
	}
	if !limiter.allow(now, "ip:1.2.3.4") {
		t.Fatal("second allow denied")
	}
	if limiter.allow(now, "ip:1.2.3.4") {
		t.Fatal("expected deny after burst consumed")
	}
	if !limiter.allow(now.Add(1100*time.Millisecond), "ip:1.2.3.4") {
		t.Fatal("expected refill to allow")
	}
}

func TestSearchRemoteIP(t *testing.T) {
	req := httptest.NewRequest("GET", "/search?q=nostr", nil)
	req.RemoteAddr = "203.0.113.8:9012"
	if got := searchRemoteIP(req); got != "203.0.113.8" {
		t.Fatalf("searchRemoteIP = %q, want 203.0.113.8", got)
	}
}
