import {
  fetchWithSession,
  isFeedLikePath,
  normalizedPubkey,
  shouldSyncViewerPrefLocation,
  stripViewerPrefSearchParams,
  updateRelayAwareLinks,
  updateSessionLinks,
} from "./session.js";
import { feedTopCursor, initFeedLoadMore, prependNewNotes, profilePostsTopCursor } from "./feed.js";
import { clearFeedSnapshots, restoreFeed, snapshotFeed } from "./feed-cache.js";
import { clearProfileSnapshots, restoreProfile, snapshotProfile } from "./profile-cache.js";
import { clearThreadSnapshots, restoreThread, snapshotThread, threadSnapshotKey } from "./thread-cache.js";
import { refreshAscii } from "./ascii.js";
import {
  dismissOpenMobileMenuForNavigation,
  initLayoutUI,
  syncLocationFromStoredPrefs,
  syncMobileAppNavHeight,
  wireAvatarImageFallbacks,
} from "./layout.js";
import { replaceRouteOutletHTML, scrollRouteToTop } from "./shell-swap.js";
import { closestLink, routeKind, shouldInterceptLink, withRelays } from "./nav-routing.js";
import { initViewMore } from "./notes.js";
import { bindProfileStatLinks } from "./profile-tabs.js";
import { initRelaysPage } from "./relays.js";
import { refreshVisibleFeedNoteMetadata } from "./feed-metadata.js";
import {
  feedLoaderMarkup,
  feedRightRail,
  readsRightRail,
  renderRouteOutletLayout,
  skeletonWaveStackMarkup,
  staticRightRail,
  threadTreeSkeletonMarkup,
} from "./shell.js";
import {
  feedSortForSession,
  getFeedSortPref,
} from "./sort-prefs.js";
import { initThreadPage, maybeScrollThreadPageToRootForNavigation, teardownThreadTreeConnector } from "./thread.js";
import { addAsciiWidthHint } from "./ascii-width-hint.js";
import {
  clearFragmentPrefetch,
  fragmentPrefetchCache as prefetchCache,
} from "./prefetch.js";

const main = document.querySelector("[data-nav-root]");

/** Mirrors server default sort when `sort` is absent. Reads from the URL
 *  first (back-compat for old bookmarks), then localStorage, then the server
 *  default for the current session pubkey. */
function effectiveFeedSortFromURL(url) {
  const pubkey = normalizedPubkey();
  const raw = url.searchParams.get("sort") || getFeedSortPref() || "";
  return feedSortForSession(pubkey, raw) || "recent";
}
const prefetchQueue = [];
let prefetchActive = 0;
const maxPrefetchConcurrency = 2;
// Guest first-page fragments can exceed 12s on cold WoT cohort resolution; keep
// under browser/proxy oddities while avoiding false timeouts on slow cache fills.
const fragmentRequestTimeoutMs = 25000;
const newerFragmentPollMs = 30000;
let currentRoute = routeKind(window.location.pathname);
let feedPollTimer = 0;
let feedPollInFlight = false;
let profilePostsPollTimer = 0;
let profilePostsPollInFlight = false;
let asciiRefreshFrame = 0;
let profileFollowBaseURL = null;
let profileFollowControlsBound = false;
const routesNeedingAsciiRefresh = new Set(["feed", "thread", "bookmarks", "notifications", "profile", "search", "tag"]);
const boundProfileLazyInputs = new WeakSet();

/** Serializes in-app navigations so a second `navigate` cannot run while the first is awaiting hydration (avoids corrupt feed snapshots). */
let navigateChain = Promise.resolve();

// Feed hydrate reload helpers must run before `if (main)` — that block calls
// `bootstrapInitialRoute()` synchronously, which reads the consts below.
function feedHasServerLoader(root = main) {
  if (!root) return false;
  const feed = root.querySelector("[data-feed]");
  if (!feed) return false;
  return Boolean(feed.querySelector("[data-feed-loader]"));
}

const feedHydrateReloadStorageKey = "ptxt_feed_hydrate_reload_once";
/** Survives `location.reload()` when sessionStorage is unavailable; must not collide with app uses of window.name (grep is clean). */
const feedHydrateReloadWindowNameSentinel = "ptxt:feed-hydrate-reload-once";

function feedHydrateReloadSessionStorageWorks() {
  try {
    const probe = `${feedHydrateReloadStorageKey}:probe`;
    sessionStorage.setItem(probe, "1");
    sessionStorage.removeItem(probe);
    return true;
  } catch {
    return false;
  }
}

function feedHydrateReloadAlreadyRetried() {
  try {
    if (sessionStorage.getItem(feedHydrateReloadStorageKey) === "1") {
      return true;
    }
  } catch {
    // ignore
  }
  return window.name === feedHydrateReloadWindowNameSentinel;
}

/** Clears retry markers after a successful fragment hydration (and tab-normal window.name). */
function feedHydrateReloadClearFlags() {
  try {
    sessionStorage.removeItem(feedHydrateReloadStorageKey);
  } catch {
    // ignore
  }
  if (window.name === feedHydrateReloadWindowNameSentinel) {
    window.name = "";
  }
}

/**
 * Persists "reload once" across refresh; returns true if reload was issued.
 * If sessionStorage cannot persist and window.name is already in use, skips reload to avoid an infinite loop.
 */
function feedHydrateReloadMarkAndReload() {
  if (feedHydrateReloadSessionStorageWorks()) {
    try {
      sessionStorage.setItem(feedHydrateReloadStorageKey, "1");
      window.location.reload();
      return true;
    } catch {
      // fall through to window.name or skip
    }
  }
  if (window.name && window.name !== feedHydrateReloadWindowNameSentinel) {
    return false;
  }
  window.name = feedHydrateReloadWindowNameSentinel;
  window.location.reload();
  return true;
}

if (main) {
  document.addEventListener("click", (event) => {
    const link = closestLink(event.target);
    if (!shouldInterceptLink(event, link, main)) return;
    event.preventDefault();
    const nextURL = withRelays(link.href);
    const fromMainMenu = link.hasAttribute("data-main-menu-link");
    void navigate(nextURL, { push: true, fromMainMenu });
  });

  window.addEventListener("popstate", () => {
    void navigate(withRelays(window.location.href), { push: false, fromMainMenu: false });
  });

  window.addEventListener("ptxt:navigate", (event) => {
    const href = event?.detail?.href;
    if (!href) return;
    void navigate(withRelays(href), { push: true, fromMainMenu: false });
  });

  const prefetchEvents = ["pointerenter", "focus", "pointerdown"];
  prefetchEvents.forEach((name) => {
    document.addEventListener(name, (event) => {
      const link = closestLink(event.target);
      if (!link) return;
      const href = withRelays(link.href);
      const route = routeKind(new URL(href, window.location.origin).pathname);
      if (!route) return;
      scheduleRoutePrefetch(href, route);
    }, true);
  });

  window.addEventListener("ptxt:session", clearRouteSnapshots);
  const onViewerTransportPrefsChanged = () => {
    clearRouteSnapshots();
    clearFragmentPrefetch();
    void rehydrateCurrentRouteForPrefChange();
  };
  for (const evt of ["ptxt:relays", "ptxt:web-of-trust-changed", "ptxt:viewer-prefs-changed"]) {
    window.addEventListener(evt, onViewerTransportPrefsChanged);
  }
  if (currentRoute === "thread") {
    initThreadPage();
  }
  void bootstrapInitialRoute();
}

