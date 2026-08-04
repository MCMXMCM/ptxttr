package httpx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"ptxt-nstr/internal/bloom"
	"ptxt-nstr/internal/config"
	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
	staticfs "ptxt-nstr/web/static"
)

const (
	maxFeedAuthors            = 80
	maxAuthorsCacheKeyAuthors = 512
	feedTTL                   = 2 * time.Minute
	profileHeadRefreshTTL     = 90 * time.Second
	threadTTL                 = 2 * time.Minute
	trendingLimit             = 10
	trendingCacheLimit        = 200
	trending24h               = "24h"
	trending1w                = "1w"
	loggedInFetchWindow       = 160
	// First-visit fallbacks must fit before ascii.js can measure the center
	// column. The desktop shell can narrow to roughly 53 monospace columns
	// when the participants rail first appears, so keep the cold-load fallback
	// below that boundary. Every viewport replaces these defaults with the exact
	// measured value persisted by ascii.js.
	asciiWidthMobile               = 45
	asciiWidthTablet               = 64
	asciiWidthDesktop              = 52
	asciiWidthUserDesktop          = 52
	loggedOutMaxPerAuthor          = 8
	loggedOutFetchLimit            = 160
	defaultFeedCacheKey            = "firehose"
	readsCacheKey                  = "reads"
	readsFetchLimit                = 120
	feedSortRecent                 = "recent"
	feedSortTrend24h               = "trend24h"
	feedSortTrend7d                = "trend7d"
	scanFeedChunkSize              = 256
	feedMuteTopUpMaxRounds         = 4
	trendingScanLimit              = 4800
	defaultLoggedOutWOTDepth       = 1
	defaultLoggedOutThreadWOTDepth = 3
	defaultLoggedOutWOTSeedNPub    = "npub1dergggklka99wwrs92yz8wdjs952h2ux2ha2ed598ngwu9w7a6fsh9xzpc"

	// Pinned into the logged-out Gigi slice even when the current Gigi kind-3
	// graph does not include it yet.
	defaultLoggedOutPinnedProfilePubkey = "d84d5f410eba46f8ee9c8e56aabf88996f4f0e3b32a8d87d4f07476ffd1bdd5d"
)

var noteTimelineKinds = []int{nostrx.KindTextNote, nostrx.KindRepost}

// allBootstrapSeedPubkeys returns sorted hex pubkeys for every named logged-out seed.
func allBootstrapSeedPubkeys() []string {
	keys := make([]string, 0, len(loggedOutWOTSeedNamesByPubkey))
	for pk := range loggedOutWOTSeedNamesByPubkey {
		if pk != "" {
			keys = append(keys, pk)
		}
	}
	sort.Strings(keys)
	return keys
}

var loggedOutWOTSeedNamesByPubkey = func() map[string]string {
	decoded, err := nostrx.DecodeIdentifier(defaultLoggedOutWOTSeedNPub)
	if err != nil || decoded == "" {
		return map[string]string{}
	}
	return map[string]string{
		decoded: "Gigi",
	}
}()

type webOfTrustOptions struct {
	Enabled bool
	Depth   int
}

type authorMembership struct {
	filter *bloom.Filter
	exact  map[string]struct{}
}

func (m authorMembership) Authors() []string {
	if len(m.exact) == 0 {
		return nil
	}
	authors := make([]string, 0, len(m.exact))
	for author := range m.exact {
		authors = append(authors, author)
	}
	sort.Strings(authors)
	return authors
}

func defaultLoggedOutPinnedPubkeys() []string {
	return []string{defaultLoggedOutPinnedProfilePubkey}
}

func appendDefaultLoggedOutPinnedPubkeys(authors []string) []string {
	return append(authors, defaultLoggedOutPinnedPubkeys()...)
}

// feedRequest bundles the parameters shared by the feed-rendering helpers.
// Carrying them through one struct avoids long signatures duplicated across
// feedData / feedItemsData / feedDataNewer / feedNewerCount.
type feedRequest struct {
	Pubkey     string
	SeedPubkey string
	Cursor     int64
	CursorID   string
	Limit      int
	Relays     []string
	Timeframe  string
	SortMode   string
	WoT        webOfTrustOptions
}

type requestAuthors struct {
	allAuthors      []string
	authors         []string
	userPubkey      string
	wotViewerPubkey string
	loggedOut       bool
	wotEnabled      bool
	seedWOTEnabled  bool
}

// viewerForMuteFilter returns the hex pubkey whose kind-10000 mute list applies
// when filtering notes (signed-in user, or WoT center when userPubkey is empty).
func (a requestAuthors) viewerForMuteFilter() string {
	if strings.TrimSpace(a.userPubkey) != "" {
		return a.userPubkey
	}
	return strings.TrimSpace(a.wotViewerPubkey)
}

// feedQueryAuthors returns the author list used for SQL IN queries and relay
// refresh (clamped WoT prefix). Scan fallbacks still use allAuthors membership.
func (a requestAuthors) feedQueryAuthors() []string {
	return a.authors
}

// cohortAuthors returns the bounded author set used for cohort cache keys and
// trending work. The full WoT graph can be very large; it remains available for
// membership filtering, but background warmers must stay bounded.
func (a requestAuthors) cohortAuthors() []string {
	if a.wotEnabled {
		if len(a.authors) > 0 {
			return a.authors
		}
		return clampAuthors(a.allAuthors)
	}
	return a.authors
}

// trendingScope returns cohort key and authors for WoT-scoped trending sidebars.
func (a requestAuthors) trendingScope() (cohortKey string, authors []string) {
	if !a.wotEnabled {
		return "", nil
	}
	authors = a.cohortAuthors()
	return authorsCacheKey(authors), authors
}

func feedCacheKeyForResolved(resolved requestAuthors) string {
	return authorsCacheKey(resolved.cohortAuthors())
}

func filterFeedEventsToAuthors(events []nostrx.Event, authors []string) []nostrx.Event {
	if len(events) == 0 || len(authors) == 0 {
		return events
	}
	membership := newAuthorMembership(authors)
	out := events[:0]
	for _, ev := range events {
		if membership.Contains(ev.PubKey) {
			out = append(out, ev)
		}
	}
	return out
}

type outboxRouteGroup struct {
	authors []string
	relays  []string
}

func normalizeFeedSort(value string) string {
	switch value {
	case feedSortTrend24h:
		return feedSortTrend24h
	case feedSortTrend7d:
		return feedSortTrend7d
	default:
		return feedSortRecent
	}
}

func feedSortForPubkey(_ string, sortMode string) string {
	sortMode = normalizeFeedSort(sortMode)
	if sortMode != feedSortRecent {
		return sortMode
	}
	return feedSortRecent
}

func feedSortTimeframe(sortMode string) string {
	if normalizeFeedSort(sortMode) == feedSortTrend7d {
		return trending1w
	}
	return trending24h
}

func webOfTrustOptionsFromRequest(r *http.Request) webOfTrustOptions {
	if r == nil {
		return webOfTrustOptions{Depth: 1}
	}
	_, enabledRaw := wotEnabledFromRequest(r)
	_, depthRaw := wotDepthFromRequest(r)
	return buildWebOfTrust(enabledRaw, depthRaw)
}

// buildWebOfTrust assembles the resolved WoT options from the raw strings
// returned by wotEnabledFromRequest / wotDepthFromRequest. Splitting parsing
// from option-construction lets feedRequestFromHTTP read the raw values once
// and reuse them for both the "supplied?" check and the resolved options.
func buildWebOfTrust(enabledRaw, depthRaw string) webOfTrustOptions {
	depth, _ := strconv.Atoi(strings.TrimSpace(depthRaw))
	enabled, _ := config.ParseBool(enabledRaw)
	return webOfTrustOptions{
		Enabled: enabled,
		Depth:   store.ClampDepth(depth),
	}
}

func feedSortSince(sortMode string, now time.Time) int64 {
	return trendingSince(feedSortTimeframe(sortMode), now)
}

func (s *Server) requestRelays(r *http.Request) []string {
	requestRelays := nostrx.ParseRelayParams(relayParamsFromRequest(r))
	return nostrx.NormalizeRelayList(append(append([]string(nil), s.cfg.DefaultRelays...), requestRelays...), nostrx.MaxRelays)
}

func (s *Server) asciiWidthForRequest(r *http.Request) int {
	return requestASCIIWidth(r)
}

func requestASCIIWidth(r *http.Request) int {
	isMobileHint := r.Header.Get("Sec-CH-UA-Mobile") == "?1"
	ua := strings.ToLower(r.UserAgent())
	isTablet := strings.Contains(ua, "ipad") ||
		strings.Contains(ua, "tablet") ||
		strings.Contains(ua, "kindle") ||
		strings.Contains(ua, "silk") ||
		(strings.Contains(ua, "android") && !strings.Contains(ua, "mobile"))
	if isTablet && !isMobileHint {
		if width, ok := asciiWidthFromRequestCookie(r, asciiWidthCookieName); ok {
			return width
		}
		return asciiWidthTablet
	}
	isMobile := isMobileHint || strings.Contains(ua, "mobile") ||
		strings.Contains(ua, "iphone") ||
		strings.Contains(ua, "ipod") ||
		strings.Contains(ua, "android") ||
		strings.Contains(ua, "mobi")
	if isMobile {
		if width, ok := asciiWidthFromRequestCookie(r, asciiWidthCookieName); ok {
			return width
		}
		return asciiWidthMobile
	}
	if width, ok := asciiWidthFromRequestCookie(r, asciiWidthDesktopCookieName); ok {
		return width
	}
	return asciiWidthDesktop
}

const (
	asciiWidthCookieName        = "ptxt_ascii_w"
	asciiWidthDesktopCookieName = "ptxt_ascii_w_desktop"
)

func asciiWidthFromRequestCookie(r *http.Request, name string) (int, bool) {
	cookie, err := r.Cookie(name)
	if err != nil {
		return 0, false
	}
	return parseAsciiWidth(cookie.Value)
}

func parseAsciiWidth(raw string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 32 || n > 160 {
		return 0, false
	}
	return n, true
}

// parseAsciiWQuery returns a client-supplied ASCII column count from ?ascii_w=.
// In-page fetch() often omits Sec-CH-UA-Mobile; client hydration sends this hint so
// fragments match the real viewport width.
func parseAsciiWQuery(r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("ascii_w"))
	if raw == "" {
		return 0, false
	}
	return parseAsciiWidth(raw)
}

func (s *Server) asciiWidthForRequestWithQuery(r *http.Request) int {
	return requestASCIIWidthWithQuery(r)
}

func requestASCIIWidthWithQuery(r *http.Request) int {
	if w, ok := parseAsciiWQuery(r); ok {
		// Older desktop bundles used 120 as a viewport-size guess. Preserve
		// exact measured hints while keeping that legacy sentinel from
		// reintroducing clipped first-paint chrome during rolling deploys.
		if w == 120 && requestASCIIWidth(r) == asciiWidthDesktop {
			return asciiWidthDesktop
		}
		return w
	}
	return requestASCIIWidth(r)
}

