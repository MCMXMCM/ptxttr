package httpx

import (
	"context"
	"log/slog"
	"time"

	"ptxt-nstr/internal/store"
)

const (
	knownViewerHydrationFailBackoff    = 45 * time.Second
	knownViewerHydrationSuccessBackoff = 15 * time.Minute
)

func (s *Server) runViewerCrawler() {
	if s == nil || s.store == nil || !s.cfg.ViewerCrawlerEnabled {
		return
	}
	interval := s.cfg.ViewerCrawlerInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	for {
		s.tryRunMaintenanceWork(maintenanceLaneViewer, func() {
			s.runWithRelayWriteBudget(s.ctx, "crawler.viewer", s.crawlViewerTick)
		})
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func (s *Server) crawlViewerTick() {
	s.metrics.Add("crawler.viewer.tick", 1)
	defer s.observe("crawler.viewer.tick_ms", time.Now())
	batchSize := s.cfg.ViewerCrawlerBatch
	if batchSize <= 0 {
		batchSize = 8
	}
	maxFail := s.cfg.SeedContactMaxFailCount
	if maxFail <= 0 {
		maxFail = 12
	}
	targets, err := s.store.StaleKnownViewerBatch(s.ctx, time.Now().Unix(), batchSize, maxFail)
	if err != nil {
		slog.Debug("viewer crawler stale batch failed", "err", err)
		s.metrics.Add("crawler.viewer.query_error", 1)
		return
	}
	if len(targets) == 0 {
		s.metrics.Add("crawler.viewer.skipped", 1)
		return
	}

	graphLimit := s.cfg.ViewerCrawlerFollowEnqueuePerTick
	if graphLimit <= 0 {
		graphLimit = 80
	}
	timeout := seedCrawlerPerTickTimeout(s.cfg.RequestTimeout, len(targets)*(graphLimit+1))
	ctx, cancel := context.WithTimeout(s.ctx, timeout)
	defer cancel()

	fetchLimit := s.cfg.SeedCrawlerFetchLimit
	if fetchLimit <= 0 {
		fetchLimit = 60
	}
	var noteSince int64
	if lb := s.cfg.SeedCrawlerAuthorNoteLookback; lb > 0 {
		noteSince = time.Now().Add(-lb).Unix()
	}
	replyWarmLimit := s.cfg.ViewerCrawlerReplyWarmLimit
	if replyWarmLimit <= 0 {
		replyWarmLimit = 24
	}

	var refreshEvents int64
	for _, target := range targets {
		if target.EntityID == "" {
			continue
		}
		viewer := target.EntityID
		if ctx.Err() != nil {
			break
		}

		// Refresh the viewer first so a cold cache can discover direct follows.
		// Owners within two hops are then rotated through author refreshes; their
		// kind-3 lists materialize the complete third hop in follow_edges.
		s.refreshAuthor(ctx, viewer, nil)
		graphOwners, graphErr := s.viewerCrawlerAuthors(ctx, viewer, store.MaxDepth-1, false)
		if graphErr != nil {
			_ = s.store.MarkHydrationAttempt(ctx, store.EntityTypeKnownViewer, viewer, false, knownViewerHydrationFailBackoff)
			s.metrics.Add("crawler.viewer.refresh_error", 1)
			continue
		}
		selectedGraphOwners := rotateHotFeedAuthors(graphOwners, graphLimit, s.viewerGraphCursor.Add(1))
		for _, author := range selectedGraphOwners {
			if ctx.Err() != nil {
				break
			}
			s.refreshAuthor(ctx, author, nil)
		}
		s.metrics.Add("crawler.viewer.graph_authors", int64(len(selectedGraphOwners)))

		cohort, cohortErr := s.viewerCrawlerAuthors(ctx, viewer, store.MaxDepth, true)
		if cohortErr != nil || len(cohort) == 0 {
			_ = s.store.MarkHydrationAttempt(ctx, store.EntityTypeKnownViewer, viewer, false, knownViewerHydrationFailBackoff)
			s.metrics.Add("crawler.viewer.refresh_error", 1)
			continue
		}
		authorLimit := hotFeedAuthorLimit(s.cfg.HotFeedCrawlerAuthorLimit)
		selectedAuthors := rotateHotFeedAuthors(cohort, authorLimit, s.viewerNoteCursor.Add(1))
		s.metrics.Add("crawler.viewer.cohort_authors", int64(len(cohort)))
		s.metrics.Add("crawler.viewer.note_authors", int64(len(selectedAuthors)))

		s.refreshHotFeedRelayHints(ctx, selectedAuthors, nil)
		relays := s.filterCrawlerRelays(s.outboxSeedRelays(ctx, viewer, selectedAuthors, nil))
		if len(relays) == 0 {
			relays = s.crawlRelays(nil)
		}
		n := s.refreshRecent(ctx, viewer, selectedAuthors, 0, fetchLimit, relays, noteSince)
		if n > 0 {
			refreshEvents += int64(n)
		}

		recent, qerr := s.store.RecentSummariesByAuthorsCursor(ctx, selectedAuthors, noteTimelineKinds, 0, "", replyWarmLimit)
		if qerr == nil && len(recent) > 0 {
			s.warmThreadsFromRecentSummaries(viewer, relays, recent, replyWarmLimit)
		}

		// A valid empty relay response is still a successful crawl. The graph and
		// routing data were refreshed even when nobody in this slice posted notes.
		success := ctx.Err() == nil && qerr == nil
		if !success {
			_ = s.store.MarkHydrationAttempt(ctx, store.EntityTypeKnownViewer, viewer, false, knownViewerHydrationFailBackoff)
			s.metrics.Add("crawler.viewer.refresh_error", 1)
			continue
		}
		_ = s.store.MarkHydrationAttempt(ctx, store.EntityTypeKnownViewer, viewer, true, knownViewerHydrationSuccessBackoff)
	}
	s.metrics.Add("crawler.viewer.refresh_events", refreshEvents)
}

// viewerCrawlerAuthors returns the locally materialized follow graph at depth.
// The viewer is optionally included for note crawling but omitted while
// refreshing graph owners because crawlViewerTick refreshes it first.
func (s *Server) viewerCrawlerAuthors(ctx context.Context, viewer string, depth int, includeViewer bool) ([]string, error) {
	if s == nil || s.store == nil || viewer == "" {
		return nil, nil
	}
	authors, err := s.store.ReachablePubkeysWithin(ctx, viewer, depth)
	if err != nil {
		return nil, err
	}
	authors = filterValidFollowPubkeys(authors)
	if includeViewer {
		authors = append(authors, viewer)
	}
	return uniqueNonEmptyStrings(authors), nil
}

func (s *Server) touchKnownViewer(ctx context.Context, pubkey string) {
	if s == nil || s.store == nil || pubkey == "" {
		return
	}
	_ = s.store.TouchKnownViewer(ctx, pubkey, 3)
	max := s.cfg.KnownViewerMax
	if max <= 0 {
		max = 512
	}
	_ = s.store.TrimKnownViewers(ctx, max)
}
