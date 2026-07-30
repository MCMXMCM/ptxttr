import { normalizePubkey } from "./relay-utils.js";

export const FEED_AUTHOR_QUERY_BATCH_SIZE = 64;
export const FEED_AUTHOR_QUERY_MAX = 240;
export const FEED_GLOBAL_FETCH_LIMIT = 200;

export function chunkAuthors(authors, batchSize = FEED_AUTHOR_QUERY_BATCH_SIZE) {
  const pubkeys = (authors || []).map(normalizePubkey).filter(Boolean);
  if (!pubkeys.length) return [];
  const batches = [];
  for (let index = 0; index < pubkeys.length; index += batchSize) {
    batches.push(pubkeys.slice(index, index + batchSize));
  }
  return batches;
}

export function authorMembershipSet(authors) {
  const membership = new Set();
  for (const author of authors || []) {
    const pk = normalizePubkey(author);
    if (pk) membership.add(pk);
  }
  return membership;
}

export function filterEventsByAuthorMembership(events, membership) {
  if (!membership?.size) return [];
  return (events || []).filter((event) => membership.has(normalizePubkey(event?.pubkey)));
}

export function sortEventsNewestFirst(events) {
  return [...(events || [])].sort((a, b) => {
    const delta = Number(b?.created_at || 0) - Number(a?.created_at || 0);
    if (delta !== 0) return delta;
    return String(b?.id || "").localeCompare(String(a?.id || ""));
  });
}

export function sortEventsOldestFirst(events) {
  return [...(events || [])].sort((a, b) => {
    const delta = Number(a?.created_at || 0) - Number(b?.created_at || 0);
    if (delta !== 0) return delta;
    return String(a?.id || "").localeCompare(String(b?.id || ""));
  });
}

export function clampQueryAuthors(authors, max = FEED_AUTHOR_QUERY_MAX) {
  const pubkeys = (authors || []).map(normalizePubkey).filter(Boolean);
  return pubkeys.slice(0, max);
}
