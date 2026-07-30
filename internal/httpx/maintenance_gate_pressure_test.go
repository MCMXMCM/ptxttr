package httpx

import "testing"

func TestRunBackgroundUserAsyncDropsWhenForegroundHot(t *testing.T) {
	srv, _ := testServer(t)
	srv.Stop()

	srv.activeRequests.Store(userAsyncDropActiveRequestThreshold)
	ran := false
	srv.runBackgroundUserAsync(func() { ran = true })

	if ran {
		t.Fatal("background async job ran synchronously")
	}
	if got := len(srv.userAsyncQueue); got != 0 {
		t.Fatalf("queue length = %d, want 0", got)
	}
	counters := srv.metrics.Snapshot()["counters"].(map[string]int64)
	if counters["bg.user_async_dropped_foreground_hot"] != 1 {
		t.Fatalf("foreground hot drop counter = %d, want 1", counters["bg.user_async_dropped_foreground_hot"])
	}
}

func TestRunBackgroundUserAsyncDropsWhenQueueHot(t *testing.T) {
	srv, _ := testServer(t)
	srv.Stop()

	for len(srv.userAsyncQueue) < userAsyncDropQueueLenThreshold {
		srv.userAsyncQueue <- func() {}
	}
	srv.runBackgroundUserAsync(func() {})

	if got := len(srv.userAsyncQueue); got != userAsyncDropQueueLenThreshold {
		t.Fatalf("queue length = %d, want %d", got, userAsyncDropQueueLenThreshold)
	}
	counters := srv.metrics.Snapshot()["counters"].(map[string]int64)
	if counters["bg.user_async_dropped_queue_hot"] != 1 {
		t.Fatalf("queue hot drop counter = %d, want 1", counters["bg.user_async_dropped_queue_hot"])
	}
}
