package httpx

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"ptxt-nstr/internal/nostrx"
)

const (
	desktopOpenExternalPath = "/__ptxt/desktop/open-external"
	desktopStoragePath      = "/__ptxt/desktop/storage"
	desktopStorageClearPath = "/__ptxt/desktop/storage/clear"
	desktopFollowGraphPath  = "/__ptxt/desktop/follow-graph"
	maxDesktopOpenURLLen    = 2048
)

type desktopOpenExternalBody struct {
	URL string `json:"url"`
}

type desktopStorageClearBody struct {
	Scope string `json:"scope"`
}

type desktopFollowGraphResponse struct {
	Pubkey    string   `json:"pubkey"`
	Following []string `json:"following"`
	Followers []string `json:"followers"`
	Relays    []string `json:"relays"`
}

// handleDesktopFollowGraph is a loopback-only fallback for WebKit relay reads.
// It asks the local relay client for the profile's current kind-3 event, stores
// it locally, and returns the graph already available on this device.
func (s *Server) handleDesktopFollowGraph(w http.ResponseWriter, r *http.Request) {
	if s == nil || !s.cfg.DesktopMode || s.store == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	pubkey := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("pubkey")))
	if !nostrx.IsValidPubKeyHex(pubkey) {
		http.Error(w, "invalid pubkey", http.StatusBadRequest)
		return
	}
	relays := s.authorMetadataRelays(r.Context(), pubkey, s.requestRelays(r))
	if s.nostr != nil && len(relays) > 0 {
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		fetched, err := s.nostr.FetchFrom(ctx, relays, nostrx.Query{
			Authors: []string{pubkey},
			Kinds:   []int{nostrx.KindFollowList},
			Limit:   3,
		})
		cancel()
		if err == nil && len(fetched) > 0 {
			_, _ = s.store.SaveEvents(r.Context(), fetched)
		}
	}
	event, _ := s.store.LatestReplaceable(r.Context(), pubkey, nostrx.KindFollowList)
	following := filterValidFollowPubkeys(nostrx.FollowPubkeys(event))
	followers := s.followers(r.Context(), pubkey, 250)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(desktopFollowGraphResponse{
		Pubkey:    pubkey,
		Following: following,
		Followers: followers,
		Relays:    relays,
	})
}

func (s *Server) handleDesktopStorage(w http.ResponseWriter, r *http.Request) {
	if s == nil || !s.cfg.DesktopMode || s.store == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	usage, err := s.store.CacheUsage(r.Context())
	if err != nil {
		slog.Warn("desktop storage usage failed", "err", err)
		http.Error(w, "storage usage failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(usage)
}

func (s *Server) handleDesktopStorageClear(w http.ResponseWriter, r *http.Request) {
	if s == nil || !s.cfg.DesktopMode || s.store == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var body desktopStorageClearBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	result, err := s.store.ClearCache(r.Context(), strings.TrimSpace(body.Scope))
	if err != nil {
		slog.Warn("desktop cache clear failed", "scope", body.Scope, "err", err)
		http.Error(w, "cache clear failed", http.StatusBadRequest)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
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
