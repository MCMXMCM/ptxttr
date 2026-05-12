package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	goruntime "runtime"
	"sync"
	"sync/atomic"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"ptxt-nstr/internal/config"
	"ptxt-nstr/internal/httpx"
	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

// App owns the desktop lifecycle: same stack as cmd/server, served on loopback
// for the embedded webview after the splash screen.
type App struct {
	ctx context.Context

	bootMu sync.Mutex
	booted bool
	bootOk bool

	// splashHandoffComplete is true after we run JS to leave the embedded splash for loopback.
	// Reset on failed boot or before WindowReload so a subsequent DOM-ready can navigate again.
	splashHandoffComplete atomic.Bool

	store      *store.Store
	server     *httpx.Server
	listener   net.Listener
	httpServer *http.Server
	dataDir    string
	loopPort   int
}

func newApp() *App {
	return &App{}
}

func (a *App) onStartup(ctx context.Context) {
	a.ctx = ctx

	a.bootMu.Lock()
	defer a.bootMu.Unlock()
	if a.booted {
		return
	}
	a.booted = true

	dir, err := desktopDataDir()
	if err != nil {
		slog.Error("desktop data dir resolve failed", "err", err)
		return
	}
	a.dataDir = dir

	cfg := config.Load()
	openCtx := context.Background()

	st, err := store.Open(openCtx, cfg.DBPath)
	if err != nil {
		slog.Error("store open failed", "err", err, "path", cfg.DBPath)
		return
	}
	st.SetEventRetention(cfg.EventRetention)
	st.SetReplaceableHistory(cfg.ReplaceableHistory)
	if cfg.CompactOnStart {
		compactCtx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		deleted, compactErr := st.Compact(compactCtx, cfg.EventRetention)
		cancel()
		if compactErr != nil {
			slog.Error("startup compact failed", "err", compactErr)
			_ = st.Close()
			return
		}
		slog.Info("startup compact complete", "deleted_events", deleted, "retention", cfg.EventRetention)
	}
	trendingMetaCtx, trendingMetaCancel := context.WithTimeout(context.Background(), 5*time.Second)
	st.EnsureTrendingCacheSchemaVersion(trendingMetaCtx)
	trendingMetaCancel()

	nostrClient := nostrx.NewClient(cfg.DefaultRelays, cfg.RequestTimeout)
	srv, err := httpx.New(cfg, st, nostrClient)
	if err != nil {
		slog.Error("httpx init failed", "err", err)
		_ = st.Close()
		return
	}
	a.store = st
	a.server = srv

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		slog.Error("loopback listen failed", "err", err)
		_ = st.Close()
		return
	}
	a.listener = ln
	a.loopPort = ln.Addr().(*net.TCPAddr).Port

	a.httpServer = &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		slog.Info("desktop server starting", "addr", ln.Addr().String(), "db", cfg.DBPath)
		if err := a.httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("desktop server exited", "err", err)
		}
	}()

	a.bootOk = true
}

func (a *App) onDomReady(ctx context.Context) {
	a.ctx = ctx

	// After the first splash→loopback handoff, Wails still fires OnDomReady on
	// full reloads; reinstall the external-link hook (reload clears window state).
	if a.splashHandoffComplete.Load() {
		wailsruntime.WindowExecJS(ctx, desktopLoopbackLinkHookOnlyJS())
		return
	}

	if !a.splashHandoffComplete.CompareAndSwap(false, true) {
		wailsruntime.WindowExecJS(ctx, desktopLoopbackLinkHookOnlyJS())
		return
	}

	a.bootMu.Lock()
	port := a.loopPort
	ok := a.bootOk
	a.bootMu.Unlock()

	if !ok || port == 0 {
		a.splashHandoffComplete.Store(false)
		slog.Warn("desktop boot did not complete; staying on splash")
		return
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/", port)
	js := desktopSplashToLoopbackJS(url)
	wailsruntime.WindowExecJS(ctx, js)
}

// desktopLinkHookBody defines loop() + install() for http(s) → system browser.
const desktopLinkHookBody = `function loop(){return location.protocol==="http:"&&(location.hostname==="127.0.0.1"||location.hostname==="localhost");}function install(){if(window.__ptxtDesktopLinkHook)return;window.__ptxtDesktopLinkHook=true;document.addEventListener("click",function(ev){var p=ev.target,a=null;for(var n=p;n;n=n.parentElement){if(n.tagName==="A"){a=n;break;}}if(!a||!a.href)return;var u;try{u=new URL(a.href,location.href);}catch(e){return;}var h=u.hostname;if(h==="127.0.0.1"||h==="localhost")return;if(u.protocol!=="http:"&&u.protocol!=="https:")return;ev.preventDefault();ev.stopPropagation();fetch("/__ptxt/desktop/open-external",{method:"POST",headers:{"Content-Type":"application/json"},credentials:"same-origin",body:JSON.stringify({url:u.toString()})}).catch(function(){});},true);}`

func desktopLoopbackLinkHookOnlyJS() string {
	return "(function(){" + desktopLinkHookBody + "if(loop())install();})();"
}

// desktopSplashToLoopbackJS leaves the embedded splash for the loopback app and
// installs a capture-phase click handler so http(s) links open in the default
// browser (Wails does not inject window.runtime into httpx-served pages).
func desktopSplashToLoopbackJS(loopbackOriginURL string) string {
	tj, _ := json.Marshal(loopbackOriginURL)
	return fmt.Sprintf(
		`(function(){var T=%s;%s if(loop()){install();return;}location.replace(T);var c=0,t=setInterval(function(){if(loop()){clearInterval(t);install();}else if(++c>400){clearInterval(t);}},25);})();`,
		string(tj),
		desktopLinkHookBody,
	)
}

func (a *App) forceRenavigate(ctx context.Context) {
	a.splashHandoffComplete.Store(false)
	a.onDomReady(ctx)
}

// resetSplashHandoff allows the next DOM-ready to navigate off the embedded splash again.
func (a *App) resetSplashHandoff() {
	a.splashHandoffComplete.Store(false)
}

func (a *App) onBeforeClose(_ context.Context) bool {
	slog.Info("desktop close requested")
	return false
}

func (a *App) onShutdown(_ context.Context) {
	if a.httpServer != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = a.httpServer.Shutdown(shutCtx)
		cancel()
	}
	if a.server != nil {
		a.server.Close()
	}
	if a.store != nil {
		_ = a.store.Close()
	}
	slog.Info("desktop shutdown complete")
}

func (a *App) openDataDir() {
	if a.dataDir == "" {
		return
	}
	if goruntime.GOOS == "darwin" {
		if err := exec.Command("open", a.dataDir).Run(); err != nil {
			slog.Warn("open data dir failed", "err", err, "path", a.dataDir)
		}
		return
	}
	slog.Warn("Open Data Folder is only wired for macOS", "path", a.dataDir)
}
