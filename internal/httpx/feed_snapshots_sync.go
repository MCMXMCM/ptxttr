package httpx

import (
	"maps"
	"strings"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

const resolvedAuthorsDurableMaxAge = 48 * time.Hour

// feedSnapshotRecordVersion must match store.FeedSnapshotRecord JSON version.
const feedSnapshotRecordVersion = 2

// guestCanonicalFeedSnapshotKey is the durable key for canonical logged-out
// default-seed first pages (per sort mode and relay set).
func guestCanonicalFeedSnapshotKey(sortMode string, relays []string) string {
	return "gc:" + normalizeFeedSort(sortMode) + ":" + hashStringSlice(relays)
}

func signedInFeedSnapshotKey(viewerHex string, sortMode string, wot webOfTrustOptions, relays []string) string {
	rk := resolvedAuthorsCacheKey(viewerHex, wot)
	return "si:" + rk + ":" + normalizeFeedSort(sortMode) + ":" + hashStringSlice(relays)
}

// isGuestCanonicalSnapshotTarget matches anonymous home shapes we keep durable
// snapshots for: default Jack seed, default WoT depth, canonical default relays.
func (s *Server) isGuestCanonicalSnapshotTarget(req feedRequest) bool {
	if s == nil {
		return false
	}
	pub := strings.TrimSpace(req.Pubkey)
	if pub != "" {
		if _, err := nostrx.DecodeIdentifier(pub); err == nil {
			return false
		}
	}
	if req.Cursor != 0 || req.CursorID != "" {
		return false
	}
	if !req.WoT.Enabled {
		return false
	}
	if req.WoT.Depth != defaultLoggedOutWOTDepth {
		return false
	}
	if !isDefaultLoggedOutSeed(req.SeedPubkey) {
		return false
	}
	switch normalizeFeedSort(req.SortMode) {
	case feedSortRecent, feedSortTrend24h, feedSortTrend7d:
	default:
		return false
	}
	return hashStringSlice(req.Relays) == hashStringSlice(s.canonicalDefaultLoggedOutRelays())
}

func feedSnapshotRecordFromFeedPageData(req feedRequest, data *FeedPageData, isStarter bool) *store.FeedSnapshotRecord {
	if data == nil || len(data.Feed) == 0 {
		return nil
	}
	prof := make(map[string]store.DefaultSeedProfileSnap, len(data.Profiles))
	for k, p := range data.Profiles {
		prof[k] = store.DefaultSeedProfileSnap{
			PubKey:  p.PubKey,
			Name:    p.Name,
			Display: p.Display,
			About:   p.About,
			Picture: p.Picture,
			Website: p.Website,
			NIP05:   p.NIP05,
		}
	}
	rec := &store.FeedSnapshotRecord{
		IsStarter:        isStarter,
		RelaysHash:       hashStringSlice(req.Relays),
		Feed:             append([]nostrx.Event(nil), data.Feed...),
		Profiles:         prof,
		Cursor:           data.Cursor,
		CursorID:         data.CursorID,
		HasMore:          data.HasMore,
		ComputedAtUnix:   time.Now().Unix(),
	}
	if data.ReferencedEvents != nil {
		rec.ReferencedEvents = maps.Clone(data.ReferencedEvents)
	}
	if data.ReplyCounts != nil {
		rec.ReplyCounts = maps.Clone(data.ReplyCounts)
	}
	if data.ReactionTotals != nil {
		rec.ReactionTotals = maps.Clone(data.ReactionTotals)
	}
	if data.ReactionViewers != nil {
		rec.ReactionViewers = maps.Clone(data.ReactionViewers)
	}
	return rec
}

func mergeFeedSnapshotRecordIntoFeedPageData(data *FeedPageData, rec *store.FeedSnapshotRecord, starter bool) {
	if data == nil || rec == nil || len(rec.Feed) == 0 {
		return
	}
	data.Feed = rec.Feed
	data.ReferencedEvents = rec.ReferencedEvents
	if data.ReferencedEvents == nil {
		data.ReferencedEvents = map[string]nostrx.Event{}
	}
	data.ReplyCounts = rec.ReplyCounts
	if data.ReplyCounts == nil {
		data.ReplyCounts = map[string]int{}
	}
	data.ReactionTotals = rec.ReactionTotals
	if data.ReactionTotals == nil {
		data.ReactionTotals = map[string]int{}
	}
	data.ReactionViewers = rec.ReactionViewers
	if data.ReactionViewers == nil {
		data.ReactionViewers = map[string]string{}
	}
	data.Cursor = rec.Cursor
	data.CursorID = rec.CursorID
	data.HasMore = rec.HasMore
	if data.Profiles == nil {
		data.Profiles = make(map[string]nostrx.Profile)
	}
	for pk, row := range rec.Profiles {
		if _, exists := data.Profiles[pk]; exists {
			continue
		}
		data.Profiles[pk] = nostrx.Profile{
			PubKey:  row.PubKey,
			Name:    row.Name,
			Display: row.Display,
			About:   row.About,
			Picture: row.Picture,
			Website: row.Website,
			NIP05:   row.NIP05,
		}
	}
	data.FeedSnapshotStarter = starter || rec.IsStarter
}

func defaultSeedGuestSnapToFeedSnapshotRecord(snap *store.DefaultSeedGuestFeedSnapshot) *store.FeedSnapshotRecord {
	if snap == nil || len(snap.Feed) == 0 {
		return nil
	}
	return &store.FeedSnapshotRecord{
		Version:          feedSnapshotRecordVersion,
		IsStarter:        false,
		RelaysHash:       snap.RelaysHash,
		Feed:             snap.Feed,
		ReferencedEvents: snap.ReferencedEvents,
		ReplyCounts:      snap.ReplyCounts,
		ReactionTotals:   snap.ReactionTotals,
		ReactionViewers:  snap.ReactionViewers,
		Profiles:         snap.Profiles,
		Cursor:           snap.Cursor,
		CursorID:         snap.CursorID,
		HasMore:          snap.HasMore,
		ComputedAtUnix:   snap.ComputedAtUnix,
	}
}
