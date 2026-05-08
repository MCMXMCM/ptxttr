package httpx

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"ptxt-nstr/internal/nostrx"
)

const (
	searchRateBurstDefault   = 5
	searchRatePerSecondDelta = 1.0
	searchRateMaxKeys        = 4096
)

type searchRateBucket struct {
	tokens     float64
	lastRefill time.Time
}

type searchLimiter struct {
	mu         sync.Mutex
	buckets    map[string]searchRateBucket
	burst      float64
	refillRate float64
}

func newSearchLimiter(burst int, perSecond float64) *searchLimiter {
	if burst <= 0 {
		burst = searchRateBurstDefault
	}
	if perSecond <= 0 {
		perSecond = searchRatePerSecondDelta
	}
	return &searchLimiter{
		buckets:    make(map[string]searchRateBucket),
		burst:      float64(burst),
		refillRate: perSecond,
	}
}

func (l *searchLimiter) allow(now time.Time, keys ...string) bool {
	if l == nil || l.burst <= 0 || l.refillRate <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, key := range keys {
		if key == "" {
			continue
		}
		bucket := l.buckets[key]
		if bucket.lastRefill.IsZero() {
			bucket.lastRefill = now
			bucket.tokens = l.burst
		}
		elapsed := now.Sub(bucket.lastRefill).Seconds()
		if elapsed > 0 {
			bucket.tokens += elapsed * l.refillRate
			if bucket.tokens > l.burst {
				bucket.tokens = l.burst
			}
			bucket.lastRefill = now
		}
		if bucket.tokens < 1 {
			l.buckets[key] = bucket
			return false
		}
		bucket.tokens--
		l.buckets[key] = bucket
	}
	if len(l.buckets) <= searchRateMaxKeys {
		return true
	}
	for key, bucket := range l.buckets {
		if now.Sub(bucket.lastRefill) > 10*time.Minute {
			delete(l.buckets, key)
		}
	}
	for key := range l.buckets {
		if len(l.buckets) <= searchRateMaxKeys {
			break
		}
		delete(l.buckets, key)
	}
	return true
}

func searchRemoteIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	host := strings.TrimSpace(r.RemoteAddr)
	if host == "" {
		return ""
	}
	parsedHost, _, err := net.SplitHostPort(host)
	if err == nil {
		return parsedHost
	}
	return host
}

func normalizeViewerKey(raw string) string {
	if raw == "" {
		return ""
	}
	decoded, err := nostrx.DecodeIdentifier(raw)
	if err != nil {
		return ""
	}
	return decoded
}
