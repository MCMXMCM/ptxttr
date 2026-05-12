package httpx

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
)

const (
	desktopOpenExternalPath = "/__ptxt/desktop/open-external"
	maxDesktopOpenURLLen    = 2048
)

type desktopOpenExternalBody struct {
	URL string `json:"url"`
}

// handleDesktopOpenExternal opens an http(s) URL in the system browser. It is
// only registered when cfg.DesktopMode is true (Wails desktop shell). Intended
// for same-origin fetch from injected UI script on the loopback server.
func (s *Server) handleDesktopOpenExternal(w http.ResponseWriter, r *http.Request) {
	if s == nil || !s.cfg.DesktopMode {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	const maxBody = 4096
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	var body desktopOpenExternalBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	raw := strings.TrimSpace(body.URL)
	if raw == "" || len(raw) > maxDesktopOpenURLLen {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		http.Error(w, "unsupported scheme", http.StatusBadRequest)
		return
	}
	_, _ = io.Copy(io.Discard, r.Body)

	if err := openURLInSystemBrowser(u.String()); err != nil {
		slog.Warn("desktop open external failed", "url", u.Redacted(), "err", err)
		http.Error(w, "open failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func openURLInSystemBrowser(raw string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", raw).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", raw).Start()
	default:
		return exec.Command("xdg-open", raw).Start()
	}
}
