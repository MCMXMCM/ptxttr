package httpx

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

const (
	defaultLoggedOutSeedBootstrapKey = "logged-out-seed-gigi"
	defaultLoggedOutSeedBootstrapTTL = 15 * time.Minute

	seedBootstrapAttemptTimeout = 90 * time.Second
	seedBootstrapRetryInitial   = 10 * time.Second
	seedBootstrapRetryMax       = 2 * time.Minute
)

// runDefaultSeedPrewarmLoop retries the default logged-out seed bootstrap until
// it succeeds or the server context is cancelled. Failed attempts do not call
// MarkRefreshed (see prewarmBootstrapLoggedOutSeed / prewarmLoggedOutSeedNow).
func (s *Server) runDefaultSeedPrewarmLoop() {
	if s == nil {
		return
	}
	// Materialize the request-path caches from the local follow graph even when
	// the relay bootstrap fetch log is still fresh. In particular, this creates
	// the thread-specific three-hop cache immediately after an upgrade without
	// making the first guest request traverse the graph.
	refreshCtx, refreshCancel := context.WithTimeout(s.ctx, seedBootstrapAttemptTimeout)
	if err := s.refreshDefaultLoggedOutAuthorMemberships(refreshCtx); err != nil {
		slog.Warn("initial logged-out author membership refresh failed", "err", err)
	}
	refreshCancel()

	delay := seedBootstrapRetryInitial
	for {
		var lastErr error
		seeds := allBootstrapSeedPubkeys()
		seeds = append(seeds, s.cfg.CuratedPubkeys...)
		seeds = uniqueNonEmptyStrings(seeds)
		if len(seeds) == 0 {
			seeds = []string{""}
		}
		okCount := 0
		var optionalFailed int64
		defaultOK := false
		for _, seedPK := range seeds {
			seedID := nostrx.EncodeNPub(seedPK)
			if seedPK == "" {
				seedID = defaultLoggedOutWOTSeedNPub
			}
			requireDefault := seedPK == "" || isDefaultLoggedOutSeed(seedID)
			ctx, cancel := context.WithTimeout(s.ctx, seedBootstrapAttemptTimeout)
			err := s.prewarmBootstrapLoggedOutSeed(ctx, seedID, defaultLoggedOutWOTDepth)
			cancel()
			if err != nil {
				if requireDefault {
					lastErr = err
				} else {
					optionalFailed++
					slog.Warn("optional seed bootstrap failed", "seed", seedID, "err", err)
				}
				continue
			}
			okCount++
			if requireDefault {
				defaultOK = true
			}
		}
		if defaultOK {
			if optionalFailed > 0 {
				s.metrics.Add("crawler.seed.optional_bootstrap_failed", optionalFailed)
			}
			s.metrics.Add("crawler.seed.multi_bootstrap", int64(okCount))
			// Clear pinned viewer so crawlSeedTick round-robins across seeds.
			s.setSeedCrawlViewerHex("")
			return
		}
		err := lastErr
		if err == nil {
			err = fmt.Errorf("no seeds bootstrapped")
		}
		if s.ctx.Err() != nil {
			return
		}
		slog.Warn("default seed prewarm failed; will retry", "err", err, "next_retry_in", delay)
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, seedBootstrapRetryMax)
	}
}

func (s *Server) prewarmDefaultLoggedOutSeed(ctx context.Context) error {
	return s.prewarmBootstrapLoggedOutSeed(ctx, defaultLoggedOutWOTSeedNPub, defaultLoggedOutWOTDepth)
}

// prewarmBootstrapLoggedOutSeed runs the logged-out seed bootstrap for the
// given npub/hex seed and WoT depth, then marks the shared bootstrap fetch_log
// key on full success only. Used for the default Gigi seed in production and
// for tests with a synthetic relay-backed seed.
func bootstrapFetchLogKey(seed string) string {
	pk, err := nostrx.DecodeIdentifier(seed)
	if err != nil || pk == "" {
		return defaultLoggedOutSeedBootstrapKey
	}
	return "logged-out-seed-" + pk
}

