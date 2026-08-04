package httpx

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

const (
	desktopActivityPath     = "/__ptxt/desktop/activity"
	desktopStoragePath      = "/__ptxt/desktop/storage"
	desktopStorageClearPath = "/__ptxt/desktop/storage/clear"
	desktopFollowGraphPath  = "/__ptxt/desktop/follow-graph"
	desktopReplaceablePath  = "/__ptxt/desktop/replaceable"
	desktopRelayFetchPath   = "/__ptxt/desktop/relay-fetch"
	desktopCacheMinBytes    = int64(64 * 1024 * 1024)
	desktopCacheMaxBytes    = int64(1024 * 1024 * 1024 * 1024)
)

const (
	desktopRelayFetchMaxBody    = 64 << 10
	desktopRelayFetchMaxFilters = 16
	desktopRelayFetchMaxEvents  = 2000
)

type desktopActivityBody struct {
	Mode   string `json:"mode"`
	Active *bool  `json:"active,omitempty"`
}

type desktopRelayFilter struct {
	IDs     []string `json:"ids"`
	Authors []string `json:"authors"`
	Kinds   []int    `json:"kinds"`
	ETags   []string `json:"#e"`
	PTags   []string `json:"#p"`
	TTags   []string `json:"#t"`
	Search  string   `json:"search"`
	Since   int64    `json:"since"`
	Until   int64    `json:"until"`
	Limit   int      `json:"limit"`
}

type desktopRelayFetchBody struct {
	Relays    []string             `json:"relays"`
	Filters   []desktopRelayFilter `json:"filters"`
	TimeoutMS int                  `json:"timeout_ms"`
}

// handleDesktopRelayFetch keeps Chromium out of the relay transport layer.
// It accepts the small subset of Nostr filters used by the renderer, persists
// every accepted event in SQLite, and returns the same event array expected by
// relayFetch callers.
func (s *Server) handleDesktopRelayFetch(w http.ResponseWriter, r *http.Request) {
	if s == nil || !s.runtimeCapabilities().DesktopShell || s.store == nil || s.nostr == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, desktopRelayFetchMaxBody)
	var body desktopRelayFetchBody
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if len(body.Filters) == 0 || len(body.Filters) > desktopRelayFetchMaxFilters {
		http.Error(w, "invalid relay filter count", http.StatusBadRequest)
		return
	}
	relays := nostrx.NormalizeRelayList(append(body.Relays, s.requestRelays(r)...), nostrx.MaxRelays)
	if len(relays) == 0 {
		relays = nostrx.NormalizeRelayList(append(append([]string(nil), s.cfg.DefaultRelays...), s.cfg.MetadataRelays...), nostrx.MaxRelays)
	}
	if len(relays) == 0 {
		http.Error(w, "no relays configured", http.StatusBadRequest)
		return
	}
	queries := make([]nostrx.Query, 0, len(body.Filters))
	for _, filter := range body.Filters {
		query, err := desktopRelayQuery(filter)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		queries = append(queries, query)
	}
	timeout := time.Duration(body.TimeoutMS) * time.Millisecond
	if timeout < time.Second || timeout > 15*time.Second {
		timeout = 12 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	type result struct {
		events []nostrx.Event
	}
	results := make(chan result, len(queries))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, query := range queries {
		query := query
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			events, _ := s.nostr.FetchFrom(ctx, relays, query)
			if len(events) > 0 {
				results <- result{events: events}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()
	byID := make(map[string]nostrx.Event)
	for row := range results {
		for _, event := range row.events {
			if event.ID == "" {
				continue
			}
			current, exists := byID[event.ID]
			if !exists && len(byID) >= desktopRelayFetchMaxEvents {
				continue
			}
			if !exists || event.CreatedAt > current.CreatedAt {
				byID[event.ID] = event
			}
		}
	}
	events := make([]nostrx.Event, 0, len(byID))
	for _, event := range byID {
		events = append(events, event)
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].CreatedAt == events[j].CreatedAt {
			return events[i].ID > events[j].ID
		}
		return events[i].CreatedAt > events[j].CreatedAt
	})
	if len(events) > 0 {
		persistCtx, persistCancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, err := s.store.SaveEvents(persistCtx, events)
		persistCancel()
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, events, nil)
}

