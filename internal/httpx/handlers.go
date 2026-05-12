package httpx

import (
	"context"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"ptxt-nstr/internal/nostrx"
	"ptxt-nstr/internal/thread"
)

func (s *Server) handleUser(w http.ResponseWriter, r *http.Request) {
	identifier := strings.TrimPrefix(r.URL.Path, "/u/")
	pubkey, err := nostrx.DecodeIdentifier(identifier)
	if err != nil {
		s.renderNotFound(w, "error_shell", ErrorPageData{
			BasePageData: s.userBasePageData(r, "User", "feed", "feed-shell"),
			ErrorPanelCopy: ErrorPanelCopy{
				Heading:          "User not found",
				Message:          "That path is not a valid Nostr public key or profile identifier.",
				Detail:           err.Error(),
				MainSectionClass: "feed-column user-profile-column route-error-column",
				ExtraScript:      "/static/js/relays.js",
			},
		})
		return
	}
	relays := s.requestRelays(r)
	viewerPub, loggedOut := s.resolveViewer(viewerFromRequest(r), relays)
	allowUserRelayWork := allowSyncRelayWork(viewerPub, loggedOut)
	if event, err := s.store.LatestReplaceable(r.Context(), pubkey, nostrx.KindProfileMetadata); err == nil && event == nil {
		if allowUserRelayWork && s.store.ShouldRefresh(r.Context(), "author", pubkey, 10*time.Minute) {
			// Explicit user profile lookup: fetch metadata only when cache is missing/stale.
			s.refreshAuthor(r.Context(), pubkey, relays)
		}
	}
	userBase := s.userBasePageData(r, "User", "feed", "feed-shell")
	fragment := r.URL.Query().Get("fragment")
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	cursorID := r.URL.Query().Get("cursor_id")
	followingQuery, followingPage := followListParams(r, "following")
	followersQuery, followersPage := followListParams(r, "followers")
	loadFollowStats := func() (FollowListView, FollowListView) {
		return s.followingList(r.Context(), pubkey, "", 1), s.followersList(r.Context(), pubkey, "", 1)
	}
	loadNotes := func() ([]nostrx.Event, bool, int64, string) {
		notes, hasMore := s.fetchAuthorsPage(r.Context(), "", []string{pubkey}, cursor, cursorID, 30, relays, "profile", pubkey)
		if len(notes) > 30 {
			notes = notes[:30]
		}
		var next int64
		var nextID string
		if len(notes) > 0 {
			last := notes[len(notes)-1]
			next = last.CreatedAt
			nextID = last.ID
		}
		return notes, hasMore, next, nextID
	}
	hydrateUserFeed := func(events []nostrx.Event) (map[string]nostrx.Event, []nostrx.Event) {
		if allowUserRelayWork {
			s.warmFeedEntities(events, relays)
			return s.referencedHydration(r.Context(), events, relays)
		}
		return s.referencedHydrationFromStore(r.Context(), events)
	}
	switch fragment {
	case "header":
		profile := s.profile(r.Context(), pubkey)
		if applyUserFragmentCache(w, r, profile) {
			return
		}
		followingStats, followersStats := loadFollowStats()
		s.render(w, "user_header", UserPageData{
			BasePageData:  userBase,
			Profile:       profile,
			FollowingList: followingStats,
			FollowersList: followersStats,
		})
		return
	case "stats":
		profile := s.profile(r.Context(), pubkey)
		if applyUserFragmentCache(w, r, profile) {
			return
		}
		followingStats, followersStats := loadFollowStats()
		data := UserPageData{
			BasePageData:  userBase,
			Profile:       profile,
			FollowingList: followingStats,
			FollowersList: followersStats,
		}
		s.render(w, "user_stats", data)
		return
	case "identifiers":
		profile := s.profile(r.Context(), pubkey)
		if applyUserFragmentCache(w, r, profile) {
			return
		}
		s.render(w, "user_identifiers", UserPageData{
			BasePageData: userBase,
			Profile:      profile,
		})
		return
	case "following":
		followingList := s.followingList(r.Context(), pubkey, followingQuery, followingPage)
		s.render(w, "user_following", UserPageData{
			BasePageData:    userBase,
			Profile:         s.profile(r.Context(), pubkey),
			FollowingList:   followingList,
			ContactProfiles: s.contactProfiles(r.Context(), followingList.Items),
		})
		return
	case "followers":
		followersList := s.followersList(r.Context(), pubkey, followersQuery, followersPage)
		s.render(w, "user_followers", UserPageData{
			BasePageData:    userBase,
			Profile:         s.profile(r.Context(), pubkey),
			FollowersList:   followersList,
			ContactProfiles: s.contactProfiles(r.Context(), followersList.Items),
		})
		return
	case "relays":
		s.render(w, "user_relays", UserPageData{
			BasePageData: userBase,
			Profile:      s.profile(r.Context(), pubkey),
			UserRelays:   s.userRelays(r.Context(), pubkey),
		})
		return
	case "posts-newer":
		since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
		sinceID := r.URL.Query().Get("since_id")
		summaries, _ := s.store.NewerSummariesByAuthorsCursor(r.Context(), []string{pubkey}, noteTimelineKinds, since, sinceID, profilePostsNewerLimit)
		count := len(summaries)
		w.Header().Set("X-Ptxt-New-Count", strconv.Itoa(count))
		if r.URL.Query().Get("body") != "1" || count == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		data := s.profilePostsNewerFeedPageDataFromSummaries(r.Context(), r, pubkey, summaries)
		s.render(w, "user_posts_items", data)
		return
	case "posts", "notes":
		notes, hasMore, next, nextID := loadNotes()
		referenced, combined := hydrateUserFeed(notes)
		rt, rv := s.viewerReactionMaps(r, relays, combined)
		data := FeedPageData{
			BasePageData:     userBase,
			Feed:             notes,
			ReferencedEvents: referenced,
			ReplyCounts:      s.replyCounts(r.Context(), combined),
			ReactionTotals:   rt,
			ReactionViewers:  rv,
			Profiles:         s.profilesFor(r.Context(), combined),
			Cursor:           next,
			CursorID:         nextID,
			HasMore:          hasMore,
			Relays:           relays,
		}
		setFeedPaginationHeaders(w, data)
		s.render(w, "user_posts_items", data)
		return
	case "replies":
		notes, _, _, _ := loadNotes()
		replies, _ := splitUserTimeline(notes)
		referenced, combined := hydrateUserFeed(replies)
		rt, rv := s.viewerReactionMaps(r, relays, combined)
		s.render(w, "user_replies_items", FeedPageData{
			BasePageData:     userBase,
			Feed:             replies,
			ReferencedEvents: referenced,
			ReplyCounts:      s.replyCounts(r.Context(), combined),
			ReactionTotals:   rt,
			ReactionViewers:  rv,
			Profiles:         s.profilesFor(r.Context(), combined),
			Relays:           relays,
		})
		return
	case "media":
		notes, _, _, _ := loadNotes()
		_, media := splitUserTimeline(notes)
		referenced, combined := hydrateUserFeed(media)
		rt, rv := s.viewerReactionMaps(r, relays, combined)
		s.render(w, "user_media_items", FeedPageData{
			BasePageData:     userBase,
			Feed:             media,
			ReferencedEvents: referenced,
			ReplyCounts:      s.replyCounts(r.Context(), combined),
			ReactionTotals:   rt,
			ReactionViewers:  rv,
			Profiles:         s.profilesFor(r.Context(), combined),
			Relays:           relays,
		})
		return
	}

	notes, hasMore, next, nextID := loadNotes()
	replies, media := splitUserTimeline(notes)
	followingList := s.followingList(r.Context(), pubkey, followingQuery, followingPage)
	followersList := s.followersList(r.Context(), pubkey, followersQuery, followersPage)
	contactKeys := uniqueNonEmptyStrings(append(append([]string{}, followingList.Items...), followersList.Items...))
	warmContacts := limitedStrings(uniqueNonEmptyStable(append([]string{pubkey}, contactKeys...)), maxWarmUserContactAuthors)
	if allowUserRelayWork {
		s.warmAuthors(warmContacts, relays)
	}
	contactProfiles := s.contactProfiles(r.Context(), contactKeys)
	referenced, combined := hydrateUserFeed(notes)
	rt, rv := s.viewerReactionMaps(r, relays, combined)
	profile := s.profile(r.Context(), pubkey)
	userBase.OG = userOG(r, pubkey, profile)
	data := UserPageData{
		BasePageData:     userBase,
		Profile:          profile,
		FollowingList:    followingList,
		FollowersList:    followersList,
		UserRelays:       s.userRelays(r.Context(), pubkey),
		Feed:             notes,
		Replies:          replies,
		Media:            media,
		ReferencedEvents: referenced,
		ReplyCounts:      s.replyCounts(r.Context(), combined),
		ReactionTotals:   rt,
		ReactionViewers:  rv,
		Profiles:         s.profilesFor(r.Context(), combined),
		ContactProfiles:  contactProfiles,
		Relays:           relays,
		Cursor:           next,
		CursorID:         nextID,
		HasMore:          hasMore,
	}
	s.render(w, "user", data)
}

