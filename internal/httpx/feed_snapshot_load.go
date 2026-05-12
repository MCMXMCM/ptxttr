package httpx

import (
	"context"
	"time"

	"ptxt-nstr/internal/nostrx"
)

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
		if ts := time.Unix(rec.ComputedAtUnix, 0); time.Since(ts) > 10*time.Minute {
			s.metrics.Add("feed.snapshot_stale_served", 1)
		}
		data := s.baseFeedPageFromSnapshotShell(ctx, req, decoded, tf, includeTrending)
		mergeFeedSnapshotRecordIntoFeedPageData(&data, rec, false)
		data.FeedSort = sortMode
		return data, true
	}
	canonicalRelays := s.canonicalDefaultLoggedOutRelays()
	guestKey := guestCanonicalFeedSnapshotKey(sortMode, canonicalRelays)
	if rec, ok, err := s.store.GetFeedSnapshot(ctx, guestKey); err == nil && ok && rec != nil && len(rec.Feed) > 0 {
		s.metrics.Add("feed.snapshot_starter_served", 1)
		data := s.baseFeedPageFromSnapshotShell(ctx, req, decoded, tf, includeTrending)
		mergeFeedSnapshotRecordIntoFeedPageData(&data, rec, true)
		data.FeedSort = sortMode
		return data, true
	}
	if sortMode == feedSortRecent {
		snap, ok, err := s.store.GetDefaultSeedGuestFeedSnapshot(ctx)
		if err == nil && ok && snap != nil && len(snap.Feed) > 0 {
			s.metrics.Add("feed.snapshot_starter_served", 1)
			data := s.baseFeedPageFromSnapshotShell(ctx, req, decoded, tf, includeTrending)
			mergeFeedSnapshotRecordIntoFeedPageData(&data, defaultSeedGuestSnapToFeedSnapshotRecord(snap), true)
			data.FeedSort = sortMode
			return data, true
		}
	}
	return FeedPageData{}, false
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
		data.Trending = s.trendingData(ctx, timeframe, req.Relays, true)
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
