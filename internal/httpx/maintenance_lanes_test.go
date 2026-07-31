package httpx

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMaintenanceLanesRunConcurrently(t *testing.T) {
	srv, _ := newTestServer(t, testServerOptions{})

	var seedHeld, viewerHeld atomic.Bool
	seedDone := make(chan struct{})
	viewerDone := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		srv.tryRunMaintenanceWork(maintenanceLaneSeed, func() {
			seedHeld.Store(true)
			time.Sleep(100 * time.Millisecond)
			close(seedDone)
		})
	}()
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)
		srv.tryRunMaintenanceWork(maintenanceLaneViewer, func() {
			viewerHeld.Store(true)
			close(viewerDone)
		})
	}()

	select {
	case <-viewerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("viewer maintenance lane blocked while seed lane held gate")
	}
	select {
	case <-seedDone:
	case <-time.After(2 * time.Second):
		t.Fatal("seed maintenance lane did not finish")
	}
	wg.Wait()
	if !seedHeld.Load() || !viewerHeld.Load() {
		t.Fatal("expected both maintenance lanes to run")
	}
}

func TestMaintenancePausesAndResumesWithDesktopActivity(t *testing.T) {
	srv, _ := newTestServer(t, testServerOptions{})
	runs := 0

	srv.backgroundActive.Store(false)
	srv.tryRunMaintenanceWork(maintenanceLaneSeed, func() { runs++ })
	if runs != 0 {
		t.Fatalf("maintenance ran while desktop activity was paused: runs=%d", runs)
	}

	srv.backgroundActive.Store(true)
	srv.tryRunMaintenanceWork(maintenanceLaneSeed, func() { runs++ })
	if runs != 1 {
		t.Fatalf("maintenance did not resume with desktop activity: runs=%d", runs)
	}
}
