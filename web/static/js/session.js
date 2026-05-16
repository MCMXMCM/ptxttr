import { nip19 } from "../lib/nostr-tools.js";

const KEY = "ptxt_nostr_session";
const RELAYS = "ptxt_relays";
// Viewer prefs live under sort-prefs' localStorage keys; we read them directly
// here (rather than importing the getters) to avoid a circular import
// (sort-prefs.js -> session.js). Keep these key names in sync with sort-prefs.js.
const WOT_SEED_KEY = "ptxt_wot_seed_pubkey";
const FEED_SORT_KEY = "ptxt_feed_sort";
const READS_SORT_KEY = "ptxt_reads_sort";
const TRENDING_TF_KEY = "ptxt_trending_tf";
const READS_TRENDING_TF_KEY = "ptxt_reads_trending_tf";
const WEB_OF_TRUST_ENABLED_KEY = "ptxt_wot_enabled";
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
const LOGIN_METHOD_META = {
  readonly: { label: "Npub Login", canSign: false, readOnly: true, needsExtension: false, needsRemoteSigner: false },
  nip07: { label: "Browser Extension", canSign: true, readOnly: false, needsExtension: true, needsRemoteSigner: false },
  yolo: { label: "Nsec Login", canSign: true, readOnly: false, needsExtension: false, needsRemoteSigner: false },
  ephemeral: { label: "Sign up", canSign: true, readOnly: false, needsExtension: false, needsRemoteSigner: false },
  nip46: { label: "Remote Signer", canSign: false, readOnly: false, needsExtension: false, needsRemoteSigner: true },
};

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
/** When `/api/profile` returned no display_name/name for this pubkey, skip repeat fetches until pubkey changes. */
let viewerProfileEmptyResultPubkey = "";

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
  localStorage.setItem(KEY, JSON.stringify(normalized));
  invalidateSessionCache();
  window.dispatchEvent(new CustomEvent("ptxt:session", { detail: normalized }));
}

export function clearSession() {
  sessionStorage.removeItem("ptxt_nsec");
  localStorage.removeItem(KEY);
  invalidateSessionCache();
  window.dispatchEvent(new CustomEvent("ptxt:session", { detail: {} }));
}

export function normalizedPubkey(session = getSession()) {
  return session.pubkey || "";
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

/** Call after `/api/events` 200 so this tab's fetches bust CDN for ~5m. */
export function recordPublishedAt(now = Date.now()) {
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
  if (pubkey) headers.set(HEADER_VIEWER_PUBKEY, pubkey);
  const seed = storedWotSeed();
  if (seed) headers.set(HEADER_WOT_SEED, seed);
  const relays = relayParam();
  if (relays) headers.set(HEADER_RELAYS, relays);
  const sort = storedSortForPath(requestPath);
  if (sort) headers.set(HEADER_FEED_SORT, sort);
  const tf = readLocalStorageString(TRENDING_TF_KEY);
  if (tf) headers.set(HEADER_FEED_TRENDING_TF, tf);
  const readsTf = readLocalStorageString(READS_TRENDING_TF_KEY);
  if (readsTf) headers.set(HEADER_READS_TRENDING_TF, readsTf);
  const wotRaw = readLocalStorageString(WEB_OF_TRUST_ENABLED_KEY);
  if (wotRaw) headers.set(HEADER_WOT_ENABLED, wotRaw);
  const wotDepth = readLocalStorageString(WEB_OF_TRUST_DEPTH_KEY);
  if (wotDepth) headers.set(HEADER_WOT_DEPTH, wotDepth);
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
  let target = input;
  const methodSource = input instanceof Request ? input.method : baseInit.method;
  const method = String(methodSource || "GET").toUpperCase();
  if ((method === "GET" || method === "HEAD") && shouldCacheBustPath(pathname)) {
    const token = recentPublishToken();
    if (token) target = urlWithCacheBust(input, token);
  }
  return fetch(target, { ...baseInit, headers });
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

/**
 * No-op kept for backwards compatibility with callers that still pass URLs
 * through this helper. Relays are now sent as the `X-Ptxt-Relays` header via
 * `fetchWithSession()`, so server-bound URLs no longer need a `relays` query
 * string. We additionally strip any stale `relays` / `relay` parameters so
 * routes that were called before this refactor do not leak the values back
 * into the address bar.
 */
export function applyRelayParamsToURL(url) {
  if (!url?.searchParams) return;
  url.searchParams.delete("relays");
  url.searchParams.delete("relay");
}

// Legacy viewer-pref keys that used to live on feed-like URLs. Fragment and
// SPA fetches must omit them; sessionHeaders() sends the values as X-Ptxt-*.
const VIEWER_PREF_QUERY_KEYS = ["pubkey", "seed_pubkey", "sort", "tf", "reads_tf", "wot", "wot_depth"];

/** Removes relay + legacy viewer-pref query keys from `url` in place. */
export function stripViewerPrefSearchParams(url) {
  if (!url?.searchParams) return;
  applyRelayParamsToURL(url);
  for (const k of VIEWER_PREF_QUERY_KEYS) {
    url.searchParams.delete(k);
  }
}

export function loginMethodMeta(method) {
  return LOGIN_METHOD_META[String(method || "").toLowerCase()] || {
    label: "Logged in",
    canSign: false,
    readOnly: false,
    needsExtension: false,
    needsRemoteSigner: false,
  };
}

export function loginMethodLabel(session = getSession()) {
  return loginMethodMeta(session.method).label;
}

export function loginCapabilities(session = getSession()) {
  return {
    ...loginMethodMeta(session.method),
    method: session.method || "",
    isLoggedIn: Boolean(session.pubkey),
    hasSessionSecret: Boolean(sessionStorage.getItem("ptxt_nsec")),
  };
}

export function sessionFeedURL() {
  return "/";
}

export function sessionReadsURL() {
  return "/reads";
}

export function selectedRelays() {
  try {
    return JSON.parse(localStorage.getItem(RELAYS) || "[]").filter(Boolean);
  } catch {
    return [];
  }
}

export function saveSelectedRelays(relays) {
  localStorage.setItem(RELAYS, JSON.stringify([...new Set(relays)].slice(0, 8)));
  window.dispatchEvent(new CustomEvent("ptxt:relays", { detail: selectedRelays() }));
}

export function relayParam() {
  const relays = selectedRelays();
  return relays.length ? relays.join(",") : "";
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
    link.href = pubkey ? `/u/${encodeURIComponent(pubkey)}` : "/login";
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
    const needsSrcUpdate = node.dataset.ptxtAvatarPubkey !== pubkey || node.getAttribute("src") !== avatarURL;
    node.dataset.ptxtAvatarPubkey = pubkey;
    if (needsSrcUpdate) node.src = avatarURL;
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
      const response = await fetchWithSession("/api/profile");
      if (!response.ok) return;
      const data = await response.json();
      const rowPub = normalizePubkey(data?.pubkey);
      if (!rowPub || rowPub !== wantPub) return;
      if (normalizedPubkey() !== rowPub) return;
      const display = String(data.display_name ?? "").trim();
      const name = String(data.name ?? "").trim();
      if (!display && !name) {
        viewerProfileEmptyResultPubkey = rowPub;
        return;
      }
      const label = display || name;
      const current = getSession();
      if (normalizePubkey(current.pubkey) !== rowPub) return;
      if (String(current.profileLabel || "").trim()) return;
      setSession({ ...current, profileLabel: label });
    } catch {
      // keep short-hex fallback; allow retry on next navigation
    } finally {
      if (viewerProfileLabelFetch === promise) viewerProfileLabelFetch = null;
    }
  })();
  viewerProfileLabelFetch = promise;
  await promise;
}

