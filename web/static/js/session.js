import { nip19 } from "../lib/nostr-tools.js";
import {
  applyRelayParamsToURL,
  stripViewerPrefSearchParams,
} from "./viewer-pref-url.js";
import {
	applyDefaultViewerPrefsIfUnset,
	DEFAULT_LOGGED_OUT_WOT_SEED_NPUB,
	desktopModeEnabled,
	loggedOutWebOfTrustDepthPref,
} from "./viewer-defaults.js";
import { profilePath } from "./relay-utils.js";
import {
  clearBootstrapPending,
  clearBootstrapPendingIfViewerChanged,
  markBootstrapPending,
} from "./first-login-bootstrap.js";
import { setAvatarImageSource } from "./avatar-cache.js";
import { effectiveReadRelays, effectiveWriteRelays, loadRelayConfig, relayConfigUserRelayURLs, RELAY_CONFIG_KEY, saveRelayConfig } from "./relay-state.js";

export { applyRelayParamsToURL, stripViewerPrefSearchParams };

const KEY = "ptxt_nostr_session";
const SESSION_NSEC_KEY = "ptxt_nsec";
const SIGNING_ACCOUNTS_KEY = "ptxt_nostr_signing_accounts";
const NIP07_RELAY_SYNC_KEY = "ptxt_nip07_relay_sync_pubkey";
const MAX_SIGNING_ACCOUNTS = 3;
// Viewer prefs live under sort-prefs' localStorage keys; we read them directly
// here (rather than importing the getters) to avoid a circular import
// (sort-prefs.js -> session.js). Keep these key names in sync with sort-prefs.js.
const WOT_SEED_KEY = "ptxt_wot_seed_pubkey";
const FEED_SORT_KEY = "ptxt_feed_sort";
const READS_SORT_KEY = "ptxt_reads_sort";
const TRENDING_TF_KEY = "ptxt_trending_tf";
const READS_TRENDING_TF_KEY = "ptxt_reads_trending_tf";
const WEB_OF_TRUST_DEPTH_KEY = "ptxt_wot_depth";

// After a successful publish, GET/HEAD to `/thread*`, `/u/*`, `/e/*` append
// `?_=<publishMs>` so the publisher's CloudFront key diverges until the edge
// TTL (~setContentAddressedCache s-maxage, 5m) would have expired anyway.
const RECENT_PUBLISH_KEY = "ptxt_last_publish_at_ms";
const RECENT_PUBLISH_WINDOW_MS = 5 * 60 * 1000;
const CACHE_BUST_PATH_PREFIXES = ["/thread", "/u/", "/e/"];

/** In-tab memo so parallel fragment fetches after publish avoid N× getItem. */
let publishBustToken = "";
let publishBustExpires = 0;

// HTTP headers the client uses to send viewer identity + view preferences to
// the origin without putting them in URLs (so anonymous SSR HTML can share a
// single CloudFront cache entry across all viewers). The server prefers these
// headers, falling back to the legacy `?pubkey=`, `?seed_pubkey=`, `?relays=`,
// `?sort=`, `?tf=`, `?reads_tf=`, `?wot=`, `?wot_depth=` query strings only
// for old bookmarks.
const HEADER_VIEWER_PUBKEY = "X-Ptxt-Viewer";
const HEADER_WOT_SEED = "X-Ptxt-Wot-Seed";
const HEADER_RELAYS = "X-Ptxt-Relays";
const HEADER_FEED_SORT = "X-Ptxt-Sort";
const HEADER_FEED_TRENDING_TF = "X-Ptxt-Tf";
const HEADER_READS_TRENDING_TF = "X-Ptxt-Reads-Tf";
const HEADER_WOT_ENABLED = "X-Ptxt-Wot";
const HEADER_WOT_DEPTH = "X-Ptxt-Wot-Depth";

function loggedOutWotEnabledForTransport() {
  return true;
}

function loggedOutWotDepthForTransport() {
  return String(loggedOutWebOfTrustDepthPref());
}

const LOGIN_METHOD_META = {
  readonly: { label: "Npub Login", canSign: false, readOnly: true, needsExtension: false },
  nip07: { label: "Browser Extension", canSign: true, readOnly: false, needsExtension: true },
  yolo: { label: "Nsec Login", canSign: true, readOnly: false, needsExtension: false },
  ephemeral: { label: "Sign up", canSign: true, readOnly: false, needsExtension: false },
};

const PERSISTED_SIGNING_METHODS = new Set(["yolo", "ephemeral"]);

let sessionCacheRaw = null;
let sessionCacheValue = {};

function invalidateSessionCache() {
  sessionCacheRaw = null;
  sessionCacheValue = {};
}

export function shortPubkey(pubkey) {
  if (!pubkey) return "";
  if (pubkey.length <= 12) return pubkey;
  return `${pubkey.slice(0, 8)}…${pubkey.slice(-4)}`;
}

