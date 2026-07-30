package httpx

import (
	"context"
	"errors"
	"hash/fnv"
	"log/slog"
	"sort"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

const (
	guestSliceActivityWindow = 90 * 24 * time.Hour
	guestSliceMetadataBatch  = 40
	guestSliceNoteBatch      = 80
	guestSliceNIP05PerTick   = 2
)

type guestRefreshTier struct {
	name     string
	minAge   time.Duration
	maxAge   time.Duration
	interval time.Duration
	since    time.Duration
}

var guestRefreshTiers = []guestRefreshTier{
	{name: "active7d", maxAge: 7 * 24 * time.Hour, interval: 5 * time.Minute, since: 90 * 24 * time.Hour},
	{name: "active30d", minAge: 7 * 24 * time.Hour, maxAge: 30 * 24 * time.Hour, interval: 30 * time.Minute, since: 90 * 24 * time.Hour},
	{name: "active90d", minAge: 30 * 24 * time.Hour, maxAge: 90 * 24 * time.Hour, interval: 6 * time.Hour, since: 90 * 24 * time.Hour},
	{name: "unknown", minAge: 90 * 24 * time.Hour, interval: 24 * time.Hour, since: 90 * 24 * time.Hour},
}

func (s *Server) runGuestSliceScheduler() {
	if s == nil || s.store == nil || s.nostr == nil || !s.cfg.GuestSliceV2Enabled {
		return
	}
	interval := s.cfg.GuestSliceInterval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	run := func() {
		budget := s.cfg.GuestSliceBudget
		if budget <= 0 || budget > 45*time.Second {
			budget = 45 * time.Second
		}
		ctx, cancel := context.WithTimeout(s.ctx, budget)
		defer cancel()
		s.tryRunMaintenanceWork(maintenanceLaneSeed, func() {
			s.runWithRelayWriteBudget(ctx, "crawler.guest_slice_v2", func() {
				if err := s.buildGuestSliceGeneration(ctx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					if errors.Is(err, store.ErrGuestSliceNotReady) {
						s.metrics.Add("guest_slice.publish_incomplete", 1)
						slog.Info("guest slice generation retained previous snapshot", "err", err)
						return
					}
					s.metrics.Add("guest_slice.error", 1)
					slog.Warn("guest slice generation failed", "err", err)
				}
			})
		})
	}
	run()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func (s *Server) buildGuestSliceGeneration(ctx context.Context) error {
	seed, err := nostrx.DecodeIdentifier(defaultLoggedOutWOTSeedNPub)
	if err != nil || seed == "" {
		return err
	}
	if err := s.seedFollowListMaterialized(ctx, seed); err != nil {
		s.refreshAuthor(ctx, seed, nil)
		if err := s.seedFollowListMaterialized(ctx, seed); err != nil {
			return err
		}
	}
	previous, hasPrevious, err := s.store.GetGuestSliceState(ctx, store.GuestSliceDefaultKey)
	if err != nil {
		return err
	}
	if !hasPrevious {
		previous = store.GuestSliceState{Key: store.GuestSliceDefaultKey, Cursors: map[string]int64{}}
	}
	if previous.Cursors == nil {
		previous.Cursors = map[string]int64{}
	}
	progress, err := s.store.GuestSliceProgress(ctx, store.GuestSliceDefaultKey)
	if err != nil {
		return err
	}
	for cursor, value := range previous.Cursors {
		if _, ok := progress[cursor]; !ok {
			progress[cursor] = value
		}
	}
	previous.Cursors = progress
	now := time.Now()
	allDirect, err := s.store.DirectFollowMembers(ctx, seed, 5000)
	if err != nil {
		return err
	}
	if len(allDirect) == 0 {
		return errors.New("guest slice seed has no direct follows")
	}
	previousDirect, _ := s.store.GuestSliceMembers(ctx, previous.Generation, "direct")
	previousByPubkey := make(map[string]store.GuestSliceMember, len(previousDirect))
	for _, member := range previousDirect {
		previousByPubkey[member.PubKey] = member
	}
	for i := range allDirect {
		allDirect[i].Role = "direct"
		if old, ok := previousByPubkey[allDirect[i].PubKey]; ok && old.MetadataCheckedAt > allDirect[i].MetadataCheckedAt {
			allDirect[i].MetadataCheckedAt = old.MetadataCheckedAt
			allDirect[i].MetadataFound = allDirect[i].MetadataFound || old.MetadataFound
		}
	}
	cohortLimit := s.cfg.GuestSliceCohortLimit
	if cohortLimit <= 0 || cohortLimit > 600 {
		cohortLimit = 600
	}
	cohortMembers, err := s.store.ActivityRankedDirectFollows(ctx, seed, now.Add(-guestSliceActivityWindow).Unix(), cohortLimit)
	if err != nil {
		return err
	}
	cohortMembers = mergePinnedGuestCohort(cohortMembers, allDirect, append([]string{seed}, defaultLoggedOutPinnedPubkeys()...), cohortLimit)
	cohort := guestMemberPubkeys(cohortMembers)
	cohortSet := make(map[string]struct{}, len(cohort))
	for _, pubkey := range cohort {
		cohortSet[pubkey] = struct{}{}
	}

	// Prioritize active cohort identities, then rotate through every direct
	// follow. A successful empty result is persisted as a recent negative.
	dueMetadata := make([]*store.GuestSliceMember, 0, guestSliceMetadataBatch)
	cutoff := now.Add(-s.cfg.GuestSliceMetadataTTL).Unix()
	if s.cfg.GuestSliceMetadataTTL <= 0 {
		cutoff = now.Add(-24 * time.Hour).Unix()
	}
	for pass := 0; pass < 2 && len(dueMetadata) < guestSliceMetadataBatch; pass++ {
		for i := range allDirect {
			_, active := cohortSet[allDirect[i].PubKey]
			if (pass == 0) != active || allDirect[i].MetadataCheckedAt > cutoff {
				continue
			}
			dueMetadata = append(dueMetadata, &allDirect[i])
			if len(dueMetadata) >= guestSliceMetadataBatch {
				break
			}
		}
	}
	if len(dueMetadata) > 0 && ctx.Err() == nil {
		authors := make([]string, 0, len(dueMetadata))
		for _, member := range dueMetadata {
			authors = append(authors, member.PubKey)
		}
		result := s.refreshCached(ctx, "guest_slice_metadata", authorsCacheKey(authors), 0,
			s.filterCrawlerRelays(s.mergeCrawlRelayTiers(s.cfg.MetadataRelays)), nostrx.Query{
				Authors: authors,
				Kinds:   []int{nostrx.KindProfileMetadata},
				Limit:   max(80, len(authors)*2),
			})
		if result >= 0 {
			profiles, _ := s.store.ProfileSummariesByPubkeys(ctx, authors)
			checkedAt := time.Now().Unix()
			for _, member := range dueMetadata {
				member.MetadataCheckedAt = checkedAt
				_, member.MetadataFound = profiles[member.PubKey]
			}
			if err := s.store.MarkGuestMetadataChecked(ctx, authors, checkedAt, s.cfg.GuestSliceMetadataTTL); err != nil {
				return err
			}
			s.refreshGuestNIP05(ctx, profiles)
		}
	}

	for _, tier := range guestRefreshTiers {
		if ctx.Err() != nil {
			break
		}
		lastRun := previous.Cursors["tier_"+tier.name+"_at"]
		if lastRun > 0 && now.Sub(time.Unix(lastRun, 0)) < tier.interval {
			continue
		}
		authors := guestTierAuthors(allDirect, tier, now, guestSliceNoteBatch, previous.Cursors["tier_"+tier.name+"_cursor"])
		if len(authors) == 0 {
			previous.Cursors["tier_"+tier.name+"_at"] = now.Unix()
			if err := s.store.SetGuestSliceProgress(ctx, store.GuestSliceDefaultKey, map[string]int64{
				"tier_" + tier.name + "_at": now.Unix(),
			}); err != nil {
				return err
			}
			continue
		}
		result := s.refreshCached(ctx, "guest_slice_"+tier.name, authorsCacheKey(authors), 0,
			s.filterCrawlerRelays(s.crawlRelays(nil)), nostrx.Query{
				Authors: authors,
				Kinds:   noteTimelineKinds,
				Since:   now.Add(-tier.since).Unix(),
				Limit:   120,
			})
		if result >= 0 {
			previous.Cursors["tier_"+tier.name+"_at"] = now.Unix()
			previous.Cursors["tier_"+tier.name+"_cursor"] += int64(len(authors))
			if err := s.store.SetGuestSliceProgress(ctx, store.GuestSliceDefaultKey, map[string]int64{
				"tier_" + tier.name + "_at":     previous.Cursors["tier_"+tier.name+"_at"],
				"tier_" + tier.name + "_cursor": previous.Cursors["tier_"+tier.name+"_cursor"],
			}); err != nil {
				return err
			}
		}
	}

	trust, err := s.store.GuestSliceBuildTrust(ctx, store.GuestSliceDefaultKey)
	if err != nil {
		return err
	}
	if len(trust) == 0 {
		trust = previous.Trust
	}
	followEvent, err := s.store.LatestReplaceable(ctx, seed, nostrx.KindFollowList)
	if err != nil {
		return err
	}
	followFingerprint := guestFollowFingerprint(followEvent)
	trustTTL := s.cfg.GuestSliceTrustTTL
	if trustTTL <= 0 {
		trustTTL = 6 * time.Hour
	}
	if len(trust) == 0 || previous.Cursors["seed_follow_fingerprint"] != followFingerprint ||
		now.Sub(time.Unix(previous.Cursors["trust_computed_at"], 0)) >= trustTTL {
		trust, err = s.store.ReachablePubkeysWithin(ctx, seed, defaultLoggedOutThreadWOTDepth)
		if err != nil {
			return err
		}
		trustLimit := s.cfg.GuestSliceTrustLimit
		if trustLimit <= 0 || trustLimit > 100000 {
			trustLimit = 100000
		}
		trust = limitedStrings(uniqueNonEmptyStable(append(trust, seed)), trustLimit)
		trust = uniqueNonEmptyStable(appendDefaultLoggedOutPinnedPubkeys(trust))
		previous.Cursors["trust_computed_at"] = now.Unix()
		previous.Cursors["seed_follow_fingerprint"] = followFingerprint
		if err := s.store.SetGuestSliceBuildTrust(ctx, store.GuestSliceDefaultKey, trust); err != nil {
			return err
		}
		if err := s.store.SetGuestSliceProgress(ctx, store.GuestSliceDefaultKey, map[string]int64{
			"trust_computed_at":       previous.Cursors["trust_computed_at"],
			"seed_follow_fingerprint": previous.Cursors["seed_follow_fingerprint"],
		}); err != nil {
			return err
		}
	}

	candidateLimit := s.cfg.GuestSliceCandidateLimit
	if candidateLimit <= 0 {
		candidateLimit = 60
	}
	recent, err := s.store.RecentSummariesByAuthorsCursor(ctx, cohort, noteTimelineKinds, 0, "", candidateLimit)
	if err != nil {
		return err
	}
	s.warmThreadsFromRecentSummaries(seed, s.canonicalDefaultLoggedOutRelays(), recent, candidateLimit)
	publishLimit := s.cfg.GuestSlicePublishLimit
	if publishLimit <= 0 || publishLimit > 30 {
		publishLimit = 30
	}
	req := s.canonicalDefaultLoggedOutGuestFeedRequest()
	req.Limit = publishLimit
	resolved := requestAuthors{
		allAuthors:      cohort,
		authors:         cohort,
		wotViewerPubkey: seed,
		loggedOut:       true,
		wotEnabled:      true,
		seedWOTEnabled:  true,
	}
	data := s.feedPageDataExResolved(ctx, req, false, feedPageDataOptions{
		lightStatsOnly:          true,
		guestCacheReadDisabled:  true,
		guestCacheWriteDisabled: true,
	}, resolved)
	if len(data.Feed) < publishLimit {
		return store.ErrGuestSliceNotReady
	}
	snap := s.feedPageDataToDefaultSeedSnapshot(req, &data)
	if snap == nil {
		return store.ErrGuestSliceNotReady
	}
	sortSnapshots := make(map[string]*store.FeedSnapshotRecord, 3)
	for _, sortMode := range []string{feedSortRecent, feedSortTrend24h, feedSortTrend7d} {
		sortReq := req
		sortReq.SortMode = sortMode
		sortData := data
		if sortMode != normalizeFeedSort(req.SortMode) {
			sortData = s.feedPageDataExResolved(ctx, sortReq, false, feedPageDataOptions{
				lightStatsOnly:          true,
				guestCacheReadDisabled:  true,
				guestCacheWriteDisabled: true,
			}, resolved)
		}
		if len(sortData.Feed) < publishLimit {
			return store.ErrGuestSliceNotReady
		}
		record := feedSnapshotRecordFromFeedPageData(sortReq, &sortData, false)
		if record == nil {
			return store.ErrGuestSliceNotReady
		}
		sortSnapshots[guestCanonicalFeedSnapshotKey(sortMode, sortReq.Relays)] = record
	}
	generation := previous.Generation + 1
	if generation <= 0 {
		generation = 1
	}
	members := append([]store.GuestSliceMember(nil), allDirect...)
	checkedByPubkey := make(map[string]store.GuestSliceMember, len(allDirect))
	for _, member := range allDirect {
		checkedByPubkey[member.PubKey] = member
	}
	for i := range cohortMembers {
		cohortMembers[i].Role = store.GuestSliceRoleCohort
		if checked, ok := checkedByPubkey[cohortMembers[i].PubKey]; ok {
			cohortMembers[i].MetadataCheckedAt = max(cohortMembers[i].MetadataCheckedAt, checked.MetadataCheckedAt)
			cohortMembers[i].MetadataFound = cohortMembers[i].MetadataFound || checked.MetadataFound
		}
	}
	members = append(members, cohortMembers...)
	state := store.GuestSliceState{
		Key:        store.GuestSliceDefaultKey,
		Generation: generation,
		SeedPubKey: seed,
		Cohort:     cohort,
		Trust:      trust,
		Cursors:    previous.Cursors,
	}
	readiness, err := s.store.PublishGuestSliceSnapshots(ctx, state, members, snap, sortSnapshots, store.GuestSlicePinTTL)
	if err != nil {
		if errors.Is(err, store.ErrGuestSliceNotReady) {
			missingThreadIDs := uniqueNonEmptyStable(append(append(append(append(
				[]string(nil), readiness.MissingEvents...), readiness.MissingRoots...),
				readiness.MissingParents...), readiness.MissingReplyPages...))
			if len(missingThreadIDs) > 0 {
				s.warmThreadForViewer(seed, missingThreadIDs, s.canonicalDefaultLoggedOutRelays())
			}
			if len(readiness.MissingParticipants) > 0 {
				s.warmAuthors(readiness.MissingParticipants,
					s.filterCrawlerRelays(s.mergeCrawlRelayTiers(s.cfg.MetadataRelays)))
			}
			slog.Info("guest slice readiness rejected candidate",
				"missing_events", len(readiness.MissingEvents),
				"missing_roots", len(readiness.MissingRoots),
				"missing_parents", len(readiness.MissingParents),
				"missing_reply_pages", len(readiness.MissingReplyPages),
				"missing_participants", len(readiness.MissingParticipants))
		}
		return err
	}
	_, _ = s.store.DeleteNonDirectSeedContacts(ctx, seed, defaultLoggedOutPinnedPubkeys())
	s.invalidateResolvedViewerAuthors(seed)
	s.guestFeedCache.reset()
	s.anonymousHTMLCache.reset()
	s.metrics.Add("guest_slice.published", 1)
	s.metrics.Add("guest_slice.pinned_events", int64(len(readiness.Dependencies)))
	return nil
}

func guestFollowFingerprint(event *nostrx.Event) int64 {
	if event == nil {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(event.ID))
	return int64(h.Sum64() & uint64(^uint64(0)>>1))
}

func mergePinnedGuestCohort(active, direct []store.GuestSliceMember, pinned []string, limit int) []store.GuestSliceMember {
	byPubkey := make(map[string]store.GuestSliceMember, len(active)+len(pinned))
	for _, member := range active {
		byPubkey[member.PubKey] = member
	}
	directByPubkey := make(map[string]store.GuestSliceMember, len(direct))
	for _, member := range direct {
		directByPubkey[member.PubKey] = member
	}
	for _, pubkey := range pinned {
		if pubkey == "" {
			continue
		}
		if _, ok := byPubkey[pubkey]; ok {
			continue
		}
		member := directByPubkey[pubkey]
		member.PubKey = pubkey
		member.Role = store.GuestSliceRoleCohort
		byPubkey[pubkey] = member
	}
	out := make([]store.GuestSliceMember, 0, len(byPubkey))
	for _, member := range byPubkey {
		out = append(out, member)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].LatestActivityAt == out[j].LatestActivityAt {
			return out[i].PubKey < out[j].PubKey
		}
		return out[i].LatestActivityAt > out[j].LatestActivityAt
	})
	if limit > 0 && len(out) > limit {
		pinnedSet := make(map[string]struct{}, len(pinned))
		for _, pubkey := range pinned {
			pinnedSet[pubkey] = struct{}{}
		}
		kept := make([]store.GuestSliceMember, 0, limit)
		for _, member := range out {
			if _, ok := pinnedSet[member.PubKey]; ok {
				kept = append(kept, member)
			}
		}
		for _, member := range out {
			if len(kept) >= limit {
				break
			}
			if _, ok := pinnedSet[member.PubKey]; !ok {
				kept = append(kept, member)
			}
		}
		out = kept
	}
	return out
}

