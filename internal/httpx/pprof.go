package httpx

import (
	"context"
	"errors"
	"expvar"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"time"
)

// pprofShutdownTimeout bounds how long Close() waits for the pprof listener
// to drain in-flight profile requests (a 30s CPU profile is the worst case).
const pprofShutdownTimeout = 35 * time.Second

// startPprofListener brings up an HTTP server bound to addr that mounts
// net/http/pprof and expvar handlers on a fresh ServeMux. It is deliberately
// kept out of the public Handler() chain so the public site never serves
// /debug/pprof, even briefly during a config flip.
//
// Lifecycle:
//   - listener creation failure logs a warning and returns (no fatal).
//   - one background goroutine runs Serve; context.AfterFunc schedules Shutdown
//     when s.ctx is cancelled so Close() does not rely on a second waiter.
func (s *Server) startPprofListener(addr string) {
	if s == nil || addr == "" {
		return
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		slog.Warn("pprof listener failed to bind; profiling disabled", "addr", addr, "err", err)
		return
	}
	mux := http.NewServeMux()
	registerPprofHandlers(mux)

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	slog.Info("pprof listener started", "addr", listener.Addr().String())

	stopShutdown := context.AfterFunc(s.ctx, func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), pprofShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Warn("pprof listener shutdown error", "err", err)
		}
	})

	s.runBackground(func() {
		defer func() { stopShutdown() }()
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Warn("pprof listener exited with error", "err", err)
		}
	})
}

func registerPprofHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	mux.Handle("/debug/pprof/allocs", pprof.Handler("allocs"))
	mux.Handle("/debug/pprof/block", pprof.Handler("block"))
	mux.Handle("/debug/pprof/goroutine", pprof.Handler("goroutine"))
	mux.Handle("/debug/pprof/heap", pprof.Handler("heap"))
	mux.Handle("/debug/pprof/mutex", pprof.Handler("mutex"))
	mux.Handle("/debug/pprof/threadcreate", pprof.Handler("threadcreate"))
	mux.Handle("/debug/vars", expvar.Handler())
}
