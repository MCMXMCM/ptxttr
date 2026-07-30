import { nip19 } from "../lib/nostr-tools.js";
import { relayFetch } from "./relay-pool.js";
import { readRelaysForViewer } from "./publish-plan.js";
import { fetchWithSession } from "./session.js";
import {
  INDEXER_NIP50_RELAYS,
  TRENDING_SEARCH_RELAYS,
  REACTION_SEARCH_RELAYS,
} from "./relay-config.js";
import {
  KIND_BOOKMARK,
  KIND_FOLLOW,
  KIND_MUTE,
  KIND_NOTE,
  KIND_PROFILE,
  KIND_POLL_RESPONSE,
  KIND_POLL,
  KIND_REACTION,
  KIND_RELAY_LIST,
  KIND_ZAP_RECEIPT,
  KIND_LONG_FORM,
  DEFAULT_QUERY_LIMIT,
  MAX_QUERY_LIMIT,
} from "./nostr-kinds.js";
import {
  bookmarkEntries,
  authorWriteRelaysFromKind10002,
  canonicalHex64,
  dedupeEventsByID,
  followPubkeys,
  followRelayHints,
  isCanonicalEventID,
  mutePubkeys,
  normalizePubkey,
  relayHintsFromKind10002,
  resolveEventID,
  uniqueNonEmpty,
} from "./relay-utils.js";
import { normalizeRelayList } from "./relay-config.js";
import { effectiveWriteRelays } from "./relay-state.js";
import { parseProfile, profileAPIEntry } from "./profile-parse.js";
import { rememberProfile, rememberProfiles } from "./profile-memory-cache.js";
import { chunkAuthors, sortEventsNewestFirst } from "./feed-query.js";
import {
  getEvent,
  getEvents,
  latestReplaceable,
  putEvents,
  eventsByTag,
  eventsByAuthors,
  replyCounts as localReplyCounts,
  reactionTotals as localReactionTotals,
} from "./event-store.js";
import { isClientDBUnavailableError } from "./client-store.js";
import { fetchCachedQuery, peekQueryData, primeQueryData, queryKeys } from "./query-client.js";

const METADATA_BATCH_SIZE = 10;
const OUTBOX_PLAN_TTL_MS = 5 * 60 * 1000;
const outboxPlanCache = new Map();

function mergedReadRelays(preferred = []) {
  return normalizeRelayList([...preferred, ...readRelaysForViewer()]);
}

function readRelaysForFetch(preferred = [], { includeViewerRelays = true } = {}) {
  const relays = includeViewerRelays
    ? [...preferred, ...readRelaysForViewer()]
    : preferred;
  return normalizeRelayList(relays);
}

function replaceableRelaysForFetch(preferred = [], { includeViewerRelays = true } = {}) {
  const relays = includeViewerRelays
    ? [...preferred, ...readRelaysForViewer(), ...effectiveWriteRelays()]
    : preferred;
  return normalizeRelayList(relays);
}

function metadataFetchRelays(preferred = []) {
  return normalizeRelayList([...preferred, ...readRelaysForViewer(), ...REACTION_SEARCH_RELAYS]);
}

function mergeReplyCountMaps(remote = {}, local = new Map()) {
  const out = { ...remote };
  for (const [id, count] of local.entries()) {
    const next = Number.parseInt(`${count ?? 0}`, 10) || 0;
    const prev = Number.parseInt(`${out[id] ?? 0}`, 10) || 0;
    out[id] = Math.max(prev, next);
  }
  return out;
}

function mergeReactionStatsMaps(remote = {}, local = new Map()) {
  const out = { ...remote };
  for (const [id, total] of local.entries()) {
    const localTotal = Number.parseInt(`${total ?? 0}`, 10) || 0;
    const row = out[id] || { total: 0, viewer: "" };
    const remoteTotal = Number.parseInt(`${row.total ?? 0}`, 10) || 0;
    out[id] = { total: Math.max(remoteTotal, localTotal), viewer: row.viewer || "" };
  }
  return out;
}