func (s *Server) prewarmBootstrapLoggedOutSeed(ctx context.Context, seed string, depth int) error {
	if s == nil || s.store == nil || s.nostr == nil {
		return nil
	}
	logKey := bootstrapFetchLogKey(seed)
	if !s.store.ShouldRefresh(ctx, "bootstrap", logKey, defaultLoggedOutSeedBootstrapTTL) {
		return nil
	}
	refreshKey := "bootstrap:" + logKey
	if !s.beginRefresh(refreshKey) {
		return nil
	}
	defer s.endRefresh(refreshKey)
	if err := s.prewarmLoggedOutSeedNow(ctx, seed, depth); err != nil {
		return err
	}
	s.store.MarkRefreshed(ctx, "bootstrap", logKey)
	if seed == defaultLoggedOutWOTSeedNPub && depth == defaultLoggedOutWOTDepth {
		s.scheduleCanonicalDefaultSeedGuestFeedWarmOneShot()
	}
	return nil
}

// seedFollowListMaterialized returns nil when the seed has at least one follow
// in the store (graph-backed projection or kind-3 tags).
func (s *Server) seedFollowListMaterialized(ctx context.Context, seedPubkey string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("gigi seed bootstrap: missing store")
	}
	follows, err := s.store.FollowingPubkeys(ctx, seedPubkey, 1)
	if err != nil {
		return fmt.Errorf("gigi seed bootstrap: following query: %w", err)
	}
	if len(follows) > 0 {
		return nil
	}
	ev, err := s.store.LatestReplaceable(ctx, seedPubkey, nostrx.KindFollowList)
	if err != nil {
		return fmt.Errorf("gigi seed bootstrap: latest kind-3: %w", err)
	}
	if ev == nil {
		return fmt.Errorf("gigi seed bootstrap: follow list not in store for seed")
	}
	if len(nostrx.FollowPubkeys(ev)) == 0 {
		return fmt.Errorf("gigi seed bootstrap: empty follow list for seed")
	}
	return nil
}

// seedContactFollowGraphPresent is true when we have a follow edge or a stored
// kind-3 replaceable (including an empty follow list).
func (s *Server) seedContactFollowGraphPresent(ctx context.Context, pubkey string) bool {
	if s == nil || s.store == nil || pubkey == "" {
		return false
	}
	follows, err := s.store.FollowingPubkeys(ctx, pubkey, 1)
	if err == nil && len(follows) > 0 {
		return true
	}
	ev, err := s.store.LatestReplaceable(ctx, pubkey, nostrx.KindFollowList)
	return err == nil && ev != nil
}

func (s *Server) enqueueSeedContactFrontier(ctx context.Context, owner string, priority int, pageSize int) (int, error) {
	return s.enqueueSeedContactFrontierCapped(ctx, owner, priority, pageSize, 0)
}

func (s *Server) enqueueSeedContactFrontierCapped(ctx context.Context, owner string, priority int, pageSize, maxTotal int) (int, error) {
	if s == nil || s.store == nil || owner == "" {
		return 0, nil
	}
	if pageSize <= 0 {
		pageSize = 200
	}
	total := 0
	after := ""
	for {
		if maxTotal > 0 && total >= maxTotal {
			return total, nil
		}
		fetchSize := pageSize
		if maxTotal > 0 {
			if remaining := maxTotal - total; remaining < fetchSize {
				fetchSize = remaining
			}
		}
		follows, err := s.store.FollowingPubkeysAfter(ctx, owner, after, fetchSize)
		if err != nil {
			return total, err
		}
		if len(follows) == 0 {
			return total, nil
		}
		if err := s.store.TouchSeedContactFrontier(ctx, follows, priority); err != nil {
			return total, err
		}
		total += len(follows)
		after = follows[len(follows)-1]
		if len(follows) < fetchSize {
			return total, nil
		}
	}
}

