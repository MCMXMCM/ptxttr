import { openImageViewer, refreshAscii, refreshThreadTreeQuotes } from "./ascii.js";
import { addAsciiWidthHint } from "./ascii-width-hint.js";
import { createMediaGrid, hydrateMediaGrid, mediaGridSignature } from "./media-grid.js";
import { refreshVisibleFeedReactionStats } from "./feed-metadata.js";
import { fetchWithSession } from "./session.js";
import { threadRepliesPageSkeletonMarkup, threadTreeSkeletonMarkup } from "./shell.js";
import {
  applyTruncatableViewMore,
  embeddedMediaSelector,
  initViewMore,
  interactiveSelector,
  resetTruncatableViewMore,
} from "./notes.js";
import { syncMobileAppNavHeight } from "./layout.js";
import { scrollRouteToTop } from "./shell-swap.js";
import { getImageModePref, getThreadRenderModePref, setThreadRenderModePref } from "./sort-prefs.js";
import { openThreadInlineComposer } from "./mutations.js";
import { withRelays } from "./nav-routing.js";
import { refreshVisibleNoteProfiles } from "./note-profiles.js";

let listenersAttached = false;
let hashListenerBound = false;
let treeMediaModeListenerBound = false;
const threadTreeCardSelector = "#thread-tree-view [data-thread-tree-note]";
/** Set before navigating to a subthread so init opens linear mode even when tree pref is on. */
const THREAD_TREE_TO_LINEAR_KEY = "ptxtTreeToThreadLinear";
const THREAD_INLINE_REPLY_PENDING_KEY = "ptxt-inline-reply-v1";

function doubleRaf() {
  return new Promise((resolve) => {
    requestAnimationFrame(() => {
      requestAnimationFrame(resolve);
    });
  });
}

function closestFromEventTarget(target, selector) {
  if (!(target instanceof Element)) return null;
  return target.closest(selector);
}

function appendThreadReplies(html) {
  const list = document.querySelector("#thread-replies");
  if (!list || !html.trim()) return 0;
  const template = document.createElement("template");
  template.innerHTML = html;
  const insertedComments = [];
  let appended = 0;
  template.content.querySelectorAll(".comment").forEach((comment) => {
    if (comment.id && document.getElementById(comment.id)) return;
    list.append(comment);
    insertedComments.push(comment);
    appended += 1;
  });
  if (appended > 0) {
    initViewMore(list);
    void refreshVisibleNoteProfiles(insertedComments);
  }
  return appended;
}

function threadRepliesFragmentURL(button) {
  const current = new URL(window.location.href);
  const params = new URLSearchParams({
    fragment: "replies",
    cursor: button.dataset.cursor || "",
    cursor_id: button.dataset.cursorId || "",
  });
  const selectedID = button.dataset.selectedId || "";
  if (selectedID) params.set("selected", selectedID);
  addAsciiWidthHint(params, current.pathname);
  return `${current.pathname}?${params.toString()}`;
}

export async function loadMoreReplies(button) {
  if (!button || button.dataset.loading === "1") return;
  const {
    isRelayNativeThread,
    loadMoreRelayNativeThreadReplies,
  } = await import("./client-render.js");
  if (isRelayNativeThread()) {
    return loadMoreRelayNativeThreadReplies(button);
  }
  button.dataset.loading = "1";
  button.disabled = true;
  button.textContent = "Loading...";
  const list = document.querySelector("#thread-replies");
  let pageSkeleton = null;
  if (list) {
    const wrap = document.createElement("div");
    wrap.innerHTML = threadRepliesPageSkeletonMarkup();
    pageSkeleton = wrap.firstElementChild;
    if (pageSkeleton) list.append(pageSkeleton);
    refreshAscii(list);
  }
  try {
    const previousCursor = button.dataset.cursor || "";
    const previousCursorID = button.dataset.cursorId || "";
    const response = await fetchWithSession(threadRepliesFragmentURL(button), {
      headers: { Accept: "text/html" },
      credentials: "same-origin",
    });
    if (!response.ok) {
      const error = new Error("Reply request failed");
      error.status = response.status;
      throw error;
    }
    const html = await response.text();
    const appended = appendThreadReplies(html);
    button.dataset.cursor = response.headers.get("X-Ptxt-Cursor") || button.dataset.cursor || "";
    button.dataset.cursorId = response.headers.get("X-Ptxt-Cursor-Id") || button.dataset.cursorId || "";
    const hasMore = response.headers.get("X-Ptxt-Has-More") === "1";
    const cursorAdvanced =
      button.dataset.cursor !== previousCursor || button.dataset.cursorId !== previousCursorID;
    if (appended === 0 && hasMore) {
      button.textContent = cursorAdvanced
        ? (button.dataset.loadLabel || "Load more replies")
        : "No new replies to show";
      // A non-advancing cursor would otherwise let one click repeatedly issue
      // the same store query. Stop locally; the server shield remains authoritative.
      button.disabled = !cursorAdvanced;
      return;
    }
    if (!hasMore || !html.trim()) {
      button.textContent = "No more replies";
      button.disabled = true;
      return;
    }
    button.textContent = button.dataset.loadLabel || "Load more replies";
    button.disabled = false;
  } catch (error) {
    button.textContent = error?.status === 429
      ? "Too many requests. Try again shortly."
      : "Could not load replies. Try again.";
    button.disabled = false;
  } finally {
    pageSkeleton?.remove();
    if (list) refreshAscii(list);
    button.dataset.loading = "0";
  }
}