function mergeProfileMaps(...maps) {
  const out = {};
  for (const map of maps) {
    if (!map || typeof map !== "object") continue;
    for (const [pubkey, profile] of Object.entries(map)) {
      if (!pubkey || !profile || typeof profile !== "object") continue;
      out[pubkey] = { ...(out[pubkey] || {}), ...profile };
    }
  }
  return out;
}

export function chunkValues(values = [], size = METADATA_BATCH_SIZE) {
  const normalizedSize = Math.max(1, Number(size) || 1);
  const chunks = [];
  for (let index = 0; index < values.length; index += normalizedSize) {
    chunks.push(values.slice(index, index + normalizedSize));
  }
  return chunks;
}

export function groupedRelayHintBatches(ids = [], relayHintsByID = {}) {
  const groups = new Map();
  ids.forEach((id) => {
    const hints = normalizeRelayList(relayHintsByID[id] || []).sort();
    const key = hints.join("|");
    if (!groups.has(key)) {
      groups.set(key, { ids: [], hints });
    }
    groups.get(key).ids.push(id);
  });
  return [...groups.values()];
}

export function eventFetchRelayStages(explicitHints = [], authorOutbox = [], fallbackRelays = []) {
  const stages = [];
  const push = (relays) => {
    const normalized = normalizeRelayList(relays);
    if (!normalized.length) return;
    if (stages.some((stage) => eventRelayListsMatch(stage, normalized))) return;
    stages.push(normalized);
  };
  push(explicitHints);
  push(authorOutbox);
  push(fallbackRelays);
  return stages;
}

function eventRelayListsMatch(current = [], next = []) {
  if (current.length !== next.length) return false;
  return current.every((relay, index) => relay === next[index]);
}

function eventsMatchingReferencedID(events = [], id = "", tagName = "e") {
  if (!id) return [];
  return events.filter((event) => (event.tags || []).some((tag) => Array.isArray(tag) && tag[0] === tagName && tag[1] === id));
}

async function fetchETagEventsBatched(relays, ids, { kinds, perEventLimit, tagName = "e", chunkSize = METADATA_BATCH_SIZE } = {}) {
  const uniqueIDs = [...new Set((ids || []).map(canonicalHex64).filter(Boolean))];
  if (!uniqueIDs.length) return [];
  const filters = chunkValues(uniqueIDs, chunkSize).map((chunk) => ({
    kinds,
    [`#${tagName}`]: chunk,
    limit: chunk.length * perEventLimit,
  }));
  const events = await relayFetch(relays, filters);
  return dedupeEventsByID(events);
}

async function withServerFallback(directFn, serverFn) {
  void serverFn;
  return directFn();
}

async function fetchReplaceable(pubkey, kind, { relays = [], forceRefresh = false, includeViewerRelays = true } = {}) {
  const pk = normalizePubkey(pubkey);
  if (!pk) return null;
  const relayList = replaceableRelaysForFetch(relays, { includeViewerRelays });
  if (!relayList.length) return latestReplaceable(pk, kind);
  const queryKey = queryKeys.replaceable(pk, kind, relayList);
  const queryFn = async () => {
    const cached = await latestReplaceable(pk, kind);
    const events = await relayFetch(relayList, [{ authors: [pk], kinds: [kind], limit: 3 }]);
    const latest = dedupeEventsByID(events).sort((a, b) => Number(b.created_at) - Number(a.created_at))[0];
    if (latest) void putEvents([latest]);
    if (!latest) return cached;
    if (!cached || Number(latest.created_at) >= Number(cached.created_at)) return latest;
    return cached;
  };

  if (!forceRefresh) {
    const seeded = peekQueryData(queryKey);
    const cached = await latestReplaceable(pk, kind).catch(() => null);
    const immediate = cached || seeded || null;
    if (immediate) {
      primeQueryData(queryKey, immediate, {
        updatedAt: Number(immediate?.created_at || 0) * 1000 || Date.now(),
      });
      void fetchCachedQuery({
        queryKey,
        queryFn,
        cacheMode: "refresh",
        staleTime: 0,
      }).catch(() => {});
      return immediate;
    }
  }

  return fetchCachedQuery({
    queryKey,
    queryFn,
    cacheMode: forceRefresh ? "refresh" : "cache-first",
    staleTime: forceRefresh ? 0 : 30_000,
  });
}