func (s *Server) viewerReactionMaps(r *http.Request, relays []string, combined []nostrx.Event) (map[string]int, map[string]string) {
	viewerPub, _ := s.resolveViewer(viewerFromRequest(r), relays)
	return s.reactionMapsForEvents(r.Context(), combined, viewerPub)
}

// threadLongFormShouldOpenAsRead reports whether a NIP-23 long-form note
// should use the reads view instead of the ASCII thread layout. When
// back_read is set (replies opened from a read), the thread UI is kept.
func threadLongFormShouldOpenAsRead(r *http.Request, selected *nostrx.Event) bool {
	if selected == nil || selected.Kind != nostrx.KindLongForm {
		return false
	}
	return r.URL.Query().Get("back_read") == ""
}

func (s *Server) handleEvent(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/e/")
	if id == "" {
		s.renderNotFound(w, "error_shell", ErrorPageData{
			BasePageData: s.basePageData(r, "Note", "feed", "feed-shell"),
			ErrorPanelCopy: ErrorPanelCopy{
				Heading: "Not found",
				Message: "Missing note id after /e/.",
			},
		})
		return
	}
	target := "/thread/" + id
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func threadFragmentUsesRelayFetch(fragment string) bool {
	switch fragment {
	case "", "hydrate", "focus", "summary", "ancestors":
		return true
	default:
		return false
	}
}

func (s *Server) handleThread(w http.ResponseWriter, r *http.Request) {
	defer s.observe("handler.thread", time.Now())
	id := strings.TrimPrefix(r.URL.Path, "/thread/")
	fragment := r.URL.Query().Get("fragment")
	relays := s.requestRelays(r)
	viewerPub, loggedOut := s.resolveViewer(viewerFromRequest(r), relays)
	// UI fragments use relays for missing ancestry; fragment=replies alone stays
	// store-first (selected, resolveEvent, and reply paging bodies).
	allowThreadRelayFetch := allowSyncRelayWork(viewerPub, loggedOut) || threadFragmentUsesRelayFetch(fragment)
	repliesPaginationFragment := fragment == "replies"
	var selected *nostrx.Event
	if repliesPaginationFragment {
		selected = s.eventFromStore(r.Context(), id)
	}
	if selected == nil {
		selected = s.eventByIDEx(r.Context(), id, relays, allowThreadRelayFetch)
	}
	if selected == nil {
		s.renderNotFound(w, "error_shell", ThreadErrorShellData{
			ThreadPageData: ThreadPageData{
				BasePageData: s.basePageData(r, "Thread", "thread", "feed-shell"),
				Profiles:     map[string]nostrx.Profile{},
			},
			ErrorPanelCopy: ErrorPanelCopy{
				Heading:    "Note not found",
				Message:    "No note with this id was found in the local cache or on the relays you selected.",
				ThreadRail: true,
			},
		})
		return
	}
	if threadLongFormShouldOpenAsRead(r, selected) {
		readPath := "/reads/" + selected.ID
		if fragment != "" {
			w.Header().Set("X-Ptxt-Navigate", readPath)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if q := r.URL.RawQuery; q != "" {
			http.Redirect(w, r, readPath+"?"+q, http.StatusFound)
			return
		}
		http.Redirect(w, r, readPath, http.StatusFound)
		return
	}
	backThreadID := r.URL.Query().Get("back")
	backNoteID := r.URL.Query().Get("back_note")
	backReadID := r.URL.Query().Get("back_read")
	treeNoteID := thread.NormalizeHexEventID(r.URL.Query().Get("tree_note"))
	threadRelays := s.threadRelays(relays, *selected)
	resolvedByID := map[string]*nostrx.Event{selected.ID: selected}
	missingByID := map[string]bool{}
	resolveEvent := func(id string) *nostrx.Event {
		id = thread.NormalizeHexEventID(id)
		if id == "" {
			return nil
		}
		if event, ok := resolvedByID[id]; ok {
			return event
		}
		if missingByID[id] {
			return nil
		}
		var event *nostrx.Event
		if repliesPaginationFragment {
			event = s.eventFromStore(r.Context(), id)
		} else {
			event = s.eventByIDEx(r.Context(), id, threadRelays, allowThreadRelayFetch)
		}
		if event == nil {
			missingByID[id] = true
			return nil
		}
		resolvedByID[id] = event
		return event
	}
	rootID := resolveThreadRootID(*selected, resolveEvent)
	if rootID == "" {
		rootID = selected.ID
	}
	root := selected
	if fetchedRoot := resolveEvent(rootID); fetchedRoot != nil {
		root = fetchedRoot
	}
	// Prefer NIP-10 explicit root when the parent walk stalled on the selected
	// note or the marker names a strict ancestor of the resolved anchor—not
	// when a bogus root tag points at an intermediate (see
	// TestThreadSummaryRepairsBogusExplicitRootMarkerFromAncestorChain).
	if dec := thread.ExplicitRootID(*selected); dec != "" && dec != selected.ID {
		if ev := resolveEvent(dec); ev != nil && ev.ID != root.ID {
			if root.ID == selected.ID || explicitRootIsAboveResolvedRoot(dec, *root, resolveEvent) {
				root = ev
			}
		}
	}
	parentID := thread.ParentID(root.ID, *selected)
	// Use root.ID (resolved anchor), not rootID from the parent walk.
	if parentID != "" && parentID != root.ID && parentID != selected.ID {
		_ = resolveEvent(parentID)
	}
	refreshIDs := []string{root.ID, selected.ID}
	if parentID != "" && parentID != root.ID {
		refreshIDs = append(refreshIDs, parentID)
	}
	if allowThreadRelayFetch {
		s.warmThread(refreshIDs, threadRelays)
	}
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	cursorID := r.URL.Query().Get("cursor_id")
	fullReplyWalk := !repliesPaginationFragment && cursor == 0 && cursorID == ""
	var replies []nostrx.Event
	var fullReplies []nostrx.Event
	var nextCursor int64
	var nextID string
	var hasMore bool
	if fullReplyWalk {
		fullReplies, _ = s.threadTreeReplies(r.Context(), root.ID, repliesPaginationFragment, threadRelays)
		replies, nextCursor, nextID, hasMore = linearFirstPageFromFullReplies(fullReplies, *root, *selected)
	} else {
		// Always load direct replies to the thread root so BuildSelected can place
		// the URL-selected note in the tree. When selected != root, we also need
		// direct replies to selected (focus descendants); merging both avoids an
		// empty linear list when findFocusPath fails (e.g. missing ancestor in the
		// store) while still showing the OP's top-level conversation.
		replyParents := []string{root.ID}
		replies, nextCursor, nextID, hasMore = s.threadRepliesPage(r.Context(), cursor, cursorID, 25, repliesPaginationFragment, threadRelays, replyParents...)
		if selected.ID != root.ID && cursor == 0 && cursorID == "" {
			subReplies, subNext, subNextID, subHasMore := s.threadRepliesPage(
				r.Context(), 0, "", 25, repliesPaginationFragment, threadRelays, selected.ID,
			)
			replies = mergeThreadReplyPages(replies, subReplies)
			if !hasMore && subHasMore {
				nextCursor, nextID = subNext, subNextID
			}
			hasMore = hasMore || subHasMore
		}
	}

	all := append([]nostrx.Event(nil), *root, *selected)
	if fullReplyWalk {
		all = append(all, fullReplies...)
	} else {
		all = append(all, replies...)
	}
	var referenced map[string]nostrx.Event
	var allWithRefs []nostrx.Event
	if repliesPaginationFragment {
		referenced, allWithRefs = s.referencedHydrationFromStore(r.Context(), all)
	} else if !allowThreadRelayFetch {
		referenced, allWithRefs = s.referencedHydrationFromStore(r.Context(), all)
	} else {
		referenced, allWithRefs = s.referencedHydration(r.Context(), all, threadRelays)
	}
	if fragment == "hydrate" {
		warmReplies := replies
		if fullReplyWalk {
			warmReplies = fullReplies
		}
		s.scheduleThreadHydrateContextWarm(*selected, warmReplies, threadRelays)
	}
	needsParticipants := fragment == "" || fragment == "participants" || fragment == "hydrate"
	needsView := fragment != "participants"
	needsReplyCounts := fragment == "" || fragment == "summary" || fragment == "tree" || fragment == "focus" || fragment == "ancestors" || fragment == "replies" || fragment == "hydrate"

	profileEvents := append([]nostrx.Event(nil), allWithRefs...)

	view := thread.View{}
	treeData := ThreadTreeData{}
	replyCounts := map[string]int{}
	hiddenReplies := 0
	totalReplyCount := 0
	selectedDepth := 0
	treeSelectedID := selected.ID
	if treeNoteID != "" {
		treeSelectedID = treeNoteID
	}
	traversalPath := []nostrx.Event{*root}
	chainReplyEvents := replies
	if fullReplyWalk {
		chainReplyEvents = fullReplies
	}
	chainCandidates := collectThreadChainCandidates(root.ID, *selected, chainReplyEvents)
	if len(chainCandidates) > 0 {
		prefetched := s.eventsByIDFromStore(r.Context(), chainCandidates)
		for _, candidateID := range chainCandidates {
			if prefetched[candidateID] != nil {
				resolvedByID[candidateID] = prefetched[candidateID]
			}
		}
	}
	if needsView {
		viewReplies := buildThreadViewReplies(*root, *selected, replies, resolveEvent)
		view = thread.BuildSelected(*root, *selected, viewReplies)
		selectedDepth = selectedDepthFromRoot(*root, *selected, view, resolveEvent)
		traversalPath = buildTraversalPath(*root, *selected, view, resolveEvent)
		// Tree view is always rooted at the thread OP (full reply tree to thread.MaxDepth);
		// URL path / hash only affects TreeSelectedID (highlight), not the subtree root.
		if fullReplyWalk {
			treeData = buildThreadTreeDataFromReplies(*root, fullReplies)
		} else if !repliesPaginationFragment {
			treeData = s.buildThreadTreeData(r.Context(), *root, repliesPaginationFragment, threadRelays)
		}
		profileEvents = appendThreadProfileEvents(profileEvents, view, traversalPath)
		profileEvents = appendThreadTreeProfileEvents(profileEvents, treeData)
		totalReplyCount = view.ReplyCount
		hiddenReplies = len(view.HiddenAncestors)
		if needsReplyCounts {
			replyCounts = buildThreadDirectReplyCounts(view, all)
			if stats, err := s.store.ReplyStatsByNoteIDs(r.Context(), extractEventIDs(appendThreadTreeEvents(all, treeData))); err == nil {
				for id, stat := range stats {
					replyCounts[id] = stat.DirectReplies
					if id == root.ID {
						if stat.DirectReplies > totalReplyCount {
							totalReplyCount = stat.DirectReplies
						}
						if stat.DescendantReplies > totalReplyCount {
							totalReplyCount = stat.DescendantReplies
						}
					}
				}
			}
		}
		view.ReplyCount = totalReplyCount
	}
	if allowThreadRelayFetch {
		s.warmAuthors(limitedStrings(extractPubkeys(profileEvents), maxWarmThreadProfileAuthors), threadRelays)
	}
	profiles := s.profilesFor(r.Context(), profileEvents)
	participants := []ThreadParticipant(nil)
	if needsParticipants {
		participants = threadParticipants(all, profiles, 8)
	}
	if len(referenced) > 0 {
		for id, count := range s.replyCounts(r.Context(), mapEvents(referenced)) {
			if _, exists := replyCounts[id]; exists {
				continue
			}
			replyCounts[id] = count
		}
	}
	reactionEvents := collectThreadNotesForReactions(view, all)
	// Thread SSR is publicly cached keyed by URL only, so we must never bake
	// viewer-specific reactions in here. The client refills viewer state from
	// /api/reaction-stats in thread.js initThreadPage().
	reactionTotals, reactionViewers := s.reactionMapsForEvents(r.Context(), reactionEvents, "")

	data := ThreadPageData{
		BasePageData:     s.basePageData(r, "Thread", "thread", "feed-shell"),
		Thread:           view,
		Tree:             treeData,
		ReferencedEvents: referenced,
		ReplyCounts:      replyCounts,
		ReactionTotals:   reactionTotals,
		ReactionViewers:  reactionViewers,
		Profiles:         profiles,
		Participants:     participants,
		SelectedID:       selected.ID,
		TreeSelectedID:   treeSelectedID,
		SelectedDepth:    selectedDepth,
		TraversalPath:    traversalPath,
		RootID:           root.ID,
		ParentID:         parentID,
		BackThreadID:     backThreadID,
		BackNoteID:       backNoteID,
		BackReadID:       backReadID,
		FocusedView:      view.FocusMode,
		HiddenReplies:    hiddenReplies,
		ReplyCursor:      nextCursor,
		ReplyCursorID:    nextID,
		HasMore:          hasMore,
	}
	switch fragment {
	case "hydrate":
		setPaginationHeaders(w, nextCursor, nextID, hasMore)
		s.render(w, "thread_hydrate", data)
		return
	case "summary":
		s.render(w, "thread_summary", data)
		return
	case "tree":
		s.render(w, "thread_tree_fragment", data)
		return
	case "focus":
		s.render(w, "thread_focus_fragment", data)
		return
	case "ancestors":
		s.render(w, "thread_ancestors_fragment", data)
		return
	case "replies":
		setPaginationHeaders(w, nextCursor, nextID, hasMore)
		s.render(w, "thread_reply_items", data)
		return
	case "participants":
		s.render(w, "thread_right_rail", data)
		return
	}
	threadETag := threadPageETag(selected.ID, totalReplyCount)
	if matchesETag(r, threadETag) {
		writeNotModified(w, threadETag)
		return
	}
	setContentAddressedCache(w, threadETag)
	emitOEmbedDiscoveryHeaders(w, r)
	data.OG = threadOG(r, *selected, profiles)
	if shouldRenderInstantView(r, selected.Content, selected.Kind) {
		s.render(w, "telegram_instant_view", buildInstantViewData(r, *selected, profiles, data.OG))
		return
	}
	s.render(w, "thread", data)
}

func (s *Server) scheduleThreadHydrateContextWarm(selected nostrx.Event, replies []nostrx.Event, relays []string) {
	if s == nil {
		return
	}
	ids := collectThreadChainCandidates("", selected, replies)
	ids = append(ids, collectReferencedEventIDs(append([]nostrx.Event{selected}, replies...))...)
	ids = limitedStrings(uniqueNonEmptyStable(ids), 24)
	if len(ids) == 0 {
		return
	}
	timeout := requestTimeout(s.cfg.RequestTimeout)
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	s.runBackgroundWithTimeout("thread hydrate context warm", timeout, func(ctx context.Context) error {
		_ = s.eventsByID(ctx, ids, relays)
		return nil
	})
}

// buildInstantViewData assembles the template data passed to the Telegram
// Instant View template. The body is rendered through the existing markdown
// renderer so NIP-27 references and basic formatting survive the IV pass.
func buildInstantViewData(r *http.Request, selected nostrx.Event, profiles map[string]nostrx.Profile, og OpenGraphMeta) any {
	authorName := authorLabel(profiles, selected.PubKey)
	if authorName == "" {
		authorName = short(selected.PubKey)
	}
	publishedAt := ""
	if selected.CreatedAt > 0 {
		publishedAt = time.Unix(selected.CreatedAt, 0).UTC().Format(time.RFC3339)
	}
	return struct {
		Title       string
		AuthorName  string
		AuthorURL   string
		PublishedAt string
		ContentHTML template.HTML
		SiteName    string
		OG          OpenGraphMeta
	}{
		Title:       firstNonEmpty(og.Title, "Note on "+ogSiteName),
		AuthorName:  authorName,
		AuthorURL:   absoluteURL(r, "/u/"+selected.PubKey),
		PublishedAt: publishedAt,
		ContentHTML: renderMarkdown(selected.Content),
		SiteName:    ogSiteName,
		OG:          og,
	}
}

// userOG builds the OpenGraph meta for a /u/<pubkey> page from the resolved
// profile. We use the display/short name as title, the about text as
// description, and the proxied avatar (or the site default) as image.
func userOG(r *http.Request, pubkey string, profile nostrx.Profile) OpenGraphMeta {
	name := nostrx.DisplayName(profile)
	if name == "" {
		name = short(pubkey)
	}
	title := name + " on " + ogSiteName
	description := shortenForOG(profile.About, ogDescriptionMax)
	if description == "" {
		description = "Profile on " + ogSiteName
	}
	return OpenGraphMeta{
		Type:        ogTypeProfile,
		Title:       title,
		Description: description,
		URL:         canonicalRequestURL(r),
		Image:       ogImageForProfile(r, pubkey, profile),
		SiteName:    ogSiteName,
		Author:      name,
	}
}

// threadOG builds the OpenGraph meta for a thread page using the selected
// event's author profile (image), display name (title), and content
// (description). Falls back gracefully when the author profile is missing.
// The og:image always points at /og/<id>.png for a content-rendered card;
// social platforms that fail to fetch it gracefully fall through to the
// platform-default behavior.
func threadOG(r *http.Request, selected nostrx.Event, profiles map[string]nostrx.Profile) OpenGraphMeta {
	authorName := authorLabel(profiles, selected.PubKey)
	if authorName == "" {
		authorName = short(selected.PubKey)
	}
	description := shortenForOG(selected.Content, ogDescriptionMax)
	title := authorName
	if excerpt := shortenForOG(selected.Content, ogTitleMax-len(authorName)-3); excerpt != "" {
		title = authorName + ": " + excerpt
	}
	if title == "" {
		title = "Note on Plain Text Nostr"
	}
	image := absoluteURL(r, "/og/"+selected.ID+".png")
	if image == "" {
		image = ogImageForProfile(r, selected.PubKey, profiles[selected.PubKey])
	}
	return OpenGraphMeta{
		Type:        ogTypeArticle,
		Title:       title,
		Description: description,
		URL:         canonicalRequestURL(r),
		Image:       image,
		SiteName:    ogSiteName,
		Author:      authorName,
	}
}

// threadPageETag returns a stable identifier for the rendered /thread/<id>
// page. We mix in the total reply count so a page with new replies gets a
// fresh etag and any CDN/browser cache revalidates instead of serving the
// pre-reply snapshot for the full stale-while-revalidate window.
func threadPageETag(selectedID string, replyCount int) string {
	if selectedID == "" {
		return ""
	}
	return selectedID + "-r" + strconv.Itoa(replyCount)
}

// applyUserFragmentCache attaches content-addressed cache headers to a profile
// fragment response when the underlying kind-0 event is known. Returns true
// when a 304 was written and the caller must NOT render a body.
func applyUserFragmentCache(w http.ResponseWriter, r *http.Request, profile nostrx.Profile) bool {
	if profile.Event == nil || profile.Event.ID == "" {
		return false
	}
	etag := profile.Event.ID
	if matchesETag(r, etag) {
		writeNotModified(w, etag)
		return true
	}
	setContentAddressedCache(w, etag)
	return false
}

// explicitRootIsAboveResolvedRoot reports whether explicitID names an event
// strictly above resolvedRoot in the absolute parent chain (ParentID with
// empty root context), so upgrading the thread anchor is safe.
func explicitRootIsAboveResolvedRoot(explicitID string, resolvedRoot nostrx.Event, lookup func(string) *nostrx.Event) bool {
	explicitID = thread.NormalizeHexEventID(explicitID)
	if explicitID == "" || resolvedRoot.ID == "" || explicitID == resolvedRoot.ID {
		return false
	}
	cur := resolvedRoot
	seen := map[string]bool{resolvedRoot.ID: true}
	for hops := 0; hops < thread.MaxDepth; hops++ {
		p := thread.NormalizeHexEventID(thread.ParentID("", cur))
		if p == "" || p == cur.ID || seen[p] {
			return false
		}
		if p == explicitID {
			return true
		}
		seen[p] = true
		parent := lookup(p)
		if parent == nil {
			return false
		}
		cur = *parent
	}
	return false
}

func resolveThreadRootID(selected nostrx.Event, lookup func(string) *nostrx.Event) string {
	if selected.ID == "" {
		return ""
	}
	current := selected
	lastID := current.ID
	seen := map[string]bool{current.ID: true}
	for hops := 0; hops < thread.MaxDepth; hops++ {
		parentID := thread.ParentID("", current)
		if parentID == "" || parentID == current.ID || seen[parentID] {
			break
		}
		lastID = parentID
		seen[parentID] = true
		if lookup == nil {
			break
		}
		parent := lookup(parentID)
		if parent == nil {
			break
		}
		current = *parent
	}
	return lastID
}

// mergeThreadReplyPages merges two reply slices (e.g. direct children of the
// thread root and of the URL-selected note), deduplicated by event id and
// ordered like threadRepliesPage (created_at, then id).
func mergeThreadReplyPages(primary, extra []nostrx.Event) []nostrx.Event {
	seen := make(map[string]bool, len(primary)+len(extra))
	out := make([]nostrx.Event, 0, len(primary)+len(extra))
	for _, ev := range primary {
		if ev.ID == "" || seen[ev.ID] {
			continue
		}
		seen[ev.ID] = true
		out = append(out, ev)
	}
	for _, ev := range extra {
		if ev.ID == "" || seen[ev.ID] {
			continue
		}
		seen[ev.ID] = true
		out = append(out, ev)
	}
	sortThreadRepliesStable(out)
	return out
}

const threadLinearPageLimit = 25

// linearFirstPageFromFullReplies matches threadRepliesPage + mergeThreadReplyPages
// for cursor 0: direct replies to root (first threadLinearPageLimit), merged with
// direct replies to selected when selected is not the root. Used so the linear
// column keeps Twitter-style shallow rendering while the tree uses the full BFS slice.
func linearFirstPageFromFullReplies(full []nostrx.Event, root, selected nostrx.Event) (merged []nostrx.Event, nextCursor int64, nextID string, hasMore bool) {
	rootKids := make([]nostrx.Event, 0, threadLinearPageLimit)
	var selKids []nostrx.Event
	selectedIsRoot := selected.ID == "" || selected.ID == root.ID
	if !selectedIsRoot {
		selKids = make([]nostrx.Event, 0, threadLinearPageLimit)
	}
	for i := range full {
		e := full[i]
		if e.ID == "" {
			continue
		}
		switch thread.ParentID(root.ID, e) {
		case root.ID:
			rootKids = append(rootKids, e)
		case selected.ID:
			if selKids != nil {
				selKids = append(selKids, e)
			}
		}
	}
	sortThreadRepliesStable(rootKids)
	var rootPage []nostrx.Event
	if len(rootKids) > threadLinearPageLimit {
		rootPage = rootKids[:threadLinearPageLimit]
	} else {
		rootPage = rootKids
	}
	rootHasMore := len(rootKids) > threadLinearPageLimit

	if selectedIsRoot {
		if rootHasMore {
			n := rootKids[threadLinearPageLimit]
			return rootPage, n.CreatedAt, n.ID, true
		}
		return rootPage, 0, "", false
	}

	sortThreadRepliesStable(selKids)
	var selPage []nostrx.Event
	selHasMore := len(selKids) > threadLinearPageLimit
	if selHasMore {
		selPage = selKids[:threadLinearPageLimit]
	} else {
		selPage = selKids
	}
	merged = mergeThreadReplyPages(rootPage, selPage)
	allHasMore := rootHasMore || selHasMore
	if rootHasMore {
		n := rootKids[threadLinearPageLimit]
		return merged, n.CreatedAt, n.ID, allHasMore
	}
	if selHasMore {
		n := selKids[threadLinearPageLimit]
		return merged, n.CreatedAt, n.ID, allHasMore
	}
	return merged, 0, "", false
}

func buildThreadViewReplies(root nostrx.Event, selected nostrx.Event, directReplies []nostrx.Event, lookup func(string) *nostrx.Event) []nostrx.Event {
	seen := make(map[string]bool, len(directReplies)+4)
	viewReplies := make([]nostrx.Event, 0, len(directReplies)+4)
	appendUnique := func(event nostrx.Event) {
		if event.ID == "" || event.ID == root.ID || seen[event.ID] {
			return
		}
		seen[event.ID] = true
		viewReplies = append(viewReplies, event)
	}
	for _, reply := range directReplies {
		appendUnique(reply)
	}
	if selected.ID != root.ID {
		appendUnique(selected)
	}
	if lookup == nil {
		return viewReplies
	}
	current := selected
	for depth := 0; depth < thread.MaxDepth; depth++ {
		parentID := thread.ParentID(root.ID, current)
		if parentID == "" || parentID == root.ID || parentID == current.ID || seen[parentID] {
			break
		}
		parent := lookup(parentID)
		if parent == nil {
			break
		}
		appendUnique(*parent)
		current = *parent
	}
	return viewReplies
}

func selectedDepthFromRoot(root nostrx.Event, selected nostrx.Event, view thread.View, lookup func(string) *nostrx.Event) int {
	if root.ID == "" || selected.ID == "" {
		return 1
	}
	if selected.ID == root.ID {
		return 1
	}
	if view.FocusMode && view.SelectedNode != nil && view.SelectedNode.Depth > 0 {
		return view.SelectedNode.Depth + 1
	}
	depth := 0
	current := selected
	seen := map[string]bool{current.ID: true}
	for hops := 0; hops < thread.MaxDepth; hops++ {
		parentID := thread.ParentID(root.ID, current)
		if parentID == "" || parentID == current.ID || seen[parentID] {
			break
		}
		depth++
		if parentID == root.ID {
			return depth + 1
		}
		seen[parentID] = true
		if lookup == nil {
			break
		}
		parent := lookup(parentID)
		if parent == nil {
			break
		}
		current = *parent
	}
	if depth == 0 {
		return 1
	}
	return depth + 1
}

func buildTraversalPath(root nostrx.Event, selected nostrx.Event, view thread.View, lookup func(string) *nostrx.Event) []nostrx.Event {
	if root.ID == "" {
		return nil
	}
	path := make([]nostrx.Event, 0, 8)
	appendUnique := func(event nostrx.Event) {
		if event.ID == "" {
			return
		}
		if len(path) > 0 && path[len(path)-1].ID == event.ID {
			return
		}
		path = append(path, event)
	}
	appendUnique(root)
	if selected.ID == "" || selected.ID == root.ID {
		return path
	}
	if view.FocusMode {
		for _, ancestor := range view.HiddenAncestors {
			appendUnique(ancestor.Event)
		}
		if view.ParentNode != nil {
			appendUnique(view.ParentNode.Event)
		}
		appendUnique(selected)
		return path
	}
	reversed := []nostrx.Event{selected}
	current := selected
	seen := map[string]bool{current.ID: true}
	for hops := 0; hops < thread.MaxDepth; hops++ {
		parentID := thread.ParentID(root.ID, current)
		if parentID == "" || parentID == current.ID || seen[parentID] {
			break
		}
		if parentID == root.ID {
			reversed = append(reversed, root)
			break
		}
		if lookup == nil {
			break
		}
		parent := lookup(parentID)
		if parent == nil {
			break
		}
		reversed = append(reversed, *parent)
		seen[parent.ID] = true
		current = *parent
	}
	for i := len(reversed) - 1; i >= 0; i-- {
		appendUnique(reversed[i])
	}
	return path
}

func appendThreadProfileEvents(base []nostrx.Event, view thread.View, traversalPath []nostrx.Event) []nostrx.Event {
	seen := make(map[string]bool, len(base)+len(traversalPath)+8)
	out := make([]nostrx.Event, 0, len(base)+len(traversalPath)+8)
	appendEvent := func(event nostrx.Event) {
		if event.ID == "" || seen[event.ID] {
			return
		}
		seen[event.ID] = true
		out = append(out, event)
	}
	for _, event := range base {
		appendEvent(event)
	}
	for _, event := range traversalPath {
		appendEvent(event)
	}
	if view.Root != nil {
		appendEvent(*view.Root)
	}
	if view.Selected != nil {
		appendEvent(*view.Selected)
	}
	for _, ancestor := range view.HiddenAncestors {
		appendEvent(ancestor.Event)
	}
	if view.ParentNode != nil {
		appendEvent(view.ParentNode.Event)
	}
	if view.SelectedNode != nil {
		appendThreadProfileNodeEvents(&out, seen, *view.SelectedNode)
	}
	for _, node := range view.Nodes {
		appendThreadProfileNodeEvents(&out, seen, node)
	}
	return out
}

func appendThreadTreeProfileEvents(base []nostrx.Event, treeData ThreadTreeData) []nostrx.Event {
	if treeData.Root.ID == "" {
		return base
	}
	view := thread.View{
		Root:  &treeData.Root,
		Nodes: treeData.Nodes,
	}
	return appendThreadProfileEvents(base, view, nil)
}

func appendThreadTreeEvents(base []nostrx.Event, treeData ThreadTreeData) []nostrx.Event {
	if treeData.Root.ID == "" {
		return base
	}
	out := append([]nostrx.Event(nil), base...)
	out = append(out, treeData.Root)
	var walk func(thread.Node)
	walk = func(node thread.Node) {
		if node.Event.ID != "" {
			out = append(out, node.Event)
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	for _, node := range treeData.Nodes {
		walk(node)
	}
	return out
}

func appendThreadProfileNodeEvents(out *[]nostrx.Event, seen map[string]bool, node thread.Node) {
	if node.Event.ID != "" && !seen[node.Event.ID] {
		seen[node.Event.ID] = true
		*out = append(*out, node.Event)
	}
	for _, child := range node.Children {
		appendThreadProfileNodeEvents(out, seen, child)
	}
}

func extractPubkeys(events []nostrx.Event) []string {
	pubkeys := make([]string, 0, len(events))
	seen := make(map[string]bool, len(events))
	for _, event := range events {
		if event.PubKey == "" || seen[event.PubKey] {
			continue
		}
		seen[event.PubKey] = true
		pubkeys = append(pubkeys, event.PubKey)
	}
	return pubkeys
}

func threadParticipants(events []nostrx.Event, profiles map[string]nostrx.Profile, limit int) []ThreadParticipant {
	if limit <= 0 {
		return nil
	}
	type participantStats struct {
		posts        int
		firstCreated int64
	}
	statsByPubKey := make(map[string]participantStats, len(events))
	seenEvents := make(map[string]bool, len(events))
	for _, event := range events {
		if event.PubKey == "" {
			continue
		}
		if event.ID != "" {
			if seenEvents[event.ID] {
				continue
			}
			seenEvents[event.ID] = true
		}
		stats := statsByPubKey[event.PubKey]
		stats.posts++
		if stats.firstCreated == 0 || (event.CreatedAt > 0 && event.CreatedAt < stats.firstCreated) {
			stats.firstCreated = event.CreatedAt
		}
		statsByPubKey[event.PubKey] = stats
	}
	participants := make([]ThreadParticipant, 0, len(statsByPubKey))
	for pubkey, stats := range statsByPubKey {
		participants = append(participants, ThreadParticipant{
			PubKey:  pubkey,
			Profile: profiles[pubkey],
			Posts:   stats.posts,
		})
	}
	sort.Slice(participants, func(i, j int) bool {
		left := participants[i]
		right := participants[j]
		if left.Posts != right.Posts {
			return left.Posts > right.Posts
		}
		leftCreated := statsByPubKey[left.PubKey].firstCreated
		rightCreated := statsByPubKey[right.PubKey].firstCreated
		if leftCreated != rightCreated {
			return leftCreated < rightCreated
		}
		return left.PubKey < right.PubKey
	})
	if len(participants) > limit {
		participants = participants[:limit]
	}
	return participants
}

func extractEventIDs(events []nostrx.Event) []string {
	ids := make([]string, 0, len(events))
	seen := make(map[string]bool, len(events))
	for _, event := range events {
		if event.ID == "" || seen[event.ID] {
			continue
		}
		seen[event.ID] = true
		ids = append(ids, event.ID)
	}
	return ids
}

func collectThreadChainCandidates(rootID string, selected nostrx.Event, replies []nostrx.Event) []string {
	seen := make(map[string]bool, len(replies)+8)
	candidates := make([]string, 0, len(replies)+8)
	appendID := func(id string) {
		if id == "" || id == rootID || id == selected.ID || seen[id] {
			return
		}
		seen[id] = true
		candidates = append(candidates, id)
	}
	appendID(thread.ParentID(rootID, selected))
	for _, reply := range replies {
		appendID(thread.ParentID(rootID, reply))
		appendID(thread.RootID(reply))
		for _, tag := range reply.Tags {
			if len(tag) >= 2 && tag[0] == "e" {
				appendID(tag[1])
			}
		}
	}
	return candidates
}

func buildThreadDirectReplyCounts(view thread.View, events []nostrx.Event) map[string]int {
	counts := make(map[string]int, len(events))
	for _, event := range events {
		if event.ID == "" {
			continue
		}
		counts[event.ID] = 0
	}
	if view.Root != nil {
		counts[view.Root.ID] = len(view.Nodes)
	}
	accumulateNodeDirectCounts(counts, view.Nodes)
	return counts
}

func setFeedPaginationHeaders(w http.ResponseWriter, data FeedPageData) {
	setPaginationHeaders(w, data.Cursor, data.CursorID, data.HasMore)
	if data.FeedSnapshotStarter {
		w.Header().Set("X-Ptxt-Feed-Snapshot-Starter", "1")
	}
}

func setPaginationHeaders(w http.ResponseWriter, cursor int64, cursorID string, hasMore bool) {
	if cursor > 0 {
		w.Header().Set("X-Ptxt-Cursor", strconv.FormatInt(cursor, 10))
	}
	if cursorID != "" {
		w.Header().Set("X-Ptxt-Cursor-Id", cursorID)
	}
	if hasMore {
		w.Header().Set("X-Ptxt-Has-More", "1")
	} else {
		w.Header().Set("X-Ptxt-Has-More", "0")
	}
}

func accumulateNodeDirectCounts(counts map[string]int, nodes []thread.Node) {
	for _, node := range nodes {
		counts[node.Event.ID] = len(node.Children)
		if len(node.Children) > 0 {
			accumulateNodeDirectCounts(counts, node.Children)
		}
	}
}

func splitUserTimeline(events []nostrx.Event) (replies []nostrx.Event, media []nostrx.Event) {
	replies = make([]nostrx.Event, 0, len(events))
	media = make([]nostrx.Event, 0, len(events))
	for _, event := range events {
		if isReplyEvent(event) {
			replies = append(replies, event)
		}
		if hasMediaContent(event.Content) {
			media = append(media, event)
		}
	}
	return replies, media
}

func isReplyEvent(event nostrx.Event) bool {
	if event.Kind != nostrx.KindTextNote {
		return false
	}
	hasReference := false
	for _, tag := range event.Tags {
		if len(tag) < 2 || tag[0] != "e" {
			continue
		}
		hasReference = true
		if len(tag) >= 4 && (tag[3] == "reply" || tag[3] == "root") {
			return true
		}
	}
	return hasReference
}

func hasMediaContent(content string) bool {
	lower := strings.ToLower(content)
	if !strings.Contains(lower, "http://") && !strings.Contains(lower, "https://") {
		return false
	}
	markers := []string{
		".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".svg",
		".mp4", ".webm", ".mov", ".m4v", ".mkv", ".mp3", ".wav", ".ogg",
		"youtube.com/", "youtu.be/", "vimeo.com/", "tenor.com/", "giphy.com/",
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func followListParams(r *http.Request, list string) (string, int) {
	query := strings.TrimSpace(r.URL.Query().Get(list + "_q"))
	pageRaw := r.URL.Query().Get(list + "_page")
	page, _ := strconv.Atoi(pageRaw)
	if page < 1 {
		page = 1
	}
	return query, page
}
