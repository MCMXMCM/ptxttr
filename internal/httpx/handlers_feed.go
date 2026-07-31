package httpx

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"ptxt-nstr/internal/nostrx"
)

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	defer s.observe("handler.home", time.Now())
	if r.URL.Path != "/" {
		if redirect, ok := tryNip19Redirect(strings.TrimPrefix(r.URL.Path, "/")); ok {
			if r.URL.RawQuery != "" {
				redirect += "?" + r.URL.RawQuery
			}
			http.Redirect(w, r, redirect, http.StatusFound)
			return
		}
		s.renderNotFound(w, "error_shell", ErrorPageData{
			BasePageData: s.basePageData(r, "Not found", "feed", "feed-shell"),
			ErrorPanelCopy: ErrorPanelCopy{
				Heading: "Page not found",
				Message: "There is nothing at this path.",
				Detail:  r.URL.Path,
			},
		})
		return
	}
	s.renderFeedRoute(w, r)
}

func (s *Server) renderFeedRoute(w http.ResponseWriter, r *http.Request) {
	req := s.feedRequestFromHTTP(r)
	if _, err := nostrx.DecodeIdentifier(req.Pubkey); err == nil && strings.TrimSpace(req.Pubkey) != "" {
		w.Header().Set("Cache-Control", "private, no-store")
	} else if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "public, max-age=60, s-maxage=600, stale-while-revalidate=3600")
	}
	fragment := r.URL.Query().Get("fragment")
	if fragment == "heading" {
		data := s.feedHeadingData(req)
		data.BasePageData = s.basePageData(r, "Nostr Feed", "feed", "feed-shell")
		s.render(w, "feed_heading", data)
		return
	}
	if fragment != "" {
		req.Cursor, _ = strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
		req.CursorID = strings.TrimSpace(r.URL.Query().Get("cursor_id"))
		data := s.feedItemsData(r.Context(), req)
		data.BasePageData = s.basePageData(r, "Nostr Feed", "feed", "feed-shell")
		setPaginationHeaders(w, data.Cursor, data.CursorID, data.HasMore)
		s.render(w, "feed_items", data)
		return
	}
	var data FeedPageData
	if deferGuestLoggedOutFeedFirstPage(req) {
		data = s.homeFeedShellPageData(r.Context(), req)
		if len(data.Feed) == 0 {
			s.scheduleGuestFeedFragmentWarm(req)
		}
	} else {
		data = s.feedData(r.Context(), req)
	}
	data.BasePageData = s.basePageData(r, "Nostr Feed", "feed", "feed-shell")
	if r.URL.Path == "/" {
		data.BasePageData.OG = homeOG(r)
	}
	s.render(w, "home", data)
}

func (s *Server) handleFeed(w http.ResponseWriter, r *http.Request) {
	defer s.observe("handler.feed", time.Now())
	s.renderFeedRoute(w, r)
}

// feedRequestFromHTTP extracts the common feed parameters from the request.
// The viewer identity comes from the X-Ptxt-Viewer header (with `?pubkey=`
// fallback); cursors are left zero so callers can fill those in per route loader.
func (s *Server) feedRequestFromHTTP(r *http.Request) feedRequest {
	pubkey := viewerFromRequest(r)
	seedPubkey := seedPubkeyFromRequest(r)
	_, wotRaw := wotEnabledFromRequest(r)
	_, wotDepthRaw := wotDepthFromRequest(r)
	wot := buildWebOfTrust(wotRaw, wotDepthRaw)
	loggedOut := strings.TrimSpace(pubkey) == ""
	if !loggedOut {
		_, err := nostrx.DecodeIdentifier(pubkey)
		loggedOut = err != nil
	}
	if loggedOut {
		// Anonymous feeds always use the canonical Gigi seed. Hosted requests
		// stay fixed at one hop for shared-cache/resource control; desktop keeps
		// the user's selected depth because all graph work and storage are local.
		wot.Enabled = true
		if !s.cfg.DesktopMode {
			wot.Depth = defaultLoggedOutWOTDepth
		}
		seedPubkey = defaultLoggedOutWOTSeedNPub
	}
	return feedRequest{
		Pubkey:     pubkey,
		SeedPubkey: seedPubkey,
		Limit:      30,
		Relays:     s.requestRelays(r),
		Timeframe:  normalizeTrendingTimeframe(feedTrendingTfFromRequest(r)),
		SortMode:   feedSortForPubkey(pubkey, feedSortFromRequest(r)),
		WoT:        wot,
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	s.renderAppShell(w, r, "Login", "login", false)
}

func (s *Server) handleReads(w http.ResponseWriter, r *http.Request) {
	defer s.observe("handler.reads", time.Now())
	req := s.readsRequestFromHTTP(r)
	data := s.readsData(r.Context(), req, normalizeTrendingTimeframe(readsTrendingTfFromRequest(r)))
	data.BasePageData = s.basePageData(r, "Reads", "reads", "feed-shell")
	if r.URL.Query().Get("fragment") != "" {
		setPaginationHeaders(w, data.Cursor, data.CursorID, data.HasMore)
		s.render(w, "reads_items", data)
		return
	}
	s.render(w, "reads", data)
}

func (s *Server) handleRead(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/reads/")
	if id == "" || strings.Contains(id, "/") {
		s.renderReadNotFound(w, r, "Not found", "That read URL is not valid.")
		return
	}
	relays := s.requestRelays(r)
	event := s.eventByIDEx(r.Context(), id, relays, true)
	if event == nil {
		s.renderReadNotFound(w, r, "Read not found", "No long-form note with this id was found in the local cache or on the relays you selected.")
		return
	}
	data := s.readDetailData(r.Context(), *event, relays, true)
	data.BasePageData = s.basePageData(r, data.Read.Title, "reads", "feed-shell")
	s.render(w, "read", data)
}

func (s *Server) renderReadNotFound(w http.ResponseWriter, r *http.Request, heading, message string) {
	s.renderNotFound(w, "error_shell", ErrorPageData{
		BasePageData: s.basePageData(r, "Read", "reads", "feed-shell"),
		ErrorPanelCopy: ErrorPanelCopy{
			Heading:          heading,
			Message:          message,
			AppShellClass:    "app-shell reads-shell",
			MainSectionClass: "feed-column reads-column read-detail-column route-error-column",
			ShowReadsBack:    true,
		},
	})
}

func (s *Server) handleBookmarks(w http.ResponseWriter, r *http.Request) {
	defer s.observe("handler.bookmarks", time.Now())
	pubkey := normalizedViewerPubkey(viewerFromRequest(r))
	if pubkey != "" {
		w.Header().Set("Cache-Control", "private, no-store")
	} else if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "public, max-age=60, s-maxage=600, stale-while-revalidate=3600")
	}
	relays := s.requestRelays(r)
	if isBotLikeRequest(r) {
		relays = nil
	}
	data := s.bookmarksData(r.Context(), pubkey, relays)
	data.BasePageData = s.basePageData(r, "Bookmarks", "bookmarks", "feed-shell")
	if r.URL.Query().Get("fragment") != "" {
		s.render(w, "bookmarks_items", data)
		return
	}
	s.render(w, "bookmarks", data)
}

