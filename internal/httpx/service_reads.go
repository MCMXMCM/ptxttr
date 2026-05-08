package httpx

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"ptxt-nstr/internal/nostrx"
)

func (s *Server) readsData(ctx context.Context, req feedRequest, trendingTimeframe string) ReadsPageData {
	defer s.observe("reads.data", time.Now())
	if req.Limit <= 0 {
		req.Limit = 20
	}
	sortMode := normalizeFeedSort(req.SortMode)
	trendingTimeframe = normalizeTrendingTimeframe(trendingTimeframe)
	pageLimit := req.Limit + 1
	window := pageLimit * 3
	if window > readsFetchLimit {
		window = readsFetchLimit
	}
	var events []nostrx.Event
	var hasMore bool
	var next int64
	var nextID string
	resolved := s.resolveRequestAuthors(ctx, req.Pubkey, req.SeedPubkey, req.Relays, req.WoT)
	var wotMembership authorMembership
	if resolved.wotEnabled {
		wotMembership = newAuthorMembership(resolved.allAuthors)
	}
	if sortMode == feedSortRecent {
		events, hasMore = s.readsRecentPage(ctx, req, resolved, wotMembership, window)
		if hasMore && len(events) > req.Limit {
			events = events[:req.Limit]
		}
		if len(events) > 0 {
			last := events[len(events)-1]
			next = last.CreatedAt
			nextID = last.ID
		}
	} else {
		offset := int(req.Cursor)
		if offset < 0 {
			offset = 0
		}
		var authors []string
		if !resolved.loggedOut {
			authors = resolved.authors
		}
		// Scope the trending pageKey by the author cohort so different WoT
		// cohorts don't share a freshness flag with each other (or with the
		// global trending cache).
		trendingKey := readsCacheKey + "-" + sortMode
		var cohort []string
		if resolved.wotEnabled {
			cohort = resolved.allAuthors
		} else if !resolved.loggedOut {
			cohort = resolved.authors
		}
		if cohortKey := authorsCacheKey(cohort); cohortKey != "" {
			trendingKey += "|" + cohortKey
		}
		pageKey := feedRefreshKey(trendingKey, 0, "")
		if offset == 0 && s.store.ShouldRefresh(ctx, "reads", pageKey, feedTTL) {
			if s.refreshReadsForTrending(ctx, resolved, authors, window, req.Relays) {
				s.store.MarkRefreshed(ctx, "reads", pageKey)
			}
		}
		since := feedSortSince(sortMode, time.Now())
		if resolved.wotEnabled {
			events, hasMore = s.scanTrendingReadEvents(ctx, wotMembership, since, offset, req.Limit)
		} else {
			events, _ = s.store.TrendingSummariesByKinds(ctx, []int{nostrx.KindLongForm}, since, authors, offset, req.Limit+1)
			hasMore = len(events) > req.Limit
			if hasMore {
				events = events[:req.Limit]
			}
		}
		next = int64(offset + len(events))
		nextID = ""
	}

	events = s.hydrateReadEventsFromStore(ctx, events)

	pubkeys := make([]string, 0, len(events))
	items := make([]ReadItem, 0, len(events))
	for _, event := range events {
		pubkeys = append(pubkeys, event.PubKey)
		items = append(items, readItem(event))
	}
	s.warmAuthors(pubkeys, req.Relays)
	var trendingAuthors []string
	var trendingMembership authorMembership
	if resolved.wotEnabled {
		trendingMembership = wotMembership
	} else if !resolved.loggedOut {
		trendingAuthors = append([]string(nil), resolved.authors...)
	}
	trendingReads := s.trendingReadsData(ctx, trendingTimeframe, trendingAuthors, trendingMembership)
	profileEvents := append([]nostrx.Event(nil), events...)
	for _, item := range trendingReads {
		profileEvents = append(profileEvents, item.Event)
	}
	seedPubkey := ""
	seedDisplayName := ""
	if resolved.seedWOTEnabled {
		seedPubkey = strings.TrimSpace(req.SeedPubkey)
		seedDisplayName = loggedOutWOTSeedDisplayName(req.SeedPubkey)
	}
	return ReadsPageData{
		Items:                       items,
		Trending:                    trendingReads,
		Profiles:                    s.profilesFor(ctx, profileEvents),
		UserPubKey:                  resolved.userPubkey,
		ReadsSort:                   sortMode,
		ReadsTrendingTimeframe:      trendingTimeframe,
		Cursor:                      next,
		CursorID:                    nextID,
		HasMore:                     hasMore,
		Relays:                      req.Relays,
		WebOfTrustEnabled:           resolved.wotEnabled,
		WebOfTrustDepth:             req.WoT.Depth,
		WebOfTrustSeedPubkey:        seedPubkey,
		LoggedOutWOTSeedDisplayName: seedDisplayName,
	}
}

