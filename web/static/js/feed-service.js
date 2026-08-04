import { fetchNotesByAuthors, nip50Search, fetchMuteList } from "./relay-reads.js";
import { expandWebOfTrust, peekWebOfTrustDiskMembership } from "./wot-service.js";
import { getEvents, putEvents, recentTimelineEvents, searchLocalEvents } from "./event-store.js";
import { relayFetch } from "./relay-pool.js";
import { readRelaysForViewer } from "./publish-plan.js";
import { KIND_NOTE, KIND_REPOST, KIND_LONG_FORM } from "./nostr-kinds.js";
import { dedupeEventsByID, normalizePubkey } from "./relay-utils.js";
import {
  getEffectiveLoggedOutWebOfTrustSeed,
  getWebOfTrustDepthPref,
  getWebOfTrustEnabledPref,
  getWebOfTrustSeedPref,
  feedSortForSession,
  getFeedSortPref,
} from "./sort-prefs.js";
import { feedFetchModeFromPrefs, resolveFeedWoTFromInputs } from "./feed-wot.js";
import {
  authorMembershipSet,
  clampQueryAuthors,
  FEED_GLOBAL_FETCH_LIMIT,
  filterEventsByAuthorMembership,
  sortEventsNewestFirst,
} from "./feed-query.js";
import { feedPageCursor, feedPaginationCursorFromDatasets, isNewerThanFeedCursor } from "./feed-pagination.js";
import { isTrendingSort } from "./trending-service.js";
import { loadFeedPage, makeFeedQueryKey, saveFeedPage } from "./client-store.js";
import { pageIsHidden, powerLimitedCount } from "./power-mode.js";
import { fetchCachedQuery, primeQueryData, queryKeys } from "./query-client.js";
import { fetchWithSession } from "./session.js";
import { rememberProfiles } from "./profile-memory-cache.js";
import { rememberServerFeedMetadata } from "./server-feed-metadata.js";
import { appFeatures } from "./app/bootstrap.js";

export { feedPageCursor } from "./feed-pagination.js";
export { resolveFeedWoTFromInputs } from "./feed-wot.js";

const FEED_TIMELINE_KINDS = [KIND_NOTE, KIND_REPOST];
export const FEED_FIRST_PAINT_LIMIT = 25;
const FEED_FIRST_PAINT_WOT_AUTHOR_MAX = 64;
const FEED_FIRST_PAINT_GLOBAL_LIMIT = 60;

function feedCursorCacheKey({ since, until, untilID } = {}) {
  return stableCursorPart(since) + ":" + stableCursorPart(until) + ":" + String(untilID || "").toLowerCase();
}

function stableCursorPart(value) {
  const n = Number(value) || 0;
  return n > 0 ? String(n) : "";
}

function feedQueryCacheKey({ viewerPubkey, sort } = {}) {
  const mode = resolveFeedFetchModeForViewer(viewerPubkey);
  return makeFeedQueryKey({
    route: "feed",
    viewerPubkey,
    sort,
    wotEnabled: mode.kind === "wot",
    wotDepth: mode.kind === "wot" ? mode.depth : 0,
    wotSeed: mode.kind === "wot" ? mode.seed : "",
    relays: readRelaysForViewer(),
  });
}

async function cachedFeedPageEvents({ viewerPubkey, sort, since, until, untilID, limit } = {}) {
  const queryKey = feedQueryCacheKey({ viewerPubkey, sort });
  const page = await loadFeedPage(queryKey, feedCursorCacheKey({ since, until, untilID })).catch(() => null);
  const ids = page?.note_ids || [];
  if (!ids.length) return [];
  const byID = await getEvents(ids);
  const ordered = ids.map((id) => byID.get(String(id || "").toLowerCase())).filter(Boolean);
  primeQueryData(queryKeys.feedPage({
    viewerPubkey,
    sort,
    relays: readRelaysForViewer(),
    since,
    until,
    untilID,
    limit: limit || ordered.length || 50,
  }), ordered, {
    updatedAt: page?.saved_at,
  });
  return ordered.slice(0, limit || ordered.length);
}

export function preferServerFeedOnThisBrowser() {
  return true;
}

