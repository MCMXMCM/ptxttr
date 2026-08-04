package httpx

import (
	"context"
	"log/slog"
	"time"

	"ptxt-nstr/internal/store"
)

const (
	knownViewerHydrationFailBackoff    = 45 * time.Second
	knownViewerHydrationSuccessBackoff = 30 * time.Second
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
		if s.desktopBackgroundMode() == desktopBackgroundReduced {
			s.crawlViewerReduced(ctx, viewer, fetchLimit, noteSince)
			continue
		}

		// Direct follows stay hot continuously. Deeper graph owners and their note
		// cohorts are admitted on durable tier-specific intervals.
		directMetadataTTL := positiveDuration(s.cfg.ViewerCrawlerDirectMetadataInterval, 15*time.Minute)
		s.refreshAuthorWithTTL(ctx, viewer, nil, directMetadataTTL)
		directAuthors, graphErr := s.viewerCrawlerAuthors(ctx, viewer, 1, true)
		if graphErr != nil || len(directAuthors) == 0 {
			_ = s.store.MarkHydrationAttempt(ctx, store.EntityTypeKnownViewer, viewer, false, knownViewerHydrationFailBackoff)
			s.metrics.Add("crawler.viewer.refresh_error", 1)
			continue
		}
		noteAuthors := append([]string(nil), directAuthors...)
		refreshDepth := 0
		if s.store.ShouldRefresh(ctx, "viewer_graph_d2", viewer, positiveDuration(s.cfg.ViewerCrawlerDegreeTwoInterval, 5*time.Minute)) {
			refreshDepth = 1
			if degreeTwo, err := s.viewerCrawlerAuthors(ctx, viewer, 2, true); err == nil {
				noteAuthors = append(noteAuthors, degreeTwo...)
			}
		}
		if s.store.ShouldRefresh(ctx, "viewer_graph_d3", viewer, positiveDuration(s.cfg.ViewerCrawlerDegreeThreeInterval, 30*time.Minute)) {
			refreshDepth = 2
			if degreeThree, err := s.viewerCrawlerAuthors(ctx, viewer, store.MaxDepth, true); err == nil {
				noteAuthors = append(noteAuthors, degreeThree...)
			}
		}
		directOwners, graphErr := s.viewerCrawlerAuthors(ctx, viewer, 1, false)
		if graphErr != nil {
			_ = s.store.MarkHydrationAttempt(ctx, store.EntityTypeKnownViewer, viewer, false, knownViewerHydrationFailBackoff)
			s.metrics.Add("crawler.viewer.refresh_error", 1)
			continue
		}
		selectedDirectOwners := rotateHotFeedAuthors(directOwners, graphLimit, s.viewerGraphCursor.Add(1))
		for _, author := range selectedDirectOwners {
			if ctx.Err() != nil {
				break
			}
			s.refreshAuthorWithTTL(ctx, author, nil, directMetadataTTL)
		}
		metadataRefreshes := len(selectedDirectOwners)
		degreeTwoOwners := []string(nil)
		degreeTwoMetadataTTL := positiveDuration(s.cfg.ViewerCrawlerDegreeTwoMetadataInterval, 6*time.Hour)
		if s.store.ShouldRefresh(ctx, "viewer_graph_meta_d2", viewer, degreeTwoMetadataTTL) {
			if owners, err := s.viewerCrawlerAuthors(ctx, viewer, 2, false); err == nil {
				degreeTwoOwners = excludingStrings(owners, directOwners)
				selected := rotateHotFeedAuthors(degreeTwoOwners, graphLimit, s.viewerGraphCursor.Add(1))
				for _, author := range selected {
					if ctx.Err() != nil {
						break
					}
					s.refreshAuthorWithTTL(ctx, author, nil, degreeTwoMetadataTTL)
				}
				metadataRefreshes += len(selected)
				s.store.MarkRefreshed(ctx, "viewer_graph_meta_d2", viewer)
			}
		}
		degreeThreeMetadataTTL := positiveDuration(s.cfg.ViewerCrawlerDegreeThreeMetadataInterval, 24*time.Hour)
		if s.store.ShouldRefresh(ctx, "viewer_graph_meta_d3", viewer, degreeThreeMetadataTTL) {
			if owners, err := s.viewerCrawlerAuthors(ctx, viewer, store.MaxDepth, false); err == nil {
				shallower, _ := s.viewerCrawlerAuthors(ctx, viewer, 2, false)
				selected := rotateHotFeedAuthors(excludingStrings(owners, shallower), graphLimit, s.viewerGraphCursor.Add(1))
				for _, author := range selected {
					if ctx.Err() != nil {
						break
					}
					s.refreshAuthorWithTTL(ctx, author, nil, degreeThreeMetadataTTL)
				}
				metadataRefreshes += len(selected)
				s.store.MarkRefreshed(ctx, "viewer_graph_meta_d3", viewer)
			}
		}
		s.metrics.Add("crawler.viewer.graph_authors", int64(metadataRefreshes))

		noteAuthors = uniqueNonEmptyStrings(noteAuthors)
		authorLimit := hotFeedAuthorLimit(s.cfg.HotFeedCrawlerAuthorLimit)
		selectedAuthors := rotateHotFeedAuthors(noteAuthors, authorLimit, s.viewerNoteCursor.Add(1))
		s.metrics.Add("crawler.viewer.cohort_authors", int64(len(noteAuthors)))
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
		if refreshDepth >= 1 {
			s.store.MarkRefreshed(ctx, "viewer_graph_d2", viewer)
		}
		if refreshDepth >= 2 {
			s.store.MarkRefreshed(ctx, "viewer_graph_d3", viewer)
		}
		s.scheduleFeedSnapshotPersonalizedRebuild(feedRequest{
			Pubkey:   viewer,
			Relays:   relays,
			Limit:    30,
			SortMode: feedSortRecent,
			WoT:      webOfTrustOptions{Enabled: true, Depth: store.MaxDepth},
		})
		_ = s.store.MarkHydrationAttempt(ctx, store.EntityTypeKnownViewer, viewer, true, knownViewerHydrationSuccessBackoff)
	}
	s.metrics.Add("crawler.viewer.refresh_events", refreshEvents)
}

