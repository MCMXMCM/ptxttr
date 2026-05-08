package httpx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"ptxt-nstr/internal/bloom"
	"ptxt-nstr/internal/config"
	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

const (
	maxFeedAuthors              = 80
	feedTTL                     = 2 * time.Minute
	profileHeadRefreshTTL       = 90 * time.Second
	threadTTL                   = 2 * time.Minute
	trendingLimit               = 10
	trendingCacheLimit          = 200
	trending24h                 = "24h"
	trending1w                  = "1w"
	loggedInFetchWindow         = 160
	asciiWidthMobile            = 42
	asciiWidthTablet            = 64
	asciiWidthDesktop           = 120
	asciiWidthUserDesktop       = 82
	loggedOutMaxPerAuthor       = 8
	loggedOutFetchLimit         = 160
	defaultFeedCacheKey         = "firehose"
	readsCacheKey               = "reads"
	readsFetchLimit             = 120
	feedSortRecent              = "recent"
	profilePostsNewerLimit      = 30
	feedSortTrend24h            = "trend24h"
	feedSortTrend7d             = "trend7d"
	scanFeedChunkSize           = 256
	defaultLoggedOutWOTDepth    = 3
	defaultLoggedOutWOTSeedNPub = "npub1sg6plzptd64u62a878hep2kev88swjh3tw00gjsfl8f237lmu63q0uf63m"
)

var noteTimelineKinds = []int{nostrx.KindTextNote, nostrx.KindRepost}

var loggedOutWOTSeedNamesByPubkey = func() map[string]string {
	type namedSeed struct {
		name string
		npub string
	}
	seeds := []namedSeed{
		{name: "Jack Dorsey", npub: defaultLoggedOutWOTSeedNPub},
		{name: "Fiatjaf", npub: "npub180cvv07tjdrrgpa0j7j7tmnyl2yr6yr7l8j4s3evf6u64th6gkwsyjh6w6"},
		{name: "Gigi", npub: "npub1dergggklka99wwrs92yz8wdjs952h2ux2ha2ed598ngwu9w7a6fsh9xzpc"},
		{name: "Lyn Alden", npub: "npub1a2cww4kn9wqte4ry70vyfwqyqvpswksna27rtxd8vty6c74era8sdcw83a"},
		{name: "Odell", npub: "npub1qny3tkh0acurzla8x3zy4nhrjz5zd8l9sy9jys09umwng00manysew95gx"},
	}
	m := make(map[string]string, len(seeds))
	for _, seed := range seeds {
		decoded, err := nostrx.DecodeIdentifier(seed.npub)
		if err != nil || decoded == "" {
			continue
		}
		m[decoded] = seed.name
	}
	return m
}()

type webOfTrustOptions struct {
	Enabled bool
	Depth   int
}

type authorMembership struct {
	filter *bloom.Filter
	exact  map[string]struct{}
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
	q := r.URL.Query()
	depth, _ := strconv.Atoi(strings.TrimSpace(q.Get("wot_depth")))
	enabled, _ := config.ParseBool(q.Get("wot"))
	return webOfTrustOptions{
		Enabled: enabled,
		Depth:   store.ClampDepth(depth),
	}
}

func feedSortSince(sortMode string, now time.Time) int64 {
	return trendingSince(feedSortTimeframe(sortMode), now)
}

func (s *Server) requestRelays(r *http.Request) []string {
	rawValues := append([]string(nil), r.URL.Query()["relays"]...)
	rawValues = append(rawValues, r.URL.Query()["relay"]...)
	requestRelays := nostrx.ParseRelayParams(rawValues)
	return nostrx.NormalizeRelayList(append(append([]string(nil), s.cfg.DefaultRelays...), requestRelays...), nostrx.MaxRelays)
}

func (s *Server) asciiWidthForRequest(r *http.Request) int {
	if r.Header.Get("Sec-CH-UA-Mobile") == "?1" {
		return asciiWidthMobile
	}
	ua := strings.ToLower(r.UserAgent())
	isTablet := strings.Contains(ua, "ipad") ||
		strings.Contains(ua, "tablet") ||
		strings.Contains(ua, "kindle") ||
		strings.Contains(ua, "silk") ||
		(strings.Contains(ua, "android") && !strings.Contains(ua, "mobile"))
	if isTablet {
		return asciiWidthTablet
	}
	isMobile := strings.Contains(ua, "mobile") ||
		strings.Contains(ua, "iphone") ||
		strings.Contains(ua, "ipod") ||
		strings.Contains(ua, "android") ||
		strings.Contains(ua, "mobi")
	if isMobile {
		return asciiWidthMobile
	}
	return asciiWidthDesktop
}

