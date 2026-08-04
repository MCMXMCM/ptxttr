package httpx

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

const profileTimelinePageSize = 25
const profileTimelineCacheScanLimit = 250

// userHeaderPageData deliberately excludes timelines, reactions, follow lists,
// and related-profile expansion so identity metadata can render independently
// from the slower profile posts response.
func (s *Server) userHeaderPageData(ctx context.Context, r *http.Request, pubkey string) UserPageData {
	profile := s.profile(ctx, pubkey)
	if profile.PubKey == "" {
		profile.PubKey = pubkey
	}
	nip05Status := nostrx.NIP05VerificationResult("")
	nip05Label := ""
	if strings.TrimSpace(profile.NIP05) != "" {
		if anonymousRequestFromHTTP(r) {
			nip05Status = s.cachedNIP05Verification(ctx, profile.NIP05, pubkey)
		} else {
			nip05Status = s.verifyNIP05Cached(ctx, profile.NIP05, pubkey)
		}
		nip05Label = nip05StatusLabel(nip05Status)
	}
	return UserPageData{
		BasePageData:     s.userBasePageData(r, "User", "feed", "feed-shell"),
		Profile:          profile,
		NIP05Status:      nip05Status,
		NIP05StatusLabel: nip05Label,
	}
}

func (s *Server) userPageData(ctx context.Context, r *http.Request, pubkey string, fragment string) UserPageData {
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	cursorID := strings.TrimSpace(r.URL.Query().Get("cursor_id"))
	relays := s.requestRelays(r)
	guestStoreOnly := anonymousRequestFromHTTP(r)
	if guestStoreOnly {
		// Anonymous profile documents are authoritative cache views. Never let a
		// crawler-selected relay header turn a guest page into synchronous relay
		// work at the origin.
		relays = nil
	}
	profile := s.profile(ctx, pubkey)
	nip05Status := nostrx.NIP05VerificationResult("")
	nip05Label := ""
	if strings.TrimSpace(profile.NIP05) != "" {
		if guestStoreOnly {
			nip05Status = s.cachedNIP05Verification(ctx, profile.NIP05, pubkey)
		} else {
			nip05Status = s.verifyNIP05Cached(ctx, profile.NIP05, pubkey)
		}
		nip05Label = nip05StatusLabel(nip05Status)
	}
	userRelays := s.userRelays(ctx, pubkey)
	contactRelays := nostrx.NormalizeRelayList(append(append([]string(nil), relays...), userRelays...), nostrx.MaxRelays)
	followingQuery := r.URL.Query().Get("following_q")
	followersQuery := r.URL.Query().Get("followers_q")
	followingPage := pageFromQuery(r, "following_page")
	followersPage := pageFromQuery(r, "followers_page")
	following := FollowListView{Page: 1, PageSize: followListPageSize, CachedExact: true}
	followers := FollowListView{Page: 1, PageSize: followListPageSize, CachedExact: true}
	if fragment == "following" {
		following = s.followingList(ctx, pubkey, followingQuery, followingPage)
	} else if fragment == "followers" {
		followers = s.followersList(ctx, pubkey, followersQuery, followersPage)
	} else if followingCount, followerCount, countErr := s.store.FollowCounts(ctx, pubkey); countErr == nil {
		following.CachedTotal = followingCount
		following.FilteredTotal = followingCount
		followers.CachedTotal = followerCount
		followers.FilteredTotal = followerCount
	}

	events, hasMore := s.profileTimelinePage(ctx, pubkey, fragment, cursor, cursorID, profileTimelinePageSize, relays, guestStoreOnly)
	if len(events) > profileTimelinePageSize {
		events = events[:profileTimelinePageSize]
		hasMore = true
	}
	nextCursor, nextID := int64(0), ""
	if len(events) > 0 {
		last := events[len(events)-1]
		nextCursor, nextID = last.CreatedAt, last.ID
	}
	posts := make([]nostrx.Event, 0, len(events))
	replies := make([]nostrx.Event, 0, len(events))
	media := make([]nostrx.Event, 0, len(events))
	for _, event := range events {
		if isProfilePostEvent(event) {
			posts = append(posts, event)
		}
		if isReplyEvent(event) {
			replies = append(replies, event)
		}
		if hasProfileMedia(event) {
			media = append(media, event)
		}
	}
	feed := posts
	switch fragment {
	case "posts", "":
		feed = posts
	case "replies":
		feed = replies
	case "media":
		feed = media
	}
	var referenced map[string]nostrx.Event
	var combined []nostrx.Event
	if guestStoreOnly {
		referenced, combined = s.referencedHydrationFromStore(ctx, feed)
	} else {
		referenced, combined = s.referencedHydration(ctx, feed, relays)
	}
	rt, rv := s.reactionMapsForEvents(ctx, combined, "")
	var contacts []string
	var contactProfiles map[string]nostrx.Profile
	switch fragment {
	case "following":
		if enriched, profiles, ok := s.enrichedFollowList(ctx, pubkey, followingQuery, followingPage, true, contactRelays); ok {
			following = enriched
			contactProfiles = profiles
		}
		contacts = append([]string(nil), following.Items...)
	case "followers":
		if enriched, profiles, ok := s.enrichedFollowList(ctx, pubkey, followersQuery, followersPage, false, contactRelays); ok {
			followers = enriched
			contactProfiles = profiles
		}
		contacts = append([]string(nil), followers.Items...)
	}
	if contactProfiles == nil {
		contactProfiles = s.contactProfiles(ctx, contacts, contactRelays)
	}
	counts, countsErr := s.store.ProfileTimelineCounts(ctx, pubkey)
	data := UserPageData{
		BasePageData:            s.userBasePageData(r, "User", "feed", "feed-shell"),
		Profile:                 profile,
		NIP05Status:             nip05Status,
		NIP05StatusLabel:        nip05Label,
		ProfileTimelineTerminal: guestStoreOnly,
		CachedPostCount:         counts.Posts,
		CachedReplyCount:        counts.Replies,
		CachedMediaCount:        counts.Media,
		HasCachedCounts:         countsErr == nil,
		FollowingList:           following,
		FollowersList:           followers,
		UserRelays:              userRelays,
		Feed:                    feed,
		Replies:                 replies,
		Media:                   media,
		ReferencedEvents:        referenced,
		ReplyCounts:             s.replyCounts(ctx, combined),
		ReactionTotals:          rt,
		ReactionViewers:         rv,
		Profiles:                s.profilesFor(ctx, append(combined, nostrx.Event{PubKey: pubkey})),
		ContactProfiles:         contactProfiles,
		Relays:                  relays,
		Cursor:                  nextCursor,
		CursorID:                nextID,
		HasMore:                 hasMore,
	}
	if data.Profile.PubKey == "" {
		data.Profile.PubKey = pubkey
	}
	data.Profiles[pubkey] = data.Profile
	return data
}

