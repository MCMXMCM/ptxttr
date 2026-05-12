import { refreshAscii } from "./ascii.js";
import { prepareInlineVideo } from "./inline-video.js";
import { addAsciiWidthHint } from "./ascii-width-hint.js";
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

let listenersAttached = false;
let hashListenerBound = false;
let treeMediaModeListenerBound = false;
const threadTreeCardSelector = "#thread-tree-view [data-thread-tree-note]";
/** Set before SPA navigate to a subthread so init opens linear mode even when tree pref is on. */
const THREAD_TREE_TO_LINEAR_KEY = "ptxtTreeToThreadLinear";

function closestFromEventTarget(target, selector) {
  if (!(target instanceof Element)) return null;
  return target.closest(selector);
}

function appendThreadReplies(html) {
  const list = document.querySelector("#thread-replies");
  if (!list || !html.trim()) return 0;
  const template = document.createElement("template");
  template.innerHTML = html;
  let appended = 0;
  template.content.querySelectorAll(".comment").forEach((comment) => {
    if (comment.id && document.getElementById(comment.id)) return;
    list.append(comment);
    appended += 1;
  });
  if (appended > 0) initViewMore(list);
  return appended;
}

async function loadMoreReplies(button) {
  if (!button || button.dataset.loading === "1") return;
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
  const current = new URL(window.location.href);
  const params = new URLSearchParams({
    fragment: "replies",
    cursor: button.dataset.cursor || "",
    cursor_id: button.dataset.cursorId || "",
  });
  const rootID = button.dataset.rootId || "";
  const parentID = button.dataset.parentId || "";
  const selectedID = button.dataset.selectedId || "";
  if (rootID) params.set("root_id", rootID);
  if (parentID) params.set("parent_id", parentID);
  if (selectedID) params.set("selected_id", selectedID);
  // Relays now flow via the X-Ptxt-Relays header (fetchWithSession).
  addAsciiWidthHint(params, current.pathname);
  try {
    const response = await fetchWithSession(`${current.pathname}?${params.toString()}`);
    if (!response.ok) throw new Error(`Reply request failed: ${response.status}`);
    const html = await response.text();
    const appended = appendThreadReplies(html);
    button.dataset.cursor = response.headers.get("X-Ptxt-Cursor") || button.dataset.cursor || "";
    button.dataset.cursorId = response.headers.get("X-Ptxt-Cursor-Id") || button.dataset.cursorId || "";
    const hasMore = response.headers.get("X-Ptxt-Has-More") === "1";
    if (appended === 0 && hasMore) {
      button.textContent = "No new replies to show";
      button.disabled = true;
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
    button.textContent = error.message || "Load failed";
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
  const next = new URL(`/thread/${noteID}`, cur.origin);
  next.search = cur.search;
  return next.toString();
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

async function navigateFromTreeToThreadNote(noteID) {
  if (!noteID || !isThreadTreeMode()) return;
  const base = cloneThreadHistoryState();
  const prevPtxt =
    base.ptxt && typeof base.ptxt === "object" && !Array.isArray(base.ptxt) ? { ...base.ptxt } : {};
  const u = new URL(window.location.href);
  const here = `${u.pathname}${u.search}${u.hash}`;
  const treeTagged = { ...base, ptxt: { ...prevPtxt, threadView: "tree" } };
  history.replaceState(treeTagged, "", here);

  const linearEl = threadLinearTarget(noteID);
  if (linearEl) {
    const nextURL = `${u.pathname}${u.search}#note-${noteID}`;
    history.pushState(
      { ...treeTagged, ptxt: { ...treeTagged.ptxt, threadView: "linear" } },
      "",
      nextURL,
    );
    const root = threadDOMRootID();
    const jumpToRoot = Boolean(root && noteID.toLowerCase() === root);
    await setThreadTreeMode(false, { persist: true, preserveFocus: false });
    requestAnimationFrame(() => {
      const main = document.querySelector("[data-nav-root]");
      if (jumpToRoot) {
        scrollRouteToTop(main);
        focusThreadNoteByID(noteID, { preferTree: false, scroll: false, updateHash: false });
        return;
      }
      focusThreadNoteByID(noteID, { preferTree: false, scroll: true, updateHash: false });
    });
    return;
  }

  try {
    sessionStorage.setItem(THREAD_TREE_TO_LINEAR_KEY, "1");
  } catch {
    /* ignore quota / private mode */
  }
  window.dispatchEvent(new CustomEvent("ptxt:navigate", { detail: { href: threadHrefForNote(noteID) } }));
}

function currentFocusedThreadID() {
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
  const raw =
    scope
      .querySelector("#thread-tree-view [data-thread-tree-view][data-thread-tree-root-id]")
      ?.getAttribute("data-thread-tree-root-id") || "";
  return raw.toLowerCase();
}

/**
 * After SPA navigation to /thread/{id}, scroll to top when moving from a deeper
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

function currentThreadFragmentURL(fragment, focusID = "") {
  const current = new URL(window.location.href);
  const params = new URLSearchParams({ fragment });
  if (focusID) params.set("tree_note", focusID);
  // Relays travel as X-Ptxt-Relays via fetchWithSession; no need in the URL.
  addAsciiWidthHint(params, current.pathname);
  return `${current.pathname}?${params.toString()}`;
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

async function ensureTreeFragmentForFocus(focusID) {
  const section = threadTreeSection();
  if (!section) return;
  // Full thread tree is always rooted at the OP; do not refetch to re-root on a different note.
  if (section.querySelector("[data-thread-tree-view]")) {
    return;
  }
  section.setAttribute("aria-busy", "true");
  section.innerHTML = threadTreeSkeletonMarkup();
  refreshAscii(section);
  try {
    const response = await fetchWithSession(currentThreadFragmentURL("tree", focusID || ""));
    if (!response.ok) throw new Error(`Tree request failed: ${response.status}`);
    section.innerHTML = await response.text();
    applyTreeMediaMode();
    refreshAscii(section);
  } catch {
    section.textContent = "";
    const err = document.createElement("p");
    err.className = "muted thread-tree-load-error";
    err.setAttribute("role", "alert");
    err.textContent = "Could not load tree view.";
    section.append(err);
  } finally {
    section.removeAttribute("aria-busy");
  }
}

async function setThreadTreeMode(showTree, { persist = true, preserveFocus = true } = {}) {
  const focusID = preserveFocus ? currentFocusedThreadID() : "";
  if (showTree) {
    await ensureTreeFragmentForFocus(focusID);
  }
  const tree = threadTreeSection();
  if (tree) tree.hidden = !showTree;
  if (showTree && tree) {
    requestAnimationFrame(() => initViewMore(tree));
  }
  document.querySelectorAll("[data-thread-tree-toggle]").forEach((button) => {
    button.textContent = showTree
      ? button.dataset.expandedLabel || "thread view"
      : button.dataset.collapsedLabel || "tree view";
  });
  threadLinearSections().forEach((section) => {
    if (!section) return;
    section.hidden = showTree;
  });
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
  syncThreadTreeWideBodyClass();
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
    preserveFocus: getThreadRenderModePref() === "tree" || Boolean(parseFocusedHashID()),
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

function treeMediaPreview(item) {
  const figure = document.createElement("figure");
  if (item.type === "video") {
    figure.className = "note-media-preview note-video-preview";
    const video = document.createElement("video");
    video.src = item.url;
    video.controls = true;
    video.preload = "metadata";
    prepareInlineVideo(video);
    figure.append(video);
    return figure;
  }
  figure.className = "note-media-preview note-image-preview";
  const img = document.createElement("img");
  img.src = item.url;
  img.alt = "";
  img.loading = "lazy";
  img.decoding = "async";
  figure.append(img);
  return figure;
}

function renderTreeMediaMount(item, items, expanded) {
  const mount = item.querySelector("[data-thread-tree-media-mount]");
  if (!mount) return;
  const shouldShow = expanded && items.length > 0;
  // Avoid teardown/rebuild when nothing changed; checking children + hidden
  // is enough since items are stable for a given row.
  if (mount.hidden === !shouldShow && mount.children.length === items.length && !shouldShow) {
    return;
  }
  mount.textContent = "";
  mount.hidden = !shouldShow;
  if (!shouldShow) return;
  items.forEach((entry) => {
    mount.append(treeMediaPreview(entry));
  });
}

function applyTreeMediaItem(item, enabled) {
  const source = item.dataset.threadTreeSource || "";
  const displaySource = item.dataset.threadTreeDisplaySource || source;
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
  mediaWrap.hidden = !enabled;
  const expanded = enabled && mediaButton.getAttribute("aria-expanded") === "true";
  mediaButton.setAttribute("aria-expanded", expanded ? "true" : "false");
  renderTreeMediaMount(item, items, expanded);
}

function applyTreeMediaMode() {
  const enabled = getImageModePref();
  document.querySelectorAll("[data-thread-tree-note]").forEach((item) => {
    applyTreeMediaItem(item, enabled);
  });
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
  const mode = section.querySelector("[data-thread-tree-view].thread-tree-mode");
  if (!mode) return;
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

function attachListeners() {
  if (listenersAttached) return;
  listenersAttached = true;
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
    const treeToggle = closestFromEventTarget(event.target, "[data-thread-tree-toggle]");
    if (treeToggle) {
      event.preventDefault();
      void setThreadTreeMode(!isThreadTreeMode());
      return;
    }

    const hiddenToggle = closestFromEventTarget(event.target, "[data-thread-hidden-toggle]");
    if (hiddenToggle) {
      const hiddenItems = [...document.querySelectorAll("[data-focused-hidden]")];
      if (!hiddenItems.length) return;
      const expand = hiddenItems.some((item) => item.hidden);
      hiddenItems.forEach((item) => {
        item.hidden = !expand;
      });
      hiddenToggle.textContent = expand
        ? hiddenToggle.dataset.expandedLabel || "hide messages above"
        : hiddenToggle.dataset.collapsedLabel || "show messages above";
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
  const id = parseFocusedHashID();
  if (!id) return;
  focusThreadNoteByID(id, { preferTree: isThreadTreeMode(), scroll: true, updateHash: false });
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

export function initThreadPage() {
  attachListeners();
  applyTreeMediaMode();
  void applyThreadViewFromHistoryStateOrPreference();
  bindThreadTreeConnectorObserver();
  scheduleThreadTreeConnectorGeometry();
  refreshThreadViewerReactionState();
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
  focusFromHash();
}