function rememberFeedPage({ viewerPubkey, sort, since, until, untilID, notes, persist = true } = {}) {
  if (!notes?.length) return;
  const queryKey = feedQueryCacheKey({ viewerPubkey, sort });
  if (persist) {
    void saveFeedPage(queryKey, feedCursorCacheKey({ since, until, untilID }), {
      note_ids: notes.map((event) => event.id).filter(Boolean),
      cursor: feedPageCursor(notes),
      viewerPubkey,
      sort,
      relays: readRelaysForViewer(),
      since,
      until,
      untilID,
      limit: notes.length,
    }).catch(() => {});
  }
  primeQueryData(queryKeys.feedPage({
    viewerPubkey,
    sort,
    relays: readRelaysForViewer(),
    since,
    until,
    untilID,
    limit: notes.length,
  }), notes);
}

function normalizedNoteIDs(noteIDs = []) {
  return [...new Set(
    (noteIDs || [])
      .map((id) => String(id || "").trim().toLowerCase())
      .filter(Boolean),
  )];
}

export function visibleFeedNoteIDs(feed) {
  if (!feed?.querySelectorAll) return [];
  return normalizedNoteIDs(
    [...feed.querySelectorAll(".note[id^='note-']")].map((note) => note.id.replace(/^note-/, "")),
  );
}

export function persistHomeFeedPageSnapshot({ viewerPubkey, sort, noteIDs = [] } = {}) {
  const ids = normalizedNoteIDs(noteIDs);
  if (!ids.length) return;
  const queryKey = feedQueryCacheKey({ viewerPubkey, sort });
  void saveFeedPage(queryKey, feedCursorCacheKey({}), {
    note_ids: ids,
  }).catch(() => {});
}

export function resolveFeedFetchModeForViewer(viewerPubkey) {
  return feedFetchModeFromPrefs(viewerPubkey, {
    wotEnabled: getWebOfTrustEnabledPref(),
    seedPref: getWebOfTrustSeedPref(),
    loggedOutDefaultSeed: getEffectiveLoggedOutWebOfTrustSeed(),
    depth: getWebOfTrustDepthPref(),
  });
}

async function fetchGlobalFeedNotes({ limit, since, until, kinds = FEED_TIMELINE_KINDS } = {}) {
  const relays = readRelaysForViewer();
  const filter = { kinds, limit };
  if (since) filter.since = since;
  if (until) filter.until = until;
  const events = await relayFetch(relays, [filter]);
  await putEvents(events);
  return dedupeEventsByID(events);
}

async function fetchWoTFeedNotes(membership, queryAuthors, { limit = 50, since, until, globalLimit: requestedGlobalLimit = FEED_GLOBAL_FETCH_LIMIT } = {}) {
  const boundedGlobalLimit = Math.max(limit, Math.min(FEED_GLOBAL_FETCH_LIMIT, Number(requestedGlobalLimit) || FEED_GLOBAL_FETCH_LIMIT));
  const globalLimit = powerLimitedCount(boundedGlobalLimit, Math.min(boundedGlobalLimit, 40));
  const fetchLimit = Math.max(limit, globalLimit);
  const [authorEvents, globalEvents] = await Promise.all([
    fetchNotesByAuthors(queryAuthors, {
      limit: fetchLimit,
      since,
      until,
      kinds: FEED_TIMELINE_KINDS,
    }),
    fetchGlobalFeedNotes({
      limit: globalLimit,
      since,
      until,
      kinds: FEED_TIMELINE_KINDS,
    }),
  ]);
  const merged = filterEventsByAuthorMembership(
    dedupeEventsByID([...authorEvents, ...globalEvents]),
    membership,
  );
  return sortEventsNewestFirst(merged).slice(0, limit);
}