func desktopRelayQuery(filter desktopRelayFilter) (nostrx.Query, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = nostrx.DefaultRelayQueryLimit
	} else if limit > nostrx.MaxRelayQueryLimit {
		limit = nostrx.MaxRelayQueryLimit
	}
	query := nostrx.Query{
		IDs:     limitedStrings(uniqueNonEmptyStrings(filter.IDs), 64),
		Authors: limitedStrings(uniqueNonEmptyStrings(filter.Authors), 64),
		Kinds:   limitedInts(filter.Kinds, 32),
		Search:  strings.TrimSpace(filter.Search),
		Since:   filter.Since,
		Until:   filter.Until,
		Limit:   limit,
		Tags:    map[string][]string{},
	}
	if len(query.Search) > 256 {
		return nostrx.Query{}, httpError("search is too long", http.StatusBadRequest)
	}
	for name, values := range map[string][]string{
		"e": filter.ETags,
		"p": filter.PTags,
		"t": filter.TTags,
	} {
		values = limitedStrings(uniqueNonEmptyStrings(values), 64)
		if len(values) > 0 {
			query.Tags[name] = values
		}
	}
	if len(query.IDs) == 0 && len(query.Authors) == 0 && len(query.Kinds) == 0 && len(query.Tags) == 0 && query.Search == "" {
		return nostrx.Query{}, httpError("relay filter must be constrained", http.StatusBadRequest)
	}
	return query, nil
}

func limitedInts(values []int, limit int) []int {
	if limit <= 0 || len(values) == 0 {
		return nil
	}
	seen := make(map[int]bool, len(values))
	out := make([]int, 0, min(len(values), limit))
	for _, value := range values {
		if value < 0 || value > 65535 || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
		if len(out) == limit {
			break
		}
	}
	return out
}

func (s *Server) hydrateDesktopETaggedEvents(ctx context.Context, r *http.Request, ids []string, kinds []int, ttl time.Duration) {
	if s == nil || !s.runtimeCapabilities().DesktopShell || s.nostr == nil || s.store == nil {
		return
	}
	ids = limitedStrings(uniqueNonEmptyStrings(ids), 50)
	kinds = limitedInts(kinds, 8)
	if len(ids) == 0 || len(kinds) == 0 {
		return
	}
	kindKey := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		kindKey = append(kindKey, strconv.Itoa(kind))
	}
	scope := "desktop.etagged." + strings.Join(kindKey, ".")
	stale := make([]string, 0, len(ids))
	for _, id := range ids {
		if s.store.ShouldRefresh(ctx, scope, id, ttl) {
			stale = append(stale, id)
		}
	}
	if len(stale) == 0 {
		return
	}
	relays := nostrx.NormalizeRelayList(append(
		append(append([]string(nil), s.requestRelays(r)...), s.cfg.DefaultRelays...),
		s.cfg.MetadataRelays...,
	), nostrx.MaxRelays)
	if len(relays) == 0 {
		return
	}
	budget := requestTimeout(s.cfg.RequestTimeout)
	if budget <= 0 || budget > 8*time.Second {
		budget = 8 * time.Second
	}
	fetchCtx, cancel := context.WithTimeout(ctx, budget)
	events, err := s.nostr.FetchFrom(fetchCtx, relays, nostrx.Query{
		Kinds: kinds,
		Tags:  map[string][]string{"e": stale},
		Limit: nostrx.MaxRelayQueryLimit,
	})
	cancel()
	if err != nil {
		return
	}
	if len(events) > 0 {
		persistCtx, persistCancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, err = s.store.SaveEvents(persistCtx, events)
		persistCancel()
		if err != nil {
			return
		}
	}
	for _, id := range stale {
		s.store.MarkRefreshed(ctx, scope, id)
	}
}