/** Shared with settings publish: mirrors server `nostrx.DisplayName` (display_name, then name, then short hex). */
export function sessionAuthorLabelFromMetadata(meta, pubkeyHex) {
  const display = String(meta?.display_name ?? "").trim();
  const name = String(meta?.name ?? "").trim();
  if (display) return display;
  if (name) return name;
  return shortPubkey(pubkeyHex);
}

let viewerProfileLabelFetch = null;
/** When relay metadata returned no display_name/name for this pubkey, skip repeat fetches until pubkey changes. */
let viewerProfileEmptyResultPubkey = "";
let nip07RelaySyncPubkey = "";
let nip07RelaySyncPromise = null;

export function getSession() {
  const raw = localStorage.getItem(KEY) || "";
  if (raw === sessionCacheRaw) return sessionCacheValue;
  try {
    sessionCacheValue = normalizeSessionState(JSON.parse(raw || "{}"));
  } catch {
    sessionCacheValue = {};
  }
  sessionCacheRaw = raw;
  return sessionCacheValue;
}

export function setSession(session) {
  const normalized = normalizeSessionState(session);
  const current = getSession();
  const keys = [...new Set([...Object.keys(current), ...Object.keys(normalized)])].sort();
  if (keys.every((key) => JSON.stringify(current[key]) === JSON.stringify(normalized[key]))) {
    return current;
  }
  clearBootstrapPendingIfViewerChanged(normalized.pubkey);
  localStorage.setItem(KEY, JSON.stringify(normalized));
  syncStoredSigningAccount(normalized);
  invalidateSessionCache();
  window.dispatchEvent(new CustomEvent("ptxt:session", { detail: normalized }));
  return normalized;
}

export function clearSession() {
  clearBootstrapPending();
  sessionStorage.removeItem(SESSION_NSEC_KEY);
  localStorage.removeItem(KEY);
  localStorage.removeItem(NIP07_RELAY_SYNC_KEY);
  nip07RelaySyncPubkey = "";
  nip07RelaySyncPromise = null;
  invalidateSessionCache();
  window.dispatchEvent(new CustomEvent("ptxt:session", { detail: {} }));
}

export function normalizedPubkey(session) {
  const s = session ?? getSession();
  return s?.pubkey ?? "";
}

function readLocalStorageString(key) {
  try {
    const raw = localStorage.getItem(key);
    return raw == null ? "" : String(raw).trim();
  } catch {
    return "";
  }
}

function storedWotSeed() {
  if (!normalizedPubkey()) return "";
  return readLocalStorageString(WOT_SEED_KEY);
}

function storedSortForPath(pathname) {
  if (pathname === "/" || pathname === "/feed") {
    return readLocalStorageString(FEED_SORT_KEY);
  }
  if (pathname === "/reads") {
    return readLocalStorageString(READS_SORT_KEY);
  }
  return "";
}

function inputPathname(input) {
  try {
    if (typeof input === "string") return new URL(input, window.location.origin).pathname;
    if (input instanceof URL) return input.pathname;
    if (input && typeof input === "object" && typeof input.url === "string") {
      return new URL(input.url, window.location.origin).pathname;
    }
  } catch {
    return "";
  }
  return "";
}

/** Keep legacy viewer-pref URLs authoritative when fetch transport adds headers. */
export function applyViewerQueryOverrides(headers, input) {
  let url = null;
  try {
    if (typeof input === "string") url = new URL(input, window.location.origin);
    else if (input instanceof URL) url = input;
    else if (input && typeof input === "object" && typeof input.url === "string") {
      url = new URL(input.url, window.location.origin);
    }
  } catch {
    return headers;
  }
  if (!url) return headers;
  const overrides = [
    ["pubkey", HEADER_VIEWER_PUBKEY],
    ["seed_pubkey", HEADER_WOT_SEED],
    ["sort", HEADER_FEED_SORT],
    ["tf", HEADER_FEED_TRENDING_TF],
    ["reads_tf", HEADER_READS_TRENDING_TF],
    ["wot", HEADER_WOT_ENABLED],
    ["wot_depth", HEADER_WOT_DEPTH],
  ];
  for (const [queryKey, header] of overrides) {
    if (url.searchParams.has(queryKey)) headers.set(header, url.searchParams.get(queryKey) || "");
  }
  const relayValues = [...url.searchParams.getAll("relays"), ...url.searchParams.getAll("relay")]
    .map((value) => value.trim())
    .filter(Boolean);
  if (relayValues.length) headers.set(HEADER_RELAYS, relayValues.join(","));
  return headers;
}

/** Call after `/api/events` 200 so this tab's fetches bust CDN for ~5m. */
export function recordPublishedAt(now = Date.now()) {
	if (desktopModeEnabled()) return;
  try {
    localStorage.setItem(RECENT_PUBLISH_KEY, String(now));
  } catch {
    // private mode / quota — no bust; staleness bounded by origin s-maxage
  }
  publishBustToken = String(now);
  publishBustExpires = now + RECENT_PUBLISH_WINDOW_MS;
}

