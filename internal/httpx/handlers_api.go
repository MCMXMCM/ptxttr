package httpx

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
	"ptxt-nstr/internal/thread"
)

const sharePreviewWarmTTL = 7 * 24 * time.Hour

func queryTokensFromRequest(r *http.Request, key string, maxN int, normalize func(string) string) []string {
	if r == nil || maxN <= 0 {
		return nil
	}
	rawValues := r.URL.Query()[key]
	values := make([]string, 0, len(rawValues))
	seen := make(map[string]struct{}, maxN)
	for _, raw := range rawValues {
		for _, token := range strings.Split(raw, ",") {
			value := strings.TrimSpace(token)
			if normalize != nil {
				value = normalize(value)
			}
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			values = append(values, value)
			if len(values) >= maxN {
				return values
			}
		}
		if len(values) >= maxN {
			return values
		}
	}
	return values
}

// noteIDsFromQuery collects up to maxN deduplicated note ids from repeated ?id=
// and comma-separated values. When canonical is true, each token is passed
// through nostrx.CanonicalHex64 (invalid tokens become empty and are skipped).
func noteIDsFromQuery(r *http.Request, maxN int, canonical bool) []string {
	var normalize func(string) string
	if canonical {
		normalize = nostrx.CanonicalHex64
	}
	return queryTokensFromRequest(r, "id", maxN, normalize)
}

type reactionStatsRow struct {
	Total  int    `json:"total"`
	Viewer string `json:"viewer"`
}

func (s *Server) handleRelayInfo(w http.ResponseWriter, r *http.Request) {
	url := r.URL.Query().Get("url")
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.RequestTimeout)
	defer cancel()
	info := s.nostr.FetchRelayInfo(ctx, url)
	_ = s.store.SetRelayStatus(ctx, info.URL, info.Error == "", info.Error)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}

func (s *Server) handleReplyCounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ids := noteIDsFromQuery(r, 50, true)
	if len(ids) == 0 {
		writeJSON(w, map[string]int{}, nil)
		return
	}
	counts, err := s.descendantReplyCounts(r.Context(), ids)
	if err != nil {
		counts = make(map[string]int, len(ids))
	}
	writeJSON(w, counts, nil)
}

// handleReactionStats returns per-note reaction totals and the viewer's latest
// vote (+ / - / "") for up to 50 note ids (same query shape as reply-counts).
func (s *Server) handleReactionStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ids := noteIDsFromQuery(r, 50, true)
	if len(ids) == 0 {
		writeJSON(w, map[string]reactionStatsRow{}, nil)
		return
	}
	viewer := viewerFromRequest(r)
	if decoded, err := nostrx.DecodeIdentifier(viewer); err == nil && decoded != "" {
		viewer = decoded
	}
	timeout := requestTimeout(s.cfg.RequestTimeout)
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	stats, viewers, err := s.store.ReactionStatsByNoteIDs(ctx, ids, viewer)
	if err != nil {
		slog.Warn("reaction stats batch failed", "ids", len(ids), "err", err)
		writeJSON(w, map[string]reactionStatsRow{}, nil)
		return
	}
	out := make(map[string]reactionStatsRow, len(ids))
	for _, id := range ids {
		st := stats[id]
		out[id] = reactionStatsRow{
			Total:  st.Total,
			Viewer: viewers[id],
		}
	}
	writeJSON(w, out, nil)
}

type wotAuthorsResponse struct {
	Authors        []string `json:"authors"`
	Cached         bool     `json:"cached"`
	ComputedAtUnix int64    `json:"computed_at"`
}

