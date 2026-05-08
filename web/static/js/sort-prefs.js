import { ensureFeedURLHasSessionPubkey, isFeedLikePath, normalizedPubkey } from "./session.js";

const FEED_SORT_KEY = "ptxt_feed_sort";
const READS_SORT_KEY = "ptxt_reads_sort";
const IMAGE_MODE_KEY = "ptxt_image_mode";
const WEB_OF_TRUST_ENABLED_KEY = "ptxt_wot_enabled";
const WEB_OF_TRUST_DEPTH_KEY = "ptxt_wot_depth";
const WEB_OF_TRUST_SEED_KEY = "ptxt_wot_seed_pubkey";
const TRENDING_TF_KEY = "ptxt_trending_tf";
const READS_TRENDING_TF_KEY = "ptxt_reads_trending_tf";
const THREAD_RENDER_MODE_KEY = "ptxt_thread_render_mode";
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

function hasCompleteStoredWebOfTrustParams(url) {
  if (!url?.searchParams) return false;
  const { searchParams } = url;
  if (searchParams.get("wot") !== "1") return false;
  if (!searchParams.has("wot_depth")) return false;
  if (normalizedPubkey()) return true;
  return searchParams.has("seed_pubkey");
}

export function applyWebOfTrustParams(target, options = {}) {
  if (!target) return;
  const {
    depth = getWebOfTrustDepthPref(),
    viewer = normalizedPubkey(),
    seedPubkey = "",
  } = options;
  target.set("wot", "1");
  target.set("wot_depth", `${normalizeWebOfTrustDepth(depth)}`);
  const customSeed = String(seedPubkey || "").trim();
  if (customSeed) {
    target.set("seed_pubkey", customSeed);
    return;
  }
  if (!viewer) {
    target.set("seed_pubkey", getEffectiveLoggedOutWebOfTrustSeed());
    return;
  }
  target.delete("seed_pubkey");
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

/**
 * If the URL has no `sort` param, set it from localStorage (feed vs reads by path).
 * Does nothing when the page already encodes a sort (e.g. shared link).
 */
export function applyStoredSortIfMissing(url) {
  if (url.searchParams.has("sort")) return;
  const p = url.pathname;
  if (p === "/" || p === "/feed") {
    const s = feedSortForSession(normalizedPubkey(), getFeedSortPref());
    if (s) url.searchParams.set("sort", s);
  } else if (p === "/reads") {
    const s = getReadsSortPref();
    if (s) url.searchParams.set("sort", s);
  }
}

export function applyStoredTrendingTfIfMissing(url) {
  if (url.searchParams.has("tf")) return;
  const p = url.pathname;
  if (p !== "/" && p !== "/feed") return;
  const tf = getTrendingTimeframePref();
  if (tf === "1w") url.searchParams.set("tf", "1w");
}

export function applyStoredReadsTrendingTfIfMissing(url) {
  if (url.searchParams.has("reads_tf")) return;
  if (url.pathname !== "/reads") return;
  const tf = getReadsTrendingTimeframePref();
  if (tf === "1w") url.searchParams.set("reads_tf", "1w");
}

export function applyStoredWebOfTrustIfMissing(url) {
  if (!isFeedLikePath(url.pathname)) return;
  if (hasCompleteStoredWebOfTrustParams(url)) return;
  if (!getWebOfTrustEnabledPref()) return;
  applyWebOfTrustParams(url.searchParams);
}

/**
 * Mutates feed-like URLs so they match SPA navigation and fetch keys:
 * session pubkey, stored sort, trending window, and WoT defaults.
 * Avoids treating "/?sort=recent" and "/?wot=1&wot_depth=3&sort=recent" as
 * different places (which broke snapshot restore and triggered redundant
 * hydrations that could replace the feed with an empty fragment).
 */
export function applyCanonicalFeedLikeParams(url) {
  if (!isFeedLikePath(url.pathname)) {
    return;
  }
  ensureFeedURLHasSessionPubkey(url);
  applyStoredSortIfMissing(url);
  if (url.pathname === "/reads") {
    applyStoredReadsTrendingTfIfMissing(url);
  } else {
    applyStoredTrendingTfIfMissing(url);
  }
  applyStoredWebOfTrustIfMissing(url);
}

/**
 * Copy WoT params from `source` (URL or URLSearchParams) onto `target`
 * (URLSearchParams). No-op when wot=1 is not set on source. The depth
 * defaults to "1" to mirror server-side clamping when the param is absent.
 */
export function copyWebOfTrustParams(source, target) {
  const src = source instanceof URLSearchParams
    ? source
    : (source && source.searchParams);
  if (!src || !target) return;
  if (src.get("wot") !== "1") return;
  applyWebOfTrustParams(target, {
    depth: src.get("wot_depth") || "1",
    seedPubkey: src.get("seed_pubkey") || "",
  });
}

/** Replace `window.location` when `mutate` changes the URL; otherwise no-op. */
export function replaceLocationIfChanged(mutate) {
  const u = new URL(window.location.href);
  if (mutate(u) === false) return;
  const next = u.toString();
  if (next !== window.location.href) {
    location.replace(next);
  }
}

/**
 * Full page load: if the address bar has no `sort` but the user has a non-default
 * sort saved, replace the URL and reload so SSR matches the preference.
 * Skips when stored preference is "recent" (same as default without param).
 */
export function replaceLocationIfSortPreferenceNeedsURL() {
  replaceLocationIfChanged((u) => {
    if (u.searchParams.has("sort")) return false;
    const p = u.pathname;
    let s = "";
    if (p === "/" || p === "/feed") {
      const pubkey = normalizedPubkey();
      const loggedIn = Boolean(pubkey);
      s = feedSortForSession(pubkey, getFeedSortPref());
      if (!s || (loggedIn && s === "recent") || (!loggedIn && s === "trend7d")) return false;
    } else if (p === "/reads") {
      s = getReadsSortPref();
      if (!s || s === "recent") return false;
    } else {
      return false;
    }
    u.searchParams.set("sort", s);
  });
}

export function replaceLocationIfTrendingTfPreferenceNeedsURL() {
  replaceLocationIfChanged((u) => {
    if (u.searchParams.has("tf")) return false;
    if (u.pathname !== "/" && u.pathname !== "/feed") return false;
    if (getTrendingTimeframePref() !== "1w") return false;
    u.searchParams.set("tf", "1w");
  });
}

export function replaceLocationIfReadsTrendingTfPreferenceNeedsURL() {
  replaceLocationIfChanged((u) => {
    if (u.searchParams.has("reads_tf")) return false;
    if (u.pathname !== "/reads") return false;
    if (getReadsTrendingTimeframePref() !== "1w") return false;
    u.searchParams.set("reads_tf", "1w");
  });
}

export function replaceLocationIfWebOfTrustPreferenceNeedsURL() {
  replaceLocationIfChanged((u) => {
    if (!isFeedLikePath(u.pathname)) return false;
    if (!getWebOfTrustEnabledPref()) return false;
    if (hasCompleteStoredWebOfTrustParams(u)) return false;
    applyWebOfTrustParams(u.searchParams);
  });
}
