package httpx

import (
	"context"
	"time"
)

const (
	foregroundBusyWindow     = 150 * time.Millisecond
	foregroundQuietWaitLimit = 750 * time.Millisecond
)

// tryRunMaintenanceWork runs fn when no other maintenance job holds the global
// gate and the foreground is not actively serving requests. Skipped ticks
// increment metrics for tuning.
func (s *Server) tryRunMaintenanceWork(fn func()) {
	if s == nil || fn == nil {
		return
	}
	if s.foregroundBusy() {
		s.metrics.Add("bg.maintenance_deferred_hot", 1)
		return
	}
	if !s.maintenanceRunning.CompareAndSwap(false, true) {
		s.metrics.Add("bg.maintenance_skipped_busy", 1)
		return
	}
	defer s.maintenanceRunning.Store(false)
	fn()
}

func (s *Server) foregroundBusy() bool {
	if s == nil {
		return false
	}
	if s.activeRequests.Load() > 0 {
		return true
	}
	last := s.lastRequestAt.Load()
	if last <= 0 {
		return false
	}
	return time.Since(time.Unix(last, 0)) < foregroundBusyWindow
}

func (s *Server) waitForForegroundQuiet(ctx context.Context, maxWait time.Duration) {
	if s == nil || !s.foregroundBusy() {
		return
	}
	s.metrics.Add("bg.foreground_wait", 1)
	deadline := time.Now().Add(maxWait)
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for s.foregroundBusy() && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) runWithRelayWriteBudget(ctx context.Context, kind string, fn func()) {
	if s == nil || fn == nil {
		return
	}
	if ctx == nil {
		ctx = s.ctx
	}
	select {
	case <-ctx.Done():
		s.metrics.Add(kind+".cancelled", 1)
		return
	case s.relayWriteSem <- struct{}{}:
	}
	defer func() { <-s.relayWriteSem }()
	s.waitForForegroundQuiet(ctx, foregroundQuietWaitLimit)
	fn()
}

// runBackgroundUserAsync enqueues short follow-up work (e.g. guest fragment
// warm) onto a bounded worker queue instead of dropping it under burst load.
func (s *Server) runBackgroundUserAsync(fn func()) {
	if s == nil || fn == nil {
		return
	}
	select {
	case <-s.ctx.Done():
		return
	default:
	}
	select {
	case s.userAsyncQueue <- fn:
		s.metrics.Add("bg.user_async_enqueued", 1)
	default:
		s.metrics.Add("bg.user_async_backpressure", 1)
		select {
		case <-s.ctx.Done():
			return
		case s.userAsyncQueue <- fn:
			s.metrics.Add("bg.user_async_enqueued", 1)
		}
	}
}

func (s *Server) runUserAsyncWorker() {
	if s == nil {
		return
	}
	for {
		select {
		case <-s.ctx.Done():
			return
		case fn := <-s.userAsyncQueue:
			started := time.Now()
			s.runWithRelayWriteBudget(s.ctx, "bg.user_async", fn)
			s.metrics.Observe("bg.user_async_exec", time.Since(started))
		}
	}
}

func (s *Server) markRequestStart(now time.Time) {
	if s == nil {
		return
	}
	s.lastRequestAt.Store(now.Unix())
	s.activeRequests.Add(1)
}

func (s *Server) markRequestDone() {
	if s == nil {
		return
	}
	s.activeRequests.Add(-1)
}