function parseFocusedHashID() {
  const raw = window.location.hash.replace(/^#/, "");
  if (!raw.startsWith("note-")) return "";
  return raw.slice(5);
}

function parseSelectedQueryID() {
  try {
    const id = new URL(window.location.href).searchParams.get("selected") || "";
    return /^[0-9a-f]{64}$/i.test(id) ? id.toLowerCase() : "";
  } catch {
    return "";
  }
}

function cloneThreadHistoryState() {
  const raw = history.state;
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return {};
  return { ...raw };
}

function syncThreadViewIntoBrowserHistory(showTree) {
  const base = cloneThreadHistoryState();
  const prevPtxt =
    base.ptxt && typeof base.ptxt === "object" && !Array.isArray(base.ptxt) ? { ...base.ptxt } : {};
  history.replaceState(
    { ...base, ptxt: { ...prevPtxt, threadView: showTree ? "tree" : "linear" } },
    "",
    window.location.href,
  );
}

function threadHrefForNote(noteID) {
  const cur = new URL(window.location.href);
  const id = String(noteID || "").trim().toLowerCase();
  const root = threadDOMRootID() || id;
  const next = new URL(`/thread/${root}`, cur.origin);
  next.search = cur.search;
  next.searchParams.delete("selected");
  next.searchParams.delete("tree_note");
  next.searchParams.delete("fragment");
  next.searchParams.delete("cursor");
  next.searchParams.delete("cursor_id");
  if (id && root && id !== root) {
    next.searchParams.set("selected", id);
    next.hash = `#note-${id}`;
  }
  return next.toString();
}

function peekPendingTreeToThreadLinear() {
  try {
    return sessionStorage.getItem(THREAD_TREE_TO_LINEAR_KEY) === "1";
  } catch {
    return false;
  }
}

function consumePendingTreeToThreadLinear() {
  try {
    if (sessionStorage.getItem(THREAD_TREE_TO_LINEAR_KEY) !== "1") return false;
    sessionStorage.removeItem(THREAD_TREE_TO_LINEAR_KEY);
    return true;
  } catch {
    return false;
  }
}

function threadTreeModeRoot(scope = document) {
  if (!scope) return null;
  if (scope instanceof Element && scope.matches("[data-thread-tree-view]")) {
    return scope;
  }
  return scope.querySelector?.("[data-thread-tree-view]") || null;
}

function threadTreeNeedsFetch(scope = document) {
  const section = scope.querySelector("#thread-tree-view") ?? threadTreeSection();
  return Boolean(section && !threadTreeModeRoot(section));
}

function clearThreadTreeToggleLoadingState(button, showTree = resolveThreadViewMode()) {
  if (!(button instanceof HTMLButtonElement)) return;
  if (button.dataset.loading !== "1") return;
  delete button.dataset.loading;
  button.disabled = false;
  button.removeAttribute("aria-busy");
  button.classList.remove("is-pressed");
  const mode = showTree ? "tree" : "thread";
  const other = showTree ? "thread" : "tree";
  button.textContent = mode;
  button.dataset.threadViewCurrent = mode;
  button.setAttribute("aria-label", `Viewing ${mode}. Tap to switch to ${other}.`);
}

function setThreadTreeToggleLoading(loading, root = document) {
  root.querySelectorAll("[data-thread-view-toggle]").forEach((button) => {
    if (!(button instanceof HTMLButtonElement)) return;
    if (loading) {
      if (button.dataset.loading === "1") return;
      button.dataset.loading = "1";
      button.disabled = true;
      button.setAttribute("aria-busy", "true");
      button.classList.add("is-pressed");
      button.innerHTML =
        'loading<span class="thread-tree-toggle-dots" aria-hidden="true"><span>.</span><span>.</span><span>.</span></span>';
      return;
    }
    clearThreadTreeToggleLoadingState(button);
  });
}

function syncThreadViewToggle(button, showTree) {
  if (!(button instanceof HTMLButtonElement)) return;
  if (button.dataset.loading === "1") return;
  const mode = showTree ? "tree" : "thread";
  const other = showTree ? "thread" : "tree";
  button.textContent = mode;
  button.dataset.threadViewCurrent = mode;
  button.setAttribute("aria-label", `Viewing ${mode}. Tap to switch to ${other}.`);
}

/** Resolve tree vs linear from pending navigation, history, or localStorage pref. */
export function resolveThreadViewMode() {
  if (typeof window.__ptxtResolveThreadViewMode === "function") {
    return window.__ptxtResolveThreadViewMode() === "tree";
  }
  if (peekPendingTreeToThreadLinear()) return false;
  const raw = history.state;
  const ptxt = raw && typeof raw === "object" && !Array.isArray(raw) ? raw.ptxt : null;
  if (ptxt && (ptxt.threadView === "tree" || ptxt.threadView === "linear")) {
    return ptxt.threadView === "tree";
  }
  return getThreadRenderModePref() === "tree";
}

export function applyThreadViewVisibility(showTree, root = document) {
  document.documentElement.dataset.ptxtThreadView = showTree ? "tree" : "thread";
  const tree = root.querySelector("#thread-tree-view") ?? threadTreeSection();
  if (tree) tree.hidden = !showTree;
  root.querySelectorAll("[data-thread-view-toggle]").forEach((button) => {
    if (button instanceof HTMLButtonElement && button.dataset.loading === "1") {
      clearThreadTreeToggleLoadingState(button, showTree);
    }
    syncThreadViewToggle(button, showTree);
  });
  [
    root.querySelector("#thread-ancestors"),
    root.querySelector("#thread-focus"),
    root.querySelector(".thread-replies"),
  ].forEach((section) => {
    if (!section) return;
    section.hidden = showTree;
  });
  syncThreadTreeWideBodyClass();
}

export function applyThreadViewVisibilityFromPreference(root = document) {
  applyThreadViewVisibility(resolveThreadViewMode(), root);
}

export function setThreadParticipantsExpanded(expanded, root = document) {
  root.querySelectorAll(".right-rail[data-thread-fragment='participants']").forEach((rail) => {
    const collapsedList = rail.querySelector("[data-thread-collapsed-participants]");
    const expandedList = rail.querySelector("[data-thread-expanded-participants]");
    if (!(collapsedList instanceof HTMLElement) || !(expandedList instanceof HTMLElement)) return;
    collapsedList.hidden = expanded;
    expandedList.hidden = !expanded;
  });
}

/**
 * @returns {"none"|"linear"|"full"} none if not applicable, linear for in-page switch, full when document navigation was dispatched.
 */
async function navigateFromTreeToThreadNote(noteID, options = {}) {
  if (!noteID || !isThreadTreeMode()) return "none";
  const base = cloneThreadHistoryState();
  const prevPtxt =
    base.ptxt && typeof base.ptxt === "object" && !Array.isArray(base.ptxt) ? { ...base.ptxt } : {};
  const u = new URL(window.location.href);
  const here = `${u.pathname}${u.search}${u.hash}`;
  const treeTagged = { ...base, ptxt: { ...prevPtxt, threadView: "tree" } };
  history.replaceState(treeTagged, "", here);

  // Existing linear rows are only reply-list copies; selecting from tree must
  // rebuild the canonical focused-note panel rather than just highlighting the row.
  try {
    sessionStorage.setItem(THREAD_TREE_TO_LINEAR_KEY, "1");
  } catch {
    /* ignore quota / private mode */
  }
  if (options.inlineReplyPayload) {
    try {
      sessionStorage.setItem(THREAD_INLINE_REPLY_PENDING_KEY, JSON.stringify(options.inlineReplyPayload));
    } catch {
      /* ignore */
    }
  }
  window.location.assign(withRelays(threadHrefForNote(noteID)));
  return "full";
}

function currentFocusedThreadID() {
  const selectedID = parseSelectedQueryID();
  if (selectedID) return selectedID;
  const focused = document.querySelector(
    ".note.is-focused, .comment.is-focused, [data-thread-tree-note].is-focused",
  );
  if (focused?.dataset?.threadFocusId) return focused.dataset.threadFocusId;
  if (focused?.id?.startsWith("note-")) return focused.id.slice(5);
  return parseFocusedHashID();
}

function threadLinearSections() {
  return [
    document.querySelector("#thread-ancestors"),
    document.querySelector("#thread-focus"),
    document.querySelector(".thread-replies"),
  ];
}

function threadTreeSection() {
  return document.querySelector("#thread-tree-view");
}

/** Lowercase hex OP id from the loaded tree fragment, or "" if not yet available. */
function threadDOMRootID(scope = document) {
  const section = scope.querySelector?.("#thread-tree-view") ?? scope;
  const raw =
    threadTreeModeRoot(section)?.getAttribute("data-thread-tree-root-id") ||
    scope.querySelector?.(".feed-column[data-thread-root-id]")?.getAttribute("data-thread-root-id") ||
    scope.querySelector?.("[data-thread-root-id]")?.getAttribute("data-thread-root-id") ||
    "";
  return raw.toLowerCase();
}

/**
 * After navigation to /thread/{id}, scroll to top when moving from a deeper
 * anchor note to the thread OP URL so the header and root are visible.
 */
export function maybeScrollThreadPageToRootForNavigation(urlLike, prevPathNoteIdLower, mainEl) {
  if (!mainEl || !prevPathNoteIdLower) return;
  const url = new URL(urlLike, window.location.origin);
  const m = url.pathname.match(/^\/thread\/([^/]+)/);
  const newId = (m ? m[1] : "").toLowerCase();
  if (!newId || newId === prevPathNoteIdLower) return;
  const root = threadDOMRootID(mainEl);
  if (!root || newId !== root) return;
  requestAnimationFrame(() => {
    scrollRouteToTop(mainEl);
    requestAnimationFrame(() => scrollRouteToTop(mainEl));
  });
}

function isThreadTreeMode() {
  const tree = threadTreeSection();
  return Boolean(tree) && !tree.hidden;
}

function syncThreadTreeWideBodyClass() {
  document.body.classList.toggle("thread-tree-wide-layout", isThreadTreeMode());
}

function threadLinearTarget(id) {
  if (!id) return null;
  const el = document.getElementById(`note-${id}`);
  if (!el || el.closest("#thread-tree-view")) return null;
  return el;
}

function threadTreeTarget(id) {
  if (!id) return null;
  return document.querySelector(
    `${threadTreeCardSelector}[data-thread-tree-note="note-${CSS.escape(id)}"]`,
  );
}

function clearFocusedThreadTargets() {
  document
    .querySelectorAll(".note.is-focused, .comment.is-focused, [data-thread-tree-note].is-focused")
    .forEach((item) => item.classList.remove("is-focused"));
}

function syncTreeViewSelectionHighlight(focusID) {
  const section = threadTreeSection();
  if (!section || section.hidden) return;
  section
    .querySelectorAll(".thread-tree-root-note.is-selected, [data-thread-tree-note].is-selected")
    .forEach((el) => el.classList.remove("is-selected"));
  if (!focusID) return;
  const row = threadTreeTarget(focusID);
  if (row) {
    row.classList.add("is-selected");
    return;
  }
  const rootStory = section.querySelector(".thread-tree-root-note");
  if (rootStory?.dataset?.threadFocusId === focusID) {
    rootStory.classList.add("is-selected");
  }
}

function focusThreadTarget(target, { scroll = true, updateHash = true } = {}) {
  if (!target) return;
  clearFocusedThreadTargets();
  target.classList.add("is-focused");
  const focusID = target.dataset.threadFocusId || target.id?.replace(/^note-/, "") || "";
  if (focusID && target.closest("#thread-tree-view")) {
    syncTreeViewSelectionHighlight(focusID);
  }
  if (focusID && updateHash) {
    const nextHash = `#note-${focusID}`;
    if (window.location.hash !== nextHash) {
      history.replaceState(history.state, "", nextHash);
    }
  }
  if (scroll) {
    target.scrollIntoView({ block: "center" });
  }
}

function focusThreadNoteByID(id, options = {}) {
  if (!id) return;
  const preferTree = options.preferTree ?? isThreadTreeMode();
  const treeEl = threadTreeTarget(id);
  const linearEl = threadLinearTarget(id);
  const target = preferTree ? treeEl || linearEl : linearEl || treeEl;
  const root = threadDOMRootID();
  const idLower = id.toLowerCase();
  const scroll = options.scroll !== false;
  if (
    scroll &&
    !preferTree &&
    root &&
    idLower === root &&
    linearEl?.closest("[data-focused-hidden]")
  ) {
    scrollRouteToTop(document.querySelector("[data-nav-root]"));
    focusThreadTarget(target, { ...options, scroll: false });
  } else {
    focusThreadTarget(target, options);
  }
  if (isThreadTreeMode() && !treeEl) {
    syncTreeViewSelectionHighlight("");
  }
}

export async function ensureTreeFragmentForFocus(focusID) {
  const section = threadTreeSection();
  if (!section) return false;
  // Full thread tree is always rooted at the OP; do not refetch to re-root on a different note.
  if (threadTreeModeRoot(section)) {
    return true;
  }
  const {
    isRelayNativeThread,
    rerenderRelayNativeThread,
    hydrateThreadRoute,
  } = await import("./client-render.js");
  if (isRelayNativeThread()) {
    await rerenderRelayNativeThread();
    return Boolean(threadTreeModeRoot(threadTreeSection()));
  }
  section.setAttribute("aria-busy", "true");
  section.innerHTML = threadTreeSkeletonMarkup();
  refreshAscii(section);
  try {
    const url = new URL(window.location.href);
    url.searchParams.set("fragment", "tree");
    url.searchParams.delete("cursor");
    url.searchParams.delete("cursor_id");
    if (focusID) url.searchParams.set("tree_note", focusID);
    const res = await fetch(withRelays(`${url.pathname}${url.search}${url.hash}`), {
      headers: { Accept: "text/html" },
      credentials: "same-origin",
    });
    if (!res.ok) throw new Error("Tree view fragment failed");
    section.innerHTML = await res.text();
    if (!threadTreeModeRoot(section)) {
      throw new Error("Tree view fragment was empty");
    }
    refreshAscii(section);
    try {
      applyTreeMediaMode();
    } catch {
      // Media decoration is progressive; a valid tree fragment should still be usable.
    }
    return true;
  } catch {
    try {
      await hydrateThreadRoute(document, { forceRefresh: true });
      if (!isRelayNativeThread()) {
        throw new Error("Tree view requires client hydration");
      }
      await rerenderRelayNativeThread();
      return Boolean(threadTreeModeRoot(threadTreeSection()));
    } catch {
      section.textContent = "";
      const err = document.createElement("p");
      err.className = "muted thread-tree-load-error";
      err.setAttribute("role", "alert");
      err.textContent = "Could not load tree view.";
      section.append(err);
      return false;
    }
  } finally {
    section.removeAttribute("aria-busy");
  }
}

async function setThreadTreeMode(showTree, { persist = true, preserveFocus = true } = {}) {
  const focusID = preserveFocus ? currentFocusedThreadID() : "";
  const treeLoading = showTree && threadTreeNeedsFetch();
  if (treeLoading) {
    setThreadTreeToggleLoading(true);
  }
  try {
    if (showTree) {
      const treeReady = await ensureTreeFragmentForFocus(focusID);
      if (!treeReady) {
        showTree = false;
        persist = false;
      }
    }
  } finally {
    if (treeLoading) {
      setThreadTreeToggleLoading(false);
    }
  }
  applyThreadViewVisibility(showTree);
  if (showTree && threadTreeSection()) {
    requestAnimationFrame(() => {
      const tree = threadTreeSection();
      if (!tree) return;
      initViewMore(tree);
      refreshThreadTreeQuotes(tree);
    });
  }
  if (persist) {
    setThreadRenderModePref(showTree ? "tree" : "thread");
  }
  if (focusID) {
    requestAnimationFrame(() => {
      focusThreadNoteByID(focusID, { preferTree: showTree, scroll: true, updateHash: true });
    });
  }
  if (showTree) {
    scheduleThreadTreeConnectorGeometry();
    bindThreadTreeConnectorObserver();
  }
  queueMicrotask(() => {
    syncMobileAppNavHeight();
  });
  if (persist) {
    syncThreadViewIntoBrowserHistory(showTree);
  }
}

export function applyThreadRenderModePreference({ preserveFocus = true } = {}) {
  return setThreadTreeMode(getThreadRenderModePref() === "tree", { persist: false, preserveFocus });
}

function applyThreadViewFromHistoryStateOrPreference() {
  if (consumePendingTreeToThreadLinear()) {
    return setThreadTreeMode(false, { persist: false, preserveFocus: true });
  }
  const raw = history.state;
  const ptxt = raw && typeof raw === "object" && !Array.isArray(raw) ? raw.ptxt : null;
  if (ptxt && (ptxt.threadView === "tree" || ptxt.threadView === "linear")) {
    return setThreadTreeMode(ptxt.threadView === "tree", { persist: true, preserveFocus: true });
  }
  return applyThreadRenderModePreference({
    preserveFocus: getThreadRenderModePref() === "tree" || Boolean(parseSelectedQueryID() || parseFocusedHashID()),
  });
}

// Cache parsed tree media JSON keyed by the raw attribute string so a full
// applyTreeMediaMode pass doesn't re-parse identical attributes per row.
const treeMediaItemsCache = new WeakMap();

function treeMediaItems(item) {
  const raw = item?.dataset?.threadTreeMedia;
  if (!raw) return [];
  const cached = treeMediaItemsCache.get(item);
  if (cached && cached.raw === raw) return cached.items;
  let items = [];
  try {
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed)) {
      items = parsed.filter((entry) =>
        entry && typeof entry.url === "string" && entry.url &&
        (entry.type === "image" || entry.type === "video"));
    }
  } catch {
    items = [];
  }
  treeMediaItemsCache.set(item, { raw, items });
  return items;
}

