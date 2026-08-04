import {
  applyImmediateProfileShell,
  homeFeedSessionChanged,
  hydrateClientRoute,
  renderCachedThreadRoutePreview,
} from "../client-render.js";
import { relayHintsFromNoteElement } from "../dom-relay-hints.js";
import { initFeedLoadMore } from "../feed.js";
import { applyFeedHeadingMarkup, feedHeadingNeedsRefresh } from "../feed-heading.js";
import { dismissOpenMobileMenuForNavigation, wireAvatarImageFallbacks, initLayoutUI, syncMobileAppNavHeight } from "../layout.js";
import { initLoginPage } from "../login.js";
import { pubkeyFromProfilePath, routeKind, withRelays } from "../nav-routing.js";
import { refreshVisibleNoteProfiles, rememberVisibleNoteProfiles } from "../note-profiles.js";
import { initViewMore, interactiveSelector } from "../notes.js";
import { openImageViewer, refreshAsciiSync } from "../ascii.js";
import { initRetroLoaders, markRetroLoaderComplete, setRetroLoaderProgress } from "../retro-loader.js";
import {
  isThreadHydrateComplete,
  isThreadHydrateRenderable,
  isThreadHydrateResponseIncomplete,
  threadFocusNeedsFullHydrate,
  threadPathNoteID,
  threadServerHydrateHref,
} from "../thread-hydrate.js";
import { clearLegacyRouteRecords } from "../client-store.js";
import { putEvents } from "../event-store.js";
import { applyCarriedNoteExpansion } from "../note-expansion.js";
import {
  applyDestinationThreadTransition,
  clearThreadTransition,
  prepareThreadFocusTransition,
  prepareThreadTransition,
  runNoteViewTransition,
} from "../note-transition.js";
import { rememberProfiles } from "../profile-memory-cache.js";
import { rememberProfileRoutePreviewFromLink } from "../profile-route-preview.js";
import { bindProfileStatLinks } from "../profile-tabs.js";
import { bindProfileLazyTabs, syncRoutePolling } from "../route-polling.js";
import { clearThreadWarmCache } from "../thread-graph.js";
import { initRelaysPage } from "../relays.js";
import { fetchWithSession, normalizedPubkey, normalizeRelayURL, shortPubkey, updateRelayAwareLinks, updateSessionLinks } from "../session.js";
import { replaceRouteOutletHTML, routeOutletElement, routeScrollTop, scrollRouteToTop, setRouteScrollTop } from "../shell-swap.js";
import { initThreadPage, teardownThreadTreeConnector } from "../thread.js";
import { initThreadIntentWarm } from "../thread-intent-warm.js";
import { threadParentSkeletonMarkup } from "../shell.js";
import { parentID, rootIDForEvent } from "../thread-tags.js";
import { appBootstrap } from "./bootstrap.js";
import { renderShellForRoute } from "./route-shells.js";
import { setCurrentRoute, nextRouteRefreshToken } from "../navigation-route-state.js";

const main = document.querySelector("[data-nav-root]");
const routeCardSelector = ".note[data-ascii-select-href], .comment[data-ascii-select-href]";
const routeCardReferenceSelector = "[data-ascii-ref-select-href]";
const routeCardMediaNavigationSelector = ".note-media-drawer, .note-media-preview, [data-note-image-mount], .note-media-tile";
const routeCardLocalMediaActionSelector = "[data-media-grid-open], .note-media-image-tile, .note-media-more-tile";
const routeCardNativeMediaSelector = "video, audio";
const THREAD_PREVIEW_HANDOFF_KEY = "ptxt_document_thread_preview";
const ROUTE_SCROLL_STORAGE_PREFIX = "ptxt_document_route_scroll:";
const ROUTE_TOUCH_TAP_MAX_MOVE = 12;
const ROUTE_SNAPSHOT_LIMIT = 3;
const THREAD_SERVER_RENDER_RETRY_DELAYS_MS = [450, 1000, 1800, 3000];
const THREAD_PARTIAL_UPGRADE_RETRY_DELAYS_MS = [800, 1800, 3500, 7000];
let routeTouchStart = null;
let routeTouchHandled = null;
let routePopstateNavigation = false;
let threadPartialUpgradeGeneration = 0;
const routeSnapshots = new Map();

function currentURL() {
  return new URL(window.location.href);
}

function routeStorageHref(url) {
  return `${url.pathname}${url.search}${url.hash}`;
}

function routeSnapshotHref(route, url) {
  if (route === "feed" && (url.pathname === "/" || url.pathname === "/feed")) {
    return `/${url.search}${url.hash}`;
  }
  return routeStorageHref(url);
}

function routeScrollStorageKey(url) {
  return `${ROUTE_SCROLL_STORAGE_PREFIX}${routeStorageHref(url)}`;
}

function isBackForwardNavigation() {
  const entry = globalThis.performance?.getEntriesByType?.("navigation")?.[0];
  return entry?.type === "back_forward";
}

function routeOutletHasShell(root = main) {
  const outlet = routeOutletElement(root);
  if (!outlet) return false;
  return Boolean(outlet.querySelector(".feed-column, .right-rail, [data-profile-shell], [data-shell-main]"));
}

function routeOutletHasPendingThread(root = main) {
  return Boolean(root?.querySelector?.(".feed-column[data-thread-route-pending]"));
}

function visibleThreadSelectedNote(root, url) {
  const selectedID = threadPathNoteID(url?.toString?.() || window.location.href);
  if (!selectedID || !root?.querySelector) return null;
  const selected = root.querySelector(`#thread-focus #note-${CSS.escape(selectedID)}`);
  return selected instanceof HTMLElement ? selected : null;
}

function settleVisiblePartialThread(root, url) {
  if (!visibleThreadSelectedNote(root, url)) return false;
  const column = root.querySelector(".feed-column[data-thread-route-pending]");
  if (!(column instanceof HTMLElement)) return false;
  column.removeAttribute("data-thread-route-pending");
  column.dataset.threadRoutePartial = "1";
  root.querySelector("#thread-summary")?.replaceChildren?.();
  return true;
}

function renderCurrentRouteShell(route, url, { force = false } = {}) {
  if (!main || (!force && routeOutletHasShell(main))) return;
  const html = renderShellForRoute(route, url);
  if (!html) return;
  replaceRouteOutletPreservingProfiles(html);
  if (route === "profile") {
    applyImmediateProfileShell(main, pubkeyFromProfilePath(url.pathname));
  }
  initRetroLoaders(main);
}

function profilePostsRouteLoader() {
  const loader = main?.querySelector?.('[data-retro-loader-type="profile-posts"]');
  return loader instanceof HTMLElement ? loader : null;
}

function updateProfilePostsRouteLoader({ percent, statusMessage, summary } = {}) {
  const loader = profilePostsRouteLoader();
  if (!loader) return;
  setRetroLoaderProgress(loader, { percent, statusMessage, summary });
}

function mergeKnownProfileIdentity(nextHeader, currentHeader, pubkey) {
  if (!(nextHeader instanceof HTMLElement) || !(currentHeader instanceof HTMLElement)) return;
  const currentName = currentHeader.querySelector(".profile-display-name:not(.text-skeleton)");
  const nextName = nextHeader.querySelector(".profile-display-name");
  const nextLabel = String(nextName?.textContent || "").trim();
  if (
    currentName instanceof HTMLElement &&
    nextName instanceof HTMLElement &&
    (!nextLabel || nextLabel === shortPubkey(pubkey))
  ) {
    nextName.textContent = currentName.textContent || "";
    nextName.classList.remove("text-skeleton", "profile-skeleton-display-name");
  }
  const currentAvatar = currentHeader.querySelector(".profile-avatar-wrap > img.profile-avatar");
  const nextAvatarWrap = nextHeader.querySelector(".profile-avatar-wrap");
  if (
    currentAvatar instanceof HTMLImageElement &&
    nextAvatarWrap instanceof HTMLElement &&
    !nextAvatarWrap.querySelector("img.profile-avatar")
  ) {
    nextAvatarWrap.replaceChildren(currentAvatar.cloneNode(true));
  }
}

