package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"syscall"
	"time"

	"ptxt-nstr/internal/config"
	"ptxt-nstr/internal/httpx"
	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

// defaultMemoryLimitBytes is the soft heap ceiling we apply when neither
// GOMEMLIMIT nor PTXT_MEMORY_LIMIT_BYTES is set in the environment. The Go
// runtime will run GC more aggressively as the live heap approaches this
// number, which prevents pathological renderer paths from quietly growing
// the resident set into the tens of gigabytes before backpressure kicks in.
// 1 GiB leaves room for SQLite page cache, mmap, and OS headroom on a
// t4g.small while still giving the heap enough space for normal bursts.
// Operators on larger hosts should set
// GOMEMLIMIT (or PTXT_MEMORY_LIMIT_BYTES) explicitly.
const defaultMemoryLimitBytes int64 = 1 * 1024 * 1024 * 1024

const (
	trendingCacheSchemaVersionKey = "trending_cache.schema_version"
	trendingCacheSchemaVersion    = "3"
)

func main() {
	applyDefaultMemoryLimit()
	cfg := config.Load()
	ctx := context.Background()

	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	st.SetEventRetention(cfg.EventRetention)
	st.SetReplaceableHistory(cfg.ReplaceableHistory)
	st.SetRetentionPolicy(cfg.RetentionByAccess)
	st.SetDiskRetentionPolicy(cfg.DBDiskMaxPercent, cfg.DBDiskPruneTargetPercent)
	removeLegacyBoltGraphFile(cfg.DBPath)
	compactTimeout := cfg.VacuumTimeout
	if compactTimeout <= 0 {
		compactTimeout = 60 * time.Minute
	}
	shouldCompact := cfg.CompactOnStart
	shouldVacuum := false
	if _, _, used, ok := store.DBFileUsage(cfg.DBPath); ok && cfg.DBDiskMaxPercent > 0 && used >= float64(cfg.DBDiskMaxPercent) {
		slog.Warn("db file budget prune at startup", "db_file_percent", used, "threshold", cfg.DBDiskMaxPercent, "target", cfg.DBDiskPruneTargetPercent)
		pruneCtx, cancel := context.WithTimeout(context.Background(), compactTimeout)
		deleted, pruneErr := st.PruneEventsToDiskTarget(pruneCtx)
		cancel()
		if pruneErr != nil {
			log.Fatal(pruneErr)
		}
		slog.Info("startup db file budget prune complete", "deleted_events", deleted)
		shouldVacuum = true
	}
	if used, ok := store.DBDiskUsagePercent(cfg.DBPath); ok && used >= float64(cfg.DiskPressurePercent) {
		slog.Warn("disk pressure compact at startup", "used_percent", used, "threshold", cfg.DiskPressurePercent)
		shouldCompact = true
	}
	if shouldCompact && cfg.EventRetention > 0 {
		compactCtx, cancel := context.WithTimeout(context.Background(), compactTimeout)
		deleted, compactErr := st.Compact(compactCtx, cfg.EventRetention)
		cancel()
		if compactErr != nil {
			log.Fatal(compactErr)
		}
		slog.Info("startup compact complete", "deleted_events", deleted, "retention", cfg.EventRetention)
		shouldVacuum = false
	} else if shouldCompact {
		shouldVacuum = true
	}
	if shouldVacuum {
		vacuumCtx, cancel := context.WithTimeout(context.Background(), compactTimeout)
		vacuumErr := st.VacuumFull(vacuumCtx)
		cancel()
		if vacuumErr != nil {
			log.Fatal(vacuumErr)
		}
		slog.Info("startup vacuum complete")
	}
	trendingMetaCtx, trendingMetaCancel := context.WithTimeout(context.Background(), 5*time.Second)
	ensureTrendingCacheVersion(trendingMetaCtx, st)
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

	go func() {
		<-stop
		slog.Warn("second shutdown signal received; exiting immediately")
		os.Exit(1)
	}()

	app.Stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatal(err)
	}
}

// removeLegacyBoltGraphFile deletes the former default bbolt WoT file next to
// the SQLite database if present (duplicate data after migrating to SQLite-only).
func removeLegacyBoltGraphFile(dbPath string) {
	if dbPath == "" {
		return
	}
	legacy := filepath.Join(filepath.Dir(dbPath), "ptxt-nstr.graph.bbolt")
	st, err := os.Stat(legacy)
	if err != nil || st.IsDir() {
		return
	}
	if err := os.Remove(legacy); err != nil {
		slog.Warn("legacy bolt graph file could not be removed", "path", legacy, "err", err)
		return
	}
	slog.Info("removed legacy bolt graph file", "path", legacy)
}

// applyDefaultMemoryLimit installs a runtime memory limit when the operator
// hasn't already done so. We honor an explicit GOMEMLIMIT (handled by the Go
// runtime itself), and we additionally accept PTXT_MEMORY_LIMIT_BYTES as a
// raw integer override for environments that find GOMEMLIMIT's IEC suffixes
// awkward. If neither is set we fall back to defaultMemoryLimitBytes so a
// runaway renderer can't silently allocate the host into swap.
func applyDefaultMemoryLimit() {
	if os.Getenv("GOMEMLIMIT") != "" {
		return
	}
	limit := defaultMemoryLimitBytes
	if raw := os.Getenv("PTXT_MEMORY_LIMIT_BYTES"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed <= 0 {
			slog.Warn("ignoring PTXT_MEMORY_LIMIT_BYTES", "value", raw, "err", err)
		} else {
			limit = parsed
		}
	}
	debug.SetMemoryLimit(limit)
	slog.Info("memory limit applied", "bytes", limit)
}

func ensureTrendingCacheVersion(ctx context.Context, st *store.Store) {
	if st == nil {
		return
	}
	version, ok, err := st.AppMeta(ctx, trendingCacheSchemaVersionKey)
	if err != nil {
		slog.Warn("startup trending cache version check failed", "err", err)
		return
	}
	if ok && version == trendingCacheSchemaVersion {
		return
	}
	if ok && version != "" {
		if err := st.ClearTrendingCache(ctx, "", ""); err != nil {
			slog.Warn("startup trending cache clear failed; continuing", "err", err)
			return
		}
	}
	if err := st.SetAppMeta(ctx, trendingCacheSchemaVersionKey, trendingCacheSchemaVersion); err != nil {
		slog.Warn("startup trending cache version write failed", "err", err)
	}
}
