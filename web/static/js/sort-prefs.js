import { normalizedPubkey } from "./session.js";

const FEED_SORT_KEY = "ptxt_feed_sort";
const READS_SORT_KEY = "ptxt_reads_sort";
const IMAGE_MODE_KEY = "ptxt_image_mode";
const WEB_OF_TRUST_ENABLED_KEY = "ptxt_wot_enabled";
const WEB_OF_TRUST_DEPTH_KEY = "ptxt_wot_depth";
const WEB_OF_TRUST_SEED_KEY = "ptxt_wot_seed_pubkey";
const TRENDING_TF_KEY = "ptxt_trending_tf";
const READS_TRENDING_TF_KEY = "ptxt_reads_trending_tf";
const THREAD_RENDER_MODE_KEY = "ptxt_thread_render_mode";
const BLOSSOM_SERVERS_KEY = "ptxt_blossom_servers";

/** Default Blossom bases (first = primary upload target, rest = fallback order). */
export const BLOSSOM_DEFAULT_SERVER_URLS = Object.freeze([
  "https://blossom.primal.net/",
  "https://blossom.nostr.build/",
]);

const BLOSSOM_PRESET_NOSTR_BUILD_URLS = Object.freeze([
  "https://blossom.nostr.build/",
  "https://blossom.primal.net/",
]);

function blossomURLsMatchPreset(list, preset) {
  if (list.length !== preset.length) return false;
  return list.every((u, i) => u === preset[i]);
}

const VALID = new Set(["recent", "trend24h", "trend7d"]);
const VALID_TRENDING_TF = new Set(["24h", "1w"]);
const MAX_WEB_OF_TRUST_DEPTH = 3;
const DEFAULT_LOGGED_OUT_WOT_DEPTH = 3;
const DEFAULT_LOGGED_OUT_WOT_SEED = "npub1sg6plzptd64u62a878hep2kev88swjh3tw00gjsfl8f237lmu63q0uf63m";

export const WEB_OF_TRUST_SEED_PRESETS = [
  {
    id: "jack",
    label: "Jack Dorsey",
    value: "npub1sg6plzptd64u62a878hep2kev88swjh3tw00gjsfl8f237lmu63q0uf63m",
    bio: "cofounder of Twitter, cofounder and chairman of Block, and active Nostr user",
  },
  {
    id: "fiatjaf",
    label: "Fiatjaf",
    value: "npub180cvv07tjdrrgpa0j7j7tmnyl2yr6yr7l8j4s3evf6u64th6gkwsyjh6w6",
    bio: "Nostr protocol contributor and creator of several Nostr tools",
  },
  {
    id: "gigi",
    label: "Gigi",
    value: "npub1dergggklka99wwrs92yz8wdjs952h2ux2ha2ed598ngwu9w7a6fsh9xzpc",
    bio: "Bitcoin educator and writer focused on open protocols",
  },
  {
    id: "lyn_alden",
    label: "Lyn Alden",
    value: "npub1a2cww4kn9wqte4ry70vyfwqyqvpswksna27rtxd8vty6c74era8sdcw83a",
    bio: "macro and Bitcoin researcher sharing long-form analysis",
  },
  {
    id: "odell",
    label: "Odell",
    value: "npub1qny3tkh0acurzla8x3zy4nhrjz5zd8l9sy9jys09umwng00manysew95gx",
    bio: "Bitcoin signal curator and host of discussion spaces",
  },
];

function normalize(value) {
  const s = String(value || "").trim();
  return VALID.has(s) ? s : "";
}

function normalizeTrendingTf(value) {
  const s = String(value || "").trim();
  return VALID_TRENDING_TF.has(s) ? s : "";
}

/** isTruthyToken matches the same truthy set as the server-side ParseBool helper. */
export function isTruthyToken(value) {
  const raw = String(value ?? "").trim().toLowerCase();
  return raw === "1" || raw === "true" || raw === "on" || raw === "yes";
}

export function normalizeWebOfTrustDepth(value) {
  const n = Number.parseInt(`${value ?? ""}`, 10);
  if (!Number.isFinite(n)) return 1;
  return Math.min(MAX_WEB_OF_TRUST_DEPTH, Math.max(1, n));
}