export async function fetchBookmarks(pubkey) {
  const event = await fetchReplaceable(pubkey, KIND_BOOKMARK);
  const entries = bookmarkEntries(event);
  return {
    pubkey: normalizePubkey(pubkey),
    event_id: event?.id || "",
    created_at: event?.created_at || 0,
    entries: entries.map((row) => ({ id: row.id, relay: row.relay })),
    ids: entries.map((row) => row.id),
    count: entries.length,
  };
}

export async function fetchMuteList(pubkey) {
  const event = await fetchReplaceable(pubkey, KIND_MUTE);
  return {
    pubkey: normalizePubkey(pubkey),
    muted_pubkeys: mutePubkeys(event),
  };
}

export async function fetchViewerProfile(pubkey, { forceRefresh = false } = {}) {
  const event = await fetchReplaceable(pubkey, KIND_PROFILE, { forceRefresh });
  return rememberProfile(parseProfile(pubkey, event));
}

/** Full kind-0 profile for any pubkey (relay-first). */
export async function fetchProfile(pubkey, { relays = [], forceRefresh = false, includeViewerRelays = true } = {}) {
  const event = await fetchReplaceable(pubkey, KIND_PROFILE, { relays, forceRefresh, includeViewerRelays });
  return rememberProfile(parseProfile(pubkey, event));
}

export async function fetchProfileRelayHints(pubkey, { relays = [], includeViewerRelays = true } = {}) {
  const event = await fetchReplaceable(pubkey, KIND_RELAY_LIST, { relays, includeViewerRelays });
  return relayHintsFromKind10002(event);
}

export function relayPreferencesFromKind10002Event(event) {
  const hints = relayHintsFromKind10002(event);
  const byURL = new Map();
  const add = (url, usage) => {
    const relay = normalizeRelayList([url])[0];
    if (!relay) return;
    const existing = byURL.get(relay);
    if (!existing) {
      byURL.set(relay, usage);
      return;
    }
    if (existing !== usage) byURL.set(relay, "any");
  };
  (hints.any || []).forEach((url) => add(url, "any"));
  (hints.write || []).forEach((url) => add(url, "write"));
  (hints.read || []).forEach((url) => add(url, "read"));
  return [...byURL.entries()].map(([url, usage]) => ({ url, usage }));
}

export async function fetchViewerRelayPreferences(pubkey, { forceRefresh = true } = {}) {
  const pk = normalizePubkey(pubkey);
  const event = await fetchReplaceable(pk, KIND_RELAY_LIST, { forceRefresh });
  return {
    pubkey: pk,
    event_id: event?.id || "",
    created_at: Number(event?.created_at || 0) || 0,
    relays: relayPreferencesFromKind10002Event(event),
  };
}

export async function fetchProfiles(pubkeys, { relays = [] } = {}) {
  const keys = (pubkeys || []).map(normalizePubkey).filter(Boolean);
  if (!keys.length) return {};
  const relayList = mergedReadRelays(relays);
  const queryKey = queryKeys.profiles(keys, relayList);
  const queryFn = async () => {
    const existing = peekQueryData(queryKey);
    const events = await relayFetch(relayList, [{ authors: keys, kinds: [KIND_PROFILE], limit: keys.length * 2 }]);
    await putEvents(events);
    const byAuthor = new Map();
    for (const event of dedupeEventsByID(events)) {
      const pk = normalizePubkey(event.pubkey);
      const prev = byAuthor.get(pk);
      if (!prev || Number(event.created_at) > Number(prev.created_at)) {
        byAuthor.set(pk, event);
      }
    }
    const out = mergeProfileMaps(existing);
    for (const pk of keys) {
      const cached = await latestReplaceable(pk, KIND_PROFILE);
      const event = byAuthor.get(pk) || cached;
      if (event) {
        out[pk] = profileAPIEntry(rememberProfile(parseProfile(pk, event)));
      } else if (!out[pk]) {
        out[pk] = {};
      }
    }
    return rememberProfiles(out);
  };

  const cachedEntries = await Promise.all(keys.map(async (pk) => [pk, await latestReplaceable(pk, KIND_PROFILE).catch(() => null)]));
  const existing = peekQueryData(queryKey);
  const cachedProfiles = mergeProfileMaps(
    existing,
    Object.fromEntries(
      cachedEntries
        .filter(([, event]) => Boolean(event))
        .map(([pk, event]) => [pk, profileAPIEntry(rememberProfile(parseProfile(pk, event)))]),
    ),
  );
  rememberProfiles(cachedProfiles);
  if (Object.keys(cachedProfiles).length > 0) {
    primeQueryData(queryKey, cachedProfiles);
    void fetchCachedQuery({
      queryKey,
      queryFn,
      cacheMode: "refresh",
      staleTime: 0,
    }).catch(() => {});
    return cachedProfiles;
  }

  return fetchCachedQuery({
    queryKey,
    queryFn,
    cacheMode: "cache-first",
    staleTime: 30_000,
  });
}

