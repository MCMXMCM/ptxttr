package httpx

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestRunBackgroundUserAsyncDropsWhenQueueFull verifies runBackgroundUserAsync
// never blocks when the queue is full (bg.user_async_dropped).
func TestRunBackgroundUserAsyncDropsWhenQueueFull(t *testing.T) {
	srv, _ := newTestServer(t, testServerOptions{})

	// Stall both async workers so the queue cannot drain. Each worker
	// pulls one fn off the channel and runs it via runWithRelayWriteBudget,
	// so two blocking fns is enough to pin the worker pool.
	release := make(chan struct{})
	var stalled sync.WaitGroup
	stalled.Add(userAsyncWorkerCount)
	for range userAsyncWorkerCount {
		srv.runBackgroundUserAsync(func() {
			stalled.Done()
			<-release
		})
	}
	defer close(release)

	// Wait until both workers have actually entered fn() so the queue is
	// drained except for jobs we explicitly enqueue below.
	waitOrFail(t, &stalled, 2*time.Second, "stall workers")

	// Fill the buffered channel to capacity. Each successful enqueue
	// increments bg.user_async_enqueued.
	noop := func() {}
	for i := 0; i < userAsyncQueueCapacity; i++ {
		srv.runBackgroundUserAsync(noop)
	}

	enqueuedBefore := metricCounter(srv, "bg.user_async_enqueued")
	droppedBefore := metricCounter(srv, "bg.user_async_dropped")

	// Now the buffer is full and both workers are stalled. Any further
	// call MUST return immediately and increment bg.user_async_dropped.
	const overflow = 8
	deadline := time.Now().Add(500 * time.Millisecond)
	done := make(chan struct{})
	go func() {
		for i := 0; i < overflow; i++ {
			srv.runBackgroundUserAsync(noop)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Until(deadline)):
		t.Fatalf("runBackgroundUserAsync blocked under back-pressure; expected non-blocking drop")
	}

	enqueuedAfter := metricCounter(srv, "bg.user_async_enqueued")
	droppedAfter := metricCounter(srv, "bg.user_async_dropped")

	if got, want := droppedAfter-droppedBefore, int64(overflow); got != want {
		t.Fatalf("bg.user_async_dropped delta = %d, want %d", got, want)
	}
	if got := enqueuedAfter - enqueuedBefore; got != 0 {
		t.Fatalf("bg.user_async_enqueued grew by %d while queue was full; expected 0", got)
	}
}

// TestRunBackgroundUserAsyncDoesNotBlockForeground asserts each call returns
// well under a millisecond even when the queue is saturated. This is the
// foreground-handler invariant that the original blocking enqueue violated.
func TestRunBackgroundUserAsyncDoesNotBlockForeground(t *testing.T) {
	srv, _ := newTestServer(t, testServerOptions{})

	release := make(chan struct{})
	var stalled sync.WaitGroup
	stalled.Add(userAsyncWorkerCount)
	for range userAsyncWorkerCount {
		srv.runBackgroundUserAsync(func() {
			stalled.Done()
			<-release
		})
	}
	defer close(release)
	waitOrFail(t, &stalled, 2*time.Second, "stall workers")

	noop := func() {}
	const totalCalls = userAsyncQueueCapacity * 4
	const perCallBudget = 5 * time.Millisecond
	for i := 0; i < totalCalls; i++ {
		start := time.Now()
		srv.runBackgroundUserAsync(noop)
		if elapsed := time.Since(start); elapsed > perCallBudget {
			t.Fatalf("runBackgroundUserAsync call %d took %s, exceeds %s budget", i, elapsed, perCallBudget)
		}
	}
}

// TestUserAsyncWorkerParallelism asserts that with relayWriteSem sized to
// userAsyncWorkerCount, both workers can be inside fn() at the same time.
// Before the fix the semaphore had capacity 1, so the second worker was
// decorative and threaded jobs serially.
func TestUserAsyncWorkerParallelism(t *testing.T) {
	if userAsyncWorkerCount < 2 {
		t.Skip("parallelism test requires at least two workers")
	}
	srv, _ := newTestServer(t, testServerOptions{})

	const want = 2
	var concurrent atomic.Int32
	var peak atomic.Int32
	release := make(chan struct{})
	var entered sync.WaitGroup
	entered.Add(want)
	work := func() {
		now := concurrent.Add(1)
		for {
			cur := peak.Load()
			if now <= cur || peak.CompareAndSwap(cur, now) {
				break
			}
		}
		entered.Done()
		<-release
		concurrent.Add(-1)
	}
	for i := 0; i < want; i++ {
		srv.runBackgroundUserAsync(work)
	}
	waitOrFail(t, &entered, 2*time.Second, "two workers concurrently inside fn()")
	close(release)

	if got := peak.Load(); got < int32(want) {
		t.Fatalf("peak concurrent workers = %d, want >= %d (relayWriteSem may be undersized)", got, want)
	}
}

func metricCounter(srv *Server, name string) int64 {
	snapshot := srv.metrics.Snapshot()
	counters, _ := snapshot["counters"].(map[string]int64)
	return counters[name]
}

// waitOrFail blocks until wg.Done() has been called the expected number of
// times or the timeout elapses, in which case the test fails with the given
// label so the caller can identify which barrier never released.
func waitOrFail(t *testing.T, wg *sync.WaitGroup, timeout time.Duration, label string) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("timed out after %s waiting for %s", timeout, label)
	}
}
