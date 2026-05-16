package httpx

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
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
	req := s.feedRequestFromHTTP(r)
	base := s.basePageData(r, "Nostr Feed", "feed", "feed-shell")
	switch r.URL.Query().Get("fragment") {
	case "1":
		s.renderFeedItemsFragment(w, r.Context(), base, req)
		return
	case "heading":
		s.renderFeedHeadingFragment(w, base, req)
		return
	}
	base.OG = homeOG(r)
	data := s.homeOrFeedDocumentData(r.Context(), req)
	data.BasePageData = base
	s.render(w, "home", data)
}

func (s *Server) handleFeed(w http.ResponseWriter, r *http.Request) {
	defer s.observe("handler.feed", time.Now())
	req := s.feedRequestFromHTTP(r)
	base := s.basePageData(r, "Nostr Feed", "feed", "feed-shell")
	switch r.URL.Query().Get("fragment") {
	case "newer":
		since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
		newerReq := req
		newerReq.Cursor = since
		newerReq.CursorID = r.URL.Query().Get("since_id")
		count := s.feedNewerCount(r.Context(), newerReq)
		w.Header().Set("X-Ptxt-New-Count", strconv.Itoa(count))
		if r.URL.Query().Get("body") != "1" || count == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		newer := s.feedDataNewer(r.Context(), newerReq)
		newer.BasePageData = base
		s.render(w, "feed_items", newer)
		return
	case "1":
		cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
		req.Cursor = cursor
		req.CursorID = r.URL.Query().Get("cursor_id")
		s.renderFeedItemsFragment(w, r.Context(), base, req)
		return
	case "heading":
		s.renderFeedHeadingFragment(w, base, req)
		return
	}
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	req.Cursor = cursor
	req.CursorID = r.URL.Query().Get("cursor_id")
	data := s.homeOrFeedDocumentData(r.Context(), req)
	data.BasePageData = base
	s.render(w, "home", data)
}

// homeOrFeedDocumentData serves the full HTML shell for / and /feed when the
// logged-out first-page deferral path applies; otherwise assembles the full SSR
// feed page.
func (s *Server) homeOrFeedDocumentData(ctx context.Context, req feedRequest) FeedPageData {
	if deferGuestLoggedOutFeedFirstPage(req) {
		data := s.homeFeedShellPageData(ctx, req)
		s.scheduleGuestFeedFragmentWarm(req)
		return data
	}
	return s.feedData(ctx, req)
}