async function renderProfileHeaderIfAvailable(url) {
  if (!main || routeKind(url.pathname) !== "profile") return false;
  const headerURL = new URL(url.toString());
  headerURL.searchParams.set("fragment", "header");
  const response = await fetchWithSession(withRelays(routeHref(headerURL)), {
    headers: { Accept: "text/html" },
  }).catch(() => null);
  if (!response?.ok) return false;
  const html = (await response.text()).trim();
  if (!html || routeStorageHref(currentURL()) !== routeStorageHref(url)) return false;
  const template = document.createElement("template");
  template.innerHTML = html;
  const nextHeader = template.content.querySelector(".profile.profile-modern");
  const currentHeader = main.querySelector("#user-header");
  if (!(nextHeader instanceof HTMLElement) || !(currentHeader instanceof HTMLElement)) return false;
  const pubkey = pubkeyFromProfilePath(url.pathname);
  mergeKnownProfileIdentity(nextHeader, currentHeader, pubkey);
  currentHeader.replaceChildren(nextHeader);
  wireAvatarImageFallbacks(currentHeader);
  updateSessionLinks();
  updateRelayAwareLinks();
  bindProfileStatLinks();
  updateProfilePostsRouteLoader({
    percent: 28,
    summary: "Profile details are ready. Posts are still loading.",
    statusMessage: "profile details loaded; waiting for posts...",
  });
  return true;
}

function noteIDFromElement(node) {
  return String(node?.id || "").replace(/^note-/, "").toLowerCase();
}

function feedScrollAnchorFromElement(node) {
  const anchor = node?.closest?.("#feed .note[id], #feed .comment[id]");
  return anchor instanceof HTMLElement ? anchor : null;
}

function profileScrollAnchorFromElement(node) {
  const anchor = node?.closest?.("[data-profile-shell] .note[id], [data-profile-shell] .comment[id]");
  return anchor instanceof HTMLElement ? anchor : null;
}

function visibleFeedScrollAnchor() {
  const candidates = main?.querySelectorAll?.("#feed .note[id], #feed .comment[id]") || [];
  const viewportHeight = Math.max(1, window.innerHeight || document.documentElement?.clientHeight || 0);
  for (const node of candidates) {
    if (!(node instanceof HTMLElement)) continue;
    const rect = node.getBoundingClientRect();
    if (rect.bottom > 0 && rect.top < viewportHeight) return node;
  }
  return null;
}

function renderedRouteProfiles(outlet) {
  return rememberVisibleNoteProfiles(outlet);
}

function replaceRouteOutletPreservingProfiles(html) {
  if (!main) return;
  renderedRouteProfiles(routeOutletElement(main));
  replaceRouteOutletHTML(main, html);
  void refreshVisibleNoteProfiles(main);
}

function saveCurrentRouteSnapshot(url = currentURL()) {
  const route = routeKind(url.pathname);
  if ((route !== "feed" && route !== "profile") || !main) return;
  const outlet = routeOutletElement(main);
  if (!(outlet instanceof HTMLElement)) return;
  const routeCard = route === "profile"
    ? outlet.querySelector("[data-profile-shell] .note[id], [data-profile-shell] .comment[id]")
    : outlet.querySelector("#feed .note[id], #feed .comment[id]");
  if (!routeCard) return;
  const html = outlet.innerHTML;
  if (!html.trim()) return;
  const href = routeSnapshotHref(route, url);
  routeSnapshots.delete(href);
  routeSnapshots.set(href, {
    href,
    html,
    profiles: renderedRouteProfiles(outlet),
    savedAt: Date.now(),
  });
  while (routeSnapshots.size > ROUTE_SNAPSHOT_LIMIT) {
    const oldest = routeSnapshots.keys().next().value;
    routeSnapshots.delete(oldest);
  }
}

function restoreRouteSnapshot(route, url, { directFeedNavigation = false } = {}) {
  const canRestore = routePopstateNavigation || (directFeedNavigation && route === "feed");
  if (!canRestore || (route !== "feed" && route !== "profile") || !main) return false;
  const href = routeSnapshotHref(route, url);
  const snapshot = routeSnapshots.get(href);
  if (!snapshot?.html || Date.now() - Number(snapshot.savedAt || 0) > 30 * 60_000) return false;
  replaceRouteOutletPreservingProfiles(snapshot.html);
  if (snapshot.profiles && typeof snapshot.profiles === "object") {
    rememberProfiles(snapshot.profiles);
  }
  routeSnapshots.delete(href);
  routeSnapshots.set(href, snapshot);
  return true;
}

function saveCurrentRouteScroll(anchorHint = null) {
  const url = currentURL();
  const route = routeKind(url.pathname);
  if (route !== "feed" && route !== "profile") return;
  saveCurrentRouteSnapshot(url);
  const anchor = route === "profile"
    ? profileScrollAnchorFromElement(anchorHint)
    : feedScrollAnchorFromElement(anchorHint) || visibleFeedScrollAnchor();
  const rect = anchor?.getBoundingClientRect?.();
  try {
    sessionStorage.setItem(routeScrollStorageKey(url), JSON.stringify({
      href: routeStorageHref(url),
      route,
      scrollTop: routeScrollTop(main),
      anchorID: anchor?.id || "",
      anchorTop: Number.isFinite(rect?.top) ? rect.top : null,
      savedAt: Date.now(),
    }));
  } catch {
    // Best-effort same-tab scroll restoration when bfcache is unavailable.
  }
}

function readRouteScrollRestore(route, url, { pageCacheRestore = false } = {}) {
  if (
    (route !== "feed" && route !== "profile") ||
    (!pageCacheRestore && !routePopstateNavigation && !isBackForwardNavigation())
  ) return null;
  const key = routeScrollStorageKey(url);
  try {
    const payload = JSON.parse(sessionStorage.getItem(key) || "null");
    sessionStorage.removeItem(key);
    if (!payload || payload.href !== routeStorageHref(url)) return null;
    if (Date.now() - Number(payload.savedAt || 0) > 30 * 60_000) return null;
    return payload;
  } catch {
    return null;
  }
}

function restoreRouteScroll(payload) {
  if (!payload) return;
  const scrollTop = Math.max(0, Number(payload.scrollTop) || 0);
  const anchorID = String(payload.anchorID || "");
  const anchorTop = Number(payload.anchorTop);
  const apply = () => {
    setRouteScrollTop(main, scrollTop);
    if (!anchorID || !Number.isFinite(anchorTop)) return;
    const anchor = document.getElementById(anchorID);
    if (!(anchor instanceof HTMLElement)) return;
    const delta = anchor.getBoundingClientRect().top - anchorTop;
    if (Number.isFinite(delta) && Math.abs(delta) > 1) {
      setRouteScrollTop(main, routeScrollTop(main) + delta);
    }
  };
  apply();
  requestAnimationFrame(apply);
  window.setTimeout(apply, 80);
  window.setTimeout(apply, 240);
  window.setTimeout(apply, 600);
}

function syncPreservedShellChromeFromURL(root, url) {
  if (!root || !url) return;
  const q = url.searchParams.get("q") || "";
  root.querySelectorAll('input[name="q"]').forEach((input) => {
    if (input instanceof HTMLInputElement && input.value !== q) input.value = q;
  });
}

function routeHref(url) {
  return `${url.pathname}${url.search}${url.hash}`;
}

function syncDocumentTitleForRoute(route) {
  // Full guest-v2 navigations receive the server document title naturally.
  // The legacy same-document router preserves <head>, so explicitly repair
  // the profile title after swapping the route outlet.
  if (route === "profile") document.title = "User | Plain Text Nostr";
}

export function serverThreadHydrateHref(url) {
  return threadServerHydrateHref(url.toString());
}

function extractRouteOutletHTML(documentHTML) {
  const template = document.createElement("template");
  template.innerHTML = documentHTML;
  const outlet = template.content.querySelector('[data-route-outlet="root"]');
  if (outlet?.innerHTML?.trim()) return outlet.innerHTML;
  const appMain = template.content.querySelector("[data-nav-root]");
  if (appMain?.innerHTML?.trim()) return appMain.innerHTML;
  return "";
}

