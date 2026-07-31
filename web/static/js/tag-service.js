import { relayFetch } from "./relay-pool.js";
import { readRelaysForViewer } from "./publish-plan.js";
import { putEvents, hashtagEvents } from "./event-store.js";
import { fetchMuteList } from "./relay-reads.js";
import { expandWebOfTrust } from "./wot-service.js";
import { KIND_LONG_FORM, KIND_NOTE, KIND_REPOST, MAX_QUERY_LIMIT } from "./nostr-kinds.js";
import { dedupeEventsByID, normalizePubkey } from "./relay-utils.js";
import {
  authorMembershipSet,
  chunkAuthors,
  sortEventsNewestFirst,
} from "./feed-query.js";
import { feedPaginationCursorFromDatasets } from "./feed-pagination.js";
import {
  getEffectiveLoggedOutWebOfTrustSeed,
  getWebOfTrustDepthPref,
  getWebOfTrustEnabledPref,
  getWebOfTrustSeedPref,
} from "./sort-prefs.js";
import { feedFetchModeFromPrefs } from "./feed-wot.js";
import { eventHasHashtag, normalizeHashtag, tagScopeFromURL } from "./hashtag-utils.js";
import { fetchWithSession } from "./session.js";

const HASHTAG_KINDS = [KIND_NOTE, KIND_REPOST, KIND_LONG_FORM];
const HASHTAG_OVERFETCH = 120;

function hashtagRelayTagValues(tag) {
  const normalized = normalizeHashtag(tag);
  if (!normalized) return [];
  const seen = new Set();
  const out = [];
  for (const value of [normalized, normalized.toLowerCase()]) {
    const key = value.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(value);
  }
  return out;
}

async function resolveTagAuthors(viewerPubkey, scope) {
  if (scope === "all") return null;
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

async function filterHashtagResults(events, viewerPubkey, authors) {
  const muteList = viewerPubkey ? await fetchMuteList(viewerPubkey) : null;
  const muted = new Set((muteList?.muted_pubkeys || []).map(normalizePubkey).filter(Boolean));
  const membership = authors ? authorMembershipSet(authors) : null;
  return (events || []).filter((event) => {
    const pk = normalizePubkey(event.pubkey);
    if (muted.has(pk)) return false;
    if (membership && !membership.has(pk)) return false;
    return true;
  });
}

/** Relay fetch for hashtag timelines (mirrors iOS refreshFeedFromRelays with #t filter). */
export async function fetchHashtagNotesFromRelays({
  tag,
  limit = 50,
  since,
  until,
  authors = null,
} = {}) {
  const normalized = normalizeHashtag(tag);
  const tagValues = hashtagRelayTagValues(normalized);
  if (!tagValues.length) return [];

  const relays = readRelaysForViewer();
  const fetchLimit = Math.min(MAX_QUERY_LIMIT, Math.max(limit, HASHTAG_OVERFETCH));
  const filter = {
    kinds: HASHTAG_KINDS,
    "#t": tagValues,
    limit: fetchLimit,
  };
  if (since) filter.since = since;
  if (until) filter.until = until;

  let events = [];
  const queryAuthors = authors?.length ? authors.map(normalizePubkey).filter(Boolean) : [];
  if (queryAuthors.length) {
    const batches = chunkAuthors(queryAuthors);
    const fetched = await Promise.all(
      batches.map(async (batch) => {
        const authorFilter = { ...filter, authors: batch };
        return relayFetch(relays, [authorFilter]);
      }),
    );
    events = dedupeEventsByID(fetched.flat());
  } else {
    events = await relayFetch(relays, [filter]);
  }

  events = events.filter((event) => eventHasHashtag(event, normalized));
  await putEvents(events);
  return sortEventsNewestFirst(events).slice(0, limit);
}

export async function cachedHashtagNotes({
  tag,
  viewerPubkey = "",
  scope = "network",
  limit = 50,
  until,
  untilID,
} = {}) {
  const normalized = normalizeHashtag(tag);
  if (!normalized) return [];
  const authors = await resolveTagAuthors(viewerPubkey, scope);
  if (authors && !authors.length) return [];
  const { beforeCreatedAt, beforeID } = feedPaginationCursorFromDatasets({ until, untilID });
  return hashtagEvents({
    tag: normalized,
    limit,
    authors,
    beforeCreatedAt,
    beforeID,
  });
}

async function fetchServerHashtagPage({
  tag,
  scope,
  limit,
  until,
  untilID,
} = {}) {
  try {
    const url = new URL("/api/tag-notes", window.location.origin);
    url.searchParams.set("tag", tag);
    url.searchParams.set("scope", scope || "network");
    url.searchParams.set("limit", String(limit || 50));
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

export async function fetchHashtagPage({
  tag,
  viewerPubkey = "",
  scope,
  limit = 50,
  until,
  untilID,
  forceFetch = false,
  urlLike = window.location.href,
} = {}) {
  const normalized = normalizeHashtag(tag);
  if (!normalized) return [];
  const effectiveScope = scope || tagScopeFromURL(urlLike);
  const authors = await resolveTagAuthors(viewerPubkey, effectiveScope);
  if (authors && !authors.length) return [];

  const { beforeCreatedAt, beforeID } = feedPaginationCursorFromDatasets({ until, untilID });
  let events = [];
  if (!forceFetch) {
    events = await fetchServerHashtagPage({
      tag: normalized,
      scope: effectiveScope,
      limit,
      until,
      untilID,
    });
    if (events.length >= Math.min(limit, 10)) {
      return filterHashtagResults(events.slice(0, limit), viewerPubkey, authors);
    }
    events = await cachedHashtagNotes({
      tag: normalized,
      viewerPubkey,
      scope: effectiveScope,
      limit,
      until,
      untilID,
    });
  }

  if (forceFetch || events.length < limit) {
    await fetchHashtagNotesFromRelays({
      tag: normalized,
      limit: Math.max(limit, HASHTAG_OVERFETCH),
      until: beforeCreatedAt,
      authors,
    });
    events = await cachedHashtagNotes({
      tag: normalized,
      viewerPubkey,
      scope: effectiveScope,
      limit,
      until,
      untilID,
    });
  }

  return filterHashtagResults(events, viewerPubkey, authors);
}

export function tagScopeToggleURLs(tag, urlLike = window.location.href) {
  const normalized = normalizeHashtag(tag);
  if (!normalized) return { allURL: "", networkURL: "" };
  const base = new URL(`/tag/${encodeURIComponent(normalized)}`, window.location.origin);
  const all = new URL(base);
  all.searchParams.set("scope", "all");
  const network = new URL(base);
  network.searchParams.set("scope", "network");
  const current = tagScopeFromURL(urlLike);
  return {
    scope: current,
    allURL: all.pathname + all.search,
    networkURL: network.pathname + network.search,
  };
}