export function feedSortForSession(pubkey, sortMode) {
  const sort = normalize(sortMode);
  if (pubkey) return sort;
  if (sort) return sort;
  return "recent";
}

export function getFeedSortPref() {
  try {
    return normalize(localStorage.getItem(FEED_SORT_KEY));
  } catch {
    return "";
  }
}

export function getReadsSortPref() {
  try {
    return normalize(localStorage.getItem(READS_SORT_KEY));
  } catch {
    return "";
  }
}

export function setFeedSortPref(value) {
  try {
    const s = normalize(value);
    if (s) localStorage.setItem(FEED_SORT_KEY, s);
    else localStorage.removeItem(FEED_SORT_KEY);
  } catch {
    // ignore
  }
}

export function setReadsSortPref(value) {
  try {
    const s = normalize(value);
    if (s) localStorage.setItem(READS_SORT_KEY, s);
    else localStorage.removeItem(READS_SORT_KEY);
  } catch {
    // ignore
  }
}

export function getImageModePref() {
  try {
    const raw = String(localStorage.getItem(IMAGE_MODE_KEY) || "").trim().toLowerCase();
    if (!raw) return true;
    return !(raw === "0" || raw === "false" || raw === "off");
  } catch {
    return true;
  }
}

export function setImageModePref(enabled) {
  try {
    localStorage.setItem(IMAGE_MODE_KEY, enabled ? "1" : "0");
  } catch {
    // ignore
  }
}

export function getThreadRenderModePref() {
  try {
    const raw = String(localStorage.getItem(THREAD_RENDER_MODE_KEY) || "").trim().toLowerCase();
    return raw === "tree" ? "tree" : "thread";
  } catch {
    return "thread";
  }
}

export function setThreadRenderModePref(mode) {
  try {
    if (String(mode).trim().toLowerCase() === "tree") {
      localStorage.setItem(THREAD_RENDER_MODE_KEY, "tree");
      return;
    }
    localStorage.removeItem(THREAD_RENDER_MODE_KEY);
  } catch {
    // ignore
  }
}

export function getWebOfTrustEnabledPref() {
  try {
    const raw = localStorage.getItem(WEB_OF_TRUST_ENABLED_KEY);
    if (raw == null) return !normalizedPubkey();
    return isTruthyToken(raw);
  } catch {
    return false;
  }
}

export function setWebOfTrustEnabledPref(enabled) {
  try {
    localStorage.setItem(WEB_OF_TRUST_ENABLED_KEY, enabled ? "1" : "0");
  } catch {
    // ignore
  }
}

export function getWebOfTrustDepthPref() {
  try {
    const raw = localStorage.getItem(WEB_OF_TRUST_DEPTH_KEY);
    if (raw == null && !normalizedPubkey()) return DEFAULT_LOGGED_OUT_WOT_DEPTH;
    return normalizeWebOfTrustDepth(raw);
  } catch {
    return 1;
  }
}

export function setWebOfTrustDepthPref(value) {
  try {
    localStorage.setItem(WEB_OF_TRUST_DEPTH_KEY, `${normalizeWebOfTrustDepth(value)}`);
  } catch {
    // ignore
  }
}

export function getWebOfTrustSeedPref() {
  try {
    return String(localStorage.getItem(WEB_OF_TRUST_SEED_KEY) || "").trim();
  } catch {
    return "";
  }
}

export function setWebOfTrustSeedPref(seed) {
  try {
    const next = String(seed || "").trim();
    if (next) localStorage.setItem(WEB_OF_TRUST_SEED_KEY, next);
    else localStorage.removeItem(WEB_OF_TRUST_SEED_KEY);
  } catch {
    // ignore
  }
}

export function getEffectiveLoggedOutWebOfTrustSeed() {
  return getWebOfTrustSeedPref() || DEFAULT_LOGGED_OUT_WOT_SEED;
}

/** Feed sidebar trending; empty storage means default 24h window. */
export function getTrendingTimeframePref() {
  try {
    return normalizeTrendingTf(localStorage.getItem(TRENDING_TF_KEY));
  } catch {
    return "";
  }
}

