package httpx

import (
	"context"
	"time"

	"ptxt-nstr/internal/nostrx"
)

func (s *Server) runHydrationSweeper() {
	interval := s.cfg.HydrationSweepInterval
	if interval <= 0 {
		interval = 3 * time.Minute
	}
	s.runSweeper(interval, 10*time.Second, s.sweepHydrationTargets)
}

func (s *Server) sweepHydrationTargets(ctx context.Context) {
	s.tryRunMaintenanceWork(func() {
		s.sweepHydrationTargetsBody(ctx)
	})
}

func (s *Server) sweepHydrationTargetsBody(ctx context.Context) {
	baseRelays := append(append([]string(nil), s.cfg.DefaultRelays...), s.cfg.MetadataRelays...)
	baseRelays = nostrx.NormalizeRelayList(baseRelays, nostrx.MaxRelays)
	s.seedProfileHydrationFromRecent(ctx)
	for _, batch := range []hydrationBatch{
		{entityType: "profile", limit: 8, metric: "hydration.swept.profile", warm: s.warmHydrationAuthors},
		{entityType: "noteReplies", limit: 8, metric: "hydration.swept.noteReplies", warm: s.warmHydrationThreads},
		{entityType: "followGraph", limit: 6, metric: "hydration.swept.followGraph", warm: s.warmHydrationAuthors},
		{entityType: "relayHints", limit: 6, metric: "hydration.swept.relayHints", warm: s.warmHydrationAuthors},
	} {
		s.sweepHydrationBatch(ctx, baseRelays, batch)
	}
}

func (s *Server) seedProfileHydrationFromRecent(ctx context.Context) {
	if s.recentlyActive(recentProfileSeedQuietWindow) {
		return
	}
	events, err := s.store.RecentByKinds(ctx, noteTimelineKinds, s.feedSince(), 0, "", recentProfileSeedScanLimit)
	if err != nil {
		return
	}
	seen := make(map[string]bool, len(events))
	pubkeys := make([]string, 0, len(events))
	for _, event := range events {
		if event.PubKey == "" || seen[event.PubKey] {
			continue
		}
		seen[event.PubKey] = true
		pubkeys = append(pubkeys, event.PubKey)
	}
	if len(pubkeys) > 0 {
		s.touchHydrationTargets(ctx, profileTouchTargets(pubkeys, 1))
	}
}