func (s *Server) asciiWidthForUserRequestWithQuery(r *http.Request) int {
	width := s.asciiWidthForRequestWithQuery(r)
	if width == asciiWidthDesktop {
		return asciiWidthUserDesktop
	}
	return width
}

func (s *Server) basePageData(r *http.Request, title, active, pageClass string) BasePageData {
	guestSliceV2 := s.cfg.GuestSliceV2Enabled && r != nil &&
		anonymousRequestFromHTTP(r) && guestDocumentPath(r.URL.Path)
	guestGeneration := int64(0)
	if guestSliceV2 {
		guestGeneration = s.currentGuestGeneration(r.Context())
	}
	return BasePageData{
		Title:              title,
		Active:             active,
		PageClass:          pageClass,
		AsciiWidth:         s.asciiWidthForRequestWithQuery(r),
		SearchQuery:        searchQueryFromRequest(r),
		SearchMode:         searchModeFromRequest(r),
		SearchModeNotesURL: searchModeRouteURL(r, searchModeNotes),
		SearchModeUsersURL: searchModeRouteURL(r, searchModeUsers),
		AssetVersion:       staticfs.ReleaseVersion(),
		AssetBasePath:      staticfs.VersionedBasePath(),
		ViewerPubKey:       normalizedViewerPubkey(viewerFromRequest(r)),
		GuestGeneration:    guestGeneration,
		GuestSliceV2:       guestSliceV2,
		DesktopMode:        s.runtimeCapabilities().DesktopShell,
	}
}

// userBasePageData applies the narrower desktop width used by user-profile
// pages while keeping the other base-page fields aligned with basePageData.
func (s *Server) userBasePageData(r *http.Request, title, active, pageClass string) BasePageData {
	guestSliceV2 := s.cfg.GuestSliceV2Enabled && r != nil &&
		anonymousRequestFromHTTP(r) && guestDocumentPath(r.URL.Path)
	guestGeneration := int64(0)
	if guestSliceV2 {
		guestGeneration = s.currentGuestGeneration(r.Context())
	}
	return BasePageData{
		Title:              title,
		Active:             active,
		PageClass:          pageClass,
		AsciiWidth:         s.asciiWidthForUserRequestWithQuery(r),
		SearchQuery:        searchQueryFromRequest(r),
		SearchMode:         searchModeFromRequest(r),
		SearchModeNotesURL: searchModeRouteURL(r, searchModeNotes),
		SearchModeUsersURL: searchModeRouteURL(r, searchModeUsers),
		AssetVersion:       staticfs.ReleaseVersion(),
		AssetBasePath:      staticfs.VersionedBasePath(),
		ViewerPubKey:       normalizedViewerPubkey(viewerFromRequest(r)),
		GuestGeneration:    guestGeneration,
		GuestSliceV2:       guestSliceV2,
		DesktopMode:        s.runtimeCapabilities().DesktopShell,
	}
}

func searchQueryFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.URL.Query().Get("q"))
}

func searchModeFromRequest(r *http.Request) string {
	if r == nil {
		return searchModeNotes
	}
	return normalizeSearchMode(r.URL.Query().Get("mode"))
}

func searchModeRouteURL(r *http.Request, mode string) string {
	values := url.Values{}
	if query := searchQueryFromRequest(r); query != "" {
		values.Set("q", query)
	}
	values.Set("mode", normalizeSearchMode(mode))
	return "/search?" + values.Encode()
}

func (s *Server) feedData(ctx context.Context, req feedRequest) FeedPageData {
	if req.Cursor == 0 && req.CursorID == "" {
		if data, ok := s.tryLoadFeedPageFromDurableSnapshots(ctx, req, true); ok {
			s.scheduleFeedSnapshotPersonalizedRebuild(req)
			return data
		}
	}
	return s.feedPageDataEx(ctx, req, true, feedPageDataOptions{})
}

// feedPageDataOptions tweaks feed assembly for SSR shells and first-paint
// fragments (skip heavy reply/reaction maps; optional guest cache read skip).
type feedPageDataOptions struct {
	lightStatsOnly          bool
	guestCacheReadDisabled  bool // when true, do not read guest feed TTL cache
	guestCacheWriteDisabled bool // when true, do not write guest feed TTL cache
}

func (s *Server) feedPageDataEx(ctx context.Context, req feedRequest, includeTrending bool, opts feedPageDataOptions) FeedPageData {
	resolved := s.resolveRequestAuthors(ctx, req.Pubkey, req.SeedPubkey, req.Relays, req.WoT)
	return s.feedPageDataExResolved(ctx, req, includeTrending, opts, resolved)
}

func (s *Server) feedPageDataExResolved(ctx context.Context, req feedRequest, includeTrending bool, opts feedPageDataOptions, resolved requestAuthors) FeedPageData {
	defer s.observe("feed.page_data", time.Now())
	sortMode := normalizeFeedSort(req.SortMode)
	timeframe := normalizeTrendingTimeframe(req.Timeframe)
	// Logged-out WoT ranked feeds may read cohort cache only on the request path,
	// falling back to global trending_cache when the cohort row is cold.
	cacheOnlyFeedRanking := resolved.loggedOut && resolved.wotEnabled
	// Anonymous right-rail: never sync-recompute trending on the request path.
	cacheOnlyTrending := resolved.loggedOut && resolved.wotEnabled
	guestCacheKey := ""
	if key, ok := s.guestFeedCacheKey(req, resolved, sortMode, timeframe, includeTrending); ok && !opts.guestCacheReadDisabled {
		guestCacheKey = key
		if cached, hit := s.guestFeedCache.get(key, time.Now()); hit {
			if opts.lightStatsOnly {
				s.metrics.Add("feed.guest_items_cache_hit", 1)
			} else {
				s.metrics.Add("feed.guest_page_cache_hit", 1)
			}
			return cached
		}
		if opts.lightStatsOnly {
			s.metrics.Add("feed.guest_items_cache_miss", 1)
		} else {
			s.metrics.Add("feed.guest_page_cache_miss", 1)
		}
	}
	var events []nostrx.Event
	var hasMore bool
	var next int64
	var nextID string
	if sortMode == feedSortRecent {
		cacheKey := feedCacheKeyForResolved(resolved)
		if resolved.loggedOut {
			if resolved.wotEnabled {
				viewer := resolved.wotViewerPubkey
				queryAuthors := resolved.feedQueryAuthors()
				membership := newAuthorMembership(resolved.allAuthors)
				events, hasMore = s.fetchWoTFeedPage(ctx, viewer, queryAuthors, membership, req.Cursor, req.CursorID, req.Limit, req.Relays, "feed", cacheKey)
			} else {
				events, hasMore = s.fetchDefaultFeedPage(ctx, req.Cursor, req.CursorID, req.Limit, req.Relays)
			}
		} else if resolved.wotEnabled {
			queryAuthors := resolved.feedQueryAuthors()
			membership := newAuthorMembership(resolved.allAuthors)
			events, hasMore = s.fetchWoTFeedPage(ctx, resolved.userPubkey, queryAuthors, membership, req.Cursor, req.CursorID, req.Limit, req.Relays, "feed", cacheKey)
		} else {
			events, hasMore = s.fetchAuthorsPage(ctx, resolved.userPubkey, resolved.authors, req.Cursor, req.CursorID, req.Limit, req.Relays, "feed", cacheKey, nil, false)
		}
		if len(events) > req.Limit {
			events = events[:req.Limit]
		}
		if len(events) > 0 {
			last := events[len(events)-1]
			next = last.CreatedAt
			nextID = last.ID
		}
	} else {
		timeframe := feedSortTimeframe(sortMode)
		cohortKey := ""
		var cohort []string
		if resolved.wotEnabled {
			cohort = resolved.cohortAuthors()
		} else if !resolved.loggedOut {
			cohort = resolved.authors
		}
		cohortKey = authorsCacheKey(cohort)
		rankAfter := s.resolveTrendingFeedCursor(ctx, req.Cursor, req.CursorID, timeframe, cohortKey, cohort)
		pageLimit := req.Limit + 1
		window := pageLimit * 4
		if window > loggedInFetchWindow {
			window = loggedInFetchWindow
		}
		trendingKey := feedRefreshKey("feed-"+sortMode, 0, "")
		if cohortKey != "" {
			trendingKey += "|" + cohortKey
		}
		if req.Cursor == 0 && req.CursorID == "" && s.store.ShouldRefresh(ctx, "feed", trendingKey, feedTTL) {
			s.refreshFeedForTrendingAsync(resolved, window, req.Relays, trendingKey, timeframe)
		}
		allowGlobalTrendFallback := resolved.loggedOut && resolved.wotEnabled && isDefaultLoggedOutSeed(req.SeedPubkey)
		muteViewer := resolved.viewerForMuteFilter()
		usedRelayOrdered := false
		var muted map[string]struct{}
		muteBlocked := false
		if muteViewer != "" {
			var muteErr error
			muted, muteErr = s.viewerMutePubkeySet(ctx, muteViewer)
			if muteErr != nil {
				slog.Warn("viewer mutes: MutedPubkeys failed", "viewer", short(muteViewer), "err", muteErr)
				muteBlocked = true
				events = nil
				hasMore = false
			}
		}
		if !muteBlocked {
			for round := 0; round < feedMuteTopUpMaxRounds && len(events) < pageLimit; round++ {
				need := pageLimit - len(events)
				var batch []nostrx.Event
				relayOrdered := false
				batch, hasMore, rankAfter, relayOrdered = s.fetchRankedFeedPage(ctx, cohort, rankAfter, need, sortMode, cacheOnlyFeedRanking, allowGlobalTrendFallback)
				usedRelayOrdered = usedRelayOrdered || relayOrdered
				if muteViewer != "" {
					batch = s.filterEventsByViewerMutedSet(batch, muted)
				}
				events = append(events, batch...)
				if relayOrdered && len(batch) > 0 {
					last := batch[len(batch)-1]
					rankAfter = trendingRankKey{score: int(last.CreatedAt), createdAt: last.CreatedAt, id: last.ID}
				}
				if len(batch) == 0 {
					if !hasMore {
						break
					}
					continue
				}
				if !hasMore {
					break
				}
			}
		}
		if len(events) > req.Limit {
			events = events[:req.Limit]
			hasMore = true
		} else if muteViewer != "" && len(events) == req.Limit && rankAfter.id != "" {
			last := events[len(events)-1]
			if rankAfter.id != last.ID {
				hasMore = true
			}
		}
		if req.Cursor == 0 && req.CursorID == "" && len(events) == 0 {
			s.metrics.Add("feed.ranked.cold_miss", 1)
		}
		if len(events) > 0 {
			if usedRelayOrdered {
				last := events[len(events)-1]
				next, nextID = last.CreatedAt, last.ID
			} else {
				next, nextID = rankedFeedPaginationCursor(s, ctx, events, timeframe, cohortKey, cohort, allowGlobalTrendFallback, rankAfter)
			}
		}
	}
	s.warmFeedEntities(events, req.Relays)
	referenced, combined := s.referencedHydration(ctx, events, req.Relays)
	var reactionTotals map[string]int
	var reactionViewers map[string]string
	var replyCounts map[string]int
	if opts.lightStatsOnly {
		reactionTotals = map[string]int{}
		reactionViewers = map[string]string{}
		replyCounts = map[string]int{}
	} else {
		rt, rv := s.reactionMapsForEvents(ctx, combined, resolved.userPubkey)
		reactionTotals = rt
		reactionViewers = rv
		replyCounts = s.replyCounts(ctx, combined)
	}

	trending := []TrendingNote{}
	profileEvents := append([]nostrx.Event(nil), combined...)
	if includeTrending {
		trendCohort, trendAuthors := resolved.trendingScope()
		trending = s.trendingData(ctx, timeframe, trendCohort, trendAuthors, req.Relays, cacheOnlyTrending)
		for _, item := range trending {
			profileEvents = append(profileEvents, item.Event)
		}
	}

	data := FeedPageData{
		BasePageData:                BasePageData{},
		FeedSort:                    sortMode,
		Feed:                        events,
		ReferencedEvents:            referenced,
		ReplyCounts:                 replyCounts,
		ReactionTotals:              reactionTotals,
		ReactionViewers:             reactionViewers,
		Profiles:                    s.profilesFor(ctx, profileEvents),
		Cursor:                      next,
		CursorID:                    nextID,
		HasMore:                     hasMore,
		UserPubKey:                  resolved.userPubkey,
		UserNPub:                    nostrx.EncodeNPub(resolved.userPubkey),
		DefaultFeed:                 resolved.loggedOut,
		Relays:                      req.Relays,
		WebOfTrustEnabled:           resolved.wotEnabled,
		LoggedOutWOTSeedDisplayName: loggedOutWOTSeedDisplayName(req.SeedPubkey),
		WebOfTrustDepth:             req.WoT.Depth,
		Trending:                    trending,
		TrendingTimeframe:           timeframe,
	}
	// Default seed + non-recent + empty: skip guest cache so the next hit
	// can pick up async hydration. Custom seeds: empty is cohort-scoped truth.
	skipEmptyDefaultSeedCache := cacheOnlyFeedRanking && sortMode != feedSortRecent && req.Cursor == 0 && len(events) == 0
	if guestCacheKey != "" && !skipEmptyDefaultSeedCache && !opts.guestCacheWriteDisabled {
		s.guestFeedCache.put(guestCacheKey, data, time.Now())
	}
	s.maybePersistFeedSnapshots(ctx, req, resolved, &data)
	return data
}

