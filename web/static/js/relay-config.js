import { MAX_RELAYS } from "./nostr-kinds.js";

/** Default relay set (mirrors internal/config/config.go). */
export const DEFAULT_RELAYS = Object.freeze([
  "wss://relay.primal.net",
  "wss://relay.damus.io",
  "wss://nos.lol",
]);

export const METADATA_RELAYS = DEFAULT_RELAYS;

export const INDEXER_NIP50_RELAYS = Object.freeze([
  "wss://relay.nostr.band",
  "wss://search.nos.today",
  "wss://relay.primal.net",
]);

/** NIP-50 hot-rank relays (mirrors iOS FeedService.trendingSearchRelays). */
export const TRENDING_SEARCH_RELAYS = Object.freeze([
  "wss://relay.nostr.band",
]);

/** Reaction hydration relays (iOS excludes search.nos.today). */
export const REACTION_SEARCH_RELAYS = Object.freeze(
  TRENDING_SEARCH_RELAYS.filter((url) => url !== "wss://search.nos.today"),
);

export const DIRECT_RELAYS_KEY = "ptxt_direct_relays";

/** When unset, direct relay I/O is enabled (relay-native default). Set "0" to force server APIs. */
export function directRelaysEnabled() {
  try {
    return localStorage.getItem(DIRECT_RELAYS_KEY) !== "0";
  } catch {
    return true;
  }
}

export function normalizeRelayList(raw, max = MAX_RELAYS) {
  const out = [];
  const seen = new Set();
  const list = Array.isArray(raw) ? raw : [];
  for (const item of list) {
    const relay = normalizeRelayURL(item);
    if (!relay || seen.has(relay)) continue;
    seen.add(relay);
    out.push(relay);
    if (out.length >= max) break;
  }
  return out;
}

export function normalizeRelayURL(raw) {
  const value = String(raw || "").trim().replace(/\/+$/, "");
  if (!value.startsWith("ws://") && !value.startsWith("wss://")) return "";
  return value;
}

export function baseReadRelays(selected = []) {
  const picked = normalizeRelayList(selected);
  if (picked.length) return picked;
  return [...DEFAULT_RELAYS];
}