function navigate(href, { push, fromMainMenu = false } = {}) {
  const task = navigateChain.then(() => navigateImpl(href, { push, fromMainMenu }));
  navigateChain = task.catch(() => {});
  return task;
}

async function navigateImpl(href, { push, fromMainMenu = false }) {
  const url = new URL(href, window.location.origin);
  const route = routeKind(url.pathname);
  if (route === "profile") {
    url.searchParams.delete("cursor");
    url.searchParams.delete("cursor_id");
  }
  const effectiveHref = `${url.pathname}${url.search}${url.hash}`;
  if (!route) {
    window.location.href = href;
    return;
  }
  let currentHref = `${window.location.pathname}${window.location.search}${window.location.hash}`;
  if (isFeedLikePath(window.location.pathname)) {
    const cur = new URL(window.location.href);
    currentHref = `${cur.pathname}${cur.search}${cur.hash}`;
  }
  if (push && currentHref === effectiveHref) {
    return;
  }
  const prevThreadPathNoteId =
    push && currentRoute === "thread" && route === "thread"
      ? (() => {
          const m = window.location.pathname.match(/^\/thread\/([^/]+)/);
          return m ? m[1].toLowerCase() : "";
        })()
      : "";
  // Only snapshot when we're leaving this URL via in-app navigation (push).
  // On popstate, location already reflects the destination while the DOM is
  // still the previous entry — snapshotting would corrupt the cache keyed
  // by the new URL and break back/forward restore.
  if (push && currentRoute === "feed" && main) {
    snapshotFeed(withRelays(window.location.href), main);
  }
  if (push && currentRoute === "thread" && main) {
    snapshotThread(withRelays(window.location.href), main);
  }
  if (push && currentRoute === "profile" && main) {
    snapshotProfile(withRelays(window.location.href), main);
  }
  if (route !== "feed") {
    stopFeedPolling();
  }
  if (route !== "profile") {
    stopProfilePostsPolling();
  }
  if (push) {
    history.pushState({}, "", effectiveHref);
  } else if (currentHref !== effectiveHref) {
    history.replaceState({}, "", effectiveHref);
  }
  const intraThreadHistory =
    !push &&
    route === "thread" &&
    currentRoute === "thread" &&
    threadSnapshotKey(url.toString()) === threadSnapshotKey(window.location.href);
  const restoredFeed = route === "feed" ? restoreFeed(url.toString(), main) : null;
  const restoredThread = route === "thread" ? restoreThread(url.toString(), main) : null;
  const restoredProfile = route === "profile" ? restoreProfile(url.toString(), main) : null;
  if (!restoredFeed && !restoredThread && !restoredProfile && !intraThreadHistory) {
    renderShell(route, url);
  }
  if (route === "thread" && !restoredThread) {
    scheduleAsciiRefresh(main);
  }
  if (push && fromMainMenu && route === "reads") {
    requestAnimationFrame(() => {
      scrollRouteToTop(main);
    });
  }
  dismissOpenMobileMenuForNavigation(main);
  try {
    await hydrateRoute(route, url, {
      restoredFeed: Boolean(restoredFeed),
      restoredThread: Boolean(restoredThread),
      restoredProfile: Boolean(restoredProfile),
      intraThreadHistory,
    });
    if (route === "thread" && push) {
      maybeScrollThreadPageToRootForNavigation(effectiveHref, prevThreadPathNoteId, main);
    }
    currentRoute = route;
  } catch {
    window.location.href = effectiveHref;
  }
}

function canonicalURLFromLocation() {
  return new URL(window.location.href);
}

async function bootstrapInitialRoute() {
  // Run before first fragment fetches: hydrateRoute calls loadFeedFragments
  // before initLayoutUI, so address-bar cleanup must happen here too.
  if (shouldSyncViewerPrefLocation(window.location.pathname)) {
    syncLocationFromStoredPrefs();
  }
  const url = canonicalURLFromLocation();
  const currentHref = `${window.location.pathname}${window.location.search}${window.location.hash}`;
  const effectiveHref = `${url.pathname}${url.search}${url.hash}`;
  if (effectiveHref !== currentHref) {
    history.replaceState({}, "", effectiveHref);
    try {
      if (currentRoute !== "feed" && currentRoute !== "reads") {
        window.location.href = effectiveHref;
        return;
      }
      await hydrateRoute(currentRoute, url, { restoredFeed: false });
      feedHydrateReloadClearFlags();
    } catch (error) {
      console.error("Initial route bootstrap failed", error);
      window.location.href = effectiveHref;
    }
    return;
  }
  if (currentRoute === "feed" && feedHasServerLoader()) {
    const alreadyRetriedFeedHydrate = feedHydrateReloadAlreadyRetried();
    try {
      await hydrateRoute(currentRoute, url, { restoredFeed: false });
      feedHydrateReloadClearFlags();
      return;
    } catch (error) {
      console.error("Initial feed hydration failed", error);
      if (!alreadyRetriedFeedHydrate && feedHydrateReloadMarkAndReload()) {
        return;
      }
      if (window.name === feedHydrateReloadWindowNameSentinel) {
        window.name = "";
      }
    }
  }
  rehydrateRouteUI(currentRoute, url, false, { skipAsciiRefresh: true });
  if (currentRoute === "feed" && !feedHasServerLoader()) {
    void refreshVisibleFeedNoteMetadata(main, url);
  }
  if (currentRoute === "feed") startFeedPolling(url, true);
}

async function hydrateRoute(route, url, options = {}) {
  const {
    restoredFeed = false,
    restoredThread = false,
    restoredProfile = false,
    intraThreadHistory = false,
  } = options;
  if (route === "feed") {
    if (restoredFeed) {
      void refreshVisibleFeedNoteMetadata(main, url);
    } else {
      await loadFeedFragments(url);
    }
  }
  if (route === "profile") {
    if (restoredProfile) {
      void refreshVisibleFeedNoteMetadata(main, url, { feedSelector: "#user-panel-posts [data-feed]" });
    } else {
      await loadProfileFragments(url);
    }
  }
  if (route === "thread" && !restoredThread && !intraThreadHistory) {
    await loadThreadFragments(url);
  }
  if (route === "reads") await loadReadsFragments(url);
  if (route === "bookmarks") await loadMainShellFragment(url);
  if (route === "notifications") await loadMainShellFragment(url);
  if (route === "search") await loadMainShellFragment(url);
  if (route === "tag") await loadMainShellFragment(url);
  if (route === "stub") await loadMainShellFragment(url);
  if (route === "relays") await loadRelaysFragment(url);
  updateSessionLinks();
  updateRelayAwareLinks();
  rehydrateRouteUI(route, url, restoredFeed, { restoredProfile });
  if (route === "feed") startFeedPolling(url, true);
  if (route === "thread") syncMobileAppNavHeight();
}

function setMainHTMLPreservingRailUser(html) {
  replaceRouteOutletHTML(main, html);
}

/** Menu markup is preserved across navigations; keep search field aligned with the current URL. */
function syncPreservedShellChromeFromURL(root, url) {
  if (!root || !url) return;
  const q = url.searchParams.get("q") || "";
  const input = root.querySelector('.mobile-menu-search input[name="q"]');
  if (input instanceof HTMLInputElement && input.value !== q) input.value = q;
}

