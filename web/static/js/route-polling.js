import { feedTopCursor, profilePostsTopCursor } from "./feed.js";
import { countUnseenFeedEvents } from "./feed-pagination.js";
import {
  collectVisibleFeedNoteIds,
  fetchFeedNoteMetadataMaps,
  refreshVisibleFeedNoteMetadata,
} from "./feed-metadata.js";
import { persistHomeFeedPageSnapshot, visibleFeedNoteIDs } from "./feed-service.js";
import { routeKind } from "./nav-routing.js";
import { getCurrentRoute } from "./navigation-route-state.js";
import { fetchWithSession, normalizedPubkey } from "./session.js";
import { feedSortForSession, getFeedSortPref } from "./sort-prefs.js";
import { pageIsHidden, powerSaverActive } from "./power-mode.js";
import { canonicalHomeFeedURL } from "./viewer-pref-url.js";
import { createPendingTaskSlot } from "./pending-task.js";
import { refreshAscii } from "./ascii.js";
import { wireAvatarImageFallbacks } from "./layout.js";
import { refreshNIP05Verification } from "./nip05-verify.js";
import { displayName, nip05DisplayText, preferredAvatarURL } from "./profile-parse.js";
import { PROFILE_MEMORY_CACHE_UPDATED_EVENT, cachedProfile } from "./profile-memory-cache.js";
import { fetchProfiles } from "./relay-reads.js";
import { normalizePubkey } from "./relay-utils.js";
import {
  hideInlineRetroLoader,
  setRetroLoaderProgress,
  settleRetroLoader,
  showInlineRetroLoader,
} from "./retro-loader.js";
import {
  fetchClientProfileNewer,
  hydrateRelayNativeProfileTab,
  prependClientProfileNewer,
} from "./client-render.js";
import { updateRelayAwareLinks } from "./session.js";

const newerFragmentPollMs = 30000;
const saverNewerFragmentPollMs = 120000;
const feedLoaderRecoveryRetryMs = 4000;

let feedPollTimer = 0;
let feedPollInFlight = false;
let pendingRankedFeedRefresh = null;
let pendingRecentFeedRefresh = null;
let pendingRecentFeedPreparation = null;
let feedLoaderRecoveryInFlight = false;
let feedLoaderRecoveryTimer = 0;
let profilePostsPollTimer = 0;
let profilePostsPollInFlight = false;
const profileNewerPosts = createPendingTaskSlot();
const boundProfileLazyInputs = new WeakSet();
const profileFragmentInFlight = new WeakSet();
const profileFollowHydrationInFlight = new WeakSet();
let profileFollowCacheListenerBound = false;

function isFollowPanelElement(node) {
  return Boolean(
    node &&
    typeof node.querySelector === "function" &&
    typeof node.querySelectorAll === "function" &&
    typeof node.addEventListener === "function" &&
    "dataset" in node,
  );
}

function shouldAutoLoadFeedOnHydrate() {
  return false;
}

let currentRoot = null;

function defaultNavRoot() {
  return document.querySelector("[data-nav-root]") ?? document;
}

function root() {
  return currentRoot ?? defaultNavRoot();
}

function withRoot(explicit, fn) {
  const prev = currentRoot;
  currentRoot = explicit ?? defaultNavRoot();
  try {
    return fn();
  } finally {
    currentRoot = prev;
  }
}

async function withRootAsync(explicit, fn) {
  const prev = currentRoot;
  currentRoot = explicit ?? defaultNavRoot();
  try {
    return await fn();
  } finally {
    currentRoot = prev;
  }
}

function effectiveFeedSortFromURL(url) {
  const pubkey = normalizedPubkey();
  const raw = url.searchParams.get("sort") || getFeedSortPref() || "";
  return feedSortForSession(pubkey, raw) || "recent";
}
export function feedHasServerLoader(explicitRoot) {
  const r = explicitRoot ?? root();
  if (!r) return false;
  const feed = r.querySelector("[data-feed]");
  if (!feed) return false;
  return Boolean(feed.querySelector("[data-feed-loader]"));
}
function setPendingCountButton(button, count, countSelector) {
  if (!(button instanceof HTMLElement)) return;
  const normalizedCount = Math.max(0, Number(count) || 0);
  const countNode = countSelector ? button.querySelector(countSelector) : null;
  if (countNode) countNode.textContent = `${normalizedCount}`;
  button.dataset.pendingCount = `${normalizedCount}`;
  button.hidden = normalizedCount < 1;
}

function clearPendingCountButton(button, countSelector) {
  setPendingCountButton(button, 0, countSelector);
}

function suppressFeedPendingButtonWhileShellVisible(button) {
  if (!button?.matches?.("[data-new-notes]")) return false;
  if (!feedHasServerLoader()) return false;
  clearPendingCountButton(button, "[data-new-notes-count]");
  return true;
}

function startPendingButtonLoader(button, options) {
  const loader = showInlineRetroLoader(button, options);
  if (loader) {
    setRetroLoaderProgress(loader, {
      percent: 8,
    });
  }
  return loader;
}

function homeFeedElement(explicitRoot) {
  const r = explicitRoot ?? root();
  return r.querySelector("#feed[data-feed]") || r.querySelector(".feed-column [data-feed]");
}
export function bindNewNotesButton(url, explicitRoot = null) {
  const existing = root().querySelector("[data-new-notes]");
  if (!existing) return;
  // Restored snapshots keep data-* attributes but not listeners, so clone to
  // guarantee a clean button before attaching handlers again.
  const button = existing.cloneNode(true);
  existing.replaceWith(button);
  if (suppressFeedPendingButtonWhileShellVisible(button)) return;
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
  button.addEventListener("click", () => {
    void loadFeedNewerNotes(url, button, setLoadingState);
  });
  const top = feedTopCursor(root());
  button.dataset.topCursor = top.cursor;
  button.dataset.topCursorId = top.cursorID;
}