function treeMediaGrid(item, items) {
  return createMediaGrid(items, {
    wrapperTag: "div",
    gridTag: "div",
    wrapperClass: "thread-tree-media-grid-wrap",
    stopPropagation: true,
    onOpen: (index) => openImageViewer(items, index, item),
  });
}

function renderTreeMediaMount(item, items, enabled) {
  const mount = item.querySelector("[data-thread-tree-media-mount]");
  if (!mount) return;
  const shouldShow = enabled && items.length > 0;
  const existing = mount.querySelector(":scope > .note-media-grid-wrap");
  if (
    shouldShow &&
    existing instanceof HTMLElement &&
    existing.dataset.mediaGridSignature === mediaGridSignature(items)
  ) {
    mount.hidden = false;
    const hydrated = hydrateMediaGrid(existing, items, {
      stopPropagation: true,
      onOpen: (index) => openImageViewer(items, index, item),
    });
    if (hydrated) return;
  }
  mount.textContent = "";
  mount.hidden = !shouldShow;
  if (!shouldShow) return;
  mount.append(treeMediaGrid(item, items));
}

function applyTreeMediaItem(item, enabled) {
  const source = item.dataset.threadTreeSource || "";
  const displayAttr = item.getAttribute("data-thread-tree-display-source");
  const hasTreeMedia = item.hasAttribute("data-thread-tree-media");
  // thread.html always emits data-thread-tree-display-source (often ""). That is
  // ambiguous: no-media rows need full `source`, while media rows with blank display
  // mean the body was only URLs after strip. data-thread-tree-media disambiguates.
  let displaySource = source;
  if (displayAttr !== null && displayAttr.length > 0) {
    displaySource = displayAttr;
  } else if (hasTreeMedia) {
    displaySource = "";
  }
  const textTarget = item.querySelector(".thread-tree-text");
  if (textTarget) {
    const desired = enabled ? displaySource : source;
    if (textTarget.dataset.lastApplied !== desired) {
      resetTruncatableViewMore(textTarget);
      textTarget.textContent = "";
      if (desired.trim()) {
        desired.split("\n").forEach((line) => {
          const row = document.createElement("span");
          row.className = "thread-tree-text-line";
          row.textContent = line;
          textTarget.append(row);
        });
      }
      textTarget.dataset.lastApplied = desired;
      requestAnimationFrame(() => {
        if (textTarget.isConnected) applyTruncatableViewMore(textTarget);
      });
    }
    textTarget.hidden = !desired.trim();
  }

  const mediaWrap = item.querySelector("[data-thread-tree-media-wrap]");
  const mediaButton = item.querySelector("[data-thread-tree-media-toggle]");
  const items = treeMediaItems(item);
  if (!mediaWrap || !mediaButton || items.length === 0) return;
  mediaWrap.hidden = true;
  mediaButton.setAttribute("aria-expanded", enabled ? "true" : "false");
  renderTreeMediaMount(item, items, enabled);
}