function serverRouteHasRenderableHTML(route, html, url = currentURL()) {
  if (!String(html || "").trim()) return false;
  const template = document.createElement("template");
  template.innerHTML = html;
  if (route === "thread") {
    const selectedID = threadPathNoteID(url.toString());
    return !selectedID || isThreadHydrateRenderable(html, selectedID);
  }
  return Boolean(template.content.querySelector(
    "[data-shell-main], [data-route-outlet='main'], .feed-column, .read-detail, [data-profile-shell], #feed, [data-feed], .profile-shell, .search-results, .tag-heading, .notifications-toolbar",
  ));
}

function serverPrimaryRoute(route) {
  return new Set([
    "feed",
    "thread",
    "profile",
    "search",
    "tag",
    "bookmarks",
    "notifications",
    "reads",
    "read",
    "relays",
  ]).has(route);
}

function relayNativeDebugEnabled() {
  try {
    if (localStorage.getItem("ptxt_direct_relays") === "1") return true;
    if (localStorage.getItem("ptxt_relay_native_routes") === "1") return true;
  } catch {
    // localStorage can throw in hardened browsing modes.
  }
  return false;
}

function relayNativeRouteOverrideEnabled() {
  return relayNativeDebugEnabled() ||
    appBootstrap().features?.relayNativeRoutesPrimary === true;
}

function relayNativeFallbackEnabled(route) {
  if (!route || route === "stub") return false;
  if (relayNativeRouteOverrideEnabled()) return true;
  return appBootstrap().features?.directRelayReads === true;
}

function serverRenderedInitialRouteUsable(route, root, url) {
  if (route === "feed") {
    return Boolean(root?.querySelector?.("#feed .note[id], #feed .comment[id]"));
  }
  if (route === "thread") {
    const outlet = routeOutletElement(root);
    const html = outlet?.innerHTML || "";
    const selectedID = threadPathNoteID(url.toString());
    return Boolean(routeOutletHasShell(root) && (!selectedID || isThreadHydrateComplete(html, selectedID)));
  }
  return routeOutletHasShell(root);
}

function threadRelayHintHeaders(preferredRelays = []) {
  const relays = [];
  const seen = new Set();
  for (const raw of preferredRelays || []) {
    const relay = normalizeRelayURL(raw);
    if (!relay || seen.has(relay)) continue;
    seen.add(relay);
    relays.push(relay);
    if (relays.length >= 4) break;
  }
  return relays.length ? { "X-Ptxt-Relays": relays.join(",") } : {};
}

function makeThreadTelemetryID() {
  try {
    if (typeof crypto?.randomUUID === "function") {
      return crypto.randomUUID().replace(/-/g, "");
    }
  } catch {
    // Fall through to timestamp/random fallback.
  }
  return `tr${Date.now().toString(36)}${Math.random().toString(36).slice(2, 14)}`;
}

function threadTelemetryHeaders(telemetryID = "") {
  return telemetryID ? { "X-Ptxt-Thread-Request": telemetryID } : {};
}

function beginThreadTelemetry(route, url) {
  if (route !== "thread" || typeof EventSource === "undefined") {
    return { id: "", close() {} };
  }
  const loader = main?.querySelector?.('[data-retro-loader-type="thread"]');
  const id = makeThreadTelemetryID();
  let closed = false;
  let highestPercent = 4;
  let source = null;
  if (loader instanceof Element) {
    initRetroLoaders(loader);
    setRetroLoaderProgress(loader, {
      percent: 4,
      summary: "Preparing server-rendered thread.",
      statusMessage: "opening live thread status stream",
    });
  }
  try {
    source = new EventSource(`/api/thread-telemetry?id=${encodeURIComponent(id)}`);
    source.addEventListener("status", (event) => {
      if (!(loader instanceof Element)) return;
      let payload = null;
      try {
        payload = JSON.parse(event.data || "{}");
      } catch {
        return;
      }
      const message = String(payload.message || "").trim();
      const percent = Number(payload.percent);
      if (payload.done) {
        markRetroLoaderComplete(loader, {
          summary: message || "Thread response ready.",
          completionMessage: message || "thread response ready",
        });
        window.setTimeout(() => source?.close?.(), 500);
        return;
      }
      const nextPercent = Number.isFinite(percent)
        ? Math.max(highestPercent, percent)
        : undefined;
      if (Number.isFinite(nextPercent)) highestPercent = nextPercent;
      setRetroLoaderProgress(loader, {
        percent: nextPercent,
        summary: message,
        statusMessage: message,
      });
    });
    source.onerror = () => {
      // Mobile browsers may reconnect SSE streams during long requests. Do not
      // rewind the visible loader; the hydrate fetch is the source of truth.
    };
  } catch {
    source = null;
  }
  return {
    id,
    close() {
      closed = true;
      window.setTimeout(() => source?.close?.(), 750);
    },
  };
}

function markThreadServerRenderUnavailable() {
  const loader = main?.querySelector?.('[data-retro-loader-type="thread"]');
  if (!(loader instanceof Element)) return;
  setRetroLoaderProgress(loader, {
    percent: 84,
    summary: "Server thread render stopped before HTML was ready.",
    statusMessage: "server thread render did not return usable HTML",
  });
  loader.setAttribute("aria-busy", "false");
  loader.dataset.retroLoaderPending = "false";
  let actions = loader.querySelector("[data-thread-load-failure-actions]");
  if (!(actions instanceof HTMLElement)) {
    actions = document.createElement("div");
    actions.dataset.threadLoadFailureActions = "true";
    actions.className = "route-error-actions";
    const retry = document.createElement("a");
    retry.href = `${window.location.pathname}${window.location.search}${window.location.hash}`;
    retry.textContent = "retry thread";
    const back = document.createElement("button");
    back.type = "button";
    back.textContent = "go back";
    back.addEventListener("click", () => {
      if (window.history.length > 1) window.history.back();
      else window.location.assign("/");
    });
    actions.append(retry, document.createTextNode(" "), back);
    loader.append(actions);
  }
}

function markThreadServerRenderRetry(attempt) {
  const loader = main?.querySelector?.('[data-retro-loader-type="thread"]');
  if (!(loader instanceof Element)) return;
  setRetroLoaderProgress(loader, {
    percent: 84,
    summary: "Waiting for the server-rendered thread to finish.",
    statusMessage: `server thread render incomplete; retrying (${attempt}/${THREAD_SERVER_RENDER_RETRY_DELAYS_MS.length})`,
  });
}

function delay(ms) {
  return new Promise((resolve) => {
    window.setTimeout(resolve, Math.max(0, Number(ms) || 0));
  });
}

function applyCompletedThreadUpgrade(html, url) {
  if (!html?.trim?.()) return false;
  if (routeStorageHref(currentURL()) !== routeStorageHref(url)) return false;
  replaceRouteOutletPreservingProfiles(html);
  setCurrentRoute("thread");
  updateSessionLinks();
  updateRelayAwareLinks();
  rehydrateRouteChrome("thread", url, main);
  initFeedLoadMore(main);
  initLayoutUI(main);
  initViewMore(main);
  wireAvatarImageFallbacks(main);
  initThreadPage();
  syncMobileAppNavHeight();
  ensureFocusedThreadBelowHeader();
  syncRoutePolling("thread", url, main);
  document.dispatchEvent(new CustomEvent("page:load", {
    detail: {
      page: "thread",
      route: "thread",
      url: url.toString(),
      container: routeOutletElement(main) || main,
    },
  }));
  return true;
}