// parseAsciiWQuery returns a client-supplied ASCII column count from ?ascii_w=.
// In-page fetch() often omits Sec-CH-UA-Mobile; the SPA sends this hint so
// fragments match the real viewport width.
func parseAsciiWQuery(r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("ascii_w"))
	if raw == "" {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	switch n {
	case asciiWidthMobile, asciiWidthTablet, asciiWidthDesktop, asciiWidthUserDesktop:
		return n, true
	default:
		return 0, false
	}
}

func (s *Server) asciiWidthForRequestWithQuery(r *http.Request) int {
	if w, ok := parseAsciiWQuery(r); ok {
		return w
	}
	return s.asciiWidthForRequest(r)
}

func (s *Server) asciiWidthForUserRequestWithQuery(r *http.Request) int {
	width := s.asciiWidthForRequestWithQuery(r)
	if width == asciiWidthDesktop {
		return asciiWidthUserDesktop
	}
	return width
}

func (s *Server) basePageData(r *http.Request, title, active, pageClass string) BasePageData {
	return BasePageData{
		Title:       title,
		Active:      active,
		PageClass:   pageClass,
		AsciiWidth:  s.asciiWidthForRequestWithQuery(r),
		SearchQuery: searchQueryFromRequest(r),
	}
}

// userBasePageData applies the narrower desktop width used by user-profile
// pages while keeping the other base-page fields aligned with basePageData.
func (s *Server) userBasePageData(r *http.Request, title, active, pageClass string) BasePageData {
	return BasePageData{
		Title:       title,
		Active:      active,
		PageClass:   pageClass,
		AsciiWidth:  s.asciiWidthForUserRequestWithQuery(r),
		SearchQuery: searchQueryFromRequest(r),
	}
}

func searchQueryFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.URL.Query().Get("q"))
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
	defer s.observe("feed.page_data", time.Now())
	resolved := s.resolveRequestAuthors(ctx, req.Pubkey, req.SeedPubkey, req.Relays, req.WoT)
	sortMode := normalizeFeedSort(req.SortMode)
	timeframe := normalizeTrendingTimeframe(req.Timeframe)
	// Default Jack seed: ranked feed may fall back to global trending when
	// cohort cache is cold. Custom seeds stay cohort-scoped only.
	cacheOnlyFeedRanking := resolved.loggedOut && resolved.wotEnabled && isDefaultLoggedOutSeed(req.SeedPubkey)
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
		if resolved.loggedOut {
			if resolved.wotEnabled {
				membership := newAuthorMembership(resolved.allAuthors)
				events, hasMore = s.fetchScannedFeedPage(ctx, resolved.wotViewerPubkey, resolved.authors, membership, req.Cursor, req.CursorID, req.Limit, req.Relays, "feed", authorsCacheKey(resolved.authors))
			} else {
				events, hasMore = s.fetchDefaultFeedPage(ctx, req.Cursor, req.CursorID, req.Limit, req.Relays)
			}
		} else if resolved.wotEnabled {
			membership := newAuthorMembership(resolved.allAuthors)
			events, hasMore = s.fetchScannedFeedPage(ctx, resolved.userPubkey, resolved.authors, membership, req.Cursor, req.CursorID, req.Limit, req.Relays, "feed", authorsCacheKey(resolved.authors))
		} else {
			events, hasMore = s.fetchAuthorsPage(ctx, resolved.userPubkey, resolved.authors, req.Cursor, req.CursorID, req.Limit, req.Relays, "feed", authorsCacheKey(resolved.authors))
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
		offset := int(req.Cursor)
		if offset < 0 {
			offset = 0
		}
		rawOffset := offset
		pageLimit := req.Limit + 1
		window := pageLimit * 4
		if window > loggedInFetchWindow {
			window = loggedInFetchWindow
		}
		trendingKey := feedRefreshKey("feed-"+sortMode, 0, "")
		var cohort []string
		if resolved.wotEnabled {
			cohort = resolved.allAuthors
		} else if !resolved.loggedOut {
			cohort = resolved.authors
		}
		if cohortKey := authorsCacheKey(cohort); cohortKey != "" {
			trendingKey += "|" + cohortKey
		}
		if offset == 0 && s.store.ShouldRefresh(ctx, "feed", trendingKey, feedTTL) {
			s.refreshFeedForTrendingAsync(resolved, window, req.Relays, trendingKey, feedSortTimeframe(sortMode))
		}
		events, hasMore, offset = s.fetchRankedFeedPage(ctx, cohort, offset, req.Limit, sortMode, cacheOnlyFeedRanking)
		if rawOffset == 0 && len(events) == 0 {
			s.metrics.Add("feed.ranked.cold_miss", 1)
		}
		next = int64(offset)
		nextID = ""
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
		trending = s.trendingData(ctx, timeframe, req.Relays, cacheOnlyTrending)
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
				if deferGuestLoggedOutFeedFirstPage(req) {
					data.ReactionTotals = map[string]int{}
					data.ReactionViewers = map[string]string{}
					data.ReplyCounts = map[string]int{}
				}
				return data
			}
		}
		// Canonical logged-out guest first fragment: serve only from durable
		// snapshots on the request path. Live feed assembly runs in background
		// warmers (never feedPageDataEx here) so t4g.small stays responsive.
		if deferGuestLoggedOutFeedFirstPage(req) && s.isGuestCanonicalSnapshotTarget(req) {
			sm := normalizeFeedSort(req.SortMode)
			data := s.feedHeadingData(req)
			if sm == feedSortTrend24h || sm == feedSortTrend7d {
				s.mergeGuestCanonicalTrendSnapshotIntoShell(ctx, &data, req)
				if len(data.Feed) > 0 {
					s.metrics.Add("feed.snapshot_hit", 1)
					data.FeedSort = sm
					data.ReactionTotals = map[string]int{}
					data.ReactionViewers = map[string]string{}
					data.ReplyCounts = map[string]int{}
					s.scheduleGuestFeedFragmentWarm(req)
					return data
				}
				s.metrics.Add("feed.guest_ranked_fragment_snapshot_miss", 1)
				s.scheduleGuestFeedFragmentWarm(req)
				return data
			}
			if sm == feedSortRecent && s.isCanonicalDefaultLoggedOutGuestFeedRequest(req) {
				if s.mergePersistedDefaultSeedGuestFeedIntoShell(ctx, &data, req) && len(data.Feed) > 0 {
					s.metrics.Add("feed.guest_recent_fragment_snapshot_hit", 1)
					s.scheduleGuestFeedFragmentWarm(req)
					return data
				}
				s.metrics.Add("feed.guest_recent_fragment_snapshot_miss", 1)
				s.scheduleGuestFeedFragmentWarm(req)
				return data
			}
		}
	}
	o := feedPageDataOptions{}
	if deferGuestLoggedOutFeedFirstPage(req) {
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
		if len(events) == 0 && len(resolved.authors) > 0 {
			events, _ = s.store.NewerSummariesByAuthorsCursor(ctx, resolved.authors, noteTimelineKinds, req.Cursor, req.CursorID, req.Limit)
		}
	} else {
		events, _ = s.store.NewerSummariesByAuthorsCursor(ctx, resolved.authors, noteTimelineKinds, req.Cursor, req.CursorID, req.Limit)
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
		if len(events) == 0 && len(resolved.authors) > 0 {
			events, _ = s.store.NewerSummariesByAuthorsCursor(ctx, resolved.authors, noteTimelineKinds, req.Cursor, req.CursorID, req.Limit)
		}
	} else {
		events, _ = s.store.NewerSummariesByAuthorsCursor(ctx, resolved.authors, noteTimelineKinds, req.Cursor, req.CursorID, req.Limit)
	}
	if len(events) > req.Limit {
		events = events[:req.Limit]
	}
	return len(events)
}

func (s *Server) fetchRankedFeedPage(ctx context.Context, authors []string, offset int, limit int, sortMode string, cacheOnly bool) ([]nostrx.Event, bool, int) {
	if offset < 0 {
		offset = 0
	}
	timeframe := feedSortTimeframe(sortMode)
	cohortKey := authorsCacheKey(authors)
	if events, hasMore, nextOffset, ok := s.cachedTrendingFeedPage(ctx, timeframe, cohortKey, authors, offset, limit); ok {
		return events, hasMore, nextOffset
	}
	if cacheOnly {
		if cohortKey != "" {
			if events, hasMore, nextOffset, ok := s.cachedTrendingFeedPage(ctx, timeframe, "", nil, offset, limit); ok {
				s.metrics.Add("trending.cohort_global_fallback_cache_hit", 1)
				return events, hasMore, nextOffset
			}
			pageLimit := limit + 1
			since := feedSortSince(sortMode, time.Now())
			events, _ := s.store.TrendingSummariesByKinds(ctx, noteTimelineKinds, since, nil, offset, pageLimit)
			hasMore := len(events) > limit
			if hasMore {
				events = events[:limit]
			}
			if len(events) > 0 {
				s.metrics.Add("trending.cohort_global_fallback_db_hit", 1)
				return events, hasMore, offset + len(events)
			}
		}
		s.metrics.Add("trending.cache_miss.fast_empty", 1)
		if offset == 0 {
			return nil, false, 0
		}
		return nil, false, offset
	}
	pageLimit := limit + 1
	since := feedSortSince(sortMode, time.Now())
	events, _ := s.store.TrendingSummariesByKinds(ctx, noteTimelineKinds, since, authors, offset, pageLimit)
	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}
	if len(events) > 0 {
		return events, hasMore, offset + len(events)
	}
	if offset == 0 {
		return nil, false, 0
	}
	return nil, false, offset
}

