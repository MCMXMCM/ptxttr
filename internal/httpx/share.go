package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image/png"
	"net/http"
	"strings"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
	"ptxt-nstr/internal/thread"
	staticfs "ptxt-nstr/web/static"
)

const (
	shareSurfaceNoteOP      = "note_op"
	shareSurfaceThreadFocus = "thread_focus"
	sharePayloadJSONVersion = 1
	shareMintThrottle       = 1 * time.Second
	shareTouchThrottle      = 1 * time.Hour
)

type ShareRenderPayload struct {
	Version          int                       `json:"version"`
	Surface          string                    `json:"surface"`
	Selected         nostrx.Event              `json:"selected"`
	Parent           *nostrx.Event             `json:"parent,omitempty"`
	ReferencedEvents map[string]nostrx.Event   `json:"referenced_events,omitempty"`
	Profiles         map[string]nostrx.Profile `json:"profiles,omitempty"`
	ReplyCounts      map[string]int            `json:"reply_counts,omitempty"`
	ReactionTotals   map[string]int            `json:"reaction_totals,omitempty"`
	ReactionViewers  map[string]string         `json:"reaction_viewers,omitempty"`
}

type SharePageData struct {
	BasePageData
	Surface          string
	Selected         nostrx.Event
	Parent           *nostrx.Event
	ReferencedEvents map[string]nostrx.Event
	Profiles         map[string]nostrx.Profile
	ReplyCounts      map[string]int
	ReactionTotals   map[string]int
	ReactionViewers  map[string]string
	RootID           string
	LiveThreadURL    string
}

type createShareRequest struct {
	NoteID   string `json:"note_id"`
	Surface  string `json:"surface"`
	RootID   string `json:"root_id,omitempty"`
	ParentID string `json:"parent_id,omitempty"`
}

type createShareResponse struct {
	Token         string `json:"token"`
	URL           string `json:"url"`
	OGImageURL    string `json:"og_image_url"`
	LiveThreadURL string `json:"live_thread_url"`
	HasImage      bool   `json:"has_image"`
}

func normalizeShareSurface(raw string) string {
	switch strings.TrimSpace(raw) {
	case shareSurfaceNoteOP:
		return shareSurfaceNoteOP
	case shareSurfaceThreadFocus:
		return shareSurfaceThreadFocus
	default:
		return ""
	}
}

func shareToken() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func sharePathToken(path, prefix string) string {
	token := strings.TrimSpace(strings.TrimPrefix(path, prefix))
	token = strings.Trim(token, "/")
	if token == "" || strings.ContainsAny(token, "/?#") {
		return ""
	}
	return token
}

