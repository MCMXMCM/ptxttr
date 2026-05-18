package httpx

import (
	"ptxt-nstr/internal/nostrx"
)

// crawlRelays merges default/metadata tiers onto extra and drops relays in
// policy backoff. Use filterCrawlerRelays when extra already includes tiers.
func (s *Server) crawlRelays(extra []string) []string {
	if s == nil {
		return nil
	}
	return s.filterCrawlerRelays(s.mergeCrawlRelayTiers(extra))
}

func (s *Server) mergeCrawlRelayTiers(extra []string) []string {
	if s == nil {
		return extra
	}
	merged := make([]string, 0, len(extra)+len(s.cfg.DefaultRelays)+len(s.cfg.MetadataRelays))
	merged = append(merged, extra...)
	merged = append(merged, s.cfg.DefaultRelays...)
	merged = append(merged, s.cfg.MetadataRelays...)
	return merged
}

func (s *Server) filterCrawlerRelays(relays []string) []string {
	if s == nil || len(relays) == 0 {
		return relays
	}
	if s.nostr == nil {
		return nostrx.NormalizeRelayList(relays, nostrx.MaxRelays)
	}
	return s.nostr.FilterAvailableRelays(relays)
}
