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
		PubKey:  summary.PubKey,
		Display: summary.DisplayName,
		Name:    summary.Name,
		About:   summary.About,
		Picture: summary.Picture,
		NIP05:   summary.NIP05,
	}
}

func (s *Server) profile(ctx context.Context, pubkey string) nostrx.Profile {
	if summaries, err := s.store.ProfileSummariesByPubkeys(ctx, []string{pubkey}); err == nil {
		if summary, ok := summaries[pubkey]; ok {
			out := summaryToProfile(summary)
			// Profile LRU can lag SQLite: empty picture in cache while kind-0 has picture.
			if strings.TrimSpace(out.Picture) == "" {
				event, _ := s.store.LatestReplaceable(ctx, pubkey, nostrx.KindProfileMetadata)
				parsed := nostrx.ParseProfile(pubkey, event)
				if pic := strings.TrimSpace(parsed.Picture); pic != "" {
					out.Picture = pic
				}
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

func (s *Server) contactProfiles(ctx context.Context, pubkeys []string) map[string]nostrx.Profile {
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
	return profiles
}

func (s *Server) following(ctx context.Context, pubkey string) []string {
	defer s.observe("store.following", time.Now())
	if follows, err := s.store.FollowingPubkeys(ctx, pubkey, maxFeedAuthors); err == nil && len(follows) > 0 {
		return follows
	}
	event, _ := s.store.LatestReplaceable(ctx, pubkey, nostrx.KindFollowList)
	return nostrx.FollowPubkeys(event)
}

func (s *Server) followers(ctx context.Context, pubkey string, limit int) []string {
	defer s.observe("store.followers", time.Now())
	if follows, err := s.store.FollowerPubkeys(ctx, pubkey, limit); err == nil && len(follows) > 0 {
		return follows
	}
	events, _ := s.store.FollowersOf(ctx, pubkey, limit)
	seen := make(map[string]bool)
	var followers []string
	for _, event := range events {
		if event.PubKey == "" || seen[event.PubKey] {
			continue
		}
		seen[event.PubKey] = true
		followers = append(followers, event.PubKey)
	}
	return followers
}

const followListPageSize = 100

func (s *Server) followingList(ctx context.Context, pubkey string, query string, page int) FollowListView {
	return s.followList(ctx, pubkey, query, page, true)
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

func (s *Server) followListFallback(ctx context.Context, pubkey string, query string, page int, following bool) FollowListView {
	var all []string
	if following {
		all = s.following(ctx, pubkey)
	} else {
		all = s.followers(ctx, pubkey, 500)
	}
	filtered := all
	if query != "" {
		summaries, _ := s.store.ProfileSummariesByPubkeys(ctx, all)
		filtered = filtered[:0]
		for _, pubkey := range all {
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
	return buildFollowListView(items, query, page, total, total, false)
}

func buildFollowListView(items []string, query string, page int, filteredTotal int, cachedTotal int, cachedExact bool) FollowListView {
	view := FollowListView{
		Items:         items,
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