func (s *Server) handleCreateShare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	viewer, _ := nostrx.DecodeIdentifier(viewerFromRequest(r))
	viewer = strings.TrimSpace(viewer)
	if viewer != "" {
		if s.store != nil && !s.store.ShouldRefresh(r.Context(), "share_mint", viewer, shareMintThrottle) {
			writeJSON(w, nil, httpError("share rate limit exceeded", http.StatusTooManyRequests))
			return
		}
	} else if !s.allowPublicAPIRequest(w, r, "share-mint", "") {
		return
	}
	var payload createShareRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&payload); err != nil {
		writeJSON(w, nil, httpError("invalid JSON payload", http.StatusBadRequest))
		return
	}
	surface := normalizeShareSurface(payload.Surface)
	if surface == "" {
		writeJSON(w, nil, httpError("invalid surface", http.StatusBadRequest))
		return
	}
	noteID := nostrx.CanonicalHex64(payload.NoteID)
	if noteID == "" {
		writeJSON(w, nil, httpError("invalid note_id", http.StatusBadRequest))
		return
	}
	rootID := thread.NormalizeHexEventID(payload.RootID)
	parentID := thread.NormalizeHexEventID(payload.ParentID)
	relays := s.requestRelays(r)
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout(s.cfg.RequestTimeout))
	defer cancel()
	selected := s.eventFromStore(ctx, noteID)
	if selected == nil {
		selected = s.eventByIDEx(ctx, noteID, relays, true)
	}
	if selected == nil {
		writeJSON(w, nil, httpError("note not found", http.StatusNotFound))
		return
	}
	if rootID == "" {
		rootID = thread.NormalizeHexEventID(probableThreadRootID(*selected))
	}
	if rootID == "" {
		rootID = selected.ID
	}
	if surface == shareSurfaceNoteOP && rootID != selected.ID {
		surface = shareSurfaceThreadFocus
	}
	actualParentID := thread.NormalizeHexEventID(thread.ParentID(rootID, *selected))
	if surface == shareSurfaceThreadFocus && parentID != "" && actualParentID != "" && parentID != actualParentID {
		writeJSON(w, nil, httpError("parent_id does not match note context", http.StatusBadRequest))
		return
	}
	if surface == shareSurfaceThreadFocus && parentID == "" {
		parentID = actualParentID
	}
	var parent *nostrx.Event
	if parentID != "" && parentID != selected.ID {
		parent = s.eventFromStore(ctx, parentID)
		if parent == nil {
			parent = s.eventByIDEx(ctx, parentID, relays, true)
		}
	}
	events := []nostrx.Event{*selected}
	if parent != nil {
		events = append(events, *parent)
	}
	referenced := s.referencedEventsForFromStore(ctx, events)
	if len(referenced) == 0 {
		referenced = s.referencedEventsFor(ctx, events, relays)
	}
	referenceIDs := collectReferencedEventIDs(events)
	statsIDs := uniqueNonEmptyStrings(append([]string{selected.ID, rootID}, referenceIDs...))
	if parent != nil {
		statsIDs = uniqueNonEmptyStrings(append(statsIDs, parent.ID))
	}
	replyCounts, _ := s.descendantReplyCounts(ctx, statsIDs)
	reactionStats, reactionViewers, _ := s.store.ReactionStatsByNoteIDs(ctx, statsIDs, viewer)
	reactionTotals := make(map[string]int, len(reactionStats))
	for id, stat := range reactionStats {
		reactionTotals[id] = stat.Total
	}
	profileEvents := make([]nostrx.Event, 0, len(events)+len(referenced))
	profileEvents = append(profileEvents, events...)
	for _, event := range referenced {
		profileEvents = append(profileEvents, event)
	}
	profiles := s.profilesFor(ctx, profileEvents)
	payloadData := ShareRenderPayload{
		Version:          sharePayloadJSONVersion,
		Surface:          surface,
		Selected:         *selected,
		Parent:           parent,
		ReferencedEvents: referenced,
		Profiles:         sanitizeShareProfiles(profiles),
		ReplyCounts:      replyCounts,
		ReactionTotals:   reactionTotals,
		ReactionViewers:  reactionViewers,
	}
	payloadJSON, err := json.Marshal(payloadData)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	imageURL, _, hasImage := ogSingleImageNote(selected.Content, selected.Tags)
	liveThreadURL := threadSelectHrefForRoot(rootID, selected.ID)
	now := time.Now().Unix()
	var token string
	var insertErr error
	for i := 0; i < 4; i++ {
		token, err = shareToken()
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		insertErr = s.store.CreateShareArtifact(ctx, store.ShareArtifactRecord{
			Token:          token,
			ViewerPubkey:   viewer,
			NoteID:         selected.ID,
			RootID:         rootID,
			ParentID:       parentID,
			Surface:        surface,
			HasImage:       hasImage,
			ImageURL:       imageURL,
			LiveThreadURL:  liveThreadURL,
			PayloadJSON:    string(payloadJSON),
			CreatedAt:      now,
			LastAccessedAt: now,
		})
		if insertErr == nil {
			break
		}
	}
	if insertErr != nil {
		writeJSON(w, nil, insertErr)
		return
	}
	if viewer != "" {
		s.store.MarkRefreshed(r.Context(), "share_mint", viewer)
	}
	shareURL := absoluteURL(r, "/s/"+token)
	ogImageURL := imageURL
	if !hasImage {
		ogImageURL = absoluteURL(r, "/og/share/"+token+".png")
	}
	writeJSON(w, createShareResponse{
		Token:         token,
		URL:           shareURL,
		OGImageURL:    ogImageURL,
		LiveThreadURL: liveThreadURL,
		HasImage:      hasImage,
	}, nil)
}

func sanitizeShareProfiles(in map[string]nostrx.Profile) map[string]nostrx.Profile {
	out := make(map[string]nostrx.Profile, len(in))
	for k, v := range in {
		v.Event = nil
		out[k] = v
	}
	return out
}

func (s *Server) shareArtifactPayload(rec *store.ShareArtifactRecord) (*ShareRenderPayload, error) {
	if rec == nil || rec.PayloadJSON == "" {
		return nil, errors.New("share payload unavailable")
	}
	var payload ShareRenderPayload
	if err := json.Unmarshal([]byte(rec.PayloadJSON), &payload); err != nil {
		return nil, err
	}
	if payload.Version != sharePayloadJSONVersion || payload.Selected.ID == "" {
		return nil, errors.New("share payload invalid")
	}
	return &payload, nil
}

