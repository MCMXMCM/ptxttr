package httpx

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"ptxt-nstr/internal/nostrx"
)

const (
	defaultLoggedOutSeedBootstrapKey = "logged-out-seed-jack"
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
	delay := seedBootstrapRetryInitial
	for {
		ctx, cancel := context.WithTimeout(s.ctx, seedBootstrapAttemptTimeout)
		err := s.prewarmDefaultLoggedOutSeed(ctx)
		cancel()
		if err == nil {
			return
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
// key on full success only. Used for the default Jack seed in production and
// for tests with a synthetic relay-backed seed.
func (s *Server) prewarmBootstrapLoggedOutSeed(ctx context.Context, seed string, depth int) error {
	if s == nil || s.store == nil || s.nostr == nil {
		return nil
	}
	if !s.store.ShouldRefresh(ctx, "bootstrap", defaultLoggedOutSeedBootstrapKey, defaultLoggedOutSeedBootstrapTTL) {
		return nil
	}
	refreshKey := "bootstrap:" + defaultLoggedOutSeedBootstrapKey
	if !s.beginRefresh(refreshKey) {
		return nil
	}
	defer s.endRefresh(refreshKey)
	if err := s.prewarmLoggedOutSeedNow(ctx, seed, depth); err != nil {
		return err
	}
	s.store.MarkRefreshed(ctx, "bootstrap", defaultLoggedOutSeedBootstrapKey)
	if seed == defaultLoggedOutWOTSeedNPub && depth == defaultLoggedOutWOTDepth {
		s.scheduleCanonicalDefaultSeedGuestFeedWarmOneShot()
	}
	return nil
}

// seedFollowListMaterialized returns nil when the seed has at least one follow
// in the store (graph-backed projection or kind-3 tags).
func (s *Server) seedFollowListMaterialized(ctx context.Context, seedPubkey string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("jack seed bootstrap: missing store")
	}
	follows, err := s.store.FollowingPubkeys(ctx, seedPubkey, 1)
	if err != nil {
		return fmt.Errorf("jack seed bootstrap: following query: %w", err)
	}
	if len(follows) > 0 {
		return nil
	}
	ev, err := s.store.LatestReplaceable(ctx, seedPubkey, nostrx.KindFollowList)
	if err != nil {
		return fmt.Errorf("jack seed bootstrap: latest kind-3: %w", err)
	}
	if ev == nil {
		return fmt.Errorf("jack seed bootstrap: follow list not in store for seed")
	}
	if len(nostrx.FollowPubkeys(ev)) == 0 {
		return fmt.Errorf("jack seed bootstrap: empty follow list for seed")
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
	if s == nil || s.store == nil || owner == "" {
		return 0, nil
	}
	if pageSize <= 0 {
		pageSize = 200
	}
	total := 0
	after := ""
	for {
		follows, err := s.store.FollowingPubkeysAfter(ctx, owner, after, pageSize)
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
		if len(follows) < pageSize {
			return total, nil
		}
	}
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
		return fmt.Errorf("jack seed bootstrap: empty seed pubkey")
	}

	s.refreshAuthor(ctx, seedPubkey, nil)
	if err := s.seedFollowListMaterialized(ctx, seedPubkey); err != nil {
		return err
	}

	pageSize := s.cfg.SeedBootstrapFollowEnqueueLimit
	if pageSize <= 0 {
		pageSize = 400
	}
	enqueued, err := s.enqueueSeedContactFrontier(ctx, seedPubkey, 3, pageSize)
	if err != nil {
		return fmt.Errorf("jack seed bootstrap: enqueue seed contacts: %w", err)
	}
	if enqueued == 0 {
		return fmt.Errorf("jack seed bootstrap: no follows to enqueue for seed frontier")
	}
	s.setSeedCrawlViewerHex(seedPubkey)
	s.invalidateResolvedSeedAuthors(seedPubkey)
	return nil
}

func (s *Server) invalidateResolvedSeedAuthors(seedPubkey string) {
	s.invalidateResolvedViewerAuthors(seedPubkey)
}
