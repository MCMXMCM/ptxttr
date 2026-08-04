// Command desktop-server is the loopback-only Go sidecar packaged with the
// Electron application.
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"ptxt-nstr/internal/apprun"
	"ptxt-nstr/internal/config"
	"ptxt-nstr/internal/memlimit"
	"ptxt-nstr/internal/store"
)

const defaultDesktopAddr = "127.0.0.1:24787"

func main() {
	if err := applyDesktopDefaults(); err != nil {
		log.Fatal(err)
	}
	memlimit.ApplyDefaultFromEnv()
	instance, err := apprun.Start(context.Background(), config.Load())
	if err != nil {
		log.Fatal(err)
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case <-stop:
	case err := <-waitForServer(instance):
		if err != nil {
			log.Fatal(err)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := instance.Shutdown(ctx); err != nil {
		log.Fatal(err)
	}
}

func applyDesktopDefaults() error {
	if strings.TrimSpace(os.Getenv("PTXT_DESKTOP_SESSION_TOKEN")) == "" &&
		strings.TrimSpace(os.Getenv("PTXT_DESKTOP_ACTIVITY_TOKEN")) == "" {
		return errors.New("PTXT_DESKTOP_SESSION_TOKEN is required")
	}
	// These values define the privileged shell contract and are not user
	// overrides. The packaged sidecar can never become a network service.
	for key, value := range map[string]string{
		"PTXT_ADDR":         defaultDesktopAddr,
		"PTXT_SERVER_MODE":  "app",
		"PTXT_DESKTOP_MODE": "1",
		"PTXT_PPROF_ADDR":   "off",
	} {
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	dataDir := os.Getenv("PTXT_DESKTOP_DATA_DIR")
	if dataDir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		dataDir = filepath.Join(base, "Plain Text Nostr", "local")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	defaults := map[string]string{
		"PTXT_DB":                                       filepath.Join(dataDir, "ptxt-nstr.sqlite"),
		"PTXT_EVENT_RETENTION":                          "0",
		"PTXT_RETENTION_BY_ACCESS":                      "true",
		"PTXT_DB_MAX_BYTES":                             strconv.FormatInt(store.DefaultCacheMaxBytes, 10),
		"PTXT_DB_PRUNE_TARGET_BYTES":                    strconv.FormatInt(store.DefaultCacheMaxBytes*9/10, 10),
		"PTXT_DB_MAX_DISK_PERCENT":                      "0",
		"PTXT_RELAY_MAX_OUTBOUND_CONNS":                 "12",
		"PTXT_WARM_WORKERS":                             "1",
		"PTXT_WARM_QUEUE_CAPACITY":                      "64",
		"PTXT_HYDRATION_NOTE_REPLIES_BATCH":             "16",
		"PTXT_GUEST_SLICE_V2":                           "false",
		"PTXT_HYDRATION_ENABLED":                        "true",
		"PTXT_SEED_CRAWLER_ENABLED":                     "false",
		"PTXT_VIEWER_CRAWLER_ENABLED":                   "true",
		"PTXT_VIEWER_CRAWLER_INTERVAL":                  "30s",
		"PTXT_VIEWER_CRAWLER_DEGREE2_INTERVAL":          "5m",
		"PTXT_VIEWER_CRAWLER_DEGREE3_INTERVAL":          "30m",
		"PTXT_VIEWER_CRAWLER_REDUCED_INTERVAL":          "5m",
		"PTXT_VIEWER_CRAWLER_DIRECT_METADATA_INTERVAL":  "15m",
		"PTXT_VIEWER_CRAWLER_DEGREE2_METADATA_INTERVAL": "6h",
		"PTXT_VIEWER_CRAWLER_DEGREE3_METADATA_INTERVAL": "24h",
		"PTXT_VIEWER_CRAWLER_PROFILE_INTERVAL":          "6h",
		"PTXT_ACTIVE_VIEWER_TRENDING":                   "false",
		"PTXT_HOT_FEED_CRAWLER_ENABLED":                 "false",
		"PTXT_COALESCE_ENABLED":                         "false",
		"PTXT_COMPACT_ON_START":                         "false",
	}
	for key, value := range defaults {
		if _, ok := os.LookupEnv(key); !ok {
			_ = os.Setenv(key, value)
		}
	}
	return nil
}

func waitForServer(instance *apprun.Instance) <-chan error {
	done := make(chan error, 1)
	go func() { done <- instance.Wait() }()
	return done
}