function clearRouteSnapshots() {
  clearFeedSnapshots();
  clearThreadSnapshots();
  clearProfileSnapshots();
}

/**
 * Re-runs the current route's hydration pipeline. Used when the viewer
 * toggles a localStorage preference (sort, tf, wot, relays, …) so the SPA
 * picks up the new `X-Ptxt-*` header value without a URL change. Routes that
 * don't have a corresponding hydrator (e.g. /settings) become no-ops here.
 */
async function rehydrateCurrentRouteForPrefChange() {
  if (!main) return;
  const route = routeKind(window.location.pathname);
  if (!route) return;
  const url = canonicalURLFromLocation();
  try {
    await hydrateRoute(route, url, {
      restoredFeed: false,
      restoredThread: false,
      restoredProfile: false,
      intraThreadHistory: false,
    });
  } catch {
    // Stay on the current view if the re-hydration fails; the user can
    // refresh manually. Throwing here would replace the route with the bare
    // /href page load, which is more disruptive than a stale view.
  }
}

function replaceThreadShellContent(body) {
  const shell = main.querySelector(".app-shell");
  if (!shell) {
    setMainHTMLPreservingRailUser(
      renderRouteOutletLayout({
        mainContent: body,
      }),
    );
    return;
  }
  const template = document.createElement("template");
  template.innerHTML = body.trim();
  const nextFeedColumn = template.content.querySelector(".feed-column");
  const nextRightRail = template.content.querySelector(".right-rail");
  const currentFeedColumn = shell.querySelector(".feed-column");
  const currentRightRail = shell.querySelector(".right-rail");
  if (!nextFeedColumn || !nextRightRail || !currentFeedColumn || !currentRightRail) {
    setMainHTMLPreservingRailUser(
      renderRouteOutletLayout({
        mainContent: body,
      }),
    );
    return;
  }
  currentFeedColumn.replaceWith(nextFeedColumn);
  currentRightRail.replaceWith(nextRightRail);
}