// readsRecentPage returns the recent-sort reads page for the given resolution,
// dispatching to the right cache + relay-refresh strategy based on whether the
// viewer is logged out, scoped to authors, or scanning a WoT membership set.
//
// The scan-based WoT path (logged-in WoT) keeps its own pagination handling in
// fetchScannedReadsPage; the other two branches share a near-identical
// "query → maybe refresh → re-query" structure that we collapse into a single
// closure pair here so the cache-key derivation stays in one place.
func (s *Server) readsRecentPage(ctx context.Context, req feedRequest, resolved requestAuthors, membership authorMembership, window int) ([]nostrx.Event, bool) {
	if resolved.wotEnabled {
		viewer := resolved.userPubkey
		if resolved.wotViewerPubkey != "" {
			viewer = resolved.wotViewerPubkey
		}
		return s.fetchScannedReadsPage(ctx, viewer, resolved.allAuthors, membership, req.Cursor, req.CursorID, req.Limit, req.Relays, authorsCacheKey(resolved.allAuthors))
	}

	since := s.feedSince()
	scoped := !resolved.loggedOut
	cacheKey := readsCacheKey
	var queryEvents func() []nostrx.Event
	var refresh func() bool
	if scoped {
		if authorKey := authorsCacheKey(resolved.authors); authorKey != "" {
			cacheKey += "|" + authorKey
		}
		queryEvents = func() []nostrx.Event {
			out, _ := s.store.RecentByAuthorsCursor(ctx, resolved.authors, []int{nostrx.KindLongForm}, req.Cursor, req.CursorID, window)
			return out
		}
		refresh = func() bool {
			if len(resolved.authors) == 0 {
				return false
			}
			return s.refreshReadsForAuthors(ctx, resolved.userPubkey, resolved.authors, req.Cursor, window, req.Relays) >= 0
		}
	} else {
		queryEvents = func() []nostrx.Event {
			out, _ := s.store.RecentByKinds(ctx, []int{nostrx.KindLongForm}, since, req.Cursor, req.CursorID, window)
			return out
		}
		refresh = func() bool {
			return s.refreshReads(ctx, req.Cursor, window, req.Relays) >= 0
		}
	}
	events := queryEvents()
	pageKey := feedRefreshKey(cacheKey, req.Cursor, req.CursorID)
	if len(events) == 0 || s.store.ShouldRefresh(ctx, "reads", pageKey, feedTTL) {
		// Logged-out global reads: never block the request on relay refresh when
		// we already have cached rows; warm in the background instead.
		if !scoped && resolved.loggedOut && len(events) > 0 {
			s.runBackgroundUserAsync(func() {
				timeout := requestTimeout(s.cfg.RequestTimeout)
				if timeout <= 0 {
					timeout = 20 * time.Second
				}
				bgCtx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				if s.refreshReads(bgCtx, req.Cursor, window, req.Relays) >= 0 {
					s.store.MarkRefreshed(bgCtx, "reads", pageKey)
				}
			})
		} else {
			if refresh() {
				s.store.MarkRefreshed(ctx, "reads", pageKey)
			}
			events = queryEvents()
		}
	}
	return events, len(events) > req.Limit
}

func (s *Server) readDetailData(ctx context.Context, event nostrx.Event, relays []string, allowBackgroundWarm bool) ReadDetailPageData {
	read := readItem(event)
	moreEvents, _ := s.store.RecentByAuthorsCursor(ctx, []string{event.PubKey}, []int{nostrx.KindLongForm}, 0, "", 12)
	moreReads := make([]ReadItem, 0, len(moreEvents))
	profileEvents := []nostrx.Event{event}
	for _, candidate := range moreEvents {
		if candidate.ID == event.ID {
			continue
		}
		moreReads = append(moreReads, readItem(candidate))
		profileEvents = append(profileEvents, candidate)
		if len(moreReads) >= 6 {
			break
		}
	}
	if allowBackgroundWarm {
		s.warmAuthors([]string{event.PubKey}, relays)
	}
	return ReadDetailPageData{
		Read:      read,
		MoreReads: moreReads,
		Profiles:  s.profilesFor(ctx, profileEvents),
	}
}

func (s *Server) trendingReadsData(ctx context.Context, timeframe string, authors []string, membership authorMembership) []TrendingNote {
	timeframe = normalizeTrendingTimeframe(timeframe)
	since := trendingSince(timeframe, time.Now())
	var candidates []nostrx.Event
	if len(membership.exact) > 0 {
		matches, _ := s.scanTrendingReadEvents(ctx, membership, since, 0, 8)
		candidates = matches
	} else {
		candidates, _ = s.store.TrendingSummariesByKinds(ctx, []int{nostrx.KindLongForm}, since, authors, 0, 200)
	}
	if len(candidates) == 0 {
		return []TrendingNote{}
	}
	counts := s.replyCounts(ctx, candidates)
	trending := make([]TrendingNote, 0, min(len(candidates), 8))
	for _, event := range candidates {
		trending = append(trending, TrendingNote{Event: event, ReplyCount: counts[event.ID]})
		if len(trending) >= 8 {
			break
		}
	}
	return trending
}