/** Small first-paint feed fetch: get a usable page quickly, then let the full feed refresh continue elsewhere. */
export async function fetchFirstPaintFeedNotes({
  viewerPubkey,
  limit = FEED_FIRST_PAINT_LIMIT,
  sort,
  until,
  untilID,
} = {}) {
  const firstPaintLimit = powerLimitedCount(
    Math.min(Math.max(1, Number(limit) || FEED_FIRST_PAINT_LIMIT), FEED_FIRST_PAINT_LIMIT),
    Math.min(20, Math.max(1, Number(limit) || FEED_FIRST_PAINT_LIMIT)),
  );
  const feedSort = sort || feedSortForSession(viewerPubkey, getFeedSortPref()) || "recent";
  const serverFirst = preferServerFeedOnThisBrowser();
  if (serverFirst) {
    const serverNotes = await fetchServerFeedNotes({
      viewerPubkey,
      sort: feedSort,
      limit: firstPaintLimit,
      until,
      untilID,
      persist: false,
    });
    if (serverNotes.length) {
      rememberFeedPage({ viewerPubkey, sort: feedSort, until, untilID, notes: serverNotes, persist: false });
      return serverNotes.slice(0, firstPaintLimit);
    }
  }
  const cached = await cachedFeedNotes({
    viewerPubkey,
    sort: feedSort,
    limit: firstPaintLimit,
    until,
    untilID,
  }).catch(() => []);
  if (cached.length >= Math.min(8, firstPaintLimit)) return cached.slice(0, firstPaintLimit);

  const serverNotes = await fetchServerFeedNotes({
    viewerPubkey,
    sort: feedSort,
    limit: firstPaintLimit,
    until,
    untilID,
  });
  if (serverNotes.length) {
    rememberFeedPage({ viewerPubkey, sort: feedSort, until, untilID, notes: serverNotes });
    return serverNotes.slice(0, firstPaintLimit);
  }

  const mode = resolveFeedFetchModeForViewer(viewerPubkey);
  let notes = [];
  if (mode.kind === "firehose") {
    const events = await fetchGlobalFeedNotes({
      limit: firstPaintLimit,
      until,
      kinds: FEED_TIMELINE_KINDS,
    });
    notes = sortEventsNewestFirst(events).slice(0, firstPaintLimit);
  } else if (mode.kind === "wot") {
    const cachedMembership = peekWebOfTrustDiskMembership(mode.seed, mode.depth);
    const authors = cachedMembership
      ? [...cachedMembership].slice(0, FEED_FIRST_PAINT_WOT_AUTHOR_MAX)
      : await expandWebOfTrust(mode.seed, mode.depth, { maxAuthors: FEED_FIRST_PAINT_WOT_AUTHOR_MAX });
    const membership = authorMembershipSet(authors);
    if (membership.size) {
      notes = await fetchWoTFeedNotes(membership, clampQueryAuthors(authors, FEED_FIRST_PAINT_WOT_AUTHOR_MAX), {
        limit: firstPaintLimit,
        until,
        globalLimit: FEED_FIRST_PAINT_GLOBAL_LIMIT,
      });
    }
  }
  if (cached.length) {
    notes = sortEventsNewestFirst(dedupeEventsByID([...cached, ...notes])).slice(0, firstPaintLimit);
  }
  rememberFeedPage({ viewerPubkey, sort: feedSort, until, untilID, notes });
  return notes;
}

async function fetchFeedNotesFromRelays({
  viewerPubkey,
  limit = 50,
  since,
  until,
  sort = "",
  cacheMode = "network-first",
} = {}) {
  const relays = readRelaysForViewer();
  return fetchCachedQuery({
    queryKey: queryKeys.feedPage({
      viewerPubkey,
      sort,
      relays,
      since,
      until,
      limit,
    }),
    cacheMode,
    staleTime: since ? 5_000 : 60_000,
    queryFn: async () => {
      const mode = resolveFeedFetchModeForViewer(viewerPubkey);
      let notes = [];
      if (mode.kind === "firehose") {
        const events = await fetchGlobalFeedNotes({ limit, since, until, kinds: FEED_TIMELINE_KINDS });
        notes = sortEventsNewestFirst(events).slice(0, limit);
      } else if (mode.kind === "empty") {
        notes = [];
      } else {
        const authors = await expandWebOfTrust(mode.seed, mode.depth);
        const membership = authorMembershipSet(authors);
        if (!membership.size) {
          notes = [];
        } else {
          const queryAuthors = clampQueryAuthors(authors);
          notes = await fetchWoTFeedNotes(membership, queryAuthors, { limit, since, until });
        }
      }
      rememberFeedPage({ viewerPubkey, sort, since, until, untilID: "", notes });
      return notes;
    },
  });
}

