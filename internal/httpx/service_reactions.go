package httpx

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/thread"
)

func collectThreadNotesForReactions(view thread.View, static []nostrx.Event) []nostrx.Event {
	seen := make(map[string]struct{}, 64+len(static))
	out := make([]nostrx.Event, 0, len(static)+32)
	add := func(ev nostrx.Event) {
		if ev.ID == "" {
			return
		}
		if _, ok := seen[ev.ID]; ok {
			return
		}
		seen[ev.ID] = struct{}{}
		out = append(out, ev)
	}
	for _, ev := range static {
		add(ev)
	}
	if view.Root != nil {
		add(*view.Root)
	}
	if view.Selected != nil {
		add(*view.Selected)
	}
	var walk func([]thread.Node)
	walk = func(nodes []thread.Node) {
		for i := range nodes {
			add(nodes[i].Event)
			walk(nodes[i].Children)
		}
	}
	walk(view.Nodes)
	for _, n := range view.HiddenAncestors {
		add(n.Event)
	}
	if view.ParentNode != nil {
		add(view.ParentNode.Event)
	}
	if view.SelectedNode != nil {
		add(view.SelectedNode.Event)
	}
	return out
}

func (s *Server) reactionMapsForEvents(ctx context.Context, events []nostrx.Event, viewerPubkey string) (map[string]int, map[string]string) {
	ids := extractEventIDs(events)
	if len(ids) == 0 {
		return map[string]int{}, map[string]string{}
	}
	stats, viewers, err := s.store.ReactionStatsByNoteIDs(ctx, ids, strings.TrimSpace(viewerPubkey))
	if err != nil {
		slog.Warn("reaction stats by note ids failed", "notes", len(ids), "err", err)
		return map[string]int{}, map[string]string{}
	}
	totals := make(map[string]int, len(stats))
	for id, st := range stats {
		totals[id] = st.Total
	}
	return totals, viewers
}

func (s *Server) validateReactionPublishTarget(ctx context.Context, ev nostrx.Event) error {
	if ev.Kind != nostrx.KindReaction {
		return nil
	}
	targetID := nostrx.CanonicalHex64(nostrx.ReactionLastETagID(ev))
	if targetID == "" {
		return errors.New("reaction target id missing")
	}
	target, err := s.store.GetEvent(ctx, targetID)
	if err != nil || target == nil {
		return errors.New("reaction target is not in the local cache")
	}
	if target.Kind != nostrx.KindTextNote && target.Kind != nostrx.KindComment {
		return errors.New("reactions are only supported on notes and comments")
	}
	pTag := nostrx.CanonicalHex64(strings.TrimSpace(ev.FirstTagValue("p")))
	if pTag == "" {
		return errors.New("reaction requires a p tag for the target author")
	}
	if pTag != nostrx.CanonicalHex64(target.PubKey) {
		return errors.New("p tag must match the target note author")
	}
	if k := strings.TrimSpace(ev.FirstTagValue("k")); k != "" {
		want := strconv.Itoa(target.Kind)
		if k != want {
			return errors.New("k tag must match the target event kind")
		}
	}
	return nil
}