func (s *Server) bootstrapFollowEnqueueLimits(ctx context.Context, seed string) (pageSize, maxTotal int, skip bool) {
	pageSize = s.cfg.SeedBootstrapFollowEnqueueLimit
	if pageSize <= 0 {
		pageSize = 80
	}
	maxTotal = s.cfg.SeedBootstrapFollowEnqueueMaxTotal
	isPrimary := isDefaultLoggedOutSeed(seed)
	if !isPrimary {
		secondary := s.cfg.SeedBootstrapSecondaryMaxTotal
		if secondary <= 0 {
			secondary = 40
		}
		if maxTotal <= 0 {
			maxTotal = secondary
		} else if secondary < maxTotal {
			maxTotal = secondary
		}
	} else if maxTotal <= 0 {
		maxTotal = 80
	}
	threshold := s.cfg.SeedFrontierPauseThreshold
	if threshold <= 0 {
		threshold = 1500
	}
	if stale, err := s.store.CountStaleHydration(ctx, store.EntityTypeSeedContact, time.Now().Unix()); err == nil && stale > threshold {
		s.metrics.Add("crawler.seed.frontier_paused", 1)
		if isPrimary {
			const reduced = 20
			if maxTotal > reduced {
				maxTotal = reduced
			}
		} else {
			skip = true
		}
	}
	return pageSize, maxTotal, skip
}

func (s *Server) prewarmLoggedOutSeedNow(ctx context.Context, seed string, _ int) error {
	if s == nil || s.store == nil || s.nostr == nil {
		return nil
	}
	seedPubkey, err := nostrx.DecodeIdentifier(seed)
	if err != nil {
		return err
	}
	if seedPubkey == "" {
		return fmt.Errorf("gigi seed bootstrap: empty seed pubkey")
	}

	s.refreshAuthor(ctx, seedPubkey, nil)
	if err := s.seedFollowListMaterialized(ctx, seedPubkey); err != nil {
		return err
	}

	pageSize, maxTotal, skipEnqueue := s.bootstrapFollowEnqueueLimits(ctx, seed)
	enqueued := 0
	if !skipEnqueue {
		enqueued, err = s.enqueueSeedContactFrontierCapped(ctx, seedPubkey, 3, pageSize, maxTotal)
		if err != nil {
			return fmt.Errorf("guest seed bootstrap: enqueue seed contacts: %w", err)
		}
	}
	if isDefaultLoggedOutSeed(seed) && enqueued == 0 {
		return fmt.Errorf("guest seed bootstrap: no follows to enqueue for seed frontier")
	}
	if isDefaultLoggedOutSeed(seed) {
		if err := s.store.TouchSeedContactFrontier(ctx, defaultLoggedOutPinnedPubkeys(), 4); err != nil {
			return fmt.Errorf("guest seed bootstrap: enqueue pinned contacts: %w", err)
		}
	}
	s.setLoggedOutSeedCenterHex(seedPubkey)
	if isDefaultLoggedOutSeed(seed) || s.seedCrawlViewerPubkey() == "" {
		s.setSeedCrawlViewerHex(seedPubkey)
	}
	s.invalidateResolvedSeedAuthors(seedPubkey)
	if isDefaultLoggedOutSeed(seed) {
		if err := s.refreshDefaultLoggedOutAuthorMemberships(ctx); err != nil {
			return fmt.Errorf("guest seed bootstrap: cache guest memberships: %w", err)
		}
	}
	return nil
}

func (s *Server) invalidateResolvedSeedAuthors(seedPubkey string) {
	s.invalidateResolvedViewerAuthors(seedPubkey)
}

func loggedOutWOTSeedNPubs() []string {
	return []string{
		defaultLoggedOutWOTSeedNPub,
	}
}

// runClientModeSeedGraphBootstrap one-shot fetches kind-0/kind-3 for default
// logged-out WoT seeds without enqueueing note crawlers or guest feed warms.
func (s *Server) runClientModeSeedGraphBootstrap() {
	if s == nil || s.store == nil || s.nostr == nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Minute)
	defer cancel()
	seeds := loggedOutWOTSeedNPubs()
	for _, npub := range seeds {
		pk, err := nostrx.DecodeIdentifier(npub)
		if err != nil || pk == "" {
			continue
		}
		s.refreshAuthor(ctx, pk, nil)
	}
	if pk, err := nostrx.DecodeIdentifier(defaultLoggedOutWOTSeedNPub); err == nil && pk != "" {
		s.setLoggedOutSeedCenterHex(pk)
		if err := s.refreshDefaultLoggedOutAuthorMemberships(ctx); err != nil {
			slog.Warn("client-mode guest membership cache refresh failed", "err", err)
		}
	}
	slog.Info("client-mode seed graph bootstrap complete", "seeds", len(seeds))
}