function currentHomeFeedMatches(urlLike) {
  const expected = canonicalHomeFeedURL(urlLike);
  const current = canonicalHomeFeedURL(window.location.href);
  if (!expected || !current) return false;
  return (
    current.pathname === expected.pathname &&
    current.searchParams.toString() === expected.searchParams.toString()
  );
}

function recentBatchCursorMatches(batch) {
  if (!batch) return false;
  const top = feedTopCursor(root());
  return Number(batch.topCursor || 0) === Number(top.cursor || 0) &&
    String(batch.topCursorID || "") === String(top.cursorID || "");
}

function preloadAvatarURL(url) {
  if (!url || typeof Image !== "function") return Promise.resolve();
  return new Promise((resolve) => {
    const image = new Image();
    let settled = false;
    const finish = () => {
      if (settled) return;
      settled = true;
      resolve();
    };
    image.onload = finish;
    image.onerror = finish;
    image.src = url;
    window.setTimeout(finish, 3000);
  });
}

async function prepareRecentFeedRefresh(url) {
  if (pendingRecentFeedPreparation) return pendingRecentFeedPreparation;
  const work = (async () => {
    const sort = effectiveFeedSortFromURL(url);
    if (sort !== "recent" || !currentHomeFeedMatches(url)) return null;
    const top = feedTopCursor(root());
    const visibleIds = collectVisibleFeedNoteIds(root(), "#feed[data-feed]");
    const {
      fetchNewerHomeFeedNotes,
      syncNewerHomeFeedFromRelays,
    } = await import("./feed-service.js");
    await syncNewerHomeFeedFromRelays({
      viewerPubkey: normalizedPubkey(),
      since: top.cursor,
      sort,
    });
    const events = await fetchNewerHomeFeedNotes({
      viewerPubkey: normalizedPubkey(),
      since: top.cursor,
      sinceID: top.cursorID,
      sort,
      visibleIds,
      skipSync: true,
    });
    if (!events.length || !currentHomeFeedMatches(url)) {
      pendingRecentFeedRefresh = null;
      return null;
    }
    const [{ fetchNoteProfiles }, { hydrateReferencedEvents }, renderModule] = await Promise.all([
      import("./note-profiles.js"),
      import("./note-references.js"),
      import("./note-event-render.js"),
    ]);
    const referencedByIDPromise = hydrateReferencedEvents(events).catch(() => new Map());
    const referencedByID = await referencedByIDPromise;
    const authorPubkeys = [...new Set([
      ...events,
      ...referencedByID.values(),
    ].map((event) => normalizePubkey(event?.pubkey)).filter(Boolean))];
    const noteIDs = events.map((event) => event.id).filter(Boolean);
    const [profiles, metadata] = await Promise.all([
      fetchNoteProfiles(authorPubkeys),
      fetchFeedNoteMetadataMaps(noteIDs, {
        viewerPubkey: normalizedPubkey(),
        sort,
      }),
    ]);
    await Promise.all(Object.values(profiles || {}).map((profile) => (
      preloadAvatarURL(preferredAvatarURL(profile))
    )));
    if (!currentHomeFeedMatches(url)) return null;
    const liveTop = feedTopCursor(root());
    if (Number(liveTop.cursor || 0) !== Number(top.cursor || 0) ||
      String(liveTop.cursorID || "") !== String(top.cursorID || "")) {
      return null;
    }
    pendingRecentFeedRefresh = {
      topCursor: top.cursor,
      topCursorID: top.cursorID,
      events,
      profiles,
      referencedByID,
      metadata,
      prependNoteFeed: renderModule.prependNoteFeed,
    };
    return pendingRecentFeedRefresh;
  })();
  pendingRecentFeedPreparation = work;
  try {
    return await work;
  } finally {
    if (pendingRecentFeedPreparation === work) pendingRecentFeedPreparation = null;
  }
}

function commitPreparedRecentFeed(url, button, batch) {
  const feed = root().querySelector("#feed[data-feed]") || root().querySelector("[data-feed]");
  if (!feed || !batch?.events?.length || !recentBatchCursorMatches(batch) || !currentHomeFeedMatches(url)) {
    return false;
  }
  batch.prependNoteFeed(feed, batch.events, batch.profiles, {
    referencedByID: batch.referencedByID,
    replyCounts: batch.metadata?.replyCounts,
    reactionStats: batch.metadata?.reactionStats,
    zapTotals: batch.metadata?.zapTotals,
  });
  refreshAscii(feed);
  wireAvatarImageFallbacks(feed);
  persistHomeFeedPageSnapshot({
    viewerPubkey: normalizedPubkey(),
    sort: "recent",
    noteIDs: visibleFeedNoteIDs(feed),
  });
  const nextTop = feedTopCursor(root());
  button.dataset.topCursor = nextTop.cursor;
  button.dataset.topCursorId = nextTop.cursorID;
  clearPendingCountButton(button, "[data-new-notes-count]");
  pendingRecentFeedRefresh = null;
  return true;
}