function scheduleThreadPartialUpgrade(urlLike) {
  let url = null;
  try {
    url = new URL(urlLike?.toString?.() || window.location.href, window.location.origin);
  } catch {
    return;
  }
  const selectedID = threadPathNoteID(url.toString());
  if (!selectedID || !threadFocusNeedsFullHydrate(main)) return;
  const generation = ++threadPartialUpgradeGeneration;
  void (async () => {
    for (const retryDelay of THREAD_PARTIAL_UPGRADE_RETRY_DELAYS_MS) {
      await delay(retryDelay);
      if (generation !== threadPartialUpgradeGeneration) return;
      if (routeStorageHref(currentURL()) !== routeStorageHref(url)) return;
      let response = null;
      try {
        response = await fetchWithSession(withRelays(serverThreadHydrateHref(url)), {
          headers: { Accept: "text/html" },
        });
      } catch {
        continue;
      }
      if (!response?.ok) continue;
      const html = await response.text();
      if (!isThreadHydrateComplete(html, selectedID)) continue;
      if (generation !== threadPartialUpgradeGeneration) return;
      if (applyCompletedThreadUpgrade(html, url)) {
        threadPartialUpgradeGeneration += 1;
      }
      return;
    }
    if (generation !== threadPartialUpgradeGeneration) return;
    const parent = main?.querySelector?.("#thread-focus .thread-focus-parent--skeleton");
    if (parent instanceof HTMLElement) {
      parent.dataset.threadParentState = "unavailable";
    }
  })();
}

async function fetchServerRenderedRouteHTML(route, url, { preferredRelays = [], telemetryID = "" } = {}) {
  if (!serverPrimaryRoute(route)) return "";
  if (route === "thread") {
    const selectedID = threadPathNoteID(url.toString());
    const requestURL = withRelays(serverThreadHydrateHref(url));
    for (let attempt = 0; attempt <= THREAD_SERVER_RENDER_RETRY_DELAYS_MS.length; attempt += 1) {
      if (routeStorageHref(currentURL()) !== routeStorageHref(url)) return "";
      const response = await fetchWithSession(requestURL, {
        headers: {
          Accept: "text/html",
          ...threadRelayHintHeaders(preferredRelays),
          ...threadTelemetryHeaders(telemetryID),
        },
      });
      if (!response.ok) return "";
      const html = await response.text();
      // Navigation paintability is determined by selected-thread context.
      // Reply counts advertised by the source card are a freshness hint, not
      // a reason to discard a renderable root/focus response and show a full
      // route loader while background materialization catches up.
      if (!isThreadHydrateResponseIncomplete(response, html, selectedID)) return html.trim();
      // A carried/clicked note is already a better foreground state than an
      // incomplete server response. Do not spend the full-route retry budget
      // or expose its telemetry; settle the preview and retry only the missing
      // parent/replies in the localized upgrade lane.
      if (visibleThreadSelectedNote(main, url)) return "";
      const retryDelay = THREAD_SERVER_RENDER_RETRY_DELAYS_MS[attempt];
      if (!Number.isFinite(retryDelay)) return "";
      markThreadServerRenderRetry(attempt + 1);
      await delay(retryDelay);
    }
    return "";
  }
  const response = await fetchWithSession(withRelays(routeHref(url)), {
    headers: { Accept: "text/html" },
  });
  if (!response.ok) return "";
  const html = extractRouteOutletHTML(await response.text()).trim();
  return serverRouteHasRenderableHTML(route, html, url) ? html : "";
}

async function renderServerRouteIfAvailable(route, url, { force = false, preferredRelays = [], telemetryID = "" } = {}) {
  if (!main) return false;
  if (relayNativeRouteOverrideEnabled()) return false;
  if (!force && routeOutletHasShell(main) && !(route === "thread" && routeOutletHasPendingThread(main))) return false;
  const html = await fetchServerRenderedRouteHTML(route, url, { preferredRelays, telemetryID }).catch(() => "");
  if (!html) {
    if (route === "thread" && routeOutletHasPendingThread(main)) markThreadServerRenderUnavailable();
    return false;
  }
  if (routeStorageHref(currentURL()) !== routeStorageHref(url)) return false;
  replaceRouteOutletPreservingProfiles(html);
  return true;
}

function rehydrateRouteChrome(route, url, root) {
  syncPreservedShellChromeFromURL(root, url);
  if (route === "profile") {
    root.querySelectorAll("[data-profile-tab]").forEach((el) => {
      delete el.dataset.bound;
    });
    root.querySelectorAll(".profile-stats-menu-trigger").forEach((el) => {
      delete el.dataset.boundStatsMenu;
    });
    bindProfileStatLinks();
    bindProfileLazyTabs(url, root);
  }
  if (route === "thread") applyCarriedNoteExpansion(root);
}

function shouldHandleThreadCardAnchor(link, card) {
  if (!(link instanceof HTMLAnchorElement)) return false;
  if (!(card instanceof HTMLElement)) return false;
  if (link.hasAttribute("data-reply-action")) return false;
  if (link.closest("[data-ascii-action-menu-trigger], [data-note-menu-action]")) return false;
  try {
    const url = new URL(link.href, window.location.origin);
    return routeKind(url.pathname) === "thread";
  } catch {
    return false;
  }
}

function isFocusedThreadCardSelfRoute(card, href) {
  if (!(card instanceof HTMLElement)) return false;
  if (routeKind(window.location.pathname) !== "thread") return false;
  if (!card.closest("#thread-focus")) return false;
  if (!card.classList.contains("is-focused")) return false;
  const cardID = noteIDFromElement(card);
  if (!cardID) return false;
  try {
    return threadPathNoteID(href) === cardID;
  } catch {
    return false;
  }
}

function hasActiveTextSelectionWithin(node) {
  if (!(node instanceof Node)) return false;
  const selection = window.getSelection?.();
  if (!selection || selection.isCollapsed || !selection.toString().trim()) return false;
  for (let i = 0; i < selection.rangeCount; i += 1) {
    const range = selection.getRangeAt(i);
    try {
      if (range.intersectsNode(node)) return true;
    } catch {
      const container = range.commonAncestorContainer;
      if (node.contains(container.nodeType === Node.ELEMENT_NODE ? container : container.parentElement)) {
        return true;
      }
    }
  }
  return false;
}

function shouldSuppressThreadCardNavigation(card, href) {
  if (!(card instanceof HTMLElement)) return false;
  if (isFocusedThreadCardSelfRoute(card, href)) return true;
  return routeKind(window.location.pathname) === "thread" && hasActiveTextSelectionWithin(card);
}

function clearRouteClientCaches() {
  clearThreadWarmCache();
  void clearLegacyRouteRecords().catch(() => {});
}

function mediaItemsFromGrid(grid) {
  if (!(grid instanceof Element)) return [];
  return [...grid.querySelectorAll(".note-media-tile")].map((tile) => {
    const image = tile.querySelector?.("img");
    if (image instanceof HTMLImageElement && image.currentSrc) return { type: "image", url: image.currentSrc };
    if (image instanceof HTMLImageElement && image.src) return { type: "image", url: image.src };
    const video = tile.querySelector?.("video");
    if (video instanceof HTMLVideoElement && video.currentSrc) return { type: "video", url: video.currentSrc };
    if (video instanceof HTMLVideoElement && video.src) return { type: "video", url: video.src };
    return null;
  }).filter((item) => item?.url);
}

function openDocumentMediaViewer(event, owner) {
  if (!(event.target instanceof Element)) return false;
  const trigger = event.target.closest("[data-media-grid-open]");
  if (!(trigger instanceof Element)) return false;
  const grid = trigger.closest(".note-media-grid");
  const items = mediaItemsFromGrid(grid);
  if (!items.length) return false;
  event.preventDefault();
  event.stopPropagation();
  const index = Math.min(
    items.length - 1,
    Math.max(0, Number.parseInt(trigger.getAttribute("data-media-grid-open") || "0", 10) || 0),
  );
  openImageViewer(items, index, owner instanceof HTMLElement ? owner : null);
  return true;
}

function ensureFocusedThreadBelowHeader() {
  syncMobileAppNavHeight();
}

function parsedHandoffEvent(source) {
  const raw = String(source?.dataset?.asciiEvent || "").trim();
  if (!raw) return null;
  try {
    const event = JSON.parse(raw);
    return event?.id ? event : null;
  } catch {
    return null;
  }
}

function threadPreviewHandoffRelays(source, event = null) {
  const relays = relayHintsFromNoteElement(source);
  const eventRelay = String(event?.relay_url || "").trim();
  if (eventRelay) relays.unshift(eventRelay);
  return [...new Set(relays.map((relay) => String(relay || "").trim()).filter(Boolean))];
}