func shareOG(r *http.Request, token string, rec *store.ShareArtifactRecord, payload *ShareRenderPayload) OpenGraphMeta {
	if rec == nil || payload == nil {
		return homeOG(r)
	}
	og := threadOG(r, payload.Selected, payload.Profiles)
	og.URL = absoluteURL(r, "/s/"+token)
	if rec.HasImage && rec.ImageURL != "" {
		og.Image = rec.ImageURL
	} else {
		og.Image = absoluteURL(r, "/og/share/"+token+".png")
	}
	return og
}

func (s *Server) handleSharePage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := sharePathToken(r.URL.Path, "/s/")
	if token == "" {
		setNegativeCache(w)
		http.NotFound(w, r)
		return
	}
	rec, ok, err := s.store.ShareArtifactByToken(r.Context(), token)
	if err != nil {
		http.Error(w, "share unavailable", http.StatusInternalServerError)
		return
	}
	if !ok || rec == nil {
		setNegativeCache(w)
		http.NotFound(w, r)
		return
	}
	payload, err := s.shareArtifactPayload(rec)
	if err != nil {
		setNegativeCache(w)
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	if matchesETag(r, token) {
		writeNotModified(w, token)
		return
	}
	setContentAddressedCache(w, token)
	data := SharePageData{
		BasePageData: BasePageData{
			Title:         firstNonEmpty(shortenForOG(payload.Selected.Content, 60), "Shared note"),
			PageClass:     "share-page",
			AsciiWidth:    s.asciiWidthForRequestWithQuery(r),
			AssetVersion:  staticfs.ReleaseVersion(),
			AssetBasePath: staticfs.VersionedBasePath(),
			OG:            shareOG(r, token, rec, payload),
		},
		Surface:          payload.Surface,
		Selected:         payload.Selected,
		Parent:           payload.Parent,
		ReferencedEvents: payload.ReferencedEvents,
		Profiles:         payload.Profiles,
		ReplyCounts:      payload.ReplyCounts,
		ReactionTotals:   payload.ReactionTotals,
		ReactionViewers:  payload.ReactionViewers,
		RootID:           firstNonEmpty(rec.RootID, probableThreadRootID(payload.Selected)),
		LiveThreadURL:    rec.LiveThreadURL,
	}
	s.touchShareArtifactThrottled(r.Context(), token)
	s.render(w, "share", data)
}

func (s *Server) touchShareArtifactThrottled(ctx context.Context, token string) {
	if s == nil || s.store == nil || token == "" {
		return
	}
	key := "share:" + token
	if !s.store.ShouldRefresh(ctx, "share_touch", key, shareTouchThrottle) {
		return
	}
	if err := s.store.TouchShareArtifact(ctx, token, time.Now().Unix()); err != nil {
		return
	}
	s.store.MarkRefreshed(ctx, "share_touch", key)
}

func (s *Server) handleShareOGImage(w http.ResponseWriter, r *http.Request) {
	defer s.observe("handler.share_og_image", time.Now())
	raw := sharePathToken(strings.TrimSuffix(r.URL.Path, ".png"), "/og/share/")
	if raw == "" {
		setNegativeCache(w)
		http.NotFound(w, r)
		return
	}
	rec, ok, err := s.store.ShareArtifactByToken(r.Context(), raw)
	if err != nil || !ok || rec == nil || rec.HasImage {
		setNegativeCache(w)
		http.NotFound(w, r)
		return
	}
	if matchesETag(r, raw) {
		writeNotModifiedLong(w, raw)
		return
	}
	payload, err := s.shareArtifactPayload(rec)
	if err != nil {
		setNegativeCache(w)
		http.NotFound(w, r)
		return
	}
	data := ogCardData{
		Event:     payload.Selected,
		Profiles:  payload.Profiles,
		Profile:   payload.Profiles[payload.Selected.PubKey],
		Reference: nil,
	}
	if payload.Parent != nil {
		data.Parent = payload.Parent
	}
	if refID := referencedEventID(payload.Selected); refID != "" {
		if ref, ok := payload.ReferencedEvents[refID]; ok {
			refCopy := ref
			data.Reference = &refCopy
		}
	}
	data.Avatar = s.ogAvatarImage(r.Context(), data.Profile)
	img, err := drawOGCard(data)
	if err != nil {
		setNegativeCache(w)
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	setContentAddressedCacheLong(w, raw)
	if r.Method == http.MethodHead {
		return
	}
	if err := png.Encode(w, img); err != nil {
		return
	}
}