function recentPublishToken() {
  const now = Date.now();
  if (publishBustToken && now < publishBustExpires) return publishBustToken;
  publishBustToken = "";
  publishBustExpires = 0;
  try {
    const raw = Number.parseInt(localStorage.getItem(RECENT_PUBLISH_KEY) || "", 10);
    if (!Number.isFinite(raw) || now - raw > RECENT_PUBLISH_WINDOW_MS) return "";
    publishBustToken = String(raw);
    publishBustExpires = raw + RECENT_PUBLISH_WINDOW_MS;
    return publishBustToken;
  } catch {
    return "";
  }
}

function shouldCacheBustPath(pathname) {
  if (!pathname) return false;
  return CACHE_BUST_PATH_PREFIXES.some((prefix) => pathname.startsWith(prefix));
}

function urlWithCacheBust(input, token) {
  if (!token) return input;
  try {
    if (input instanceof Request) {
      const u = new URL(input.url, window.location.origin);
      u.searchParams.set("_", token);
      return new Request(u, {
        method: input.method,
        headers: input.headers,
        mode: input.mode,
        credentials: input.credentials,
        cache: input.cache,
        redirect: input.redirect,
        referrer: input.referrer,
        referrerPolicy: input.referrerPolicy,
        integrity: input.integrity,
        keepalive: input.keepalive,
        signal: input.signal,
      });
    }
    let url = null;
    if (typeof input === "string") {
      url = new URL(input, window.location.origin);
    } else if (input instanceof URL) {
      url = new URL(input.href);
    }
    if (!url) return input;
    url.searchParams.set("_", token);
    if (typeof input === "string") {
      const isAbsolute = /^https?:\/\//i.test(input) || input.startsWith("//");
      return isAbsolute ? url.toString() : `${url.pathname}${url.search}${url.hash}`;
    }
    return url;
  } catch {
    return input;
  }
}

// sessionHeaders attaches the X-Ptxt-* viewer headers to `extra` and returns a
// `Headers` object suitable to drop into a fetch init. Pubkey comes from the
// local session; the remaining prefs come from localStorage. Each header is
// only set when the user has an explicit stored value, so unset prefs fall
// through to the server's defaults (matching the prior URL-less behavior).
//
// `requestPath` is the request URL's pathname (used to disambiguate the
// per-path X-Ptxt-Sort source between feed and reads). Pass "" when unknown.
export function sessionHeaders(extra, requestPath = "") {
  const headers = new Headers(extra || {});
  const pubkey = normalizedPubkey();
  if (pubkey) {
    headers.set(HEADER_VIEWER_PUBKEY, pubkey);
  } else {
    applyDefaultViewerPrefsIfUnset();
    headers.set(HEADER_WOT_ENABLED, loggedOutWotEnabledForTransport() ? "1" : "0");
    headers.set(HEADER_WOT_DEPTH, loggedOutWotDepthForTransport());
    headers.set(HEADER_WOT_SEED, DEFAULT_LOGGED_OUT_WOT_SEED_NPUB);
  }
  const relays = relayParam();
  const existingRelays = headers.get(HEADER_RELAYS);
  if (relays || existingRelays) {
    headers.set(HEADER_RELAYS, [existingRelays, relays].filter(Boolean).join(","));
  }
  const sort = storedSortForPath(requestPath);
  if (sort) headers.set(HEADER_FEED_SORT, sort);
  const tf = readLocalStorageString(TRENDING_TF_KEY);
  if (tf) headers.set(HEADER_FEED_TRENDING_TF, tf);
  const readsTf = readLocalStorageString(READS_TRENDING_TF_KEY);
  if (readsTf) headers.set(HEADER_READS_TRENDING_TF, readsTf);
  if (pubkey) {
    headers.set(HEADER_WOT_ENABLED, "1");
    const wotDepth = readLocalStorageString(WEB_OF_TRUST_DEPTH_KEY);
    if (wotDepth) headers.set(HEADER_WOT_DEPTH, wotDepth);
    const seed = storedWotSeed();
    if (seed) headers.set(HEADER_WOT_SEED, seed);
  }
  return headers;
}

// fetchWithSession wraps `fetch` so every request carries the viewer identity
// and view preferences as X-Ptxt-* request headers instead of URL params. Pass
// a string, URL, or Request as `input`; `init` follows the standard fetch
// shape.
//
// For GET/HEAD on `/thread*`, `/u/*`, `/e/*`, appends `?_=<publishMs>` while
// the recent-publish window is open (publisher-only CDN key split).
export function fetchWithSession(input, init) {
  const baseInit = init || {};
  const pathname = inputPathname(input);
  const headers = sessionHeaders(baseInit.headers, pathname);
  applyViewerQueryOverrides(headers, input);
  let target = input;
  const methodSource = input instanceof Request ? input.method : baseInit.method;
  const method = String(methodSource || "GET").toUpperCase();
  const desktop = desktopModeEnabled();
	if (!desktop && (method === "GET" || method === "HEAD") && shouldCacheBustPath(pathname)) {
    const token = recentPublishToken();
    if (token) target = urlWithCacheBust(input, token);
  }
  const desktopNoStore = desktop && (method === "GET" || method === "HEAD") &&
    !pathname.startsWith("/static/") && !pathname.startsWith("/avatar/");
  return fetch(target, {
    ...baseInit,
    ...(desktopNoStore && baseInit.cache === undefined ? { cache: "no-store" } : {}),
    headers,
  });
}