function storeThreadPreviewHandoff(href, source) {
  if (!(source instanceof HTMLElement)) return;
  let url = null;
  try {
    url = new URL(href, window.location.origin);
  } catch {
    return;
  }
  if (routeKind(url.pathname) !== "thread") return;
  const selectedID = threadPathNoteID(url.toString()) || noteIDFromElement(source);
  if (!selectedID) return;
  const event = parsedHandoffEvent(source);
  const preferredRelays = threadPreviewHandoffRelays(source, event);
  if (!event?.id && !preferredRelays.length) return;
  try {
    sessionStorage.setItem(THREAD_PREVIEW_HANDOFF_KEY, JSON.stringify({
      href: `${url.pathname}${url.search}${url.hash}`,
      selectedID,
      event: event?.id ? event : null,
      preferredRelays,
      savedAt: Date.now(),
    }));
  } catch {
    // Best-effort same-browser-document data handoff.
  }
}

async function restoreThreadPreviewHandoff(url, { renderPreview = true } = {}) {
  const empty = { previewAlreadyRendered: false, preferredRelays: [] };
  if (routeKind(url.pathname) !== "thread") return empty;
  let payload = null;
  try {
    payload = JSON.parse(sessionStorage.getItem(THREAD_PREVIEW_HANDOFF_KEY) || "null");
    sessionStorage.removeItem(THREAD_PREVIEW_HANDOFF_KEY);
  } catch {
    return empty;
  }
  if (!payload || Date.now() - Number(payload.savedAt || 0) > 30_000) return empty;
  const selectedID = threadPathNoteID(url.toString());
  if (selectedID && payload.selectedID && String(payload.selectedID).toLowerCase() !== selectedID) return empty;
  const preferredRelays = Array.isArray(payload.preferredRelays)
    ? payload.preferredRelays.map((relay) => String(relay || "").trim()).filter(Boolean)
    : [];
  let previewAlreadyRendered = false;
  if (renderPreview && payload.event?.id) {
    await putEvents([payload.event]).catch(() => {});
    const preview = await renderCachedThreadRoutePreview(main, selectedID || payload.selectedID || "", {
      preferredRelays,
      canRender: () => routeKind(window.location.pathname) === "thread"
        && routeStorageHref(currentURL()) === routeStorageHref(url),
    }).catch(() => null);
    previewAlreadyRendered = Boolean(preview?.rendered);
  }
  return { previewAlreadyRendered, preferredRelays };
}

async function navigateThreadFocusFromServer(href, sourceCard = null) {
  if (routeKind(window.location.pathname) !== "thread") return false;
  let url = null;
  try {
    url = new URL(href, window.location.origin);
  } catch {
    return false;
  }
  if (routeKind(url.pathname) !== "thread") return false;
  dismissOpenMobileMenuForNavigation(main);

  const targetHref = `${url.pathname}${url.search}${url.hash}`;
  const optimisticallyRendered = renderOptimisticThreadFocus(url.toString(), sourceCard);
  if (optimisticallyRendered) {
    history.pushState(history.state, "", targetHref);
  }

  const fragmentURL = new URL(url.toString());
  const response = await fetchWithSession(withRelays(serverThreadHydrateHref(fragmentURL)), {
    headers: { Accept: "text/html" },
  });
  if (!response.ok) return false;
  const html = await response.text();
  if (!html.trim()) return false;
  const selectedID = threadPathNoteID(url.toString());
  if (selectedID && !isThreadHydrateComplete(html, selectedID)) {
    if (optimisticallyRendered) scheduleThreadPartialUpgrade(url);
    return optimisticallyRendered;
  }

  if (!optimisticallyRendered) {
    history.pushState(history.state, "", targetHref);
  } else if (routeStorageHref(currentURL()) !== targetHref) {
    return true;
  }
  replaceRouteOutletPreservingProfiles(html);
  setCurrentRoute("thread");
  updateSessionLinks();
  updateRelayAwareLinks();
  rehydrateRouteChrome("thread", currentURL(), main);
  initFeedLoadMore(main);
  initLayoutUI(main);
  initViewMore(main);
  wireAvatarImageFallbacks(main);
  initThreadPage();
  syncMobileAppNavHeight();
  ensureFocusedThreadBelowHeader();
  scrollRouteToTop(main);
  syncRoutePolling("thread", currentURL(), main);
  document.dispatchEvent(new CustomEvent("page:load", {
    detail: {
      page: "thread",
      route: "thread",
      url: window.location.href,
      container: routeOutletElement(main) || main,
    },
  }));
  return true;
}

async function navigateDocumentRoute(href, { sourceLink = null, sourceCard = null } = {}) {
  let url = null;
  try {
    url = new URL(href, window.location.origin);
  } catch {
    return false;
  }
  if (url.origin !== window.location.origin) return false;
  const route = routeKind(url.pathname);
  if (!route) return false;
  dismissOpenMobileMenuForNavigation(main);
  if (sourceLink instanceof HTMLAnchorElement && route === "profile") {
    rememberProfileRoutePreviewFromLink(sourceLink);
  }

  history.pushState(history.state, "", `${url.pathname}${url.search}${url.hash}`);
  scrollRouteToTop(main);
  const restoredFeedSnapshot = restoreRouteSnapshot(route, url, { directFeedNavigation: true });
  renderCurrentRouteShell(route, url, { force: !restoredFeedSnapshot });
  if (route === "thread" && sourceCard instanceof HTMLElement) {
    renderFeedToThreadPreview(url.toString(), sourceCard);
  }
  if (route === "profile") {
    updateProfilePostsRouteLoader({
      percent: 8,
      summary: "Profile identity is ready. Loading posts separately.",
      statusMessage: "profile identity ready; loading posts...",
    });
    void renderProfileHeaderIfAvailable(url);
  }
  const preferredRelays = route === "thread"
    ? threadPreviewHandoffRelays(sourceCard instanceof HTMLElement ? sourceCard : sourceLink)
    : [];
  const telemetry = beginThreadTelemetry(route, url);
  const serverRendered = restoredFeedSnapshot || await renderServerRouteIfAvailable(route, url, {
    force: true,
    preferredRelays,
    telemetryID: telemetry.id,
  });
  telemetry.close();
  // Guest profiles use the canonical server document as their authoritative
  // data source, but its assembly must not block the locally renderable shell.
  // If that fetch fails, hand control back to the caller for a normal browser
  // navigation so an error response cannot leave a terminal profile skeleton.
  if (
    route === "profile" &&
    !serverRendered &&
    !normalizedPubkey() &&
    !relayNativeRouteOverrideEnabled()
  ) {
    return false;
  }
  return hydrateDocumentRoute({
    shellAlreadyRendered: true,
    serverRendered,
    serverAttempted: true,
  });
}

function renderFeedToThreadPreview(href, sourceCard) {
  if (!(sourceCard instanceof HTMLElement)) return false;
  const rendered = renderOptimisticThreadFocus(href, sourceCard);
  if (!rendered) return false;
  const selectedID = threadPathNoteID(href);
  const selected = selectedID
    ? main?.querySelector?.(`#thread-focus #note-${CSS.escape(selectedID)}`)
    : null;
  selected?.classList?.add?.("ptxt-carried-thread-note");
  main?.querySelector?.("#thread-summary")?.replaceChildren();
  main?.querySelector?.("#thread-tree-view")?.replaceChildren();
  const participants = main?.querySelector?.('[data-thread-fragment="participants"]');
  if (participants instanceof HTMLElement) participants.hidden = true;
  return true;
}

function sameDocumentRouteHref(link) {
  if (!(link instanceof HTMLAnchorElement)) return "";
  if (link.target && link.target !== "_self") return "";
  if (link.hasAttribute("download")) return "";
  let url = null;
  try {
    url = new URL(link.href, window.location.origin);
  } catch {
    return "";
  }
  if (url.origin !== window.location.origin) return "";
  return routeKind(url.pathname)
    ? `${url.pathname}${url.search}${url.hash}`
    : "";
}

