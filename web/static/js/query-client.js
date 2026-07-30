import { QueryClient } from "../lib/query-core.js";
import { stableHash } from "./client-store.js";
import { normalizeRelayList } from "./relay-config.js";

const DEFAULT_STALE_TIME_MS = 60_000;
const DEFAULT_GC_TIME_MS = 5 * 60_000;
const QUERY_TIMINGS = Object.freeze({
  feedPage: { staleTime: 60_000, gcTime: 30 * 60_000 },
  threadBundle: { staleTime: 60_000, gcTime: 30 * 60_000 },
  profile: { staleTime: 2 * 60_000, gcTime: 30 * 60_000 },
  profiles: { staleTime: 2 * 60_000, gcTime: 30 * 60_000 },
  replaceable: { staleTime: 2 * 60_000, gcTime: 30 * 60_000 },
  notifications: { staleTime: 10_000, gcTime: 5 * 60_000 },
  relayFetch: { staleTime: 5_000, gcTime: 2 * 60_000 },
  feedMetadata: { staleTime: 20_000, gcTime: 5 * 60_000 },
});

let queryClient = null;

export const queryKeys = Object.freeze({
  root: () => ["nostr"],
  relayFetch(relays = [], filters = []) {
    return ["nostr", "relayFetch", stableHash({
      relays: normalizeRelayList(relays, Number.MAX_SAFE_INTEGER),
      filters,
    })];
  },
  replaceable(pubkey, kind, relays = []) {
    return ["nostr", "replaceable", String(pubkey || "").toLowerCase(), Number(kind) || 0, stableHash({
      relays: normalizeRelayList(relays, Number.MAX_SAFE_INTEGER),
    })];
  },
  profiles(pubkeys = [], relays = []) {
    return ["nostr", "profiles", stableHash({
      pubkeys: [...new Set((pubkeys || []).map((pubkey) => String(pubkey || "").toLowerCase()).filter(Boolean))].sort(),
      relays: normalizeRelayList(relays, Number.MAX_SAFE_INTEGER),
    })];
  },
  followContacts(pubkey, relays = []) {
    return ["nostr", "followContacts", String(pubkey || "").toLowerCase(), stableHash({
      relays: normalizeRelayList(relays, Number.MAX_SAFE_INTEGER),
    })];
  },
  followGraph(pubkey, relays = [], followerLimit = 250) {
    return ["nostr", "followGraph", String(pubkey || "").toLowerCase(), Number(followerLimit) || 250, stableHash({
      relays: normalizeRelayList(relays, Number.MAX_SAFE_INTEGER),
    })];
  },
  bookmarkList(pubkey, relays = []) {
    return ["nostr", "bookmarkList", String(pubkey || "").toLowerCase(), stableHash({
      relays: normalizeRelayList(relays, Number.MAX_SAFE_INTEGER),
    })];
  },
  muteList(pubkey, relays = []) {
    return ["nostr", "muteList", String(pubkey || "").toLowerCase(), stableHash({
      relays: normalizeRelayList(relays, Number.MAX_SAFE_INTEGER),
    })];
  },
  profile(pubkey, relays = []) {
    return ["nostr", "profile", String(pubkey || "").toLowerCase(), stableHash({
      relays: normalizeRelayList(relays, Number.MAX_SAFE_INTEGER),
    })];
  },
  feedPage({
    viewerPubkey = "",
    sort = "",
    relays = [],
    since = 0,
    until = 0,
    untilID = "",
    limit = 50,
  } = {}) {
    return ["nostr", "feedPage", stableHash({
      viewerPubkey: String(viewerPubkey || "").toLowerCase(),
      sort: String(sort || ""),
      relays: normalizeRelayList(relays, Number.MAX_SAFE_INTEGER),
      since: Number(since) || 0,
      until: Number(until) || 0,
      untilID: String(untilID || "").toLowerCase(),
      limit: Number(limit) || 50,
    })];
  },
  threadBundle(rootID, selectedID = "", relays = [], forceRelayReplies = false) {
    return ["nostr", "threadBundle", String(rootID || "").toLowerCase(), String(selectedID || "").toLowerCase(), stableHash({
      relays: normalizeRelayList(relays, Number.MAX_SAFE_INTEGER),
      forceRelayReplies: forceRelayReplies === true,
    })];
  },
  notifications({
    viewerPubkey = "",
    relays = [],
    limit = 40,
    beforeCreatedAt = 0,
    beforeID = "",
  } = {}) {
    return ["nostr", "notifications", stableHash({
      viewerPubkey: String(viewerPubkey || "").toLowerCase(),
      relays: normalizeRelayList(relays, Number.MAX_SAFE_INTEGER),
      limit: Number(limit) || 40,
      beforeCreatedAt: Number(beforeCreatedAt) || 0,
      beforeID: String(beforeID || "").toLowerCase(),
    })];
  },
  feedMetadata({
    noteIDs = [],
    viewerPubkey = "",
    sort = "",
  } = {}) {
    return ["nostr", "feedMetadata", stableHash({
      noteIDs: [...new Set((noteIDs || []).map((id) => String(id || "").toLowerCase()).filter(Boolean))],
      viewerPubkey: String(viewerPubkey || "").toLowerCase(),
      sort: String(sort || ""),
    })];
  },
});

