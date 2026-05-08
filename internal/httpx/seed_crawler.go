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
		s.tryRunMaintenanceWork(s.crawlSeedTick)
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

		s.refreshAuthor(ctx, pubkey, nil)

		relays := s.outboxSeedRelays(ctx, viewer, []string{pubkey}, nil)
		if len(relays) == 0 {
			relays = append([]string(nil), s.cfg.DefaultRelays...)
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

		if followReady {
			if _, err := s.enqueueSeedContactFrontier(ctx, pubkey, 2, enqueuePageSize); err != nil {
				slog.Debug("seed contact enqueue follows", "pubkey", pubkey, "err", err)
			}
			graphExpanded = true // batch invalidate once after the loop
		}

		recent, qerr := s.store.RecentSummariesByAuthorsCursor(ctx, []string{pubkey}, noteTimelineKinds, 0, "", replyWarmLimit)
		if qerr != nil {
			slog.Debug("seed crawler recent summaries", "err", qerr)
		} else {
			nWarm := min(replyWarmLimit, len(recent))
			ids := make([]string, 0, nWarm)
			mergedRelays := append([]string(nil), relays...)
			for i := 0; i < nWarm; i++ {
				if recent[i].ID == "" {
					continue
				}
				ids = append(ids, recent[i].ID)
				mergedRelays = append(mergedRelays, s.threadRelays(relays, recent[i])...)
			}
			if len(ids) > 0 {
				mergedRelays = nostrx.NormalizeRelayList(mergedRelays, nostrx.MaxRelays)
				s.warmThread(ids, mergedRelays)
			}
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
		s.invalidateResolvedSeedAuthors(viewer)
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
// It is floored so cold-start Jack hydration is not cut off by the short HTTP
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
