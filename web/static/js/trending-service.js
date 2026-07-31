import { putEvents, localTrendingEvents } from "./event-store.js";
import { feedFetchModeFromPrefs } from "./feed-wot.js";
import { feedPaginationCursorFromDatasets } from "./feed-pagination.js";
import { expandWebOfTrust } from "./wot-service.js";
import { fetchMuteList, nip50TrendingSearch } from "./relay-reads.js";
import { KIND_LONG_FORM, KIND_NOTE } from "./nostr-kinds.js";
import { normalizePubkey } from "./relay-utils.js";
import {
  getEffectiveLoggedOutWebOfTrustSeed,
  getWebOfTrustDepthPref,
  getWebOfTrustEnabledPref,
  getWebOfTrustSeedPref,
} from "./sort-prefs.js";
import { powerLimitedCount } from "./power-mode.js";

export const TREND24H = "trend24h";
export const TREND7D = "trend7d";

const TRENDING_OVERFETCH_MULTIPLIER = 4;
const TRENDING_MAX_OVERFETCH_LIMIT = 120;
const TRENDING_MAX_PAGINATION_BATCHES = 4;
const SECONDS_24H = 86_400;
const SECONDS_7D = 604_800;

export function isTrendingSort(sort) {
  return sort === TREND24H || sort === TREND7D;
}

export function trendingWindowStart(sort, nowSec = Math.floor(Date.now() / 1000)) {
  const now = Number(nowSec) || Math.floor(Date.now() / 1000);
  if (sort === TREND7D) return now - SECONDS_7D;
  if (sort === TREND24H) return now - SECONDS_24H;
  return null;
}

export function trendingTimeframeFromSort(sort) {
  return sort === TREND7D ? "1w" : "24h";
}

export function trendingSortFromTimeframe(tf) {
  return String(tf || "").trim() === "1w" ? TREND7D : TREND24H;
}

function trendingKinds(kindFilter) {
  if (kindFilter === KIND_LONG_FORM) return [KIND_LONG_FORM];
  if (kindFilter === KIND_NOTE) return [KIND_NOTE];
  return [KIND_NOTE, KIND_LONG_FORM];
}

async function resolveTrendingAuthors(viewerPubkey) {
  const mode = feedFetchModeFromPrefs(viewerPubkey, {
    wotEnabled: getWebOfTrustEnabledPref(),
    seedPref: getWebOfTrustSeedPref(),
    loggedOutDefaultSeed: getEffectiveLoggedOutWebOfTrustSeed(),
    depth: getWebOfTrustDepthPref(),
  });
  if (mode.kind === "firehose") return null;
  if (mode.kind === "empty") return [];
  const authors = await expandWebOfTrust(mode.seed, mode.depth);
  return authors.length ? authors : [];
}

async function resolveMutedPubkeys(viewerPubkey) {
  const pk = normalizePubkey(viewerPubkey);
  if (!pk) return new Set();
  try {
    const list = await fetchMuteList(pk);
    return new Set((list?.muted_pubkeys || []).map(normalizePubkey).filter(Boolean));
  } catch {
    return new Set();
  }
}

function isMutedAuthor(event, mutedSet) {
  const pk = normalizePubkey(event?.pubkey);
  return pk && mutedSet.has(pk);
}

function filterTrendingEvent(event, { allowedAuthors, mutedSet, since, kindFilter }) {
  const createdAt = Number(event?.created_at) || 0;
  if (since && createdAt < since) return false;
  if (kindFilter != null && Number(event.kind) !== Number(kindFilter)) return false;
  const pk = normalizePubkey(event?.pubkey);
  if (allowedAuthors && !allowedAuthors.has(pk)) return false;
  if (isMutedAuthor(event, mutedSet)) return false;
  return true;
}

/**
 * Fetch hot-ranked events from NIP-50 relays (mirrors iOS fetchRelayTrendingEvents).
 * Preserves relay hot order.
 */