func (s *Server) feedItemsData(ctx context.Context, req feedRequest) FeedPageData {
	if req.Cursor == 0 && req.CursorID == "" {
		if _, err := nostrx.DecodeIdentifier(req.Pubkey); err == nil {
			if data, ok := s.tryLoadFeedPageFromDurableSnapshots(ctx, req, false); ok {
				s.scheduleFeedSnapshotPersonalizedRebuild(req)
				if s.shouldDeferGuestLoggedOutFeedFirstPage(req) {
					s.hydrateFeedStats(ctx, &data, data.UserPubKey)
				}
				return data
			}
		}
		// Canonical logged-out guest first fragment: prefer durable snapshots,
		// then assemble live on the request path when the snapshot is cold so
		// ?fragment=1 does not return an empty body (which leaves the loader up).
		if s.shouldDeferGuestLoggedOutFeedFirstPage(req) && s.isGuestCanonicalSnapshotTarget(req) {
			sm := normalizeFeedSort(req.SortMode)
			data := s.feedHeadingData(req)
			if sm == feedSortTrend24h || sm == feedSortTrend7d {
				s.mergeGuestCanonicalTrendSnapshotIntoShell(ctx, &data, req)
				if len(data.Feed) > 0 {
					s.metrics.Add("feed.snapshot_hit", 1)
					data.FeedSort = sm
					s.hydrateFeedStats(ctx, &data, data.UserPubKey)
					s.scheduleGuestFeedFragmentWarm(req)
					return data
				}
				s.metrics.Add("feed.guest_ranked_fragment_snapshot_miss", 1)
			} else if sm == feedSortRecent && s.isCanonicalDefaultLoggedOutGuestFeedRequest(req) {
				s.mergeGuestCanonicalSnapshotIntoShell(ctx, &data, req)
				if len(data.Feed) > 0 {
					s.metrics.Add("feed.guest_recent_fragment_snapshot_hit", 1)
					s.scheduleGuestFeedFragmentWarm(req)
					return data
				}
				if s.mergePersistedDefaultSeedGuestFeedIntoShell(ctx, &data, req) && len(data.Feed) > 0 {
					s.metrics.Add("feed.guest_recent_fragment_snapshot_hit", 1)
					s.scheduleGuestFeedFragmentWarm(req)
					return data
				}
				s.metrics.Add("feed.guest_recent_fragment_snapshot_miss", 1)
			}
			s.scheduleGuestFeedFragmentWarm(req)
		}
	}
	o := feedPageDataOptions{}
	if s.shouldDeferGuestLoggedOutFeedFirstPage(req) && normalizeFeedSort(req.SortMode) == feedSortRecent {
		o.lightStatsOnly = true
	}
	data := s.feedPageDataEx(ctx, req, false, o)
	if len(data.Feed) > 0 && s.isCanonicalDefaultLoggedOutGuestFeedRequest(req) {
		_ = s.persistDefaultSeedGuestFeedSnapshot(ctx, req, &data)
	}
	if len(data.Feed) == 0 && s.mergePersistedDefaultSeedGuestFeedIntoShell(ctx, &data, req) {
		s.scheduleGuestFeedFragmentWarm(req)
	}
	return data
}

func (s *Server) hydrateFeedStats(ctx context.Context, data *FeedPageData, viewerPubkey string) {
	if s == nil || data == nil || len(data.Feed) == 0 {
		return
	}
	combined := append([]nostrx.Event(nil), data.Feed...)
	if len(data.ReferencedEvents) > 0 {
		for _, event := range data.ReferencedEvents {
			combined = append(combined, event)
		}
	}
	rt, rv := s.reactionMapsForEvents(ctx, combined, viewerPubkey)
	data.ReactionTotals = rt
	data.ReactionViewers = rv
	data.ReplyCounts = s.replyCounts(ctx, combined)
}

func (s *Server) feedHeadingData(req feedRequest) FeedPageData {
	defer s.observe("feed.heading_data", time.Now())
	userPubkey, loggedOut := s.resolveViewer(req.Pubkey, req.Relays)
	seedWOTEnabled := s.isValidSeedViewer(loggedOut, req.WoT, req.SeedPubkey)
	return FeedPageData{
		BasePageData:                BasePageData{},
		UserPubKey:                  userPubkey,
		UserNPub:                    nostrx.EncodeNPub(userPubkey),
		DefaultFeed:                 loggedOut,
		FeedSort:                    normalizeFeedSort(req.SortMode),
		Relays:                      req.Relays,
		WebOfTrustEnabled:           req.WoT.Enabled && (!loggedOut || seedWOTEnabled),
		LoggedOutWOTSeedDisplayName: loggedOutWOTSeedDisplayName(req.SeedPubkey),
		WebOfTrustDepth:             req.WoT.Depth,
	}
}

func (s *Server) feedDataNewer(ctx context.Context, req feedRequest) FeedPageData {
	defer s.observe("feed.data_newer", time.Now())
	sortMode := normalizeFeedSort(req.SortMode)
	if sortMode != feedSortRecent {
		return FeedPageData{FeedSort: sortMode}
	}
	resolved := s.resolveRequestAuthors(ctx, req.Pubkey, req.SeedPubkey, req.Relays, req.WoT)
	var events []nostrx.Event
	if resolved.loggedOut && !resolved.wotEnabled {
		events, _ = s.store.NewerSummariesByKinds(ctx, noteTimelineKinds, req.Cursor, req.CursorID, req.Limit)
	} else if resolved.wotEnabled {
		membership := newAuthorMembership(resolved.allAuthors)
		events = s.scanNewerFeedEvents(ctx, membership, req.Cursor, req.CursorID, req.Limit)
		if len(events) == 0 {
			queryAuthors := resolved.feedQueryAuthors()
			if len(queryAuthors) > 0 {
				events, _ = s.store.NewerSummariesByAuthorsCursor(ctx, queryAuthors, noteTimelineKinds, req.Cursor, req.CursorID, req.Limit)
			}
		}
	} else {
		events, _ = s.store.NewerSummariesByAuthorsCursor(ctx, resolved.authors, noteTimelineKinds, req.Cursor, req.CursorID, req.Limit)
	}
	if v := resolved.viewerForMuteFilter(); v != "" {
		events = s.filterFeedEventsByViewerMutes(ctx, v, events)
	}
	if len(events) > req.Limit {
		events = events[:req.Limit]
	}
	events = s.hydrateTimelineEvents(ctx, events)
	s.warmFeedEntities(events, req.Relays)
	referenced, combined := s.referencedHydration(ctx, events, req.Relays)
	rt, rv := s.reactionMapsForEvents(ctx, combined, resolved.userPubkey)
	return FeedPageData{
		FeedSort:          sortMode,
		Feed:              events,
		ReferencedEvents:  referenced,
		ReplyCounts:       s.replyCounts(ctx, combined),
		ReactionTotals:    rt,
		ReactionViewers:   rv,
		Profiles:          s.profilesFor(ctx, combined),
		UserPubKey:        resolved.userPubkey,
		UserNPub:          nostrx.EncodeNPub(resolved.userPubkey),
		DefaultFeed:       resolved.loggedOut,
		Relays:            req.Relays,
		WebOfTrustEnabled: resolved.wotEnabled,
		WebOfTrustDepth:   req.WoT.Depth,
	}
}

// feedNewerCount returns just the number of newer notes available, without
// hydrating profiles/reply counts or rendering anything. This keeps the
// 30-second background poll cheap for clients that only need the count.
// Cursor/CursorID on the request are reused as the "since" cursor.
func (s *Server) feedNewerCount(ctx context.Context, req feedRequest) int {
	defer s.observe("feed.newer_count", time.Now())
	sortMode := normalizeFeedSort(req.SortMode)
	if sortMode != feedSortRecent {
		return 0
	}
	resolved := s.resolveRequestAuthors(ctx, req.Pubkey, req.SeedPubkey, req.Relays, req.WoT)
	var events []nostrx.Event
	if resolved.loggedOut && !resolved.wotEnabled {
		events, _ = s.store.NewerSummariesByKinds(ctx, noteTimelineKinds, req.Cursor, req.CursorID, req.Limit)
	} else if resolved.wotEnabled {
		membership := newAuthorMembership(resolved.allAuthors)
		events = s.scanNewerFeedEvents(ctx, membership, req.Cursor, req.CursorID, req.Limit)
		if len(events) == 0 {
			queryAuthors := resolved.feedQueryAuthors()
			if len(queryAuthors) > 0 {
				events, _ = s.store.NewerSummariesByAuthorsCursor(ctx, queryAuthors, noteTimelineKinds, req.Cursor, req.CursorID, req.Limit)
			}
		}
	} else {
		events, _ = s.store.NewerSummariesByAuthorsCursor(ctx, resolved.authors, noteTimelineKinds, req.Cursor, req.CursorID, req.Limit)
	}
	if v := resolved.viewerForMuteFilter(); v != "" {
		events = s.filterFeedEventsByViewerMutes(ctx, v, events)
	}
	if len(events) > req.Limit {
		events = events[:req.Limit]
	}
	return len(events)
}