func (s *Server) handleWoTAuthors(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.allowSharedCacheAPIRequest(w, r, "wot-authors") {
		return
	}
	seed := strings.TrimSpace(r.URL.Query().Get("seed"))
	if seed == "" {
		seed = defaultLoggedOutWOTSeedNPub
	}
	if !isDefaultLoggedOutSeed(seed) {
		writeJSON(w, nil, httpError("anonymous web-of-trust uses the default seed", http.StatusForbidden))
		return
	}
	decoded, err := nostrx.DecodeIdentifier(seed)
	if err != nil || decoded == "" {
		writeJSON(w, nil, httpError("invalid seed", http.StatusBadRequest))
		return
	}
	depth := defaultLoggedOutWOTDepth
	if raw := strings.TrimSpace(r.URL.Query().Get("depth")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			depth = n
		}
	}
	depth = min(3, max(1, depth))
	limit := s.resolvedAuthorLimit(webOfTrustOptions{Enabled: true, Depth: depth})
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = min(limit, n)
		}
	}
	wot := webOfTrustOptions{Enabled: true, Depth: depth}
	if authors, computedAt, ok := s.cachedResolvedAuthors(r.Context(), decoded, wot); ok {
		setShortCache(w, 60)
		writeJSON(w, wotAuthorsResponse{
			Authors:        boundedAuthors(authors, limit),
			Cached:         true,
			ComputedAtUnix: computedAt,
		}, nil)
		return
	}
	setShortCache(w, 30)
	writeJSON(w, wotAuthorsResponse{Authors: []string{}}, nil)
}

func (s *Server) cachedResolvedAuthors(ctx context.Context, viewer string, wot webOfTrustOptions) ([]string, int64, bool) {
	if s == nil {
		return nil, 0, false
	}
	key := resolvedAuthorsCacheKey(viewer, wot)
	now := time.Now()
	if authors, ok := s.resolvedAuthors.get(key, now); ok && len(authors) > 0 {
		return authors, now.Unix(), true
	}
	if s.store == nil {
		return nil, 0, false
	}
	authors, ts, ok, err := s.store.GetResolvedAuthorsDurable(ctx, key)
	if err != nil || !ok || len(authors) == 0 {
		return nil, 0, false
	}
	computed := time.Unix(ts, 0)
	if age := now.Sub(computed); age < 0 || age >= resolvedAuthorsDurableMaxAge {
		return nil, 0, false
	}
	s.resolvedAuthors.put(key, authors, now)
	return authors, ts, true
}

func boundedAuthors(authors []string, limit int) []string {
	if limit <= 0 || len(authors) <= limit {
		return append([]string(nil), authors...)
	}
	return append([]string(nil), authors[:limit]...)
}

func limitedEvents(events []nostrx.Event, limit int) []nostrx.Event {
	if limit <= 0 || len(events) <= limit {
		return events
	}
	return events[:limit]
}

func (s *Server) allowPublicAPIRequest(w http.ResponseWriter, r *http.Request, scope string, viewerKey string) bool {
	rateKeys := []string{"ip:" + searchRemoteIP(r)}
	if scope != "" {
		rateKeys = append(rateKeys, "api:"+scope+":ip:"+searchRemoteIP(r))
	}
	if viewerKey != "" {
		rateKeys = append(rateKeys, "viewer:"+viewerKey)
	}
	if s.searchLimiter.allow(time.Now(), rateKeys...) {
		return true
	}
	w.Header().Set("Retry-After", "1")
	writeJSON(w, nil, httpError("rate limited", http.StatusTooManyRequests))
	return false
}

func (s *Server) allowSharedCacheAPIRequest(w http.ResponseWriter, r *http.Request, scope string) bool {
	key := "api-cache:" + scope + ":ip:" + searchRemoteIP(r)
	if s.searchLimiter.allow(time.Now(), key) {
		return true
	}
	w.Header().Set("Retry-After", "10")
	writeJSON(w, nil, httpError("rate limited", http.StatusTooManyRequests))
	return false
}

