package httpx

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"time"

	"ptxt-nstr/internal/store"
)

type tagRequest struct {
	Pubkey     string
	SeedPubkey string
	Tag        string
	Scope      string
	Cursor     int64
	CursorID   string
	Limit      int
	Relays     []string
	WoT        webOfTrustOptions
}

type tagPlan struct {
	req           tagRequest
	resolved      requestAuthors
	scope         string
	scopedAuthors []string
	storeKey      string
	pageKey       string
}

func (s *Server) newTagPlan(ctx context.Context, req tagRequest) tagPlan {
	resolved := s.resolveRequestAuthors(ctx, req.Pubkey, req.SeedPubkey, req.Relays, req.WoT)
	scope := normalizeSearchScope(req.Scope, resolved.loggedOut && !resolved.seedWOTEnabled, resolved.wotEnabled)
	var scopedAuthors []string
	if scope == searchScopeNetwork {
		scopedAuthors = resolved.authors
	}
	viewer := resolved.viewerForMuteFilter()
	storeKey := fmt.Sprintf("tag|%s|%s|%s|%s|%s|%d|%s|%d",
		viewer,
		scope,
		searchKindsKey,
		authorsCacheKey(scopedAuthors),
		req.Tag,
		req.Cursor,
		req.CursorID,
		req.Limit,
	)
	pageKey := storeKey + "|" + strconv.FormatBool(req.WoT.Enabled) + "|" + strconv.Itoa(req.WoT.Depth) + "|" + hashStringSlice(req.Relays)
	return tagPlan{
		req:           req,
		resolved:      resolved,
		scope:         scope,
		scopedAuthors: scopedAuthors,
		storeKey:      storeKey,
		pageKey:       pageKey,
	}
}

func (s *Server) tagData(ctx context.Context, plan tagPlan) TagPageData {
	now := time.Now()
	if cached, ok := s.tagPageCache.get(plan.pageKey, now); ok {
		s.metrics.Add("tag.cache.page.hit", 1)
		return cached
	}
	s.metrics.Add("tag.cache.page.miss", 1)
	return s.tagGroup.do(plan.pageKey, func() TagPageData {
		if cached, ok := s.tagPageCache.get(plan.pageKey, time.Now()); ok {
			return cached
		}
		tagResult := s.tagStoreResult(ctx, plan)
		events := s.hydrateTimelineEvents(ctx, tagResult.Events)
		viewer := plan.resolved.viewerForMuteFilter()
		events = s.filterFeedEventsByViewerMutes(ctx, viewer, events)
		s.warmFeedEntities(events, plan.req.Relays)
		referenced, combined := s.referencedHydration(ctx, events, plan.req.Relays)
		rt, rv := s.reactionMapsForEvents(ctx, combined, viewer)
		data := TagPageData{
			Tag:              plan.req.Tag,
			TagPath:          url.PathEscape(plan.req.Tag),
			Scope:            plan.scope,
			ScopeLabel:       searchScopeLabel(plan.scope),
			ShowScopeToggle:  plan.resolved.wotEnabled,
			Feed:             events,
			ReferencedEvents: referenced,
			ReplyCounts:      s.replyCounts(ctx, combined),
			ReactionTotals:   rt,
			ReactionViewers:  rv,
			Profiles:         s.profilesFor(ctx, combined),
			Cursor:           tagResult.NextCreatedAt,
			CursorID:         tagResult.NextID,
			HasMore:          tagResult.HasMore,
			OldestCachedAt:   tagResult.OldestCachedAt,
			LatestCachedAt:   tagResult.LatestCachedAt,
		}
		s.tagPageCache.put(plan.pageKey, data, time.Now())
		return data
	})
}

func (s *Server) tagStoreResult(ctx context.Context, plan tagPlan) store.SearchNotesResult {
	if cached, ok := s.tagStoreCache.get(plan.storeKey, time.Now()); ok {
		s.metrics.Add("tag.cache.store.hit", 1)
		return cached
	}
	s.metrics.Add("tag.cache.store.miss", 1)
	tagResult, err := s.store.HashtagNoteSummaries(ctx, store.HashtagNotesQuery{
		Tag:      plan.req.Tag,
		Authors:  plan.scopedAuthors,
		Kinds:    noteTimelineKinds,
		Before:   plan.req.Cursor,
		BeforeID: plan.req.CursorID,
		Limit:    plan.req.Limit,
	})
	if err != nil {
		slog.Warn("tag: store query failed", "scope", plan.scope, "err", err)
		return store.SearchNotesResult{}
	}
	s.tagStoreCache.put(plan.storeKey, tagResult, time.Now())
	return tagResult
}