async function autoLoadFeedNewerNotes(url) {
  const feedURL = canonicalHomeFeedURL(url);
  const backoffMs = [0, 500, 1500, 3500];
  for (const delay of backoffMs) {
    if (delay > 0) {
      await new Promise((resolve) => setTimeout(resolve, delay));
    }
    if (!currentHomeFeedMatches(feedURL)) return;
    if (feedHasServerLoader(root())) continue;
    const button = root().querySelector("[data-new-notes]");
    if (!button) return;
    await loadFeedNewerNotes(feedURL, button, () => {});
    if (!currentHomeFeedMatches(feedURL)) return;
    if (Number(button.dataset.pendingCount || "0") === 0) return;
  }
}

async function primeFeedNewNotesButton(url) {
  const button = root().querySelector("[data-new-notes]");
  if (!button) return;
  const sort = effectiveFeedSortFromURL(url);
  if (sort !== "recent") {
    clearPendingCountButton(button, "[data-new-notes-count]");
    return;
  }
  const loader = startPendingButtonLoader(button, {
    loaderType: "feed-newer-check",
    title: "checking newer notes",
    summary: "",
    statusMessages: [],
    completionMessage: "check complete.",
    progressWidth: 24,
    statusWindow: 1,
    hideActivity: true,
  });
  try {
    if (feedHasServerLoader(root())) {
      const recovered = await recoverStalledFeedLoader(url);
      if (recovered) {
        clearPendingCountButton(button, "[data-new-notes-count]");
      }
    }
    let count = 0;
    if (loader) setRetroLoaderProgress(loader, { percent: 24 });
    const prepared = await prepareRecentFeedRefresh(url);
    if (loader) setRetroLoaderProgress(loader, { percent: 68 });
    count = prepared?.events?.length || 0;
    if (!currentHomeFeedMatches(url)) return;
    if (count > 0) {
      setPendingCountButton(button, count, "[data-new-notes-count]");
    } else {
      clearPendingCountButton(button, "[data-new-notes-count]");
    }
    if (loader) setRetroLoaderProgress(loader, { percent: 84 });
    await settleRetroLoader(loader, {
      completionMessage: count > 0 ? `${count} newer notes ready.` : "feed is already current.",
    });
    hideInlineRetroLoader(button, { keepTargetHidden: count < 1 });
  } catch {
    hideInlineRetroLoader(button, { keepTargetHidden: Number(button.dataset.pendingCount || "0") < 1 });
  }
}

async function loadFeedNewerNotes(url, button, setLoadingState) {
  if (button.dataset.loading === "1") return;
  const sortAtClick = effectiveFeedSortFromURL(url);
  if (sortAtClick === "recent" && commitPreparedRecentFeed(url, button, pendingRecentFeedRefresh)) {
    return;
  }
  setLoadingState(true);
  const loader = startPendingButtonLoader(button, {
    loaderType: "feed-newer",
    title: "loading newer notes",
    summary: "checking how many newer notes are ready.",
    statusMessages: ["starting request..."],
    completionMessage: "newer notes loaded.",
    progressWidth: 24,
    statusWindow: 3,
  });
  try {
    const sort = effectiveFeedSortFromURL(url);
    const feed = root().querySelector("#feed[data-feed]") || root().querySelector("[data-feed]");
    if (feed && sort !== "recent") {
      if (loader) {
        setRetroLoaderProgress(loader, {
          percent: 38,
          statusMessage: "preparing the refreshed ranking...",
        });
      }
      const ranked = pendingRankedFeedRefresh?.sort === sort
        ? pendingRankedFeedRefresh
        : await fetchRankedFeedRefresh(sort);
      if (!ranked?.events?.length || !currentHomeFeedMatches(url)) {
        clearPendingCountButton(button, "[data-new-notes-count]");
        await settleRetroLoader(loader, { completionMessage: "feed is already current." });
        hideInlineRetroLoader(button, { keepTargetHidden: true });
        return;
      }
      if (loader) {
        setRetroLoaderProgress(loader, {
          percent: 82,
          statusMessage: "updating the ranked feed...",
        });
      }
      const [{ applyFeedViewModel }, { feedPageCursor, feedPageMayHaveMore }] = await Promise.all([
        import("./client-render.js"),
        import("./feed-pagination.js"),
      ]);
      await applyFeedViewModel(root(), {
        route: "feed",
        sort,
        notes: ranked.events,
        cursor: feedPageCursor(ranked.events),
        hasMore: feedPageMayHaveMore(ranked.events),
      }, { viewer: normalizedPubkey(), allowEmptyLoaderReplacement: true });
      pendingRankedFeedRefresh = null;
      clearPendingCountButton(button, "[data-new-notes-count]");
      await settleRetroLoader(loader);
      hideInlineRetroLoader(button, { keepTargetHidden: true });
      return;
    }
    if (feed && sort === "recent") {
      if (loader) {
        setRetroLoaderProgress(loader, {
          percent: 18,
          statusMessage: "checking relay updates...",
        });
      }
      const prepared = await prepareRecentFeedRefresh(url);
      if (!currentHomeFeedMatches(url)) return;
      if (!prepared?.events?.length) {
        button.dataset.pendingCount = "0";
        button.hidden = true;
        await settleRetroLoader(loader, { completionMessage: "feed is already current." });
        hideInlineRetroLoader(button, { keepTargetHidden: true });
        return;
      }
      if (loader) {
        setRetroLoaderProgress(loader, {
          percent: 52,
          statusMessage: "hydrating authors and references...",
        });
      }
      if (loader) {
        setRetroLoaderProgress(loader, {
          percent: 82,
          statusMessage: "rendering newer notes...",
        });
      }
      commitPreparedRecentFeed(url, button, prepared);
      await settleRetroLoader(loader);
      hideInlineRetroLoader(button, { keepTargetHidden: true });
      return;
    }

    clearPendingCountButton(button, "[data-new-notes-count]");
    await settleRetroLoader(loader, { completionMessage: "feed is already current." });
    hideInlineRetroLoader(button, { keepTargetHidden: true });
  } catch {
    // Keep the existing button text/count visible so retry is obvious.
    button.hidden = false;
    hideInlineRetroLoader(button);
  } finally {
    setLoadingState(false);
  }
}
async function fetchProfileNewerPosts(baseURL, options = {}) {
  const profileURL = new URL(baseURL, window.location.origin);
  if (!profileURL.pathname.startsWith("/u/")) {
    return { body: "", count: 0 };
  }
  const events = await fetchClientProfileNewer(root());
  return { body: "", count: events.length };
}