function renderShell(route, url) {
  if (route === "feed") {
    setMainHTMLPreservingRailUser(renderRouteOutletLayout({
      active: "/",
      mainContent: `
        <section class="feed-column">
          <section id="feed-heading" data-feed-heading>
            <p class="text-skeleton text-skeleton-block" aria-hidden="true">Nostr Feed</p>
            <p class="text-skeleton text-skeleton-block" aria-hidden="true">----------------------------------------------</p>
          </section>
          <button class="feed-new-notes" type="button" data-new-notes hidden>Show <span data-new-notes-count>0</span> new notes</button>
          <section id="feed" class="feed" data-feed>
            ${feedLoaderMarkup()}
          </section>
          <button class="load-more" data-load-more data-feed-url="/feed" data-fragment="1" data-cursor="" data-cursor-id="" type="button" hidden>Load more</button>
        </section>
      `,
      rightRail: feedRightRail("24h", url.searchParams.get("q") || ""),
    }));
    return;
  }
  if (route === "reads") {
    setMainHTMLPreservingRailUser(renderRouteOutletLayout({
      active: "/reads",
      shellClass: "reads-shell",
      mainContent: `
        <section class="feed-column reads-column">
          <section id="reads-heading" data-reads-heading>
            <p class="text-skeleton text-skeleton-block" aria-hidden="true">----------------------------------------------</p>
            <p class="text-skeleton text-skeleton-block" aria-hidden="true">----------------------------------------------</p>
          </section>
          <section id="reads-list" class="reads-list" data-reads>
            <div class="text-skeleton-stack" aria-hidden="true">
              <p class="text-skeleton text-skeleton-block">----------------------------</p>
              <p class="text-skeleton text-skeleton-block">------------------------</p>
            </div>
          </section>
          <p class="reads-more">
            <button class="load-more" type="button" data-load-more data-feed-url="/reads" data-fragment="1" data-cursor="" data-cursor-id="" hidden>Load more reads</button>
          </p>
        </section>
      `,
      rightRail: readsRightRail("24h", url.searchParams.get("q") || ""),
    }));
    return;
  }
  if (route === "bookmarks") {
    setMainHTMLPreservingRailUser(renderRouteOutletLayout({
      active: "/bookmarks",
      mainContent: `
        <section class="feed-column shell-main-top" data-shell-main>
          <section class="page-heading">
            <h1 class="text-skeleton">---------</h1>
            <p class="text-skeleton text-skeleton-block" aria-hidden="true">------------------------------------------</p>
          </section>
          <section class="feed" data-feed>
            <div class="text-skeleton-stack" aria-hidden="true">
              <pre class="ascii-card text-skeleton-note">+- ---------------- -- ---- --------------------------------------+
| --------------------------------------------------------------- |
| -----------------------------------------------                 |
+-- ----- [-- -------] -------------------------------------- ----+</pre>
            </div>
          </section>
        </section>
      `,
      rightRail: staticRightRail(url.searchParams.get("q") || "", { trending: false }),
    }));
    return;
  }
  if (route === "notifications") {
    setMainHTMLPreservingRailUser(renderRouteOutletLayout({
      active: "/notifications",
      mainContent: `
        <section class="feed-column shell-main-top" data-shell-main>
          <section class="page-heading">
            <h1 class="text-skeleton">--------------</h1>
            <p class="text-skeleton text-skeleton-block" aria-hidden="true">------------------------------------------</p>
          </section>
          <section class="feed" data-feed>
            <div class="text-skeleton-stack" aria-hidden="true">
              <pre class="ascii-card text-skeleton-note">+- ---------------- -- ---- --------------------------------------+
| --------------------------------------------------------------- |
| -----------------------------------------------                 |
+-- ----- [-- -------] -------------------------------------- ----+</pre>
            </div>
          </section>
        </section>
      `,
      rightRail: staticRightRail(url.searchParams.get("q") || "", { trending: false }),
    }));
    return;
  }
  if (route === "search") {
    setMainHTMLPreservingRailUser(renderRouteOutletLayout({
      active: url.pathname,
      mainContent: `
        <section class="feed-column shell-main-top" data-shell-main>
          <section class="page-heading search-heading">
            <h1 class="text-skeleton">------</h1>
            <p class="text-skeleton text-skeleton-block" aria-hidden="true">------------------------------------------</p>
            <p class="text-skeleton text-skeleton-block" aria-hidden="true">------------------------------------------</p>
          </section>
          <section class="feed search-results" data-feed data-search-results>
            <div class="text-skeleton-stack" aria-hidden="true">
              <pre class="ascii-card text-skeleton-note">+- ---------------- -- ---- --------------------------------------+
| --------------------------------------------------------------- |
| -----------------------------------------------                 |
+-- ----- [-- -------] -------------------------------------- ----+</pre>
            </div>
          </section>
        </section>
      `,
      rightRail: staticRightRail(url.searchParams.get("q") || "", { trending: false }),
    }));
    return;
  }
  if (route === "tag") {
    setMainHTMLPreservingRailUser(renderRouteOutletLayout({
      active: url.pathname,
      mainContent: `
        <section class="feed-column shell-main-top" data-shell-main>
          <section class="page-heading search-heading" data-tag-heading>
            <h1 class="text-skeleton">---------</h1>
            <p class="text-skeleton text-skeleton-block" aria-hidden="true">------------------------------------------</p>
            <p class="text-skeleton text-skeleton-block" aria-hidden="true">------------------------------------------</p>
          </section>
          <section class="feed search-results" data-feed data-tag-results>
            <div class="text-skeleton-stack" aria-hidden="true">
              <pre class="ascii-card text-skeleton-note">+- ---------------- -- ---- --------------------------------------+
| --------------------------------------------------------------- |
| -----------------------------------------------                 |
+-- ----- [-- -------] -------------------------------------- ----+</pre>
            </div>
          </section>
        </section>
      `,
      rightRail: staticRightRail(url.searchParams.get("q") || ""),
    }));
    return;
  }
  if (route === "stub") {
    const path = url.pathname;
    const hideTrendingRail = path === "/about" || path === "/settings";
    setMainHTMLPreservingRailUser(renderRouteOutletLayout({
      active: url.pathname,
      mainContent: `
        <section class="feed-column shell-main-top" data-shell-main>
          <section class="page-heading">
            <h1 class="text-skeleton">--------------</h1>
            <p class="text-skeleton text-skeleton-block" aria-hidden="true">------------------------------------------</p>
          </section>
        </section>
      `,
      rightRail: staticRightRail(url.searchParams.get("q") || "", { trending: !hideTrendingRail }),
    }));
    return;
  }
  if (route === "relays") {
    renderShell("stub", url);
    return;
  }
  if (route === "thread") {
    setMainHTMLPreservingRailUser(renderThreadShell());
    return;
  }
  if (route === "profile") {
    setMainHTMLPreservingRailUser(renderRouteOutletLayout({
      mainContent: `
        <section class="feed-column user-profile-column">
          <section id="user-header" data-user-fragment="header">
            <section class="profile profile-modern profile-skeleton" aria-hidden="true">
              <div class="profile-hero">
                <span class="profile-back text-skeleton">&lt;-- ----</span>
                <div class="profile-avatar-wrap">
                  <div class="profile-avatar profile-avatar-fallback profile-skeleton-avatar">@</div>
                </div>
                <span class="profile-display-name text-skeleton profile-skeleton-display-name">----------------</span>
                <div class="profile-actions">
                  <span class="profile-action-link text-skeleton">-----------</span>
                </div>
              </div>
              <div class="profile-main">
                <div class="profile-ident">
                  <p class="text-skeleton text-skeleton-block">-------------------------------------------</p>
                  <p class="text-skeleton text-skeleton-block">-----------------------------</p>
                  <p class="text-skeleton text-skeleton-block">------------------------------------------------------</p>
                </div>
              </div>
            </section>
          </section>
          <section id="user-stats" class="stats profile-stats-row" data-user-fragment="stats">
            <span class="text-skeleton" aria-hidden="true">-------------------------------</span>
          </section>
          <div class="user-tabs profile-tabs">
            <nav class="user-tab-nav" aria-label="Profile timeline">
              <label class="user-tab-label" for="user-tab-posts">Posts</label><span class="user-tab-sep" aria-hidden="true">·</span>
              <label class="user-tab-label" for="user-tab-replies">Replies</label><span class="user-tab-sep" aria-hidden="true">·</span>
              <label class="user-tab-label" for="user-tab-media">Media</label><span class="user-tab-sep profile-mobile-only-tab" aria-hidden="true">·</span>
              <label class="user-tab-label profile-mobile-only-tab" for="user-tab-identifiers">Identities</label><span class="user-tab-sep profile-mobile-only-tab" aria-hidden="true">·</span>
              <label class="user-tab-label profile-mobile-only-tab" for="user-tab-relays">Relays</label>
            </nav>
            <input type="radio" name="user-tab" id="user-tab-posts" class="user-tab-state" checked>
            <section class="user-tab-panel" id="user-panel-posts" data-user-fragment="posts">
              <button class="feed-new-notes" type="button" data-profile-new-notes hidden>Show <span data-profile-new-notes-count>0</span> newer posts</button>
              <div class="feed profile-feed-skeleton" data-feed>
                ${skeletonWaveStackMarkup()}
              </div>
              <button class="load-more" data-load-more data-feed-url="${url.pathname}" data-fragment="posts" data-cursor="" data-cursor-id="" type="button" hidden>Load more</button>
            </section>
            <input type="radio" name="user-tab" id="user-tab-replies" class="user-tab-state">
            <section class="user-tab-panel" id="user-panel-replies" data-user-fragment="replies"><div class="feed"><p class="text-skeleton text-skeleton-block" aria-hidden="true">------------------------------</p></div></section>
            <input type="radio" name="user-tab" id="user-tab-media" class="user-tab-state">
            <section class="user-tab-panel" id="user-panel-media" data-user-fragment="media"><div class="feed"><p class="text-skeleton text-skeleton-block" aria-hidden="true">----------------------------</p></div></section>
            <input type="radio" name="user-tab" id="user-tab-following" class="user-tab-state">
            <section class="user-tab-panel" id="user-panel-following" data-user-fragment="following"><p class="text-skeleton text-skeleton-block" aria-hidden="true">-------------------------------</p></section>
            <input type="radio" name="user-tab" id="user-tab-followers" class="user-tab-state">
            <section class="user-tab-panel" id="user-panel-followers" data-user-fragment="followers"><p class="text-skeleton text-skeleton-block" aria-hidden="true">--------------------------------</p></section>
            <input type="radio" name="user-tab" id="user-tab-identifiers" class="user-tab-state">
            <section class="user-tab-panel profile-mobile-only-panel" id="user-panel-identifiers" data-user-fragment="identifiers"><p class="text-skeleton text-skeleton-block" aria-hidden="true">------------------------------</p></section>
            <input type="radio" name="user-tab" id="user-tab-relays" class="user-tab-state">
            <section class="user-tab-panel profile-mobile-only-panel" id="user-panel-relays" data-user-fragment="relays"><p class="text-skeleton text-skeleton-block" aria-hidden="true">---------------------------</p></section>
          </div>
        </section>
        <aside class="right-rail profile-right-rail">
          <section class="profile-card profile-right-panel profile-right-panel-skeleton" id="user-right-identifiers" data-user-fragment="identifiers">
            <div class="text-skeleton-stack" aria-hidden="true">
              <p class="text-skeleton text-skeleton-block">Identifiers</p>
              <p class="text-skeleton text-skeleton-block">npub: ------------------------------------</p>
              <p class="text-skeleton text-skeleton-block">hex:  ------------------------------------</p>
              <p class="text-skeleton text-skeleton-block">------------------------------------------</p>
            </div>
          </section>
          <section class="profile-card profile-right-panel profile-right-panel-skeleton" id="user-right-relays" data-user-fragment="relays">
            <div class="text-skeleton-stack" aria-hidden="true">
              <p class="text-skeleton text-skeleton-block">---------------------------------------------</p>
              <p class="text-skeleton text-skeleton-block">* ---------------------  ---  ----</p>
              <p class="text-skeleton text-skeleton-block">* ---------------------  ---  ----</p>
              <p class="text-skeleton text-skeleton-block">* ---------------------  ---  ----</p>
              <p class="text-skeleton text-skeleton-block">* ---------------------  ---  ----</p>
              <p class="text-skeleton text-skeleton-block">* ---------------------  ---  ----</p>
            </div>
          </section>
        </aside>
      `,
    }));
    bindProfileStatLinks();
    bindProfileLazyTabs(url);
    return;
  }
}

