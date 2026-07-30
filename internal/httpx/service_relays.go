package httpx

import (
	"context"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

const (
	threadMinRepliesBeforeIndexer = 3
)

func (s *Server) indexerRelays() []string {
	if s == nil {
		return nil
	}
	max := s.cfg.IndexerMaxRelays
	if max <= 0 {
		max = 6
	}
	return nostrx.NormalizeRelayList(s.cfg.IndexerRelays, max)
}

// nip50Relays returns relays that accept NIP-50 search for note-id queries.
// Excludes search-only relays (e.g. search.nos.today) that reject hex note-id searches.
func (s *Server) nip50Relays() []string {
	if s == nil {
		return nil
	}
	max := s.cfg.IndexerNIP50MaxRelays
	if max <= 0 {
		max = 4
	}
	relays := s.cfg.IndexerNIP50Relays
	if len(relays) == 0 {
		relays = s.cfg.IndexerRelays
	}
	return nostrx.NormalizeRelayList(relays, max)
}

func (s *Server) trendingSearchRelays() []string {
	if s == nil {
		return nil
	}
	max := s.cfg.TrendingSearchMaxRelays
	if max <= 0 {
		max = 4
	}
	relays := s.cfg.TrendingSearchRelays
	if len(relays) == 0 {
		relays = s.cfg.IndexerRelays
	}
	return nostrx.NormalizeRelayList(relays, max)
}

func mergeRelayTiers(primary, secondary []string, max int) []string {
	if max <= 0 {
		max = nostrx.MaxRelays
	}
	seen := make(map[string]bool, len(primary)+len(secondary))
	out := make([]string, 0, len(primary)+len(secondary))
	for _, relay := range primary {
		normalized, err := nostrx.NormalizeRelayURL(relay)
		if err != nil || seen[normalized] {
			continue
		}
		seen[normalized] = true
		out = append(out, normalized)
		if len(out) >= max {
			return out
		}
	}
	for _, relay := range secondary {
		normalized, err := nostrx.NormalizeRelayURL(relay)
		if err != nil || seen[normalized] {
			continue
		}
		seen[normalized] = true
		out = append(out, normalized)
		if len(out) >= max {
			return out
		}
	}
	return out
}

func (s *Server) appendAuthorRelayHints(ctx context.Context, merged []string, pubkey string) []string {
	if s == nil || s.store == nil || pubkey == "" {
		return merged
	}
	for _, usage := range []nostrx.RelayUsage{nostrx.RelayUsageRead, nostrx.RelayUsageWrite, nostrx.RelayUsageAny} {
		hints, err := s.store.RelayHintsForPubkeyByUsage(ctx, pubkey, usage)
		if err == nil {
			merged = append(merged, hints...)
		}
	}
	return merged
}

func (s *Server) appendMentionedAuthorRelayHints(ctx context.Context, merged []string, events ...*nostrx.Event) []string {
	if s == nil || s.store == nil {
		return merged
	}
	seen := make(map[string]bool)
	pubkeys := make([]string, 0)
	for _, event := range events {
		if event == nil {
			continue
		}
		if event.PubKey != "" {
			seen[event.PubKey] = true
		}
		for _, pubkey := range replyContextTargets(*event) {
			if pubkey == "" || seen[pubkey] {
				continue
			}
			seen[pubkey] = true
			pubkeys = append(pubkeys, pubkey)
		}
	}
	hints, err := s.store.RelayHintsByUsageForPubkeys(ctx, pubkeys)
	if err != nil {
		return merged
	}
	for _, pubkey := range pubkeys {
		merged = appendRelayHintSet(merged, hints[pubkey])
	}
	return merged
}

func appendRelayHintSet(merged []string, set store.RelayHintSet) []string {
	merged = append(merged, set.Read...)
	merged = append(merged, set.Write...)
	return append(merged, set.All...)
}

// threadHydrationRelays builds the primary relay set for thread reply hydration.
func (s *Server) threadHydrationRelays(ctx context.Context, viewer string, root, selected *nostrx.Event, requestRelays []string) []string {
	if s == nil {
		return nostrx.NormalizeRelayList(requestRelays, nostrx.MaxRelays)
	}
	max := s.cfg.ThreadMaxRelays
	if max <= 0 {
		max = 16
	}
	merged := make([]string, 0, len(requestRelays)+len(s.cfg.DefaultRelays)+len(s.cfg.MetadataRelays)+24)
	merged = append(merged, requestRelays...)
	merged = append(merged, s.cfg.DefaultRelays...)
	merged = append(merged, s.cfg.MetadataRelays...)
	if root != nil {
		merged = s.appendAuthorRelayHints(ctx, merged, root.PubKey)
		merged = append(merged, s.threadRelays(nil, *root)...)
		observed, _ := s.store.ObservedRelaysForAuthors(ctx, []string{root.PubKey}, []int{
			nostrx.KindTextNote,
			nostrx.KindRepost,
			nostrx.KindProfileMetadata,
			nostrx.KindRelayListMetadata,
		}, 2)
		merged = append(merged, observed[root.PubKey]...)
	}
	merged = s.appendMentionedAuthorRelayHints(ctx, merged, root, selected)
	if selected != nil && (root == nil || selected.ID != root.ID) {
		merged = s.appendAuthorRelayHints(ctx, merged, selected.PubKey)
		merged = append(merged, s.threadRelays(nil, *selected)...)
	}
	if viewer != "" {
		merged = s.appendAuthorRelayHints(ctx, merged, viewer)
	}
	if root != nil {
		merged = s.appendThreadOutboxRelayHints(ctx, viewer, root.ID, merged)
	}
	return nostrx.NormalizeRelayList(s.filterCrawlerRelays(merged), max)
}

// threadHydrationRelaysForNoteID loads a note from the store and builds hydration relays.
func (s *Server) threadHydrationRelaysForNoteID(ctx context.Context, viewer, noteID string, baseRelays []string) []string {
	if s == nil || noteID == "" {
		return baseRelays
	}
	event, _ := s.store.GetEvent(ctx, noteID)
	if event == nil {
		return s.threadHydrationRelays(ctx, viewer, nil, nil, baseRelays)
	}
	return s.threadHydrationRelays(ctx, viewer, event, event, baseRelays)
}