export async function fetchProfileFollowGraph(pubkey, { relays = [], followerLimit = 250, includeViewerRelays = true } = {}) {
  const pk = normalizePubkey(pubkey);
  if (!pk) {
    return {
      pubkey: "",
      following: [],
      followers: [],
      followEvent: null,
      relayHints: new Map(),
    };
  }
  const relayList = readRelaysForFetch(relays, { includeViewerRelays });
  return withServerFallback(
    async () => fetchCachedQuery({
      queryKey: queryKeys.followGraph(pk, relayList, followerLimit),
      queryFn: async () => {
        if (!relayList.length) {
          return {
            pubkey: pk,
            following: [],
            followers: [],
            followEvent: null,
            relayHints: new Map(),
          };
        }
        const events = await relayFetch(relayList, [
          { authors: [pk], kinds: [KIND_FOLLOW], limit: 3 },
          { kinds: [KIND_FOLLOW], "#p": [pk], limit: Math.max(1, Number(followerLimit) || 250) },
        ]);
        await putEvents(events);
        const deduped = dedupeEventsByID(events);
        const followEvent = deduped
          .filter((event) => normalizePubkey(event.pubkey) === pk && Number(event.kind) === KIND_FOLLOW)
          .sort((a, b) => Number(b.created_at) - Number(a.created_at))[0] || null;
        const following = followEvent ? followPubkeys(followEvent) : [];
        const followers = uniqueNonEmpty(
          deduped
            .filter((event) => Number(event.kind) === KIND_FOLLOW)
            .filter((event) => (event.tags || []).some((tag) => Array.isArray(tag) && tag[0] === "p" && normalizePubkey(tag[1]) === pk))
            .map((event) => normalizePubkey(event.pubkey))
            .filter((author) => author && author !== pk),
        );
        return {
          pubkey: pk,
          following,
          followers,
          followEvent,
          relayHints: followEvent ? followRelayHints(followEvent) : new Map(),
        };
      },
      cacheMode: "cache-first",
      staleTime: 30_000,
    }),
    async () => ({
      pubkey: pk,
      following: [],
      followers: [],
      followEvent: null,
      relayHints: new Map(),
    }),
  );
}

export async function fetchMentions(pubkey, { rootID = "" } = {}) {
  const pk = normalizePubkey(pubkey);
  const followEvent = await fetchReplaceable(pk, KIND_FOLLOW);
  const contacts = followPubkeys(followEvent);
  const relayHints = followRelayHints(followEvent);
  const profiles = await fetchProfiles(contacts.slice(0, 32));
  const candidates = [];
  const seen = new Set();
  for (const contact of contacts) {
    if (seen.has(contact)) continue;
    seen.add(contact);
    const profile = profiles[contact] || {};
    const relays = relayHints.has(contact) ? [relayHints.get(contact)] : [];
    let nref = nip19.npubEncode(contact);
    try {
      if (relays.length) nref = nip19.nprofileEncode({ pubkey: contact, relays });
    } catch {
      // keep npub
    }
    candidates.push({
      pubkey: contact,
      name: profile.display_name || profile.name || contact.slice(0, 12),
      npub: nip19.npubEncode(contact),
      nref,
      relays,
      source: "contact",
    });
  }
  if (rootID) {
    const threadAuthors = await distinctAuthorsUnderRoot(rootID);
    const threadProfiles = await fetchProfiles(threadAuthors.slice(0, 32));
    for (const author of threadAuthors) {
      if (seen.has(author)) continue;
      seen.add(author);
      const profile = threadProfiles[author] || {};
      candidates.push({
        pubkey: author,
        name: profile.display_name || profile.name || author.slice(0, 12),
        npub: nip19.npubEncode(author),
        nref: nip19.npubEncode(author),
        relays: [],
        source: "thread",
      });
    }
  }
  candidates.sort((a, b) => String(a.name).localeCompare(String(b.name)));
  return { pubkey: pk, root_id: rootID, candidates };
}