/** Profile follow guest chrome; also invoked from `updateSessionLinks` and after SPA subtree inject. */
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

updateSessionLinks();
updateRelayAwareLinks();

window.addEventListener("ptxt:session", updateSessionLinks);
window.addEventListener("ptxt:relays", updateRelayAwareLinks);
window.addEventListener("storage", (event) => {
  if (event.key === KEY) {
    invalidateSessionCache();
    updateSessionLinks();
  }
  if (event.key === RELAYS) updateRelayAwareLinks();
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

document.addEventListener("click", (event) => {
  const block = event.target.closest(".rail-user");
  if (!block || event.target.closest("a,button,input,select,textarea,label")) return;
  const pubkey = normalizedPubkey();
  if (!pubkey) return;
  navigateApp(`/u/${encodeURIComponent(pubkey)}`);
});

function navigateApp(href) {
  const target = withRelayParams(href);
  if (document.querySelector("[data-nav-root]")) {
    window.dispatchEvent(new CustomEvent("ptxt:navigate", { detail: { href: target } }));
    return;
  }
  window.location.href = target;
}

function normalizeSessionState(value) {
  if (!value || typeof value !== "object") return {};
  const method = String(value.method || "").toLowerCase();
  const meta = loginMethodMeta(method);
  const pubkey = normalizePubkey(value.pubkey);
  const npub = String(value.npub || "").trim();
  const bunker = String(value.bunker || "").trim();
  if (!method && !pubkey && !npub && !bunker) return {};
  let profileLabel = String(value.profileLabel || "").trim();
  if (profileLabel.length > 128) profileLabel = profileLabel.slice(0, 128);
  if (!pubkey) profileLabel = "";
  const out = {
    ...value,
    method,
    pubkey,
    npub,
    bunker,
    canSign: Boolean(value.canSign ?? meta.canSign),
    readOnly: Boolean(value.readOnly ?? meta.readOnly),
    needsExtension: Boolean(value.needsExtension ?? meta.needsExtension),
    needsRemoteSigner: Boolean(value.needsRemoteSigner ?? meta.needsRemoteSigner),
  };
  if (profileLabel) out.profileLabel = profileLabel;
  else delete out.profileLabel;
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
