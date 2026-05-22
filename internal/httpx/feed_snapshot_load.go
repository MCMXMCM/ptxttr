package httpx

import (
	"context"
	"maps"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

const signedInFeedSnapshotMaxAge = 10 * time.Minute

// tryLoadFeedPageFromDurableSnapshots serves signed-in first-page requests from
// durable personalized snapshots, then canonical guest snapshots, then the
// legacy default-seed SQLite snapshot for recent+canonical relays.
func (s *Server) tryLoadFeedPageFromDurableSnapshots(ctx context.Context, req feedRequest, includeTrending bool) (FeedPageData, bool) {
	if s == nil || s.store == nil || req.Cursor != 0 || req.CursorID != "" {
		return FeedPageData{}, false
	}
	decoded, err := nostrx.DecodeIdentifier(req.Pubkey)
	if err != nil || decoded == "" {
		return FeedPageData{}, false
	}
	sortMode := normalizeFeedSort(req.SortMode)
	tf := normalizeTrendingTimeframe(req.Timeframe)
	key := signedInFeedSnapshotKey(decoded, sortMode, req.WoT, req.Relays)
	if rec, ok, err := s.store.GetFeedSnapshot(ctx, key); err == nil && ok && rec != nil && len(rec.Feed) > 0 {
		s.metrics.Add("feed.snapshot_hit", 1)
		if ts := time.Unix(rec.ComputedAtUnix, 0); !ts.IsZero() {
			age := time.Since(ts)
			if age >= 0 {
				s.metrics.Observe("feed.snapshot_age", age)
			}
			if age > signedInFeedSnapshotMaxAge {
				s.metrics.Add("feed.snapshot_stale_bypassed", 1)
				return FeedPageData{}, false
			}
		}
		data := s.baseFeedPageFromSnapshotShell(ctx, req, decoded, tf, includeTrending)
		beforeMute := len(rec.Feed)
		s.mergeSnapshotFeedAndApplyViewerMutes(ctx, decoded, &data, rec, false)
		if len(data.Feed) < min(req.Limit, beforeMute) || (len(data.Feed) < req.Limit && rec.HasMore) {
			s.metrics.Add("feed.snapshot_after_mute_count", int64(len(data.Feed)))
			s.topUpSnapshotFeedFromLive(ctx, req, includeTrending, &data)
		}
		data.FeedSort = sortMode
		return data, true
	}
	canonicalRelays := s.canonicalDefaultLoggedOutRelays()
	guestKey := guestCanonicalFeedSnapshotKey(sortMode, canonicalRelays)
	if rec, ok, err := s.store.GetFeedSnapshot(ctx, guestKey); err == nil && ok && rec != nil && len(rec.Feed) > 0 {
		s.metrics.Add("feed.snapshot_starter_served", 1)
		data := s.baseFeedPageFromSnapshotShell(ctx, req, decoded, tf, includeTrending)
		s.mergeSnapshotFeedAndApplyViewerMutes(ctx, decoded, &data, rec, true)
		data.FeedSort = sortMode
		return data, true
	}
	if sortMode == feedSortRecent {
		snap, ok, err := s.store.GetDefaultSeedGuestFeedSnapshot(ctx)
		if err == nil && ok && snap != nil && len(snap.Feed) > 0 {
			s.metrics.Add("feed.snapshot_starter_served", 1)
			data := s.baseFeedPageFromSnapshotShell(ctx, req, decoded, tf, includeTrending)
			s.mergeSnapshotFeedAndApplyViewerMutes(ctx, decoded, &data, defaultSeedGuestSnapToFeedSnapshotRecord(snap), true)
			data.FeedSort = sortMode
			return data, true
		}
	}
	return FeedPageData{}, false
}

func (s *Server) mergeSnapshotFeedAndApplyViewerMutes(ctx context.Context, viewer string, data *FeedPageData, rec *store.FeedSnapshotRecord, starter bool) {
	mergeFeedSnapshotRecordIntoFeedPageData(data, rec, starter)
	data.Feed = s.filterFeedEventsByViewerMutes(ctx, viewer, data.Feed)
}

func (s *Server) topUpSnapshotFeedFromLive(ctx context.Context, req feedRequest, includeTrending bool, data *FeedPageData) {
	if s == nil || data == nil || req.Limit <= 0 || len(data.Feed) >= req.Limit {
		return
	}
	live := s.feedPageDataEx(ctx, req, includeTrending, feedPageDataOptions{})
	if len(live.Feed) == 0 {
		return
	}
	seen := eventIDSet(data.Feed, len(data.Feed)+len(live.Feed))
	var overflow bool
	data.Feed, overflow = appendUniqueEventsByID(data.Feed, live.Feed, seen, req.Limit)
	data.HasMore = data.HasMore || live.HasMore || overflow
	if len(data.Feed) > 0 {
		last := data.Feed[len(data.Feed)-1]
		data.Cursor = last.CreatedAt
		data.CursorID = last.ID
	}
	mergeFeedPageSupplemental(data, &live)
	s.metrics.Add("feed.snapshot_live_topup", 1)
}

func mergeFeedPageSupplemental(dst, src *FeedPageData) {
	if dst == nil || src == nil {
		return
	}
	dst.ReferencedEvents = mergeMap(dst.ReferencedEvents, src.ReferencedEvents)
	dst.ReplyCounts = mergeMap(dst.ReplyCounts, src.ReplyCounts)
	dst.ReactionTotals = mergeMap(dst.ReactionTotals, src.ReactionTotals)
	dst.ReactionViewers = mergeMap(dst.ReactionViewers, src.ReactionViewers)
	dst.Profiles = mergeMap(dst.Profiles, src.Profiles)
}

func mergeMap[K comparable, V any](dst, src map[K]V) map[K]V {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = make(map[K]V, len(src))
	}
	maps.Copy(dst, src)
	return dst
}