func guestMemberPubkeys(members []store.GuestSliceMember) []string {
	out := make([]string, 0, len(members))
	for _, member := range members {
		if member.PubKey != "" {
			out = append(out, member.PubKey)
		}
	}
	return uniqueNonEmptyStable(out)
}

func guestTierAuthors(members []store.GuestSliceMember, tier guestRefreshTier, now time.Time, limit int, cursor int64) []string {
	eligible := make([]string, 0, len(members))
	for _, member := range members {
		age := time.Duration(1<<63 - 1)
		if member.LatestActivityAt > 0 {
			age = now.Sub(time.Unix(member.LatestActivityAt, 0))
		}
		if age < tier.minAge || (tier.maxAge > 0 && age >= tier.maxAge) {
			continue
		}
		eligible = append(eligible, member.PubKey)
	}
	if len(eligible) == 0 {
		return nil
	}
	sort.Strings(eligible)
	if limit <= 0 || limit > len(eligible) {
		limit = len(eligible)
	}
	start := int(cursor % int64(len(eligible)))
	out := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, eligible[(start+i)%len(eligible)])
	}
	return out
}

func (s *Server) refreshGuestNIP05(ctx context.Context, profiles map[string]store.ProfileSummary) {
	checked := 0
	for pubkey, profile := range profiles {
		if checked >= guestSliceNIP05PerTick || ctx.Err() != nil || profile.NIP05 == "" {
			continue
		}
		record, ok, _ := s.store.GetNIP05Verification(ctx, profile.NIP05, pubkey)
		if ok && record.NextRetryAt > time.Now().Unix() {
			continue
		}
		status := s.verifyNIP05(ctx, profile.NIP05, pubkey)
		now := time.Now()
		_ = s.store.PutNIP05Verification(ctx, store.NIP05VerificationRecord{
			Identifier:  profile.NIP05,
			PubKey:      pubkey,
			Status:      string(status),
			CheckedAt:   now.Unix(),
			NextRetryAt: now.Add(24 * time.Hour).Unix(),
		})
		checked++
	}
}

func (s *Server) runGuestWALMonitor() {
	if s == nil || s.store == nil || !s.cfg.GuestSliceV2Enabled {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			size := s.store.WALSizeBytes()
			s.metrics.Set("store.wal_bytes", size)
			if size <= 256<<20 {
				continue
			}
			s.metrics.Add("store.wal_over_256m", 1)
			if s.foregroundBusy() {
				continue
			}
			ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
			if err := s.store.CheckpointWAL(ctx); err != nil {
				slog.Warn("quiet WAL checkpoint failed", "wal_bytes", size, "err", err)
			}
			cancel()
		}
	}
}