// feedRequestFromHTTP extracts the common feed parameters from the request.
// The viewer identity comes from the X-Ptxt-Viewer header (with `?pubkey=`
// fallback); cursors are left zero so callers can fill those in per fragment.
func (s *Server) feedRequestFromHTTP(r *http.Request) feedRequest {
	pubkey := viewerFromRequest(r)
	seedPubkey := seedPubkeyFromRequest(r)
	wotSet, wotRaw := wotEnabledFromRequest(r)
	wotDepthSet, wotDepthRaw := wotDepthFromRequest(r)
	wot := buildWebOfTrust(wotRaw, wotDepthRaw)
	loggedOut := strings.TrimSpace(pubkey) == ""
	if !loggedOut {
		_, err := nostrx.DecodeIdentifier(pubkey)
		loggedOut = err != nil
	}
	if loggedOut {
		if !wotSet {
			wot.Enabled = true
		}
		if wot.Enabled {
			if !wotDepthSet {
				wot.Depth = defaultLoggedOutWOTDepth
			}
			if seedPubkey == "" {
				seedPubkey = defaultLoggedOutWOTSeedNPub
			}
		}
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

func (s *Server) renderFeedItemsFragment(w http.ResponseWriter, ctx context.Context, base BasePageData, req feedRequest) {
	data := s.feedItemsData(ctx, req)
	data.BasePageData = base
	setFeedPaginationHeaders(w, data)
	s.render(w, "feed_items", data)
}

func (s *Server) renderFeedHeadingFragment(w http.ResponseWriter, base BasePageData, req feedRequest) {
	data := s.feedHeadingData(req)
	data.BasePageData = base
	s.render(w, "feed_heading", data)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	s.render(w, "login", LoginPageData{BasePageData: s.basePageData(r, "Login", "login", "feed-shell")})
}

func (s *Server) handleReads(w http.ResponseWriter, r *http.Request) {
	defer s.observe("handler.reads", time.Now())
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	trendingTimeframe := normalizeTrendingTimeframe(readsTrendingTfFromRequest(r))
	req := s.feedRequestFromHTTP(r)
	// /reads keeps the default as an unscoped firehose unless the caller
	// explicitly opts into WoT (via X-Ptxt-Wot header or ?wot= back-compat).
	wotSet, _ := wotEnabledFromRequest(r)
	if !wotSet {
		req.WoT.Enabled = false
	}
	req.Cursor = cursor
	req.CursorID = r.URL.Query().Get("cursor_id")
	req.Limit = 20
	// /reads has its own sort vocabulary (recent / trend24h / trend7d) that
	// differs from the home feed, so we intentionally bypass feedSortForPubkey
	// (which would default logged-out readers to trend7d on the feed) and use
	// the simpler normalize that defaults to recent.
	req.SortMode = normalizeFeedSort(feedSortFromRequest(r))
	data := s.readsData(r.Context(), req, trendingTimeframe)
	data.BasePageData = s.basePageData(r, "Reads", "reads", "feed-shell")
	switch r.URL.Query().Get("fragment") {
	case "1":
		setPaginationHeaders(w, data.Cursor, data.CursorID, data.HasMore)
		s.render(w, "reads_items", data)
		return
	case "heading":
		s.render(w, "reads_heading", data)
		return
	case "right-rail":
		s.render(w, "reads_right_rail", data)
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
	viewerPub, loggedOut := s.resolveViewer(viewerFromRequest(r), relays)
	allowReadRelay := allowSyncRelayWork(viewerPub, loggedOut)
	event := s.eventByIDEx(r.Context(), id, relays, allowReadRelay)
	if event == nil {
		s.renderReadNotFound(w, r, "Read not found", "No long-form read with this id was found in the local cache or on the relays you selected.")
		return
	}
	if event.Kind != nostrx.KindLongForm {
		http.Redirect(w, r, "/thread/"+id, http.StatusFound)
		return
	}
	data := s.readDetailData(r.Context(), *event, relays, allowReadRelay)
	data.BasePageData = s.basePageData(r, "Read", "reads", "feed-shell")
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
	pubkey := viewerFromRequest(r)
	data := s.bookmarksData(r.Context(), pubkey, s.requestRelays(r))
	data.BasePageData = s.basePageData(r, "Bookmarks", "bookmarks", "feed-shell")
	data.HideTrendingRail = true
	switch r.URL.Query().Get("fragment") {
	case "main":
		s.render(w, "bookmarks_main", data)
		return
	case "1":
		s.render(w, "bookmarks_items", data)
		return
	}
	s.render(w, "bookmarks", data)
}

func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	defer s.observe("handler.notifications", time.Now())
	pubkey := viewerFromRequest(r)
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	cursorID := r.URL.Query().Get("cursor_id")
	refreshFromRelays := cursor == 0 && cursorID == ""
	data := s.notificationsData(r.Context(), pubkey, seedPubkeyFromRequest(r), s.requestRelays(r), cursor, cursorID, refreshFromRelays, webOfTrustOptionsFromRequest(r))
	data.BasePageData = s.basePageData(r, "Notifications", "notifications", "feed-shell")
	data.HideTrendingRail = true
	switch r.URL.Query().Get("fragment") {
	case "main":
		s.render(w, "notifications_main", data)
		return
	case "1":
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
	if r.URL.Query().Get("fragment") == "main" {
		s.render(w, "about_main", data)
		return
	}
	s.render(w, "about", data)
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	userPub, _ := s.resolveViewer(viewerFromRequest(r), s.requestRelays(r))
	data := SettingsPageData{
		BasePageData:       s.basePageData(r, "Settings", "settings", "feed-shell"),
		UserPubKey:         userPub,
		WebOfTrustMaxDepth: store.MaxDepth,
	}
	data.HideTrendingRail = true
	if r.URL.Query().Get("fragment") == "main" {
		s.render(w, "settings_main", data)
		return
	}
	s.render(w, "settings", data)
}

func (s *Server) handleEditProfile(w http.ResponseWriter, r *http.Request) {
	data := SettingsPageData{
		BasePageData:       s.basePageData(r, "Edit profile", "settings", "feed-shell"),
		WebOfTrustMaxDepth: store.MaxDepth,
	}
	if r.URL.Query().Get("fragment") == "main" {
		s.render(w, "edit_profile_main", data)
		return
	}
	s.render(w, "edit_profile", data)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	defer s.observe("handler.search", time.Now())
	req := s.searchRequestFromHTTP(r)
	query := store.PrepareSearch(req.Query)
	if query.Empty() {
		s.metrics.Add("search.input.reject", 1)
		req.Query = ""
		s.renderSearchPage(w, r, 0, s.newSearchPageData(r, req, "", searchScopeAll))
		return
	}
	req.Query = query.Normalized
	rateKeys := []string{"ip:" + searchRemoteIP(r)}
	if viewer := normalizeViewerKey(req.Pubkey); viewer != "" {
		rateKeys = append(rateKeys, "viewer:"+viewer)
	}
	status := 0
	if !s.searchLimiter.allow(time.Now(), rateKeys...) {
		s.metrics.Add("search.ratelimit.deny", 1)
		w.Header().Set("Retry-After", "1")
		status = http.StatusTooManyRequests
	}
	var data SearchPageData
	if status == 0 {
		data = s.searchData(r.Context(), s.newSearchPlan(r.Context(), req, query))
	} else {
		data = s.newSearchPageData(r, req, req.Query, searchScopeAll)
	}
	if status == 0 {
		data.BasePageData = s.basePageData(r, "Search", "search", "feed-shell")
		data.ScopeNetworkURL = s.searchScopeURL(r, req, searchScopeNetwork)
		data.ScopeAllURL = s.searchScopeURL(r, req, searchScopeAll)
		data.HideTrendingRail = true
	}
	s.renderSearchPage(w, r, status, data)
}

func (s *Server) newSearchPageData(r *http.Request, req searchRequest, query, scope string) SearchPageData {
	data := SearchPageData{
		Query:      query,
		Scope:      scope,
		ScopeLabel: searchScopeLabel(scope),
	}
	data.BasePageData = s.basePageData(r, "Search", "search", "feed-shell")
	data.ScopeNetworkURL = s.searchScopeURL(r, req, searchScopeNetwork)
	data.ScopeAllURL = s.searchScopeURL(r, req, searchScopeAll)
	data.HideTrendingRail = true
	return data
}

func (s *Server) renderSearchPage(w http.ResponseWriter, r *http.Request, status int, data SearchPageData) {
	name := "search"
	switch r.URL.Query().Get("fragment") {
	case "1":
		name = "search_items"
		setSearchPaginationHeaders(w, data)
	case "main":
		name = "search_main"
	}
	if status == 0 {
		s.render(w, name, data)
		return
	}
	s.renderStatus(w, status, name, data)
}

func (s *Server) handleTrending(w http.ResponseWriter, r *http.Request) {
	timeframe := normalizeTrendingTimeframe(feedTrendingTfFromRequest(r))
	fragment := strings.TrimSpace(r.URL.Query().Get("fragment"))
	cacheOnly := fragment != ""
	req := s.feedRequestFromHTTP(r)
	resolved := s.resolveRequestAuthors(r.Context(), req.Pubkey, req.SeedPubkey, req.Relays, req.WoT)
	trendCohort, trendAuthors := resolved.trendingScope()
	trending := s.trendingData(r.Context(), timeframe, trendCohort, trendAuthors, s.requestRelays(r), cacheOnly)
	events := make([]nostrx.Event, 0, len(trending))
	for _, item := range trending {
		events = append(events, item.Event)
	}
	data := FeedPageData{
		Trending:          trending,
		TrendingTimeframe: timeframe,
		Profiles:          s.profilesFor(r.Context(), events),
	}
	if fragment == "" {
		viewerPub, _ := s.resolveViewer(viewerFromRequest(r), s.requestRelays(r))
		data.ReactionTotals, data.ReactionViewers = s.reactionMapsForEvents(r.Context(), events, viewerPub)
	}
	s.render(w, "trending_list", data)
}

func (s *Server) handleRelays(w http.ResponseWriter, r *http.Request) {
	relays := s.requestRelays(r)
	var suggested []string
	if pubkey, err := nostrx.DecodeIdentifier(viewerFromRequest(r)); err == nil {
		s.refreshAuthor(r.Context(), pubkey, relays)
		suggested = s.userRelays(r.Context(), pubkey)
	}
	statuses, _ := s.store.RelayStatuses(r.Context())
	data := RelaysPageData{
		BasePageData:    s.basePageData(r, "Relays", "relays", "feed-shell"),
		Relays:          relays,
		RelayStatuses:   statuses,
		SuggestedRelays: suggested,
	}
	if r.URL.Query().Get("fragment") == "suggestions" {
		s.render(w, "relay_suggestions", data)
		return
	}
	if r.URL.Query().Get("fragment") == "main" {
		s.render(w, "relays_main", data)
		return
	}
	s.render(w, "relays", data)
}

func (s *Server) searchRequestFromHTTP(r *http.Request) searchRequest {
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	return searchRequest{
		Pubkey:     viewerFromRequest(r),
		SeedPubkey: seedPubkeyFromRequest(r),
		Query:      strings.TrimSpace(r.URL.Query().Get("q")),
		Scope:      strings.TrimSpace(r.URL.Query().Get("scope")),
		Cursor:     cursor,
		CursorID:   r.URL.Query().Get("cursor_id"),
		Limit:      30,
		Relays:     s.requestRelays(r),
		WoT:        webOfTrustOptionsFromRequest(r),
	}
}

// searchScopeURL builds /search?... links. Viewer pubkey, WoT seed, WoT
// settings and relays are intentionally omitted: the client sends those as
// request headers so the URL stays cache-key-shared across all viewers.
func (s *Server) searchScopeURL(_ *http.Request, req searchRequest, scope string) string {
	values := url.Values{}
	if req.Query != "" {
		values.Set("q", req.Query)
	}
	if scope == searchScopeAll {
		values.Set("scope", searchScopeAll)
	} else {
		values.Set("scope", searchScopeNetwork)
	}
	return "/search?" + values.Encode()
}

func setSearchPaginationHeaders(w http.ResponseWriter, data SearchPageData) {
	setPaginationHeaders(w, data.Cursor, data.CursorID, data.HasMore)
}