func (s *Server) fetchRankedFeedPage(ctx context.Context, authors []string, after trendingRankKey, limit int, sortMode string, cacheOnly bool, allowGlobalFallback bool) ([]nostrx.Event, bool, trendingRankKey, bool) {
	if limit <= 0 {
		return nil, false, after, false
	}
	timeframe := feedSortTimeframe(sortMode)
	cohortKey := authorsCacheKey(authors)
	if events, hasMore, nextAfter, ok := s.rankedTrendingFeedPageFromCache(ctx, timeframe, cohortKey, authors, after, limit); ok {
		return events, hasMore, nextAfter, false
	}
	if cacheOnly {
		if cohortKey != "" {
			if events, hasMore, nextAfter, ok := s.rankedTrendingFeedPageFromCache(ctx, timeframe, "", nil, after, limit); ok {
				s.metrics.Add("trending.cohort_global_fallback_cache_hit", 1)
				if !allowGlobalFallback {
					events = filterFeedEventsToAuthors(events, authors)
				}
				if len(events) > 0 {
					return events, hasMore, nextAfter, false
				}
				s.metrics.Add("trending.cohort_global_fallback_cache_empty", 1)
			}
		}
		s.metrics.Add("trending.cache_miss.fast_empty", 1)
		if events, hasMore, nextAfter, ok := s.fetchRankedRecentFallbackPage(ctx, timeframe, authors, allowGlobalFallback, after, limit); ok {
			return events, hasMore, nextAfter, false
		}
		return nil, false, after, false
	}
	s.refreshTrendingCacheAsync(timeframe, cohortKey, authors)
	if cohortKey != "" {
		if events, hasMore, nextAfter, ok := s.rankedTrendingFeedPageFromCache(ctx, timeframe, "", nil, after, limit); ok {
			s.metrics.Add("trending.cohort_global_fallback_cache_hit", 1)
			if !allowGlobalFallback {
				events = filterFeedEventsToAuthors(events, authors)
			}
			if len(events) > 0 {
				return events, hasMore, nextAfter, false
			}
		}
	}
	if events, hasMore, nextAfter, ok := s.fetchRankedRecentFallbackPage(ctx, timeframe, authors, allowGlobalFallback, after, limit); ok {
		return events, hasMore, nextAfter, false
	}
	return nil, false, after, false
}

func (s *Server) fetchRankedSummariesPage(ctx context.Context, sortMode string, authors []string, allowGlobalAuthors bool, after trendingRankKey, limit int) ([]nostrx.Event, bool, trendingRankKey, bool) {
	queryAuthors := authors
	if allowGlobalAuthors {
		queryAuthors = nil
	}
	since := feedSortSince(sortMode, time.Now())
	events, _ := s.store.TrendingSummariesByKindsAfter(ctx, noteTimelineKinds, since, queryAuthors, after.score, after.createdAt, after.id, limit+1)
	if len(events) == 0 {
		return nil, false, after, false
	}
	events, hasMore := trimRankedOverfetch(events, limit)
	return events, hasMore, s.trendingRankKeyForEvent(ctx, events[len(events)-1]), true
}

func (s *Server) fetchRankedRecentFallbackPage(ctx context.Context, timeframe string, authors []string, allowGlobalAuthors bool, after trendingRankKey, limit int) ([]nostrx.Event, bool, trendingRankKey, bool) {
	if limit <= 0 {
		return nil, false, after, false
	}
	queryAuthors := authors
	if allowGlobalAuthors {
		queryAuthors = nil
	}
	since := trendingSince(timeframe, time.Now())
	candidates, err := s.store.TrendingCandidatesByKinds(ctx, noteTimelineKinds, since, queryAuthors, max(limit+1, trendingCacheLimit))
	if err != nil || len(candidates) == 0 {
		return nil, false, after, false
	}
	start := 0
	if after.id != "" {
		found := false
		for idx, item := range candidates {
			if item.NoteID == after.id {
				start = idx + 1
				found = true
				break
			}
		}
		if !found && after.createdAt > 0 {
			for idx, item := range candidates {
				if item.NoteCreatedAt < after.createdAt || (item.NoteCreatedAt == after.createdAt && item.NoteID < after.id) {
					start = idx
					found = true
					break
				}
			}
		}
		if !found {
			return nil, false, after, false
		}
	}
	if start >= len(candidates) {
		return nil, false, after, false
	}
	end := min(start+limit+1, len(candidates))
	ids := make([]string, 0, end-start)
	for _, item := range candidates[start:end] {
		ids = append(ids, item.NoteID)
	}
	byID := s.eventsByIDFromStore(ctx, ids)
	events := make([]nostrx.Event, 0, len(ids))
	for _, id := range ids {
		if ev := byID[id]; ev != nil {
			events = append(events, *ev)
		}
	}
	if len(events) == 0 {
		return nil, false, after, false
	}
	events, hasMore := trimRankedOverfetch(events, limit)
	last := events[len(events)-1]
	nextAfter := s.trendingRankKeyForEvent(ctx, last)
	if nextAfter.createdAt == 0 {
		nextAfter.createdAt = last.CreatedAt
	}
	return events, hasMore, nextAfter, true
}

func (s *Server) refreshFeedForTrending(ctx context.Context, resolved requestAuthors, window int, relays []string) bool {
	if resolved.wotEnabled {
		viewer := resolved.userPubkey
		if resolved.wotViewerPubkey != "" {
			viewer = resolved.wotViewerPubkey
		}
		authors := resolved.feedQueryAuthors()
		if len(authors) == 0 {
			authors = resolved.cohortAuthors()
		}
		return s.refreshRecent(ctx, viewer, authors, 0, window, relays, 0) >= 0
	}
	if len(resolved.authors) > 0 {
		return s.refreshRecent(ctx, resolved.userPubkey, resolved.authors, 0, window, relays, 0) >= 0
	}
	return s.refreshDefaultFeed(ctx, 0, window, relays) >= 0
}

func (s *Server) refreshFeedForTrendingAsync(resolved requestAuthors, window int, relays []string, refreshKey string, timeframe string) {
	if s == nil || !s.allowLegacyWarmers() {
		return
	}
	if refreshKey == "" {
		return
	}
	scopedKey := "feed.trending:" + refreshKey
	if !s.beginRefresh(scopedKey) {
		return
	}
	s.runBackgroundUserAsync(func() {
		defer s.endRefresh(scopedKey)
		timeout := requestTimeout(s.cfg.RequestTimeout)
		if timeout <= 0 {
			timeout = 20 * time.Second
		}
		refreshCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if s.refreshFeedForTrending(refreshCtx, resolved, window, relays) {
			s.store.MarkRefreshed(refreshCtx, "feed", refreshKey)
		}
		cohortAuthors := append([]string(nil), resolved.cohortAuthors()...)
		cohortKey := authorsCacheKey(cohortAuthors)
		if _, err := s.computeAndStoreCohortTrending(refreshCtx, timeframe, cohortKey, cohortAuthors, time.Now()); err != nil {
			s.metrics.Add("trending.cohort_refresh_error", 1)
		}
	})
}

func (s *Server) resolvedAuthorLimit(wot webOfTrustOptions) int {
	limit := maxFeedAuthors
	if wot.Enabled && s.store != nil {
		limit = max(maxFeedAuthors, s.cfg.WOTMaxAuthors)
	}
	return limit
}

func (s *Server) resolveAuthorsAll(ctx context.Context, pubkey string, relays []string, wot webOfTrustOptions) ([]string, string, bool) {
	decoded, err := nostrx.DecodeIdentifier(pubkey)
	if err != nil {
		return nil, "", true
	}
	s.warmAuthor(decoded, relays)
	cacheKey := resolvedAuthorsCacheKey(decoded, wot)
	now := time.Now()
	// Note: this is the central chokepoint where a signed-in viewer's WoT
	// cohort gets resolved, so it doubles as the activity signal feeding the
	// per-viewer trending hot loop (see trending_active_viewers.go).
	s.activeViewers.Touch(decoded, wot, now)
	s.touchKnownViewer(ctx, decoded)
	if cached, ok := s.resolvedAuthors.get(cacheKey, now); ok {
		s.metrics.Add("authors.cache_hit", 1)
		if isDefaultLoggedOutSeed(decoded) {
			cached = uniqueNonEmptyStable(appendDefaultLoggedOutPinnedPubkeys(cached))
		}
		return cached, decoded, false
	}
	if s.store != nil {
		if authors, ts, ok, derr := s.store.GetResolvedAuthorsDurable(ctx, cacheKey); derr == nil && ok && len(authors) > 0 {
			computed := time.Unix(ts, 0)
			if age := now.Sub(computed); age >= 0 && age < resolvedAuthorsDurableMaxAge {
				if isDefaultLoggedOutSeed(decoded) {
					authors = uniqueNonEmptyStable(appendDefaultLoggedOutPinnedPubkeys(authors))
				}
				s.resolvedAuthors.put(cacheKey, authors, now)
				s.metrics.Add("authors.durable_cache_hit", 1)
				return authors, decoded, false
			}
		}
	}
	s.metrics.Add("authors.cache_miss", 1)
	defer s.observe("authors.resolve", time.Now())
	followLimit := maxFeedAuthors
	if wot.Enabled && s != nil {
		followLimit = max(maxFeedAuthors, s.cfg.WOTMaxAuthors)
	}
	authors := append([]string(nil), s.following(ctx, decoded, followLimit)...)
	if len(authors) == 0 && s.store != nil {
		// No follows in store yet; warmAuthor already queued kind-3 fetch.
		s.metrics.Add("authors.cold_miss", 1)
	}
	if wot.Enabled && s.store != nil {
		if reachable, err := s.store.ReachablePubkeysWithin(ctx, decoded, wot.Depth); err == nil {
			authors = append(authors, filterValidFollowPubkeys(reachable)...)
		}
	}
	authors = append(authors, decoded)
	if isDefaultLoggedOutSeed(decoded) {
		authors = appendDefaultLoggedOutPinnedPubkeys(authors)
	}
	resolved := uniqueNonEmptyStable(authors)
	s.resolvedAuthors.put(cacheKey, resolved, now)
	if s.store != nil {
		_ = s.store.SetResolvedAuthorsDurable(ctx, cacheKey, resolved, now.Unix())
	}
	return resolved, decoded, false
}

