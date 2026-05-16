package httpx

import (
	"context"
	"sort"
	"strings"

	"ptxt-nstr/internal/nostrx"
)

func (s *Server) groupAuthorsForOutbox(ctx context.Context, viewer string, authors []string, relays []string) []outboxRouteGroup {
	keys := uniqueNonEmptyStrings(authors)
	if len(keys) == 0 {
		return nil
	}
	seedRelays := s.outboxSeedRelays(ctx, viewer, keys, relays)
	contactHints, _ := s.store.ContactRelayHintsForOwner(ctx, viewer, keys, s.cfg.OutboxFoFSeeds)
	observedHints, _ := s.store.ObservedRelaysForAuthors(ctx, keys, []int{
		nostrx.KindTextNote,
		nostrx.KindRepost,
		nostrx.KindProfileMetadata,
		nostrx.KindRelayListMetadata,
	}, 2)
	hintSets, _ := s.store.RelayHintsByUsageForPubkeys(ctx, keys)
	maxRelays := s.cfg.OutboxMaxRelaysPerAuthor
	if maxRelays <= 0 {
		maxRelays = nostrx.MaxRelays
	}

	grouped := make(map[string]*outboxRouteGroup)
	for _, author := range keys {
		set := hintSets[author]
		authorRelays := nostrx.NormalizeRelayList(append(
			append(
				append(
					append([]string(nil), set.Write...),
					contactHints[author]...,
				),
				observedHints[author]...,
			),
			append(set.All, seedRelays...)...,
		), maxRelays)
		if len(authorRelays) == 0 {
			authorRelays = nostrx.NormalizeRelayList(append([]string(nil), relays...), maxRelays)
		}
		key := strings.Join(authorRelays, ",")
		group := grouped[key]
		if group == nil {
			group = &outboxRouteGroup{relays: authorRelays}
			grouped[key] = group
		}
		group.authors = append(group.authors, author)
	}

	groups := make([]outboxRouteGroup, 0, len(grouped))
	for _, group := range grouped {
		group.authors = clampAuthors(group.authors)
		groups = append(groups, *group)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if len(groups[i].authors) == len(groups[j].authors) {
			return strings.Join(groups[i].relays, ",") < strings.Join(groups[j].relays, ",")
		}
		return len(groups[i].authors) > len(groups[j].authors)
	})
	maxGroups := s.cfg.OutboxMaxRouteGroups
	if maxGroups <= 0 {
		maxGroups = 6
	}
	if len(groups) > maxGroups {
		overflowAuthors := make([]string, 0, len(keys))
		for index := maxGroups - 1; index < len(groups); index++ {
			overflowAuthors = append(overflowAuthors, groups[index].authors...)
		}
		fallbackRelays := nostrx.NormalizeRelayList(append(append([]string(nil), relays...), seedRelays...), maxRelays)
		groups = append(groups[:maxGroups-1], outboxRouteGroup{
			authors: clampAuthors(uniqueNonEmptyStrings(overflowAuthors)),
			relays:  fallbackRelays,
		})
	}
	return groups
}

func (s *Server) outboxSeedRelays(ctx context.Context, viewer string, authors []string, requestRelays []string) []string {
	seedPubkeys := make([]string, 0, len(authors)+s.cfg.OutboxFoFSeeds+16)
	seedPubkeys = append(seedPubkeys, authors...)
	if viewer != "" {
		seedPubkeys = append(seedPubkeys, viewer)
		seedPubkeys = append(seedPubkeys, s.following(ctx, viewer, maxFeedAuthors)...)
		seedPubkeys = append(seedPubkeys, s.followers(ctx, viewer, s.cfg.OutboxFoFSeeds)...)
		if fof, err := s.store.SecondHopFollowingPubkeys(ctx, viewer, s.cfg.OutboxFoFSeeds); err == nil {
			seedPubkeys = append(seedPubkeys, fof...)
		}
	}
	seedPubkeys = limitedStrings(uniqueNonEmptyStrings(seedPubkeys), s.cfg.OutboxFoFSeeds+maxFeedAuthors)
	seedRelays := make([]string, 0, len(requestRelays)+len(s.cfg.DefaultRelays)+len(s.cfg.MetadataRelays)+32)
	seedRelays = append(seedRelays, requestRelays...)
	seedRelays = append(seedRelays, s.cfg.DefaultRelays...)
	seedRelays = append(seedRelays, s.cfg.MetadataRelays...)
	hintSets, _ := s.store.RelayHintsByUsageForPubkeys(ctx, seedPubkeys)
	for _, pubkey := range seedPubkeys {
		seedRelays = append(seedRelays, hintSets[pubkey].All...)
	}
	maxRelays := s.cfg.OutboxMaxRelaysPerAuthor
	if maxRelays <= 0 {
		maxRelays = nostrx.MaxRelays
	}
	return nostrx.NormalizeRelayList(seedRelays, maxRelays)
}

func (s *Server) authorMetadataRelays(ctx context.Context, pubkey string, relays []string) []string {
	merged := make([]string, 0, len(relays)+len(s.cfg.DefaultRelays)+len(s.cfg.MetadataRelays)+8)
	merged = append(merged, relays...)
	merged = append(merged, s.cfg.DefaultRelays...)
	merged = append(merged, s.cfg.MetadataRelays...)
	if hints, err := s.store.RelayHintsForPubkeyByUsage(ctx, pubkey, nostrx.RelayUsageWrite); err == nil {
		merged = append(merged, hints...)
	}
	if hints, err := s.store.RelayHintsForPubkeyByUsage(ctx, pubkey, nostrx.RelayUsageAny); err == nil {
		merged = append(merged, hints...)
	}
	maxRelays := s.cfg.OutboxMaxRelaysPerAuthor
	if maxRelays <= 0 {
		maxRelays = nostrx.MaxRelays
	}
	return nostrx.NormalizeRelayList(merged, maxRelays)
}