function routeKindFromHref(href) {
  try {
    return routeKind(new URL(href, window.location.origin).pathname);
  } catch {
    return "";
  }
}

function authoritativeGuestDocument(href) {
	if (normalizedPubkey() || relayNativeRouteOverrideEnabled()) return false;
	if (!document.body?.dataset?.guestV2) return false;
	const next = routeKindFromHref(href);
	return next === "thread" || next === "profile" || next === "feed";
}

function canonicalThreadFocusHref(href) {
  let url = null;
  try {
    url = new URL(href, window.location.origin);
  } catch {
    return href;
  }
  if (routeKind(url.pathname) !== "thread") return href;
  const selectedParam = url.searchParams.get("selected") || "";
  const selectedID = /^[0-9a-f]{64}$/i.test(selectedParam) ? selectedParam.toLowerCase() : "";
  if (selectedID && url.hash !== `#note-${selectedID}`) {
    url.hash = `#note-${selectedID}`;
  }
  return `${url.pathname}${url.search}${url.hash}`;
}

function threadHrefForRouteCard(card, fallbackHref = "") {
  if (!(card instanceof Element)) return canonicalThreadFocusHref(fallbackHref);
  return canonicalThreadFocusHref(
    card.getAttribute("data-ascii-ref-select-href") ||
      card.getAttribute("data-ascii-select-href") ||
      fallbackHref,
  );
}

function renderOptimisticThreadFocus(href, sourceCard = null) {
  if (!(sourceCard instanceof HTMLElement)) return false;
  let url = null;
  try {
    url = new URL(href, window.location.origin);
  } catch {
    return false;
  }
  const selectedID = threadPathNoteID(url.toString()) || noteIDFromElement(sourceCard);
  if (!selectedID || noteIDFromElement(sourceCard) !== selectedID) return false;
  const focus = main?.querySelector?.("#thread-focus");
  if (!(focus instanceof HTMLElement)) return false;
  const column = main.querySelector(".feed-column[data-thread-root-id], .feed-column[data-thread-selected-id]");
  const handoffEvent = parsedHandoffEvent(sourceCard);
  const rootID = String(
    column?.dataset?.threadRootId ||
    sourceCard.dataset.replyRootId ||
    sourceCard.dataset.asciiThreadRootId ||
    rootIDForEvent(handoffEvent) ||
    "",
  ).toLowerCase();
  const directParentID = String(
    parentID(rootID, handoffEvent) || parentID("", handoffEvent) || "",
  ).toLowerCase();
  const expectsParent = Boolean(
    (rootID && selectedID !== rootID) ||
    (directParentID && directParentID !== selectedID),
  );
  column?.setAttribute?.("data-thread-selected-id", selectedID);
  const previousFocused = sourceCard.classList.contains("thread-focus-parent")
    ? focus.querySelector(".thread-focus-selected, .is-focused")
    : null;
  main.querySelectorAll(".note.is-focused, .comment.is-focused").forEach((node) => {
    node.classList.remove("is-focused", "thread-focus-selected");
    if (node instanceof HTMLElement && node.dataset.asciiSelected === "true") {
      node.dataset.asciiSelected = "false";
    }
  });
  const selected = sourceCard.cloneNode(true);
  if (!(selected instanceof HTMLElement)) return false;
  const parentSource = optimisticThreadParentSource(
    sourceCard,
    selectedID,
    directParentID || rootID,
    rootID,
    focus,
  );
  selected.classList.remove("comment", "thread-focus-parent");
  selected.classList.add("note", "is-focused", "thread-focus-selected");
  normalizeOptimisticThreadAvatar(selected, "note-avatar");
  selected.dataset.asciiKind = "selected";
  selected.dataset.asciiSelected = "true";
  selected.dataset.depth = "1";
  selected.style.setProperty("--depth", "1");
  selected.querySelectorAll(":scope > .comments, :scope > .continue-thread").forEach((node) => node.remove());
  focus.replaceChildren();
  if (expectsParent) {
    const parent = parentSource
      ? optimisticThreadParentClone(parentSource)
      : optimisticThreadParentSkeleton();
    focus.append(parent);
  }
  focus.append(selected);
  renderOptimisticThreadReplies(sourceCard, selectedID, previousFocused);
  refreshAsciiSync(focus);
  refreshAsciiSync(main.querySelector("#thread-replies"));
  applyDestinationThreadTransition(main, selectedID);
  initViewMore(focus);
  initViewMore(main.querySelector("#thread-replies"));
  wireAvatarImageFallbacks(focus);
  wireAvatarImageFallbacks(main.querySelector(".thread-replies"));
  syncMobileAppNavHeight();
  return true;
}

function normalizeOptimisticThreadAvatar(shell, className) {
  const avatar = shell?.querySelector?.(":scope > .note-avatar, :scope > .comment-avatar");
  if (!(avatar instanceof HTMLElement)) return;
  avatar.classList.remove("note-avatar", "comment-avatar");
  avatar.classList.add(className);
}

function optimisticThreadParentSource(sourceCard, selectedID, expectedParentID, rootID, focus) {
  if (!(sourceCard instanceof HTMLElement) || !selectedID) return null;
  const nestedParent = sourceCard.parentElement?.closest?.(".comment[id^='note-']");
  if (nestedParent instanceof HTMLElement && noteIDFromElement(nestedParent) !== selectedID) {
    return nestedParent;
  }
  for (const candidateID of [expectedParentID, rootID]) {
    if (!candidateID || candidateID === selectedID) continue;
    const parent = focus?.querySelector?.(`#note-${CSS.escape(candidateID)}`);
    if (parent instanceof HTMLElement) return parent;
  }
  return null;
}

function optimisticThreadParentSkeleton() {
  const template = document.createElement("template");
  template.innerHTML = threadParentSkeletonMarkup().trim();
  const parent = template.content.firstElementChild;
  if (parent instanceof HTMLElement) return parent;
  const fallback = document.createElement("div");
  fallback.className = "comment thread-focus-parent thread-focus-parent--skeleton";
  fallback.setAttribute("aria-hidden", "true");
  return fallback;
}

function optimisticThreadParentClone(source) {
  const parent = source.cloneNode(true);
  if (!(parent instanceof HTMLElement)) return document.createElement("div");
  parent.classList.remove("is-focused", "thread-focus-selected", "note");
  parent.classList.add("comment", "thread-focus-parent");
  normalizeOptimisticThreadAvatar(parent, "comment-avatar");
  parent.dataset.asciiKind = "reply";
  parent.dataset.asciiSelected = "false";
  parent.querySelectorAll(":scope > .comments, :scope > .continue-thread").forEach((node) => node.remove());
  return parent;
}

function optimisticThreadReplyClone(source) {
  const reply = source?.cloneNode?.(true);
  if (!(reply instanceof HTMLElement)) return null;
  reply.classList.remove("is-focused", "thread-focus-selected", "thread-focus-parent", "note");
  reply.classList.add("comment");
  normalizeOptimisticThreadAvatar(reply, "comment-avatar");
  reply.dataset.asciiKind = "reply";
  reply.dataset.asciiSelected = "false";
  reply.querySelectorAll(":scope > .comments, :scope > .continue-thread").forEach((node) => node.remove());
  return reply;
}

function renderOptimisticThreadReplies(sourceCard, selectedID, previousFocused = null) {
  const replies = main?.querySelector?.("#thread-replies");
  if (!(replies instanceof HTMLElement) || !(sourceCard instanceof HTMLElement)) return;
  const selectedChildren = sourceCard.querySelector(":scope > .comments");
  const nextReplies = selectedChildren instanceof HTMLElement
    ? [...selectedChildren.children].map((node) => node.cloneNode(true)).filter((node) => node instanceof HTMLElement)
    : [];
  const previousFocusedID = noteIDFromElement(previousFocused);
  if (
    previousFocusedID &&
    previousFocusedID !== selectedID &&
    !nextReplies.some((node) => noteIDFromElement(node) === previousFocusedID)
  ) {
    const previousReply = optimisticThreadReplyClone(previousFocused);
    if (previousReply) nextReplies.push(previousReply);
  }
  replies.replaceChildren(...nextReplies);
  replies.classList.remove("thread-replies-skeleton");
  replies.querySelectorAll(`#note-${CSS.escape(selectedID)}`).forEach((node) => node.remove());
  main.querySelectorAll("[data-thread-load-more]").forEach((node) => {
    if (node instanceof HTMLElement) node.hidden = true;
  });
  main.querySelectorAll(".thread-filtered-replies-toggle, [data-thread-filtered-replies]").forEach((node) => {
    if (node instanceof HTMLElement) node.hidden = true;
  });
  main.querySelectorAll(".thread-other-replies-toggle, [data-focused-other-replies]").forEach((node) => {
    if (node instanceof HTMLElement) node.hidden = true;
  });
}

