package httpx

import (
	"context"
	"strings"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

func summaryToProfile(summary store.ProfileSummary) nostrx.Profile {
	return nostrx.Profile{
		PubKey:    summary.PubKey,
		EventID:   summary.EventID,
		CreatedAt: summary.CreatedAt,
		Display:   summary.DisplayName,
		Name:      summary.Name,
		About:     summary.About,
		Picture:   summary.Picture,
		Website:   summary.Website,
		NIP05:     summary.NIP05,
		Lud16:     summary.Lud16,
		Lud06:     summary.Lud06,
	}
}

func profileNeedsEventMerge(profile nostrx.Profile) bool {
	return strings.TrimSpace(profile.Picture) == "" ||
		strings.TrimSpace(profile.Website) == "" ||
		strings.TrimSpace(profile.Lud16) == "" ||
		strings.TrimSpace(profile.Lud06) == ""
}

func mergeProfileFromEvent(out, parsed nostrx.Profile) nostrx.Profile {
	if strings.TrimSpace(out.Picture) == "" {
		out.Picture = strings.TrimSpace(parsed.Picture)
	}
	if strings.TrimSpace(out.Website) == "" {
		out.Website = strings.TrimSpace(parsed.Website)
	}
	if strings.TrimSpace(out.Lud16) == "" {
		out.Lud16 = strings.TrimSpace(parsed.Lud16)
	}
	if strings.TrimSpace(out.Lud06) == "" {
		out.Lud06 = strings.TrimSpace(parsed.Lud06)
	}
	return out
}

func (s *Server) profile(ctx context.Context, pubkey string) nostrx.Profile {
	if summaries, err := s.store.ProfileSummariesByPubkeys(ctx, []string{pubkey}); err == nil {
		if summary, ok := summaries[pubkey]; ok {
			out := summaryToProfile(summary)
			// Profile cache can lag kind-0 metadata (e.g. lud16 added after initial projection).
			if profileNeedsEventMerge(out) {
				event, _ := s.store.LatestReplaceable(ctx, pubkey, nostrx.KindProfileMetadata)
				out = mergeProfileFromEvent(out, nostrx.ParseProfile(pubkey, event))
			}
			return out
		}
	}
	event, _ := s.store.LatestReplaceable(ctx, pubkey, nostrx.KindProfileMetadata)
	return nostrx.ParseProfile(pubkey, event)
}

func (s *Server) profilesFor(ctx context.Context, events []nostrx.Event) map[string]nostrx.Profile {
	defer s.observe("store.profiles_for", time.Now())
	const maxMentionResolves = 200
	seen := make(map[string]bool)
	pubkeys := make([]string, 0, len(events))
	for _, event := range events {
		if event.PubKey != "" && !seen[event.PubKey] {
			seen[event.PubKey] = true
			pubkeys = append(pubkeys, event.PubKey)
		}
		if len(pubkeys) >= maxMentionResolves {
			continue
		}
		// Harvest mention pubkeys from NIP-27 references in the body so the
		// caller's profile map can resolve display names for inline mentions
		// (not just for authors of events in the slice).
		for _, mention := range nostrx.ExtractMentionPubKeys(event.Content) {
			if mention == "" || seen[mention] {
				continue
			}
			seen[mention] = true
			pubkeys = append(pubkeys, mention)
			if len(pubkeys) >= maxMentionResolves {
				break
			}
		}
	}

	return s.profilesForPubkeys(ctx, pubkeys)
}

func (s *Server) profilesFromStore(ctx context.Context, pubkeys []string) map[string]nostrx.Profile {
	profiles := make(map[string]nostrx.Profile, len(pubkeys))
	summaries, err := s.store.ProfileSummariesByPubkeys(ctx, pubkeys)
	if err == nil {
		for pubkey, summary := range summaries {
			profiles[pubkey] = summaryToProfile(summary)
		}
	}
	latest, latestErr := s.store.LatestReplaceableByPubkeys(ctx, pubkeys, nostrx.KindProfileMetadata)
	if latestErr != nil {
		latest = map[string]*nostrx.Event{}
	}
	for _, pubkey := range pubkeys {
		if _, ok := profiles[pubkey]; ok {
			continue
		}
		profiles[pubkey] = nostrx.ParseProfile(pubkey, latest[pubkey])
	}
	return profiles
}

func (s *Server) profilesForPubkeys(ctx context.Context, pubkeys []string) map[string]nostrx.Profile {
	defer s.observe("store.profiles_for_pubkeys", time.Now())
	if len(pubkeys) == 0 {
		return map[string]nostrx.Profile{}
	}
	unique := make([]string, 0, len(pubkeys))
	seen := make(map[string]bool, len(pubkeys))
	for _, pubkey := range pubkeys {
		if pubkey == "" || seen[pubkey] {
			continue
		}
		seen[pubkey] = true
		unique = append(unique, pubkey)
	}
	profiles := s.profilesFromStore(ctx, unique)
	if !s.runtimeCapabilities().LocalFirst || s.nostr == nil {
		return profiles
	}
	stale := make([]string, 0, len(unique))
	for _, pubkey := range unique {
		profileTTL := positiveDuration(s.cfg.ViewerCrawlerProfileInterval, 6*time.Hour)
		if contactProfileNeedsHydration(profiles[pubkey]) || s.store.ShouldRefresh(ctx, "desktop.profile", pubkey, profileTTL) {
			stale = append(stale, pubkey)
		}
	}
	if len(stale) == 0 {
		return profiles
	}
	// A desktop request is a loopback call dedicated to one user. Spend a
	// bounded foreground budget filling missing kind-0 metadata so feed/thread
	// HTML arrives with identity instead of delegating the same work back to
	// Chromium after first paint.
	budget := 3 * time.Second
	if s.cfg.RequestTimeout > 0 && s.cfg.RequestTimeout < budget {
		budget = s.cfg.RequestTimeout
	}
	hydrateCtx, cancel := context.WithTimeout(ctx, budget)
	refreshed := s.hydrateContactProfiles(hydrateCtx, stale, nil)
	cancel()
	for _, pubkey := range refreshed {
		s.store.MarkRefreshed(ctx, "desktop.profile", pubkey)
	}
	return s.profilesFromStore(ctx, unique)
}

func contactProfileNeedsHydration(profile nostrx.Profile) bool {
	return strings.TrimSpace(profile.Display) == "" &&
		strings.TrimSpace(profile.Name) == "" &&
		strings.TrimSpace(profile.Picture) == "" &&
		strings.TrimSpace(profile.NIP05) == ""
}

func (s *Server) hydrateContactProfiles(ctx context.Context, pubkeys []string, relays []string) []string {
	if s == nil || s.nostr == nil || s.store == nil {
		return nil
	}
	keys := limitedStrings(uniqueNonEmptyStrings(pubkeys), followMetadataHydrationLimit)
	if len(keys) == 0 {
		return nil
	}
	relayList := nostrx.NormalizeRelayList(append(
		append(append([]string(nil), relays...), s.cfg.DefaultRelays...),
		s.cfg.MetadataRelays...,
	), nostrx.MaxRelays)
	if len(relayList) == 0 {
		return nil
	}
	events := make([]nostrx.Event, 0, len(keys))
	refreshed := make([]string, 0, len(keys))
	for start := 0; start < len(keys); start += followMetadataBatchSize {
		end := start + followMetadataBatchSize
		if end > len(keys) {
			end = len(keys)
		}
		batch := keys[start:end]
		fetched, err := s.nostr.FetchFrom(ctx, relayList, nostrx.Query{
			Authors: batch,
			Kinds:   []int{nostrx.KindProfileMetadata},
			Limit:   max(len(batch)*2, len(batch)),
		})
		if err != nil || len(fetched) == 0 {
			if err == nil {
				refreshed = append(refreshed, batch...)
			}
			continue
		}
		refreshed = append(refreshed, batch...)
		events = append(events, fetched...)
	}
	if len(events) == 0 {
		return refreshed
	}
	if _, err := s.store.SaveEvents(ctx, events); err != nil {
		return nil
	}
	return refreshed
}

func (s *Server) contactProfiles(ctx context.Context, pubkeys []string, relays []string) map[string]nostrx.Profile {
	profiles := make(map[string]nostrx.Profile, len(pubkeys))
	seen := make(map[string]bool, len(pubkeys))
	keys := make([]string, 0, len(pubkeys))
	for _, pubkey := range pubkeys {
		if pubkey == "" || seen[pubkey] {
			continue
		}
		seen[pubkey] = true
		keys = append(keys, pubkey)
	}
	if len(keys) == 0 {
		return profiles
	}
	summaries, err := s.store.ProfileSummariesByPubkeys(ctx, keys)
	if err == nil {
		for pubkey, summary := range summaries {
			profiles[pubkey] = summaryToProfile(summary)
		}
	}
	missing := make([]string, 0, len(keys))
	for _, pubkey := range keys {
		if _, ok := profiles[pubkey]; ok {
			continue
		}
		missing = append(missing, pubkey)
	}
	if len(missing) > 0 {
		latest, latestErr := s.store.LatestReplaceableByPubkeys(ctx, missing, nostrx.KindProfileMetadata)
		if latestErr == nil {
			for _, pubkey := range missing {
				profiles[pubkey] = nostrx.ParseProfile(pubkey, latest[pubkey])
			}
		}
	}
	for _, pubkey := range keys {
		if _, ok := profiles[pubkey]; ok {
			continue
		}
		profiles[pubkey] = nostrx.Profile{PubKey: pubkey}
	}
	missingMetadata := make([]string, 0, len(keys))
	for _, pubkey := range keys {
		if contactProfileNeedsHydration(profiles[pubkey]) {
			missingMetadata = append(missingMetadata, pubkey)
		}
	}
	if len(missingMetadata) > 0 {
		s.hydrateContactProfilesAsync(missingMetadata, relays)
	}
	return profiles
}

func (s *Server) hydrateContactProfilesAsync(pubkeys []string, relays []string) {
	if s == nil || s.nostr == nil || s.store == nil {
		return
	}
	keys := limitedStrings(uniqueNonEmptyStrings(pubkeys), followMetadataHydrationLimit)
	if len(keys) == 0 {
		return
	}
	relayList := nostrx.NormalizeRelayList(append(
		append(append([]string(nil), relays...), s.cfg.DefaultRelays...),
		s.cfg.MetadataRelays...,
	), nostrx.MaxRelays)
	if len(relayList) == 0 {
		return
	}
	refreshKey := "contactProfiles:" + strings.Join(keys, ",")
	if !s.beginRefresh(refreshKey) {
		return
	}
	if !s.runBackgroundUserAsync(func() {
		defer s.endRefresh(refreshKey)
		timeout := requestTimeout(s.cfg.RequestTimeout)
		if timeout <= 0 {
			timeout = 20 * time.Second
		}
		refreshCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		s.hydrateContactProfiles(refreshCtx, keys, relayList)
	}) {
		s.endRefresh(refreshKey)
	}
}

func (s *Server) following(ctx context.Context, pubkey string, limit int) []string {
	defer s.observe("store.following", time.Now())
	if limit <= 0 {
		limit = maxFeedAuthors
	}
	if follows, err := s.store.FollowingPubkeys(ctx, pubkey, limit); err == nil && len(follows) > 0 {
		return filterValidFollowPubkeys(follows)
	}
	event, _ := s.store.LatestReplaceable(ctx, pubkey, nostrx.KindFollowList)
	pubkeys := nostrx.FollowPubkeys(event)
	if len(pubkeys) > limit {
		pubkeys = pubkeys[:limit]
	}
	return pubkeys
}

func filterValidFollowPubkeys(pubkeys []string) []string {
	if len(pubkeys) == 0 {
		return nil
	}
	out := make([]string, 0, len(pubkeys))
	for _, pubkey := range pubkeys {
		if !nostrx.IsValidPubKeyHex(pubkey) {
			continue
		}
		out = append(out, pubkey)
	}
	return out
}

func (s *Server) followers(ctx context.Context, pubkey string, limit int) []string {
	defer s.observe("store.followers", time.Now())
	if follows, err := s.store.FollowerPubkeys(ctx, pubkey, limit); err == nil && len(follows) > 0 {
		return filterValidFollowPubkeys(follows)
	}
	events, _ := s.store.FollowersOf(ctx, pubkey, limit)
	seen := make(map[string]bool)
	var followers []string
	for _, event := range events {
		if event.PubKey == "" || seen[event.PubKey] {
			continue
		}
		if !nostrx.IsValidPubKeyHex(event.PubKey) {
			continue
		}
		seen[event.PubKey] = true
		followers = append(followers, event.PubKey)
	}
	return followers
}

const (
	followListPageSize           = 100
	followMetadataBatchSize      = 50
	followMetadataHydrationLimit = 2000
)

func (s *Server) followingList(ctx context.Context, pubkey string, query string, page int) FollowListView {
	if view, ok := s.followingListFromLatestEvent(ctx, pubkey, query, page); ok {
		view.Hashtags = s.followingHashtags(ctx, pubkey, query)
		return view
	}
	view := s.followList(ctx, pubkey, query, page, true)
	view.Hashtags = s.followingHashtags(ctx, pubkey, query)
	return view
}

func (s *Server) followersList(ctx context.Context, pubkey string, query string, page int) FollowListView {
	return s.followList(ctx, pubkey, query, page, false)
}

func (s *Server) followList(ctx context.Context, pubkey string, query string, page int, following bool) FollowListView {
	if page < 1 {
		page = 1
	}
	cleanQuery := strings.TrimSpace(query)
	offset := (page - 1) * followListPageSize
	var (
		result store.FollowPageResult
		err    error
	)
	if following {
		result, err = s.store.FollowingPubkeysPage(ctx, pubkey, cleanQuery, followListPageSize, offset)
	} else {
		result, err = s.store.FollowerPubkeysPage(ctx, pubkey, cleanQuery, followListPageSize, offset)
	}
	if err == nil && result.CachedTotal > 0 {
		result.Pubkeys = filterValidFollowPubkeys(result.Pubkeys)
		return buildFollowListView(result.Pubkeys, cleanQuery, page, result.FilteredTotal, result.CachedTotal, true)
	}
	fallback := s.followListFallback(ctx, pubkey, cleanQuery, page, following)
	// When the projection query succeeded but returned zero rows, treat the
	// fallback's count as an exact cached-zero rather than an estimate.
	if err == nil && result.CachedTotal == 0 && fallback.FilteredTotal == 0 {
		fallback.CachedExact = true
	}
	return fallback
}

func (s *Server) enrichedFollowList(ctx context.Context, pubkey string, query string, page int, following bool, relays []string) (FollowListView, map[string]nostrx.Profile, bool) {
	if page < 1 {
		page = 1
	}
	var view FollowListView
	if following {
		view = s.followingList(ctx, pubkey, query, page)
	} else {
		view = s.followersList(ctx, pubkey, query, page)
	}
	if len(view.Items) == 0 {
		return FollowListView{}, nil, false
	}
	profiles := s.contactProfiles(ctx, view.Items, relays)
	return view, profiles, true
}

func (s *Server) followingListFromLatestEvent(ctx context.Context, pubkey string, query string, page int) (FollowListView, bool) {
	event, _ := s.store.LatestReplaceable(ctx, pubkey, nostrx.KindFollowList)
	if event == nil {
		return FollowListView{}, false
	}
	cleanQuery := strings.TrimSpace(query)
	all := filterValidFollowPubkeys(nostrx.FollowPubkeys(event))
	filtered := all
	if cleanQuery != "" {
		summaries, _ := s.store.ProfileSummariesByPubkeys(ctx, all)
		filtered = make([]string, 0, len(all))
		for _, followedPubkey := range all {
			if followMatchesQuery(cleanQuery, followedPubkey, summaries[followedPubkey]) {
				filtered = append(filtered, followedPubkey)
			}
		}
	}
	return buildFollowListPage(filtered, cleanQuery, page, len(all), true), true
}

func (s *Server) followListFallback(ctx context.Context, pubkey string, query string, page int, following bool) FollowListView {
	var all []string
	if following {
		all = s.following(ctx, pubkey, maxFeedAuthors)
	} else {
		all = s.followers(ctx, pubkey, 500)
	}
	filtered := all
	if query != "" {
		summaries, _ := s.store.ProfileSummariesByPubkeys(ctx, all)
		filtered = filtered[:0]
		for _, pubkey := range all {
			if !nostrx.IsValidPubKeyHex(pubkey) {
				continue
			}
			if followMatchesQuery(query, pubkey, summaries[pubkey]) {
				filtered = append(filtered, pubkey)
			}
		}
	}
	total := len(filtered)
	start := (page - 1) * followListPageSize
	if start > total {
		start = total
	}
	end := start + followListPageSize
	if end > total {
		end = total
	}
	items := append([]string(nil), filtered[start:end]...)
	items = filterValidFollowPubkeys(items)
	return buildFollowListView(items, query, page, total, total, false)
}

func buildFollowListPage(filtered []string, query string, page int, cachedTotal int, cachedExact bool) FollowListView {
	if page < 1 {
		page = 1
	}
	total := len(filtered)
	start := (page - 1) * followListPageSize
	if start > total {
		start = total
	}
	end := start + followListPageSize
	if end > total {
		end = total
	}
	items := append([]string(nil), filtered[start:end]...)
	return buildFollowListView(items, query, page, total, cachedTotal, cachedExact)
}

func buildFollowListView(items []string, query string, page int, filteredTotal int, cachedTotal int, cachedExact bool) FollowListView {
	view := FollowListView{
		Items:         items,
		Hashtags:      nil,
		Query:         query,
		Page:          page,
		PageSize:      followListPageSize,
		FilteredTotal: filteredTotal,
		CachedTotal:   cachedTotal,
		CachedExact:   cachedExact,
		HasPrev:       page > 1,
		HasNext:       page*followListPageSize < filteredTotal,
	}
	view.PrevPage = 1
	if view.HasPrev {
		view.PrevPage = page - 1
	}
	view.NextPage = page
	if view.HasNext {
		view.NextPage = page + 1
	}
	return view
}

func (s *Server) followingHashtags(ctx context.Context, pubkey string, query string) []string {
	event, _ := s.store.LatestReplaceable(ctx, pubkey, nostrx.KindFollowList)
	return filterFollowHashtags(nostrx.FollowHashtags(event), query)
}

func filterFollowHashtags(hashtags []string, query string) []string {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return append([]string(nil), hashtags...)
	}
	filtered := make([]string, 0, len(hashtags))
	for _, tag := range hashtags {
		if strings.Contains(strings.ToLower(tag), needle) {
			filtered = append(filtered, tag)
		}
	}
	return filtered
}

func followMatchesQuery(query string, pubkey string, summary store.ProfileSummary) bool {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return true
	}
	return strings.Contains(strings.ToLower(pubkey), needle) ||
		strings.Contains(strings.ToLower(summary.DisplayName), needle) ||
		strings.Contains(strings.ToLower(summary.Name), needle) ||
		strings.Contains(strings.ToLower(summary.NIP05), needle) ||
		strings.Contains(strings.ToLower(summary.About), needle)
}

func (s *Server) userRelays(ctx context.Context, pubkey string) []string {
	defer s.observe("store.user_relays", time.Now())
	if relays, err := s.store.RelayHintsForPubkey(ctx, pubkey); err == nil && len(relays) > 0 {
		return relays
	}
	event, _ := s.store.LatestReplaceable(ctx, pubkey, nostrx.KindRelayListMetadata)
	return nostrx.RelayURLs(event, 12)
}
