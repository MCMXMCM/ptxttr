package httpx

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

const maxRelayPreferenceHints = nostrx.MaxRelays * 3

type relayInsightEntry struct {
	URL        string   `json:"url"`
	Sources    []string `json:"sources,omitempty"`
	Usage      string   `json:"usage"`
	Confidence string   `json:"confidence,omitempty"`
	Status     string   `json:"status,omitempty"`
	LastError  string   `json:"last_error,omitempty"`
}

type relayInsightResponse struct {
	PubKey            string              `json:"pubkey"`
	PublishedEventID  string              `json:"published_event_id,omitempty"`
	PublishedCreated  int64               `json:"published_created_at,omitempty"`
	PublishedRelays   []relayInsightEntry `json:"published_relays"`
	DiscoveredRelays  []relayInsightEntry `json:"discovered_relays"`
	RecommendedRelays []relayInsightEntry `json:"recommended_relays"`
}

// planPublishRelays returns a deduplicated outbound relay list with this
// precedence: explicit caller relays, author write hints, request relays,
// author "any" hints, thread participant write/any hints, outbox seed
// relays, and finally curated healthy fallbacks.
func (s *Server) planPublishRelays(ctx context.Context, r *http.Request, event nostrx.Event, requested []string) []string {
	baseRelays := s.requestRelays(r)
	explicitRelays := nostrx.NormalizeRelayList(requested, maxRelayPreferenceHints)
	participants := s.publishParticipantPubkeys(ctx, event)

	hintKeys := append([]string{event.PubKey}, participants...)
	hints, _ := s.store.RelayHintsByUsageForPubkeys(ctx, hintKeys)
	authorHints := hints[event.PubKey]

	participantHints := make([]string, 0, len(participants)*2)
	for _, pubkey := range participants {
		set := hints[pubkey]
		participantHints = append(participantHints, set.Write...)
		participantHints = append(participantHints, set.All...)
	}
	seedRelays := s.outboxSeedRelays(ctx, event.PubKey, participants, baseRelays)
	fallbackRelays := s.curatedFallbackRelays(ctx, baseRelays, hintKeys)

	merged := make([]string, 0, len(explicitRelays)+len(authorHints.Write)+len(baseRelays)+len(authorHints.All)+len(participantHints)+len(seedRelays)+len(fallbackRelays))
	for _, src := range [][]string{explicitRelays, authorHints.Write, baseRelays, authorHints.All, participantHints, seedRelays, fallbackRelays} {
		merged = append(merged, src...)
	}
	return nostrx.NormalizeRelayList(merged, nostrx.MaxRelays)
}

func (s *Server) curatedFallbackRelays(ctx context.Context, requestRelays []string, relatedPubkeys []string) []string {
	statuses, _ := s.store.RelayStatuses(ctx)
	return s.curatedFallbackRelaysWithStatuses(ctx, requestRelays, relatedPubkeys, statuses)
}

func (s *Server) curatedFallbackRelaysWithStatuses(ctx context.Context, requestRelays []string, relatedPubkeys []string, statuses map[string]store.RelayStatus) []string {
	merged := make([]string, 0, len(requestRelays)+len(s.cfg.DefaultRelays)+len(s.cfg.MetadataRelays)+32)
	merged = append(merged, s.cfg.DefaultRelays...)
	merged = append(merged, s.cfg.MetadataRelays...)
	merged = append(merged, requestRelays...)
	keys := uniqueNonEmptyStrings(relatedPubkeys)
	if len(keys) > 0 {
		hints, _ := s.store.RelayHintsByUsageForPubkeys(ctx, keys)
		for _, pubkey := range keys {
			set := hints[pubkey]
			merged = append(merged, set.Write...)
			merged = append(merged, set.All...)
		}
	}
	candidates := nostrx.NormalizeRelayList(merged, maxRelayPreferenceHints*2)
	curated := make(map[string]struct{}, len(s.cfg.DefaultRelays)+len(s.cfg.MetadataRelays))
	for _, relay := range s.cfg.DefaultRelays {
		curated[relay] = struct{}{}
	}
	for _, relay := range s.cfg.MetadataRelays {
		curated[relay] = struct{}{}
	}
	out := make([]string, 0, nostrx.MaxRelays*2)
	for _, relay := range candidates {
		if _, ok := curated[relay]; ok {
			out = append(out, relay)
		} else if status, ok := statuses[relay]; ok && status.OK {
			out = append(out, relay)
		}
		if len(out) >= nostrx.MaxRelays*2 {
			break
		}
	}
	if len(out) == 0 {
		return nostrx.NormalizeRelayList(append(append([]string(nil), s.cfg.DefaultRelays...), s.cfg.MetadataRelays...), nostrx.MaxRelays)
	}
	return nostrx.NormalizeRelayList(out, nostrx.MaxRelays*2)
}

