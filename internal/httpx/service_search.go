package httpx

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"ptxt-nstr/internal/store"
)

const (
	searchScopeAll     = "all"
	searchScopeNetwork = "network"
)

type searchRequest struct {
	Pubkey     string
	SeedPubkey string
	Query      string
	Scope      string
	Cursor     int64
	CursorID   string
	Limit      int
	Relays     []string
	WoT        webOfTrustOptions
}

func (s *Server) searchData(ctx context.Context, plan searchPlan) SearchPageData {
	now := time.Now()
	if cached, ok := s.searchPageCache.get(plan.pageKey, now); ok {
		s.metrics.Add("search.cache.page.hit", 1)
		return cached
	}
	s.metrics.Add("search.cache.page.miss", 1)
	return s.searchGroup.do(plan.pageKey, func() SearchPageData {
		if cached, ok := s.searchPageCache.get(plan.pageKey, time.Now()); ok {
			return cached
		}
		searchResult := s.searchStoreResult(ctx, plan)
		events := s.hydrateTimelineEvents(ctx, searchResult.Events)
		viewer := plan.resolved.viewerForMuteFilter()
		events = s.filterFeedEventsByViewerMutes(ctx, viewer, events)
		s.warmFeedEntities(events, plan.req.Relays)
		referenced, combined := s.referencedHydration(ctx, events, plan.req.Relays)
		rt, rv := s.reactionMapsForEvents(ctx, combined, viewer)
		data := SearchPageData{
			Query:            plan.query.Normalized,
			Scope:            plan.scope,
			ScopeLabel:       searchScopeLabel(plan.scope),
			ShowScopeToggle:  plan.resolved.wotEnabled,
			Feed:             events,
			ReferencedEvents: referenced,
			ReplyCounts:      s.replyCounts(ctx, combined),
			ReactionTotals:   rt,
			ReactionViewers:  rv,
			Profiles:         s.profilesFor(ctx, combined),
			Cursor:           searchResult.NextCreatedAt,
			CursorID:         searchResult.NextID,
			HasMore:          searchResult.HasMore,
			OldestCachedAt:   searchResult.OldestCachedAt,
			LatestCachedAt:   searchResult.LatestCachedAt,
		}
		s.searchPageCache.put(plan.pageKey, data, time.Now())
		return data
	})
}

func (s *Server) searchStoreResult(ctx context.Context, plan searchPlan) store.SearchNotesResult {
	if cached, ok := s.searchStoreCache.get(plan.storeKey, time.Now()); ok {
		s.metrics.Add("search.cache.store.hit", 1)
		return cached
	}
	s.metrics.Add("search.cache.store.miss", 1)
	searchResult, err := s.store.SearchNoteSummaries(ctx, store.SearchNotesQuery{
		Text:     plan.query,
		Authors:  plan.scopedAuthors,
		Kinds:    noteTimelineKinds,
		Before:   plan.req.Cursor,
		BeforeID: plan.req.CursorID,
		Limit:    plan.req.Limit,
	})
	if err != nil {
		slog.Warn("search: store query failed", "scope", plan.scope, "err", err)
		return store.SearchNotesResult{}
	}
	s.searchStoreCache.put(plan.storeKey, searchResult, time.Now())
	return searchResult
}

func normalizeSearchScope(scope string, loggedOut bool, wotEnabled bool) string {
	if loggedOut || !wotEnabled {
		return searchScopeAll
	}
	if strings.EqualFold(strings.TrimSpace(scope), searchScopeAll) {
		return searchScopeAll
	}
	return searchScopeNetwork
}

func searchScopeLabel(scope string) string {
	if scope == searchScopeNetwork {
		return "current network"
	}
	return "all cached notes"
}