async function navigateThreadCardWithTransition(href, card, navigate) {
  const sourceRoute = routeKind(window.location.pathname);
  const transition = sourceRoute === "thread"
    ? prepareThreadFocusTransition(card, href, main)
    : prepareThreadTransition(card, href);
  const selectedID = threadPathNoteID(href);
  try {
    return await runNoteViewTransition(transition, navigate, { awaitUpdate: false });
  } finally {
    if (transition) clearThreadTransition(selectedID);
  }
}

function activateRouteCardTarget(target, event) {
  if (!(target instanceof Element)) return false;
  const referenced = target.closest(routeCardReferenceSelector);
  const card = referenced || target.closest(routeCardSelector);
  if (!card) return false;
  // Native media controls own their full click/touch sequence. Treating the
  // surrounding media tile as note navigation re-selects the card on touchend
  // and can also prevent the synthetic click that starts playback.
  if (target.closest(routeCardNativeMediaSelector)) return false;
  if (routeKind(window.location.pathname) !== "feed" && target.closest(routeCardLocalMediaActionSelector)) {
    openDocumentMediaViewer(event, card);
    return true;
  }
  const mediaTarget = target.closest(routeCardMediaNavigationSelector);
  if (!mediaTarget && target.closest(interactiveSelector)) return false;
  const rawHref = referenced
    ? referenced.getAttribute("data-ascii-ref-select-href")
    : card.getAttribute("data-ascii-select-href");
  const href = canonicalThreadFocusHref(rawHref || "");
  if (!href) return false;
  if (shouldSuppressThreadCardNavigation(card, href)) {
    event.preventDefault();
    event.stopPropagation();
    return true;
  }
  saveCurrentRouteScroll(card instanceof HTMLElement ? card : null);
  storeThreadPreviewHandoff(href, card instanceof HTMLElement ? card : null);
  event.preventDefault();
  event.stopImmediatePropagation();
  if (routeKind(window.location.pathname) === "thread") {
    const sourceCard = card instanceof HTMLElement ? card : null;
    void navigateThreadCardWithTransition(
      href,
      sourceCard,
      () => navigateThreadFocusFromServer(href, sourceCard),
    ).then((handled) => {
      if (!handled) window.location.assign(withRelays(href));
    }).catch(() => {
      window.location.assign(withRelays(href));
    });
    return true;
  }
  const route = routeKindFromHref(href);
  if (route) {
    if (authoritativeGuestDocument(href)) {
      window.location.assign(href);
      return true;
    }
    const sourceCard = card instanceof HTMLElement ? card : null;
    void navigateThreadCardWithTransition(
      href,
      sourceCard,
      () => navigateDocumentRoute(withRelays(href), { sourceCard }),
    ).then((handled) => {
      if (!handled) window.location.assign(withRelays(href));
    }).catch(() => {
      window.location.assign(withRelays(href));
    });
    return true;
  }
  window.location.assign(withRelays(href));
  return true;
}

function shouldSuppressSyntheticClick(target) {
  if (!(target instanceof Element) || !routeTouchHandled) return false;
  if (Date.now() - routeTouchHandled.at >= 700) return false;
  const card = target.closest(routeCardSelector);
  if (card !== routeTouchHandled.card) return false;
  const href = card instanceof HTMLElement ? card.getAttribute("data-ascii-select-href") || "" : "";
  return Boolean(href && href === routeTouchHandled.href);
}

async function unregisterAppShellServiceWorker() {
  if (!("serviceWorker" in navigator)) return;
  const registrations = await navigator.serviceWorker.getRegistrations().catch(() => []);
  await Promise.all(registrations.map((registration) => registration.unregister().catch(() => false)));
}

function initDocumentLifecycle(hydrateRoute) {
  let routeViewerPubkey = normalizedPubkey();
  document.addEventListener("click", (event) => {
    if (event.defaultPrevented) return;
    if (typeof event.button === "number" && event.button !== 0) return;
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    if (!(event.target instanceof Element)) return;
    if (shouldSuppressSyntheticClick(event.target)) {
      event.preventDefault();
      event.stopPropagation();
      routeTouchHandled = null;
      return;
    }
    const link = event.target.closest("a[href]");
    if (link instanceof HTMLAnchorElement) {
      const sourceCard = link.closest(routeCardReferenceSelector) || link.closest(routeCardSelector);
      if (sourceCard instanceof HTMLElement) {
        const cardHref = threadHrefForRouteCard(sourceCard, link.href);
        if (routeKind(window.location.pathname) === "thread" && shouldHandleThreadCardAnchor(link, sourceCard)) {
          if (shouldSuppressThreadCardNavigation(sourceCard, cardHref)) {
            event.preventDefault();
            event.stopPropagation();
            return;
          }
          event.preventDefault();
          saveCurrentRouteScroll(sourceCard);
          storeThreadPreviewHandoff(cardHref, sourceCard);
          void navigateThreadCardWithTransition(
            cardHref,
            sourceCard,
            () => navigateThreadFocusFromServer(cardHref, sourceCard),
          ).then((handled) => {
            if (!handled) window.location.assign(withRelays(cardHref));
          }).catch(() => {
            window.location.assign(withRelays(cardHref));
          });
          return;
        }
      }
      if (link.hasAttribute("data-reply-action")) return;
      const routeHref = sameDocumentRouteHref(link);
      if (routeHref) {
        if (authoritativeGuestDocument(routeHref)) {
          saveCurrentRouteScroll(sourceCard instanceof HTMLElement ? sourceCard : link);
          if (sourceCard instanceof HTMLElement) {
            const cardHref = threadHrefForRouteCard(sourceCard, routeHref);
            storeThreadPreviewHandoff(cardHref, sourceCard);
          }
          return;
        }
        event.preventDefault();
        saveCurrentRouteScroll(sourceCard instanceof HTMLElement ? sourceCard : link);
        if (sourceCard instanceof HTMLElement) {
          const cardHref = threadHrefForRouteCard(sourceCard, routeHref);
          storeThreadPreviewHandoff(cardHref, sourceCard);
        }
        const sourceNote = sourceCard instanceof HTMLElement ? sourceCard : null;
        const navigate = () => navigateDocumentRoute(withRelays(routeHref), {
          sourceLink: link,
          sourceCard: sourceNote,
        });
        const work = routeKindFromHref(routeHref) === "thread" && sourceNote
          ? navigateThreadCardWithTransition(routeHref, sourceNote, navigate)
          : navigate();
        void work.then((handled) => {
          if (!handled) window.location.assign(withRelays(routeHref));
        }).catch(() => {
          window.location.assign(withRelays(routeHref));
        });
        return;
      }
      saveCurrentRouteScroll(sourceCard instanceof HTMLElement ? sourceCard : link);
      if (sourceCard instanceof HTMLElement) {
        const cardHref = threadHrefForRouteCard(sourceCard, link.href);
        storeThreadPreviewHandoff(cardHref, sourceCard);
      }
      return;
    }

    activateRouteCardTarget(event.target, event);
  }, true);

  document.addEventListener("touchstart", (event) => {
    if (event.defaultPrevented || event.touches.length !== 1) {
      routeTouchStart = null;
      return;
    }
    if (!(event.target instanceof Element)) {
      routeTouchStart = null;
      return;
    }
    const card = event.target.closest(routeCardSelector);
    if (!card) {
      routeTouchStart = null;
      return;
    }
    const touch = event.touches[0];
    routeTouchStart = {
      x: touch.clientX,
      y: touch.clientY,
      card,
      target: event.target,
    };
  }, { capture: true, passive: true });

  document.addEventListener("touchend", (event) => {
    const start = routeTouchStart;
    routeTouchStart = null;
    if (!start || event.defaultPrevented || event.changedTouches.length !== 1) return;
    const touch = event.changedTouches[0];
    if (
      Math.abs(touch.clientX - start.x) > ROUTE_TOUCH_TAP_MAX_MOVE ||
      Math.abs(touch.clientY - start.y) > ROUTE_TOUCH_TAP_MAX_MOVE
    ) {
      return;
    }
    const target = event.target instanceof Element ? event.target : start.target;
    if (!start.card.contains(target)) return;
    if (activateRouteCardTarget(target, event)) {
      routeTouchHandled = {
        at: Date.now(),
        card: start.card,
        href: start.card instanceof HTMLElement ? start.card.getAttribute("data-ascii-select-href") || "" : "",
      };
    }
  }, { capture: true, passive: false });

  document.addEventListener("visibilitychange", () => {
    const route = routeKind(window.location.pathname);
    const url = currentURL();
    if (document.visibilityState !== "visible") {
      syncRoutePolling("", url, main);
      return;
    }
    syncRoutePolling(route, url, main);
  });

  window.addEventListener("pageshow", (event) => {
    if (!event.persisted) return;
    const route = routeKind(window.location.pathname);
    if (!route) return;
    const url = currentURL();
    restoreRouteScroll(readRouteScrollRestore(route, url, { pageCacheRestore: true }));
    syncRoutePolling(route, url, main);
    updateSessionLinks();
    updateRelayAwareLinks();
  });

  window.addEventListener("pagehide", () => {
    saveCurrentRouteScroll();
  });

  window.addEventListener("popstate", () => {
    routePopstateNavigation = true;
    void hydrateRoute({ forceShell: true }).catch(() => {
      window.location.reload();
    }).finally(() => {
      routePopstateNavigation = false;
    });
  });

  const refreshCurrentRoute = () => {
    clearRouteClientCaches();
    void hydrateRoute({ forceRefresh: true }).catch(() => {});
  };
  window.addEventListener("ptxt:session", (event) => {
    const nextViewerPubkey = normalizedPubkey(event?.detail);
    if (nextViewerPubkey === routeViewerPubkey) return;
    routeViewerPubkey = nextViewerPubkey;
    refreshCurrentRoute();
  });
  for (const evt of ["ptxt:relays", "ptxt:web-of-trust-changed", "ptxt:viewer-prefs-changed"]) {
    window.addEventListener(evt, refreshCurrentRoute);
  }

}