func (s *Server) resolveAuthorsForSeed(ctx context.Context, seedPubkey string, relays []string, wot webOfTrustOptions) ([]string, string, bool) {
	seedPubkey = strings.TrimSpace(seedPubkey)
	if seedPubkey == "" {
		return nil, "", false
	}
	if s != nil && s.cfg.GuestSliceV2Enabled && isDefaultLoggedOutSeed(seedPubkey) && s.store != nil {
		state, ok, err := s.store.GetGuestSliceState(ctx, store.GuestSliceDefaultKey)
		if err == nil && ok && state.Status == store.GuestSliceStatusReady && len(state.Cohort) > 0 {
			return append([]string(nil), state.Cohort...), state.SeedPubKey, true
		}
	}
	authors, decoded, loggedOut := s.resolveAuthorsAll(ctx, seedPubkey, relays, wot)
	if loggedOut {
		return nil, "", false
	}
	return authors, decoded, true
}

func (s *Server) resolveRequestAuthors(ctx context.Context, pubkey, seedPubkey string, relays []string, wot webOfTrustOptions) requestAuthors {
	if !wot.Enabled {
		allAuthors, userPubkey, loggedOut := s.resolveAuthorsAll(ctx, pubkey, relays, wot)
		return requestAuthors{
			allAuthors:      allAuthors,
			authors:         clampAuthorsWithLimit(allAuthors, s.resolvedAuthorLimit(wot)),
			userPubkey:      userPubkey,
			wotViewerPubkey: userPubkey,
			loggedOut:       loggedOut,
		}
	}
	userPubkey, loggedOut := s.resolveViewer(pubkey, relays)
	if loggedOut {
		if seedAuthors, seedViewer, ok := s.resolveAuthorsForSeed(ctx, seedPubkey, relays, wot); ok {
			return requestAuthors{
				allAuthors:      seedAuthors,
				authors:         clampAuthorsWithLimit(seedAuthors, s.resolvedAuthorLimit(wot)),
				userPubkey:      userPubkey,
				wotViewerPubkey: seedViewer,
				loggedOut:       true,
				wotEnabled:      true,
				seedWOTEnabled:  true,
			}
		}
		return requestAuthors{
			userPubkey: userPubkey,
			loggedOut:  true,
		}
	}
	allAuthors, userPubkey, loggedOut := s.resolveAuthorsAll(ctx, pubkey, relays, wot)
	return requestAuthors{
		allAuthors:      allAuthors,
		authors:         clampAuthorsWithLimit(allAuthors, s.resolvedAuthorLimit(wot)),
		userPubkey:      userPubkey,
		wotViewerPubkey: userPubkey,
		loggedOut:       loggedOut,
		wotEnabled:      true,
	}
}

func (s *Server) isValidSeedViewer(loggedOut bool, wot webOfTrustOptions, seedPubkey string) bool {
	if !loggedOut || !wot.Enabled {
		return false
	}
	_, err := nostrx.DecodeIdentifier(seedPubkey)
	return err == nil
}

func loggedOutWOTSeedDisplayName(seedPubkey string) string {
	seedPubkey = strings.TrimSpace(seedPubkey)
	if seedPubkey == "" {
		return "Gigi"
	}
	decoded, err := nostrx.DecodeIdentifier(seedPubkey)
	if err != nil || decoded == "" {
		return "Gigi"
	}
	if name, ok := loggedOutWOTSeedNamesByPubkey[decoded]; ok {
		return name
	}
	return "Gigi"
}

// allowSyncRelayWork is the HTML-path gate paired with resolveViewer (store-first when false).
func allowSyncRelayWork(viewerPub string, loggedOut bool) bool {
	return !loggedOut || strings.TrimSpace(viewerPub) != ""
}

// resolveViewer returns just the decoded viewer pubkey and logged-out flag,
// skipping the SQLite follow scan + WoT reachability that resolveAuthors performs.
// Used by lightweight render paths (e.g. heading fragments) that only need
// the viewer identity, not the full author universe.
func (s *Server) resolveViewer(pubkey string, relays []string) (string, bool) {
	decoded, err := nostrx.DecodeIdentifier(pubkey)
	if err != nil {
		return "", true
	}
	s.warmAuthor(decoded, relays)
	return decoded, false
}

func (s *Server) resolveAuthors(ctx context.Context, pubkey string, relays []string, wot webOfTrustOptions) ([]string, string, bool) {
	authors, decoded, loggedOut := s.resolveAuthorsAll(ctx, pubkey, relays, wot)
	if loggedOut {
		return nil, "", true
	}
	return clampAuthorsWithLimit(authors, s.resolvedAuthorLimit(wot)), decoded, false
}

func newAuthorMembership(authors []string) authorMembership {
	membership := authorMembership{
		filter: bloom.New(len(authors)),
		exact:  make(map[string]struct{}, len(authors)),
	}
	for _, author := range authors {
		if author == "" {
			continue
		}
		membership.filter.Add(author)
		membership.exact[author] = struct{}{}
	}
	return membership
}

func (m authorMembership) Contains(pubkey string) bool {
	if pubkey == "" || len(m.exact) == 0 {
		return false
	}
	if m.filter != nil && !m.filter.Test(pubkey) {
		return false
	}
	_, ok := m.exact[pubkey]
	return ok
}

func (s *Server) scanFeedBudget() int {
	if s == nil || s.cfg.EventRetention <= 0 {
		return 10000
	}
	return max(scanFeedChunkSize, s.cfg.EventRetention*2)
}

func (s *Server) scanRecentFeedEvents(ctx context.Context, membership authorMembership, cursor int64, cursorID string, limit int) ([]nostrx.Event, bool) {
	target := limit + 1
	if target < 1 {
		target = 1
	}
	chunkSize := max(scanFeedChunkSize, target*4)
	scanBudget := s.scanFeedBudget()
	before := cursor
	beforeID := cursorID
	scanned := 0
	matched := make([]nostrx.Event, 0, target)
	exhausted := false
	for scanned < scanBudget && len(matched) < target {
		batchLimit := min(chunkSize, scanBudget-scanned)
		batchLimit = min(batchLimit, nostrx.MaxRelayQueryLimit)
		if batchLimit <= 0 {
			break
		}
		batch, err := s.store.RecentSummariesByKinds(ctx, noteTimelineKinds, 0, before, beforeID, batchLimit)
		if err != nil || len(batch) == 0 {
			exhausted = true
			break
		}
		for _, event := range batch {
			if membership.Contains(event.PubKey) {
				matched = append(matched, event)
				if len(matched) >= target {
					break
				}
			}
		}
		scanned += len(batch)
		last := batch[len(batch)-1]
		before = last.CreatedAt
		beforeID = last.ID
		if len(batch) < batchLimit {
			exhausted = true
			break
		}
	}
	return matched, exhausted
}

func (s *Server) scanRecentFeedEventsNewerThan(ctx context.Context, membership authorMembership, cursor int64, cursorID string, limit int, cutoffCreatedAt int64, cutoffID string) ([]nostrx.Event, bool) {
	target := limit + 1
	if target < 1 {
		target = 1
	}
	chunkSize := max(scanFeedChunkSize, target*4)
	scanBudget := s.scanFeedBudget()
	before := cursor
	beforeID := cursorID
	scanned := 0
	matched := make([]nostrx.Event, 0, target)
	exhausted := false
	for scanned < scanBudget && len(matched) < target {
		batchLimit := min(chunkSize, scanBudget-scanned)
		batchLimit = min(batchLimit, nostrx.MaxRelayQueryLimit)
		if batchLimit <= 0 {
			break
		}
		batch, err := s.store.RecentSummariesByKinds(ctx, noteTimelineKinds, 0, before, beforeID, batchLimit)
		if err != nil || len(batch) == 0 {
			exhausted = true
			break
		}
		for _, event := range batch {
			if !eventRanksAfter(event, cutoffCreatedAt, cutoffID) {
				exhausted = true
				return matched, exhausted
			}
			if membership.Contains(event.PubKey) {
				matched = append(matched, event)
				if len(matched) >= target {
					break
				}
			}
		}
		scanned += len(batch)
		last := batch[len(batch)-1]
		before = last.CreatedAt
		beforeID = last.ID
		if len(batch) < batchLimit {
			exhausted = true
			break
		}
	}
	return matched, exhausted
}

func (s *Server) scanNewerFeedEvents(ctx context.Context, membership authorMembership, since int64, sinceID string, limit int) []nostrx.Event {
	target := limit
	if target < 1 {
		target = 1
	}
	chunkSize := max(scanFeedChunkSize, target*4)
	scanBudget := s.scanFeedBudget()
	cursor := since
	cursorID := sinceID
	scanned := 0
	matched := make([]nostrx.Event, 0, target)
	for scanned < scanBudget && len(matched) < target {
		batchLimit := min(chunkSize, scanBudget-scanned)
		batchLimit = min(batchLimit, nostrx.MaxRelayQueryLimit)
		if batchLimit <= 0 {
			break
		}
		batch, err := s.store.NewerSummariesByKinds(ctx, noteTimelineKinds, cursor, cursorID, batchLimit)
		if err != nil || len(batch) == 0 {
			break
		}
		for _, event := range batch {
			if membership.Contains(event.PubKey) {
				matched = append(matched, event)
				if len(matched) >= target {
					break
				}
			}
		}
		scanned += len(batch)
		last := batch[len(batch)-1]
		cursor = last.CreatedAt
		cursorID = last.ID
		if len(batch) < batchLimit {
			break
		}
	}
	return matched
}

