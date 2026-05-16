package httpx

import (
	"context"
	"log/slog"
	"strings"

	"ptxt-nstr/internal/nostrx"
)

func authorPubkeyForMuteLookup(pub string) string {
	pub = strings.TrimSpace(pub)
	if normalized, err := nostrx.NormalizePubKey(pub); err == nil {
		return normalized
	}
	return strings.ToLower(pub)
}

func (s *Server) viewerMutePubkeySet(ctx context.Context, viewer string) (map[string]struct{}, error) {
	if s == nil || s.store == nil || viewer == "" {
		return nil, nil
	}
	viewer = strings.TrimSpace(viewer)
	if normalized, err := nostrx.NormalizePubKey(viewer); err == nil {
		viewer = normalized
	}
	pubkeys, err := s.store.MutedPubkeys(ctx, viewer, nostrx.MaxMuteListTagRows)
	if err != nil {
		if s.metrics != nil {
			s.metrics.Add("viewer_mutes.load_error", 1)
		}
		return nil, err
	}
	if len(pubkeys) == 0 {
		return nil, nil
	}
	out := make(map[string]struct{}, len(pubkeys))
	for _, pk := range pubkeys {
		if pk == "" {
			continue
		}
		out[authorPubkeyForMuteLookup(pk)] = struct{}{}
	}
	return out, nil
}

// filterEventsByViewerMutedSet drops events whose author pubkey is in muted.
// Pass the map from viewerMutePubkeySet once when filtering multiple slices per request.
func (s *Server) filterEventsByViewerMutedSet(events []nostrx.Event, muted map[string]struct{}) []nostrx.Event {
	if len(events) == 0 || len(muted) == 0 {
		return events
	}
	out := events[:0]
	for _, ev := range events {
		if _, hide := muted[authorPubkeyForMuteLookup(ev.PubKey)]; hide {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// filterFeedEventsByViewerMutes hides notes/reposts from pubkeys on the viewer's
// public kind-10000 mute list (feeds, search, tag pages, profile fragments, snapshots).
// On projection read error it returns an empty slice (fail-closed for privacy); threads
// use viewerMutePubkeySet directly in handlers.go with a deliberate fail-open path.
func (s *Server) filterFeedEventsByViewerMutes(ctx context.Context, viewer string, events []nostrx.Event) []nostrx.Event {
	if len(events) == 0 || viewer == "" {
		return events
	}
	muted, err := s.viewerMutePubkeySet(ctx, viewer)
	if err != nil {
		slog.Warn("viewer mutes: MutedPubkeys failed", "viewer", short(viewer), "err", err)
		return nil
	}
	return s.filterEventsByViewerMutedSet(events, muted)
}