// Routes that respect the stored Web-of-Trust preference. Keeping the
// canonical list here avoids drift between WoT URL rewriting and other
// route-aware helpers.
export const FEED_LIKE_PATHS = new Set(["/", "/feed", "/reads", "/notifications"]);
export function isFeedLikePath(pathname) {
  return FEED_LIKE_PATHS.has(pathname);
}

/** True when the address bar may carry legacy viewer-pref query params to scrub. */
export function shouldSyncViewerPrefLocation(pathname) {
  return isFeedLikePath(pathname) || pathname === "/settings";
}

export function loginMethodMeta(method) {
  return LOGIN_METHOD_META[String(method || "").toLowerCase()] || {
    label: "Logged in",
    canSign: false,
    readOnly: false,
    needsExtension: false,
  };
}

export function loginMethodLabel(session = getSession()) {
  return loginMethodMeta(session.method).label;
}

export function loginCapabilities(session = getSession()) {
  const hasSessionSecret = Boolean(getSessionSecretNsec(session));
  return {
    ...loginMethodMeta(session.method),
    method: session.method || "",
    isLoggedIn: Boolean(session.pubkey),
    hasSessionSecret,
  };
}

export function recentSigningAccounts() {
  return loadSigningAccounts();
}

export function persistSigningAccount(session, nsec) {
  const normalized = normalizeSessionState(session);
  const pubkey = normalizedPubkey(normalized);
  const secret = String(nsec || "").trim();
  if (!pubkey || !secret || !PERSISTED_SIGNING_METHODS.has(normalized.method)) return [];
  let accounts = loadSigningAccounts().filter((account) => account.pubkey !== pubkey);
  accounts.unshift({
    pubkey,
    npub: String(normalized.npub || "").trim(),
    method: normalized.method,
    lastUsedAt: Date.now(),
    profileLabel: String(normalized.profileLabel || "").trim(),
    picture: String(normalized.picture || "").trim(),
    nsec: secret,
  });
  accounts = accounts.slice(0, MAX_SIGNING_ACCOUNTS);
  saveSigningAccounts(accounts);
  try {
    sessionStorage.setItem(SESSION_NSEC_KEY, secret);
  } catch {
    // Best-effort only; signer reads from persistent storage on reload.
  }
  return accounts;
}

export function removeStoredSigningAccount(pubkey) {
  const want = normalizePubkey(pubkey);
  if (!want) return recentSigningAccounts();
  const next = loadSigningAccounts().filter((account) => account.pubkey !== want);
  saveSigningAccounts(next);
  const current = getSession();
  if (normalizePubkey(current.pubkey) === want && PERSISTED_SIGNING_METHODS.has(current.method)) {
    const replacement = next[0];
    if (replacement) switchToStoredSigningAccount(replacement.pubkey);
    else clearSession();
  }
  return next;
}

export function switchToStoredSigningAccount(pubkey) {
  const want = normalizePubkey(pubkey);
  if (!want) throw new Error("Missing account pubkey.");
  const account = loadSigningAccounts().find((entry) => entry.pubkey === want);
  if (!account?.nsec) throw new Error("That signing key is no longer available in this browser.");
  sessionStorage.setItem(SESSION_NSEC_KEY, account.nsec);
  const nextSession = {
    method: account.method,
    pubkey: account.pubkey,
    npub: account.npub,
    profileLabel: account.profileLabel,
    picture: account.picture,
  };
  setSession(nextSession);
  markBootstrapPending(nextSession.pubkey);
  touchStoredSigningAccount(nextSession);
  return nextSession;
}

export function getSessionSecretNsec(session = getSession()) {
  try {
    const raw = String(sessionStorage.getItem(SESSION_NSEC_KEY) || "").trim();
    if (raw) return raw;
  } catch {
    // Fall back to persisted account storage below.
  }
  const normalized = normalizeSessionState(session);
  const pubkey = normalizedPubkey(normalized);
  if (!pubkey || !PERSISTED_SIGNING_METHODS.has(normalized.method)) return "";
  const account = loadSigningAccounts().find((entry) => entry.pubkey === pubkey);
  const secret = String(account?.nsec || "").trim();
  if (!secret) return "";
  try {
    sessionStorage.setItem(SESSION_NSEC_KEY, secret);
  } catch {
    // Still return the recovered secret for callers that can use it immediately.
  }
  touchStoredSigningAccount(normalized);
  return secret;
}

export async function clearSessionScopedCaches() {
  const { clearLegacyRouteRecords } = await import("./client-store.js");
  void clearLegacyRouteRecords().catch(() => {});
}

