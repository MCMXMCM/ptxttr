import { NPool, NRelay1 } from "../lib/nostrify.js";
import { getEventHash, verifyEvent } from "../lib/nostr-tools.js";
import { latestReplaceable } from "./event-store.js";
import { DEFAULT_RELAYS, METADATA_RELAYS, normalizeRelayList } from "./relay-config.js";
import { effectiveReadRelays, effectiveWriteRelays } from "./relay-state.js";
import { KIND_RELAY_LIST } from "./nostr-kinds.js";
import { normalizePubkey, relayHintsFromKind10002 } from "./relay-utils.js";

const DEFAULT_EOSE_TIMEOUT_MS = 1200;
const VERIFIED_EVENT_LIMIT = 4096;

let sharedPool = null;

/**
 * Signature check for relay events that remembers signatures it already
 * verified. Several relays usually return the same events, and each schnorr
 * check costs about a millisecond on the main thread. A repeat only needs its
 * id recomputed: an event that hashes to a verified id and carries the same
 * signature is that verified event. At most `limit` ids are kept.
 */
export function createRelayEventVerifier({ verify = verifyEvent, hash = getEventHash, limit = VERIFIED_EVENT_LIMIT } = {}) {
  const verifiedSigs = new Map();
  return (event) => {
    const id = typeof event?.id === "string" ? event.id : "";
    const sig = typeof event?.sig === "string" ? event.sig : "";
    if (id && sig && verifiedSigs.get(id) === sig) {
      try {
        return hash(event) === id;
      } catch {
        return false;
      }
    }
    if (!verify(event)) return false;
    if (id && sig) {
      verifiedSigs.set(id, sig);
      if (verifiedSigs.size > limit) verifiedSigs.delete(verifiedSigs.keys().next().value);
    }
    return true;
  };
}

export const verifyRelayEvent = createRelayEventVerifier();

async function relayHintsForAuthor(pubkey) {
  const pk = normalizePubkey(pubkey);
  if (!pk) return { read: [], write: [], any: [] };
  try {
    const event = await latestReplaceable(pk, KIND_RELAY_LIST);
    return relayHintsFromKind10002(event);
  } catch {
    return { read: [], write: [], any: [] };
  }
}

function appendFilters(routes, relay, filters) {
  const normalized = normalizeRelayList([relay], 1)[0];
  if (!normalized || !filters?.length) return;
  const existing = routes.get(normalized) || [];
  routes.set(normalized, [...existing, ...filters]);
}

export async function routeFiltersToRelayMap(filters = []) {
  const normalizedFilters = Array.isArray(filters) ? filters.filter(Boolean) : [];
  const defaults = effectiveReadRelays();
  const fallback = defaults.length ? defaults : [...DEFAULT_RELAYS];
  const routes = new Map();

  for (const filter of normalizedFilters) {
    const authors = Array.isArray(filter?.authors) ? filter.authors.map(normalizePubkey).filter(Boolean) : [];
    if (!authors.length) {
      fallback.forEach((relay) => appendFilters(routes, relay, [filter]));
      continue;
    }

    const hinted = normalizeRelayList((await Promise.all(authors.map(async (author) => {
      const hints = await relayHintsForAuthor(author);
      return [...(hints.read || []), ...(hints.any || [])];
    }))).flat(), 12);

    const relays = hinted.length ? hinted : fallback;
    relays.forEach((relay) => appendFilters(routes, relay, [filter]));
  }

  return routes;
}

export async function routePublishEventToRelays(event) {
  const author = normalizePubkey(event?.pubkey);
  const hints = author ? await relayHintsForAuthor(author) : { write: [], any: [] };
  return normalizeRelayList([
    ...(hints.write || []),
    ...effectiveWriteRelays(),
    ...(hints.any || []),
    ...DEFAULT_RELAYS,
    ...METADATA_RELAYS,
  ]);
}

export function getNostrPool() {
  if (!sharedPool) {
    sharedPool = new NPool({
      open(url) {
        return new NRelay1(url, { idleTimeout: false, verifyEvent: verifyRelayEvent });
      },
      reqRouter(filters) {
        return routeFiltersToRelayMap(filters);
      },
      eventRouter(event) {
        return routePublishEventToRelays(event);
      },
      eoseTimeout: DEFAULT_EOSE_TIMEOUT_MS,
    });
  }
  return sharedPool;
}

export function closeNostrPool() {
  if (!sharedPool) return;
  void sharedPool.close().catch(() => {});
  sharedPool = null;
}
