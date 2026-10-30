package httpx

import (
	"context"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

// guestSliceView is one published guest slice generation, decoded for request
// paths. The trust list can hold up to PTXT_GUEST_SLICE_TRUST_LIMIT pubkeys of
// JSON, so it is parsed and turned into memberships once per generation rather
// than on every anonymous request. A stored view is never mutated.
type guestSliceView struct {
	state store.GuestSliceState
	// feed is the 1-hop cohort scope and thread the 3-hop trust scope, each
	// with the seed, pinned profiles and debug fixtures added.
	feed       authorMembership
	thread     authorMembership
	debugEpoch uint64
}

func (v *guestSliceView) current(generation int64, debugEpoch uint64) bool {
	return v != nil && v.state.Generation == generation && v.debugEpoch == debugEpoch
}

func (v *guestSliceView) membership(depth int) authorMembership {
	if depth >= defaultLoggedOutThreadWOTDepth {
		return v.thread
	}
	return v.feed
}

// currentGuestSliceView returns the ready guest slice generation, or nil when
// none is ready. Each call validates the cached view with a generation-only
// read, so a newly published generation replaces it on first use.
func (s *Server) currentGuestSliceView(ctx context.Context) *guestSliceView {
	if s == nil || s.store == nil {
		return nil
	}
	generation, status, found, err := s.store.GetGuestSliceGeneration(ctx, store.GuestSliceDefaultKey)
	if err != nil || !found || status != store.GuestSliceStatusReady {
		return nil
	}
	debugEpoch := s.debugAnonymousEpoch.Load()
	if view := s.guestSliceCache.Load(); view.current(generation, debugEpoch) {
		return view
	}
	s.guestSliceCacheMu.Lock()
	defer s.guestSliceCacheMu.Unlock()
	if view := s.guestSliceCache.Load(); view.current(generation, debugEpoch) {
		return view
	}
	state, found, err := s.store.GetGuestSliceState(ctx, store.GuestSliceDefaultKey)
	if err != nil || !found || state.Status != store.GuestSliceStatusReady {
		return nil
	}
	view := &guestSliceView{
		state:      state,
		feed:       s.guestSliceMembership(state.Cohort),
		debugEpoch: debugEpoch,
	}
	// Most generations keep the trust list, so keep its membership too.
	if previous := s.guestSliceCache.Load(); previous != nil && previous.debugEpoch == debugEpoch &&
		state.TrustFingerprint != "" && previous.state.TrustFingerprint == state.TrustFingerprint {
		view.thread = previous.thread
	} else {
		view.thread = s.guestSliceMembership(state.Trust)
	}
	s.guestSliceCache.Store(view)
	return view
}

// guestSliceMembership adds the authors every guest scope includes besides the
// published cohort or trust list.
func (s *Server) guestSliceMembership(authors []string) authorMembership {
	seed, _ := nostrx.DecodeIdentifier(defaultLoggedOutWOTSeedNPub)
	scope := append(append([]string(nil), authors...), seed)
	scope = appendDefaultLoggedOutPinnedPubkeys(scope)
	scope = s.appendDebugAnonymousAuthors(scope)
	return newAuthorMembership(uniqueNonEmptyStable(scope))
}