func (s *Server) resolveRequestAuthorsForPublicAPI(ctx context.Context, req feedRequest) requestAuthors {
	userPubkey, loggedOut := s.resolveViewer(req.Pubkey, req.Relays)
	if !loggedOut {
		return s.resolveRequestAuthors(ctx, req.Pubkey, req.SeedPubkey, req.Relays, req.WoT)
	}
	wot := req.WoT
	if !wot.Enabled {
		return requestAuthors{loggedOut: true, wotEnabled: false}
	}
	seed := req.SeedPubkey
	if seed == "" {
		seed = defaultLoggedOutWOTSeedNPub
	}
	if !isDefaultLoggedOutSeed(seed) {
		return requestAuthors{loggedOut: true, wotEnabled: false, seedWOTEnabled: false}
	}
	if wot.Depth <= 0 {
		wot.Depth = defaultLoggedOutWOTDepth
	}
	defaultSeed, err := nostrx.DecodeIdentifier(defaultLoggedOutWOTSeedNPub)
	if err != nil || defaultSeed == "" {
		return requestAuthors{loggedOut: true, wotEnabled: false}
	}
	authors, _, ok := s.cachedResolvedAuthors(ctx, defaultSeed, webOfTrustOptions{Enabled: true, Depth: wot.Depth})
	if !ok || len(authors) == 0 {
		return requestAuthors{
			loggedOut:       true,
			userPubkey:      userPubkey,
			wotEnabled:      true,
			seedWOTEnabled:  true,
			wotViewerPubkey: defaultSeed,
			authors:         []string{defaultSeed},
			allAuthors:      []string{defaultSeed},
		}
	}
	return requestAuthors{
		loggedOut:       true,
		userPubkey:      userPubkey,
		wotEnabled:      true,
		seedWOTEnabled:  true,
		wotViewerPubkey: defaultSeed,
		authors:         boundedAuthors(authors, maxFeedAuthors),
		allAuthors:      authors,
	}
}

func (s *Server) handleFeedNotesAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.allowPublicAPIRequest(w, r, "feed-notes", normalizeViewerKey(viewerFromRequest(r))) {
		return
	}
	req := s.feedRequestFromHTTP(r)
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			req.Limit = min(60, n)
		}
	}
	req.Cursor, _ = strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	req.CursorID = strings.TrimSpace(r.URL.Query().Get("cursor_id"))
	data := s.feedItemsData(r.Context(), req)
	setShortCache(w, 30)
	writeJSON(w, map[string]any{
		"notes":             data.Feed,
		"profiles":          data.Profiles,
		"referenced_events": data.ReferencedEvents,
		"reply_counts":      data.ReplyCounts,
		"reaction_totals":   data.ReactionTotals,
		"reaction_viewers":  data.ReactionViewers,
		"has_more":          data.HasMore,
		"cursor":            data.Cursor,
		"cursor_id":         data.CursorID,
		"sort":              data.FeedSort,
		"snapshot_starter":  data.FeedSnapshotStarter,
	}, nil)
}

func (s *Server) handleSearchNotesAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.allowPublicAPIRequest(w, r, "search-notes", normalizeViewerKey(viewerFromRequest(r))) {
		return
	}
	query := store.PrepareSearch(r.URL.Query().Get("q"))
	if query.Empty() {
		writeJSON(w, map[string]any{"notes": []nostrx.Event{}}, nil)
		return
	}
	limit := 30
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = min(60, n)
		}
	}
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	cursorID := strings.TrimSpace(r.URL.Query().Get("cursor_id"))
	req := feedRequest{
		Pubkey:     viewerFromRequest(r),
		SeedPubkey: seedPubkeyFromRequest(r),
		Relays:     s.requestRelays(r),
		WoT:        webOfTrustOptionsFromRequest(r),
	}
	resolved := s.resolveRequestAuthorsForPublicAPI(r.Context(), req)
	scope := normalizeSearchScope(r.URL.Query().Get("scope"), resolved.loggedOut, resolved.wotEnabled)
	var authors []string
	if scope == searchScopeNetwork {
		authors = resolved.authors
	}
	result := s.searchNotesStoreResult(r.Context(), query, scope, authors, cursor, cursorID, limit)
	events := s.hydrateTimelineEvents(r.Context(), result.Events)
	events = s.filterFeedEventsByViewerMutes(r.Context(), resolved.viewerForMuteFilter(), events)
	writeJSON(w, map[string]any{
		"notes":            events,
		"has_more":         result.HasMore,
		"cursor":           result.NextCreatedAt,
		"cursor_id":        result.NextID,
		"oldest_cached_at": result.OldestCachedAt,
		"latest_cached_at": result.LatestCachedAt,
		"scope":            scope,
	}, nil)
}

