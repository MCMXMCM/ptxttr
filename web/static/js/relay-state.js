import { MAX_RELAYS } from "./nostr-kinds.js";
import { DEFAULT_RELAYS, normalizeRelayList, normalizeRelayURL } from "./relay-config.js";

export const RELAY_CONFIG_KEY = "ptxt_relay_config";
export const LEGACY_RELAYS_KEY = "ptxt_relays";

function defaultUserRelayMetadata() {
  return { relays: [], updatedAt: 0 };
}

function defaultRelayConfig() {
  return {
    useAppRelays: true,
    useUserRelays: true,
    userRelayMetadata: defaultUserRelayMetadata(),
  };
}

function readStorage(key) {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function writeStorage(key, value) {
  try {
    localStorage.setItem(key, value);
    return true;
  } catch {
    return false;
  }
}

function removeStorage(key) {
  try {
    localStorage.removeItem(key);
  } catch {
    // ignore
  }
}

function normalizeRelayEntry(entry) {
  const relay = normalizeRelayURL(entry?.url || "");
  if (!relay) return null;
  const read = entry?.read !== false;
  const write = entry?.write !== false;
  return { url: relay, read, write };
}

function normalizeRelayEntries(entries = [], max = MAX_RELAYS) {
  const out = [];
  const seen = new Set();
  for (const entry of Array.isArray(entries) ? entries : []) {
    const normalized = normalizeRelayEntry(entry);
    if (!normalized || seen.has(normalized.url)) continue;
    seen.add(normalized.url);
    out.push(normalized);
    if (out.length >= max) break;
  }
  return out;
}

function normalizeRelayMetadata(metadata) {
  const relays = normalizeRelayEntries(metadata?.relays || [], MAX_RELAYS);
  const updatedAt = Math.max(0, Number.parseInt(`${metadata?.updatedAt ?? 0}`, 10) || 0);
  return { relays, updatedAt };
}

function normalizeRelayConfig(config) {
  const defaults = defaultRelayConfig();
  const userRelayMetadata = normalizeRelayMetadata(config?.userRelayMetadata || defaults.userRelayMetadata);
  return {
    useAppRelays: config?.useAppRelays !== false,
    useUserRelays: config?.useUserRelays === true && userRelayMetadata.relays.length > 0,
    userRelayMetadata,
  };
}

function notifyRelayConfigChanged(config) {
  if (typeof window === "undefined" || typeof window.dispatchEvent !== "function" || typeof CustomEvent !== "function") return;
  window.dispatchEvent(new CustomEvent("ptxt:relays", { detail: config }));
}

function persistRelayConfig(config, { notify = true } = {}) {
  const normalized = normalizeRelayConfig(config);
  writeStorage(RELAY_CONFIG_KEY, JSON.stringify(normalized));
  if (notify) notifyRelayConfigChanged(normalized);
  return normalized;
}

function migrateLegacyRelays(raw) {
  let parsed = [];
  try {
    parsed = JSON.parse(raw || "[]");
  } catch {
    parsed = [];
  }
  const relays = normalizeRelayList(Array.isArray(parsed) ? parsed : [], MAX_RELAYS).map((url) => ({
    url,
    read: true,
    write: true,
  }));
  const migrated = {
    useAppRelays: false,
    useUserRelays: relays.length > 0,
    userRelayMetadata: {
      relays,
      updatedAt: 0,
    },
  };
  persistRelayConfig(migrated, { notify: false });
  removeStorage(LEGACY_RELAYS_KEY);
  return migrated;
}

export function migrateLegacyRelayState() {
  const existing = readStorage(RELAY_CONFIG_KEY);
  if (existing) {
    try {
      return normalizeRelayConfig(JSON.parse(existing));
    } catch {
      const defaults = defaultRelayConfig();
      return persistRelayConfig(defaults, { notify: false });
    }
  }
  const legacy = readStorage(LEGACY_RELAYS_KEY);
  if (legacy != null) return migrateLegacyRelays(legacy);
  return normalizeRelayConfig(defaultRelayConfig());
}

export function loadRelayConfig() {
  const existing = readStorage(RELAY_CONFIG_KEY);
  if (existing) {
    try {
      return normalizeRelayConfig(JSON.parse(existing));
    } catch {
      return persistRelayConfig(defaultRelayConfig(), { notify: false });
    }
  }
  return migrateLegacyRelayState();
}

export function saveRelayConfig(config) {
  return persistRelayConfig(config, { notify: true });
}

function appRelayEntries() {
  return normalizeRelayEntries(DEFAULT_RELAYS.map((url) => ({ url, read: true, write: true })));
}

function mergeRelayEntries(sources = []) {
  const out = [];
  const seen = new Set();
  for (const source of sources) {
    for (const entry of Array.isArray(source) ? source : []) {
      const normalized = normalizeRelayEntry(entry);
      if (!normalized || seen.has(normalized.url)) continue;
      seen.add(normalized.url);
      out.push(normalized);
    }
  }
  return out;
}

export function effectiveRelayMetadata(config = loadRelayConfig()) {
  const normalized = normalizeRelayConfig(config);
  const sources = [];
  if (normalized.useAppRelays) sources.push(appRelayEntries());
  if (normalized.useUserRelays) sources.push(normalized.userRelayMetadata.relays);
  return {
    relays: mergeRelayEntries(sources),
    updatedAt: normalized.userRelayMetadata.updatedAt,
  };
}

export function effectiveReadRelays(config = loadRelayConfig()) {
  return effectiveRelayMetadata(config).relays.filter((relay) => relay.read).map((relay) => relay.url);
}

export function effectiveWriteRelays(config = loadRelayConfig()) {
  return effectiveRelayMetadata(config).relays.filter((relay) => relay.write).map((relay) => relay.url);
}

export function relayConfigUserRelayURLs(config = loadRelayConfig()) {
  return normalizeRelayEntries(config?.userRelayMetadata?.relays || [], MAX_RELAYS).map((relay) => relay.url);
}
