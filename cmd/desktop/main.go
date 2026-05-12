// Command desktop wraps the same httpx.Server used by cmd/server in a Wails v2
// native window so users can run the app locally without deploying the server
// stack. cmd/server remains the canonical CLI entrypoint.
package main

import (
	"embed"
	"log"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"

	"ptxt-nstr/internal/memlimit"
)

//go:embed all:frontend/dist
var frontendAssets embed.FS

func main() {
	memlimit.ApplyDefaultFromEnv()
	if err := applyDesktopDefaults(); err != nil {
		log.Fatalf("desktop defaults: %v", err)
	}

	app := newApp()

	err := wails.Run(&options.App{
		Title:             "Plain Text Nostr",
		Width:             1280,
		Height:            900,
		MinWidth:          720,
		MinHeight:         480,
		HideWindowOnClose: false,
		BackgroundColour:  options.NewRGB(15, 15, 15),
		AssetServer: &assetserver.Options{
			Assets: frontendAssets,
		},
		Menu:          buildMenu(app),
		OnStartup:     app.onStartup,
		OnDomReady:    app.onDomReady,
		OnBeforeClose: app.onBeforeClose,
		OnShutdown:    app.onShutdown,
		Mac: &mac.Options{
			About: &mac.AboutInfo{
				Title:   "Plain Text Nostr",
				Message: "A local Nostr reader / writer.\nYou are running the desktop build.",
			},
		},
	})
	if err != nil {
		log.Fatalf("wails: %v", err)
	}
}

// applyDesktopDefaults seeds env defaults for config.Load() before the Wails
// app starts. Only sets keys that are not already present so power users can
// override via PTXT_* from the shell. The desktop entry does not use PTXT_ADDR;
// the HTTP server binds a dynamic loopback port in (*App).onStartup.
func applyDesktopDefaults() error {
	dir, err := desktopDataDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	setEnvIfUnset("PTXT_DB", filepath.Join(dir, "ptxt-nstr.sqlite"))
	// Background compaction can stall first launch on a fresh machine; the
	// hosted server may compact on boot but desktop users would see a frozen window.
	setEnvIfUnset("PTXT_COMPACT_ON_START", "false")
	setEnvIfUnset("PTXT_ACTIVE_VIEWER_TRENDING", "false")
	// Avoid clashing with a dev server on 6060; set PTXT_PPROF_ADDR explicitly to enable.
	setEnvIfUnset("PTXT_PPROF_ADDR", "off")
	setEnvIfUnset("PTXT_DESKTOP_MODE", "1")
	slog.Info("desktop defaults applied", "data_dir", dir)
	return nil
}

func setEnvIfUnset(key, value string) {
	if _, ok := os.LookupEnv(key); ok {
		return
	}
	_ = os.Setenv(key, value)
}

// desktopDataDir is the per-user data directory for SQLite (macOS:
// ~/Library/Application Support/ptxt-nstr/).
func desktopDataDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ptxt-nstr"), nil
}
