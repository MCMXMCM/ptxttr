package httpx

import (
	"context"

	"ptxt-nstr/internal/nostrx"
)

// invalidateResolvedViewerAuthors clears in-memory and durable resolved-author
// entries for the given viewer across all WoT modes/depths.
func (s *Server) invalidateResolvedViewerAuthors(viewerPubkey string) {
	if s == nil || s.resolvedAuthors == nil || viewerPubkey == "" {
		return
	}
	prefix := viewerPubkey + "|"
	s.resolvedAuthors.deletePrefix(prefix)
	if s.store != nil {
		_ = s.store.DeleteResolvedAuthorsDurablePrefix(context.Background(), prefix)
	}
}

func (s *Server) invalidateResolvedAuthorsForEvents(events []nostrx.Event) {
	if s == nil || len(events) == 0 {
		return
	}
	viewers := make([]string, 0, len(events))
	for _, event := range events {
		if event.Kind != nostrx.KindFollowList || event.PubKey == "" {
			continue
		}
		viewers = append(viewers, event.PubKey)
	}
	for _, viewer := range uniqueNonEmptyStable(viewers) {
		s.invalidateResolvedViewerAuthors(viewer)
	}
}