function applyTreeMediaMode() {
  const enabled = getImageModePref();
  document.querySelectorAll("[data-thread-tree-note]").forEach((item) => {
    applyTreeMediaItem(item, enabled);
  });
  refreshThreadTreeQuotes(document);
  scheduleThreadTreeConnectorGeometry();
}

let threadTreeConnectorRaf = 0;
let threadTreeConnectorObserver = null;

function threadTreeConnectorJoinY(row) {
  if (!(row instanceof Element)) return null;
  const av = row.querySelector(".thread-tree-avatar, .thread-tree-avatar-fallback");
  if (av) {
    const r = av.getBoundingClientRect();
    if (r.height > 0) return r.top + r.height / 2;
  }
  const r = row.getBoundingClientRect();
  if (r.height > 0) return r.top + r.height / 2;
  return null;
}

function clearThreadTreeGutterInlineParentRises(mode) {
  mode.querySelectorAll(".thread-tree-gutter.has-parent").forEach((gutter) =>
    gutter.style.removeProperty("--thread-tree-parent-rise"),
  );
}

/** Only first-child gutters get per-row measured rise; sibling gutters keep stylesheet fallbacks (no lineage inheritance bugs). */
function setThreadTreeGutterParentRisePx(gutter, px) {
  if (!(gutter instanceof Element)) return;
  if (px <= 0) gutter.style.removeProperty("--thread-tree-parent-rise");
  else gutter.style.setProperty("--thread-tree-parent-rise", `${px}px`);
}

