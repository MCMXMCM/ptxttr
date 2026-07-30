package httpx

import (
	"context"
	"log/slog"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

const seedContactHydrationFailBackoff = 45 * time.Second

const seedContactHydrationSuccessBackoff = 24 * time.Hour

func (s *Server) runSeedCrawler() {
	if s == nil || s.store == nil || !s.cfg.SeedCrawlerEnabled {
		return
	}
	interval := s.cfg.SeedCrawlerInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	for {
		s.tryRunMaintenanceWork(maintenanceLaneSeed, func() {
			s.runWithRelayWriteBudget(s.ctx, "crawler.seed", s.crawlSeedTick)
		})
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func (s *Server) crawlSeedTick() {
	defer s.observe("crawler.seed.tick", time.Now())
	viewer := s.seedCrawlViewerPubkey()
	if viewer == "" {
		s.metrics.Add("crawler.seed.skipped", 1)
		return
	}

	batchSize := s.cfg.SeedCrawlerAuthorBatch
	if batchSize <= 0 {
		batchSize = 32
	}
	maxFail := s.cfg.SeedContactMaxFailCount
	if maxFail <= 0 {
		maxFail = 12
	}
	targets, err := s.store.StaleSeedContactBatch(s.ctx, time.Now().Unix(), batchSize, maxFail)
	if err != nil {
		slog.Debug("seed crawler stale batch failed", "err", err)
		s.metrics.Add("crawler.seed.query_error", 1)
		return
	}
	if isDefaultLoggedOutSeed(viewer) {
		targets = appendPinnedSeedContactTargets(targets)
	}
	if len(targets) == 0 {
		s.metrics.Add("crawler.seed.skipped", 1)
		return
	}

	timeout := seedCrawlerPerTickTimeout(s.cfg.RequestTimeout, len(targets))
	ctx, cancel := context.WithTimeout(s.ctx, timeout)
	defer cancel()

	fetchLimit := s.cfg.SeedCrawlerFetchLimit
	if fetchLimit <= 0 {
		fetchLimit = 100
	}
	var noteSince int64
	if lb := s.cfg.SeedCrawlerAuthorNoteLookback; lb > 0 {
		noteSince = time.Now().Add(-lb).Unix()
	}
	replyWarmLimit := s.cfg.SeedCrawlerReplyWarmLimit
	if replyWarmLimit <= 0 {
		replyWarmLimit = 24
	}
	enqueuePageSize := s.cfg.SeedContactFollowEnqueuePerTick
	if enqueuePageSize <= 0 {
		enqueuePageSize = 120
	}

	var refreshEvents int64
	graphExpanded := false
	for _, target := range targets {
		if target.EntityID == "" {
			continue
		}
		pubkey := target.EntityID
		if ctx.Err() != nil {
			break
		}

		followsBefore := 0
		if follows, err := s.store.FollowingPubkeys(ctx, pubkey, 8); err == nil {
			followsBefore = len(follows)
		}
		s.refreshAuthor(ctx, pubkey, nil)
		if follows, err := s.store.FollowingPubkeys(ctx, pubkey, 8); err == nil && len(follows) > followsBefore {
			if _, err := s.enqueueSeedContactFrontier(ctx, pubkey, 2, enqueuePageSize); err != nil {
				slog.Debug("seed contact enqueue follows", "pubkey", pubkey, "err", err)
			}
			s.invalidateLoggedOutSeedCenter()
			graphExpanded = true
		}

		relays := s.filterCrawlerRelays(s.outboxSeedRelays(ctx, viewer, []string{pubkey}, nil))
		if len(relays) == 0 {
			relays = s.crawlRelays(nil)
		}
		n := s.refreshRecent(ctx, viewer, []string{pubkey}, 0, fetchLimit, relays, noteSince)
		if n > 0 {
			refreshEvents += int64(n)
		}

		followReady := s.seedFollowListMaterialized(ctx, pubkey) == nil
		noteProgress := n > 0
		graphOK := followReady
		if !graphOK {
			graphOK = s.seedContactFollowGraphPresent(ctx, pubkey)
		}
		success := ctx.Err() == nil && (noteProgress || graphOK)

		if !success {
			_ = s.store.MarkHydrationAttempt(ctx, store.EntityTypeSeedContact, pubkey, false, seedContactHydrationFailBackoff)
			s.metrics.Add("crawler.seed.refresh_error", 1)
			continue
		}

		recent, qerr := s.store.RecentSummariesByAuthorsCursor(ctx, []string{pubkey}, noteTimelineKinds, 0, "", replyWarmLimit)
		if qerr != nil {
			slog.Debug("seed crawler recent summaries", "err", qerr)
		} else {
			s.warmThreadsFromRecentSummaries(viewer, relays, recent, replyWarmLimit)
		}

		if err := s.store.MarkHydrationAttempt(ctx, store.EntityTypeSeedContact, pubkey, true, seedContactHydrationSuccessBackoff); err != nil {
			slog.Debug("seed contact mark success", "pubkey", pubkey, "err", err)
		}
		if qerr == nil {
			s.metrics.Add("crawler.seed.cached_notes", int64(len(recent)))
		}
	}
	s.metrics.Add("crawler.seed.refresh_events", refreshEvents)
	if graphExpanded {
		s.invalidateLoggedOutSeedCenter()
		if isDefaultLoggedOutSeed(viewer) {
			if err := s.refreshDefaultLoggedOutAuthorMemberships(ctx); err != nil {
				s.metrics.Add("crawler.seed.membership_cache_error", 1)
				slog.Warn("seed crawler guest membership cache refresh failed", "err", err)
			}
		}
	}
	if (graphExpanded || refreshEvents > 0) && isDefaultLoggedOutSeed(viewer) {
		s.scheduleCanonicalDefaultSeedGuestFeedWarmOneShot()
	}
}

func appendPinnedSeedContactTargets(targets []store.HydrationTarget) []store.HydrationTarget {
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		if target.EntityID != "" {
			seen[target.EntityID] = struct{}{}
		}
	}
	for _, pubkey := range defaultLoggedOutPinnedPubkeys() {
		if pubkey == "" {
			continue
		}
		if _, ok := seen[pubkey]; ok {
			continue
		}
		targets = append(targets, store.HydrationTarget{
			EntityType: store.EntityTypeSeedContact,
			EntityID:   pubkey,
			Priority:   4,
		})
	}
	return targets
}

func (s *Server) setLoggedOutSeedCenterHex(pk string) {
	if s == nil || pk == "" {
		return
	}
	s.loggedOutSeedCenterMu.Lock()
	s.loggedOutSeedCenterHex = pk
	s.loggedOutSeedCenterMu.Unlock()
}

func (s *Server) getLoggedOutSeedCenter() string {
	if s == nil {
		return ""
	}
	s.loggedOutSeedCenterMu.RLock()
	defer s.loggedOutSeedCenterMu.RUnlock()
	return s.loggedOutSeedCenterHex
}

func (s *Server) invalidateLoggedOutSeedCenter() {
	if center := s.getLoggedOutSeedCenter(); center != "" {
		s.invalidateResolvedViewerAuthors(center)
		return
	}
	if center := s.seedCrawlViewerPubkey(); center != "" {
		s.invalidateResolvedViewerAuthors(center)
	}
}

func (s *Server) seedCrawlViewerPubkey() string {
	if s == nil {
		return ""
	}
	s.seedCrawlViewerMu.RLock()
	v := s.seedCrawlViewerHex
	s.seedCrawlViewerMu.RUnlock()
	if v != "" {
		return v
	}
	seeds := allBootstrapSeedPubkeys()
	if len(seeds) > 0 {
		idx := s.seedCrawlIndex.Add(1) % uint64(len(seeds))
		return seeds[idx]
	}
	pk, err := nostrx.DecodeIdentifier(defaultLoggedOutWOTSeedNPub)
	if err != nil {
		return ""
	}
	return pk
}

func (s *Server) setSeedCrawlViewerHex(pk string) {
	if s == nil {
		return
	}
	s.seedCrawlViewerMu.Lock()
	s.seedCrawlViewerHex = pk
	s.seedCrawlViewerMu.Unlock()
}

const (
	seedCrawlerTickTimeoutFloor       = 2 * time.Minute
	seedCrawlerPerAuthorTimeoutBudget = 12 * time.Second
	seedCrawlerTickTimeoutMax         = 10 * time.Minute
)

// seedCrawlerPerTickTimeout returns a context budget for each seed crawl tick.
// It is floored so cold-start Gigi hydration is not cut off by the short HTTP
// request timeout used for interactive requests. When authorCount > 0, extra
// time is added so a full batch can run refreshAuthor + sync + refreshRecent
// per author without the tick context expiring mid-loop.
func seedCrawlerPerTickTimeout(relayTimeout time.Duration, authorCount int) time.Duration {
	base := requestTimeout(relayTimeout)
	if base < seedCrawlerTickTimeoutFloor {
		base = seedCrawlerTickTimeoutFloor
	}
	if authorCount > 0 {
		add := time.Duration(authorCount) * seedCrawlerPerAuthorTimeoutBudget
		base += add
	}
	if base > seedCrawlerTickTimeoutMax {
		return seedCrawlerTickTimeoutMax
	}
	return base
}