func (s *Server) refreshReads(ctx context.Context, before int64, limit int, relays []string) int {
	defer s.observe("refresh.reads", time.Now())
	since := s.feedSince()
	query := nostrx.Query{
		Kinds: []int{nostrx.KindLongForm},
		Limit: limit,
	}
	if before > 0 {
		query.Until = before
	}
	events, err := s.nostr.FetchFrom(ctx, relays, query)
	if err != nil {
		s.metrics.Add("refresh.error", 1)
		return -1
	}
	toSave := make([]nostrx.Event, 0, len(events))
	for _, event := range events {
		if event.CreatedAt < since {
			continue
		}
		toSave = append(toSave, event)
	}
	saved := 0
	for _, event := range toSave {
		if err := s.store.SaveEvent(ctx, event); err != nil {
			slog.Warn("failed to cache event", "scope", "reads", "event", short(event.ID), "err", err)
			continue
		}
		saved++
	}
	s.metrics.Add("refresh.success", 1)
	s.metrics.Add("refresh.events", int64(saved))
	return saved
}

func (s *Server) refreshReadsForAuthors(ctx context.Context, viewer string, authors []string, before int64, limit int, relays []string) int {
	defer s.observe("refresh.reads_authors", time.Now())
	if len(authors) == 0 {
		return -1
	}
	if before <= 0 {
		before = time.Now().Unix() + 1
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
		fetched := s.refreshCached(ctx, "reads_authors", authorsCacheKey(group.authors), 0, group.relays, nostrx.Query{
			Authors: group.authors,
			Kinds:   []int{nostrx.KindLongForm},
			Until:   before,
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

// refreshReadsForTrending picks the right relay-refresh strategy for the
// trending sort branch and reports whether any refresh succeeded so the caller
// only marks the page key fresh on real progress.
func (s *Server) refreshReadsForTrending(ctx context.Context, resolved requestAuthors, authors []string, window int, relays []string) bool {
	if resolved.wotEnabled {
		viewer := resolved.userPubkey
		if resolved.wotViewerPubkey != "" {
			viewer = resolved.wotViewerPubkey
		}
		return s.refreshReadsForAuthors(ctx, viewer, resolved.allAuthors, 0, window, relays) >= 0
	}
	if len(authors) > 0 {
		return s.refreshReadsForAuthors(ctx, resolved.userPubkey, authors, 0, window, relays) >= 0
	}
	return s.refreshReads(ctx, 0, window, relays) >= 0
}

func (s *Server) scanRecentReadEvents(ctx context.Context, membership authorMembership, cursor int64, cursorID string, limit int) ([]nostrx.Event, bool) {
	target := limit + 1
	if target < 1 {
		target = 1
	}
	chunkSize := max(scanFeedChunkSize, target*4)
	scanBudget := s.scanFeedBudget()
	before := cursor
	beforeID := cursorID
	scanned := 0
	matched := make([]nostrx.Event, 0, target)
	exhausted := false
	for scanned < scanBudget && len(matched) < target {
		batchLimit := min(chunkSize, scanBudget-scanned)
		batchLimit = min(batchLimit, nostrx.MaxRelayQueryLimit)
		if batchLimit <= 0 {
			break
		}
		batch, err := s.store.RecentSummariesByKinds(ctx, []int{nostrx.KindLongForm}, 0, before, beforeID, batchLimit)
		if err != nil || len(batch) == 0 {
			exhausted = true
			break
		}
		for _, event := range batch {
			if membership.Contains(event.PubKey) {
				matched = append(matched, event)
				if len(matched) >= target {
					break
				}
			}
		}
		scanned += len(batch)
		last := batch[len(batch)-1]
		before = last.CreatedAt
		beforeID = last.ID
		if len(batch) < batchLimit {
			exhausted = true
			break
		}
	}
	return matched, exhausted
}

func (s *Server) fetchScannedReadsPage(ctx context.Context, viewer string, authors []string, membership authorMembership, cursor int64, cursorID string, limit int, relays []string, cacheKey string) ([]nostrx.Event, bool) {
	pageLimit := limit + 1
	pageKey := feedRefreshKey(cacheKey, cursor, cursorID)
	events, exhausted := s.scanRecentReadEvents(ctx, membership, cursor, cursorID, limit)
	shouldRefresh := len(events) == 0 || s.store.ShouldRefresh(ctx, "reads", pageKey, feedTTL)
	if len(events) >= pageLimit {
		return events, true
	}
	if !shouldRefresh {
		return events, len(events) >= pageLimit || (!exhausted && len(events) > 0)
	}
	before := cursor
	if len(events) > 0 {
		oldest := events[len(events)-1]
		before = oldest.CreatedAt
	}
	fetchLimit := max(pageLimit*4, loggedInFetchWindow)
	if fetchLimit > readsFetchLimit {
		fetchLimit = readsFetchLimit
	}
	if fetched := s.refreshReadsForAuthors(ctx, viewer, authors, before, fetchLimit, relays); fetched >= 0 {
		s.store.MarkRefreshed(ctx, "reads", pageKey)
	}
	next, nextExhausted := s.scanRecentReadEvents(ctx, membership, cursor, cursorID, limit)
	return next, len(next) >= pageLimit || (len(next) > 0 && !nextExhausted)
}

func (s *Server) scanTrendingReadEvents(ctx context.Context, membership authorMembership, since int64, offset int, limit int) ([]nostrx.Event, bool) {
	target := limit + 1
	if target < 1 {
		target = 1
	}
	batchSize := max(200, target*4)
	rawOffset := 0
	skipped := 0
	matched := make([]nostrx.Event, 0, target)
	exhausted := false
	for len(matched) < target {
		batch, err := s.store.TrendingSummariesByKinds(ctx, []int{nostrx.KindLongForm}, since, nil, rawOffset, batchSize)
		if err != nil || len(batch) == 0 {
			exhausted = true
			break
		}
		rawOffset += len(batch)
		for _, event := range batch {
			if !membership.Contains(event.PubKey) {
				continue
			}
			if skipped < offset {
				skipped++
				continue
			}
			matched = append(matched, event)
			if len(matched) >= target {
				break
			}
		}
		if len(batch) < batchSize {
			exhausted = true
			break
		}
	}
	hasMore := len(matched) > limit || (!exhausted && len(matched) >= limit)
	if len(matched) > limit {
		matched = matched[:limit]
	}
	return matched, hasMore
}

// hydrateReadEventsFromStore swaps in full events (including NIP-23 tags) for
// rows that were loaded via summary queries, which only expose id/pubkey/kind/
// created_at/content. Without tags, readTitle falls back to the first line of
// body text while the article detail view still shows the real title.
func (s *Server) hydrateReadEventsFromStore(ctx context.Context, events []nostrx.Event) []nostrx.Event {
	if len(events) == 0 {
		return events
	}
	ids := make([]string, 0, len(events))
	for _, e := range events {
		if id := strings.TrimSpace(e.ID); id != "" {
			ids = append(ids, id)
		}
	}
	full, err := s.store.GetEvents(ctx, ids)
	if err != nil || len(full) == 0 {
		return events
	}
	out := make([]nostrx.Event, len(events))
	for i, e := range events {
		if fe, ok := full[e.ID]; ok && fe != nil {
			out[i] = *fe
			continue
		}
		out[i] = e
	}
	return out
}

func readTitle(event nostrx.Event) string {
	title := event.FirstTagValue("title")
	if title != "" {
		return title
	}
	firstLine := strings.TrimSpace(strings.SplitN(event.Content, "\n", 2)[0])
	if firstLine == "" {
		return "Untitled"
	}
	return truncateRunes(firstLine, 80)
}

func readPublishedAt(event nostrx.Event) int64 {
	value := event.FirstTagValue("published_at")
	if value == "" {
		return event.CreatedAt
	}
	publishedAt, err := strconv.ParseInt(value, 10, 64)
	if err != nil || publishedAt <= 0 {
		return event.CreatedAt
	}
	return publishedAt
}

func readSummary(event nostrx.Event) string {
	summary := event.FirstTagValue("summary")
	if summary != "" {
		return truncateRunes(summary, 240)
	}
	trimmed := strings.TrimSpace(event.Content)
	if trimmed == "" {
		return ""
	}
	paragraphs := strings.Split(trimmed, "\n\n")
	for _, paragraph := range paragraphs {
		line := strings.TrimSpace(paragraph)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return truncateRunes(strings.ReplaceAll(line, "\n", " "), 240)
	}
	return truncateRunes(strings.ReplaceAll(paragraphs[0], "\n", " "), 240)
}

func readImage(event nostrx.Event) string {
	image := event.FirstTagValue("image")
	if image == "" {
		return ""
	}
	if strings.HasPrefix(image, "http://") || strings.HasPrefix(image, "https://") {
		return image
	}
	return ""
}

func readItem(event nostrx.Event) ReadItem {
	return ReadItem{
		Event:       event,
		Title:       readTitle(event),
		PublishedAt: readPublishedAt(event),
		Summary:     readSummary(event),
		ImageURL:    readImage(event),
	}
}
