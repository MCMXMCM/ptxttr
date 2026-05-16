package httpx

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
	"ptxt-nstr/internal/thread"
)

// noteIDsFromQuery collects up to maxN deduplicated note ids from repeated ?id=
// and comma-separated values. When canonical is true, each token is passed
// through nostrx.CanonicalHex64 (invalid tokens become empty and are skipped).
func noteIDsFromQuery(r *http.Request, maxN int, canonical bool) []string {
	if r == nil || maxN <= 0 {
		return nil
	}
	rawIDs := r.URL.Query()["id"]
	ids := make([]string, 0, len(rawIDs))
	seen := make(map[string]struct{}, maxN)
	for _, rawID := range rawIDs {
		for _, id := range strings.Split(rawID, ",") {
			id = strings.TrimSpace(id)
			if canonical {
				id = nostrx.CanonicalHex64(id)
			}
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
			if len(ids) >= maxN {
				return ids
			}
		}
		if len(ids) >= maxN {
			return ids
		}
	}
	return ids
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
	profiles := s.contactProfiles(ctx, pubkeys)
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

func (s *Server) handleProfileAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	pubkey, err := nostrx.DecodeIdentifier(viewerFromRequest(r))
	if err != nil || pubkey == "" {
		writeJSON(w, nil, httpError("valid pubkey is required", http.StatusBadRequest))
		return
	}
	profile := s.profile(r.Context(), pubkey)
	if event, err := s.store.LatestReplaceable(r.Context(), pubkey, nostrx.KindProfileMetadata); err == nil && event != nil {
		profile = nostrx.ParseProfile(pubkey, event)
	} else if s.store.ShouldRefresh(r.Context(), "author", pubkey, 10*time.Minute) {
		// Explicit profile lookup: allow an on-demand refresh on cache miss.
		s.refreshAuthor(r.Context(), pubkey, s.requestRelays(r))
		if refreshed, refreshErr := s.store.LatestReplaceable(r.Context(), pubkey, nostrx.KindProfileMetadata); refreshErr == nil && refreshed != nil {
			profile = nostrx.ParseProfile(pubkey, refreshed)
		}
	}
	content := ""
	eventID := ""
	if profile.Event != nil {
		content = profile.Event.Content
		eventID = profile.Event.ID
	}
	writeJSON(w, map[string]any{
		"pubkey":       profile.PubKey,
		"name":         profile.Name,
		"display_name": profile.Display,
		"about":        profile.About,
		"picture":      profile.Picture,
		"website":      profile.Website,
		"nip05":        profile.NIP05,
		"event_id":     eventID,
		"content":      content,
	}, nil)
}

func (s *Server) handleRelayInsightAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	pubkey, err := nostrx.DecodeIdentifier(viewerFromRequest(r))
	if err != nil || pubkey == "" {
		writeJSON(w, nil, httpError("valid pubkey is required", http.StatusBadRequest))
		return
	}
	relays := s.requestRelays(r)
	if s.store.ShouldRefresh(r.Context(), "author", pubkey, 10*time.Minute) {
		s.refreshAuthor(r.Context(), pubkey, relays)
	}
	writeJSON(w, s.buildRelayInsight(r.Context(), pubkey, relays), nil)
}

type mentionCandidate struct {
	PubKey string   `json:"pubkey"`
	Name   string   `json:"name"`
	NPub   string   `json:"npub"`
	NRef   string   `json:"nref"`
	Relays []string `json:"relays,omitempty"`
	Source string   `json:"source"`
}