function renderThreadShell() {
  return renderRouteOutletLayout({
    mainContent: `
      <section class="feed-column">
        <section id="thread-summary" data-thread-fragment="summary">
          <div class="thread-toolbar-slot" aria-hidden="true"></div>
          <section class="thread-header" aria-hidden="true">
            <div class="thread-header-top">
              <p class="thread-back thread-back-primary text-skeleton">&lt;-- ---- -- ---- ----</p>
              <p class="thread-header-op text-skeleton">-- -- --</p>
              <div class="thread-header-actions">
                <span class="text-skeleton">--- ----</span>
              </div>
            </div>
          </section>
          <div class="thread-summary">
            <p class="text-skeleton">-------------------------</p>
          </div>
        </section>
        <section id="thread-tree-view" data-thread-fragment="tree" hidden>${threadTreeSkeletonMarkup()}</section>
        <section id="thread-ancestors" data-thread-fragment="ancestors"></section>
        <section id="thread-focus" data-thread-fragment="focus">
          <section class="thread-focus thread-focus-skeleton" aria-hidden="true">
            ${skeletonWaveStackMarkup()}
          </section>
        </section>
        <section class="thread-replies">
          <div class="comments thread-replies-skeleton" id="thread-replies" data-thread-fragment="replies">
            ${skeletonWaveStackMarkup()}
          </div>
          <button class="load-more" type="button" data-thread-load-more data-load-label="Load more thread replies" data-cursor="" data-cursor-id="" hidden>Load more thread replies</button>
        </section>
      </section>
      <aside class="right-rail" data-thread-fragment="participants">
        <section class="thread-people-panel">
          <h2>People in this thread</h2>
          <ul class="thread-people" aria-hidden="true">
            <li>
              <div class="thread-person">
                <span class="thread-person-avatar-skeleton" aria-hidden="true">@</span>
                <div class="thread-person-meta">
                  <strong class="text-skeleton">---------</strong>
                  <span class="text-skeleton text-skeleton-block">-------------------------</span>
                  <em class="text-skeleton">------</em>
                </div>
              </div>
            </li>
            <li>
              <div class="thread-person">
                <span class="thread-person-avatar-skeleton" aria-hidden="true">@</span>
                <div class="thread-person-meta">
                  <strong class="text-skeleton">--------</strong>
                  <span class="text-skeleton text-skeleton-block">-----------------------</span>
                  <em class="text-skeleton">------</em>
                </div>
              </div>
            </li>
          </ul>
        </section>
      </aside>
    `,
  });
}

async function loadMainShellFragment(url) {
  const fragment = await fetchFragment(url, "main");
  const mainShell = main.querySelector("[data-shell-main]");
  if (mainShell) mainShell.outerHTML = fragment.body;
}

async function loadRelaysFragment(url) {
  await loadMainShellFragment(url);
  initRelaysPage(main);
}

/** Avoid replacing a populated list with an empty-looking fragment (errors, cold cache).
 *  Always replace when the SSR deferred shell loader is still present, so the loader can
 *  be cleared even if the cold-start fragment came back empty. */
function replaceListFragmentBody(container, html, { rowSelector, fragmentMarker }) {
  if (!container) return;
  const hasRows = Boolean(container.querySelector(rowSelector));
  const fragmentHasRows = html.includes(fragmentMarker);
  const deferredShell = Boolean(container.querySelector("[data-feed-loader]"));
  if (hasRows && !fragmentHasRows && !deferredShell) return;
  container.innerHTML = html;
  wireAvatarImageFallbacks(container);
}

async function loadReadsFragments(url) {
  const listRequest = fetchFragment(url, "1");
  const headingRequest = fetchFragment(url, "heading");
  const railRequest = fetchFragment(url, "right-rail");

  const list = await listRequest;
  const readsList = main.querySelector("[data-reads]");
  replaceListFragmentBody(readsList, list.body, {
    rowSelector: ".read-article",
    fragmentMarker: "read-article",
  });
  const readButton = main.querySelector("[data-load-more][data-feed-url=\"/reads\"]");
  if (readButton) {
    readButton.dataset.cursor = list.cursor;
    readButton.dataset.cursorId = list.cursorID;
    readButton.dataset.hasMore = list.hasMore ? "1" : "0";
    readButton.hidden = !list.hasMore;
  }
  headingRequest
    .then((heading) => {
      const headingNode = main.querySelector("[data-reads-heading]");
      if (!headingNode) return;
      headingNode.innerHTML = heading.body;
      initLayoutUI(headingNode);
    })
    .catch(() => {});
  railRequest
    .then((rail) => {
      const railRoot = main.querySelector("[data-reads-right-rail]");
      if (!railRoot || !rail.body.trim()) return;
      const template = document.createElement("template");
      template.innerHTML = rail.body.trim();
      const nextAside = template.content.querySelector("aside");
      if (nextAside) railRoot.replaceWith(nextAside);
    })
    .catch(() => {});
}

async function loadFeedFragments(url) {
  const feed = main.querySelector("[data-feed]");
  const hadDeferredLoader = Boolean(feed?.querySelector("[data-feed-loader]"));

  const headingRequest = fetchFragment(url, "heading");
  const trendingRequest = fetchTrendingFragment(url);

  let notes;
  try {
    notes = await fetchFragment(url, "1");
  } catch {
    invalidateFragmentPrefetch(url, "1");
    notes = await fetchFragment(url, "1");
  }
  // Cold-start guard: fragment may briefly return empty while the background warmer
  // populates the canonical guest cache; retry a few times before clearing the loader.
  if (hadDeferredLoader && !notes.body.includes('id="note-')) {
    const backoffMs = [500, 1500, 3500];
    for (let i = 0; i < backoffMs.length; i += 1) {
      await new Promise((resolve) => setTimeout(resolve, backoffMs[i]));
      invalidateFragmentPrefetch(url, "1");
      try {
        notes = await fetchFragment(url, "1");
      } catch {
        continue;
      }
      if (notes.body.includes('id="note-')) break;
    }
  }

  replaceListFragmentBody(feed, notes.body, {
    rowSelector: ".note[id^='note-']",
    fragmentMarker: 'id="note-',
  });
  const button = main.querySelector("[data-load-more]:not([data-feed-url=\"/reads\"])");
  if (button) {
    button.dataset.cursor = notes.cursor;
    button.dataset.cursorId = notes.cursorID;
    button.dataset.hasMore = notes.hasMore ? "1" : "0";
    button.hidden = !notes.hasMore;
  }

  headingRequest
    .then((heading) => {
      const headingNode = main.querySelector("[data-feed-heading]");
      if (!headingNode) return;
      headingNode.innerHTML = heading.body;
      initLayoutUI(headingNode);
    })
    .catch(() => {});
  trendingRequest
    .then((trending) => {
      const trendingTarget = main.querySelector("[data-trending-target]");
      if (trendingTarget) trendingTarget.innerHTML = trending.body;
    })
    .catch(() => {});
  void refreshVisibleFeedNoteMetadata(main, url);

  if (notes.snapshotStarter) {
    window.setTimeout(() => {
      invalidateFragmentPrefetch(url, "1");
      void fetchFragment(url, "1")
        .then((n2) => {
          const feedEl = main.querySelector("[data-feed]");
          if (!feedEl || !n2.body.includes('id="note-')) return;
          replaceListFragmentBody(feedEl, n2.body, {
            rowSelector: ".note[id^='note-']",
            fragmentMarker: 'id="note-',
          });
          const loadBtn = main.querySelector("[data-load-more]:not([data-feed-url=\"/reads\"])");
          if (loadBtn) {
            loadBtn.dataset.cursor = n2.cursor;
            loadBtn.dataset.cursorId = n2.cursorID;
            loadBtn.dataset.hasMore = n2.hasMore ? "1" : "0";
            loadBtn.hidden = !n2.hasMore;
          }
          void refreshVisibleFeedNoteMetadata(main, url);
        })
        .catch(() => {});
    }, 1500);
  }
}