export function bindProfileNewNotesButton(url, explicitRoot = null) {
  const existing = root().querySelector("[data-profile-new-notes]");
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
  button.addEventListener("click", () => {
    void profileNewerPosts.track(loadProfileNewerPosts(url, button, setLoadingState));
  });
  const top = profilePostsTopCursor(root());
  button.dataset.topCursor = top.cursor;
  button.dataset.topCursorId = top.cursorID;
}

async function loadProfileNewerPosts(url, button, setLoadingState) {
  if (button.dataset.loading === "1") return;
  setLoadingState(true);
  const loader = startPendingButtonLoader(button, {
    loaderType: "profile-newer",
    title: "loading newer posts",
    summary: "checking for newer profile posts.",
    statusMessages: ["starting request..."],
    completionMessage: "newer posts loaded.",
    progressWidth: 24,
    statusWindow: 3,
  });
  try {
    if (loader) {
      setRetroLoaderProgress(loader, {
        percent: 18,
        statusMessage: "checking relay updates...",
      });
    }
    const newer = await prependClientProfileNewer(root());
    if (loader) {
      setRetroLoaderProgress(loader, {
        percent: 82,
        statusMessage: "updating profile feed...",
      });
    }
    applyProfileNewerPosts(url, { body: "", count: newer.count }, button);
    await settleRetroLoader(loader);
    hideInlineRetroLoader(button, { keepTargetHidden: true });
  } catch {
    button.hidden = false;
    hideInlineRetroLoader(button);
  } finally {
    setLoadingState(false);
  }
}

function applyProfileNewerPosts(url, newer, button) {
  const profileURL = new URL(url, window.location.origin);
  if (routeKind(window.location.pathname) !== "profile" || window.location.pathname !== profileURL.pathname) {
    return;
  }
  const top = profilePostsTopCursor(root());
  button.dataset.topCursor = top.cursor;
  button.dataset.topCursorId = top.cursorID;
  clearPendingCountButton(button, "[data-profile-new-notes-count]");
}

export function startProfilePostsPolling(url, runImmediately, options = {}, explicitRoot = null) {
  return withRoot(explicitRoot, () => {
  stopProfilePostsPolling();
  if (pageIsHidden()) return;
  if (runImmediately) {
    if (options.autoLoadInitial === true) {
      void profileNewerPosts.track(autoLoadProfileNewerPosts(url));
    } else {
      void pollProfilePostsNewer(url);
    }
  }
  profilePostsPollTimer = window.setInterval(() => {
    void pollProfilePostsNewer(url);
  }, powerSaverActive() ? saverNewerFragmentPollMs : newerFragmentPollMs);
  });
}

export function stopProfilePostsPolling() {
  if (!profilePostsPollTimer) return;
  window.clearInterval(profilePostsPollTimer);
  profilePostsPollTimer = 0;
}

async function autoLoadProfileNewerPosts(url) {
  const profileURL = new URL(url, window.location.origin);
  const backoffMs = [0, 500, 1500, 3500];
  for (const delay of backoffMs) {
    if (delay > 0) {
      await new Promise((resolve) => setTimeout(resolve, delay));
    }
    if (routeKind(window.location.pathname) !== "profile" || window.location.pathname !== profileURL.pathname) {
      return;
    }
    const button = root().querySelector("[data-profile-new-notes]");
    if (!button) return;
    const newer = await prependClientProfileNewer(root());
    if (newer.count > 0) {
      applyProfileNewerPosts(url, newer, button);
      return;
    }
  }
}

async function pollProfilePostsNewer(url) {
  if (document.visibilityState !== "visible") return;
  if (profilePostsPollInFlight) return;
  const button = root().querySelector("[data-profile-new-notes]");
  if (!button) return;
  profilePostsPollInFlight = true;
  try {
    const newer = await fetchProfileNewerPosts(url);
    if (newer.count > 0) {
      setPendingCountButton(button, newer.count, "[data-profile-new-notes-count]");
    } else {
      clearPendingCountButton(button, "[data-profile-new-notes-count]");
    }
  } finally {
    profilePostsPollInFlight = false;
  }
}

export function startFeedPolling(url, runImmediately, options = {}, explicitRoot = null) {
  return withRoot(explicitRoot, () => {
  stopFeedPolling();
  if (pageIsHidden()) return;
  const sort = effectiveFeedSortFromURL(url);
  const button = root().querySelector("[data-new-notes]");
  suppressFeedPendingButtonWhileShellVisible(button);
  if (runImmediately) {
    if (options.autoLoadInitial === true) {
      void primeFeedNewNotesButton(url);
    } else {
      void pollFeedNewNotes(url);
    }
  }
  feedPollTimer = window.setInterval(() => {
    void pollFeedNewNotes(url);
  }, powerSaverActive() ? saverNewerFragmentPollMs : newerFragmentPollMs);
  });
}


export function stopFeedPolling() {
  pendingRankedFeedRefresh = null;
  pendingRecentFeedRefresh = null;
  if (!feedPollTimer) return;
  window.clearInterval(feedPollTimer);
  feedPollTimer = 0;
}