func (s *Server) searchNotesStoreResult(ctx context.Context, query store.PreparedSearch, scope string, authors []string, cursor int64, cursorID string, limit int) store.SearchNotesResult {
	if query.Empty() || s == nil || s.store == nil {
		return store.SearchNotesResult{}
	}
	key := strings.Join([]string{
		"search",
		scope,
		searchKindsKey,
		authorsCacheKey(authors),
		query.Normalized,
		strconv.FormatInt(cursor, 10),
		cursorID,
		strconv.Itoa(limit),
	}, "|")
	if cached, ok := s.searchStoreCache.get(key, time.Now()); ok {
		s.metrics.Add("search.cache.store.hit", 1)
		return cached
	}
	return s.searchGroup.do(key, func() store.SearchNotesResult {
		if cached, ok := s.searchStoreCache.get(key, time.Now()); ok {
			s.metrics.Add("search.cache.store.hit_after_wait", 1)
			return cached
		}
		s.metrics.Add("search.cache.store.miss", 1)
		result, err := s.store.SearchNoteSummaries(ctx, store.SearchNotesQuery{
			Text:     query,
			Authors:  authors,
			Kinds:    noteTimelineKinds,
			Before:   cursor,
			BeforeID: cursorID,
			Limit:    limit,
		})
		if err != nil {
			slog.Warn("search API store query failed", "scope", scope, "err", err)
			return store.SearchNotesResult{}
		}
		s.searchStoreCache.put(key, result, time.Now())
		return result
	})
}

func (s *Server) newPublicAPITagPlan(ctx context.Context, req tagRequest) tagPlan {
	feedReq := feedRequest{
		Pubkey:     req.Pubkey,
		SeedPubkey: req.SeedPubkey,
		Relays:     req.Relays,
		WoT:        req.WoT,
	}
	resolved := s.resolveRequestAuthorsForPublicAPI(ctx, feedReq)
	scope := normalizeSearchScope(req.Scope, resolved.loggedOut && !resolved.seedWOTEnabled, resolved.wotEnabled)
	var scopedAuthors []string
	if scope == searchScopeNetwork {
		scopedAuthors = resolved.authors
	}
	viewer := resolved.viewerForMuteFilter()
	tagCacheKey := normalizeTagCacheKey(req.Tag)
	storeKey := strings.Join([]string{
		"tag",
		viewer,
		scope,
		searchKindsKey,
		authorsCacheKey(scopedAuthors),
		tagCacheKey,
		strconv.FormatInt(req.Cursor, 10),
		req.CursorID,
		strconv.Itoa(req.Limit),
	}, "|")
	pageKey := storeKey + "|" + req.Tag + "|" + strconv.FormatBool(req.WoT.Enabled) + "|" + strconv.Itoa(req.WoT.Depth) + "|" + hashStringSlice(req.Relays)
	return tagPlan{
		req:           req,
		resolved:      resolved,
		scope:         scope,
		scopedAuthors: scopedAuthors,
		storeKey:      storeKey,
		pageKey:       pageKey,
	}
}

func (s *Server) handleTagNotesAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.allowPublicAPIRequest(w, r, "tag-notes", normalizeViewerKey(viewerFromRequest(r))) {
		return
	}
	tag := normalizeTagCacheKey(r.URL.Query().Get("tag"))
	if tag == "" {
		writeJSON(w, nil, httpError("tag is required", http.StatusBadRequest))
		return
	}
	limit := 30
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = min(60, n)
		}
	}
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	req := tagRequest{
		Pubkey:     viewerFromRequest(r),
		SeedPubkey: seedPubkeyFromRequest(r),
		Tag:        tag,
		Scope:      strings.TrimSpace(r.URL.Query().Get("scope")),
		Cursor:     cursor,
		CursorID:   strings.TrimSpace(r.URL.Query().Get("cursor_id")),
		Limit:      limit,
		Relays:     s.requestRelays(r),
		WoT:        webOfTrustOptionsFromRequest(r),
	}
	plan := s.newPublicAPITagPlan(r.Context(), req)
	result := s.tagStoreResult(r.Context(), plan)
	events := s.hydrateTimelineEvents(r.Context(), result.Events)
	events = s.filterFeedEventsByViewerMutes(r.Context(), plan.resolved.viewerForMuteFilter(), events)
	writeJSON(w, map[string]any{
		"notes":            events,
		"has_more":         result.HasMore,
		"cursor":           result.NextCreatedAt,
		"cursor_id":        result.NextID,
		"oldest_cached_at": result.OldestCachedAt,
		"latest_cached_at": result.LatestCachedAt,
		"scope":            plan.scope,
	}, nil)
}

