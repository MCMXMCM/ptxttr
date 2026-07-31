// Package apprun owns the shared Go application lifecycle used by the CLI and
// the Electron sidecar. Keeping server construction here prevents the desktop
// build from quietly drifting away from the local CLI server.
package apprun

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ptxt-nstr/internal/config"
	"ptxt-nstr/internal/httpx"
	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

const (
	trendingCacheSchemaVersionKey = "trending_cache.schema_version"
	trendingCacheSchemaVersion    = "3"
)

// Instance is a running local application server.
type Instance struct {
	App      *httpx.Server
	Store    *store.Store
	HTTP     *http.Server
	Listener net.Listener
	done     chan error
}

// Start opens storage, constructs the application, binds cfg.Addr, and starts
// serving. Binding happens synchronously so a desktop port collision is
// reported to Electron instead of being lost in a background goroutine.
func Start(ctx context.Context, cfg config.Config) (*Instance, error) {
	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return nil, err
	}
	cleanupStore := true
	defer func() {
		if cleanupStore {
			_ = st.Close()
		}
	}()

	st.SetEventRetention(cfg.EventRetention)
	applySavedDesktopCacheLimit(ctx, &cfg, st)
	st.SetReplaceableHistory(cfg.ReplaceableHistory)
	st.SetRetentionPolicy(cfg.RetentionByAccess)
	st.SetDiskRetentionPolicy(cfg.DBDiskMaxPercent, cfg.DBDiskPruneTargetPercent)
	st.SetDiskByteRetentionPolicy(cfg.DBMaxBytes, cfg.DBPruneTargetBytes)
	compactTimeout := cfg.VacuumTimeout
	if compactTimeout <= 0 {
		compactTimeout = 60 * time.Minute
	}
	if err := runStartupMaintenance(cfg, st, compactTimeout); err != nil {
		return nil, err
	}
	versionCtx, cancelVersion := context.WithTimeout(ctx, 5*time.Second)
	ensureTrendingCacheVersion(versionCtx, st)
	cancelVersion()

	nostrClient := nostrx.NewClient(cfg.DefaultRelays, cfg.RequestTimeout)
	app, err := httpx.New(cfg, st, nostrClient)
	if err != nil {
		return nil, err
	}
	cleanupApp := true
	defer func() {
		if cleanupApp {
			app.Close()
		}
	}()

	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return nil, err
	}
	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	instance := &Instance{
		App: app, Store: st, HTTP: httpServer, Listener: listener,
		done: make(chan error, 1),
	}
	go func() {
		slog.Info("server starting", "addr", listener.Addr().String(), "db", cfg.DBPath)
		err := httpServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		instance.done <- err
		close(instance.done)
	}()
	cleanupApp = false
	cleanupStore = false
	return instance, nil
}

func applySavedDesktopCacheLimit(ctx context.Context, cfg *config.Config, st *store.Store) {
	if cfg == nil || st == nil || !cfg.DesktopMode {
		return
	}
	raw, ok, err := st.AppMeta(ctx, store.AppMetaKeyCacheMaxBytes)
	if err != nil {
		slog.Warn("desktop cache preference read failed", "err", err)
		return
	}
	if !ok {
		return
	}
	maxBytes, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || maxBytes <= 0 {
		slog.Warn("ignoring invalid desktop cache preference", "value", raw)
		return
	}
	cfg.DBMaxBytes = maxBytes
	cfg.DBPruneTargetBytes = maxBytes * 9 / 10
	cfg.RetentionByAccess = true
}

func runStartupMaintenance(cfg config.Config, st *store.Store, timeout time.Duration) error {
	shouldCompact := cfg.CompactOnStart
	shouldVacuum := false
	if _, _, used, ok := store.DBFileUsage(cfg.DBPath); ok && cfg.DBDiskMaxPercent > 0 && used >= float64(cfg.DBDiskMaxPercent) {
		pruneCtx, cancel := context.WithTimeout(context.Background(), timeout)
		_, err := st.PruneEventsToDiskTarget(pruneCtx)
		cancel()
		if err != nil {
			return err
		}
		shouldVacuum = true
	}
	if cfg.DBMaxBytes > 0 && store.DBFileBytes(cfg.DBPath) >= cfg.DBMaxBytes {
		pruneCtx, cancel := context.WithTimeout(context.Background(), timeout)
		_, err := st.PruneEventsToByteTarget(pruneCtx)
		cancel()
		if err != nil {
			return err
		}
		shouldVacuum = true
	}
	if used, ok := store.DBDiskUsagePercent(cfg.DBPath); ok && used >= float64(cfg.DiskPressurePercent) {
		shouldCompact = true
	}
	if shouldCompact && cfg.EventRetention > 0 {
		compactCtx, cancel := context.WithTimeout(context.Background(), timeout)
		_, err := st.Compact(compactCtx, cfg.EventRetention)
		cancel()
		if err != nil {
			return err
		}
		shouldVacuum = false
	} else if shouldCompact {
		shouldVacuum = true
	}
	if shouldVacuum {
		vacuumCtx, cancel := context.WithTimeout(context.Background(), timeout)
		err := st.VacuumFull(vacuumCtx)
		cancel()
		return err
	}
	return nil
}

// Wait blocks until the HTTP server exits.
func (i *Instance) Wait() error {
	if i == nil || i.done == nil {
		return nil
	}
	return <-i.done
}

// Shutdown gracefully stops HTTP work, background workers, and SQLite.
func (i *Instance) Shutdown(ctx context.Context) error {
	if i == nil {
		return nil
	}
	if i.App != nil {
		i.App.Stop()
	}
	var shutdownErr error
	if i.HTTP != nil {
		shutdownErr = i.HTTP.Shutdown(ctx)
	}
	if i.App != nil {
		i.App.Close()
	}
	if i.Store != nil {
		if err := i.Store.Close(); shutdownErr == nil {
			shutdownErr = err
		}
	}
	return shutdownErr
}

func ensureTrendingCacheVersion(ctx context.Context, st *store.Store) {
	version, ok, err := st.AppMeta(ctx, trendingCacheSchemaVersionKey)
	if err != nil || (ok && version == trendingCacheSchemaVersion) {
		return
	}
	if ok && version != "" {
		if err := st.ClearTrendingCache(ctx, "", ""); err != nil {
			slog.Warn("startup trending cache clear failed", "err", err)
			return
		}
	}
	if err := st.SetAppMeta(ctx, trendingCacheSchemaVersionKey, trendingCacheSchemaVersion); err != nil {
		slog.Warn("startup trending cache version write failed", "err", err)
	}
}