// handleDesktopReplaceable exposes the sidecar's single durable copy of the
// small replaceable records the renderer needs for controls such as bookmarks,
// mute lists, relay preferences, and account metadata.
func (s *Server) handleDesktopReplaceable(w http.ResponseWriter, r *http.Request) {
	if s == nil || !s.runtimeCapabilities().DesktopShell || s.store == nil {
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
	kind, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("kind")))
	if err != nil || !desktopReplaceableKind(kind) {
		http.Error(w, "unsupported replaceable kind", http.StatusBadRequest)
		return
	}
	key := pubkey + ":" + strconv.Itoa(kind)
	refresh := r.URL.Query().Get("refresh") == "1"
	if refresh || s.store.ShouldRefresh(r.Context(), "desktop.replaceable", key, 5*time.Minute) {
		relays := s.authorMetadataRelays(r.Context(), pubkey, s.requestRelays(r))
		refreshed := false
		if s.nostr != nil && len(relays) > 0 {
			ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
			fetched, fetchErr := s.nostr.FetchFrom(ctx, relays, nostrx.Query{
				Authors: []string{pubkey},
				Kinds:   []int{kind},
				Limit:   3,
			})
			cancel()
			if fetchErr == nil {
				refreshed = true
				if len(fetched) > 0 {
					persistCtx, persistCancel := context.WithTimeout(context.Background(), 30*time.Second)
					_, saveErr := s.store.SaveEvents(persistCtx, fetched)
					persistCancel()
					refreshed = saveErr == nil
				}
			}
		}
		if refreshed {
			s.store.MarkRefreshed(r.Context(), "desktop.replaceable", key)
		}
	}
	event, err := s.store.LatestReplaceable(r.Context(), pubkey, kind)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, event, nil)
}

func desktopReplaceableKind(kind int) bool {
	switch kind {
	case nostrx.KindProfileMetadata, nostrx.KindFollowList, nostrx.KindMuteList,
		nostrx.KindRelayListMetadata, nostrx.KindBookmarkList:
		return true
	default:
		return false
	}
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
	if s == nil || !s.runtimeCapabilities().DesktopShell || s.cfg.DesktopSessionToken == "" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	provided := r.Header.Get("X-Ptxt-Desktop-Token")
	if len(provided) != len(s.cfg.DesktopSessionToken) || subtle.ConstantTimeCompare([]byte(provided), []byte(s.cfg.DesktopSessionToken)) != 1 {
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
	mode := desktopBackgroundPaused
	switch strings.ToLower(strings.TrimSpace(body.Mode)) {
	case "foreground":
		mode = desktopBackgroundForeground
	case "reduced":
		mode = desktopBackgroundReduced
	case "paused":
		mode = desktopBackgroundPaused
	case "":
		if body.Active == nil {
			http.Error(w, "activity mode required", http.StatusBadRequest)
			return
		}
		if *body.Active {
			mode = desktopBackgroundForeground
		}
	default:
		http.Error(w, "invalid activity mode", http.StatusBadRequest)
		return
	}
	previous := s.setDesktopBackgroundMode(mode)
	if mode == desktopBackgroundForeground && previous != desktopBackgroundForeground && s.cfg.ViewerCrawlerEnabled {
		_ = s.runBackgroundUserAsync(s.crawlViewerTick)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// handleDesktopFollowGraph refreshes both the profile's kind-3 event and a
// bounded reverse-follower sample through the sidecar, then returns the graph
// projected into SQLite on this device.
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
		type followFetchResult struct {
			events []nostrx.Event
			err    error
		}
		results := make(chan followFetchResult, 2)
		go func() {
			events, err := s.nostr.FetchFrom(ctx, relays, nostrx.Query{
				Authors: []string{pubkey}, Kinds: []int{nostrx.KindFollowList}, Limit: 3,
			})
			results <- followFetchResult{events: events, err: err}
		}()
		go func() {
			events, err := s.nostr.FetchFrom(ctx, relays, nostrx.Query{
				Kinds: []int{nostrx.KindFollowList},
				Tags:  map[string][]string{"p": {pubkey}},
				Limit: nostrx.MaxRelayQueryLimit,
			})
			results <- followFetchResult{events: events, err: err}
		}()
		var fetched []nostrx.Event
		for range 2 {
			result := <-results
			if result.err == nil {
				fetched = append(fetched, result.events...)
			}
		}
		cancel()
		if len(fetched) > 0 {
			persistCtx, persistCancel := context.WithTimeout(context.Background(), 30*time.Second)
			_, _ = s.store.SaveEvents(persistCtx, fetched)
			persistCancel()
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