/** IndexedDB-only home feed page (mirrors iOS FeedService.cachedHomeFeed). */
export async function cachedFeedNotes({ viewerPubkey, limit = 50, since, until, untilID, sort, skipPageCache = false } = {}) {
  const feedSort = sort || feedSortForSession(viewerPubkey, getFeedSortPref()) || "recent";
  if (isTrendingSort(feedSort)) {
    if (skipPageCache) return [];
    return cachedFeedPageEvents({ viewerPubkey, sort: feedSort, since, until, untilID, limit });
  }
  if (!skipPageCache) {
    const cachedPage = await cachedFeedPageEvents({ viewerPubkey, sort: feedSort, since, until, untilID, limit });
    if (cachedPage.length) return cachedPage;
  }
  const { beforeCreatedAt, beforeID } = feedPaginationCursorFromDatasets({ until, untilID });
  const mode = resolveFeedFetchModeForViewer(viewerPubkey);
  if (mode.kind === "firehose") {
    const events = await recentTimelineEvents({
      kinds: FEED_TIMELINE_KINDS,
      limit,
      beforeCreatedAt,
      beforeID,
      since,
    });
    return sortEventsNewestFirst(events).slice(0, limit);
  }
  if (mode.kind === "empty") {
    return [];
  }

  const authors = await expandWebOfTrust(mode.seed, mode.depth);
  const membership = authorMembershipSet(authors);
  if (!membership.size) {
    return [];
  }
  const events = await recentTimelineEvents({
    kinds: FEED_TIMELINE_KINDS,
    authors: [...membership],
    limit,
    beforeCreatedAt,
    beforeID,
    since,
  });
  return sortEventsNewestFirst(events).slice(0, limit);
}

/** Cache-first home feed load (mirrors iOS FeedService.loadHomeFeed). */
export async function fetchFeedNotes({
  viewerPubkey,
  limit = 50,
  since,
  until,
  untilID,
  forceFetch = false,
  sort,
} = {}) {
  limit = powerLimitedCount(limit, Math.min(limit, 30));
  const feedSort = sort || feedSortForSession(viewerPubkey, getFeedSortPref()) || "recent";
  const serverFirst = preferServerFeedOnThisBrowser() && !since;

  let events = [];
  let fetchedEvents = [];
  if (serverFirst) {
    const serverNotes = await fetchServerFeedNotes({
      viewerPubkey,
      sort: feedSort,
      limit,
      until,
      untilID,
      persist: false,
    });
    if (serverNotes.length) {
      events = serverNotes.slice(0, limit);
      rememberFeedPage({ viewerPubkey, sort: feedSort, since, until, untilID, notes: events, persist: false });
    }
    if (events.length) return events;
  }
  if (!forceFetch) {
    events = await cachedFeedNotes({ viewerPubkey, limit, since, until, untilID, sort: feedSort });
    if (events.length > 0 && !since && !until && !untilID) {
      void fetchFeedNotesFromRelays({
        viewerPubkey,
        limit,
        since,
        until,
        sort: feedSort,
        cacheMode: "refresh",
      }).catch(() => {});
      rememberFeedPage({ viewerPubkey, sort: feedSort, since, until, untilID, notes: events });
      return events;
    }
  }
  if (!since && !until && !untilID) {
    const serverNotes = await fetchServerFeedNotes({
      viewerPubkey,
      sort: feedSort,
      limit,
      until,
      untilID,
    });
    if (serverNotes.length) {
      events = serverNotes.slice(0, limit);
      rememberFeedPage({ viewerPubkey, sort: feedSort, since, until, untilID, notes: events });
      if (!forceFetch) {
        void fetchFeedNotesFromRelays({
          viewerPubkey,
          limit,
          since,
          until,
          sort: feedSort,
          cacheMode: "refresh",
        }).catch(() => {});
        return events;
      }
    }
  }
  if (forceFetch || events.length < limit) {
    fetchedEvents = await fetchFeedNotesFromRelays({ viewerPubkey, limit, since, until, sort: feedSort });
    events = await cachedFeedNotes({
      viewerPubkey,
      limit,
      since,
      until,
      untilID,
      sort: feedSort,
      skipPageCache: true,
    }).catch(() => []);
    if (!events.length && fetchedEvents.length) {
      events = fetchedEvents.slice(0, limit);
    }
  }
  rememberFeedPage({ viewerPubkey, sort: feedSort, since, until, untilID, notes: events });
  return events;
}

async function filterSearchResults(events, viewerPubkey) {
  const muteList = viewerPubkey ? await fetchMuteList(viewerPubkey) : null;
  const muted = new Set((muteList?.muted_pubkeys || []).map(normalizePubkey).filter(Boolean));
  const mode = resolveFeedFetchModeForViewer(viewerPubkey);
  let membership = null;
  if (mode.kind === "wot") {
    const authors = await expandWebOfTrust(mode.seed, mode.depth);
    membership = authorMembershipSet(authors);
  }
  return (events || []).filter((event) => {
    const pk = normalizePubkey(event.pubkey);
    if (muted.has(pk)) return false;
    if (membership && !membership.has(pk)) return false;
    return true;
  });
}