async function fetchTrendingFragment(_baseURL) {
  // tf + relays now travel via X-Ptxt-Tf / X-Ptxt-Relays headers
  // (fetchWithSession reads them from localStorage on every request).
  const response = await fetchWithSession(`/trending?fragment=1`);
  if (!response.ok) throw new Error("fragment trending failed");
  return { body: await response.text() };
}

function bindNewNotesButton(url) {
  const existing = main.querySelector("[data-new-notes]");
  if (!existing) return;
  // Restored snapshots keep data-* attributes but not listeners, so clone to
  // guarantee a clean button before attaching handlers again.
  const button = existing.cloneNode(true);
  existing.replaceWith(button);
  const sort = effectiveFeedSortFromURL(url);
  if (sort !== "recent") {
    button.hidden = true;
    return;
  }
  if (button.dataset.loading === "1") {
    delete button.dataset.loading;
    button.classList.remove("is-pressed");
    button.disabled = false;
    button.removeAttribute("aria-busy");
  }
  const setLoadingState = (isLoading) => {
    if (isLoading) {
      button.dataset.loading = "1";
      button.classList.add("is-pressed");
      button.disabled = true;
      button.setAttribute("aria-busy", "true");
      return;
    }
    delete button.dataset.loading;
    button.classList.remove("is-pressed");
    button.disabled = false;
    button.removeAttribute("aria-busy");
  };
  button.addEventListener("click", async () => {
    if (button.dataset.loading === "1") return;
    setLoadingState(true);
    try {
      const newer = await fetchNewerNotes(url, { includeBody: true });
      const feed = main.querySelector("[data-feed]");
      if (feed && newer.body) {
        prependNewNotes(feed, newer.body);
        void refreshVisibleFeedNoteMetadata(main, url);
      }
      const top = feedTopCursor(main);
      button.dataset.topCursor = top.cursor;
      button.dataset.topCursorId = top.cursorID;
      button.dataset.pendingCount = "0";
      button.hidden = true;
    } catch {
      // Keep the existing button text/count visible so retry is obvious.
      button.hidden = false;
    } finally {
      setLoadingState(false);
    }
  });
  const top = feedTopCursor(main);
  button.dataset.topCursor = top.cursor;
  button.dataset.topCursorId = top.cursorID;
}

function normalizeRestoredFeedControls() {
  const loadMore = main.querySelector("[data-load-more]:not([data-feed-url=\"/reads\"])");
  if (loadMore && loadMore.dataset.loading === "1") {
    delete loadMore.dataset.loading;
    loadMore.classList.remove("is-pressed");
    loadMore.disabled = false;
    loadMore.removeAttribute("aria-busy");
  }
  if (loadMore && loadMore.disabled && loadMore.dataset.hasMore !== "0") {
    loadMore.disabled = false;
  }
}

function normalizeRestoredProfileLoadMore() {
  const loadMore = main.querySelector("#user-panel-posts [data-load-more]");
  if (!loadMore) return;
  if (loadMore.dataset.loading === "1") {
    delete loadMore.dataset.loading;
    loadMore.classList.remove("is-pressed");
    loadMore.disabled = false;
    loadMore.removeAttribute("aria-busy");
  }
  if (loadMore.disabled && loadMore.dataset.hasMore !== "0") {
    loadMore.disabled = false;
  }
}

function rehydrateRouteUI(route, url, restoredFeed, options = {}) {
  const { skipAsciiRefresh = false, restoredProfile = false } = options;
  syncPreservedShellChromeFromURL(main, url);
  if (route === "feed" && restoredFeed) {
    normalizeRestoredFeedControls();
  }
  if (route === "profile" && restoredProfile) {
    normalizeRestoredProfileLoadMore();
    main.querySelectorAll("[data-profile-tab]").forEach((el) => {
      delete el.dataset.bound;
    });
    bindProfileStatLinks();
    bindProfileLazyTabs(url);
  }
  if (route === "feed") {
    bindNewNotesButton(url);
  }
  if (route === "profile") {
    bindProfileNewNotesButton(url);
    startProfilePostsPolling(url, true);
  }
  initFeedLoadMore(main);
  initLayoutUI(main);
  initViewMore(main);
  if (route === "thread") {
    initThreadPage();
  } else {
    teardownThreadTreeConnector();
  }
  if (!skipAsciiRefresh && routesNeedingAsciiRefresh.has(route)) {
    scheduleAsciiRefresh(main);
  }
}

function scheduleAsciiRefresh(root = document) {
  if (asciiRefreshFrame) {
    cancelAnimationFrame(asciiRefreshFrame);
  }
  asciiRefreshFrame = requestAnimationFrame(() => {
    asciiRefreshFrame = 0;
    refreshAscii(root);
  });
}

async function parseNewerFragmentResponse(response, errorMessage) {
  if (!response.ok) throw new Error(errorMessage);
  const count = Number.parseInt(response.headers.get("X-Ptxt-New-Count") || "0", 10) || 0;
  const body = response.status === 204 ? "" : await response.text();
  return { body, count };
}

async function fetchNewerNotes(baseURL, options = {}) {
  const button = main.querySelector("[data-new-notes]");
  const feedRef = new URL(baseURL, window.location.origin);
  const sort = effectiveFeedSortFromURL(feedRef);
  if (sort !== "recent") {
    return { body: "", count: 0 };
  }
  // tf / relays / wot flow through X-Ptxt-* headers (sessionHeaders), so we
  // only need to carry per-request bits in the URL.
  const requestURL = new URL("/feed", window.location.origin);
  requestURL.searchParams.set("fragment", "newer");
  requestURL.searchParams.set("since", button?.dataset.topCursor || "0");
  requestURL.searchParams.set("since_id", button?.dataset.topCursorId || "");
  if (options.includeBody) requestURL.searchParams.set("body", "1");
  const response = await fetchWithSession(requestURL.toString());
  return parseNewerFragmentResponse(response, "fragment newer failed");
}