func (s *Server) crawlViewerReduced(ctx context.Context, viewer string, fetchLimit int, noteSince int64) {
	authors, err := s.viewerCrawlerAuthors(ctx, viewer, 1, true)
	if err != nil || len(authors) == 0 {
		_ = s.store.MarkHydrationAttempt(ctx, store.EntityTypeKnownViewer, viewer, false, knownViewerHydrationFailBackoff)
		return
	}
	authors = rotateHotFeedAuthors(authors, hotFeedAuthorLimit(s.cfg.HotFeedCrawlerAuthorLimit), s.viewerNoteCursor.Add(1))
	relays := s.filterCrawlerRelays(s.outboxSeedRelays(ctx, viewer, authors, nil))
	if len(relays) == 0 {
		relays = s.crawlRelays(nil)
	}
	result := s.refreshRecent(ctx, viewer, authors, 0, fetchLimit, relays, noteSince)
	if ctx.Err() != nil || result < 0 {
		_ = s.store.MarkHydrationAttempt(ctx, store.EntityTypeKnownViewer, viewer, false, knownViewerHydrationFailBackoff)
		s.metrics.Add("crawler.viewer.reduced_error", 1)
		return
	}
	_ = s.store.MarkHydrationAttempt(ctx, store.EntityTypeKnownViewer, viewer, true, positiveDuration(s.cfg.ViewerCrawlerReducedInterval, 5*time.Minute))
	s.metrics.Add("crawler.viewer.reduced_events", int64(result))
}

func positiveDuration(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}

func excludingStrings(values, excluded []string) []string {
	blocked := make(map[string]struct{}, len(excluded))
	for _, value := range excluded {
		blocked[value] = struct{}{}
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, found := blocked[value]; value == "" || found {
			continue
		}
		out = append(out, value)
	}
	return uniqueNonEmptyStrings(out)
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