// fetchWoTFeedPage serves recent WoT notes via indexed author queries first,
// falling back to a global timeline scan only when the store has no matches.
func (s *Server) fetchWoTFeedPage(ctx context.Context, viewer string, queryAuthors []string, membership authorMembership, cursor int64, cursorID string, limit int, relays []string, scope, cacheKey string) ([]nostrx.Event, bool) {
	muted, ok := s.feedMuteSet(ctx, viewer)
	if !ok {
		return nil, false
	}
	pageLimit := limit + 1
	var sqlEvents []nostrx.Event
	sqlHasMore := false
	if len(queryAuthors) > 0 {
		sqlEvents, sqlHasMore = s.fetchAuthorsPage(ctx, viewer, queryAuthors, cursor, cursorID, limit, relays, scope, cacheKey, muted, true)
		if len(sqlEvents) >= pageLimit && len(membership.exact) <= len(queryAuthors) {
			s.metrics.Add("feed.wot_sql_hit", 1)
			return sqlEvents, sqlHasMore
		}
		if len(sqlEvents) > 0 {
			s.metrics.Add("feed.wot_sql_thin", 1)
		}
	}
	scanOpts := scanFeedPageOptions{
		muted:               muted,
		mutesLoaded:         true,
		refreshAuthors:      queryAuthors,
		allowAuthorFallback: false,
	}
	if len(sqlEvents) >= pageLimit {
		tail := sqlEvents[len(sqlEvents)-1]
		scanOpts.stopAtCreatedAt = tail.CreatedAt
		scanOpts.stopAtID = tail.ID
	}
	scanEvents, scanHasMore := s.fetchScannedFeedPageWithOptions(ctx, viewer, membership, cursor, cursorID, limit, relays, scope, cacheKey, scanOpts)
	if len(sqlEvents) > 0 || len(scanEvents) > 0 {
		if len(sqlEvents) > 0 {
			s.metrics.Add("feed.wot_sql_hit", 1)
		} else {
			s.metrics.Add("feed.wot_sql_miss_scan_fallback", 1)
		}
		merged, hasMore := mergeFeedPagesByRecency(sqlEvents, scanEvents, limit)
		if len(scanEvents) > 0 && len(sqlEvents) > 0 {
			s.metrics.Add("feed.wot_scan_backfill", 1)
		}
		return merged, hasMore || sqlHasMore || scanHasMore
	}
	if len(queryAuthors) > 0 {
		s.metrics.Add("feed.wot_sql_miss_scan_fallback", 1)
	}
	return nil, false
}

func (s *Server) fetchScannedFeedPage(ctx context.Context, viewer string, authors []string, membership authorMembership, cursor int64, cursorID string, limit int, relays []string, scope, cacheKey string) ([]nostrx.Event, bool) {
	return s.fetchScannedFeedPageWithOptions(ctx, viewer, membership, cursor, cursorID, limit, relays, scope, cacheKey, scanFeedPageOptions{
		refreshAuthors:      authors,
		allowAuthorFallback: true,
	})
}

type scanFeedPageOptions struct {
	muted               map[string]struct{}
	mutesLoaded         bool
	refreshAuthors      []string
	allowAuthorFallback bool
	stopAtCreatedAt     int64
	stopAtID            string
}

func (s *Server) fetchScannedFeedPageWithOptions(ctx context.Context, viewer string, membership authorMembership, cursor int64, cursorID string, limit int, relays []string, scope, cacheKey string, opts scanFeedPageOptions) ([]nostrx.Event, bool) {
	defer s.observe("feed.scan_page", time.Now())
	pageLimit := limit + 1
	pageKey := feedRefreshKey(cacheKey, cursor, cursorID)
	muted := opts.muted
	if !opts.mutesLoaded {
		var ok bool
		muted, ok = s.feedMuteSet(ctx, viewer)
		if !ok {
			return nil, false
		}
	}
	scanTarget := limit
	if len(muted) > 0 {
		scanTarget = limit * feedMuteTopUpMaxRounds
	}
	var events []nostrx.Event
	var exhausted bool
	if opts.stopAtCreatedAt > 0 {
		events, exhausted = s.scanRecentFeedEventsNewerThan(ctx, membership, cursor, cursorID, scanTarget, opts.stopAtCreatedAt, opts.stopAtID)
	} else {
		events, exhausted = s.scanRecentFeedEvents(ctx, membership, cursor, cursorID, scanTarget)
	}
	events = s.hydrateTimelineEvents(ctx, events)
	if len(muted) > 0 {
		events = s.filterEventsByViewerMutedSet(events, muted)
	}
	if len(events) > pageLimit {
		events = events[:pageLimit]
	}
	if opts.allowAuthorFallback && len(events) == 0 && len(opts.refreshAuthors) > 0 {
		return s.fetchAuthorsPage(ctx, viewer, opts.refreshAuthors, cursor, cursorID, limit, relays, scope, cacheKey, muted, true)
	}
	shouldRefresh := len(events) == 0 || s.store.ShouldRefresh(ctx, scope, pageKey, feedTTL)
	if len(events) >= pageLimit {
		if shouldRefresh {
			oldest := events[len(events)-1]
			s.refreshRecentAsync(viewer, opts.refreshAuthors, oldest.CreatedAt, max(pageLimit*4, loggedInFetchWindow), relays, scope, pageKey)
		} else {
			s.metrics.Add("feed.scan_cache_hit_full", 1)
		}
		return events, true
	}
	if !shouldRefresh {
		if len(events) > 0 {
			oldest := events[len(events)-1]
			s.warmRecent(viewer, opts.refreshAuthors, oldest.CreatedAt, loggedInFetchWindow, relays)
		}
		s.metrics.Add("feed.scan_cache_hit_thin", 1)
		return events, len(events) >= pageLimit || (!exhausted && len(events) > 0)
	}
	// At this point we have a thin (non-empty, sub-page) result whose cache
	// entry needs refreshing. The empty-events case was already routed to
	// fetchAuthorsPage above (and an empty `authors` list implies an empty
	// `membership`, so a synchronous relay refresh + rescan would still
	// match nothing). Trigger an async refresh and return what we have.
	fetchLimit := max(pageLimit*4, loggedInFetchWindow)
	if len(events) > 0 {
		oldest := events[len(events)-1]
		s.refreshRecentAsync(viewer, opts.refreshAuthors, oldest.CreatedAt, fetchLimit, relays, scope, pageKey)
	}
	return events, len(events) > 0 && !exhausted
}

func mergeFeedPagesByRecency(left, right []nostrx.Event, limit int) ([]nostrx.Event, bool) {
	pageLimit := limit + 1
	if pageLimit <= 0 {
		pageLimit = 1
	}
	seen := eventIDSet(left, len(left)+len(right))
	merged := append([]nostrx.Event(nil), left...)
	merged, _ = appendUniqueEventsByID(merged, right, seen, 0)
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].CreatedAt != merged[j].CreatedAt {
			return merged[i].CreatedAt > merged[j].CreatedAt
		}
		return merged[i].ID > merged[j].ID
	})
	hasMore := len(merged) > limit
	if len(merged) > pageLimit {
		merged = merged[:pageLimit]
	}
	return merged, hasMore
}

func eventIDSet(events []nostrx.Event, capacity int) map[string]struct{} {
	seen := make(map[string]struct{}, capacity)
	for _, event := range events {
		if event.ID != "" {
			seen[event.ID] = struct{}{}
		}
	}
	return seen
}

func appendUniqueEventsByID(dst []nostrx.Event, src []nostrx.Event, seen map[string]struct{}, limit int) ([]nostrx.Event, bool) {
	for _, event := range src {
		if event.ID != "" {
			if _, ok := seen[event.ID]; ok {
				continue
			}
			seen[event.ID] = struct{}{}
		}
		if limit > 0 && len(dst) >= limit {
			return dst, true
		}
		dst = append(dst, event)
	}
	return dst, false
}

func eventRanksAfter(event nostrx.Event, createdAt int64, id string) bool {
	return event.CreatedAt > createdAt || (event.CreatedAt == createdAt && event.ID > id)
}

func (s *Server) feedMuteSet(ctx context.Context, viewer string) (map[string]struct{}, bool) {
	if strings.TrimSpace(viewer) == "" {
		return nil, true
	}
	muted, err := s.viewerMutePubkeySet(ctx, viewer)
	if err != nil {
		slog.Warn("viewer mutes: MutedPubkeys failed", "viewer", short(viewer), "err", err)
		return nil, false
	}
	return muted, true
}

func (s *Server) fetchAuthorsPage(ctx context.Context, viewer string, authors []string, cursor int64, cursorID string, limit int, relays []string, scope, cacheKey string, muted map[string]struct{}, mutesLoaded bool) ([]nostrx.Event, bool) {
	defer s.observe("feed.authors_page", time.Now())
	allowRefresh := s.allowLegacyRelayBackend()
	pageLimit := limit + 1
	pageKey := feedRefreshKey(cacheKey, cursor, cursorID)
	if allowRefresh && scope == "profile" && cursor <= 0 && cursorID == "" {
		headKey := cacheKey + "|head"
		if s.store.ShouldRefresh(ctx, scope, headKey, profileHeadRefreshTTL) {
			fetchLimit := recentAuthorsFetchLimit(pageLimit)
			if strings.TrimSpace(viewer) == "" {
				s.refreshRecentAsync(viewer, authors, 0, fetchLimit, relays, scope, headKey)
			} else if s.refreshRecent(ctx, viewer, authors, 0, fetchLimit, relays, s.feedSince()) >= 0 {
				s.store.MarkRefreshed(ctx, scope, headKey)
			}
		}
	}
	if !mutesLoaded {
		var ok bool
		muted, ok = s.feedMuteSet(ctx, viewer)
		if !ok {
			return nil, false
		}
	}
	events := make([]nostrx.Event, 0, pageLimit)
	scanCursor := cursor
	scanCursorID := cursorID
	for round := 0; round < feedMuteTopUpMaxRounds && len(events) < pageLimit; round++ {
		need := pageLimit - len(events)
		fetchN := need
		if len(muted) > 0 {
			fetchN = need * 2
			if fetchN < pageLimit {
				fetchN = pageLimit
			}
		}
		batch, _ := s.store.RecentSummariesByAuthorsCursor(ctx, authors, noteTimelineKinds, scanCursor, scanCursorID, fetchN)
		if len(batch) == 0 {
			break
		}
		tail := batch[len(batch)-1]
		batch = s.hydrateTimelineEvents(ctx, batch)
		if len(muted) > 0 {
			batch = s.filterEventsByViewerMutedSet(batch, muted)
		}
		for _, ev := range batch {
			events = append(events, ev)
			if len(events) >= pageLimit {
				break
			}
		}
		if len(events) >= pageLimit {
			break
		}
		if tail.CreatedAt == scanCursor && tail.ID == scanCursorID {
			break
		}
		scanCursor = tail.CreatedAt
		scanCursorID = tail.ID
		if len(batch) < fetchN {
			break
		}
	}
	shouldRefresh := len(events) == 0 || s.store.ShouldRefresh(ctx, scope, pageKey, feedTTL)
	fetchLimit := recentAuthorsFetchLimit(pageLimit)
	if len(events) >= pageLimit {
		if allowRefresh && shouldRefresh {
			oldest := events[len(events)-1]
			s.refreshRecentAsync(viewer, authors, oldest.CreatedAt, fetchLimit, relays, scope, pageKey)
		} else {
			s.metrics.Add("feed.cache_hit_full", 1)
		}
		return events, true
	}

	if !shouldRefresh {
		// Keep pagination open for thin cached pages; relay backfill may still find older notes.
		if allowRefresh && len(events) > 0 && strings.TrimSpace(viewer) != "" {
			oldest := events[len(events)-1]
			s.warmRecent(viewer, authors, oldest.CreatedAt, loggedInFetchWindow, relays)
		}
		s.metrics.Add("feed.cache_hit_thin", 1)
		return events, len(events) > 0
	}

	before := cursor
	if len(events) > 0 {
		oldest := events[len(events)-1]
		before = oldest.CreatedAt
	}
	if allowRefresh {
		s.refreshRecentAsync(viewer, authors, before, fetchLimit, relays, scope, pageKey)
	}
	maybeMore := len(events) >= pageLimit || len(events) > 0
	return events, maybeMore
}

