package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ptxt-nstr/internal/config"
	"ptxt-nstr/internal/httpx"
	"ptxt-nstr/internal/memlimit"
	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

func main() {
	memlimit.ApplyDefaultFromEnv()
	cfg := config.Load()
	ctx := context.Background()

	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	st.SetEventRetention(cfg.EventRetention)
	st.SetReplaceableHistory(cfg.ReplaceableHistory)
	if cfg.CompactOnStart {
		compactCtx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		deleted, compactErr := st.Compact(compactCtx, cfg.EventRetention)
		cancel()
		if compactErr != nil {
			log.Fatal(compactErr)
		}
		slog.Info("startup compact complete", "deleted_events", deleted, "retention", cfg.EventRetention)
	}
	trendingMetaCtx, trendingMetaCancel := context.WithTimeout(context.Background(), 5*time.Second)
	st.EnsureTrendingCacheSchemaVersion(trendingMetaCtx)
	trendingMetaCancel()

	nostrClient := nostrx.NewClient(cfg.DefaultRelays, cfg.RequestTimeout)
	app, err := httpx.New(cfg, st, nostrClient)
	if err != nil {
		log.Fatal(err)
	}
	defer app.Close()

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		slog.Info("server starting", "addr", cfg.Addr, "db", cfg.DBPath)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatal(err)
	}
}
