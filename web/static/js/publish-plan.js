import {
  DEFAULT_RELAYS,
  METADATA_RELAYS,
  normalizeRelayList,
} from "./relay-config.js";
import {
  KIND_FOLLOW,
  KIND_RELAY_LIST,
  MAX_RELAYS,
} from "./nostr-kinds.js";
import {
  followPubkeys,
  followRelayHints,
  normalizePubkey,
  participantPubkeys,
  relayHintsFromKind10002,
} from "./relay-utils.js";
import { relayFetch } from "./relay-pool.js";
import { effectiveReadRelays, effectiveWriteRelays } from "./relay-state.js";
import { fetchCachedQuery, queryKeys } from "./query-client.js";

const bookmarkPublishFallbackRelaysCache = new Map();

/**
 * Plan outbound relays for publish (subset of internal/httpx/service_publish.go).
 * Precedence: explicit caller relays, author kind-10002 write, selected relays,
 * author kind-10002 any, participant hints, defaults/metadata relays.
 */
export async function planPublishRelays(event, explicitRelays = []) {
  const merged = [];
  const seen = new Set();
  const append = (list) => {
    for (const relay of normalizeRelayList(list, MAX_RELAYS * 3)) {
      if (seen.has(relay)) continue;
      seen.add(relay);
      merged.push(relay);
    }
  };

  append(explicitRelays);
  const author = normalizePubkey(event?.pubkey);
  if (author) {
    const hints = await authorRelayHints(author);
    append(hints.write);
  }
  append(effectiveWriteRelays());
  if (author) {
    const hints = await authorRelayHints(author);
    append(hints.any);
  }
  append(participantPubkeys(event).flatMap((pk) => bookmarkPublishFallbackRelaysCache.get(pk) || []));
  append(DEFAULT_RELAYS);
  append(METADATA_RELAYS);
  return normalizeRelayList(merged, MAX_RELAYS);
}

export async function bookmarkPublishFallbackRelays(pubkey, attempted = []) {
  const seen = new Set(normalizeRelayList(attempted));
  const merged = [];
  appendUnique(merged, seen, METADATA_RELAYS);
  appendUnique(merged, seen, DEFAULT_RELAYS);
  const hints = await authorRelayHints(pubkey);
  appendUnique(merged, seen, hints.write);
  appendUnique(merged, seen, hints.any);
  return normalizeRelayList(merged, MAX_RELAYS);
}

function appendUnique(out, seen, relays) {
  for (const relay of normalizeRelayList(relays, MAX_RELAYS * 3)) {
    if (seen.has(relay)) continue;
    seen.add(relay);
    out.push(relay);
  }
}

async function authorRelayHints(pubkey) {
  const pk = normalizePubkey(pubkey);
  if (!pk) return { write: [], read: [], any: [] };
  const cached = bookmarkPublishFallbackRelaysCache.get(`${pk}:hints`);
  if (cached) return cached;
  const relays = effectiveReadRelays();
  const hints = await fetchCachedQuery({
    queryKey: queryKeys.replaceable(pk, KIND_RELAY_LIST, relays),
    queryFn: async () => {
      const events = await relayFetch(relays, [{ authors: [pk], kinds: [KIND_RELAY_LIST], limit: 1 }]);
      const latest = events.sort((a, b) => Number(b.created_at) - Number(a.created_at))[0];
      return relayHintsFromKind10002(latest);
    },
    cacheMode: "cache-first",
    staleTime: 60_000,
  });
  bookmarkPublishFallbackRelaysCache.set(`${pk}:hints`, hints);
  bookmarkPublishFallbackRelaysCache.set(pk, [...hints.write, ...hints.any]);
  return hints;
}

export function readRelaysForViewer() {
  return effectiveReadRelays();
}

export async function fetchFollowContacts(pubkey) {
  const pk = normalizePubkey(pubkey);
  if (!pk) return { pubkeys: [], relayHints: new Map() };
  const relays = readRelaysForViewer();
  return fetchCachedQuery({
    queryKey: queryKeys.followContacts(pk, relays),
    queryFn: async () => {
      const events = await relayFetch(relays, [{ authors: [pk], kinds: [KIND_FOLLOW], limit: 3 }]);
      const latest = events.sort((a, b) => Number(b.created_at) - Number(a.created_at))[0];
      const relayHints = latest ? followRelayHints(latest) : new Map();
      return {
        pubkeys: latest ? followPubkeys(latest) : [],
        relayHints,
        event: latest || null,
      };
    },
    cacheMode: "cache-first",
    staleTime: 30_000,
  });
}