func (s *Server) publishParticipantPubkeys(ctx context.Context, event nostrx.Event) []string {
	participants := make([]string, 0, len(event.Tags)+2)
	for _, tag := range event.Tags {
		if len(tag) < 2 || tag[0] != "p" {
			continue
		}
		if normalized, err := nostrx.NormalizePubKey(tag[1]); err == nil {
			participants = append(participants, normalized)
		}
	}
	if event.Kind == nostrx.KindTextNote || event.Kind == nostrx.KindRepost {
		ids := make([]string, 0, len(event.Tags))
		for _, tag := range event.Tags {
			if len(tag) >= 2 && tag[0] == "e" {
				if v := strings.TrimSpace(tag[1]); v != "" {
					ids = append(ids, v)
				}
			}
		}
		if len(ids) > 0 {
			events := s.eventsByIDFromStore(ctx, ids)
			for _, id := range ids {
				if referenced := events[id]; referenced != nil && referenced.PubKey != "" {
					participants = append(participants, referenced.PubKey)
				}
			}
		}
	}
	return uniqueNonEmptyStrings(participants)
}

func (s *Server) buildRelayInsight(ctx context.Context, pubkey string, requestRelays []string) relayInsightResponse {
	statuses, _ := s.store.RelayStatuses(ctx)
	response := relayInsightResponse{
		PubKey:            pubkey,
		PublishedRelays:   []relayInsightEntry{},
		DiscoveredRelays:  []relayInsightEntry{},
		RecommendedRelays: []relayInsightEntry{},
	}
	newEntry := func(url string, sources []string, usage, confidence string) relayInsightEntry {
		status, ok := statuses[url]
		entry := relayInsightEntry{URL: url, Sources: uniqueNonEmptyStrings(sources), Usage: usage, Confidence: confidence, Status: "unknown"}
		if ok {
			if status.OK {
				entry.Status = "ok"
			} else {
				entry.Status = "error"
			}
			entry.LastError = status.LastError
		}
		return entry
	}
	seenPublished := make(map[string]bool)
	published, _ := s.store.LatestReplaceable(ctx, pubkey, nostrx.KindRelayListMetadata)
	if published != nil {
		response.PublishedEventID = published.ID
		response.PublishedCreated = published.CreatedAt
		for _, hint := range nostrx.RelayHints(published, maxRelayPreferenceHints) {
			usage := "any"
			switch {
			case hint.Read && !hint.Write:
				usage = "read"
			case hint.Write && !hint.Read:
				usage = "write"
			}
			response.PublishedRelays = append(response.PublishedRelays, newEntry(hint.URL, []string{"published_kind10002"}, usage, "high"))
			seenPublished[hint.URL] = true
		}
	}
	discovered := make(map[string]relayInsightEntry)
	appendDiscovered := func(relays []string, source string, confidence string, usage string) {
		for _, relay := range relays {
			if relay == "" || seenPublished[relay] {
				continue
			}
			if existing, ok := discovered[relay]; ok {
				existing.Sources = uniqueNonEmptyStrings(append(existing.Sources, source))
				if relayConfidenceRank(confidence) > relayConfidenceRank(existing.Confidence) {
					existing.Confidence = confidence
				}
				if existing.Usage == "any" && usage != "any" {
					existing.Usage = usage
				}
				discovered[relay] = existing
				continue
			}
			discovered[relay] = newEntry(relay, []string{source}, usage, confidence)
		}
	}
	if writeHints, err := s.store.RelayHintsForPubkeyByUsage(ctx, pubkey, nostrx.RelayUsageWrite); err == nil {
		appendDiscovered(writeHints, "cached_write_hint", "high", "write")
	}
	if anyHints, err := s.store.RelayHintsForPubkeyByUsage(ctx, pubkey, nostrx.RelayUsageAny); err == nil {
		appendDiscovered(anyHints, "cached_any_hint", "medium", "any")
	}
	if observed, err := s.store.ObservedRelaysForAuthors(ctx, []string{pubkey}, []int{
		nostrx.KindTextNote,
		nostrx.KindRepost,
		nostrx.KindProfileMetadata,
		nostrx.KindRelayListMetadata,
	}, 6); err == nil {
		appendDiscovered(observed[pubkey], "observed_activity", "medium", "any")
	}
	response.DiscoveredRelays = sortedRelayInsightEntries(discovered)

	for _, relay := range s.curatedFallbackRelaysWithStatuses(ctx, requestRelays, []string{pubkey}, statuses) {
		response.RecommendedRelays = append(response.RecommendedRelays, newEntry(relay, []string{"backend_recommended"}, "write", "high"))
	}
	sortRelayInsightEntries(response.RecommendedRelays)
	return response
}

func sortedRelayInsightEntries(entries map[string]relayInsightEntry) []relayInsightEntry {
	out := make([]relayInsightEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry)
	}
	sortRelayInsightEntries(out)
	return out
}

func sortRelayInsightEntries(out []relayInsightEntry) {
	sort.SliceStable(out, func(i, j int) bool {
		rankI := relayConfidenceRank(out[i].Confidence)
		rankJ := relayConfidenceRank(out[j].Confidence)
		if rankI == rankJ {
			return out[i].URL < out[j].URL
		}
		return rankI > rankJ
	})
}

func relayConfidenceRank(confidence string) int {
	switch strings.ToLower(strings.TrimSpace(confidence)) {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}