export async function fetchServerFeedNotes({
  viewerPubkey,
  limit = 30,
  sort,
  until,
  untilID,
  persist = true,
} = {}) {
  try {
    const url = new URL("/api/feed-notes", window.location.origin);
    url.searchParams.set("limit", String(limit || 30));
    if (sort) url.searchParams.set("sort", sort);
    if (until) url.searchParams.set("cursor", String(until));
    if (untilID) url.searchParams.set("cursor_id", String(untilID));
    const response = await fetchWithSession(url.pathname + url.search, {
      headers: { Accept: "application/json" },
    });
    if (!response.ok) return [];
    const payload = await response.json();
    rememberServerFeedMetadata(payload);
    const notes = Array.isArray(payload?.notes) ? payload.notes : [];
    if (notes.length && persist) await putEvents(notes);
    if (payload?.profiles && typeof payload.profiles === "object") {
      rememberProfiles(payload.profiles);
    }
    return notes;
  } catch {
    return [];
  }
}

async function fetchServerSearchNotes(query, {
  limit,
  scope,
  until,
  untilID,
} = {}) {
  try {
    const url = new URL("/api/search-notes", window.location.origin);
    url.searchParams.set("q", query);
    url.searchParams.set("limit", String(limit || 50));
    url.searchParams.set("scope", scope || "network");
    if (until) url.searchParams.set("cursor", String(until));
    if (untilID) url.searchParams.set("cursor_id", String(untilID));
    const response = await fetchWithSession(url.pathname + url.search, {
      headers: { Accept: "application/json" },
    });
    if (!response.ok) return [];
    const payload = await response.json();
    const notes = Array.isArray(payload?.notes) ? payload.notes : [];
    if (notes.length) await putEvents(notes);
    return notes;
  } catch {
    return [];
  }
}

export async function searchNotes(query, {
  preferLocal = true,
  limit = 50,
  viewerPubkey = "",
  scope = "network",
  until,
  untilID,
} = {}) {
  const q = String(query || "").trim();
  if (!q) return [];
  const searchKinds = [KIND_NOTE, KIND_REPOST, KIND_LONG_FORM];
  const normalizedScope = String(scope || "").trim().toLowerCase() === "all" ? "all" : "network";
  const filterViewer = normalizedScope === "network" ? viewerPubkey : "";
  const server = await fetchServerSearchNotes(q, {
    limit,
    scope: normalizedScope,
    until,
    untilID,
  });
  if (server.length) {
    const filtered = await filterSearchResults(server, filterViewer);
    if (filtered.length) return filtered.slice(0, limit);
  }
  if (preferLocal) {
    const local = await searchLocalEvents(q, {
      limit,
      kinds: searchKinds,
      beforeCreatedAt: until,
      beforeID: untilID,
    });
    const filtered = await filterSearchResults(local, filterViewer);
    if (filtered.length >= Math.min(10, limit)) return filtered.slice(0, limit);
  }
  if (until || untilID || normalizedScope === "all") {
    const local = await searchLocalEvents(q, {
      limit,
      kinds: searchKinds,
      beforeCreatedAt: until,
      beforeID: untilID,
    });
    return filterSearchResults(local, filterViewer).then((rows) => rows.slice(0, limit));
  }
  try {
    const remote = await nip50Search(q, { limit, kinds: searchKinds });
    const filtered = await filterSearchResults(remote, filterViewer);
    if (filtered.length) return filtered.slice(0, limit);
  } catch {
    // fall through
  }
  const local = await searchLocalEvents(q, {
    limit,
    kinds: searchKinds,
    beforeCreatedAt: until,
    beforeID: untilID,
  });
  return filterSearchResults(local, filterViewer).then((rows) => rows.slice(0, limit));
}

export async function hydrateFeedMetadata(events) {
  const authorSet = new Set();
  for (const event of events || []) {
    const pk = normalizePubkey(event.pubkey);
    if (pk) authorSet.add(pk);
  }
  return [...authorSet];
}

function isNewerThanTop(event, sinceCreatedAt, sinceID) {
  return isNewerThanFeedCursor(event, sinceCreatedAt, sinceID);
}

