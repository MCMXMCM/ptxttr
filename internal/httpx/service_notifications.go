package httpx

import (
	"context"
	"sort"
	"strings"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/store"
	"ptxt-nstr/internal/thread"
)

const notificationPageLimit = 30

const notificationRecentAuthorLimit = 100
const notificationReplyFetchLimit = 120
const notificationScanBatchSize = 80
const notificationCursorRollupPrefix = "rollup:"
const notificationCursorEventPrefix = "event:"

var notificationMentionKinds = []int{nostrx.KindTextNote, nostrx.KindRepost}
var notificationAuthoredKinds = []int{nostrx.KindTextNote}

type notificationContext struct {
	authoredEvents []nostrx.Event
	authoredIDs    map[string]struct{}
}

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
			Kinds:   notificationMentionKinds,
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

func notificationCursorParts(cursorID string) (string, string) {
	cursorID = strings.TrimSpace(cursorID)
	switch {
	case strings.HasPrefix(cursorID, notificationCursorRollupPrefix):
		return "reaction_rollup", strings.TrimPrefix(cursorID, notificationCursorRollupPrefix)
	case strings.HasPrefix(cursorID, notificationCursorEventPrefix):
		return "event", strings.TrimPrefix(cursorID, notificationCursorEventPrefix)
	default:
		return "event", cursorID
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

func notificationTagsViewer(event nostrx.Event, viewerPubkey string) bool {
	viewerPubkey = strings.TrimSpace(strings.ToLower(viewerPubkey))
	if viewerPubkey == "" {
		return false
	}
	for _, tag := range event.Tags {
		if len(tag) < 2 || tag[0] != "p" {
			continue
		}
		value := strings.TrimSpace(strings.ToLower(tag[1]))
		if value != "" && value == viewerPubkey {
			return true
		}
	}
	return false
}

func notificationReplyTarget(event nostrx.Event) string {
	if event.Kind != nostrx.KindTextNote {
		return ""
	}
	rootID := thread.RootID(event)
	return thread.NormalizeHexEventID(thread.ParentID(rootID, event))
}

func notificationCategoryForEvent(event nostrx.Event, viewerPubkey string, viewerOwnedIDs map[string]struct{}) string {
	switch event.Kind {
	case nostrx.KindRepost:
		if notificationTagsViewer(event, viewerPubkey) {
			return "repost"
		}
		return ""
	case nostrx.KindTextNote:
		if parentID := notificationReplyTarget(event); parentID != "" {
			if _, ok := viewerOwnedIDs[parentID]; ok {
				return "reply"
			}
		}
		if notificationTagsViewer(event, viewerPubkey) {
			return "mention"
		}
		return ""
	default:
		return ""
	}
}

func notificationTargetEventID(entryType string, event nostrx.Event, row store.ReactionRollupRow) string {
	switch entryType {
	case "reaction_rollup":
		return row.NoteID
	default:
		switch event.Kind {
		case nostrx.KindRepost:
			for _, tag := range event.Tags {
				if len(tag) >= 2 && tag[0] == "e" {
					return thread.NormalizeHexEventID(tag[1])
				}
			}
			return ""
		default:
			return event.ID
		}
	}
}

func uniqueNotificationEvents(events []nostrx.Event) []nostrx.Event {
	if len(events) == 0 {
		return nil
	}
	byID := make(map[string]nostrx.Event, len(events))
	for _, event := range events {
		if event.ID == "" {
			continue
		}
		byID[event.ID] = event
	}
	out := make([]nostrx.Event, 0, len(byID))
	for _, event := range byID {
		out = append(out, event)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].ID > out[j].ID
	})
	return out
}

func notificationEntriesForPage(entries []NotificationEntry, before int64, beforeID string, limit int) ([]NotificationEntry, bool, int64, string) {
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
	filtered := make([]NotificationEntry, 0, len(entries))
	for _, entry := range entries {
		if notificationEntryBeforeCursor(entry, before, cursorCompositeID) {
			filtered = append(filtered, entry)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return notificationEntryLess(filtered[i], filtered[j])
	})
	hasMore := len(filtered) > limit
	if hasMore {
		filtered = filtered[:limit]
	}
	if len(filtered) == 0 {
		return filtered, hasMore, 0, ""
	}
	last := filtered[len(filtered)-1]
	return filtered, hasMore, last.CreatedAt, last.CursorID
}