function syncThreadTreeTailStubRails(section) {
  section.querySelectorAll(".thread-tree-tail-rail").forEach((rail) => {
    rail.style.removeProperty("--thread-tree-tail-stub-height");
    rail.style.removeProperty("--thread-tree-tail-continuation-top");
  });
  section.querySelectorAll(".thread-tree-tail-rail--stub").forEach((rail) => {
    const item = rail.closest(".thread-tree-item");
    if (!item) return;
    const nestedRow = item.querySelector(
      ":scope > .thread-tree-row-cols .thread-tree.thread-tree-branch > .thread-tree-item:first-child .thread-tree-row-cols .thread-tree-card-stack > .thread-tree-card:first-child .thread-tree-row",
    );
    const joinNested = threadTreeConnectorJoinY(nestedRow);
    const railRect = rail.getBoundingClientRect();
    /* Leaf row with no nested branch: L has no downward stub (0px). */
    const stubPx =
      joinNested == null ? 0 : Math.round(Math.max(0, joinNested - railRect.top));
    rail.style.setProperty("--thread-tree-tail-stub-height", `${stubPx}px`);
    if (rail.classList.contains("thread-tree-tail-rail--continuation-below")) {
      rail.style.setProperty("--thread-tree-tail-continuation-top", `${stubPx}px`);
    }
  });
}