export function stopFeedLoaderRecovery() {
  if (!feedLoaderRecoveryTimer) return;
  clearInterval(feedLoaderRecoveryTimer);
  feedLoaderRecoveryTimer = 0;
}

export function startFeedLoaderRecovery(url, explicitRoot = null) {
  stopFeedLoaderRecovery();
  if (getCurrentRoute() !== "feed" || !feedHasServerLoader(root())) return;
  feedLoaderRecoveryTimer = window.setInterval(() => {
    if (getCurrentRoute() !== "feed") {
      stopFeedLoaderRecovery();
      return;
    }
    if (!feedHasServerLoader(root())) {
      stopFeedLoaderRecovery();
      return;
    }
    void recoverStalledFeedLoader(url, { forceRefresh: true }).then((recovered) => {
      if (recovered || !feedHasServerLoader(root())) stopFeedLoaderRecovery();
    });
  }, feedLoaderRecoveryRetryMs);
}

async function recoverStalledFeedLoader(url, options = {}) {
  if (feedLoaderRecoveryInFlight || getCurrentRoute() !== "feed" || !feedHasServerLoader(root())) return false;
  feedLoaderRecoveryInFlight = true;
  try {
    const { forceRefresh = false } = options;
    const { hydrateClientRoute } = await import("./client-render.js");
    await hydrateClientRoute("feed", root(), { forceRefresh });
    if (!feedHasServerLoader(root())) return true;

    const feed = homeFeedElement(root());
    if (feed?.querySelector(".note[id^='note-']")) {
      feed.querySelectorAll("[data-feed-loader]").forEach((loader) => loader.remove());
      return true;
    }
  } catch (error) {
    console.error("Feed loader recovery failed", error);
  } finally {
    feedLoaderRecoveryInFlight = false;
  }
  return false;
}

async function pollFeedNewNotes(url) {
  if (document.visibilityState !== "visible") return;
  if (feedPollInFlight) return;
  const button = root().querySelector("[data-new-notes]");
  if (!button) return;
  feedPollInFlight = true;
  try {
    if (suppressFeedPendingButtonWhileShellVisible(button)) return;
    if (feedHasServerLoader(root())) {
      const recovered = await recoverStalledFeedLoader(url);
      if (recovered) {
        clearPendingCountButton(button, "[data-new-notes-count]");
        return;
      }
    }
    const sort = effectiveFeedSortFromURL(url);
    if (sort !== "recent") {
      const ranked = await fetchRankedFeedRefresh(sort);
      if (!currentHomeFeedMatches(url)) return;
      if (ranked.count > 0) {
        setPendingCountButton(button, ranked.count, "[data-new-notes-count]");
      } else if (button.dataset.loading !== "1") {
        clearPendingCountButton(button, "[data-new-notes-count]");
      }
      return;
    }
    if (sort === "recent") {
      const prepared = await prepareRecentFeedRefresh(url);
      const count = prepared?.events?.length || 0;
      if (count > 0 && feedHasServerLoader(root())) {
        const recovered = await recoverStalledFeedLoader(url);
        if (recovered) {
          clearPendingCountButton(button, "[data-new-notes-count]");
          return;
        }
      }
      if (count > 0) {
        setPendingCountButton(button, count, "[data-new-notes-count]");
      } else if (button.dataset.loading !== "1") {
        clearPendingCountButton(button, "[data-new-notes-count]");
      }
      return;
    }
  } finally {
    feedPollInFlight = false;
  }
}

async function fetchRankedFeedRefresh(sort) {
  const visibleIds = new Set(collectVisibleFeedNoteIds(root(), "#feed[data-feed]"));
  const { fetchFeedNotes } = await import("./feed-service.js");
  const events = await fetchFeedNotes({
    viewerPubkey: normalizedPubkey(),
    limit: 50,
    sort,
    forceFetch: true,
  });
  const count = countUnseenFeedEvents(events, visibleIds);
  pendingRankedFeedRefresh = { sort, events, count };
  return pendingRankedFeedRefresh;
}

export function bindProfileLazyTabs(url, explicitRoot = null) {
  const r = explicitRoot ?? root();
  const mapping = [
    { id: "user-tab-replies", panel: "#user-panel-replies" },
    { id: "user-tab-media", panel: "#user-panel-media" },
    { id: "user-tab-following", panel: "#user-panel-following" },
    { id: "user-tab-followers", panel: "#user-panel-followers" },
  ];
  mapping.forEach(({ id, panel }) => {
    const input = r.querySelector(`#${id}`);
    if (!input) return;
    if (boundProfileLazyInputs.has(input)) return;
    boundProfileLazyInputs.add(input);
    input.addEventListener("change", () => {
      if (!input.checked) return;
      const target = r.querySelector(panel);
      if (!target) return;
      target.dataset.loaded = "1";
      const fragment = target.getAttribute("data-user-fragment") || "";
      if ((fragment === "replies" || fragment === "media") && target.querySelector("[data-retro-loader]")) {
        void hydrateServerProfileTimelineFragment(url, fragment, target, r);
      }
      if (fragment === "following" || fragment === "followers") {
        void hydrateProfileFollowFragment(url, fragment, target, r);
      }
      updateRelayAwareLinks();
    });
  });
  ["following", "followers"].forEach((fragment) => {
    const panel = r.querySelector(`#user-panel-${fragment}`);
    if (isFollowPanelElement(panel)) {
      bindServerProfileFollowNavigation(panel, url, fragment, r);
      void hydrateProfileFollowRows(panel);
    }
  });
  syncProfileTabFromURLHash(url, r);
}

