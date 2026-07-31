package httpx

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

const (
	desktopActivityPath     = "/__ptxt/desktop/activity"
	desktopStoragePath      = "/__ptxt/desktop/storage"
	desktopStorageClearPath = "/__ptxt/desktop/storage/clear"
	desktopFollowGraphPath  = "/__ptxt/desktop/follow-graph"
	desktopCacheMinBytes    = int64(64 * 1024 * 1024)
	desktopCacheMaxBytes    = int64(1024 * 1024 * 1024 * 1024)
)

type desktopActivityBody struct {
	Active bool `json:"active"`
}

type desktopStorageClearBody struct {
	Scope string `json:"scope"`
}

type desktopStorageLimitBody struct {
	MaxBytes int64 `json:"max_bytes"`
}

type desktopFollowGraphResponse struct {
	Pubkey    string   `json:"pubkey"`
	Following []string `json:"following"`
	Followers []string `json:"followers"`
	Relays    []string `json:"relays"`
}

func (s *Server) handleDesktopActivity(w http.ResponseWriter, r *http.Request) {
	if s == nil || !s.runtimeCapabilities().DesktopShell || s.cfg.DesktopActivityToken == "" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	provided := r.Header.Get("X-Ptxt-Desktop-Token")
	if len(provided) != len(s.cfg.DesktopActivityToken) || subtle.ConstantTimeCompare([]byte(provided), []byte(s.cfg.DesktopActivityToken)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256)
	var body desktopActivityBody
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.backgroundActive.Store(body.Active)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// handleDesktopFollowGraph is a loopback-only fallback for browser relay reads.
// It asks the local relay client for the profile's current kind-3 event, stores
// it locally, and returns the graph already available on this device.
func (s *Server) handleDesktopFollowGraph(w http.ResponseWriter, r *http.Request) {
	if s == nil || !s.runtimeCapabilities().StorageControls || s.store == nil {
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
	if s == nil || !s.runtimeCapabilities().StorageControls || s.store == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPut {
		s.handleDesktopStorageLimit(w, r)
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

func (s *Server) handleDesktopStorageLimit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	var body desktopStorageLimitBody
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if body.MaxBytes < desktopCacheMinBytes || body.MaxBytes > desktopCacheMaxBytes {
		http.Error(w, "cache limit must be between 64 MiB and 1 TiB", http.StatusBadRequest)
		return
	}
	if err := s.store.SetAppMeta(r.Context(), store.AppMetaKeyCacheMaxBytes, strconv.FormatInt(body.MaxBytes, 10)); err != nil {
		slog.Warn("desktop cache preference save failed", "err", err)
		http.Error(w, "cache limit save failed", http.StatusInternalServerError)
		return
	}
	s.store.SetRetentionPolicy(true)
	s.store.SetDiskByteRetentionPolicy(body.MaxBytes, body.MaxBytes*9/10)
	s.store.RequestPruneAsync()
	usage, err := s.store.CacheUsage(r.Context())
	if err != nil {
		slog.Warn("desktop storage usage after limit update failed", "err", err)
		http.Error(w, "storage usage failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(usage)
}

func (s *Server) handleDesktopStorageClear(w http.ResponseWriter, r *http.Request) {
	if s == nil || !s.runtimeCapabilities().StorageControls || s.store == nil {
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