// fetchDefaultFeedPage returns kind-1 notes from the configured relays within
// the feed window (no author filter). Older pages beyond the window trigger a
// relay fetch, so users seeking older notes still work — we just don't try to
// keep every note forever.
func (s *Server) fetchDefaultFeedPage(ctx context.Context, cursor int64, cursorID string, limit int, relays []string) ([]nostrx.Event, bool) {
	defer s.observe("feed.default_page", time.Now())
	pageLimit := limit + 1
	window := pageLimit * 4
	if window > loggedOutFetchLimit {
		window = loggedOutFetchLimit
	}
	since := s.feedSince()
	events, _ := s.store.RecentSummariesByKinds(ctx, noteTimelineKinds, since, cursor, cursorID, window)
	events = s.hydrateTimelineEvents(ctx, events)
	pageKey := feedRefreshKey(defaultFeedCacheKey, cursor, cursorID)
	shouldRefresh := len(events) == 0 || s.store.ShouldRefresh(ctx, "feed", pageKey, feedTTL)
	if len(events) >= pageLimit && !shouldRefresh {
		s.metrics.Add("feed.default_cache_hit", 1)
	} else if shouldRefresh {
		s.refreshDefaultFeedAsync(cursor, window, relays, pageKey)
	}
	events = limitEventsPerAuthor(events, loggedOutMaxPerAuthor, pageLimit)
	return events, len(events) >= pageLimit
}

func (s *Server) refreshDefaultFeedAsync(cursor int64, limit int, relays []string, pageKey string) {
	if s == nil || !s.allowLegacyWarmers() {
		return
	}
	if pageKey == "" {
		return
	}
	if !s.beginRefresh(pageKey) {
		return
	}
	s.runBackgroundUserAsync(func() {
		defer s.endRefresh(pageKey)
		timeout := requestTimeout(s.cfg.RequestTimeout)
		if timeout <= 0 {
			timeout = 20 * time.Second
		}
		refreshCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		refreshed := s.refreshDefaultFeed(refreshCtx, cursor, limit, relays)
		if refreshed >= 0 {
			s.store.MarkRefreshed(refreshCtx, "feed", pageKey)
		}
	})
}

func (s *Server) refreshRecentAsync(viewer string, authors []string, before int64, limit int, relays []string, scope, pageKey string) {
	if s == nil || !s.allowLegacyWarmers() {
		return
	}
	if pageKey == "" {
		return
	}
	refreshKey := scope + ":" + pageKey
	if !s.beginRefresh(refreshKey) {
		return
	}
	s.runBackgroundUserAsync(func() {
		defer s.endRefresh(refreshKey)
		timeout := requestTimeout(s.cfg.RequestTimeout)
		if timeout <= 0 {
			timeout = 20 * time.Second
		}
		refreshCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if fetched := s.refreshRecent(refreshCtx, viewer, authors, before, limit, relays, 0); fetched >= 0 {
			s.store.MarkRefreshed(refreshCtx, scope, pageKey)
		}
	})
}

func (s *Server) beginRefresh(key string) bool {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	if s.inFlight[key] {
		return false
	}
	s.inFlight[key] = true
	return true
}

func (s *Server) endRefresh(key string) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	delete(s.inFlight, key)
}

func (s *Server) feedSince() int64 {
	window := s.cfg.FeedWindow
	if window <= 0 {
		window = 7 * 24 * time.Hour
	}
	return time.Now().Add(-window).Unix()
}

func (s *Server) refreshCached(ctx context.Context, scope, key string, ttl time.Duration, relays []string, query nostrx.Query) int {
	defer s.observe("refresh."+scope, time.Now())
	if !s.store.ShouldRefresh(ctx, scope, key, ttl) {
		s.metrics.Add("refresh.cache_fresh", 1)
		return 0
	}
	events, err := s.nostr.FetchFrom(ctx, relays, query)
	if err != nil {
		s.metrics.Add("refresh.error", 1)
		slog.Debug("refresh failed", "scope", scope, "key", short(key), "err", err)
		return -1
	}
	if _, err := s.store.SaveEvents(ctx, events); err != nil {
		if strings.Contains(err.Error(), "SQLITE_BUSY") || strings.Contains(err.Error(), "database is locked") {
			s.metrics.Add("store.sqlite_busy", 1)
		}
		slog.Warn("failed to cache refresh events", "scope", scope, "key", short(key), "err", err)
		return -1
	}
	s.invalidateResolvedAuthorsForEvents(events)
	s.store.MarkRefreshed(ctx, scope, key)
	s.metrics.Add("refresh.success", 1)
	s.metrics.Add("refresh.events", int64(len(events)))
	return len(events)
}

func (s *Server) refreshAuthor(ctx context.Context, pubkey string, relays []string) {
	s.refreshAuthorWithTTL(ctx, pubkey, relays, 10*time.Minute)
}

func (s *Server) refreshAuthorWithTTL(ctx context.Context, pubkey string, relays []string, ttl time.Duration) {
	authorRelays := s.authorMetadataRelays(ctx, pubkey, relays)
	if s.nostr != nil {
		authorRelays = s.nostr.FilterAvailableRelays(authorRelays)
	}
	result := s.refreshCached(ctx, "author", pubkey, positiveDuration(ttl, 10*time.Minute), authorRelays, nostrx.Query{
		Authors: []string{pubkey},
		Kinds: []int{
			nostrx.KindProfileMetadata,
			nostrx.KindFollowList,
			nostrx.KindMuteList,
			nostrx.KindRelayListMetadata,
		},
		Limit: 40,
	})
	_ = s.store.MarkHydrationAttempt(ctx, "profile", pubkey, result >= 0, 2*time.Minute)
	_ = s.store.MarkHydrationAttempt(ctx, "followGraph", pubkey, result >= 0, 2*time.Minute)
	_ = s.store.MarkHydrationAttempt(ctx, "relayHints", pubkey, result >= 0, 5*time.Minute)
}

// refreshRecent fetches note timeline kinds before `before`; non-zero sinceUnix sets relay filter Since.
func (s *Server) refreshRecent(ctx context.Context, viewer string, authors []string, before int64, limit int, relays []string, sinceUnix int64) int {
	if before <= 0 {
		before = time.Now().Unix() + 1
	}
	if sinceUnix > 0 && sinceUnix >= before {
		sinceUnix = 0
	}
	groups := s.groupAuthorsForOutbox(ctx, viewer, authors, relays)
	if len(groups) == 0 {
		groups = []outboxRouteGroup{{authors: append([]string(nil), authors...), relays: append([]string(nil), relays...)}}
	}
	total := 0
	for _, group := range groups {
		q := nostrx.Query{
			Authors: group.authors,
			Kinds:   noteTimelineKinds,
			Until:   before,
			Limit:   limit,
		}
		if sinceUnix > 0 {
			q.Since = sinceUnix
		}
		fetched := s.refreshCached(ctx, "recent", authorsCacheKey(group.authors), 0, group.relays, q)
		if fetched < 0 {
			continue
		}
		total += fetched
	}
	return total
}

// refreshDefaultFeed pulls recent kind-1 notes from the configured relays,
// saves them, and prunes the cache to stay within EventRetention.
//
// Many public relays refuse unconstrained queries that include both since and
// until, or that have no time bounds and no authors. We only send the minimal
// constraint a relay needs to honor a plain firehose: kinds + limit (and an
// upper bound when paginating older notes). Time-window filtering is enforced
// client-side via the store.
func (s *Server) refreshDefaultFeed(ctx context.Context, before int64, limit int, relays []string) int {
	defer s.observe("refresh.default_feed", time.Now())
	since := s.feedSince()
	query := nostrx.Query{
		Kinds: noteTimelineKinds,
		Limit: limit,
	}
	if before > 0 {
		query.Until = before
	}
	events, err := s.nostr.FetchFrom(ctx, relays, query)
	if err != nil {
		s.metrics.Add("refresh.error", 1)
		slog.Info("refresh default feed failed", "relays", len(relays), "err", err)
		return -1
	}
	filtered := make([]nostrx.Event, 0, len(events))
	for _, event := range events {
		if event.CreatedAt < since {
			continue
		}
		filtered = append(filtered, event)
	}
	saved, saveErr := s.store.SaveEvents(ctx, filtered)
	if saveErr != nil {
		if strings.Contains(saveErr.Error(), "SQLITE_BUSY") || strings.Contains(saveErr.Error(), "database is locked") {
			s.metrics.Add("store.sqlite_busy", 1)
		}
		slog.Warn("failed to cache default feed batch", "scope", "feed", "err", saveErr)
	}
	slog.Info("refresh default feed", "fetched", len(events), "saved", saved, "since", since, "until", before)
	s.metrics.Add("refresh.success", 1)
	s.metrics.Add("refresh.events", int64(saved))
	if s.cfg.EventRetention > 0 {
		s.store.RequestPruneAsync()
	}
	return saved
}

func (s *Server) warmFeedEntities(events []nostrx.Event, relays []string) {
	if len(events) == 0 {
		return
	}
	pubkeys := make([]string, 0, len(events))
	eventIDs := make([]string, 0, len(events))
	for _, event := range events {
		pubkeys = append(pubkeys, event.PubKey)
		eventIDs = append(eventIDs, event.ID)
	}
	s.warmAuthors(trimWarmStrings(pubkeys, maxWarmFeedAuthors), relays)
	ids := trimWarmStrings(eventIDs, maxWarmFeedThreads)
	if s.runtimeCapabilities().DesktopShell && s.warmer != nil {
		for _, id := range limitedStrings(ids, 12) {
			s.warmer.enqueue(warmJob{
				key:      "threadMaterialize:" + id,
				kind:     "threadMaterialize",
				eventIDs: []string{id},
				relays:   append([]string(nil), relays...),
			})
		}
		if len(ids) > 12 {
			s.warmThread(ids[12:], relays)
		}
		return
	}
	s.warmThread(ids, relays)
}

func mapEvents(byID map[string]nostrx.Event) []nostrx.Event {
	if len(byID) == 0 {
		return nil
	}
	events := make([]nostrx.Event, 0, len(byID))
	for _, event := range byID {
		events = append(events, event)
	}
	return events
}

// referencedHydration loads the events referenced (reposted/quoted) by the
// given feed and returns both the lookup map and a fresh combined slice
// (feed + referenced) suitable for passing to replyCounts/profilesFor without
// risking aliasing the caller's slice.
func (s *Server) referencedHydration(ctx context.Context, events []nostrx.Event, relays []string) (map[string]nostrx.Event, []nostrx.Event) {
	referenced := s.referencedEventsFor(ctx, events, relays)
	combined := make([]nostrx.Event, 0, len(events)+len(referenced))
	combined = append(combined, events...)
	for _, event := range referenced {
		combined = append(combined, event)
	}
	return referenced, combined
}