async function hydrateProfileFollowFragment(url, fragment, panel, scopeRoot) {
  if (!isFollowPanelElement(panel)) return false;
  if (!panel.querySelector("[data-retro-loader]") && panel.dataset.loaded === "1") return true;
  const relayNativeLoaded = await hydrateRelayNativeProfileTab(fragment, scopeRoot).catch(() => false);
  if (relayNativeLoaded) return true;
  return hydrateServerProfileFollowFragment(url, fragment, panel, scopeRoot);
}

function syncProfileTabFromURLHash(url, scopeRoot) {
  const hash = String(url?.hash || window.location.hash || "").trim();
  if (!hash.startsWith("#user-panel-")) return false;
  let panelID = "";
  try {
    panelID = decodeURIComponent(hash.slice(1));
  } catch {
    panelID = hash.slice(1);
  }
  if (!/^user-panel-[a-z-]+$/.test(panelID)) return false;
  const panel = scopeRoot?.querySelector?.(`#${panelID}`);
  if (!(panel instanceof HTMLElement)) return false;
  const inputID = panelID.replace("user-panel-", "user-tab-");
  const input = scopeRoot.querySelector?.(`#${inputID}`);
  if (!(input instanceof HTMLInputElement)) return false;
  if (input.checked) return true;
  input.checked = true;
  input.dispatchEvent(new Event("change", { bubbles: true }));
  return true;
}

function followFragmentURL(baseURL, fragment, href = "") {
  const target = href ? new URL(href, window.location.origin) : new URL(baseURL.toString());
  target.searchParams.set("fragment", fragment);
  return target;
}