export function updateStoredSigningAccountProfile(pubkey, profile = {}) {
  const want = normalizePubkey(pubkey);
  if (!want) return [];
  const accounts = loadSigningAccounts().map((account) => {
    if (account.pubkey !== want) return account;
    const profileLabel = sessionAuthorLabelFromMetadata(profile, want);
    return {
      ...account,
      profileLabel: String(profileLabel || account.profileLabel || "").trim(),
      picture: String(profile?.avatar_url || profile?.picture || account.picture || "").trim(),
    };
  });
  saveSigningAccounts(accounts);
  const current = getSession();
  if (normalizePubkey(current.pubkey) === want) {
    setSession({
      ...current,
      profileLabel: sessionAuthorLabelFromMetadata(profile, want),
      picture: String(profile?.avatar_url || profile?.picture || current.picture || "").trim(),
    });
  }
  return accounts;
}

export function sessionFeedURL() {
  return "/";
}

export function sessionReadsURL() {
  return "/reads";
}

export function selectedRelays() {
  return relayConfigUserRelayURLs(loadRelayConfig());
}

export function saveSelectedRelays(relays) {
  const current = loadRelayConfig();
  const next = {
    ...current,
    useUserRelays: true,
    userRelayMetadata: {
      ...current.userRelayMetadata,
      relays: [...new Set(relays)]
        .map((relay) => normalizeRelayURL(relay))
        .filter(Boolean)
        .slice(0, 8)
        .map((url) => ({ url, read: true, write: true })),
    },
  };
  saveRelayConfig(next);
}

export function relayParam() {
  const config = loadRelayConfig();
  const relays = [...new Set([...effectiveReadRelays(config), ...effectiveWriteRelays(config)])];
  return relays.length ? relays.join(",") : "";
}

export function relayEntriesFromNIP07Relays(raw) {
  const entries = raw instanceof Map
    ? [...raw.entries()]
    : Object.entries(raw && typeof raw === "object" ? raw : {});
  const out = [];
  const seen = new Set();
  for (const [url, value] of entries) {
    const relay = normalizeRelayURL(url);
    if (!relay || seen.has(relay)) continue;
    seen.add(relay);
    const meta = value && typeof value === "object" ? value : {};
    const read = meta.read !== false;
    const write = meta.write !== false;
    out.push({ url: relay, read, write });
    if (out.length >= 8) break;
  }
  return out;
}

function storedNIP07RelaySyncPubkey() {
  try {
    return normalizePubkey(localStorage.getItem(NIP07_RELAY_SYNC_KEY) || "");
  } catch {
    return "";
  }
}

function markNIP07RelaySyncAttempted(pubkey) {
  const pk = normalizePubkey(pubkey);
  if (!pk) return;
  nip07RelaySyncPubkey = pk;
  try {
    localStorage.setItem(NIP07_RELAY_SYNC_KEY, pk);
  } catch {
    // In-memory guard still prevents repeat prompts in this tab.
  }
}

export async function syncNIP07RelayConfigFromExtension(options = {}) {
  const force = options?.force === true;
  const session = getSession();
  const pubkey = normalizePubkey(options?.pubkey || session.pubkey);
  if (!force && pubkey && (nip07RelaySyncPubkey === pubkey || storedNIP07RelaySyncPubkey() === pubkey)) {
    nip07RelaySyncPubkey = pubkey;
    return loadRelayConfig();
  }
  if (!force && pubkey && nip07RelaySyncPromise) return nip07RelaySyncPromise;
  if (typeof window === "undefined" || !window.nostr?.getRelays) return loadRelayConfig();
  markNIP07RelaySyncAttempted(pubkey);
  const syncPromise = (async () => {
    let extensionRelays = [];
    try {
      extensionRelays = relayEntriesFromNIP07Relays(await window.nostr.getRelays());
    } catch {
      return loadRelayConfig();
    }
    if (extensionRelays.length === 0) return loadRelayConfig();
    const current = loadRelayConfig();
    const merged = [];
    const byURL = new Map();
    const add = (entry) => {
      const relay = normalizeRelayURL(entry?.url || "");
      if (!relay) return;
      const normalized = {
        url: relay,
        read: entry?.read !== false,
        write: entry?.write !== false,
      };
      const existing = byURL.get(relay);
      if (existing) {
        existing.read = existing.read || normalized.read;
        existing.write = existing.write || normalized.write;
        return;
      }
      byURL.set(relay, normalized);
      merged.push(normalized);
    };
    (current.userRelayMetadata?.relays || []).forEach(add);
    extensionRelays.forEach(add);
    return saveRelayConfig({
      ...current,
      useUserRelays: true,
      userRelayMetadata: {
        ...current.userRelayMetadata,
        relays: merged.slice(0, 8),
        updatedAt: current.userRelayMetadata?.updatedAt || 0,
      },
    });
  })();
  if (!force && pubkey) nip07RelaySyncPromise = syncPromise;
  try {
    return await syncPromise;
  } finally {
    if (nip07RelaySyncPromise === syncPromise) nip07RelaySyncPromise = null;
  }
}