func (s *Server) baseFeedPageFromSnapshotShell(ctx context.Context, req feedRequest, viewerHex string, timeframe string, includeTrending bool) FeedPageData {
	data := FeedPageData{
		BasePageData:                BasePageData{},
		UserPubKey:                  viewerHex,
		UserNPub:                    nostrx.EncodeNPub(viewerHex),
		DefaultFeed:                 false,
		Relays:                      req.Relays,
		WebOfTrustEnabled:           req.WoT.Enabled,
		WebOfTrustDepth:             req.WoT.Depth,
		LoggedOutWOTSeedDisplayName: loggedOutWOTSeedDisplayName(req.SeedPubkey),
		ReferencedEvents:            map[string]nostrx.Event{},
		ReplyCounts:                 map[string]int{},
		ReactionTotals:              map[string]int{},
		ReactionViewers:             map[string]string{},
		Profiles:                    map[string]nostrx.Profile{},
		TrendingTimeframe:           timeframe,
	}
	if includeTrending {
		// Cache-only: never block first paint on synchronous trending recompute.
		resolved := s.resolveRequestAuthors(ctx, req.Pubkey, req.SeedPubkey, req.Relays, req.WoT)
		trendCohort, trendAuthors := resolved.trendingScope()
		data.Trending = s.trendingData(ctx, timeframe, trendCohort, trendAuthors, req.Relays, true)
		profEvents := make([]nostrx.Event, 0, len(data.Trending))
		for _, item := range data.Trending {
			profEvents = append(profEvents, item.Event)
		}
		for pk, p := range s.profilesFor(ctx, profEvents) {
			data.Profiles[pk] = p
		}
	}
	return data
}

func (s *Server) scheduleFeedSnapshotPersonalizedRebuild(req feedRequest) {
	if s == nil {
		return
	}
	decoded, err := nostrx.DecodeIdentifier(req.Pubkey)
	if err != nil || decoded == "" {
		return
	}
	lockKey := "feed_snap_rebuild:" + signedInFeedSnapshotKey(decoded, normalizeFeedSort(req.SortMode), req.WoT, req.Relays)
	if !s.beginRefresh(lockKey) {
		return
	}
	reqCopy := req
	s.runBackgroundUserAsync(func() {
		defer s.endRefresh(lockKey)
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		full := s.feedPageDataEx(ctx, reqCopy, true, feedPageDataOptions{})
		if len(full.Feed) == 0 || s.store == nil {
			return
		}
		resolved := s.resolveRequestAuthors(ctx, reqCopy.Pubkey, reqCopy.SeedPubkey, reqCopy.Relays, reqCopy.WoT)
		if resolved.loggedOut || resolved.userPubkey == "" {
			return
		}
		sk := signedInFeedSnapshotKey(resolved.userPubkey, normalizeFeedSort(reqCopy.SortMode), reqCopy.WoT, reqCopy.Relays)
		rec := feedSnapshotRecordFromFeedPageData(reqCopy, &full, false)
		if rec == nil {
			return
		}
		rec.Version = feedSnapshotRecordVersion
		if err := s.store.SetFeedSnapshot(ctx, sk, rec); err == nil {
			s.metrics.Add("feed.snapshot_rebuild_persist", 1)
		}
	})
}

func (s *Server) maybePersistFeedSnapshots(ctx context.Context, req feedRequest, resolved requestAuthors, data *FeedPageData) {
	if s == nil || s.store == nil || data == nil || req.Cursor != 0 || req.CursorID != "" || len(data.Feed) == 0 {
		return
	}
	if resolved.loggedOut && s.isGuestCanonicalSnapshotTarget(req) {
		sm := normalizeFeedSort(req.SortMode)
		if sm != feedSortRecent {
			key := guestCanonicalFeedSnapshotKey(sm, req.Relays)
			rec := feedSnapshotRecordFromFeedPageData(req, data, false)
			if rec != nil {
				rec.Version = feedSnapshotRecordVersion
				if err := s.store.SetFeedSnapshot(ctx, key, rec); err == nil {
					s.metrics.Add("feed.snapshot_persist_guest_canonical", 1)
				}
			}
		}
		return
	}
	if !resolved.loggedOut && resolved.userPubkey != "" {
		key := signedInFeedSnapshotKey(resolved.userPubkey, normalizeFeedSort(req.SortMode), req.WoT, req.Relays)
		rec := feedSnapshotRecordFromFeedPageData(req, data, false)
		if rec != nil {
			rec.Version = feedSnapshotRecordVersion
			if err := s.store.SetFeedSnapshot(ctx, key, rec); err == nil {
				s.metrics.Add("feed.snapshot_persist_signed_in", 1)
			}
		}
	}
}