func (s *Server) handleMentionsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	pubkey, err := nostrx.DecodeIdentifier(viewerFromRequest(r))
	if err != nil || pubkey == "" {
		writeJSON(w, nil, httpError("valid pubkey is required", http.StatusBadRequest))
		return
	}
	rootID := strings.TrimSpace(r.URL.Query().Get("root_id"))
	relays := s.requestRelays(r)

	contactKeys, relayHints := s.mentionContactsAndRelays(r.Context(), pubkey)
	contactProfiles := s.contactProfiles(r.Context(), contactKeys)
	candidates := make([]mentionCandidate, 0, len(contactKeys)+32)
	sortKeys := make([]string, 0, len(contactKeys)+32)
	seen := make(map[string]bool, len(contactKeys)+32)
	appendCandidate := func(source string, key string, profile nostrx.Profile, relays []string) {
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		name := authorLabel(contactProfiles, key)
		if profile.PubKey != "" {
			name = nostrx.DisplayName(profile)
		}
		normalizedRelays := nostrx.NormalizeRelayList(relays, nostrx.MaxRelays)
		npub := nostrx.EncodeNPub(key)
		nref := npub
		if len(normalizedRelays) > 0 {
			if encoded := nostrx.EncodeNProfile(key, normalizedRelays); encoded != "" {
				nref = encoded
			}
		}
		candidates = append(candidates, mentionCandidate{
			PubKey: key,
			Name:   name,
			NPub:   npub,
			NRef:   nref,
			Relays: normalizedRelays,
			Source: source,
		})
		sortKeys = append(sortKeys, strings.ToLower(name))
	}
	for _, key := range contactKeys {
		appendCandidate("contact", key, contactProfiles[key], relayHints[key])
	}
	if rootID != "" {
		threadKeys := s.threadMentionPubKeys(r.Context(), rootID, relays)
		threadProfiles := s.contactProfiles(r.Context(), threadKeys)
		for _, key := range threadKeys {
			appendCandidate("thread", key, threadProfiles[key], nil)
		}
	}
	indices := make([]int, len(candidates))
	for i := range indices {
		indices[i] = i
	}
	sort.SliceStable(indices, func(i, j int) bool {
		a, b := indices[i], indices[j]
		if sortKeys[a] == sortKeys[b] {
			return candidates[a].PubKey < candidates[b].PubKey
		}
		return sortKeys[a] < sortKeys[b]
	})
	sorted := make([]mentionCandidate, len(candidates))
	for i, idx := range indices {
		sorted[i] = candidates[idx]
	}
	writeJSON(w, map[string]any{
		"pubkey":     pubkey,
		"root_id":    rootID,
		"candidates": sorted,
	}, nil)
}

// threadMentionPubKeys returns up to mentionThreadParticipantLimit distinct
// author pubkeys participating in the thread that contains rootID. It uses the
// store-backed root_id index so we don't hydrate hundreds of reply events just
// to harvest authors. Falls back to fetching the selected event when the root
// id has not been indexed yet (e.g. a thread the viewer just navigated to).
func (s *Server) threadMentionPubKeys(ctx context.Context, rootID string, relays []string) []string {
	const mentionThreadParticipantLimit = 250
	if rootID == "" {
		return nil
	}
	authors, err := s.store.DistinctAuthorsUnderRoot(ctx, rootID, mentionThreadParticipantLimit)
	if err != nil {
		slog.Warn("mentions: distinct authors under root failed", "root", short(rootID), "err", err)
	}
	if len(authors) > 0 {
		return authors
	}
	selected := s.eventByID(ctx, rootID, relays)
	if selected == nil {
		return nil
	}
	actualRootID := thread.RootID(*selected)
	if actualRootID == "" {
		actualRootID = selected.ID
	}
	if actualRootID != rootID {
		fallback, err := s.store.DistinctAuthorsUnderRoot(ctx, actualRootID, mentionThreadParticipantLimit)
		if err != nil {
			slog.Warn("mentions: distinct authors under canonical root failed", "root", short(actualRootID), "err", err)
		}
		if len(fallback) > 0 {
			return fallback
		}
	}
	return []string{selected.PubKey}
}