export function queryTimingForKey(queryKey, overrides = {}) {
  const family = Array.isArray(queryKey) ? String(queryKey[1] || "") : "";
  const base = QUERY_TIMINGS[family] || {
    staleTime: DEFAULT_STALE_TIME_MS,
    gcTime: DEFAULT_GC_TIME_MS,
  };
  return {
    staleTime: Number.isFinite(overrides.staleTime) ? overrides.staleTime : base.staleTime,
    gcTime: Number.isFinite(overrides.gcTime) ? overrides.gcTime : base.gcTime,
  };
}

export function getQueryClient() {
  if (!queryClient) {
    queryClient = new QueryClient({
      defaultOptions: {
        queries: {
          gcTime: DEFAULT_GC_TIME_MS,
          staleTime: DEFAULT_STALE_TIME_MS,
          retry: false,
        },
      },
    });
  }
  return queryClient;
}

export function peekQueryData(queryKey) {
  if (!queryKey) return undefined;
  return getQueryClient().getQueryData(queryKey);
}

export function primeQueryData(queryKey, value, options = {}) {
  if (!queryKey || value === undefined) return value;
  const client = getQueryClient();
  const timings = queryTimingForKey(queryKey, options);
  client.setQueryDefaults(queryKey, {
    gcTime: timings.gcTime,
    staleTime: timings.staleTime,
    retry: false,
  });
  client.setQueryData(queryKey, value, {
    updatedAt: Number(options.updatedAt) || Date.now(),
  });
  return value;
}

export function primeQueryDataFromPersisted(record, queryKey, options = {}) {
  if (!record || !queryKey) return null;
  return primeQueryData(queryKey, record, {
    updatedAt: options.updatedAt ?? record.saved_at ?? record.updated_at,
    staleTime: options.staleTime,
    gcTime: options.gcTime,
  });
}

export async function fetchCachedQuery({
  queryKey,
  queryFn,
  cacheMode = "network-first",
  staleTime = DEFAULT_STALE_TIME_MS,
  gcTime,
} = {}) {
  const client = getQueryClient();
  if (!queryKey || typeof queryFn !== "function") {
    throw new Error("fetchCachedQuery requires queryKey and queryFn");
  }

  if (cacheMode === "cache-first") {
    const cached = peekQueryData(queryKey);
    if (cached !== undefined) return cached;
  }

  const timings = queryTimingForKey(queryKey, { staleTime, gcTime });

  if (cacheMode === "refresh") {
    return client.fetchQuery({
      queryKey,
      queryFn,
      staleTime: 0,
      gcTime: timings.gcTime,
    });
  }

  return client.fetchQuery({
    queryKey,
    queryFn,
    staleTime: timings.staleTime,
    gcTime: timings.gcTime,
  });
}

export function invalidateNostrQueries(filters = {}) {
  return getQueryClient().invalidateQueries({
    queryKey: queryKeys.root(),
    ...filters,
  });
}

export function removeNostrQueries(filters = {}) {
  return getQueryClient().removeQueries({
    queryKey: queryKeys.root(),
    ...filters,
  });
}

export function resetQueryClientForTests() {
  if (queryClient) {
    try {
      queryClient.clear();
      queryClient.unmount?.();
    } catch {
      // ignore cleanup failures in tests
    }
  }
  queryClient = null;
}