function syncThreadTreeConnectorGeometry() {
  const section = threadTreeSection();
  if (!section || section.hidden) return;
  const mode = threadTreeModeRoot(section);
  if (!mode || !mode.classList.contains("thread-tree-mode")) return;
  /* HN-style flat tree has no gutter spines; connector math is unused. */
  if (!mode.querySelector(".thread-tree-gutter")) return;

  clearThreadTreeGutterInlineParentRises(mode);

  const rootRow = mode.querySelector(":scope > .thread-tree-root-note .thread-tree-row");
  const list = mode.querySelector(":scope > .thread-tree");
  const firstGutter = list?.querySelector(
    ":scope > .thread-tree-item:first-child .thread-tree-row-cols .thread-tree-gutter.has-parent",
  );

  const rootJoin = threadTreeConnectorJoinY(rootRow);
  const fg = firstGutter?.getBoundingClientRect();
  if (rootJoin != null && fg != null && fg.height >= 0) {
    setThreadTreeGutterParentRisePx(firstGutter, Math.max(0, fg.top - rootJoin));
  }

  mode.querySelectorAll(".thread-tree.thread-tree-branch").forEach((branch) => {
    const parentItem = branch.closest(".thread-tree-item");
    if (!parentItem) return;
    const parentRow = parentItem.querySelector(
      ":scope > .thread-tree-row-cols .thread-tree-card-stack > .thread-tree-card:first-child .thread-tree-row",
    );
    const nestGutter = branch.querySelector(
      ":scope > .thread-tree-item:first-child .thread-tree-row-cols .thread-tree-gutter.has-parent",
    );
    const pJoin = threadTreeConnectorJoinY(parentRow);
    const ng = nestGutter?.getBoundingClientRect();
    if (pJoin != null && ng != null && ng.height >= 0) {
      setThreadTreeGutterParentRisePx(nestGutter, Math.max(0, ng.top - pJoin));
    }
  });

  syncThreadTreeTailStubRails(mode);
}

function scheduleThreadTreeConnectorGeometry() {
  if (threadTreeConnectorRaf) cancelAnimationFrame(threadTreeConnectorRaf);
  threadTreeConnectorRaf = requestAnimationFrame(() => {
    threadTreeConnectorRaf = 0;
    syncThreadTreeConnectorGeometry();
    requestAnimationFrame(syncThreadTreeConnectorGeometry);
  });
}

function bindThreadTreeConnectorObserver() {
  const section = threadTreeSection();
  if (!section || typeof ResizeObserver === "undefined") return;
  threadTreeConnectorObserver?.disconnect();
  threadTreeConnectorObserver = new ResizeObserver(() => scheduleThreadTreeConnectorGeometry());
  threadTreeConnectorObserver.observe(section);
}

async function handleThreadReplyComposeClick(link, container) {
  const rootID =
    link.getAttribute("data-reply-root-id") || container.getAttribute("data-reply-root-id") || "";
  const targetID =
    link.getAttribute("data-reply-target-id") || container.getAttribute("data-reply-target-id") || "";
  const pubkey =
    link.getAttribute("data-reply-pubkey") || container.getAttribute("data-reply-pubkey") || "";
  const replyingToLabel = container.getAttribute("data-ascii-author") || "";
  const inlinePayload = { targetID, rootID, pubkey, replyingTo: replyingToLabel };

  if (link.closest("#thread-tree-view") && isThreadTreeMode()) {
    const mode = await navigateFromTreeToThreadNote(targetID, { inlineReplyPayload: inlinePayload });
    if (mode === "full") return;
    await doubleRaf();
  }

  const anchor = visibleThreadNoteElement(targetID, container);
  if (!anchor) return;

  await openThreadInlineComposer(document, {
    anchorEl: anchor,
    rootID,
    targetID,
    pubkey,
    replyingToLabel,
  });
}

function visibleThreadNoteElement(targetID, preferred = null) {
  if (preferred instanceof HTMLElement && !preferred.hidden && !preferred.closest("[hidden]")) {
    const rect = preferred.getBoundingClientRect();
    if (rect.width > 0 && rect.height > 0) return preferred;
  }
  if (!targetID) return null;
  const candidates = [...document.querySelectorAll(`#note-${CSS.escape(targetID)}`)];
  for (const node of candidates) {
    if (!(node instanceof HTMLElement)) continue;
    if (node.hidden || node.closest("[hidden]")) continue;
    const rect = node.getBoundingClientRect();
    if (rect.width > 0 && rect.height > 0) return node;
  }
  const fallback = document.getElementById(`note-${targetID}`);
  return fallback instanceof HTMLElement ? fallback : null;
}

function consumePendingThreadInlineReply() {
  if (!document.querySelector("#thread-summary")) return false;
  let raw = "";
  try {
    raw = sessionStorage.getItem(THREAD_INLINE_REPLY_PENDING_KEY) || "";
  } catch {
    return false;
  }
  if (!raw) return false;
  let p;
  try {
    p = JSON.parse(raw);
  } catch {
    try {
      sessionStorage.removeItem(THREAD_INLINE_REPLY_PENDING_KEY);
    } catch {
      /* ignore */
    }
    return false;
  }
  if (!p || typeof p.targetID !== "string" || !p.targetID) {
    try {
      sessionStorage.removeItem(THREAD_INLINE_REPLY_PENDING_KEY);
    } catch {
      /* ignore */
    }
    return false;
  }
  try {
    sessionStorage.removeItem(THREAD_INLINE_REPLY_PENDING_KEY);
  } catch {
    /* ignore */
  }
  void doubleRaf().then(() => {
    const anchor = visibleThreadNoteElement(p.targetID);
    if (anchor) {
      void openThreadInlineComposer(document, {
        anchorEl: anchor,
        rootID: p.rootID || "",
        targetID: p.targetID || "",
        pubkey: p.pubkey || "",
        replyingToLabel: p.replyingTo || "",
      });
    }
    focusFromHash();
  });
  return true;
}