async function hydrateDocumentRoute(options = {}) {
  if (!main) return false;
  const url = currentURL();
  const route = routeKind(url.pathname);
  if (!route) return false;

  // The local server can paint identities before the browser-side route cache
  // has metadata. Seed that cache before any shell, fragment, or relay-native
  // renderer gets a chance to replace the current document.
  rememberVisibleNoteProfiles(main);
  setCurrentRoute(route);
  const routeScrollRestore = readRouteScrollRestore(route, url);
  const restoredSnapshot = restoreRouteSnapshot(route, url);
  renderCurrentRouteShell(route, url, {
    force: options.forceShell === true && options.shellAlreadyRendered !== true && !restoredSnapshot,
  });
  const initialFeedSessionChanged = options.initialDocumentLoad === true && route === "feed" && homeFeedSessionChanged(main);
  // Top-level browser navigation cannot attach the viewer header sourced from
  // localStorage. Treat its canonical anonymous thread as an immediate preview,
  // then replace it with one private, viewer-scoped hydrate response.
  const initialThreadViewerChanged = options.initialDocumentLoad === true && route === "thread" && Boolean(normalizedPubkey());
  const telemetry = options.serverAttempted !== true && !options.serverRendered && !serverRenderedInitialRouteUsable(route, main, url)
    ? beginThreadTelemetry(route, url)
    : { id: "", close() {} };
  const serverRendered = options.serverRendered === true || (
    options.serverAttempted !== true && await renderServerRouteIfAvailable(route, url, {
      force: initialFeedSessionChanged || initialThreadViewerChanged ||
        (options.forceShell === true && !restoredSnapshot && serverPrimaryRoute(route)),
      telemetryID: telemetry.id,
    })
  );
  telemetry.close();

  if (route === "relays") initRelaysPage(main);
  if (route === "stub" && url.pathname === "/login") initLoginPage(main);
  if (route === "feed") {
    const headingNode = main.querySelector("[data-feed-heading]");
    if (headingNode && feedHeadingNeedsRefresh(headingNode, url)) {
      applyFeedHeadingMarkup(headingNode, url);
    }
  }
  const previewHandoff = route === "thread"
    ? await restoreThreadPreviewHandoff(url, {
      // Same-document navigation has already painted the carried note, and a
      // successful server render is authoritative. Re-rendering the handoff
      // here can overwrite a real/partial parent with an older cached preview.
      renderPreview: options.shellAlreadyRendered !== true && !serverRendered,
    })
    : { previewAlreadyRendered: false, preferredRelays: [] };
  const serverRenderedInitialRoute = options.initialDocumentLoad === true && !initialFeedSessionChanged && serverRenderedInitialRouteUsable(route, main, url);
  // An explicitly enabled relay-native route is an override, not merely an
  // error fallback. This keeps local/debug relay state authoritative even
  // when a server-primary shell or cached fragment happens to be renderable.
  const relayNativeOverride = relayNativeRouteOverrideEnabled();
  if (
    route !== "stub" &&
    relayNativeFallbackEnabled(route) &&
    (relayNativeOverride || (!serverRenderedInitialRoute && !serverRendered))
  ) {
    await hydrateClientRoute(route, main, {
      forceRefresh: options.forceRefresh === true || initialFeedSessionChanged,
      preserveExistingNotes: route === "feed" && options.initialDocumentLoad === true,
      previewAlreadyRendered: previewHandoff.previewAlreadyRendered,
      preferredRelays: previewHandoff.preferredRelays,
      refreshToken: nextRouteRefreshToken(route, url),
    });
  }
  if (route === "thread" && routeOutletHasPendingThread(main)) {
    // A clicked/carried note is already a useful thread route. If the server
    // exhausts its foreground budget, settle that preview as a stable partial
    // view and let background warming/polling upgrade it. Returning false here
    // triggers a hard navigation, recreates the pending shell, and loops.
    if (!settleVisiblePartialThread(main, url)) return false;
  }

  syncDocumentTitleForRoute(route);

  updateSessionLinks();
  updateRelayAwareLinks();
  rehydrateRouteChrome(route, url, main);
  initFeedLoadMore(main);
  initLayoutUI(main);
  initViewMore(main);
  wireAvatarImageFallbacks(main);
  restoreRouteScroll(routeScrollRestore);
  if (route === "thread") {
    initThreadPage();
    syncMobileAppNavHeight();
    ensureFocusedThreadBelowHeader();
    if (threadFocusNeedsFullHydrate(main)) scheduleThreadPartialUpgrade(url);
    else threadPartialUpgradeGeneration += 1;
  } else {
    threadPartialUpgradeGeneration += 1;
    teardownThreadTreeConnector();
  }
  syncRoutePolling(route, url, main, {
    initialDocumentLoad: options.initialDocumentLoad === true,
  });
  document.dispatchEvent(new CustomEvent("page:load", {
    detail: {
      page: route,
      route,
      url: url.toString(),
      container: routeOutletElement(main) || main,
    },
  }));
  return true;
}

export function initDocumentRouter() {
  if (!main) return;
  if (!document.body?.classList?.contains("feed-shell")) return;
  if ("scrollRestoration" in history) history.scrollRestoration = "manual";
  void unregisterAppShellServiceWorker();
  initDocumentLifecycle(hydrateDocumentRoute);
  initThreadIntentWarm(document);
  void hydrateDocumentRoute({ initialDocumentLoad: true }).catch((error) => {
    console.error("Document route hydration failed", error);
  });
}