func (s *Server) handleProfilesAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.allowPublicAPIRequest(w, r, "profiles", "") {
		return
	}
	pubkeys := queryTokensFromRequest(r, "pubkey", 32, func(value string) string {
		decoded, err := nostrx.DecodeIdentifier(value)
		if err != nil {
			return ""
		}
		return decoded
	})
	if len(pubkeys) == 0 {
		setShortCache(w, 60)
		writeJSON(w, map[string]nostrx.Profile{}, nil)
		return
	}
	setShortCache(w, 60)
	writeJSON(w, s.profilesForPubkeys(r.Context(), pubkeys), nil)
}

func (s *Server) handleThreadPreviewAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.allowPublicAPIRequest(w, r, "thread-preview", "") {
		return
	}
	selectedID := thread.NormalizeHexEventID(r.URL.Query().Get("id"))
	if selectedID == "" {
		writeJSON(w, nil, httpError("id is required", http.StatusBadRequest))
		return
	}
	selected := s.eventFromStore(r.Context(), selectedID)
	if selected == nil {
		setNegativeCache(w)
		writeJSON(w, nil, httpError("note not found", http.StatusNotFound))
		return
	}
	lookup := func(id string) *nostrx.Event {
		return s.eventFromStore(r.Context(), thread.NormalizeHexEventID(id))
	}
	rootID := thread.NormalizeHexEventID(r.URL.Query().Get("root"))
	if rootID == "" {
		rootID = resolveThreadRootID(*selected, lookup)
	}
	if rootID == "" {
		rootID = selected.ID
	}
	root := selected
	if rootID != selected.ID {
		if ev := s.eventFromStore(r.Context(), rootID); ev != nil {
			root = ev
		}
	}
	parentID := thread.NormalizeHexEventID(thread.ParentID(rootID, *selected))
	events := []nostrx.Event{*selected}
	if root != nil && root.ID != selected.ID {
		events = append(events, *root)
	}
	if parentID != "" && parentID != rootID && parentID != selected.ID {
		if parent := s.eventFromStore(r.Context(), parentID); parent != nil {
			events = append(events, *parent)
		}
	}
	parentByID := map[string]string{}
	if cache, _, err := s.store.ThreadGraphCache(r.Context(), rootID); err == nil && cache != nil {
		parentByID = cache.ParentByID
		for _, ev := range s.eventsByIDInOrder(r.Context(), limitedStrings(cache.EventIDs, 48), true, nil) {
			events = append(events, ev)
		}
		s.metrics.Add("thread.preview.graph_cache_hit", 1)
	} else {
		if parentID != "" {
			parentByID[selected.ID] = parentID
		}
		s.metrics.Add("thread.preview.graph_cache_miss", 1)
	}
	events = uniqueThreadEvents(events)
	events = limitedEvents(events, 50)
	combined := append([]nostrx.Event(nil), events...)
	rt, rv := s.reactionMapsForEvents(r.Context(), combined, "")
	setShortCache(w, 30)
	writeJSON(w, map[string]any{
		"root_id":          rootID,
		"selected_id":      selected.ID,
		"parent_id":        parentID,
		"events":           events,
		"parent_by_id":     parentByID,
		"profiles":         s.profilesFor(r.Context(), combined),
		"reply_counts":     s.replyCounts(r.Context(), combined),
		"reaction_totals":  rt,
		"reaction_viewers": rv,
	}, nil)
}

type outboxPlanGroupResponse struct {
	Authors []string `json:"authors"`
	Relays  []string `json:"relays"`
}

func (s *Server) handleOutboxPlanAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.allowPublicAPIRequest(w, r, "outbox-plan", "") {
		return
	}
	authors := queryTokensFromRequest(r, "author", 24, func(value string) string {
		decoded, err := nostrx.DecodeIdentifier(value)
		if err != nil {
			return ""
		}
		return decoded
	})
	if len(authors) == 0 {
		setShortCache(w, 60)
		writeJSON(w, map[string]any{"groups": []outboxPlanGroupResponse{}}, nil)
		return
	}
	groups := s.groupAuthorsForOutbox(r.Context(), "", authors, s.requestRelays(r))
	out := make([]outboxPlanGroupResponse, 0, len(groups))
	for _, group := range groups {
		out = append(out, outboxPlanGroupResponse{
			Authors: append([]string(nil), group.authors...),
			Relays:  append([]string(nil), group.relays...),
		})
	}
	setShortCache(w, 60)
	writeJSON(w, map[string]any{"groups": out}, nil)
}