export function setTrendingTimeframePref(value) {
  try {
    const s = normalizeTrendingTf(value);
    if (s === "1w") localStorage.setItem(TRENDING_TF_KEY, "1w");
    else localStorage.removeItem(TRENDING_TF_KEY);
  } catch {
    // ignore
  }
}

export function getReadsTrendingTimeframePref() {
  try {
    return normalizeTrendingTf(localStorage.getItem(READS_TRENDING_TF_KEY));
  } catch {
    return "";
  }
}

export function setReadsTrendingTimeframePref(value) {
  try {
    const s = normalizeTrendingTf(value);
    if (s === "1w") localStorage.setItem(READS_TRENDING_TF_KEY, "1w");
    else localStorage.removeItem(READS_TRENDING_TF_KEY);
  } catch {
    // ignore
  }
}

export function normalizeBlossomBaseUrl(raw) {
  const s = String(raw || "").trim();
  if (!s) return "";
  try {
    const u = new URL(s.includes("://") ? s : `https://${s}`);
    if (u.protocol !== "https:") return "";
    const trimmed = (u.pathname || "/").replace(/\/+$/, "");
    if (!trimmed || trimmed === "/") return `${u.origin}/`;
    return `${u.origin}${trimmed}/`;
  } catch {
    return "";
  }
}

function normalizeBlossomServerList(urls) {
  const seen = new Set();
  const out = [];
  for (const raw of urls || []) {
    const n = normalizeBlossomBaseUrl(raw);
    if (!n || seen.has(n)) continue;
    seen.add(n);
    out.push(n);
  }
  return out;
}

/** Ordered Blossom server base URLs (https://host/.../). */
export function getBlossomServerURLs() {
  try {
    const raw = localStorage.getItem(BLOSSOM_SERVERS_KEY);
    if (!raw) return [...BLOSSOM_DEFAULT_SERVER_URLS];
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [...BLOSSOM_DEFAULT_SERVER_URLS];
    const list = normalizeBlossomServerList(parsed.map((x) => String(x || "").trim()));
    return list.length > 0 ? list : [...BLOSSOM_DEFAULT_SERVER_URLS];
  } catch {
    return [...BLOSSOM_DEFAULT_SERVER_URLS];
  }
}

export function setBlossomServerURLs(urls) {
  try {
    const list = normalizeBlossomServerList(urls);
    if (list.length === 0) {
      localStorage.removeItem(BLOSSOM_SERVERS_KEY);
      return;
    }
    const defaults = [...BLOSSOM_DEFAULT_SERVER_URLS];
    if (list.length === defaults.length && list.every((u, i) => u === defaults[i])) {
      localStorage.removeItem(BLOSSOM_SERVERS_KEY);
      return;
    }
    localStorage.setItem(BLOSSOM_SERVERS_KEY, JSON.stringify(list));
  } catch {
    // ignore
  }
}

export function resetBlossomServerURLsToDefaults() {
  try {
    localStorage.removeItem(BLOSSOM_SERVERS_KEY);
  } catch {
    // ignore
  }
}

/** Primal-first vs nostr.build-first presets for the settings UI. */
export function setBlossomPreset(preset) {
  const p = String(preset || "").toLowerCase();
  if (p === "nostr_build" || p === "nostr.build") {
    setBlossomServerURLs(["https://blossom.nostr.build/", "https://blossom.primal.net/"]);
    return;
  }
  if (p === "primal") {
    setBlossomServerURLs([...BLOSSOM_DEFAULT_SERVER_URLS]);
    return;
  }
}

/** Which preset matches the given normalized URL list, or "custom". */
export function getBlossomPresetIdForURLs(list) {
  if (blossomURLsMatchPreset(list, BLOSSOM_DEFAULT_SERVER_URLS)) return "primal";
  if (blossomURLsMatchPreset(list, BLOSSOM_PRESET_NOSTR_BUILD_URLS)) return "nostr_build";
  return "custom";
}

/** Which preset matches the stored list, or "custom". */
export function getBlossomPresetId() {
  return getBlossomPresetIdForURLs(getBlossomServerURLs());
}

