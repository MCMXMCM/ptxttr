package httpx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewCoalesceMiddlewareDisabledIsPassthrough(t *testing.T) {
	called := false
	handler := newCoalesceMiddleware(coalesceConfig{Enabled: false})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	rr := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/thread/abc", nil)
	handler.ServeHTTP(rr, r)
	if !called {
		t.Fatal("disabled middleware should pass through")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestNewCoalesceMiddlewareWaiterRendersAfterLeader(t *testing.T) {
	gate := make(chan struct{})
	release := make(chan struct{})
	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32
	var calls atomic.Int32
	handler := newCoalesceMiddleware(coalesceConfig{Enabled: true, Buckets: 4, Timeout: 500 * time.Millisecond})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := concurrent.Add(1)
		for {
			old := maxConcurrent.Load()
			if c <= old {
				break
			}
			if maxConcurrent.CompareAndSwap(old, c) {
				break
			}
		}
		if calls.Add(1) == 1 {
			gate <- struct{}{}
			<-release
		}
		concurrent.Add(-1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	leadDone := make(chan struct{})
	leadRR := httptest.NewRecorder()
	go func() {
		handler.ServeHTTP(leadRR, httptest.NewRequest("GET", "/thread/abc", nil))
		close(leadDone)
	}()

	// Wait for the lead arriver to enter the handler.
	<-gate

	// An identical request must not enter the handler until the lead returns,
	// and then renders itself instead of redirecting.
	lateRR := httptest.NewRecorder()
	lateDone := make(chan struct{})
	go func() {
		handler.ServeHTTP(lateRR, httptest.NewRequest("GET", "/thread/abc", nil))
		close(lateDone)
	}()

	// Give the late request a moment to block behind the lead.
	time.Sleep(20 * time.Millisecond)
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler calls while the lead renders = %d, want 1", got)
	}
	close(release)

	<-leadDone
	<-lateDone

	if got := maxConcurrent.Load(); got != 1 {
		t.Fatalf("max concurrent in-flight = %d, want 1 (late arriver must wait)", got)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("handler calls = %d, want 2", got)
	}
	for name, rr := range map[string]*httptest.ResponseRecorder{"lead": leadRR, "late": lateRR} {
		if rr.Code != http.StatusOK || rr.Body.String() != "ok" {
			t.Fatalf("%s response = %d %q, want 200 ok", name, rr.Code, rr.Body.String())
		}
		if loc := rr.Header().Get("Location"); loc != "" {
			t.Fatalf("%s response redirected to %q", name, loc)
		}
	}
}

func TestNewCoalesceMiddlewareOnlyHoldsIdenticalRequests(t *testing.T) {
	mobileUA := "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Mobile/15E148"
	cases := map[string]func() *http.Request{
		"other path": func() *http.Request { return httptest.NewRequest("GET", "/thread/other", nil) },
		"other query": func() *http.Request {
			return httptest.NewRequest("GET", "/thread/abc?selected="+strings.Repeat("b", 64), nil)
		},
		"other width": func() *http.Request {
			r := httptest.NewRequest("GET", "/thread/abc", nil)
			r.Header.Set("User-Agent", mobileUA)
			return r
		},
		"signed in": func() *http.Request {
			r := httptest.NewRequest("GET", "/thread/abc", nil)
			markTestRequestLoggedIn(r)
			return r
		},
	}
	for name, second := range cases {
		t.Run(name, func(t *testing.T) {
			release := make(chan struct{})
			entered := make(chan struct{}, 2)
			// One shard puts every key in the same lock, so only the key itself
			// can keep the second request from running alongside the first.
			handler := newCoalesceMiddleware(coalesceConfig{Enabled: true, Buckets: 1, Timeout: time.Second})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				entered <- struct{}{}
				<-release
				w.WriteHeader(http.StatusOK)
			}))
			var wg sync.WaitGroup
			for _, r := range []*http.Request{httptest.NewRequest("GET", "/thread/abc", nil), second()} {
				wg.Add(1)
				go func(r *http.Request) {
					defer wg.Done()
					handler.ServeHTTP(httptest.NewRecorder(), r)
				}(r)
			}
			for i := 0; i < 2; i++ {
				select {
				case <-entered:
				case <-time.After(time.Second):
					t.Fatal("second request waited on a different coalescing key")
				}
			}
			close(release)
			wg.Wait()
		})
	}
}