// mentionContactsAndRelays returns the viewer's contact list plus a relay-hint
// map keyed by pubkey, loading the follow-list event at most once.
func (s *Server) mentionContactsAndRelays(ctx context.Context, pubkey string) ([]string, map[string][]string) {
	const mentionFollowLimit = 600
	event, err := s.store.LatestReplaceable(ctx, pubkey, nostrx.KindFollowList)
	if err != nil {
		slog.Warn("mentions: load follow-list event failed", "pubkey", short(pubkey), "err", err)
	}
	contacts, err := s.store.FollowingPubkeys(ctx, pubkey, mentionFollowLimit)
	if err != nil {
		slog.Warn("mentions: load following pubkeys failed", "pubkey", short(pubkey), "err", err)
	}
	if err != nil || len(contacts) == 0 {
		contacts = nostrx.FollowPubkeys(event)
	}
	hints := nostrx.FollowRelayHints(event, 4000)
	relayHints := make(map[string][]string, len(hints))
	for _, hint := range hints {
		relayHints[hint.PubKey] = append(relayHints[hint.PubKey], hint.Relay)
	}
	for key, relays := range relayHints {
		relayHints[key] = nostrx.NormalizeRelayList(relays, nostrx.MaxRelays)
	}
	return contacts, relayHints
}

// handleEventAPI returns a single Nostr event as JSON keyed by its hex id.
// The response is fully content-addressed and immutable (events are signed
// and not editable), so we return an aggressive `immutable` Cache-Control so
// CloudFront and viewer browsers can cache the bytes effectively forever.
//
// Cache misses are short-cached so a 404 doesn't keep the renderer hot when
// crawlers ping random ids; clients (or replay later) can re-request.
func (s *Server) handleEventAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	raw := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/event/"))
	id := strings.ToLower(thread.NormalizeHexEventID(raw))
	if !isBare64Hex(id) {
		writeJSON(w, nil, httpError("invalid event id", http.StatusBadRequest))
		return
	}
	if matchesETag(r, id) {
		writeNotModifiedImmutable(w, id)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout(s.cfg.RequestTimeout))
	defer cancel()
	event := s.eventFromStore(ctx, id)
	if event == nil {
		setNegativeCache(w)
		writeJSON(w, nil, httpError("event not found", http.StatusNotFound))
		return
	}
	setImmutableCache(w, id)
	writeJSON(w, map[string]any{"event": event}, nil)
}

func (s *Server) handleBookmarksAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	pubkey, err := nostrx.DecodeIdentifier(viewerFromRequest(r))
	if err != nil || pubkey == "" {
		writeJSON(w, nil, httpError("valid pubkey is required", http.StatusBadRequest))
		return
	}
	event := s.bookmarksEvent(r.Context(), pubkey, s.requestRelays(r))
	entries := nostrx.BookmarkEntries(event, maxBookmarkItems)
	ids := make([]string, len(entries))
	payloadEntries := make([]map[string]string, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
		payloadEntries[i] = map[string]string{"id": entry.ID, "relay": entry.Relay}
	}
	eventID := ""
	var createdAt int64
	if event != nil {
		eventID = event.ID
		createdAt = event.CreatedAt
	}
	writeJSON(w, map[string]any{
		"pubkey":     pubkey,
		"event_id":   eventID,
		"created_at": createdAt,
		"ids":        ids,
		"entries":    payloadEntries,
		"count":      len(ids),
	}, nil)
}

func (s *Server) handleMuteListAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	pubkey, err := nostrx.DecodeIdentifier(viewerFromRequest(r))
	if err != nil || pubkey == "" {
		writeJSON(w, nil, httpError("valid pubkey is required", http.StatusBadRequest))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout(s.cfg.RequestTimeout))
	defer cancel()
	mutedPubkeys, err := s.store.MutedPubkeys(ctx, pubkey, nostrx.MaxMuteListTagRows)
	if err != nil {
		slog.Warn("mute-list: MutedPubkeys failed", "pubkey", short(pubkey), "err", err)
		writeJSON(w, nil, httpError("mute list unavailable", http.StatusServiceUnavailable))
		return
	}
	writeJSON(w, map[string]any{
		"pubkey":        pubkey,
		"muted_pubkeys": mutedPubkeys,
	}, nil)
}
