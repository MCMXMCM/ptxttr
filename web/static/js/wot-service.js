import { relayFetch } from "./relay-pool.js";
import { readRelaysForViewer } from "./publish-plan.js";
import { KIND_FOLLOW } from "./nostr-kinds.js";
import { dedupeEventsByID, followPubkeys, normalizePubkey } from "./relay-utils.js";
import { latestReplaceable, putEvents } from "./event-store.js";
import { fetchWithSession } from "./session.js";
import { DEFAULT_LOGGED_OUT_WOT_SEED_NPUB } from "./viewer-defaults.js";

const WOT_CACHE_KEY = "ptxt_wot_graph_cache_v2";
const DEFAULT_MAX_AUTHORS = 240;
const WOT_CACHE_MAX_AGE_MS = 30 * 60 * 1000;
const WOT_STALE_CACHE_MAX_AGE_MS = 24 * 60 * 60 * 1000;
const WOT_SERVER_RETRY_MS = 60 * 1000;

const DEFAULT_LOGGED_OUT_WOT_SEED_HEX = normalizePubkey(DEFAULT_LOGGED_OUT_WOT_SEED_NPUB);
const serverRequests = new Map();
let serverBackoffUntil = 0;

function readDiskCache(seed, depth, { maxAgeMs = WOT_CACHE_MAX_AGE_MS } = {}) {
  try {
    const raw = localStorage.getItem(WOT_CACHE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    if (parsed.seed !== seed || parsed.depth !== depth) return null;
    if (Date.now() - Number(parsed.saved_at || 0) > maxAgeMs) return null;
    return new Set(Array.isArray(parsed.authors) ? parsed.authors : []);
  } catch {
    return null;
  }
}

function writeDiskCache(seed, depth, authors) {
  try {
    localStorage.setItem(
      WOT_CACHE_KEY,
      JSON.stringify({
        seed,
        depth,
        saved_at: Date.now(),
        authors: [...authors],
      }),
    );
  } catch {
    // ignore quota
  }
}

async function fetchServerResolvedAuthors(seed, depth, maxAuthors) {
  if (seed !== DEFAULT_LOGGED_OUT_WOT_SEED_HEX) return null;
  const stale = readDiskCache(seed, depth, { maxAgeMs: WOT_STALE_CACHE_MAX_AGE_MS });
  if (Date.now() < serverBackoffUntil) return stale;
  const key = `${seed}:${depth}:${maxAuthors || DEFAULT_MAX_AUTHORS}`;
  if (serverRequests.has(key)) return serverRequests.get(key);
  const request = fetchServerResolvedAuthorsOnce(seed, depth, maxAuthors, stale).finally(() => {
    serverRequests.delete(key);
  });
  serverRequests.set(key, request);
  return request;
}

async function fetchServerResolvedAuthorsOnce(seed, depth, maxAuthors, stale) {
  try {
    const url = new URL("/api/wot-authors", window.location.origin);
    url.searchParams.set("seed", seed);
    url.searchParams.set("depth", String(depth));
    url.searchParams.set("limit", String(maxAuthors || DEFAULT_MAX_AUTHORS));
    const response = await fetchWithSession(url.pathname + url.search, {
      headers: { Accept: "application/json" },
    });
    if (response.status === 429) {
      const retryAfter = Number.parseInt(response.headers?.get?.("Retry-After") || "", 10);
      const retryMs = Number.isFinite(retryAfter) && retryAfter > 0 ? retryAfter * 1000 : WOT_SERVER_RETRY_MS;
      serverBackoffUntil = Date.now() + Math.max(WOT_SERVER_RETRY_MS, retryMs);
      return stale;
    }
    if (!response.ok) return null;
    const payload = await response.json();
    const authors = Array.isArray(payload?.authors)
      ? payload.authors.map(normalizePubkey).filter(Boolean)
      : [];
    return authors.length ? new Set(authors) : null;
  } catch {
    return null;
  }
}

async function followGraphFor(pubkey) {
  const pk = normalizePubkey(pubkey);
  if (!pk) return [];
  const cached = await latestReplaceable(pk, KIND_FOLLOW);
  if (cached) return followPubkeys(cached);
  const relays = readRelaysForViewer();
  const events = await relayFetch(relays, [{ authors: [pk], kinds: [KIND_FOLLOW], limit: 2 }]);
  await putEvents(events);
  const latest = dedupeEventsByID(events).sort((a, b) => Number(b.created_at) - Number(a.created_at))[0];
  return followPubkeys(latest);
}

/**
 * Bounded BFS web-of-trust expansion (mirrors internal/store/wot_reach.go intent).
 */
export async function expandWebOfTrust(seedPubkey, depth = 2, { maxAuthors = DEFAULT_MAX_AUTHORS } = {}) {
  const seed = normalizePubkey(seedPubkey);
  if (!seed) return [];
  const boundedDepth = Math.min(3, Math.max(1, Number(depth) || 1));
  const cached = readDiskCache(seed, boundedDepth);
  if (cached) return [...cached].slice(0, maxAuthors);

  const serverCached = await fetchServerResolvedAuthors(seed, boundedDepth, maxAuthors);
  if (serverCached) {
    writeDiskCache(seed, boundedDepth, serverCached);
    return [...serverCached].slice(0, maxAuthors);
  }

  const visited = new Set([seed]);
  let frontier = [seed];
  for (let hop = 0; hop < boundedDepth; hop++) {
    const next = [];
    const batch = frontier.slice(0, 32);
    const graphs = await Promise.all(batch.map((pk) => followGraphFor(pk)));
    for (const follows of graphs) {
      for (const pk of follows) {
        if (visited.has(pk)) continue;
        visited.add(pk);
        next.push(pk);
        if (visited.size >= maxAuthors) break;
      }
      if (visited.size >= maxAuthors) break;
    }
    frontier = next;
    if (!frontier.length || visited.size >= maxAuthors) break;
  }
  writeDiskCache(seed, boundedDepth, visited);
  return [...visited].slice(0, maxAuthors);
}

export function clearWebOfTrustDiskCache() {
  try {
    localStorage.removeItem(WOT_CACHE_KEY);
  } catch {
    // ignore
  }
}

/** Sync read of the local WoT graph cache (null when missing or stale). */
export function peekWebOfTrustDiskMembership(seedPubkey, depth) {
  const seed = normalizePubkey(seedPubkey);
  if (!seed) return null;
  const boundedDepth = Math.min(3, Math.max(1, Number(depth) || 1));
  return readDiskCache(seed, boundedDepth);
}