func TestNewCoalesceMiddlewareTimesOut(t *testing.T) {
	holdRelease := make(chan struct{})
	defer close(holdRelease)
	handler := newCoalesceMiddleware(coalesceConfig{Enabled: true, Buckets: 2, Timeout: 50 * time.Millisecond})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-holdRelease
		w.WriteHeader(http.StatusOK)
	}))

	leadStart := make(chan struct{})
	go func() {
		close(leadStart)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/thread/abc", nil))
	}()
	<-leadStart
	time.Sleep(10 * time.Millisecond) // ensure the lead is holding the bucket

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("GET", "/thread/abc", nil))
	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", rr.Code)
	}
}

func TestNewCoalesceMiddlewareDifferentPathsRunInParallel(t *testing.T) {
	var inflight atomic.Int32
	var maxInflight atomic.Int32
	wg := sync.WaitGroup{}
	enter := make(chan struct{}, 2)
	leave := make(chan struct{})
	handler := newCoalesceMiddleware(coalesceConfig{Enabled: true, Buckets: 16, Timeout: time.Second})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := inflight.Add(1)
		for {
			old := maxInflight.Load()
			if c <= old {
				break
			}
			if maxInflight.CompareAndSwap(old, c) {
				break
			}
		}
		enter <- struct{}{}
		<-leave
		inflight.Add(-1)
		w.WriteHeader(http.StatusOK)
	}))
	for _, path := range []string{"/thread/a", "/thread/b"} {
		wg.Add(1)
		path := path
		go func() {
			defer wg.Done()
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
		}()
	}
	<-enter
	<-enter
	close(leave)
	wg.Wait()
	if got := maxInflight.Load(); got < 2 {
		t.Fatalf("max in-flight = %d, want >= 2 (different paths should not block each other)", got)
	}
}

func TestShouldCoalesceSkipsBypassPrefixes(t *testing.T) {
	cases := map[string]bool{
		"/static/foo.css":    false,
		"/avatar/abc":        false,
		"/api/relay-insight": false,
		"/debug/cache":       false,
		"/thread/abc":        true,
		"/u/abc":             true,
		"/":                  true,
		"/anything-else":     true,
	}
	for path, want := range cases {
		r := httptest.NewRequest("GET", path, nil)
		if got := shouldCoalesce(r); got != want {
			t.Fatalf("shouldCoalesce(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestShouldCoalesceSkipsSignedInRequests(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/thread/abc", nil)
	markTestRequestLoggedIn(r)
	if shouldCoalesce(r) {
		t.Fatal("signed-in request should not coalesce")
	}
}

func TestShouldCoalesceSkipsNonGet(t *testing.T) {
	r := httptest.NewRequest("POST", "/thread/abc", nil)
	if shouldCoalesce(r) {
		t.Fatal("POST should not coalesce")
	}
}

func TestShouldCoalesceSkipsFragmentQuery(t *testing.T) {
	for _, raw := range []string{
		"/thread/abc?fragment=hydrate",
		"/thread/abc?ascii_w=42&fragment=hydrate",
		"/feed?fragment=1",
		"/?fragment=heading",
	} {
		r := httptest.NewRequest(http.MethodGet, raw, nil)
		if shouldCoalesce(r) {
			t.Fatalf("shouldCoalesce(%q) = true, want false", raw)
		}
	}
}

func TestBucketIndexStable(t *testing.T) {
	a := bucketIndex("/thread/abc", 64)
	b := bucketIndex("/thread/abc", 64)
	if a != b {
		t.Fatalf("bucketIndex unstable: %d != %d", a, b)
	}
	if a < 0 || a >= 64 {
		t.Fatalf("bucketIndex out of range: %d", a)
	}
}

func TestBucketIndexSingleBucket(t *testing.T) {
	if got := bucketIndex("/anything", 1); got != 0 {
		t.Fatalf("bucketIndex with 1 bucket = %d, want 0", got)
	}
}

func TestNewCoalesceMiddlewareCancelDoesNotWriteResponse(t *testing.T) {
	holdRelease := make(chan struct{})
	defer close(holdRelease)
	handler := newCoalesceMiddleware(coalesceConfig{Enabled: true, Buckets: 2, Timeout: time.Second})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-holdRelease
		w.WriteHeader(http.StatusOK)
	}))

	leadStart := make(chan struct{})
	go func() {
		close(leadStart)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/thread/abc", nil))
	}()
	<-leadStart
	time.Sleep(10 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("GET", "/thread/abc", nil).WithContext(ctx))
	if rr.Code == http.StatusGatewayTimeout {
		t.Fatalf("canceled request should not get 504, got %d", rr.Code)
	}
}