function cssEscapeIdent(value) {
  if (globalThis.CSS && typeof globalThis.CSS.escape === "function") return globalThis.CSS.escape(value);
  return String(value).replace(/["\\]/g, "\\$&");
}

function profileHasFollowRowMetadata(profile) {
  return Boolean(
    String(profile?.display_name || "").trim() ||
    String(profile?.name || "").trim() ||
    String(profile?.picture || "").trim() ||
    String(profile?.avatar_url || "").trim() ||
    String(profile?.nip05 || "").trim(),
  );
}

function followHydrationRelays(panel) {
  const raw = panel?.closest?.("[data-profile-shell]")?.getAttribute("data-profile-relays") || "";
  return raw.split(",").map((relay) => relay.trim()).filter(Boolean);
}

function replaceFollowAvatar(row, avatarURL) {
  const avatar = row.querySelector(".profile-follow-avatar");
  if (!(avatar instanceof HTMLElement)) return;
  avatar.textContent = "";
  if (avatarURL) {
    const img = document.createElement("img");
    img.className = "profile-follow-avatar-image";
    img.src = avatarURL;
    img.alt = "";
    img.loading = "lazy";
    img.decoding = "async";
    avatar.append(img);
    return;
  }
  const fallback = document.createElement("span");
  fallback.className = "profile-follow-avatar-fallback";
  fallback.setAttribute("aria-hidden", "true");
  fallback.textContent = "@";
  avatar.append(fallback);
}

function replaceFollowNIP05(row, pubkey, nip05) {
  const secondary = row.querySelector(".profile-follow-secondary");
  if (!(secondary instanceof HTMLElement)) return;
  const clean = String(nip05 || "").trim();
  secondary.textContent = "";
  if (!clean) {
    secondary.classList.add("profile-follow-secondary--empty");
    secondary.removeAttribute("data-nip05-verify");
    secondary.removeAttribute("data-nip05");
    secondary.removeAttribute("data-pubkey");
    secondary.textContent = "no nip-05 published";
    return;
  }
  secondary.classList.remove("profile-follow-secondary--empty");
  secondary.setAttribute("data-nip05-verify", "");
  secondary.setAttribute("data-nip05", clean);
  secondary.setAttribute("data-pubkey", pubkey);
  const label = document.createElement("span");
  label.textContent = nip05DisplayText(clean);
  secondary.append(label);
  const status = document.createElement("span");
  status.className = "profile-nip05-status";
  status.setAttribute("data-nip05-status", "");
  status.setAttribute("data-nip05-status-kind", "checking");
  status.setAttribute("aria-label", "Checking NIP-5 verification");
  status.setAttribute("aria-expanded", "false");
  status.setAttribute("role", "button");
  status.tabIndex = 0;
  status.hidden = true;
  secondary.append(document.createTextNode(" "));
  secondary.append(status);
}

function applyProfileToFollowRow(row, pubkey, profile) {
  if (!profileHasFollowRowMetadata(profile)) return false;
  const name = row.querySelector(".profile-follow-display");
  if (name) name.textContent = displayName({ ...profile, pubkey });
  replaceFollowAvatar(row, preferredAvatarURL(profile));
  replaceFollowNIP05(row, pubkey, profile.nip05);
  row.dataset.profileHydrated = "1";
  return true;
}

function applyCachedProfileToFollowRows(pubkey, root = document) {
  const pk = normalizePubkey(pubkey);
  if (!pk) return false;
  const profile = cachedProfile(pk);
  if (!profileHasFollowRowMetadata(profile)) return false;
  let updated = false;
  root.querySelectorAll(`[data-profile-follow-item][data-pubkey="${cssEscapeIdent(pk)}"]`).forEach((row) => {
    if (!(row instanceof HTMLElement)) return;
    if (applyProfileToFollowRow(row, pk, profile)) updated = true;
  });
  if (updated) {
    wireAvatarImageFallbacks(root);
    refreshNIP05Verification(root);
    updateRelayAwareLinks();
  }
  return updated;
}

function bindProfileFollowCacheListener() {
  const target = globalThis.window || globalThis;
  if (profileFollowCacheListenerBound || typeof target?.addEventListener !== "function") return;
  profileFollowCacheListenerBound = true;
  target.addEventListener(PROFILE_MEMORY_CACHE_UPDATED_EVENT, (event) => {
    const pubkey = normalizePubkey(event?.detail?.profile?.pubkey || "");
    if (!pubkey) return;
    applyCachedProfileToFollowRows(pubkey);
  });
}

async function hydrateProfileFollowRows(panel) {
  if (!isFollowPanelElement(panel)) return false;
  bindProfileFollowCacheListener();
  if (profileFollowHydrationInFlight.has(panel)) return false;
  const rows = [...panel.querySelectorAll("[data-profile-follow-item][data-pubkey]")].filter((row) => row instanceof HTMLElement);
  rows.forEach((row) => {
    const pubkey = normalizePubkey(row.getAttribute("data-pubkey") || "");
    if (pubkey) applyCachedProfileToFollowRows(pubkey, panel);
  });
  const pubkeys = [...new Set(rows
    .filter((row) => row.dataset.profileHydrated !== "1")
    .map((row) => normalizePubkey(row.getAttribute("data-pubkey") || ""))
    .filter(Boolean))];
  if (!pubkeys.length) return false;
  profileFollowHydrationInFlight.add(panel);
  try {
    const relays = followHydrationRelays(panel);
    const profiles = {};
    for (let start = 0; start < pubkeys.length; start += 25) {
      const batch = pubkeys.slice(start, start + 25);
      Object.assign(profiles, await fetchProfiles(batch, { relays }).catch(() => ({})));
    }
    let updated = false;
    rows.forEach((row) => {
      const pubkey = normalizePubkey(row.getAttribute("data-pubkey") || "");
      if (!pubkey) return;
      if (applyProfileToFollowRow(row, pubkey, profiles[pubkey])) updated = true;
    });
    if (updated) {
      wireAvatarImageFallbacks(panel);
      refreshNIP05Verification(panel);
      updateRelayAwareLinks();
    }
    return updated;
  } finally {
    profileFollowHydrationInFlight.delete(panel);
  }
}

function bindServerProfileFollowNavigation(panel, baseURL, fragment, scopeRoot) {
  if (!isFollowPanelElement(panel)) return;
  if (panel.dataset.boundFollowFragmentPanel === "1") return;
  panel.dataset.boundFollowFragmentPanel = "1";
  panel.addEventListener("click", (event) => {
    const loadMore = event.target?.closest?.("[data-follow-load-more]");
    if (loadMore && panel.contains(loadMore)) {
      event.preventDefault();
      const nextFragment = loadMore.getAttribute("data-follow-fragment") || fragment;
      const href = loadMore.getAttribute("data-follow-next-url") || "";
      if (!href) return;
      void hydrateServerProfileFollowFragment(baseURL, nextFragment, panel, scopeRoot, href, {
        append: true,
        trigger: loadMore,
      });
      return;
    }
    const clear = event.target?.closest?.("[data-follow-clear]");
    if (clear && panel.contains(clear)) {
      event.preventDefault();
      const nextFragment = clear.getAttribute("data-follow-fragment") || fragment;
      const href = clear.getAttribute("data-follow-clear-url") || baseURL.toString();
      setFollowSearchLoading(panel, true);
      void hydrateServerProfileFollowFragment(baseURL, nextFragment, panel, scopeRoot, href);
      return;
    }
    const link = event.target?.closest?.("[data-follow-fragment-link]");
    if (!link || !panel.contains(link) || typeof link.href !== "string") return;
    event.preventDefault();
    const nextFragment = link.getAttribute("data-follow-fragment-link") || fragment;
    void hydrateServerProfileFollowFragment(baseURL, nextFragment, panel, scopeRoot, link.href);
  });
  panel.addEventListener("submit", (event) => {
    const form = event.target;
    if (!(form instanceof HTMLFormElement) || !panel.contains(form) || !form.matches("[data-follow-fragment-form]")) return;
    event.preventDefault();
    setFollowSearchLoading(panel, true);
    const nextFragment = form.getAttribute("data-follow-fragment") || fragment;
    const target = new URL(form.action || baseURL.toString(), window.location.origin);
    const params = new URLSearchParams(new FormData(form));
    params.forEach((value, key) => {
      if (String(value || "").trim()) target.searchParams.set(key, value);
      else target.searchParams.delete(key);
    });
    target.searchParams.delete(`${nextFragment}_page`);
    void hydrateServerProfileFollowFragment(baseURL, nextFragment, panel, scopeRoot, target.toString());
  });
}

function setFollowSearchLoading(panel, loading) {
  const form = panel.querySelector("[data-follow-fragment-form]");
  if (!(form instanceof HTMLFormElement)) return;
  form.classList.toggle("is-loading", loading);
  form.setAttribute("aria-busy", loading ? "true" : "false");
  form.querySelectorAll("button").forEach((button) => {
    if (!(button instanceof HTMLButtonElement)) return;
    button.disabled = loading;
  });
  const status = form.querySelector("[data-follow-search-status]");
  if (status instanceof HTMLElement) {
    status.hidden = !loading;
  }
}

function appendProfileFollowFragment(panel, html) {
  const template = document.createElement("template");
  template.innerHTML = html;
  const incomingPanel = template.content.querySelector(".profile-follow-panel") || template.content;
  const currentList = panel.querySelector("[data-profile-follow-list]");
  const incomingList = incomingPanel.querySelector?.("[data-profile-follow-list]");
  if (!currentList || !incomingList) return false;
  currentList.querySelectorAll(".profile-follow-empty-item").forEach((item) => item.remove());
  let appended = 0;
  incomingList.querySelectorAll("[data-profile-follow-item]").forEach((item) => {
    if (!(item instanceof HTMLElement)) return;
    if (item.classList.contains("profile-follow-hashtag")) return;
    const pubkey = item.getAttribute("data-pubkey") || "";
    if (pubkey && currentList.querySelector(`[data-profile-follow-item][data-pubkey="${cssEscapeIdent(pubkey)}"]`)) return;
    currentList.append(item);
    appended += 1;
  });
  const currentPager = panel.querySelector("[data-profile-follow-load-more-wrap]");
  const incomingPager = incomingPanel.querySelector?.("[data-profile-follow-load-more-wrap]");
  if (currentPager && incomingPager?.querySelector?.("[data-follow-load-more]")) {
    currentPager.replaceWith(incomingPager);
  } else if (currentPager) {
    currentPager.remove();
  }
  return appended > 0;
}

function setFollowLoadMoreLoading(button, loading) {
  if (!(button instanceof HTMLElement)) return;
  if (loading) {
    button.dataset.loading = "1";
    button.setAttribute("aria-busy", "true");
    button.setAttribute("disabled", "");
    button.textContent = "Loading...";
    return;
  }
  delete button.dataset.loading;
  button.removeAttribute("aria-busy");
  button.removeAttribute("disabled");
  button.textContent = "Load more";
}

async function hydrateServerProfileFollowFragment(url, fragment, panel, scopeRoot, href = "", options = {}) {
  if (!isFollowPanelElement(panel)) return false;
  if (profileFragmentInFlight.has(panel)) return false;
  profileFragmentInFlight.add(panel);
  setFollowLoadMoreLoading(options.trigger, true);
  try {
    const target = followFragmentURL(url, fragment, href);
    const response = await fetchWithSession(`${target.pathname}${target.search}`, {
      headers: { Accept: "text/html" },
    });
    if (!response.ok) return false;
    const html = await response.text();
    if (!html.trim()) return false;
    if (options.append) {
      if (!appendProfileFollowFragment(panel, html)) return false;
      panel.dataset.loaded = "1";
      bindServerProfileFollowNavigation(panel, url, fragment, scopeRoot);
      wireAvatarImageFallbacks(panel);
      refreshNIP05Verification(panel);
      updateRelayAwareLinks();
      void hydrateProfileFollowRows(panel);
      return true;
    }
    panel.innerHTML = html;
    panel.dataset.loaded = "1";
    bindServerProfileFollowNavigation(panel, url, fragment, scopeRoot);
    wireAvatarImageFallbacks(panel);
    refreshNIP05Verification(panel);
    updateRelayAwareLinks();
    void hydrateProfileFollowRows(panel);
    return true;
  } catch {
    return false;
  } finally {
    setFollowLoadMoreLoading(options.trigger, false);
    setFollowSearchLoading(panel, false);
    profileFragmentInFlight.delete(panel);
  }
}

async function hydrateServerProfileTimelineFragment(url, fragment, panel, scopeRoot) {
  if (!(panel instanceof HTMLElement)) return false;
  if (profileFragmentInFlight.has(panel)) return false;
  const feed = panel.querySelector(`[data-profile-feed="${fragment}"]`);
  if (!(feed instanceof HTMLElement)) return false;
  profileFragmentInFlight.add(panel);
  try {
    const target = new URL(url.toString());
    target.searchParams.set("fragment", fragment);
    target.searchParams.delete("cursor");
    target.searchParams.delete("cursor_id");
    const response = await fetchWithSession(`${target.pathname}${target.search}`, {
      headers: { Accept: "text/html" },
    });
    if (!response.ok) return false;
    const html = await response.text();
    if (!html.trim()) return false;
    feed.innerHTML = html;
    panel.dataset.loaded = "1";
    refreshAscii(feed);
    updateRelayAwareLinks();
    void refreshVisibleFeedNoteMetadata(scopeRoot, window.location.href, {
      feedSelector: `#${panel.id} [data-profile-feed="${fragment}"]`,
    });
    return true;
  } catch {
    return false;
  } finally {
    profileFragmentInFlight.delete(panel);
  }
}

export function syncRoutePolling(route, url, explicitRoot = null, options = {}) {
	return withRoot(explicitRoot, () => {
		const quietGuest = !normalizedPubkey() && Boolean(document.body?.dataset?.guestV2);
		if (quietGuest) {
			stopFeedPolling();
			stopFeedLoaderRecovery();
			stopProfilePostsPolling();
			if (route === "profile") bindProfileLazyTabs(url, explicitRoot);
			return;
		}
		const { initialDocumentLoad = false } = options;
    if (route === "feed") {
      startFeedPolling(
        url,
        true,
        { autoLoadInitial: shouldAutoLoadFeedOnHydrate({ initialDocumentLoad }) },
        explicitRoot,
      );
      if (feedHasServerLoader(explicitRoot)) startFeedLoaderRecovery(url, explicitRoot);
      else stopFeedLoaderRecovery();
      bindNewNotesButton(url, explicitRoot);
    } else {
      stopFeedPolling();
      stopFeedLoaderRecovery();
    }
    if (route === "profile") {
      bindProfileNewNotesButton(url, explicitRoot);
      startProfilePostsPolling(url, true, { autoLoadInitial: false }, explicitRoot);
      bindProfileLazyTabs(url, explicitRoot);
    } else {
      stopProfilePostsPolling();
    }
  });
}