async function fetchProfileNewerPosts(baseURL, options = {}) {
  const profileURL = new URL(baseURL, window.location.origin);
  if (!profileURL.pathname.startsWith("/u/")) {
    return { body: "", count: 0 };
  }
  const button = main.querySelector("[data-profile-new-notes]");
  const requestURL = new URL(profileURL.pathname, window.location.origin);
  requestURL.searchParams.set("fragment", "posts-newer");
  requestURL.searchParams.set("since", button?.dataset.topCursor || "0");
  requestURL.searchParams.set("since_id", button?.dataset.topCursorId || "");
  if (options.includeBody) requestURL.searchParams.set("body", "1");
  addAsciiWidthHint(requestURL.searchParams, requestURL.pathname);
  const response = await fetchWithSession(requestURL.toString());
  return parseNewerFragmentResponse(response, "profile posts-newer fragment failed");
}

function bindProfileNewNotesButton(url) {
  const existing = main.querySelector("[data-profile-new-notes]");
  if (!existing) return;
  const button = existing.cloneNode(true);
  existing.replaceWith(button);
  if (button.dataset.loading === "1") {
    delete button.dataset.loading;
    button.classList.remove("is-pressed");
    button.disabled = false;
    button.removeAttribute("aria-busy");
  }
  const setLoadingState = (isLoading) => {
    if (isLoading) {
      button.dataset.loading = "1";
      button.classList.add("is-pressed");
      button.disabled = true;
      button.setAttribute("aria-busy", "true");
      return;
    }
    delete button.dataset.loading;
    button.classList.remove("is-pressed");
    button.disabled = false;
    button.removeAttribute("aria-busy");
  };
  button.addEventListener("click", async () => {
    if (button.dataset.loading === "1") return;
    setLoadingState(true);
    try {
      const newer = await fetchProfileNewerPosts(url, { includeBody: true });
      const feed = main.querySelector("#user-panel-posts [data-feed]");
      if (feed && newer.body) {
        prependNewNotes(feed, newer.body);
        void refreshVisibleFeedNoteMetadata(main, url, { feedSelector: "#user-panel-posts [data-feed]" });
      }
      const top = profilePostsTopCursor(main);
      button.dataset.topCursor = top.cursor;
      button.dataset.topCursorId = top.cursorID;
      button.dataset.pendingCount = "0";
      button.hidden = true;
    } catch {
      button.hidden = false;
    } finally {
      setLoadingState(false);
    }
  });
  const top = profilePostsTopCursor(main);
  button.dataset.topCursor = top.cursor;
  button.dataset.topCursorId = top.cursorID;
}

function startProfilePostsPolling(url, runImmediately) {
  stopProfilePostsPolling();
  if (runImmediately) {
    void pollProfilePostsNewer(url);
  }
  profilePostsPollTimer = window.setInterval(() => {
    void pollProfilePostsNewer(url);
  }, newerFragmentPollMs);
}

function stopProfilePostsPolling() {
  if (!profilePostsPollTimer) return;
  window.clearInterval(profilePostsPollTimer);
  profilePostsPollTimer = 0;
}

async function pollProfilePostsNewer(url) {
  if (document.visibilityState !== "visible") return;
  if (profilePostsPollInFlight) return;
  const button = main.querySelector("[data-profile-new-notes]");
  if (!button) return;
  profilePostsPollInFlight = true;
  try {
    const newer = await fetchProfileNewerPosts(url);
    if (newer.count > 0) {
      const countNode = button.querySelector("[data-profile-new-notes-count]");
      if (countNode) countNode.textContent = `${newer.count}`;
      button.dataset.pendingCount = `${newer.count}`;
      button.hidden = false;
    }
  } finally {
    profilePostsPollInFlight = false;
  }
}

function startFeedPolling(url, runImmediately) {
  stopFeedPolling();
  const sort = effectiveFeedSortFromURL(url);
  if (sort !== "recent") {
    const button = main.querySelector("[data-new-notes]");
    if (button) button.hidden = true;
    return;
  }
  if (runImmediately) {
    void pollFeedNewNotes(url);
  }
  feedPollTimer = window.setInterval(() => {
    void pollFeedNewNotes(url);
  }, newerFragmentPollMs);
}


function stopFeedPolling() {
  if (!feedPollTimer) return;
  clearInterval(feedPollTimer);
  feedPollTimer = 0;
}

async function pollFeedNewNotes(url) {
  if (document.visibilityState !== "visible") return;
  if (feedPollInFlight) return;
  const button = main.querySelector("[data-new-notes]");
  if (!button) return;
  feedPollInFlight = true;
  try {
    const newer = await fetchNewerNotes(url);
    if (newer.count > 0) {
      const countNode = button.querySelector("[data-new-notes-count]");
      if (countNode) countNode.textContent = `${newer.count}`;
      button.dataset.pendingCount = `${newer.count}`;
      button.hidden = false;
    }
  } finally {
    feedPollInFlight = false;
  }
}

async function loadProfileFragments(url) {
  const [header, stats, identifiers, following, followers, relays, posts] = await Promise.all([
    fetchFragment(url, "header"),
    fetchFragment(url, "stats"),
    fetchFragment(url, "identifiers"),
    fetchFragment(url, "following"),
    fetchFragment(url, "followers"),
    fetchFragment(url, "relays"),
    fetchFragment(url, "posts"),
  ]);
  setFragmentHTML("#user-header", header.body);
  setFragmentHTML("#user-stats", stats.body);
  bindProfileStatLinks();
  setFragmentHTML("#user-panel-identifiers", identifiers.body);
  setFragmentHTML("#user-right-identifiers", identifiers.body);
  setFragmentHTML("#user-panel-following", following.body);
  const followingPanel = main.querySelector("#user-panel-following");
  if (followingPanel) followingPanel.dataset.loaded = "1";
  setFragmentHTML("#user-panel-followers", followers.body);
  const followersPanel = main.querySelector("#user-panel-followers");
  if (followersPanel) followersPanel.dataset.loaded = "1";
  setFragmentHTML("#user-panel-relays", relays.body);
  setFragmentHTML("#user-right-relays", relays.body);
  bindProfileFollowListControls(url);
  const postsPanel = main.querySelector("#user-panel-posts [data-feed]");
  if (postsPanel) {
    setFragmentNodeHTML(postsPanel, posts.body);
  }
  const button = main.querySelector("#user-panel-posts [data-load-more]");
  if (button) {
    button.dataset.cursor = posts.cursor;
    button.dataset.cursorId = posts.cursorID;
    button.dataset.hasMore = posts.hasMore ? "1" : "0";
    button.hidden = !posts.hasMore;
  }
}

function bindProfileFollowListControls(baseURL) {
  profileFollowBaseURL = new URL(baseURL, window.location.origin);
  if (profileFollowControlsBound) return;
  profileFollowControlsBound = true;
  main.addEventListener("submit", (event) => {
    const form = event.target instanceof HTMLFormElement ? event.target : event.target?.closest?.("form");
    if (!form || !form.matches("[data-follow-fragment-form]")) return;
    event.preventDefault();
    const fragment = form.dataset.followFragment;
    if (!fragment) return;
    const data = new FormData(form);
    const query = `${data.get(`${fragment}_q`) || ""}`.trim();
    void refreshFollowListFragment(fragment, query, 1);
  });
  main.addEventListener("click", (event) => {
    const link = closestLink(event.target);
    if (!link || !link.matches("[data-follow-fragment-link]")) return;
    event.preventDefault();
    const fragment = link.dataset.followFragmentLink;
    if (!fragment) return;
    const targetURL = new URL(link.href, window.location.origin);
    const query = (targetURL.searchParams.get(`${fragment}_q`) || "").trim();
    const page = Number.parseInt(targetURL.searchParams.get(`${fragment}_page`) || "1", 10) || 1;
    void refreshFollowListFragment(fragment, query, page);
  });
}