export function normalizeRelayURL(raw) {
  const value = String(raw || "").trim().replace(/\/+$/, "");
  if (!value) return "";
  if (!value.startsWith("ws://") && !value.startsWith("wss://")) return "";
  return value;
}

/**
 * No-op kept for backwards compatibility. Relay selection now flows via the
 * `X-Ptxt-Relays` header, so client-built URLs no longer encode `?relays=`.
 * Any stale relay params on the input are stripped to keep the address bar
 * clean as routes that pre-date this refactor flow through.
 */
export function withRelayParams(href) {
  const url = new URL(href, window.location.origin);
  applyRelayParamsToURL(url);
  return `${url.pathname}${url.search}${url.hash}`;
}

export function updateSessionLinks() {
  const session = getSession();
  const pubkey = normalizedPubkey(session);
  if (!pubkey) viewerProfileEmptyResultPubkey = "";
  const methodLabel = loginMethodLabel(session);
  const short = pubkey ? shortPubkey(pubkey) : "";
  const sessionProfileLabel = String(session.profileLabel || "").trim();
  const displayLabel = pubkey ? sessionProfileLabel || short : "Guest";
  const feedURL = sessionFeedURL();
  const readsURL = sessionReadsURL();
  document.querySelectorAll("[data-session-feed-link]").forEach((link) => {
    link.href = feedURL;
    link.hidden = !pubkey;
  });
  document.querySelectorAll("[data-session-reads-link]").forEach((link) => {
    link.href = readsURL;
  });
  document.querySelectorAll("[data-feed-home]").forEach((link) => {
    link.href = feedURL;
  });
  document.querySelectorAll("[data-session-user-link]").forEach((link) => {
    link.href = pubkey ? profilePath(pubkey) : "/login";
    link.hidden = false;
    if (link instanceof HTMLAnchorElement) {
      link.setAttribute("aria-label", pubkey ? "View profile" : "Log in");
    }
  });
  document.querySelectorAll("[data-session-bookmarks-link]").forEach((link) => {
    if (link instanceof HTMLAnchorElement) link.href = withRelayParams("/bookmarks");
  });
  document.querySelectorAll("[data-session-notifications-link]").forEach((link) => {
    if (link instanceof HTMLAnchorElement) link.href = withRelayParams("/notifications");
  });
  document.querySelectorAll("[data-session-label]").forEach((node) => {
    node.textContent = pubkey ? `Logged in via ${methodLabel} as ${displayLabel}` : "Not logged in";
  });
  document.querySelectorAll("[data-session-display-name]").forEach((node) => {
    node.textContent = displayLabel;
  });
  document.querySelectorAll("[data-session-cta]").forEach((node) => {
    if (pubkey) {
      node.hidden = true;
      return;
    }
    node.hidden = false;
    if (node instanceof HTMLAnchorElement) node.href = "/login";
  });
  document.querySelectorAll("[data-session-avatar-fallback]").forEach((node) => {
    node.hidden = !!pubkey;
  });
  document.querySelectorAll("[data-session-avatar]").forEach((node) => {
    if (!(node instanceof HTMLImageElement)) return;
    node.onerror = null;
    if (!pubkey) {
      node.hidden = true;
      delete node.dataset.ptxtAvatarPubkey;
      return;
    }
    node.hidden = false;
    const fallback = node.parentElement?.querySelector("[data-session-avatar-fallback]");
    if (fallback) fallback.hidden = true;
    node.onerror = () => {
      node.hidden = true;
      if (fallback) fallback.hidden = false;
    };
    const avatarURL = `/avatar/${encodeURIComponent(pubkey)}`;
    let canonicalAvatarURL = avatarURL;
    try {
      canonicalAvatarURL = new URL(avatarURL, window.location.origin).href;
    } catch {
      canonicalAvatarURL = avatarURL;
    }
    const needsSrcUpdate =
      node.dataset.ptxtAvatarPubkey !== pubkey || node.dataset.ptxtAvatarOriginalSrc !== canonicalAvatarURL;
    node.dataset.ptxtAvatarPubkey = pubkey;
    if (needsSrcUpdate) setAvatarImageSource(node, avatarURL);
    queueMicrotask(() => {
      if (node.complete && node.naturalWidth === 0 && node.currentSrc) {
        node.hidden = true;
        if (fallback) fallback.hidden = false;
      }
    });
  });
  document.querySelectorAll("[data-session-user-copy]").forEach((node) => {
    node.hidden = !pubkey;
  });
  document.querySelectorAll("[data-session-logout-wrap]").forEach((node) => {
    node.hidden = !pubkey;
  });
  document.querySelectorAll(".rail-user").forEach((node) => {
    node.dataset.loggedIn = pubkey ? "1" : "0";
  });
  document.querySelectorAll(".mobile-menu-header").forEach((node) => {
    node.dataset.loggedIn = pubkey ? "1" : "0";
  });
  document.querySelectorAll(".mobile-menu-profile").forEach((node) => {
    node.dataset.loggedIn = pubkey ? "1" : "0";
  });
  document.querySelectorAll("[data-profile-edit-section]").forEach((node) => {
    node.hidden = !pubkey;
  });
  document.querySelectorAll("[data-profile-edit-guest-note]").forEach((node) => {
    node.hidden = !!pubkey;
  });
  document.querySelectorAll("[data-profile-actions]").forEach((node) => {
    const profilePubkey = String(node.getAttribute("data-profile-pubkey") || "");
    const isOwnProfile = Boolean(pubkey) && profilePubkey === pubkey;
    const followMute = node.querySelector("[data-profile-follow-mute]");
    if (followMute) followMute.hidden = isOwnProfile;
    const editLink = node.querySelector("[data-own-profile-edit]");
    if (editLink) editLink.hidden = !isOwnProfile;
    const logoutButton = node.querySelector("[data-own-profile-logout]");
    if (logoutButton) logoutButton.hidden = !isOwnProfile;
  });
  syncProfileFollowGuestAria(document);
  if (pubkey && session.method === "nip07" && nip07RelaySyncPubkey !== pubkey) {
    nip07RelaySyncPubkey = pubkey;
    void syncNIP07RelayConfigFromExtension().then(() => {
      if (!String(getSession().profileLabel || "").trim()) {
        void fetchAndPersistViewerProfileLabel(pubkey);
      }
    });
  }
  if (pubkey && !sessionProfileLabel && viewerProfileEmptyResultPubkey !== pubkey) {
    void fetchAndPersistViewerProfileLabel(pubkey);
  }
}

