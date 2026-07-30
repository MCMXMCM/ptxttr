import { NPool, NRelay1 } from "../lib/nostrify.js";
import { latestReplaceable } from "./event-store.js";
import { DEFAULT_RELAYS, METADATA_RELAYS, normalizeRelayList } from "./relay-config.js";
import { effectiveReadRelays, effectiveWriteRelays } from "./relay-state.js";
import { KIND_RELAY_LIST } from "./nostr-kinds.js";
import { normalizePubkey, relayHintsFromKind10002 } from "./relay-utils.js";

const DEFAULT_EOSE_TIMEOUT_MS = 1200;

let sharedPool = null;

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
        return new NRelay1(url, { idleTimeout: false });
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