export async function fetchRelayTrendingEvents({
  sort,
  authors = null,
  limit = 50,
  beforeCreatedAt,
  beforeID,
  kindFilter = null,
  viewerPubkey = "",
} = {}) {
  const since = trendingWindowStart(sort);
  if (since == null) return [];

  const allowedAuthors = authors ? new Set(authors.map(normalizePubkey).filter(Boolean)) : null;
  const mutedSet = await resolveMutedPubkeys(viewerPubkey);
  const kinds = trendingKinds(kindFilter);
  const overfetchLimit = Math.min(
    Math.max(limit * powerLimitedCount(TRENDING_OVERFETCH_MULTIPLIER, 2), limit),
    powerLimitedCount(TRENDING_MAX_OVERFETCH_LIMIT, 50),
  );

  const collected = [];
  const seenIDs = new Set();
  let until = beforeCreatedAt != null && beforeCreatedAt > 0 ? beforeCreatedAt : undefined;
  let batches = 0;

  while (collected.length < limit && batches < powerLimitedCount(TRENDING_MAX_PAGINATION_BATCHES, 1)) {
    const fetched = await nip50TrendingSearch({
      since,
      until,
      limit: overfetchLimit,
      kinds,
    });
    batches += 1;
    if (!fetched.length) break;

    for (const event of fetched) {
      const id = String(event.id || "").toLowerCase();
      if (!id || seenIDs.has(id)) continue;
      seenIDs.add(id);
      if (!filterTrendingEvent(event, { allowedAuthors, mutedSet, since, kindFilter })) continue;
      if (beforeCreatedAt != null && beforeCreatedAt > 0) {
        const createdAt = Number(event.created_at) || 0;
        if (createdAt > beforeCreatedAt) continue;
        if (createdAt === beforeCreatedAt && beforeID && id >= String(beforeID).toLowerCase()) continue;
      }
      collected.push(event);
      if (collected.length >= limit) break;
    }

    if (collected.length >= limit) break;

    const oldest = [...fetched].sort((a, b) => {
      const delta = Number(a.created_at) - Number(b.created_at);
      if (delta !== 0) return delta;
      return String(a.id).localeCompare(String(b.id));
    })[0];
    if (!oldest) break;
    until = Number(oldest.created_at) || undefined;
    if (fetched.length < overfetchLimit) break;
  }

  return collected;
}

function mergeTrendingResults(relayEvents, localEvents, limit) {
  const seen = new Set();
  const merged = [];
  for (const event of relayEvents || []) {
    const id = String(event?.id || "").toLowerCase();
    if (!id || seen.has(id)) continue;
    seen.add(id);
    merged.push(event);
    if (merged.length >= limit) return merged;
  }
  for (const event of localEvents || []) {
    const id = String(event?.id || "").toLowerCase();
    if (!id || seen.has(id)) continue;
    seen.add(id);
    merged.push(event);
    if (merged.length >= limit) return merged;
  }
  return merged;
}

export { mergeTrendingResults };

export async function filterRelayTrendingBatch(events, {
  allowedAuthors = null,
  mutedPubkeys = [],
  since,
  kindFilter = null,
} = {}) {
  const allowed = allowedAuthors ? new Set(allowedAuthors.map(normalizePubkey).filter(Boolean)) : null;
  const mutedSet = new Set((mutedPubkeys || []).map(normalizePubkey).filter(Boolean));
  const out = [];
  for (const event of events || []) {
    if (!filterTrendingEvent(event, { allowedAuthors: allowed, mutedSet, since, kindFilter })) continue;
    out.push(event);
  }
  return out;
}

/**
 * Cache-first trending feed (mirrors iOS loadTrendingFeed + loadHomeFeed trending path).
 */
export async function loadTrendingFeed({
  sort,
  viewerPubkey = "",
  limit = 50,
  until,
  untilID,
  forceFetch = false,
  kindFilter = null,
} = {}) {
  if (!isTrendingSort(sort)) return [];

  const since = trendingWindowStart(sort);
  const authors = await resolveTrendingAuthors(viewerPubkey);
  if (authors && !authors.length) return [];

  const { beforeCreatedAt, beforeID } = feedPaginationCursorFromDatasets({ until, untilID });
  const kinds = trendingKinds(kindFilter);

  let local = [];
  if (!forceFetch) {
    local = await localTrendingEvents({
      since,
      authors,
      kinds,
      limit,
      beforeCreatedAt,
      beforeID,
      kindFilter,
    });
  }

  let relay = [];
  if (forceFetch || local.length < limit) {
    relay = await fetchRelayTrendingEvents({
      sort,
      authors,
      limit,
      beforeCreatedAt,
      beforeID,
      kindFilter,
      viewerPubkey,
    });
    if (relay.length) await putEvents(relay);
  }

  if (!forceFetch && local.length >= limit) {
    return local.slice(0, limit);
  }

  if (!forceFetch && local.length) {
    return mergeTrendingResults(relay, local, limit);
  }

  if (relay.length) return relay.slice(0, limit);

  if (!forceFetch) {
    local = await localTrendingEvents({
      since,
      authors,
      kinds,
      limit,
      beforeCreatedAt,
      beforeID,
      kindFilter,
    });
  }
  return local.slice(0, limit);
}

export async function cachedTrendingFeed(args) {
  return loadTrendingFeed({ ...args, forceFetch: false });
}

export async function fetchTrendingFeed(args) {
  return loadTrendingFeed({ ...args, forceFetch: false });
}