func (s *Server) notificationContextForViewer(ctx context.Context, pubkey string) notificationContext {
	out := notificationContext{authoredIDs: make(map[string]struct{})}
	if strings.TrimSpace(pubkey) == "" {
		return out
	}
	authored, err := s.store.RecentByAuthorsCursor(ctx, []string{pubkey}, notificationAuthoredKinds, 0, "", notificationRecentAuthorLimit)
	if err != nil {
		return out
	}
	out.authoredEvents = authored
	for _, event := range authored {
		id := thread.NormalizeHexEventID(event.ID)
		if id != "" {
			out.authoredIDs[id] = struct{}{}
		}
	}
	return out
}

func (s *Server) notificationMentionEvents(ctx context.Context, taggedPubkey string, membership authorMembership, wotEnabled bool, before int64, beforeID string, target int) ([]nostrx.Event, bool, error) {
	if !wotEnabled {
		page, err := s.store.EventsMentioningPubkey(ctx, taggedPubkey, notificationMentionKinds, before, beforeID, target)
		if err != nil {
			return nil, false, err
		}
		return page, len(page) >= target && target > 0, nil
	}
	if len(membership.exact) == 0 {
		return nil, false, nil
	}
	curBefore := before
	curBeforeID := beforeID
	if curBefore <= 0 {
		curBefore = time.Now().Unix() + 1
	}
	matched := make([]nostrx.Event, 0, target)
	exhausted := false
	for len(matched) < target && !exhausted {
		batch, err := s.store.EventsMentioningPubkey(ctx, taggedPubkey, notificationMentionKinds, curBefore, curBeforeID, notificationScanBatchSize)
		if err != nil {
			return nil, false, err
		}
		if len(batch) == 0 {
			break
		}
		last := batch[len(batch)-1]
		curBefore, curBeforeID = last.CreatedAt, last.ID
		for _, event := range batch {
			if membership.Contains(event.PubKey) {
				matched = append(matched, event)
				if len(matched) >= target {
					break
				}
			}
		}
		if len(batch) < notificationScanBatchSize {
			exhausted = true
		}
	}
	return matched, len(matched) >= target && target > 0, nil
}

func (s *Server) notificationDirectReplyEvents(ctx context.Context, authoredIDs map[string]struct{}, membership authorMembership, wotEnabled bool, target int) ([]nostrx.Event, bool) {
	if len(authoredIDs) == 0 {
		return nil, false
	}
	collected := make([]nostrx.Event, 0, target)
	for authoredID := range authoredIDs {
		replies, err := s.store.RepliesTo(ctx, authoredID, notificationReplyFetchLimit)
		if err != nil {
			continue
		}
		for _, reply := range replies {
			if notificationReplyTarget(reply) != authoredID {
				continue
			}
			if wotEnabled && !membership.Contains(reply.PubKey) {
				continue
			}
			collected = append(collected, reply)
		}
	}
	unique := uniqueNotificationEvents(collected)
	return unique, len(unique) >= target && target > 0
}