func (s *Server) handleAvatarMetaAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.allowPublicAPIRequest(w, r, "avatar-meta", "") {
		return
	}
	pubkey := avatarPathPubkey(r.URL.Query().Get("pubkey"))
	if pubkey == "" {
		writeJSON(w, nil, httpError("pubkey is required", http.StatusBadRequest))
		return
	}
	profile := s.profile(r.Context(), pubkey)
	upstream := strings.TrimSpace(profile.Picture)
	src := avatarSrcFor(pubkey, upstream)
	out := map[string]any{
		"pubkey":   pubkey,
		"src":      src,
		"upstream": upstream,
		"cached":   false,
	}
	if upstream != "" {
		if entry, ok := s.avatarCache.get(upstream); ok {
			out["cached"] = true
			out["content_type"] = entry.contentType
			out["size"] = len(entry.body)
			out["etag"] = quotedETag(entry.bodyHash)
		}
	}
	setShortCache(w, 300)
	writeJSON(w, out, nil)
}

type reactionsAPIEntry struct {
	Pubkey      string `json:"pubkey"`
	DisplayName string `json:"display_name"`
	Vote        string `json:"vote"`
}

func (s *Server) handleReactionsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	raw := strings.TrimSpace(r.URL.Query().Get("note_id"))
	if raw == "" {
		writeJSON(w, nil, httpError("note_id is required", http.StatusBadRequest))
		return
	}
	noteID := nostrx.CanonicalHex64(raw)
	if len(noteID) != 64 {
		writeJSON(w, nil, httpError("invalid note_id", http.StatusBadRequest))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.RequestTimeout)
	defer cancel()
	target, err := s.store.GetEvent(ctx, noteID)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	if target == nil {
		writeJSON(w, nil, httpError("note not found", http.StatusNotFound))
		return
	}
	if target.Kind != nostrx.KindTextNote && target.Kind != nostrx.KindComment {
		writeJSON(w, nil, httpError("note not found", http.StatusNotFound))
		return
	}
	rows, truncated, err := s.store.ReactionReactorsByNoteID(ctx, noteID, 0)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	pubkeys := make([]string, len(rows))
	for i := range rows {
		pubkeys[i] = rows[i].ReactorPubkey
	}
	profiles := s.contactProfiles(ctx, pubkeys, nil)
	out := make([]reactionsAPIEntry, 0, len(rows))
	for _, row := range rows {
		vote := "up"
		if row.Polarity < 0 {
			vote = "down"
		}
		out = append(out, reactionsAPIEntry{
			Pubkey:      row.ReactorPubkey,
			DisplayName: nostrx.DisplayName(profiles[row.ReactorPubkey]),
			Vote:        vote,
		})
	}
	writeJSON(w, map[string]any{
		"reactions": out,
		"truncated": truncated,
		"limit":     store.MaxReactionReactorsList,
	}, nil)
}

type publishEventRequest struct {
	Event  nostrx.Event `json:"event"`
	Relays []string     `json:"relays"`
}