func (s *Server) refreshFeedForTrending(ctx context.Context, resolved requestAuthors, window int, relays []string) bool {
	if resolved.wotEnabled {
		viewer := resolved.userPubkey
		if resolved.wotViewerPubkey != "" {
			viewer = resolved.wotViewerPubkey
		}
		authors := resolved.allAuthors
		if len(authors) == 0 {
			authors = resolved.authors
		}
		return s.refreshRecent(ctx, viewer, authors, 0, window, relays, 0) >= 0
	}
	if len(resolved.authors) > 0 {
		return s.refreshRecent(ctx, resolved.userPubkey, resolved.authors, 0, window, relays, 0) >= 0
	}
	return s.refreshDefaultFeed(ctx, 0, window, relays) >= 0
}

func (s *Server) refreshFeedForTrendingAsync(resolved requestAuthors, window int, relays []string, refreshKey string, timeframe string) {
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
		cohortAuthors := append([]string(nil), resolved.authors...)
		if resolved.wotEnabled {
			cohortAuthors = append([]string(nil), resolved.allAuthors...)
		}
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
	if cached, ok := s.resolvedAuthors.get(cacheKey, now); ok {
		s.metrics.Add("authors.cache_hit", 1)
		return cached, decoded, false
	}
	if s.store != nil {
		if authors, ts, ok, derr := s.store.GetResolvedAuthorsDurable(ctx, cacheKey); derr == nil && ok && len(authors) > 0 {
			computed := time.Unix(ts, 0)
			if age := now.Sub(computed); age >= 0 && age < resolvedAuthorsDurableMaxAge {
				s.resolvedAuthors.put(cacheKey, authors, now)
				s.metrics.Add("authors.durable_cache_hit", 1)
				return authors, decoded, false
			}
		}
	}
	s.metrics.Add("authors.cache_miss", 1)
	defer s.observe("authors.resolve", time.Now())
	authors := append([]string(nil), s.following(ctx, decoded)...)
	if len(authors) == 0 && s.store != nil {
		// No follows in store yet; warmAuthor already queued kind-3 fetch.
		s.metrics.Add("authors.cold_miss", 1)
	}
	if wot.Enabled && s.store != nil {
		if reachable, err := s.store.ReachablePubkeysWithin(ctx, decoded, wot.Depth); err == nil {
			authors = append(authors, reachable...)
		}
	}
	authors = append(authors, decoded)
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
		return "Jack Dorsey"
	}
	decoded, err := nostrx.DecodeIdentifier(seedPubkey)
	if err != nil || decoded == "" {
		return "Jack Dorsey"
	}
	if name, ok := loggedOutWOTSeedNamesByPubkey[decoded]; ok {
		return name
	}
	return "selected seed profile"
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