func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	defer s.observe("handler.notifications", time.Now())
	pubkey := normalizedViewerPubkey(viewerFromRequest(r))
	if pubkey != "" {
		w.Header().Set("Cache-Control", "private, no-store")
	} else if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "public, max-age=60, s-maxage=600, stale-while-revalidate=3600")
	}
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	cursorID := strings.TrimSpace(r.URL.Query().Get("cursor_id"))
	relays := s.requestRelays(r)
	refreshFromRelays := pubkey != "" && !isBotLikeRequest(r)
	if !refreshFromRelays {
		relays = nil
	}
	data := s.notificationsData(r.Context(), pubkey, seedPubkeyFromRequest(r), relays, cursor, cursorID, refreshFromRelays, webOfTrustOptionsFromRequest(r))
	data.BasePageData = s.basePageData(r, "Notifications", "notifications", "feed-shell")
	if r.URL.Query().Get("fragment") != "" {
		setPaginationHeaders(w, data.Cursor, data.CursorID, data.HasMore)
		s.render(w, "notifications_items", data)
		return
	}
	s.render(w, "notifications", data)
}

func (s *Server) handleAbout(w http.ResponseWriter, r *http.Request) {
	data := AboutPageData{
		BasePageData: s.basePageData(r, "About", "about", "feed-shell"),
	}
	data.HideTrendingRail = true
	s.render(w, "about", data)
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	s.renderAppShell(w, r, "Settings", "settings", true)
}

func (s *Server) handleEditProfile(w http.ResponseWriter, r *http.Request) {
	s.renderAppShell(w, r, "Edit profile", "settings", true)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	defer s.observe("handler.search", time.Now())
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	data := s.searchPageData(r.Context(), r)
	if r.URL.Query().Get("fragment") != "" {
		setPaginationHeaders(w, data.Cursor, data.CursorID, data.HasMore)
		if data.Mode == searchModeUsers {
			s.render(w, "search_user_items", data)
		} else {
			s.render(w, "search_note_items", data)
		}
		return
	}
	s.render(w, "search", data)
}

func (s *Server) handleTrending(w http.ResponseWriter, r *http.Request) {
	timeframe := normalizeTrendingTimeframe(feedTrendingTfFromRequest(r))
	req := s.feedRequestFromHTTP(r)
	resolved := s.resolveRequestAuthors(r.Context(), req.Pubkey, req.SeedPubkey, req.Relays, req.WoT)
	trendCohort, trendAuthors := resolved.trendingScope()
	trending := s.trendingData(r.Context(), timeframe, trendCohort, trendAuthors, s.requestRelays(r), false)
	events := make([]nostrx.Event, 0, len(trending))
	for _, item := range trending {
		events = append(events, item.Event)
	}
	data := FeedPageData{
		Trending:          trending,
		TrendingTimeframe: timeframe,
		Profiles:          s.profilesFor(r.Context(), events),
	}
	viewerPub, _ := s.resolveViewer(viewerFromRequest(r), s.requestRelays(r))
	data.ReactionTotals, data.ReactionViewers = s.reactionMapsForEvents(r.Context(), events, viewerPub)
	s.render(w, "trending_list", data)
}

func (s *Server) handleRelays(w http.ResponseWriter, r *http.Request) {
	relays := s.requestRelays(r)
	data := RelaysPageData{
		BasePageData: s.basePageData(r, "Relays", "relays", "feed-shell"),
		Relays:       relays,
	}
	var suggested []string
	if pubkey, err := nostrx.DecodeIdentifier(viewerFromRequest(r)); err == nil {
		if !s.shareServerMode() {
			s.refreshAuthor(r.Context(), pubkey, relays)
		}
		suggested = s.userRelays(r.Context(), pubkey)
	}
	statuses, _ := s.store.RelayStatuses(r.Context())
	data.RelayStatuses = statuses
	data.SuggestedRelays = suggested
	s.renderAppShell(w, r, "Relays", "relays", true)
}

func normalizedViewerPubkey(raw string) string {
	pubkey, err := nostrx.DecodeIdentifier(raw)
	if err != nil {
		return ""
	}
	return pubkey
}
