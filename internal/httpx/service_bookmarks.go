package httpx

import (
	"context"
	"strings"
	"time"

	"ptxt-nstr/internal/nostrx"
)

const maxBookmarkItems = 200

// bookmarkRelayFetchTTL bounds unconditional relay fetches for kind-300 lists.
const bookmarkRelayFetchTTL = 5 * time.Minute

func (s *Server) bookmarksData(ctx context.Context, pubkey string, relays []string) BookmarksPageData {
	decoded, err := nostrx.DecodeIdentifier(strings.TrimSpace(pubkey))
	if err != nil || decoded == "" {
		return BookmarksPageData{}
	}
	list := s.bookmarksEvent(ctx, decoded, relays)
	ids := nostrx.BookmarkEventIDs(list, maxBookmarkItems)
	if len(ids) == 0 {
		return BookmarksPageData{UserPubKey: decoded}
	}
	events := s.bookmarkSummaries(ctx, ids, relays)
	events = s.hydrateTimelineEvents(ctx, events)
	s.warmFeedEntities(events, relays)
	referenced, combined := s.referencedHydration(ctx, events, relays)
	rt, rv := s.reactionMapsForEvents(ctx, combined, decoded)
	return BookmarksPageData{
		UserPubKey:       decoded,
		Items:            events,
		ReferencedEvents: referenced,
		Profiles:         s.profilesFor(ctx, combined),
		ReplyCounts:      s.replyCounts(ctx, combined),
		ReactionTotals:   rt,
		ReactionViewers:  rv,
	}
}

func (s *Server) bookmarksEvent(ctx context.Context, pubkey string, relays []string) *nostrx.Event {
	event, _ := s.store.LatestReplaceable(ctx, pubkey, nostrx.KindBookmarkList)
	if event != nil && !s.store.ShouldRefresh(ctx, "bookmark_list", pubkey, bookmarkRelayFetchTTL) {
		return event
	}
	fetched, err := s.nostr.FetchFrom(ctx, relays, nostrx.Query{
		Authors: []string{pubkey},
		Kinds:   []int{nostrx.KindBookmarkList},
		Limit:   20,
	})
	if err == nil && len(fetched) > 0 {
		_, _ = s.store.SaveEvents(ctx, fetched)
		if latest, latestErr := s.store.LatestReplaceable(ctx, pubkey, nostrx.KindBookmarkList); latestErr == nil && latest != nil {
			return latest
		}
	}
	return event
}

func (s *Server) bookmarkSummaries(ctx context.Context, ids []string, relays []string) []nostrx.Event {
	byID, _ := s.store.EventSummariesByIDs(ctx, ids)
	missing := make([]string, 0, len(ids))
	for _, id := range ids {
		if byID[id] != nil {
			continue
		}
		missing = append(missing, id)
	}
	if len(missing) > 0 {
		fetched, err := s.nostr.FetchFrom(ctx, relays, nostrx.Query{
			IDs:   missing,
			Limit: len(missing),
		})
		if err == nil && len(fetched) > 0 {
			_, _ = s.store.SaveEvents(ctx, fetched)
			refreshed, _ := s.store.EventSummariesByIDs(ctx, missing)
			for id, event := range refreshed {
				byID[id] = event
			}
		}
	}
	events := make([]nostrx.Event, 0, len(ids))
	for _, id := range ids {
		event := byID[id]
		if event == nil {
			continue
		}
		events = append(events, *event)
	}
	return events
}