async function refreshFollowListFragment(fragment, query, page) {
  if (!profileFollowBaseURL) return;
  const baseURL = new URL(profileFollowBaseURL.toString());
  baseURL.searchParams.delete("fragment");
  baseURL.searchParams.delete(`${fragment}_q`);
  baseURL.searchParams.delete(`${fragment}_page`);
  if (query) baseURL.searchParams.set(`${fragment}_q`, query);
  if (page > 1) baseURL.searchParams.set(`${fragment}_page`, `${page}`);
  const result = await fetchFragment(baseURL, fragment);
  const panelSelector = fragment === "following" ? "#user-panel-following" : "#user-panel-followers";
  const panel = main.querySelector(panelSelector);
  if (!panel) return;
  setFragmentNodeHTML(panel, result.body);
  panel.dataset.loaded = "1";
  updateRelayAwareLinks();
  const nextURL = new URL(window.location.href);
  if (query) {
    nextURL.searchParams.set(`${fragment}_q`, query);
  } else {
    nextURL.searchParams.delete(`${fragment}_q`);
  }
  if (page > 1) {
    nextURL.searchParams.set(`${fragment}_page`, `${page}`);
  } else {
    nextURL.searchParams.delete(`${fragment}_page`);
  }
  history.replaceState({}, "", `${nextURL.pathname}${nextURL.search}${nextURL.hash}`);
}

async function loadThreadFragments(url) {
  const bundle = await fetchFragment(url, "hydrate");
  if (bundle.navigate) {
    window.location.href = withRelays(bundle.navigate);
    return;
  }
  replaceThreadShellContent(bundle.body);
}

function bindProfileLazyTabs(url) {
  const mapping = [
    { id: "user-tab-replies", fragment: "replies", panel: "#user-panel-replies" },
    { id: "user-tab-media", fragment: "media", panel: "#user-panel-media" },
    { id: "user-tab-following", fragment: "following", panel: "#user-panel-following" },
    { id: "user-tab-followers", fragment: "followers", panel: "#user-panel-followers" },
    { id: "user-tab-identifiers", fragment: "identifiers", panel: "#user-panel-identifiers" },
    { id: "user-tab-relays", fragment: "relays", panel: "#user-panel-relays" },
  ];
  mapping.forEach(({ id, fragment, panel }) => {
    const input = main.querySelector(`#${id}`);
    if (!input) return;
    if (boundProfileLazyInputs.has(input)) return;
    boundProfileLazyInputs.add(input);
    input.addEventListener("change", () => {
      if (!input.checked) return;
      const target = main.querySelector(panel);
      if (!target || target.dataset.loaded === "1") return;
      void fetchFragment(url, fragment).then((result) => {
        setFragmentNodeHTML(target, result.body);
        target.dataset.loaded = "1";
        updateRelayAwareLinks();
      });
    });
  });
}

function setFragmentHTML(selector, body) {
  const node = main.querySelector(selector);
  if (!node) return;
  setFragmentNodeHTML(node, body);
}

function setFragmentNodeHTML(node, body) {
  if (!node) return;
  node.innerHTML = body;
  initViewMore(node);
  scheduleAsciiRefresh(node);
}

const PREFETCH_FRAGMENTS = {
  profile: ["header", "stats"],
  thread: ["hydrate"],
  feed: ["heading", "1"],
  reads: ["heading", "1", "right-rail"],
  bookmarks: ["main"],
  notifications: ["main"],
  search: ["main"],
  tag: ["main"],
};

function scheduleRoutePrefetch(href, route) {
  const url = new URL(href, window.location.origin);
  if (url.origin !== window.location.origin) {
    return;
  }
  const fragments = PREFETCH_FRAGMENTS[route] || ["main"];
  fragments.forEach((fragment) => {
    const key = fragmentKey(url, fragment);
    if (prefetchCache.has(key)) return;
    prefetchQueue.push(() => fetchFragment(url, fragment));
  });
  runPrefetchQueue();
}

function runPrefetchQueue() {
  while (prefetchActive < maxPrefetchConcurrency && prefetchQueue.length) {
    const job = prefetchQueue.shift();
    prefetchActive += 1;
    Promise.resolve(job())
      .catch(() => {})
      .finally(() => {
        prefetchActive -= 1;
        runPrefetchQueue();
      });
  }
}

function canonicalFragmentBaseURL(baseURL) {
  const u = new URL(baseURL, window.location.origin);
  stripViewerPrefSearchParams(u);
  return u;
}

function fragmentKeyFromNormalized(normalized, fragment) {
  return `${normalized.pathname}?${normalized.searchParams.toString()}::${fragment}`;
}

async function fetchFragment(baseURL, fragment) {
  const canonical = canonicalFragmentBaseURL(baseURL);
  const key = fragmentKeyFromNormalized(canonical, fragment);
  const cached = prefetchCache.get(key);
  if (cached) {
    try {
      return await withFragmentTimeout(cached, key);
    } catch {
      prefetchCache.delete(key);
    }
  }
  const requestURL = new URL(canonical);
  requestURL.searchParams.set("fragment", fragment);
  addAsciiWidthHint(requestURL.searchParams, requestURL.pathname);
  const controller = new AbortController();
  const abortTimer = setTimeout(() => controller.abort(), fragmentRequestTimeoutMs);
  const request = fetchWithSession(requestURL.toString(), { signal: controller.signal }).then(async (response) => {
    clearTimeout(abortTimer);
    const navigate = response.headers.get("X-Ptxt-Navigate");
    if (navigate) {
      return {
        body: "",
        navigate,
        cursor: "",
        cursorID: "",
        hasMore: false,
        snapshotStarter: false,
      };
    }
    if (!response.ok) throw new Error(`fragment ${fragment} failed`);
    return {
      body: await response.text(),
      navigate: "",
      cursor: response.headers.get("X-Ptxt-Cursor") || "",
      cursorID: response.headers.get("X-Ptxt-Cursor-Id") || "",
      hasMore: response.headers.get("X-Ptxt-Has-More") === "1",
      snapshotStarter: response.headers.get("X-Ptxt-Feed-Snapshot-Starter") === "1",
    };
  }).catch((error) => {
    clearTimeout(abortTimer);
    prefetchCache.delete(key);
    throw error;
  });
  prefetchCache.set(key, request);
  const out = await withFragmentTimeout(request, key);
  if (out.navigate) {
    prefetchCache.delete(key);
  }
  return out;
}

function fragmentKey(url, fragment) {
  return fragmentKeyFromNormalized(canonicalFragmentBaseURL(url), fragment);
}

function invalidateFragmentPrefetch(baseURL, fragment) {
  prefetchCache.delete(fragmentKey(baseURL, fragment));
}

function withFragmentTimeout(promise, key) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      prefetchCache.delete(key);
      reject(new Error("fragment request timed out"));
    }, fragmentRequestTimeoutMs);
    promise
      .then((value) => {
        clearTimeout(timer);
        resolve(value);
      })
      .catch((error) => {
        clearTimeout(timer);
        reject(error);
      });
  });
}