func (s *Server) profileTimelinePage(ctx context.Context, pubkey string, fragment string, cursor int64, cursorID string, limit int, relays []string, storeOnly bool) ([]nostrx.Event, bool) {
	if !storeOnly && (fragment == "" || fragment == "posts") {
		events, hasMore := s.fetchAuthorsPage(ctx, "", []string{pubkey}, cursor, cursorID, limit*4, relays, "profile", pubkey, nil, false)
		posts := make([]nostrx.Event, 0, limit+1)
		for _, event := range events {
			if !isProfilePostEvent(event) {
				continue
			}
			posts = append(posts, event)
			if len(posts) > limit {
				return posts[:limit], true
			}
		}
		return posts, hasMore
	}
	if fragment != "replies" && fragment != "media" {
		if fragment != "" && fragment != "posts" {
			return s.profileTimelinePage(ctx, pubkey, "posts", cursor, cursorID, limit, relays, storeOnly)
		}
	}
	pageLimit := limit + 1
	events := make([]nostrx.Event, 0, pageLimit)
	scanCursor := cursor
	scanCursorID := cursorID
	sourceExhausted := false
	for scanned := 0; scanned < profileTimelineCacheScanLimit && len(events) < pageLimit; {
		need := pageLimit - len(events)
		fetchN := need * 4
		if fetchN < 50 {
			fetchN = 50
		}
		remaining := profileTimelineCacheScanLimit - scanned
		if fetchN > remaining {
			fetchN = remaining
		}
		if fetchN <= 0 {
			break
		}
		batch, _ := s.store.RecentSummariesByAuthorsCursor(ctx, []string{pubkey}, noteTimelineKinds, scanCursor, scanCursorID, fetchN)
		if len(batch) == 0 {
			sourceExhausted = true
			break
		}
		scanned += len(batch)
		tail := batch[len(batch)-1]
		batch = s.hydrateTimelineEvents(ctx, batch)
		for _, event := range batch {
			switch fragment {
			case "", "posts":
				if isProfilePostEvent(event) {
					events = append(events, event)
				}
			case "replies":
				if isReplyEvent(event) {
					events = append(events, event)
				}
			case "media":
				if hasProfileMedia(event) {
					events = append(events, event)
				}
			}
			if len(events) >= pageLimit {
				break
			}
		}
		if tail.CreatedAt == scanCursor && tail.ID == scanCursorID {
			sourceExhausted = true
			break
		}
		scanCursor = tail.CreatedAt
		scanCursorID = tail.ID
		if len(batch) < fetchN {
			sourceExhausted = true
			break
		}
	}
	if len(events) > limit {
		return events[:limit], true
	}
	return events, len(events) > 0 && !sourceExhausted
}