func (s *Server) fetchScannedFeedPage(ctx context.Context, viewer string, authors []string, membership authorMembership, cursor int64, cursorID string, limit int, relays []string, scope, cacheKey string) ([]nostrx.Event, bool) {
	defer s.observe("feed.scan_page", time.Now())
	pageLimit := limit + 1
	pageKey := feedRefreshKey(cacheKey, cursor, cursorID)
	events, exhausted := s.scanRecentFeedEvents(ctx, membership, cursor, cursorID, limit)
	events = s.hydrateTimelineEvents(ctx, events)
	if len(events) == 0 && len(authors) > 0 {
		return s.fetchAuthorsPage(ctx, viewer, authors, cursor, cursorID, limit, relays, scope, cacheKey)
	}
	shouldRefresh := len(events) == 0 || s.store.ShouldRefresh(ctx, scope, pageKey, feedTTL)
	if len(events) >= pageLimit {
		if shouldRefresh {
			oldest := events[len(events)-1]
			s.refreshRecentAsync(viewer, authors, oldest.CreatedAt, max(pageLimit*4, loggedInFetchWindow), relays, scope, pageKey)
		} else {
			s.metrics.Add("feed.scan_cache_hit_full", 1)
		}
		return events, true
	}
	if !shouldRefresh {
		if len(events) > 0 {
			oldest := events[len(events)-1]
			s.warmRecent(viewer, authors, oldest.CreatedAt, loggedInFetchWindow, relays)
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
		s.refreshRecentAsync(viewer, authors, oldest.CreatedAt, fetchLimit, relays, scope, pageKey)
	}
	return events, len(events) > 0 && !exhausted
}

func (s *Server) fetchAuthorsPage(ctx context.Context, viewer string, authors []string, cursor int64, cursorID string, limit int, relays []string, scope, cacheKey string) ([]nostrx.Event, bool) {
	defer s.observe("feed.authors_page", time.Now())
	pageLimit := limit + 1
	pageKey := feedRefreshKey(cacheKey, cursor, cursorID)
	if scope == "profile" && cursor <= 0 && cursorID == "" {
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
	events, _ := s.store.RecentSummariesByAuthorsCursor(ctx, authors, noteTimelineKinds, cursor, cursorID, pageLimit)
	events = s.hydrateTimelineEvents(ctx, events)
	shouldRefresh := len(events) == 0 || s.store.ShouldRefresh(ctx, scope, pageKey, feedTTL)
	fetchLimit := recentAuthorsFetchLimit(pageLimit)
	if len(events) >= pageLimit {
		if shouldRefresh {
			oldest := events[len(events)-1]
			s.refreshRecentAsync(viewer, authors, oldest.CreatedAt, fetchLimit, relays, scope, pageKey)
		} else {
			s.metrics.Add("feed.cache_hit_full", 1)
		}
		return events, true
	}

	if !shouldRefresh {
		// Keep pagination open for thin cached pages; relay backfill may still find older notes.
		if len(events) > 0 && strings.TrimSpace(viewer) != "" {
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
	s.refreshRecentAsync(viewer, authors, before, fetchLimit, relays, scope, pageKey)
	maybeMore := len(events) >= pageLimit || len(events) > 0
	return events, maybeMore
}

func (s *Server) profilePostsNewerFeedPageDataFromSummaries(ctx context.Context, r *http.Request, profilePubkey string, summaries []nostrx.Event) FeedPageData {
	base := s.userBasePageData(r, "User", "feed", "feed-shell")
	relays := s.requestRelays(r)
	events := s.hydrateTimelineEvents(ctx, summaries)
	viewer, loggedOut := s.resolveViewer(r.URL.Query().Get("pubkey"), relays)
	var referenced map[string]nostrx.Event
	var combined []nostrx.Event
	if !allowSyncRelayWork(viewer, loggedOut) {
		referenced, combined = s.referencedHydrationFromStore(ctx, events)
	} else {
		s.warmFeedEntities(events, relays)
		referenced, combined = s.referencedHydration(ctx, events, relays)
	}
	rt, rv := s.reactionMapsForEvents(ctx, combined, viewer)
	return FeedPageData{
		BasePageData:     base,
		Feed:             events,
		ReferencedEvents: referenced,
		ReplyCounts:      s.replyCounts(ctx, combined),
		ReactionTotals:   rt,
		ReactionViewers:  rv,
		Profiles:         s.profilesFor(ctx, combined),
		UserPubKey:       profilePubkey,
		UserNPub:         nostrx.EncodeNPub(profilePubkey),
		Relays:           relays,
		FeedSort:         feedSortRecent,
	}
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
	authorRelays := s.authorMetadataRelays(ctx, pubkey, relays)
	result := s.refreshCached(ctx, "author", pubkey, 10*time.Minute, authorRelays, nostrx.Query{
		Authors: []string{pubkey},
		Kinds: []int{
			nostrx.KindProfileMetadata,
			nostrx.KindFollowList,
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
	if max := s.cfg.EventRetention; max > 0 {
		go func() {
			pruneCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if deleted, err := s.store.PruneEvents(pruneCtx, max); err != nil {
				slog.Warn("prune events failed", "err", err)
			} else if deleted > 0 {
				s.metrics.Add("store.pruned", deleted)
			}
		}()
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
	s.warmThread(trimWarmStrings(eventIDs, maxWarmFeedThreads), relays)
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

func collectReferencedEventIDs(events []nostrx.Event) []string {
	ids := make([]string, 0, len(events))
	seen := make(map[string]bool, len(events))
	for _, event := range events {
		id, _ := referencedEventRef(event)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
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
	return out
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
	normalized := append([]string(nil), authors...)
	sort.Strings(normalized)
	key := strings.Join(normalized, ",")
	if key == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(key))
	return "authors:" + hex.EncodeToString(sum[:])
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
		cohortKey := authorsCacheKey(resolved.allAuthors)
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