function attachListeners() {
  if (listenersAttached) return;
  listenersAttached = true;
  document.addEventListener(
    "click",
    (event) => {
      if (!(event.target instanceof Element)) return;
      if (!document.getElementById("thread-summary")) return;
      if (
        !event.target.closest(
          "#thread-tree-view, #thread-focus, #thread-ancestors, .thread-replies, #thread-summary",
        )
      )
        return;
      const link = event.target.closest(
        "a[data-reply-action], a[href^='/thread/'], button[data-reply-action]",
      );
      if (!link) return;
      const container =
        link.closest(".comment, article.note, [data-thread-tree-note]") ||
        link.closest("[data-reply-target-id]");
      if (!container) return;
      if (link.hasAttribute("data-repost-action")) return;
      const text = (link.textContent || "").trim().toLowerCase();
      if (!link.hasAttribute("data-reply-action") && !text.startsWith("reply")) return;
      const inTree = Boolean(link.closest("#thread-tree-view, #thread-summary"));
      const inThreadBody = Boolean(
        link.closest("#thread-focus, #thread-ancestors, .thread-replies"),
      );
      if (!inTree && !inThreadBody) return;
      event.preventDefault();
      event.stopPropagation();
      void handleThreadReplyComposeClick(link, container);
    },
    true,
  );
  document.addEventListener("click", (event) => {
    const treeCard = closestFromEventTarget(event.target, "[data-thread-tree-note]");
    if (!treeCard) return;
    if (!treeCard.closest("#thread-tree-view")) return;
    if (closestFromEventTarget(event.target, interactiveSelector)) return;
    if (closestFromEventTarget(event.target, embeddedMediaSelector)) return;
    event.preventDefault();
    event.stopPropagation();
    const noteID = treeCard.dataset.threadFocusId || "";
    if (!noteID) return;
    if (isThreadTreeMode()) {
      void navigateFromTreeToThreadNote(noteID);
    } else {
      focusThreadNoteByID(noteID, { preferTree: false, scroll: true, updateHash: true });
    }
  }, true);
  document.addEventListener("click", (event) => {
    const toggle = closestFromEventTarget(event.target, "[data-thread-view-toggle]");
    if (toggle instanceof HTMLButtonElement) {
      if (toggle.dataset.loading === "1") return;
      event.preventDefault();
      const currentMode = (toggle.dataset.threadViewCurrent || toggle.textContent || "thread")
        .trim()
        .toLowerCase();
      const nextTree = currentMode === "thread";
      if (nextTree && threadTreeNeedsFetch()) {
        setThreadTreeToggleLoading(true);
      }
      void setThreadTreeMode(nextTree);
      return;
    }

    const hiddenToggle = closestFromEventTarget(event.target, "[data-thread-hidden-toggle]");
    if (hiddenToggle) {
      const hiddenItems = [...document.querySelectorAll("[data-focused-hidden]")];
      if (!hiddenItems.length) return;
      event.preventDefault();
      const expand = hiddenItems.some((item) => item.hidden);
      hiddenItems.forEach((item) => {
        item.hidden = !expand;
      });
      hiddenToggle.textContent = expand
        ? hiddenToggle.dataset.expandedLabel || "hide messages above"
        : hiddenToggle.dataset.collapsedLabel || "show messages above";
      if (expand) {
        hiddenItems.forEach((item) => {
          initViewMore(item);
          refreshAscii(item);
          void refreshVisibleNoteProfiles(item);
        });
      }
      return;
    }

    const filteredToggle = closestFromEventTarget(event.target, "[data-thread-filtered-replies-toggle]");
    if (filteredToggle) {
      const filteredBlock = document.querySelector("[data-thread-filtered-replies]");
      if (!filteredBlock) return;
      event.preventDefault();
      const expand = filteredBlock.hidden;
      filteredBlock.hidden = !expand;
      filteredToggle.dataset.expanded = expand ? "1" : "0";
      filteredToggle.textContent = expand
        ? filteredToggle.dataset.expandedLabel || "hide"
        : filteredToggle.dataset.collapsedLabel || "show more";
      if (expand) {
        initViewMore(filteredBlock);
        refreshAscii(filteredBlock);
        void refreshVisibleNoteProfiles(filteredBlock);
      }
      setThreadParticipantsExpanded(expand);
      scheduleThreadTreeConnectorGeometry();
      return;
    }

    const treeFilteredToggle = closestFromEventTarget(
      event.target,
      "[data-thread-tree-filtered-replies-toggle]",
    );
    if (treeFilteredToggle) {
      const treeMode = treeFilteredToggle.closest("[data-thread-tree-view]");
      const filteredTree = treeMode?.querySelector("[data-thread-tree-filtered-replies]");
      if (!filteredTree) return;
      event.preventDefault();
      const expand = filteredTree.hidden;
      const previousTree = filteredTree.previousElementSibling;
      const continuesVisibleTree = Boolean(
        previousTree?.classList.contains("hn-comment-tree") &&
        !previousTree.classList.contains("thread-tree-filtered-replies"),
      );
      filteredTree.hidden = !expand;
      filteredTree.classList.toggle("continues-thread-tree", expand && continuesVisibleTree);
      previousTree?.classList.toggle(
        "has-expanded-filtered-replies",
        expand && continuesVisibleTree,
      );
      treeFilteredToggle.dataset.expanded = expand ? "1" : "0";
      treeFilteredToggle.setAttribute("aria-expanded", expand ? "true" : "false");
      treeFilteredToggle.textContent = expand
        ? treeFilteredToggle.dataset.expandedLabel || "hide"
        : treeFilteredToggle.dataset.collapsedLabel || "show more";
      setThreadParticipantsExpanded(expand);
      if (expand) {
        initViewMore(filteredTree);
        refreshAscii(filteredTree);
        refreshThreadTreeQuotes(filteredTree);
        void refreshVisibleNoteProfiles(filteredTree);
      }
      scheduleThreadTreeConnectorGeometry();
      return;
    }

    const otherRepliesToggle = closestFromEventTarget(event.target, "[data-thread-other-replies-toggle]");
    if (otherRepliesToggle) {
      const hiddenItems = [...document.querySelectorAll("[data-focused-other-replies]")];
      if (!hiddenItems.length) return;
      const expand = hiddenItems.some((item) => item.hidden);
      hiddenItems.forEach((item) => {
        item.hidden = !expand;
      });
      otherRepliesToggle.textContent = expand
        ? otherRepliesToggle.dataset.expandedLabel || "hide replies to OP"
        : otherRepliesToggle.dataset.collapsedLabel || "view more replies to OP";
      if (expand) {
        const section = hiddenItems[0];
        initViewMore(section);
        refreshAscii(section);
        void refreshVisibleNoteProfiles(section);
      }
      scheduleThreadTreeConnectorGeometry();
      return;
    }

    const treeMediaToggle = closestFromEventTarget(event.target, "[data-thread-tree-media-toggle]");
    if (treeMediaToggle) {
      event.preventDefault();
      event.stopPropagation();
      const item = treeMediaToggle.closest("[data-thread-tree-note]");
      if (!item) return;
      const expanded = treeMediaToggle.getAttribute("aria-expanded") === "true";
      treeMediaToggle.setAttribute("aria-expanded", expanded ? "false" : "true");
      applyTreeMediaItem(item, getImageModePref());
      return;
    }

    const treeCollapseBtn = closestFromEventTarget(event.target, "[data-thread-tree-collapse]");
    if (treeCollapseBtn && treeCollapseBtn.closest("#thread-tree-view")) {
      event.preventDefault();
      event.stopPropagation();
      const id = treeCollapseBtn.dataset.threadTreeCollapse || "";
      if (!id) return;
      const treeView = treeCollapseBtn.closest("#thread-tree-view");
      const target = treeView?.querySelector(`[data-thread-tree-collapsible="${CSS.escape(id)}"]`);
      if (!target) return;
      const collapsed = target.hidden;
      target.hidden = !collapsed;
      treeCollapseBtn.textContent = collapsed ? "[-]" : "[+]";
      treeCollapseBtn.setAttribute("aria-expanded", target.hidden ? "false" : "true");
      scheduleThreadTreeConnectorGeometry();
      return;
    }

    const loadMore = closestFromEventTarget(event.target, "[data-thread-load-more]");
    if (loadMore) {
      event.preventDefault();
      void loadMoreReplies(loadMore);
    }
  });
}

