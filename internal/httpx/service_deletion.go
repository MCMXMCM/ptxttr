package httpx

import (
	"context"
	"fmt"

	"ptxt-nstr/internal/nostrx"
)

func (s *Server) validateDeletionPublishTarget(ctx context.Context, ev nostrx.Event) error {
	if ev.Kind != nostrx.KindEventDeletion {
		return nil
	}
	author, err := nostrx.NormalizePubKey(ev.PubKey)
	if err != nil {
		return err
	}
	ids := nostrx.DeletionEventIDs(&ev)
	events := s.eventsByIDFromStore(ctx, ids)
	for _, id := range ids {
		target := events[id]
		if target == nil {
			return fmt.Errorf("note %s is not in the local cache", id)
		}
		if nostrx.CanonicalHex64(target.PubKey) != author {
			return fmt.Errorf("only the author can delete note %s", id)
		}
		if !nostrx.CanDeleteEventKind(target.Kind) {
			return fmt.Errorf("kind %d notes cannot be deleted here", target.Kind)
		}
	}
	return nil
}
