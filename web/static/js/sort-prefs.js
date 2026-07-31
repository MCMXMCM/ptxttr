import { normalizedPubkey } from "./session.js";
import { isTruthyToken } from "./prefs-utils.js";
import {
  applyDefaultViewerPrefsIfUnset,
  DEFAULT_LOGGED_OUT_WOT_DEPTH,
  DEFAULT_LOGGED_OUT_WOT_SEED_NPUB,
  desktopModeEnabled,
  loggedOutWebOfTrustDepthPref,
} from "./viewer-defaults.js";

export { isTruthyToken } from "./prefs-utils.js";

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
const DEFAULT_LOGGED_OUT_WOT_SEED = DEFAULT_LOGGED_OUT_WOT_SEED_NPUB;

export const WEB_OF_TRUST_SEED_PRESETS = [
  {
    id: "gigi",
    label: "Gigi",
    value: DEFAULT_LOGGED_OUT_WOT_SEED_NPUB,
    bio: "Bitcoin educator and writer focused on open protocols",
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

/** Writes default viewer prefs when keys are unset (logged-out WoT on, media on). */
export function ensureDefaultViewerPrefs() {
  try {
    applyDefaultViewerPrefsIfUnset();
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
  if (typeof document !== "undefined") {
    document.documentElement.dataset.ptxtImageMode = enabled ? "on" : "off";
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
  return true;
}

export function setWebOfTrustEnabledPref(enabled) {
  try {
    localStorage.setItem(WEB_OF_TRUST_ENABLED_KEY, "1");
  } catch {
    // ignore
  }
}

export function getWebOfTrustDepthPref() {
  if (!normalizedPubkey()) return loggedOutWebOfTrustDepthPref();
  try {
    const raw = localStorage.getItem(WEB_OF_TRUST_DEPTH_KEY);
    return normalizeWebOfTrustDepth(raw);
  } catch {
    return 1;
  }
}

export function setWebOfTrustDepthPref(value) {
  try {
    if (!normalizedPubkey() && !desktopModeEnabled()) {
      localStorage.setItem(WEB_OF_TRUST_DEPTH_KEY, String(DEFAULT_LOGGED_OUT_WOT_DEPTH));
      return;
    }
    localStorage.setItem(WEB_OF_TRUST_DEPTH_KEY, `${normalizeWebOfTrustDepth(value)}`);
  } catch {
    // ignore
  }
}

export function getWebOfTrustSeedPref() {
  try {
    if (!normalizedPubkey()) return "";
    return String(localStorage.getItem(WEB_OF_TRUST_SEED_KEY) || "").trim();
  } catch {
    return "";
  }
}

export function setWebOfTrustSeedPref(seed) {
  try {
    if (!normalizedPubkey()) {
      localStorage.removeItem(WEB_OF_TRUST_SEED_KEY);
      return;
    }
    const next = String(seed || "").trim();
    if (next) localStorage.setItem(WEB_OF_TRUST_SEED_KEY, next);
    else localStorage.removeItem(WEB_OF_TRUST_SEED_KEY);
  } catch {
    // ignore
  }
}

export function getEffectiveLoggedOutWebOfTrustSeed() {
  return DEFAULT_LOGGED_OUT_WOT_SEED;
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
