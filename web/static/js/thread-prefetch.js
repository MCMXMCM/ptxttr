/** Feed / thread cards opened via notes.js card click (must match notes.js clickSelectCardSelector). */
export const cardSelectPrefetchSelector =
  ".note[data-ascii-select-href], .comment[data-ascii-select-href]";

function resolvePrefetchRoute(href, withRelaysFn, routeKindFn) {
  const resolved = withRelaysFn(href);
  const route = routeKindFn(new URL(resolved, window.location.origin).pathname);
  if (!route) return null;
  return { href: resolved, route };
}

/** Max concurrent viewport-driven thread hydrate prefetches. */
export const maxViewportThreadPrefetches = 4;

/** Feed containers that participate in viewport thread prefetch. */
export const feedPrefetchSelectors = ["[data-feed]", "#user-panel-posts [data-feed]"];

const feedObserverState = new WeakMap();
let pendingPrefetchState = null;
let cancelPendingVisibleFeedPrefetch = null;

function noteIDFromElement(note) {
  if (!note?.id) return "";
  return note.id.replace(/^note-/, "");
}

function threadHrefForNoteID(id, withRelaysFn) {
  if (!id) return "";
  return withRelaysFn(`/thread/${id}`);
}

function scanFeedNoteIds(feed, limit = maxViewportThreadPrefetches, visibleOnly = null) {
  if (!feed) return [];
  const ids = [];
  const seen = new Set();
  for (const note of feed.querySelectorAll(".note[id^='note-']")) {
    const id = noteIDFromElement(note);
    if (!id || seen.has(id)) continue;
    if (visibleOnly && !visibleOnly.has(id)) continue;
    seen.add(id);
    ids.push(id);
    if (ids.length >= limit) break;
  }
  return ids;
}

function prefetchIDs(state, ids) {
  for (const id of ids) {
    const href = threadHrefForNoteID(id, state.withRelaysFn);
    if (!href) continue;
    state.scheduleFn(href, "thread", { priority: false });
  }
}

function scheduleVisiblePrefetch(state, feed) {
  if (state.prefetchFrame) return;
  state.prefetchFrame = requestAnimationFrame(() => {
    state.prefetchFrame = 0;
    if (state.visibleIds.size === 0) return;
    prefetchIDs(state, scanFeedNoteIds(feed, maxViewportThreadPrefetches, state.visibleIds));
  });
}

function pruneVisibleIds(state, feed) {
  if (!state?.visibleIds?.size) return;
  const present = new Set(scanFeedNoteIds(feed, Number.POSITIVE_INFINITY));
  for (const id of state.visibleIds) {
    if (!present.has(id)) state.visibleIds.delete(id);
  }
}

function observeFeedNotes(feed, state) {
  for (const note of feed.querySelectorAll(".note[id^='note-']")) {
    if (note.dataset.ptxtThreadPrefetchObs === "1") continue;
    note.dataset.ptxtThreadPrefetchObs = "1";
    state.observer?.observe(note);
  }
}

function onFeedIntersection(entries, state, feed) {
  for (const entry of entries) {
    const id = noteIDFromElement(entry.target);
    if (!id) continue;
    if (entry.isIntersecting) state.visibleIds.add(id);
    else state.visibleIds.delete(id);
  }
  scheduleVisiblePrefetch(state, feed);
}

function ensureFeedObserver(feed, scheduleRoutePrefetchFn, withRelaysFn) {
  if (!feed) return null;
  let state = feedObserverState.get(feed);
  if (!state) {
    state = {
      visibleIds: new Set(),
      observer: null,
      prefetchFrame: 0,
      scheduleFn: scheduleRoutePrefetchFn,
      withRelaysFn,
    };
    if (typeof IntersectionObserver === "function") {
      state.observer = new IntersectionObserver(
        (entries) => onFeedIntersection(entries, state, feed),
        { root: null, rootMargin: "100px 0px 200px 0px", threshold: 0 },
      );
    }
    feedObserverState.set(feed, state);
  } else {
    state.scheduleFn = scheduleRoutePrefetchFn;
    state.withRelaysFn = withRelaysFn;
  }
  if (state.observer) observeFeedNotes(feed, state);
  return state;
}

function refreshFeedThreadPrefetch(feed, scheduleRoutePrefetchFn, withRelaysFn) {
  const state = ensureFeedObserver(feed, scheduleRoutePrefetchFn, withRelaysFn);
  if (!state) return;
  pruneVisibleIds(state, feed);
  if (state.observer) {
    if (state.visibleIds.size > 0) scheduleVisiblePrefetch(state, feed);
    return;
  }
  prefetchIDs(state, scanFeedNoteIds(feed, maxViewportThreadPrefetches));
}

