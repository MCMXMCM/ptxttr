package httpx

import (
	"context"
	"sort"
	"strings"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
)

const notificationPageLimit = 30

const notificationScanBatchSize = 80
const notificationCursorRollupPrefix = "rollup:"
const notificationCursorEventPrefix = "event:"

func notificationRefreshCacheKey(taggedPubkey string, authors []string) string {
	key := authorsCacheKey(authors)
	if key == "" {
		return taggedPubkey
	}
	return taggedPubkey + "|" + key
}

func (s *Server) refreshNotificationsForAuthors(ctx context.Context, viewer, taggedPubkey string, authors []string, relays []string, limit int) int {
	if taggedPubkey == "" || len(authors) == 0 {
		return -1
	}
	if limit <= 0 || limit > loggedInFetchWindow {
		limit = loggedInFetchWindow
	}
	groups := s.groupAuthorsForOutbox(ctx, viewer, authors, relays)
	if len(groups) == 0 {
		groups = []outboxRouteGroup{{authors: append([]string(nil), authors...), relays: append([]string(nil), relays...)}}
	}
	total := 0
	anySuccess := false
	for _, group := range groups {
		if len(group.authors) == 0 {
			continue
		}
		fetched := s.refreshCached(ctx, "notifications_authors", notificationRefreshCacheKey(taggedPubkey, group.authors), 0, group.relays, nostrx.Query{
			Authors: group.authors,
			Kinds:   noteTimelineKinds,
			Tags:    map[string][]string{"p": {taggedPubkey}},
			Limit:   limit,
		})
		if fetched < 0 {
			continue
		}
		anySuccess = true
		total += fetched
	}
	if !anySuccess {
		return -1
	}
	return total
}

// scanNotificationsPage returns up to `target` mention events (newest first)
// without applying the final UI page limit. When wotEnabled, only events whose
// author is in membership are kept; the store is scanned in batches until
// enough matches are found or the mention stream is exhausted.
func (s *Server) scanNotificationsPage(ctx context.Context, taggedPubkey string, kinds []int, membership authorMembership, wotEnabled bool, before int64, beforeID string, target int) ([]nostrx.Event, bool, int64, string, error) {
	if !wotEnabled {
		page, err := s.store.EventsMentioningPubkey(ctx, taggedPubkey, kinds, before, beforeID, target)
		if err != nil {
			return nil, false, 0, "", err
		}
		hasMore := len(page) >= target && target > 0
		var nextCur int64
		var nextID string
		if hasMore && len(page) > 0 {
			last := page[target-1]
			nextCur, nextID = last.CreatedAt, last.ID
		}
		return page, hasMore, nextCur, nextID, nil
	}

	if len(membership.exact) == 0 {
		return nil, false, 0, "", nil
	}

	curBefore := before
	curBeforeID := beforeID
	if curBefore <= 0 {
		curBefore = time.Now().Unix() + 1
	}
	matched := make([]nostrx.Event, 0, target)
	exhausted := false
	for len(matched) < target && !exhausted {
		batch, err := s.store.EventsMentioningPubkey(ctx, taggedPubkey, kinds, curBefore, curBeforeID, notificationScanBatchSize)
		if err != nil {
			return nil, false, 0, "", err
		}
		if len(batch) == 0 {
			break
		}
		last := batch[len(batch)-1]
		curBefore, curBeforeID = last.CreatedAt, last.ID
		for _, e := range batch {
			if membership.Contains(e.PubKey) {
				matched = append(matched, e)
				if len(matched) >= target {
					break
				}
			}
		}
		if len(batch) < notificationScanBatchSize {
			exhausted = true
		}
	}
	hasMore := len(matched) >= target && target > 0
	var nextCur int64
	var nextID string
	if hasMore && len(matched) > 0 {
		last := matched[target-1]
		nextCur, nextID = last.CreatedAt, last.ID
	}
	return matched, hasMore, nextCur, nextID, nil
}

func notificationCursorParts(cursorID string) (string, string) {
	cursorID = strings.TrimSpace(cursorID)
	switch {
	case strings.HasPrefix(cursorID, notificationCursorRollupPrefix):
		return "reaction_rollup", strings.TrimPrefix(cursorID, notificationCursorRollupPrefix)
	case strings.HasPrefix(cursorID, notificationCursorEventPrefix):
		return "mention", strings.TrimPrefix(cursorID, notificationCursorEventPrefix)
	default:
		return "mention", cursorID
	}
}

func notificationEntryLess(left, right NotificationEntry) bool {
	if left.CreatedAt != right.CreatedAt {
		return left.CreatedAt > right.CreatedAt
	}
	return left.CursorID > right.CursorID
}

func notificationEntryBeforeCursor(entry NotificationEntry, cursorAt int64, cursorID string) bool {
	if cursorAt <= 0 {
		return true
	}
	if entry.CreatedAt < cursorAt {
		return true
	}
	if entry.CreatedAt > cursorAt {
		return false
	}
	return entry.CursorID < cursorID
}