async function distinctAuthorsUnderRoot(rootID) {
  const root = canonicalHex64(rootID);
  if (!root) return [];
  let events = await eventsByTag("e", root, { limit: 250 });
  if (!events.length) {
    const relays = readRelaysForViewer();
    events = await relayFetch(relays, [{ kinds: [KIND_NOTE, 1111], "#e": [root], limit: 250 }]);
    await putEvents(events);
  }
  const authors = new Set();
  for (const event of events) {
    const pk = normalizePubkey(event.pubkey);
    if (pk) authors.add(pk);
  }
  const rootEvent = await getEvent(root);
  if (rootEvent?.pubkey) authors.add(normalizePubkey(rootEvent.pubkey));
  return [...authors];
}

export async function fetchReplyCounts(noteIDs) {
  const ids = (noteIDs || []).map(canonicalHex64).filter(Boolean);
  if (!ids.length) return {};
  const local = await localReplyCounts(ids);
  return withServerFallback(
    async () => {
      const relays = metadataFetchRelays();
      const out = Object.fromEntries(ids.map((id) => [id, 0]));
      const localRows = await Promise.all(
        ids.map(async (id) => ({ id, events: await eventsByTag("e", id, { kind: KIND_NOTE, limit: 200 }) })),
      );
      const missingIDs = [];
      localRows.forEach(({ id, events }) => {
        out[id] = events.length;
        if (!events.length) missingIDs.push(id);
      });
      if (missingIDs.length) {
        const fetched = await fetchETagEventsBatched(relays, missingIDs, {
          kinds: [KIND_NOTE, 1111],
          perEventLimit: 200,
        });
        await putEvents(fetched);
        missingIDs.forEach((id) => {
          out[id] = eventsMatchingReferencedID(fetched, id).length;
        });
      }
      return mergeReplyCountMaps(out, local);
    },
    async () => {
      const requestURL = new URL("/api/reply-counts", window.location.origin);
      ids.forEach((id) => requestURL.searchParams.append("id", id));
      const response = await fetchWithSession(requestURL.toString());
      if (!response.ok) return mergeReplyCountMaps({}, local);
      return mergeReplyCountMaps(await response.json(), local);
    },
  );
}

export async function fetchReactionStats(noteIDs, viewerPubkey = "") {
  const ids = (noteIDs || []).map(canonicalHex64).filter(Boolean);
  if (!ids.length) return {};
  const viewer = normalizePubkey(viewerPubkey);
  const local = await localReactionTotals(ids);
  return withServerFallback(
    async () => {
      const relays = metadataFetchRelays();
      const localRows = await Promise.all(
        ids.map(async (id) => ({ id, reactions: await eventsByTag("e", id, { kind: KIND_REACTION, limit: 500 }) })),
      );
      const rows = [];
      const missingIDs = [];
      localRows.forEach(({ id, reactions }) => {
        if (reactions.length) {
          rows.push({ id, reactions });
        } else {
          missingIDs.push(id);
        }
      });
      if (missingIDs.length) {
        const fetched = await fetchETagEventsBatched(relays, missingIDs, {
          kinds: [KIND_REACTION],
          perEventLimit: 500,
        });
        await putEvents(fetched);
        missingIDs.forEach((id) => {
          rows.push({ id, reactions: eventsMatchingReferencedID(fetched, id) });
        });
      }
      const summarized = rows.map(({ id, reactions }) => {
        let total = 0;
        let viewerVote = "";
        const byViewer = reactions
          .filter((event) => normalizePubkey(event.pubkey) === viewer)
          .sort((a, b) => Number(b.created_at) - Number(a.created_at));
        if (byViewer[0]) {
          viewerVote = String(byViewer[0].content || "").trim() === "-" ? "-" : "+";
        }
        for (const event of reactions) {
          const content = String(event.content || "").trim();
          if (content === "+" || content === "") total += 1;
          else if (content === "-") total -= 1;
        }
        return { id, total, viewer: viewerVote };
      });
      const out = Object.fromEntries(summarized.map((row) => [row.id, { total: row.total, viewer: row.viewer }]));
      return mergeReactionStatsMaps(out, local);
    },
    async () => {
      const requestURL = new URL("/api/reaction-stats", window.location.origin);
      ids.forEach((id) => requestURL.searchParams.append("id", id));
      const response = await fetchWithSession(requestURL.toString());
      if (!response.ok) return mergeReactionStatsMaps({}, local);
      return mergeReactionStatsMaps(await response.json(), local);
    },
  );
}