func isProfilePostEvent(event nostrx.Event) bool {
	return event.Kind == nostrx.KindTextNote && !isReplyEvent(event)
}

func pageFromQuery(r *http.Request, key string) int {
	page, _ := strconv.Atoi(r.URL.Query().Get(key))
	if page < 1 {
		return 1
	}
	return page
}

func hasProfileMedia(event nostrx.Event) bool {
	if imetaMediaItemsJSON(event.Tags) != "" {
		return true
	}
	return hasMediaContent(event.Content)
}

func (s *Server) searchPageData(ctx context.Context, r *http.Request) SearchPageData {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	mode := normalizeSearchMode(r.URL.Query().Get("mode"))
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	cursorID := strings.TrimSpace(r.URL.Query().Get("cursor_id"))
	req := feedRequest{
		Pubkey:     viewerFromRequest(r),
		SeedPubkey: seedPubkeyFromRequest(r),
		Relays:     s.requestRelays(r),
		WoT:        webOfTrustOptionsFromRequest(r),
	}
	base := s.basePageData(r, "Search", "search", "feed-shell")
	data := SearchPageData{
		BasePageData: base,
		Query:        query,
		Mode:         mode,
		Scope:        searchScopeAll,
		ScopeLabel:   searchScopeLabel(searchScopeAll),
	}
	if mode == searchModeUsers {
		pubkeys, _ := s.store.SearchProfiles(ctx, store.SearchProfilesQuery{Text: query, Limit: 30})
		profiles := s.profilesForPubkeys(ctx, pubkeys)
		data.Profiles = profiles
		for _, pubkey := range pubkeys {
			data.ProfileResults = append(data.ProfileResults, SearchProfileResult{PubKey: pubkey, Profile: profiles[pubkey]})
		}
		return data
	}
	resolved := s.resolveRequestAuthors(ctx, req.Pubkey, req.SeedPubkey, req.Relays, req.WoT)
	scope := normalizeSearchScope(r.URL.Query().Get("scope"), resolved.loggedOut, resolved.wotEnabled)
	var authors []string
	if scope == searchScopeNetwork {
		authors = resolved.authors
	}
	result := s.searchNotesStoreResult(ctx, store.PrepareSearch(query), scope, authors, cursor, cursorID, 30)
	events := s.hydrateTimelineEvents(ctx, result.Events)
	events = s.filterFeedEventsByViewerMutes(ctx, resolved.viewerForMuteFilter(), events)
	referenced, combined := s.referencedHydration(ctx, events, req.Relays)
	rt, rv := s.reactionMapsForEvents(ctx, combined, resolved.viewerForMuteFilter())
	data.Scope = scope
	data.ScopeLabel = searchScopeLabel(scope)
	data.ScopeAllURL = searchScopeURL(r, searchScopeAll)
	data.ScopeNetworkURL = searchScopeURL(r, searchScopeNetwork)
	data.ShowScopeToggle = resolved.wotEnabled
	data.Feed = events
	data.ReferencedEvents = referenced
	data.ReplyCounts = s.replyCounts(ctx, combined)
	data.ReactionTotals = rt
	data.ReactionViewers = rv
	data.Profiles = s.profilesFor(ctx, combined)
	data.Cursor = result.NextCreatedAt
	data.CursorID = result.NextID
	data.HasMore = result.HasMore
	data.OldestCachedAt = result.OldestCachedAt
	data.LatestCachedAt = result.LatestCachedAt
	return data
}

func searchScopeURL(r *http.Request, scope string) string {
	values := url.Values{}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		values.Set("q", q)
	}
	values.Set("mode", searchModeNotes)
	if scope == searchScopeAll {
		values.Set("scope", searchScopeAll)
	} else {
		values.Set("scope", searchScopeNetwork)
	}
	return "/search?" + values.Encode()
}

func (s *Server) readsRequestFromHTTP(r *http.Request) feedRequest {
	req := s.feedRequestFromHTTP(r)
	req.SortMode = getReadsSortOrDefault(r)
	req.Timeframe = normalizeTrendingTimeframe(readsTrendingTfFromRequest(r))
	req.Limit = 20
	req.Cursor, _ = strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	req.CursorID = strings.TrimSpace(r.URL.Query().Get("cursor_id"))
	return req
}

func getReadsSortOrDefault(r *http.Request) string {
	if raw := strings.TrimSpace(feedSortFromRequest(r)); raw != "" {
		return raw
	}
	return normalizeFeedSort(r.URL.Query().Get("sort"))
}