function visibleComments() {
  const selector = isThreadTreeMode() ? threadTreeCardSelector : ".note, .comment";
  return [...document.querySelectorAll(selector)].filter((item) => item.offsetParent !== null);
}

function focusComment(comment) {
  focusThreadTarget(comment, { scroll: true, updateHash: true });
}

function focusFromHash() {
  const selectedID = parseSelectedQueryID();
  const hashID = parseFocusedHashID();
  const id = selectedID || hashID;
  if (!id) return;
  focusThreadNoteByID(id, {
    preferTree: isThreadTreeMode(),
    scroll: true,
    updateHash: Boolean(selectedID && selectedID !== hashID),
  });
}

export function teardownThreadTreeConnector() {
  threadTreeConnectorObserver?.disconnect();
  threadTreeConnectorObserver = null;
  if (threadTreeConnectorRaf) {
    cancelAnimationFrame(threadTreeConnectorRaf);
    threadTreeConnectorRaf = 0;
  }
  document.body.classList.remove("thread-tree-wide-layout");
}

// Thread SSR ships with `data-ascii-reaction-viewer=""` for every note so the
// HTML is viewer-agnostic and safe to share at the CDN. After paint the
// client fills in the current viewer's reaction state by re-running the
// existing /api/reaction-stats path on whatever notes + comments are on the
// page.
function collectThreadNoteIds(root = document) {
  const ids = [];
  const seen = new Set();
  for (const el of root.querySelectorAll("[id^='note-'][data-ascii-reaction-viewer]")) {
    const id = el.id.replace(/^note-/, "");
    if (!id || seen.has(id)) continue;
    seen.add(id);
    ids.push(id);
    if (ids.length >= 50) break;
  }
  return ids;
}

function refreshThreadViewerReactionState(root = document) {
  const ids = collectThreadNoteIds(root);
  if (!ids.length) return;
  // opts.ids bypasses the feed-only scope in refreshVisibleFeedReactionStats;
  // reply counts are server-truthful + viewer-agnostic, so we skip them here.
  void refreshVisibleFeedReactionStats(root, null, "", { ids });
}

function refreshThreadViewerReactionStateDeferred(root = document) {
  const run = () => refreshThreadViewerReactionState(root);
  if (typeof requestIdleCallback === "function") {
    requestIdleCallback(run, { timeout: 1500 });
  } else {
    setTimeout(run, 0);
  }
}

export function initThreadPage() {
  applyThreadViewVisibilityFromPreference();
  attachListeners();
  applyTreeMediaMode();
  void applyThreadViewFromHistoryStateOrPreference().then(() => {
    if (!consumePendingThreadInlineReply()) {
      focusFromHash();
    }
  });
  bindThreadTreeConnectorObserver();
  scheduleThreadTreeConnectorGeometry();
  if (!document.body?.dataset?.guestV2) {
    void refreshVisibleNoteProfiles(document);
    refreshThreadViewerReactionStateDeferred();
  }
  if (!hashListenerBound) {
    hashListenerBound = true;
    window.addEventListener("hashchange", focusFromHash);
  }
  if (!treeMediaModeListenerBound) {
    treeMediaModeListenerBound = true;
    window.addEventListener("ptxt:image-mode-changed", () => {
      applyTreeMediaMode();
    });
  }
  if (!window.__ptxtThreadKeyNavBound) {
    window.__ptxtThreadKeyNavBound = true;
    document.addEventListener("keydown", (event) => {
      if (event.target.matches("input, textarea, button, select")) return;
      if (!["j", "k", "ArrowDown", "ArrowUp"].includes(event.key)) return;
      const comments = visibleComments();
      if (!comments.length) return;
      const current = document.querySelector(".note.is-focused, .comment.is-focused, [data-thread-tree-note].is-focused");
      const index = Math.max(0, comments.indexOf(current));
      if (event.key === "j" || event.key === "ArrowDown") {
        focusComment(comments[Math.min(comments.length - 1, index + 1)]);
      } else {
        focusComment(comments[Math.max(0, index - 1)]);
      }
      event.preventDefault();
    });
  }
}
