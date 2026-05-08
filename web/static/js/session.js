import { nip19 } from "../lib/nostr-tools.js";

const KEY = "ptxt_nostr_session";
const RELAYS = "ptxt_relays";
const LOGIN_METHOD_META = {
  readonly: { label: "Npub Login", canSign: false, readOnly: true, needsExtension: false, needsRemoteSigner: false },
  nip07: { label: "Browser Extension", canSign: true, readOnly: false, needsExtension: true, needsRemoteSigner: false },
  yolo: { label: "Nsec Login", canSign: true, readOnly: false, needsExtension: false, needsRemoteSigner: false },
  ephemeral: { label: "Ephemeral Login", canSign: true, readOnly: false, needsExtension: false, needsRemoteSigner: false },
  nip46: { label: "Remote Signer", canSign: false, readOnly: false, needsExtension: false, needsRemoteSigner: true },
};

let sessionCacheRaw = null;
let sessionCacheValue = {};

function invalidateSessionCache() {
  sessionCacheRaw = null;
  sessionCacheValue = {};
}

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

// Routes that get a session-derived `pubkey` query param injected and that
// also respect the stored Web-of-Trust preference. Keeping the canonical
// list here avoids drift between session pubkey injection, WoT URL
// rewriting, and other route-aware helpers.
export const FEED_LIKE_PATHS = new Set(["/", "/feed", "/reads", "/notifications"]);
export function isFeedLikePath(pathname) {
  return FEED_LIKE_PATHS.has(pathname);
}

/** For feed-like routes, set `pubkey` from session when absent. */
export function ensureFeedURLHasSessionPubkey(url) {
  if (!isFeedLikePath(url.pathname)) return false;
  if (url.searchParams.get("pubkey")) return false;
  const pk = normalizedPubkey();
  if (!pk) return false;
  url.searchParams.set("pubkey", pk);
  return true;
}

/** Thread SSR and fragments need `pubkey` so reaction fill matches the session (same as feed). */
export function ensureThreadURLHasSessionPubkey(url) {
  if (!url.pathname.startsWith("/thread/")) return false;
  if (url.searchParams.get("pubkey")) return false;
  const pk = normalizedPubkey();
  if (!pk) return false;
  url.searchParams.set("pubkey", pk);
  return true;
}

/** Mutates `url`: current relay selection plus session pubkey on `/thread/*` paths. */
export function applyRelayAndThreadSessionToURL(url) {
  const relays = relayParam();
  if (relays) {
    url.searchParams.set("relays", relays);
  }
  ensureThreadURLHasSessionPubkey(url);
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

export function sessionFeedURL(session = getSession()) {
  const pubkey = normalizedPubkey(session);
  const url = pubkey ? `/?pubkey=${encodeURIComponent(pubkey)}` : "/";
  return withRelayParams(url);
}

export function sessionReadsURL(session = getSession()) {
  const pubkey = normalizedPubkey(session);
  const url = pubkey ? `/reads?pubkey=${encodeURIComponent(pubkey)}` : "/reads";
  return withRelayParams(url);
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

export function withRelayParams(href) {
  const url = new URL(href, window.location.origin);
  applyRelayAndThreadSessionToURL(url);
  return `${url.pathname}${url.search}${url.hash}`;
}

export function updateSessionLinks() {
  const session = getSession();
  const pubkey = normalizedPubkey(session);
  const methodLabel = loginMethodLabel(session);
  const short = pubkey ? shortPubkey(pubkey) : "";
  const feedURL = sessionFeedURL(session);
  const readsURL = sessionReadsURL(session);
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
    const base = pubkey ? `/bookmarks?pubkey=${encodeURIComponent(pubkey)}` : "/bookmarks";
    if (link instanceof HTMLAnchorElement) link.href = withRelayParams(base);
  });
  document.querySelectorAll("[data-session-notifications-link]").forEach((link) => {
    const base = pubkey ? `/notifications?pubkey=${encodeURIComponent(pubkey)}` : "/notifications";
    if (link instanceof HTMLAnchorElement) link.href = withRelayParams(base);
  });
  document.querySelectorAll("[data-session-label]").forEach((node) => {
    node.textContent = pubkey ? `Logged in via ${methodLabel} as ${short}` : "Not logged in";
  });
  document.querySelectorAll("[data-session-display-name]").forEach((node) => {
    node.textContent = pubkey ? shortPubkey(pubkey) : "Guest";
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
  document.querySelectorAll(".rail-user").forEach((node) => {
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
    const followButton = node.querySelector("[data-follow-toggle]");
    if (followButton) followButton.hidden = isOwnProfile;
    const editLink = node.querySelector("[data-own-profile-edit]");
    if (editLink) editLink.hidden = !isOwnProfile;
    const logoutButton = node.querySelector("[data-own-profile-logout]");
    if (logoutButton) logoutButton.hidden = !isOwnProfile;
  });
}

function shortPubkey(pubkey) {
  if (!pubkey) return "";
  if (pubkey.length <= 12) return pubkey;
  return `${pubkey.slice(0, 8)}…${pubkey.slice(-4)}`;
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
  const form = event.target.closest("form[method='get']");
  if (!form) return;
  const relays = relayParam();
  let input = form.querySelector("input[name='relays']");
  if (!relays) {
    input?.remove();
    return;
  }
  if (!input) {
    input = document.createElement("input");
    input.type = "hidden";
    input.name = "relays";
    form.append(input);
  }
  input.value = relays;
});

document.addEventListener("click", (event) => {
  const button = event.target.closest("[data-fill-session-pubkey]");
  if (!button) return;
  const input = document.querySelector("[data-pubkey-input]");
  if (input) input.value = normalizedPubkey();
});

document.addEventListener("click", (event) => {
  const button = event.target.closest("[data-load-session-feed]");
  if (!button) return;
  const pubkey = normalizedPubkey();
  if (!pubkey) {
    window.location.href = "/login";
    return;
  }
  window.location.href = sessionFeedURL();
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
  return {
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
}

function normalizePubkey(pubkey) {
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