function feedsUnderRoot(root) {
  if (!root) return [];
  const feeds = [];
  const seen = new Set();
  for (const selector of feedPrefetchSelectors) {
    if (typeof root.matches === "function" && root.matches(selector) && !seen.has(root)) {
      seen.add(root);
      feeds.push(root);
    }
    root.querySelectorAll?.(selector)?.forEach((feed) => {
      if (seen.has(feed)) return;
      seen.add(feed);
      feeds.push(feed);
    });
  }
  if (
    feeds.length === 0 &&
    root.querySelectorAll?.(".note[id^='note-']")?.length > 0 &&
    !seen.has(root)
  ) {
    feeds.push(root);
  }
  return feeds;
}

/**
 * Resolve an in-app route href for link-hover or card-hover prefetch.
 * @param {(el: Element) => HTMLAnchorElement | null} closestLinkFn
 * @param {typeof import("./nav-routing.js").withRelays} withRelaysFn
 * @param {typeof import("./nav-routing.js").routeKind} routeKindFn
 */
export function prefetchTargetFromInteraction(closestLinkFn, target, withRelaysFn, routeKindFn) {
  if (!target || typeof target.closest !== "function") return null;

  const link = closestLinkFn(target);
  if (link) {
    return resolvePrefetchRoute(link.href, withRelaysFn, routeKindFn);
  }

  const card = target.closest(cardSelectPrefetchSelector);
  if (!card || typeof card.getAttribute !== "function") return null;
  const raw = card.getAttribute("data-ascii-select-href") || "";
  if (!raw) return null;
  return resolvePrefetchRoute(raw, withRelaysFn, routeKindFn);
}

/**
 * Thread URLs for visible feed notes (IntersectionObserver when available),
 * otherwise the first N notes in document order.
 */
export function visibleThreadHrefs(root, withRelaysFn, limit = maxViewportThreadPrefetches) {
  if (!root) return [];
  const hrefs = [];
  const seen = new Set();
  for (const feed of feedsUnderRoot(root)) {
    const state = feedObserverState.get(feed);
    const ids = state?.visibleIds?.size
      ? scanFeedNoteIds(feed, limit, state.visibleIds)
      : scanFeedNoteIds(feed, limit);
    for (const id of ids) {
      if (!id || seen.has(id)) continue;
      seen.add(id);
      hrefs.push(threadHrefForNoteID(id, withRelaysFn));
      if (hrefs.length >= limit) return hrefs;
    }
  }
  return hrefs;
}

/**
 * Re-bind viewport observers and prefetch after feed DOM changes (load more, newer notes).
 * @param {Element | Document} [root]
 */
export function notifyFeedNotesChanged(root = document) {
  const state = pendingPrefetchState;
  if (!state) return;
  const scope = root instanceof Element ? root : document.body;
  for (const feed of feedsUnderRoot(scope)) {
    refreshFeedThreadPrefetch(feed, state.scheduleFn, state.withRelaysFn);
  }
}

/**
 * Idle-schedule thread hydrate prefetches for notes in feed columns under root.
 * @param {Element | null} root
 * @param {(href: string, route: string, options?: { priority?: boolean }) => void} scheduleRoutePrefetchFn
 * @param {typeof import("./session.js").withRelays} withRelaysFn
 */
export function scheduleVisibleFeedThreadPrefetches(root, scheduleRoutePrefetchFn, withRelaysFn) {
  if (!root) return;
  pendingPrefetchState = { scheduleFn: scheduleRoutePrefetchFn, withRelaysFn };
  if (cancelPendingVisibleFeedPrefetch) {
    cancelPendingVisibleFeedPrefetch();
    cancelPendingVisibleFeedPrefetch = null;
  }
  const run = () => {
    cancelPendingVisibleFeedPrefetch = null;
    for (const feed of feedsUnderRoot(root)) {
      refreshFeedThreadPrefetch(feed, scheduleRoutePrefetchFn, withRelaysFn);
    }
  };
  if (typeof requestIdleCallback === "function") {
    const id = requestIdleCallback(run, { timeout: 2000 });
    cancelPendingVisibleFeedPrefetch = () => cancelIdleCallback(id);
  } else {
    const id = setTimeout(run, 100);
    cancelPendingVisibleFeedPrefetch = () => clearTimeout(id);
  }
}