export async function fetchZapReceipts(noteIDs, { relays = [] } = {}) {
  const ids = [...new Set((noteIDs || []).map(canonicalHex64).filter(Boolean))];
  if (!ids.length) return [];
  const relayList = relays.length ? relays : readRelaysForViewer();
  const filters = [];
  for (let index = 0; index < ids.length; index += 20) {
    filters.push({
      kinds: [KIND_ZAP_RECEIPT],
      "#e": ids.slice(index, index + 20),
      limit: 500,
    });
  }
  const events = await relayFetch(relayList, filters);
  await putEvents(events);
  return events;
}

export async function fetchPollVotes(pollID, { relays = [] } = {}) {
  const id = canonicalHex64(pollID);
  if (!id) return [];
  const relayList = relays.length ? relays : readRelaysForViewer();
  const events = await relayFetch(relayList, [{
    kinds: [KIND_POLL_RESPONSE],
    "#e": [id],
    limit: 500,
  }]);
  await putEvents(events);
  return events;
}

export async function fetchReactionsForNote(noteID) {
  const id = canonicalHex64(noteID);
  if (!id) return { reactions: [], truncated: false, limit: 0 };
  return withServerFallback(
    async () => {
      const relays = readRelaysForViewer();
      let reactions = await eventsByTag("e", id, { kind: KIND_REACTION, limit: 500 });
      if (!reactions.length) {
        reactions = await relayFetch(relays, [{ kinds: [KIND_REACTION], "#e": [id], limit: 500 }]);
        await putEvents(reactions);
      }
      const pubkeys = reactions.map((event) => normalizePubkey(event.pubkey)).filter(Boolean);
      const profiles = await fetchProfiles([...new Set(pubkeys)]);
      const rows = reactions.map((event) => {
        const pk = normalizePubkey(event.pubkey);
        const profile = profiles[pk] || {};
        const vote = String(event.content || "").trim() === "-" ? "down" : "up";
        return {
          pubkey: pk,
          display_name: profile.display_name || profile.name || pk.slice(0, 12),
          vote,
        };
      });
      return { reactions: rows, truncated: false, limit: rows.length };
    },
    async () => {
      const response = await fetchWithSession(`/api/reactions?note_id=${encodeURIComponent(id)}`);
      if (!response.ok) throw new Error("reactions request failed");
      return response.json();
    },
  );
}

async function fetchServerOutboxPlans(authors = []) {
  const now = Date.now();
  const keys = [...new Set((authors || []).map(normalizePubkey).filter(Boolean))];
  const out = new Map();
  const missing = [];
  for (const pk of keys) {
    const cached = outboxPlanCache.get(pk);
    if (cached && now - Number(cached.savedAt || 0) < OUTBOX_PLAN_TTL_MS) {
      out.set(pk, cached.relays);
    } else {
      missing.push(pk);
    }
  }
  if (!missing.length) return out;
  try {
    const url = new URL("/api/outbox-plan", window.location.origin);
    url.searchParams.set("author", missing.join(","));
    const response = await fetchWithSession(url.pathname + url.search, {
      headers: { Accept: "application/json" },
    });
    if (response.ok) {
      const payload = await response.json();
      for (const group of Array.isArray(payload?.groups) ? payload.groups : []) {
        const relays = normalizeRelayList(group?.relays || []);
        for (const author of Array.isArray(group?.authors) ? group.authors : []) {
          const pk = normalizePubkey(author);
          if (!pk) continue;
          outboxPlanCache.set(pk, { relays, savedAt: now });
          out.set(pk, relays);
        }
      }
    }
  } catch {
    // Fall through to direct kind-10002 lookup for unresolved authors.
  }
  for (const pk of missing) {
    if (!out.has(pk)) outboxPlanCache.set(pk, { relays: [], savedAt: now });
  }
  return out;
}