async function fetchAndPersistViewerProfileLabel(expectedPubkey) {
  const wantPub = normalizePubkey(expectedPubkey);
  if (viewerProfileLabelFetch) {
    await viewerProfileLabelFetch.catch(() => {});
    if (String(getSession().profileLabel || "").trim()) return;
    if (viewerProfileEmptyResultPubkey === wantPub) return;
  }
  if (viewerProfileEmptyResultPubkey === wantPub) return;

	const promise = (async () => {
		try {
			const { fetchViewerProfile } = await import("./relay-reads.js");
			const data = await fetchViewerProfile(wantPub);
      const rowPub = normalizePubkey(data?.pubkey);
      if (!rowPub || rowPub !== wantPub) return;
      if (normalizedPubkey() !== rowPub) return;
      const display = String(data.display_name ?? "").trim();
      const name = String(data.name ?? "").trim();
      const picture = String(data.avatar_url ?? data.picture ?? "").trim();
      if (!display && !name) {
        viewerProfileEmptyResultPubkey = rowPub;
        return;
      }
      const label = display || name;
      const current = getSession();
      if (normalizePubkey(current.pubkey) !== rowPub) return;
      if (!String(current.profileLabel || "").trim() || !String(current.picture || "").trim()) {
        setSession({ ...current, profileLabel: label, picture });
      }
      updateStoredSigningAccountProfile(rowPub, data);
    } catch {
      // keep short-hex fallback; allow retry on next navigation
    } finally {
      if (viewerProfileLabelFetch === promise) viewerProfileLabelFetch = null;
    }
  })();
  viewerProfileLabelFetch = promise;
  await promise;
}

/** Profile follow guest chrome; also invoked from `updateSessionLinks` and after client subtree inject. */
export function syncProfileFollowGuestAria(root = document) {
  const guest = !normalizedPubkey();
  root.querySelectorAll("[data-profile-actions]").forEach((node) => {
    const followToggle = node.querySelector("[data-profile-follow-mute] [data-follow-toggle]");
    if (!(followToggle instanceof HTMLButtonElement)) return;
    if (guest) followToggle.setAttribute("aria-disabled", "true");
    else followToggle.removeAttribute("aria-disabled");
  });
}

export function updateRelayAwareLinks() {
  document.querySelectorAll("a[data-relay-aware][href^='/']").forEach((link) => {
    link.dataset.baseHref ||= link.getAttribute("href");
    link.href = withRelayParams(link.dataset.baseHref);
  });
}

if (typeof window !== "undefined" && typeof document !== "undefined") {
  updateSessionLinks();
  updateRelayAwareLinks();

  window.addEventListener("ptxt:session", updateSessionLinks);
  window.addEventListener("ptxt:relays", updateRelayAwareLinks);
  window.addEventListener("storage", (event) => {
    if (event.key === KEY) {
      invalidateSessionCache();
      updateSessionLinks();
    }
    if (event.key === RELAY_CONFIG_KEY) updateRelayAwareLinks();
    if (event.key === SIGNING_ACCOUNTS_KEY) {
      window.dispatchEvent(new CustomEvent("ptxt:signing-accounts", { detail: recentSigningAccounts() }));
    }
  });

  document.addEventListener("submit", (event) => {
    // Strip any stale hidden `relays` input from GET forms so the address bar
    // stays clean after submission. Selected relays now travel as
    // X-Ptxt-Relays via fetchWithSession, so we no longer need to thread them
    // through form actions.
    const form = event.target.closest("form[method='get']");
    if (!form) return;
    form.querySelector("input[name='relays']")?.remove();
  });

  document.addEventListener("click", (event) => {
    const button = event.target.closest("[data-logout]");
    if (!button) return;
    event.preventDefault();
    clearSession();
    const redirect = button.getAttribute("data-logout-redirect");
    if (redirect) {
      window.location.href = withRelayParams(redirect);
    }
  });

}