type publishEventResponse struct {
	EventID    string                      `json:"event_id"`
	Kind       int                         `json:"kind"`
	PubKey     string                      `json:"pubkey"`
	Accepted   int                         `json:"accepted"`
	Rejected   int                         `json:"rejected"`
	Persisted  bool                        `json:"persisted"`
	Planned    []string                    `json:"planned_relays,omitempty"`
	Error      string                      `json:"error,omitempty"`
	RelayStats []nostrx.PublishRelayResult `json:"relay_stats"`
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	const maxBodyBytes = 512 << 10
	body := io.LimitReader(r.Body, maxBodyBytes)
	var payload publishEventRequest
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		writeJSON(w, nil, httpError("invalid JSON payload", http.StatusBadRequest))
		return
	}
	if err := nostrx.ValidateIngestEvent(nostrx.IngestFromHTTPAPI, payload.Event); err != nil {
		writeJSON(w, nil, httpError(err.Error(), http.StatusBadRequest))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.RequestTimeout)
	defer cancel()
	if err := s.validateReactionPublishTarget(ctx, payload.Event); err != nil {
		writeJSON(w, nil, httpError(err.Error(), http.StatusBadRequest))
		return
	}
	if err := s.validateDeletionPublishTarget(ctx, payload.Event); err != nil {
		writeJSON(w, nil, httpError(err.Error(), http.StatusBadRequest))
		return
	}
	relays := s.planPublishRelays(ctx, r, payload.Event, payload.Relays)
	if len(relays) == 0 {
		writeJSON(w, nil, httpError("at least one relay is required", http.StatusBadRequest))
		return
	}
	published, err := s.nostr.PublishTo(ctx, relays, payload.Event)
	if err != nil {
		writeJSON(w, nil, httpError(err.Error(), http.StatusBadRequest))
		return
	}
	if payload.Event.Kind == nostrx.KindBookmarkList && published.AcceptedCount() == 0 {
		fallbackRelays := s.bookmarkPublishFallbackRelays(ctx, payload.Event.PubKey, relays)
		if len(fallbackRelays) > 0 {
			retryPublished, retryErr := s.nostr.PublishTo(ctx, fallbackRelays, payload.Event)
			if retryErr == nil {
				published.Results = append(published.Results, retryPublished.Results...)
			}
		}
	}
	accepted := published.AcceptedCount()
	for _, relayResult := range published.Results {
		lastError := relayResult.Error
		if !relayResult.Accepted && lastError == "" {
			lastError = relayResult.Message
		}
		_ = s.store.SetRelayStatus(ctx, relayResult.RelayURL, relayResult.Accepted, lastError)
	}
	persisted := false
	if accepted > 0 {
		// Do not use the request-scoped ctx here: PublishTo may consume almost all of
		// RequestTimeout while relays respond in parallel, then SaveEvent would fail
		// with context deadline exceeded even though relays accepted the event.
		persistCtx, persistCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer persistCancel()
		if err := s.store.SaveEvent(persistCtx, payload.Event); err != nil {
			// Relays accepted the event; failing the response here would
			// discard a successful publish and trip the client's retry loop
			// against an event that already exists at the relay. Log and
			// surface persisted=false so the caller (and recordPublishedAt
			// on the JS side) still treats this as a publish success and
			// opens the publisher's cache-bust window for /thread, /u, /e.
			slog.Warn("save event after publish failed",
				"id", payload.Event.ID, "kind", payload.Event.Kind, "err", err)
		} else {
			s.invalidateResolvedAuthorsForEvents([]nostrx.Event{payload.Event})
			persisted = true
		}
	}
	response := publishEventResponse{
		EventID:    payload.Event.ID,
		Kind:       payload.Event.Kind,
		PubKey:     payload.Event.PubKey,
		Accepted:   accepted,
		Rejected:   len(published.Results) - accepted,
		Persisted:  persisted,
		Planned:    relays,
		RelayStats: published.Results,
	}
	w.Header().Set("Content-Type", "application/json")
	if accepted == 0 {
		response.Error = summarizeRelayFailures(published.Results)
		w.WriteHeader(http.StatusBadGateway)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	_ = json.NewEncoder(w).Encode(response)
}

func (s *Server) bookmarkPublishFallbackRelays(ctx context.Context, pubkey string, attempted []string) []string {
	seen := make(map[string]bool, len(attempted))
	for _, relay := range attempted {
		seen[relay] = true
	}
	merged := make([]string, 0, len(s.cfg.MetadataRelays)+len(s.cfg.DefaultRelays)+16)
	merged = append(merged, s.cfg.MetadataRelays...)
	merged = append(merged, s.cfg.DefaultRelays...)
	if hints, err := s.store.RelayHintsForPubkeyByUsage(ctx, pubkey, nostrx.RelayUsageWrite); err == nil {
		merged = append(merged, hints...)
	}
	if hints, err := s.store.RelayHintsForPubkeyByUsage(ctx, pubkey, nostrx.RelayUsageAny); err == nil {
		merged = append(merged, hints...)
	}
	candidates := nostrx.NormalizeRelayList(merged, nostrx.MaxRelays*3)
	out := make([]string, 0, nostrx.MaxRelays)
	for _, relay := range candidates {
		if seen[relay] {
			continue
		}
		out = append(out, relay)
		if len(out) >= nostrx.MaxRelays {
			break
		}
	}
	return out
}