async function authorOutboxRelays(pubkey) {
  const pk = normalizePubkey(pubkey);
  if (!pk) return [];
  const relayListEvent = await fetchReplaceable(pk, KIND_RELAY_LIST, {
    relays: readRelaysForViewer(),
    includeViewerRelays: true,
  }).catch(() => null);
  return authorWriteRelaysFromKind10002(relayListEvent);
}

export async function fetchEventsByIDs(
  ids,
  { relayHintsByID = {}, authorHintsByID = {}, relayFetchImpl = relayFetch } = {},
) {
  const want = [];
  const seen = new Set();
  const mergedHints = { ...relayHintsByID };
  const mergedAuthors = { ...authorHintsByID };

  for (const raw of ids || []) {
    const resolved = resolveEventID(raw);
    const id = resolved?.eventID || canonicalHex64(raw);
    if (!isCanonicalEventID(id) || seen.has(id)) continue;
    seen.add(id);
    want.push(id);
    if (resolved?.relays?.length) {
      mergedHints[id] = uniqueNonEmpty([...(mergedHints[id] || []), ...resolved.relays]);
    }
    if (resolved?.author) {
      mergedAuthors[id] = resolved.author;
    }
  }
  if (!want.length) return [];

  const cached = await getEvents(want).catch((error) => {
    if (isClientDBUnavailableError(error)) return new Map();
    throw error;
  });
  let missing = want.filter((id) => !cached.has(id));

  if (missing.length) {
    const hinted = [];
    let stillMissing = [];
    for (const id of missing) {
      const hints = normalizeRelayList(mergedHints[id] || []);
      if (hints.length) hinted.push({ id, hints });
      else stillMissing.push(id);
    }
    const hintGroups = groupedRelayHintBatches(hinted.map(({ id }) => id), Object.fromEntries(hinted.map(({ id, hints }) => [id, hints])));
    for (const group of hintGroups) {
      const fetched = await relayFetchImpl(group.hints, [{ ids: group.ids, limit: group.ids.length }]);
      if (fetched.length) {
        await putEvents(fetched).catch((error) => {
          if (!isClientDBUnavailableError(error)) throw error;
        });
        for (const event of fetched) cached.set(event.id, event);
      }
      for (const id of group.ids) {
        if (!fetched.some((event) => event.id === id)) {
          stillMissing.push(id);
        }
      }
    }
    if (stillMissing.length) {
      const authorRelayGroups = new Map();
      const defaultMissing = [];
      const authorPlans = await fetchServerOutboxPlans(
        stillMissing.map((id) => mergedAuthors[id]).filter(Boolean),
      );
      for (const id of stillMissing) {
        const author = normalizePubkey(mergedAuthors[id]);
        if (!author) {
          defaultMissing.push(id);
          continue;
        }
        let relays = authorPlans.get(author) || [];
        if (!relays.length) relays = await authorOutboxRelays(author).catch(() => []);
        if (!relays.length) {
          defaultMissing.push(id);
          continue;
        }
        const key = relays.join("|");
        if (!authorRelayGroups.has(key)) authorRelayGroups.set(key, { ids: [], relays });
        authorRelayGroups.get(key).ids.push(id);
      }
      for (const group of authorRelayGroups.values()) {
        const fetched = await relayFetchImpl(group.relays, [{ ids: group.ids, limit: group.ids.length }]);
        await putEvents(fetched).catch((error) => {
          if (!isClientDBUnavailableError(error)) throw error;
        });
        for (const event of fetched) cached.set(event.id, event);
        for (const id of group.ids) {
          if (!fetched.some((event) => event.id === id)) {
            defaultMissing.push(id);
          }
        }
      }
      stillMissing = defaultMissing;
    }
    if (stillMissing.length) {
      const relays = readRelaysForViewer();
      const fetched = await relayFetchImpl(relays, [{ ids: stillMissing, limit: stillMissing.length }]);
      await putEvents(fetched).catch((error) => {
        if (!isClientDBUnavailableError(error)) throw error;
      });
      for (const event of fetched) cached.set(event.id, event);
    }
  }
  return want.map((id) => cached.get(id)).filter(Boolean);
}