func notificationEntriesForPage(mentions []nostrx.Event, rollups []store.ReactionRollupRow, before int64, beforeID string, limit int) ([]NotificationEntry, bool, int64, string) {
	if limit <= 0 {
		return nil, false, 0, ""
	}
	cursorType, cursorRawID := notificationCursorParts(beforeID)
	cursorCompositeID := ""
	if before > 0 {
		switch cursorType {
		case "reaction_rollup":
			cursorCompositeID = notificationCursorRollupPrefix + cursorRawID
		default:
			cursorCompositeID = notificationCursorEventPrefix + cursorRawID
		}
	}
	entries := make([]NotificationEntry, 0, len(mentions)+len(rollups))
	for _, event := range mentions {
		entry := NotificationEntry{
			Type:      "mention",
			Event:     event,
			CreatedAt: event.CreatedAt,
			CursorID:  notificationCursorEventPrefix + event.ID,
		}
		if notificationEntryBeforeCursor(entry, before, cursorCompositeID) {
			entries = append(entries, entry)
		}
	}
	for _, row := range rollups {
		entry := NotificationEntry{
			Type:      "reaction_rollup",
			Rollup:    row,
			CreatedAt: row.LastAt,
			CursorID:  notificationCursorRollupPrefix + row.NoteID,
		}
		if notificationEntryBeforeCursor(entry, before, cursorCompositeID) {
			entries = append(entries, entry)
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return notificationEntryLess(entries[i], entries[j])
	})
	hasMore := len(entries) > limit
	if hasMore {
		entries = entries[:limit]
	}
	if len(entries) == 0 {
		return entries, hasMore, 0, ""
	}
	last := entries[len(entries)-1]
	return entries, hasMore, last.CreatedAt, last.CursorID
}

func (s *Server) notificationsData(ctx context.Context, pubkey, seedPubkey string, relays []string, cursor int64, cursorID string, refreshFromRelays bool, wot webOfTrustOptions) NotificationsPageData {
	decoded, err := nostrx.DecodeIdentifier(strings.TrimSpace(pubkey))
	if err != nil || decoded == "" {
		return NotificationsPageData{}
	}
	resolved := s.resolveRequestAuthors(ctx, pubkey, seedPubkey, relays, wot)
	var membership authorMembership
	if resolved.wotEnabled {
		membership = newAuthorMembership(resolved.allAuthors)
	}

	if refreshFromRelays {
		if resolved.wotEnabled {
			viewer := resolved.userPubkey
			if resolved.wotViewerPubkey != "" {
				viewer = resolved.wotViewerPubkey
			}
			_ = s.refreshNotificationsForAuthors(ctx, viewer, decoded, resolved.allAuthors, relays, loggedInFetchWindow)
		} else {
			fetched, err := s.nostr.FetchFrom(ctx, relays, nostrx.Query{
				Tags:  map[string][]string{"p": {decoded}},
				Kinds: noteTimelineKinds,
				Limit: loggedInFetchWindow,
			})
			if err == nil && len(fetched) > 0 {
				_, _ = s.store.SaveEvents(ctx, fetched)
			}
		}
	}

	before := cursor
	if before <= 0 {
		before = time.Now().Unix() + 1
	}
	beforeID := cursorID
	cursorType, cursorRawID := notificationCursorParts(cursorID)
	mentionBeforeID := cursorRawID
	if cursorType == "reaction_rollup" || mentionBeforeID == "" {
		mentionBeforeID = strings.Repeat("f", 64)
	}
	rollupBeforeID := cursorRawID
	if cursorType == "mention" || rollupBeforeID == "" {
		rollupBeforeID = "~"
	}

	page, mentionHasMore, _, _, err := s.scanNotificationsPage(ctx, decoded, noteTimelineKinds, membership, resolved.wotEnabled, before, mentionBeforeID, (notificationPageLimit+1)*4)
	if err != nil {
		return NotificationsPageData{UserPubKey: decoded}
	}

	rollups, rerr := s.store.ReactionRollupsForNoteAuthor(ctx, decoded, before, rollupBeforeID, (notificationPageLimit+1)*4)
	if rerr != nil {
		rollups = nil
	}

	entries, hasMore, nextCursor, nextID := notificationEntriesForPage(page, rollups, before, beforeID, notificationPageLimit)
	if !hasMore && (mentionHasMore || len(rollups) >= (notificationPageLimit+1)*4) {
		hasMore = true
		if len(entries) > 0 {
			last := entries[len(entries)-1]
			nextCursor = last.CreatedAt
			nextID = last.CursorID
		}
	}

	noteEntries := make([]nostrx.Event, 0, len(entries))
	for _, entry := range entries {
		if entry.Type == "mention" {
			noteEntries = append(noteEntries, entry.Event)
		}
	}

	noteEntries = s.hydrateTimelineEvents(ctx, noteEntries)
	eventByID := make(map[string]nostrx.Event, len(noteEntries))
	for _, event := range noteEntries {
		eventByID[event.ID] = event
	}
	for i := range entries {
		if entries[i].Type != "mention" {
			continue
		}
		if hydrated, ok := eventByID[entries[i].Event.ID]; ok {
			entries[i].Event = hydrated
		}
	}
	s.warmFeedEntities(noteEntries, relays)
	referenced, combined := s.referencedHydration(ctx, noteEntries, relays)
	rt, rv := s.reactionMapsForEvents(ctx, combined, decoded)

	seedDisplay := ""
	if resolved.seedWOTEnabled {
		seedDisplay = loggedOutWOTSeedDisplayName(seedPubkey)
	}

	return NotificationsPageData{
		UserPubKey:                  decoded,
		Entries:                     entries,
		Items:                       noteEntries,
		ReferencedEvents:            referenced,
		Profiles:                    s.profilesFor(ctx, combined),
		ReplyCounts:                 s.replyCounts(ctx, combined),
		ReactionTotals:              rt,
		ReactionViewers:             rv,
		ReactionRollups:             rollups,
		Cursor:                      nextCursor,
		CursorID:                    nextID,
		HasMore:                     hasMore,
		WebOfTrustEnabled:           resolved.wotEnabled,
		WebOfTrustDepth:             wot.Depth,
		WebOfTrustSeedPubkey:        seedPubkey,
		LoggedOutWOTSeedDisplayName: seedDisplay,
	}
}