func summarizeRelayFailures(results []nostrx.PublishRelayResult) string {
	if len(results) == 0 {
		return "No relay accepted this event."
	}
	var notes []string
	for _, result := range results {
		if result.Accepted {
			continue
		}
		reason := strings.TrimSpace(result.Error)
		if reason == "" {
			reason = strings.TrimSpace(result.Message)
		}
		if reason == "" {
			reason = "rejected without reason"
		}
		notes = append(notes, result.RelayURL+": "+reason)
		if len(notes) >= 3 {
			break
		}
	}
	if len(notes) == 0 {
		return "No relay accepted this event."
	}
	return "No relay accepted this event. " + strings.Join(notes, "; ")
}

func (s *Server) handleRelayInsightAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	target := viewerFromRequest(r)
	if strings.TrimSpace(target) == "" {
		target = seedPubkeyFromRequest(r)
	}
	pubkey, err := nostrx.DecodeIdentifier(target)
	if err != nil || pubkey == "" {
		writeJSON(w, nil, httpError("valid pubkey is required", http.StatusBadRequest))
		return
	}
	relays := s.requestRelays(r)
	sessionRelays := nostrx.NormalizeRelayList(nostrx.ParseRelayParams(relayParamsFromRequest(r)), nostrx.MaxRelays)
	if !s.shareServerMode() && s.store.ShouldRefresh(r.Context(), "author", pubkey, 10*time.Minute) {
		s.refreshAuthor(r.Context(), pubkey, relays)
	}
	writeJSON(w, s.buildRelayInsight(r.Context(), pubkey, relays, sessionRelays), nil)
}

type sharePreviewWarmRequest struct {
	NoteID string `json:"note_id"`
}

type sharePreviewWarmResponse struct {
	NoteID  string `json:"note_id"`
	Warmed  bool   `json:"warmed"`
	Cached  bool   `json:"cached"`
	Already bool   `json:"already"`
}

func (s *Server) handleSharePreviewWarm(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	viewer, err := nostrx.DecodeIdentifier(viewerFromRequest(r))
	if err != nil || strings.TrimSpace(viewer) == "" {
		writeJSON(w, nil, httpError("login required", http.StatusUnauthorized))
		return
	}
	var payload sharePreviewWarmRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&payload); err != nil {
		writeJSON(w, nil, httpError("invalid JSON payload", http.StatusBadRequest))
		return
	}
	noteID := nostrx.CanonicalHex64(payload.NoteID)
	if noteID == "" {
		writeJSON(w, nil, httpError("invalid note_id", http.StatusBadRequest))
		return
	}
	if cached := s.eventFromStore(r.Context(), noteID); cached != nil {
		already := !s.store.ShouldRefresh(r.Context(), "share_preview", noteID, sharePreviewWarmTTL)
		s.store.MarkRefreshed(r.Context(), "share_preview", noteID)
		writeJSON(w, sharePreviewWarmResponse{
			NoteID:  noteID,
			Warmed:  true,
			Cached:  true,
			Already: already,
		}, nil)
		return
	}
	relays := s.requestRelays(r)
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout(s.cfg.RequestTimeout))
	defer cancel()
	event := s.eventByIDEx(ctx, noteID, relays, true)
	if event == nil {
		writeJSON(w, nil, httpError("note not found", http.StatusNotFound))
		return
	}
	s.store.MarkRefreshed(r.Context(), "share_preview", noteID)
	if s.store.ShouldRefresh(r.Context(), "author", event.PubKey, 10*time.Minute) {
		s.refreshAuthor(ctx, event.PubKey, relays)
	}
	writeJSON(w, sharePreviewWarmResponse{
		NoteID: noteID,
		Warmed: true,
		Cached: true,
	}, nil)
}