async function resolveNewerFeedAuthors(viewerPubkey) {
  const mode = resolveFeedFetchModeForViewer(viewerPubkey);
  if (mode.kind === "firehose") return null;
  if (mode.kind === "empty") return [];
  const authors = await expandWebOfTrust(mode.seed, mode.depth);
  return authors.length ? authors : [];
}

/** Background relay ingest for newer notes (mirrors iOS syncNewerHomeFeedFromRelays). */
export async function syncNewerHomeFeedFromRelays({ viewerPubkey, since, sort = "recent" } = {}) {
  if (pageIsHidden()) return [];
  if (isTrendingSort(sort)) return [];
  const sinceAt = Number(since) || 0;
  if (sinceAt <= 0) return [];
  try {
    return await fetchFeedNotesFromRelays({
      viewerPubkey,
      limit: powerLimitedCount(120, 30),
      since: Math.max(0, sinceAt - 300),
      sort,
      cacheMode: "refresh",
    });
  } catch {
    // best-effort background sync
    return [];
  }
}

async function filterNewerFeedNotes(events, { viewerPubkey, since, sinceID, sort = "recent", limit = 50 } = {}) {
  if (isTrendingSort(sort)) return [];
  const sinceAt = Number(since) || 0;
  if (!sinceAt) return [];
  const mode = resolveFeedFetchModeForViewer(viewerPubkey);
  let authors = null;
  if (mode.kind === "wot") {
    authors = await resolveNewerFeedAuthors(viewerPubkey);
    if (!authors?.length) return [];
  } else if (mode.kind === "empty") {
    return [];
  }
  const muteList = viewerPubkey ? await fetchMuteList(viewerPubkey) : null;
  const muted = new Set((muteList?.muted_pubkeys || []).map(normalizePubkey).filter(Boolean));
  const membership = mode.kind === "wot" ? authorMembershipSet(authors) : null;
  return sortEventsNewestFirst(
    dedupeEventsByID(events || []).filter((event) => {
      if (!isNewerThanTop(event, sinceAt, sinceID)) return false;
      const pk = normalizePubkey(event.pubkey);
      if (muted.has(pk)) return false;
      if (membership && !membership.has(pk)) return false;
      return true;
    }),
  ).slice(0, limit);
}

async function scanLocalNewerFeedNotes({ viewerPubkey, since, sinceID, sort = "recent", limit = 50 }) {
  if (isTrendingSort(sort)) return [];
  const sinceAt = Number(since) || 0;
  if (!sinceAt) return [];

  const mode = resolveFeedFetchModeForViewer(viewerPubkey);
  let authors = null;
  if (mode.kind === "wot") {
    authors = await resolveNewerFeedAuthors(viewerPubkey);
    if (!authors?.length) return [];
  } else if (mode.kind === "empty") {
    return [];
  }

  const batch = await recentTimelineEvents({
    kinds: FEED_TIMELINE_KINDS,
    authors: mode.kind === "wot" ? authors : null,
    limit: Math.max(limit * 4, 120),
    since: Math.max(0, sinceAt - 300),
  });
  return filterNewerFeedNotes(batch, { viewerPubkey, since, sinceID, sort, limit });
}

export async function countNewerHomeFeedNotes({
  viewerPubkey,
  since,
  sinceID,
  sort = "recent",
  visibleIds = [],
} = {}) {
  const visible = new Set((visibleIds || []).map((id) => String(id || "").toLowerCase()).filter(Boolean));
  const events = await scanLocalNewerFeedNotes({ viewerPubkey, since, sinceID, sort, limit: 100 });
  return events.filter((event) => !visible.has(String(event.id || "").toLowerCase())).length;
}

export async function fetchNewerHomeFeedNotes({
  viewerPubkey,
  since,
  sinceID,
  sort = "recent",
  visibleIds = [],
  limit = 30,
  skipSync = false,
} = {}) {
  let synced = [];
  if (!skipSync) {
    synced = await syncNewerHomeFeedFromRelays({
      viewerPubkey,
      since,
      sort,
    });
  }
  const visible = new Set((visibleIds || []).map((id) => String(id || "").toLowerCase()).filter(Boolean));
  let events = appFeatures().localFirst
    ? await filterNewerFeedNotes(synced, { viewerPubkey, since, sinceID, sort, limit: limit * 2 })
    : await scanLocalNewerFeedNotes({ viewerPubkey, since, sinceID, sort, limit: limit * 2 });
  events = events.filter((event) => !visible.has(String(event.id || "").toLowerCase()));
  return events.slice(0, limit);
}