func notificationReferencedEventIDs(events []nostrx.Event) []string {
	ids := collectReferencedEventIDs(events)
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		seen[id] = struct{}{}
	}
	for _, event := range events {
		for _, candidate := range []string{thread.RootID(event), thread.ParentID(thread.RootID(event), event)} {
			id := thread.NormalizeHexEventID(candidate)
			if id == "" || id == thread.NormalizeHexEventID(event.ID) {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	return ids
}

func (s *Server) notificationReferencedHydration(ctx context.Context, events []nostrx.Event, relays []string) (map[string]nostrx.Event, []nostrx.Event) {
	ids := notificationReferencedEventIDs(events)
	if len(ids) == 0 {
		return map[string]nostrx.Event{}, append([]nostrx.Event(nil), events...)
	}
	loaded := s.eventsByID(ctx, ids, nostrx.NormalizeRelayList(relays, nostrx.MaxRelays))
	referenced := make(map[string]nostrx.Event, len(loaded))
	combined := make([]nostrx.Event, 0, len(events)+len(loaded))
	combined = append(combined, events...)
	for _, id := range ids {
		event := loaded[id]
		if event == nil {
			continue
		}
		referenced[id] = *event
		combined = append(combined, *event)
	}
	return referenced, combined
}

func (s *Server) notificationEntries(ctx context.Context, viewerPubkey string, context notificationContext, membership authorMembership, wotEnabled bool, before int64, beforeID string, rollups []store.ReactionRollupRow) ([]NotificationEntry, bool, error) {
	target := (notificationPageLimit + 1) * 4
	mentions, mentionHasMore, err := s.notificationMentionEvents(ctx, viewerPubkey, membership, wotEnabled, before, beforeID, target)
	if err != nil {
		return nil, false, err
	}
	replies, replyHasMore := s.notificationDirectReplyEvents(ctx, context.authoredIDs, membership, wotEnabled, target)
	candidates := uniqueNotificationEvents(append(mentions, replies...))
	entries := make([]NotificationEntry, 0, len(candidates)+len(rollups))
	for _, event := range candidates {
		category := notificationCategoryForEvent(event, viewerPubkey, context.authoredIDs)
		if category == "" {
			continue
		}
		entries = append(entries, NotificationEntry{
			Type:           "event",
			Category:       category,
			Event:          event,
			CreatedAt:      event.CreatedAt,
			CursorID:       notificationCursorEventPrefix + event.ID,
			NotificationID: event.ID,
			TargetEventID:  notificationTargetEventID("event", event, store.ReactionRollupRow{}),
		})
	}
	for _, row := range rollups {
		entries = append(entries, NotificationEntry{
			Type:           "reaction_rollup",
			Category:       "like",
			Rollup:         row,
			CreatedAt:      row.LastAt,
			CursorID:       notificationCursorRollupPrefix + row.NoteID,
			NotificationID: "rollup:" + row.NoteID,
			TargetEventID:  row.NoteID,
		})
	}
	page, hasMore, _, _ := notificationEntriesForPage(entries, before, beforeID, notificationPageLimit)
	if !hasMore && (mentionHasMore || replyHasMore || len(rollups) >= target) {
		hasMore = true
	}
	return page, hasMore, nil
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
	context := s.notificationContextForViewer(ctx, decoded)

	if refreshFromRelays {
		if resolved.wotEnabled {
			viewer := resolved.userPubkey
			if resolved.wotViewerPubkey != "" {
				viewer = resolved.wotViewerPubkey
			}
			_ = s.refreshNotificationsForAuthors(ctx, viewer, decoded, resolved.cohortAuthors(), relays, loggedInFetchWindow)
		} else {
			fetched, err := s.nostr.FetchFrom(ctx, relays, nostrx.Query{
				Tags:  map[string][]string{"p": {decoded}},
				Kinds: notificationMentionKinds,
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
	rollupBeforeID := cursorRawID
	if cursorType == "event" || rollupBeforeID == "" {
		rollupBeforeID = "~"
	}
	rollups, rerr := s.store.ReactionRollupsForNoteAuthor(ctx, decoded, before, rollupBeforeID, (notificationPageLimit+1)*4)
	if rerr != nil {
		rollups = nil
	}

	entries, hasMore, err := s.notificationEntries(ctx, decoded, context, membership, resolved.wotEnabled, before, beforeID, rollups)
	if err != nil {
		return NotificationsPageData{UserPubKey: decoded}
	}
	var nextCursor int64
	var nextID string
	if len(entries) > 0 {
		last := entries[len(entries)-1]
		nextCursor = last.CreatedAt
		nextID = last.CursorID
	}

	noteEntries := make([]nostrx.Event, 0, len(entries))
	for _, entry := range entries {
		if entry.Type == "event" {
			noteEntries = append(noteEntries, entry.Event)
		}
	}
	noteEntries = s.hydrateTimelineEvents(ctx, noteEntries)
	eventByID := make(map[string]nostrx.Event, len(noteEntries))
	for _, event := range noteEntries {
		eventByID[event.ID] = event
	}
	for i := range entries {
		if entries[i].Type != "event" {
			continue
		}
		if hydrated, ok := eventByID[entries[i].Event.ID]; ok {
			entries[i].Event = hydrated
		}
	}

	s.warmFeedEntities(noteEntries, relays)
	referenced, combined := s.notificationReferencedHydration(ctx, noteEntries, relays)
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