export const TRENDING_HOT_SEARCH = "sort:hot protocol:nostr";

export async function nip50Search(
  query,
  { limit = DEFAULT_QUERY_LIMIT, kinds = [KIND_NOTE], relayFetchImpl = relayFetch } = {},
) {
  const search = String(query || "").trim();
  if (!search) return [];
  const relays = normalizeRelayList(INDEXER_NIP50_RELAYS);
  const timelineKinds = Array.isArray(kinds) && kinds.length ? kinds : [KIND_NOTE];
  const events = await relayFetchImpl(relays, [{ search, kinds: timelineKinds, limit }]);
  await putEvents(events);
  return dedupeEventsByID(events);
}

/** NIP-50 hot trending fetch (mirrors iOS fetchRelayTrendingEvents filter). */
export async function nip50TrendingSearch({
  since,
  until,
  limit = DEFAULT_QUERY_LIMIT,
  kinds = [KIND_NOTE, KIND_LONG_FORM],
} = {}) {
  const filter = {
    search: TRENDING_HOT_SEARCH,
    kinds: Array.isArray(kinds) && kinds.length ? kinds : [KIND_NOTE, KIND_LONG_FORM],
    limit: Math.min(MAX_QUERY_LIMIT, Math.max(1, Number(limit) || DEFAULT_QUERY_LIMIT)),
  };
  if (since) filter.since = since;
  if (until) filter.until = until;
  const events = await relayFetch(TRENDING_SEARCH_RELAYS, [filter]);
  await putEvents(events);
  return dedupeEventsByID(events);
}

export async function fetchNotesByAuthors(
  authors,
  { limit = DEFAULT_QUERY_LIMIT, since, until, kinds = [KIND_NOTE], relays = [], includeViewerRelays = true } = {},
) {
  const pubkeys = (authors || []).map(normalizePubkey).filter(Boolean);
  if (!pubkeys.length) return [];
  const relayList = readRelaysForFetch(relays, { includeViewerRelays });
  if (!relayList.length) return eventsByAuthors(pubkeys, { kinds, limit, since, until }).catch(() => []);
  const timelineKinds = Array.isArray(kinds) && kinds.length ? kinds : [KIND_NOTE];
  const perAuthorLimit = Math.min(MAX_QUERY_LIMIT, Math.max(1, Number(limit) || DEFAULT_QUERY_LIMIT));
  const cached = await eventsByAuthors(pubkeys, {
    kinds: timelineKinds,
    limit: perAuthorLimit,
    since,
    until,
  }).catch(() => []);
  const batches = chunkAuthors(pubkeys);
  const fetched = await Promise.all(
    batches.map(async (batch) => {
      const filter = { kinds: timelineKinds, authors: batch, limit: perAuthorLimit };
      if (since) filter.since = since;
      if (until) filter.until = until;
      return relayFetch(relayList, [filter]);
    }),
  );
  const events = dedupeEventsByID(fetched.flat());
  await putEvents(events);
  return sortEventsNewestFirst(dedupeEventsByID([...cached, ...events])).slice(0, perAuthorLimit);
}

export async function fetchMentionsForViewer(pubkey, { limit = 50 } = {}) {
  const pk = normalizePubkey(pubkey);
  if (!pk) return [];
  const relays = readRelaysForViewer();
  const events = await relayFetch(relays, [{ kinds: [KIND_NOTE, KIND_REACTION], "#p": [pk], limit }]);
  await putEvents(events);
  return sortEventsNewestFirst(dedupeEventsByID(events));
}