function normalizeSessionState(value) {
  if (!value || typeof value !== "object") return {};
  const method = String(value.method || "").toLowerCase();
  if (method === "nip46") return {};
  const meta = loginMethodMeta(method);
  const pubkey = normalizePubkey(value.pubkey);
  const npub = String(value.npub || "").trim();
  if (!method && !pubkey && !npub) return {};
  let profileLabel = String(value.profileLabel || "").trim();
  let picture = String(value.picture || "").trim();
  if (profileLabel.length > 128) profileLabel = profileLabel.slice(0, 128);
  if (!pubkey) profileLabel = "";
  if (picture.length > 2048) picture = picture.slice(0, 2048);
  const out = {
    ...value,
    method,
    pubkey,
    npub,
    canSign: Boolean(value.canSign ?? meta.canSign),
    readOnly: Boolean(value.readOnly ?? meta.readOnly),
    needsExtension: Boolean(value.needsExtension ?? meta.needsExtension),
  };
  delete out.bunker;
  delete out.needsRemoteSigner;
  if (profileLabel) out.profileLabel = profileLabel;
  else delete out.profileLabel;
  if (picture) out.picture = picture;
  else delete out.picture;
  return out;
}

export function normalizePubkey(pubkey) {
  const value = String(pubkey || "").trim();
  if (!value) return "";
  if (/^[0-9a-fA-F]{64}$/.test(value)) return value.toLowerCase();
  if (value.toLowerCase().startsWith("npub") && value.length > 4) {
    try {
      const { data } = nip19.decode(value);
      if (typeof data === "string" && /^[0-9a-fA-F]{64}$/.test(data)) return data.toLowerCase();
    } catch {
      // leave as-is
    }
  }
  return value;
}

function normalizeStoredSigningAccount(value) {
  if (!value || typeof value !== "object") return null;
  const method = String(value.method || "").toLowerCase();
  if (!PERSISTED_SIGNING_METHODS.has(method)) return null;
  const pubkey = normalizePubkey(value.pubkey);
  const npub = String(value.npub || "").trim();
  const nsec = String(value.nsec || "").trim();
  if (!pubkey || !npub || !nsec) return null;
  let profileLabel = String(value.profileLabel || "").trim();
  let picture = String(value.picture || "").trim();
  if (profileLabel.length > 128) profileLabel = profileLabel.slice(0, 128);
  if (picture.length > 2048) picture = picture.slice(0, 2048);
  const lastUsedAt = Number(value.lastUsedAt);
  return {
    pubkey,
    npub,
    method,
    lastUsedAt: Number.isFinite(lastUsedAt) ? lastUsedAt : 0,
    profileLabel,
    picture,
    nsec,
  };
}

function loadSigningAccounts() {
  try {
    const parsed = JSON.parse(localStorage.getItem(SIGNING_ACCOUNTS_KEY) || "[]");
    if (!Array.isArray(parsed)) return [];
    return parsed
      .map(normalizeStoredSigningAccount)
      .filter(Boolean)
      .sort((a, b) => b.lastUsedAt - a.lastUsedAt)
      .slice(0, MAX_SIGNING_ACCOUNTS);
  } catch {
    return [];
  }
}

function saveSigningAccounts(accounts) {
  localStorage.setItem(SIGNING_ACCOUNTS_KEY, JSON.stringify(accounts));
  window.dispatchEvent(new CustomEvent("ptxt:signing-accounts", { detail: accounts }));
}

function syncStoredSigningAccount(session) {
  const normalized = normalizeSessionState(session);
  const pubkey = normalizedPubkey(normalized);
  if (!pubkey || !PERSISTED_SIGNING_METHODS.has(normalized.method)) return;
  const secret = getSessionSecretNsec(normalized);
  if (!secret) return;
  persistSigningAccount(normalized, secret);
}

function touchStoredSigningAccount(session) {
  const normalized = normalizeSessionState(session);
  const pubkey = normalizedPubkey(normalized);
  if (!pubkey || !PERSISTED_SIGNING_METHODS.has(normalized.method)) return;
  const accounts = loadSigningAccounts();
  const current = accounts.find((entry) => entry.pubkey === pubkey);
  if (!current?.nsec) return;
  persistSigningAccount({
    ...normalized,
    npub: normalized.npub || current.npub,
    profileLabel: String(normalized.profileLabel || current.profileLabel || "").trim(),
    picture: String(normalized.picture || current.picture || "").trim(),
  }, current.nsec);
}
