package httpx

import (
	"context"
	"net/http"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

func anonymousRequestFromHTTP(r *http.Request) bool {
	return normalizedViewerPubkey(viewerFromRequest(r)) == ""
}

func (s *Server) defaultLoggedOutAuthorMembership(ctx context.Context) authorMembership {
	return s.cachedDefaultLoggedOutAuthorMembership(ctx, defaultLoggedOutWOTDepth)
}

func (s *Server) defaultLoggedOutThreadAuthorMembership(ctx context.Context) authorMembership {
	return s.cachedDefaultLoggedOutAuthorMembership(ctx, defaultLoggedOutThreadWOTDepth)
}

// cachedDefaultLoggedOutAuthorMembership is deliberately cache-only. Anonymous
// HTTP requests may read the small durable resolved-author record, but they do
// not run the recursive follow-graph query or trigger relay hydration. The seed
// crawler refreshes both guest scopes when its locally materialized graph grows.
func (s *Server) cachedDefaultLoggedOutAuthorMembership(ctx context.Context, depth int) authorMembership {
	seed, err := nostrx.DecodeIdentifier(defaultLoggedOutWOTSeedNPub)
	if err != nil || seed == "" || s == nil || s.store == nil {
		return newAuthorMembership(nil)
	}
	if s.cfg.GuestSliceV2Enabled {
		if state, ok, stateErr := s.store.GetGuestSliceState(ctx, store.GuestSliceDefaultKey); stateErr == nil && ok && state.Status == store.GuestSliceStatusReady {
			authors := state.Cohort
			if depth >= defaultLoggedOutThreadWOTDepth {
				authors = state.Trust
			}
			authors = append(append([]string(nil), authors...), seed)
			authors = appendDefaultLoggedOutPinnedPubkeys(authors)
			authors = s.appendDebugAnonymousAuthors(authors)
			return newAuthorMembership(uniqueNonEmptyStable(authors))
		}
	}
	authors, _, ok := s.cachedResolvedAuthors(ctx, seed, webOfTrustOptions{Enabled: true, Depth: depth})
	if !ok {
		s.metrics.Add("anonymous.scope.cache_miss", 1)
		authors = nil
	} else {
		s.metrics.Add("anonymous.scope.cache_hit", 1)
	}
	authors = clampAuthorsWithLimit(authors, s.resolvedAuthorLimit(webOfTrustOptions{Enabled: true, Depth: depth}))
	authors = append(append([]string(nil), authors...), seed)
	authors = appendDefaultLoggedOutPinnedPubkeys(authors)
	authors = s.appendDebugAnonymousAuthors(authors)
	return newAuthorMembership(uniqueNonEmptyStable(authors))
}

// refreshDefaultLoggedOutAuthorMemberships materializes the feed (1-hop) and
// thread (3-hop) memberships from SQLite. It is called only by bootstrap/crawler
// background work, never from an anonymous request path.
func (s *Server) refreshDefaultLoggedOutAuthorMemberships(ctx context.Context) error {
	seed, err := nostrx.DecodeIdentifier(defaultLoggedOutWOTSeedNPub)
	if err != nil || seed == "" || s == nil || s.store == nil {
		return err
	}
	for _, depth := range []int{defaultLoggedOutWOTDepth, defaultLoggedOutThreadWOTDepth} {
		authors, reachErr := s.store.ReachablePubkeysWithin(ctx, seed, depth)
		if reachErr != nil {
			return reachErr
		}
		authors = clampAuthorsWithLimit(authors, s.resolvedAuthorLimit(webOfTrustOptions{Enabled: true, Depth: depth}))
		authors = uniqueNonEmptyStable(appendDefaultLoggedOutPinnedPubkeys(append(authors, seed)))
		key := resolvedAuthorsCacheKey(seed, webOfTrustOptions{Enabled: true, Depth: depth})
		now := time.Now()
		s.resolvedAuthors.put(key, authors, now)
		if err := s.store.SetResolvedAuthorsDurable(ctx, key, authors, now.Unix()); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) appendDebugAnonymousAuthors(authors []string) []string {
	if s == nil || !s.cfg.Debug {
		return authors
	}
	s.debugAnonymousAuthors.Range(func(key, _ any) bool {
		if pubkey, ok := key.(string); ok && pubkey != "" {
			authors = append(authors, pubkey)
		}
		return true
	})
	return authors
}

func (s *Server) debugAnonymousAuthorAllowed(pubkey string) bool {
	if s == nil || !s.cfg.Debug || pubkey == "" {
		return false
	}
	_, ok := s.debugAnonymousAuthors.Load(pubkey)
	return ok
}

func (s *Server) defaultLoggedOutAuthorAllowed(ctx context.Context, pubkey string) bool {
	if pubkey == "" {
		return false
	}
	return s.defaultLoggedOutAuthorMembership(ctx).Contains(pubkey)
}

func (s *Server) cachedProfileExists(ctx context.Context, pubkey string) bool {
	if s == nil || s.store == nil || pubkey == "" {
		return false
	}
	if summaries, err := s.store.ProfileSummariesByPubkeys(ctx, []string{pubkey}); err == nil {
		if _, ok := summaries[pubkey]; ok {
			return true
		}
	}
	event, err := s.store.LatestReplaceable(ctx, pubkey, nostrx.KindProfileMetadata)
	return err == nil && event != nil
}

// cachedProfileTimelineExists permits a guest to inspect an author whose note
// is already present in the bounded server cache (including authors revealed
// behind a thread's out-of-WoT disclosure). This is a single indexed SQLite
// read and never expands the follow graph or contacts a relay.
func (s *Server) cachedProfileTimelineExists(ctx context.Context, pubkey string) bool {
	if s == nil || s.store == nil || pubkey == "" {
		return false
	}
	events, err := s.store.RecentSummariesByAuthorsCursor(ctx, []string{pubkey}, noteTimelineKinds, 0, "", 1)
	return err == nil && len(events) > 0
}

func (s *Server) anonymousProfileAllowed(ctx context.Context, pubkey string) bool {
	if s.debugAnonymousAuthorAllowed(pubkey) {
		return true
	}
	if s.defaultLoggedOutThreadAuthorMembership(ctx).Contains(pubkey) && s.cachedProfileExists(ctx, pubkey) {
		return true
	}
	return s.cachedProfileTimelineExists(ctx, pubkey)
}

func (s *Server) renderAnonymousScopeNotFound(w http.ResponseWriter, r *http.Request, title, heading, message string) {
	w.Header().Set("X-Ptxt-Route-Status", string(ThreadRenderNotFound))
	if title == "Thread" {
		s.renderNotFound(w, "error_shell", ThreadErrorShellData{
			ThreadPageData: ThreadPageData{
				BasePageData: s.basePageData(r, "Thread", "thread", "feed-shell"),
				Profiles:     map[string]nostrx.Profile{},
			},
			ErrorPanelCopy: ErrorPanelCopy{
				Heading:    heading,
				Message:    message,
				ThreadRail: true,
			},
		})
		return
	}
	s.renderNotFound(w, "error_shell", ErrorPageData{
		BasePageData: s.basePageData(r, title, "feed", "feed-shell"),
		ErrorPanelCopy: ErrorPanelCopy{
			Heading: heading,
			Message: message,
		},
	})
}