func (s *Server) referencedHydrationFromStore(ctx context.Context, events []nostrx.Event) (map[string]nostrx.Event, []nostrx.Event) {
	referenced := s.referencedEventsForFromStore(ctx, events)
	combined := make([]nostrx.Event, 0, len(events)+len(referenced))
	combined = append(combined, events...)
	for _, event := range referenced {
		combined = append(combined, event)
	}
	return referenced, combined
}

func (s *Server) threadReferencedHydration(ctx context.Context, events []nostrx.Event, relays []string, storeOnly, allowRelay bool) (map[string]nostrx.Event, []nostrx.Event) {
	if storeOnly || !allowRelay {
		return s.referencedHydrationFromStore(ctx, events)
	}
	return s.referencedHydration(ctx, events, relays)
}

func (s *Server) hydrateTimelineEvents(ctx context.Context, events []nostrx.Event) []nostrx.Event {
	if len(events) == 0 {
		return events
	}
	ids := make([]string, 0, len(events))
	for _, event := range events {
		if event.ID == "" {
			continue
		}
		ids = append(ids, event.ID)
	}
	byID := s.eventsByIDFromStore(ctx, ids)
	out := make([]nostrx.Event, 0, len(events))
	for _, event := range events {
		full := byID[event.ID]
		if full != nil {
			out = append(out, *full)
			continue
		}
		out = append(out, event)
	}
	return out
}

func referencedEventID(event nostrx.Event) string {
	id, _ := referencedEventRef(event)
	return id
}

func referencedEventIDs(event nostrx.Event) []string {
	var tagName string
	switch event.Kind {
	case nostrx.KindRepost:
		tagName = "e"
	case nostrx.KindTextNote:
		tagName = "q"
	default:
		return nil
	}
	var ids []string
	for _, tag := range event.Tags {
		if len(tag) < 2 || tag[0] != tagName {
			continue
		}
		id := nostrx.CanonicalHex64(tag[1])
		if id == "" {
			continue
		}
		duplicate := false
		for _, existing := range ids {
			if existing == id {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

func collectReferencedEventIDs(events []nostrx.Event) []string {
	ids := make([]string, 0, len(events))
	seen := make(map[string]bool, len(events))
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for _, event := range events {
		id, _ := referencedEventRef(event)
		add(id)
		for _, ref := range nostrx.ExtractNIP27References(event.Content) {
			if ref.Event == "" {
				continue
			}
			add(ref.Event)
		}
	}
	return ids
}

func (s *Server) referencedEventsFor(ctx context.Context, events []nostrx.Event, relays []string) map[string]nostrx.Event {
	ids := collectReferencedEventIDs(events)
	merged := make([]string, 0, len(relays)+len(events))
	merged = append(merged, relays...)
	for _, event := range events {
		_, hint := referencedEventRef(event)
		if hint != "" {
			merged = append(merged, hint)
		}
		for _, ref := range nostrx.ExtractNIP27References(event.Content) {
			merged = append(merged, ref.Relays...)
		}
	}
	if len(ids) == 0 {
		return map[string]nostrx.Event{}
	}
	relayList := nostrx.NormalizeRelayList(merged, nostrx.MaxRelays)
	if len(relayList) == 0 {
		relayList = nostrx.NormalizeRelayList(s.cfg.DefaultRelays, nostrx.MaxRelays)
	}
	loaded := s.eventsByID(ctx, ids, relayList)
	out := make(map[string]nostrx.Event, len(loaded))
	for _, id := range ids {
		event := loaded[id]
		if event == nil {
			continue
		}
		out[id] = *event
	}
	s.mergeEmbeddedRepostReferences(ctx, out, events)
	return out
}

func (s *Server) referencedEventsForFromStore(ctx context.Context, events []nostrx.Event) map[string]nostrx.Event {
	ids := collectReferencedEventIDs(events)
	if len(ids) == 0 {
		return map[string]nostrx.Event{}
	}
	loaded := s.eventsByIDFromStore(ctx, ids)
	out := make(map[string]nostrx.Event, len(loaded))
	for _, id := range ids {
		event := loaded[id]
		if event == nil {
			continue
		}
		out[id] = *event
	}
	s.mergeEmbeddedRepostReferences(ctx, out, events)
	return out
}

func (s *Server) mergeEmbeddedRepostReferences(ctx context.Context, out map[string]nostrx.Event, events []nostrx.Event) {
	for _, event := range events {
		if event.Kind != nostrx.KindRepost {
			continue
		}
		refID, relayHint := referencedEventRef(event)
		refID = nostrx.CanonicalHex64(strings.TrimSpace(refID))
		if refID == "" {
			continue
		}
		if _, ok := out[refID]; ok {
			continue
		}
		embedded, ok := nostrx.ParseEmbeddedRepost(event.Content, refID)
		if !ok {
			continue
		}
		if relayHint != "" {
			embedded.RelayURL = relayHint
		} else if event.RelayURL != "" {
			embedded.RelayURL = event.RelayURL
		}
		out[refID] = embedded

		// A NIP-18 repost may be the only relay response that contains the
		// original note. Rendering used to consume that embedded event only
		// from this request-local map, so clicking the visible reference could
		// immediately miss in the thread cache. Promote signed embedded events
		// to the durable store just like events fetched by ID.
		if s == nil || s.store == nil || nostrx.ValidateSignedEvent(embedded) != nil {
			continue
		}
		_ = s.store.SaveEvent(ctx, embedded)
	}
}

func referencedEventRef(event nostrx.Event) (id string, relay string) {
	var tagName string
	switch event.Kind {
	case nostrx.KindRepost:
		tagName = "e"
	case nostrx.KindTextNote:
		tagName = "q"
	default:
		return "", ""
	}
	for _, tag := range event.Tags {
		if len(tag) < 2 || tag[0] != tagName {
			continue
		}
		id = strings.TrimSpace(tag[1])
		if len(tag) >= 3 {
			relay = strings.TrimSpace(tag[2])
		}
		return id, relay
	}
	return "", ""
}

func recentAuthorsFetchLimit(pageLimit int) int {
	fetchLimit := pageLimit * 4
	if fetchLimit < pageLimit {
		fetchLimit = pageLimit
	}
	if fetchLimit > loggedInFetchWindow {
		fetchLimit = loggedInFetchWindow
	}
	return fetchLimit
}

func clampAuthors(authors []string) []string {
	return clampAuthorsWithLimit(authors, maxFeedAuthors)
}

func clampAuthorsWithLimit(authors []string, limit int) []string {
	if limit <= 0 {
		limit = maxFeedAuthors
	}
	if len(authors) > limit {
		return authors[:limit]
	}
	return authors
}

func authorsCacheKey(authors []string) string {
	if len(authors) == 0 {
		return ""
	}
	normalized := make([]string, 0, min(len(authors), maxAuthorsCacheKeyAuthors))
	seen := make(map[string]struct{}, min(len(authors), maxAuthorsCacheKeyAuthors))
	for _, author := range authors {
		author = strings.TrimSpace(author)
		if author == "" {
			continue
		}
		if _, ok := seen[author]; ok {
			continue
		}
		seen[author] = struct{}{}
		normalized = append(normalized, author)
		if len(normalized) >= maxAuthorsCacheKeyAuthors {
			break
		}
	}
	sort.Strings(normalized)
	if len(normalized) == 0 {
		return ""
	}
	h := sha256.New()
	for _, author := range normalized {
		_, _ = h.Write([]byte(author))
		_, _ = h.Write([]byte{0})
	}
	sum := h.Sum(nil)
	return "authors:" + hex.EncodeToString(sum)
}

func feedRefreshKey(cacheKey string, cursor int64, cursorID string) string {
	if cursor <= 0 && cursorID == "" {
		return cacheKey
	}
	return cacheKey + "|before:" + cacheCursorKey(cursor, cursorID)
}

func cacheCursorKey(cursor int64, cursorID string) string {
	if cursorID == "" {
		return time.Unix(cursor, 0).UTC().Format("20060102150405")
	}
	return time.Unix(cursor, 0).UTC().Format("20060102150405") + ":" + short(cursorID)
}

func (s *Server) guestFeedCacheKey(req feedRequest, resolved requestAuthors, sortMode string, timeframe string, includeTrending bool) (string, bool) {
	if s == nil || s.guestFeedCache == nil {
		return "", false
	}
	if !resolved.loggedOut {
		return "", false
	}
	if req.Cursor > 0 || req.CursorID != "" {
		return "", false
	}
	switch normalizeFeedSort(sortMode) {
	case feedSortRecent, feedSortTrend24h, feedSortTrend7d:
	default:
		return "", false
	}
	railPart := "|rail:" + strconv.FormatBool(includeTrending)
	tfPart := "|tf:" + timeframe
	depthPart := "|depth:" + strconv.Itoa(req.WoT.Depth)
	relaysPart := "|relays:" + hashStringSlice(req.Relays)
	sortPart := "|sort:" + sortMode

	if resolved.wotEnabled {
		cohortKey := authorsCacheKey(resolved.cohortAuthors())
		if cohortKey == "" {
			return "", false
		}
		key := "guest_feed" + sortPart + tfPart + depthPart + railPart + "|cohort:" + cohortKey + relaysPart
		return key, true
	}
	// Logged-out firehose (WoT off): first-page guest cache (no cohort).
	key := "guest_feed" + sortPart + tfPart + "|wot:0" + railPart + relaysPart
	return key, true
}

func isDefaultLoggedOutSeed(seed string) bool {
	seed = strings.TrimSpace(seed)
	if seed == "" {
		seed = defaultLoggedOutWOTSeedNPub
	}
	defaultSeed, err := nostrx.DecodeIdentifier(defaultLoggedOutWOTSeedNPub)
	if err != nil || defaultSeed == "" {
		return false
	}
	decoded, err := nostrx.DecodeIdentifier(seed)
	if err == nil && decoded != "" {
		return decoded == defaultSeed
	}
	return seed == defaultLoggedOutWOTSeedNPub
}

func limitedStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}
	return values[:limit]
}

func limitEventsPerAuthor(events []nostrx.Event, maxPerAuthor int, want int) []nostrx.Event {
	if want <= 0 {
		return nil
	}
	if maxPerAuthor <= 0 {
		if len(events) <= want {
			return events
		}
		return events[:want]
	}
	counts := make(map[string]int)
	out := make([]nostrx.Event, 0, min(len(events), want))
	for _, event := range events {
		if counts[event.PubKey] >= maxPerAuthor {
			continue
		}
		counts[event.PubKey]++
		out = append(out, event)
		if len(out) >= want {
			return out
		}
	}
	return out
}

func uniqueNonEmptyStrings(values []string) []string {
	out := uniqueNonEmptyStable(values)
	sort.Strings(out)
	return out
}

func uniqueNonEmptyStable(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
