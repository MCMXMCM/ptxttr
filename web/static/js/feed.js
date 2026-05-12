import { addAsciiWidthHint } from "./ascii-width-hint.js";
import { wireAvatarImageFallbacks } from "./layout.js";
import { fetchWithSession, normalizedPubkey } from "./session.js";
import { refreshVisibleFeedNoteMetadata } from "./feed-metadata.js";
import { bindProfileStatLinks } from "./profile-tabs.js";
import { initViewMore } from "./notes.js";
import { syncBookmarkState } from "./bookmarks.js";
import { feedSortForSession, getFeedSortPref } from "./sort-prefs.js";

let initialized = false;
const loadMoreRequestTimeoutMs = 12000;
const boundLoadMoreButtons = new WeakSet();
const loadMoreIntersectionObservers = new WeakMap();
const loadMoreHandlers = new WeakMap();

function disconnectLoadMoreIntersection(button) {
  const existing = loadMoreIntersectionObservers.get(button);
  if (existing) {
    existing.disconnect();
    loadMoreIntersectionObservers.delete(button);
  }
  delete button.dataset.ptxtLoadMoreIo;
}

/** Infinite scroll must not run while the SSR deferred feed shell still shows `[data-feed-loader]` (race with navigation hydration). */
function tryConnectLoadMoreIntersection(feed, button, loadMoreFn) {
  if (!("IntersectionObserver" in window)) return;
  if (button.dataset.ptxtLoadMoreIo === "1") return;
  if (feed.querySelector("[data-feed-loader]")) return;
  const observer = new IntersectionObserver(
    (entries) => {
      if (entries.some((entry) => entry.isIntersecting)) void loadMoreFn();
    },
    {
      rootMargin: "600px 0px",
    },
  );
  observer.observe(button);
  loadMoreIntersectionObservers.set(button, observer);
  button.dataset.ptxtLoadMoreIo = "1";
}

function appendReadArticles(reads, html) {
  const template = document.createElement("template");
  template.innerHTML = html;
  let appended = 0;
  template.content.querySelectorAll(".read-article").forEach((article) => {
    if (article.id && document.getElementById(article.id)) return;
    reads.append(article);
    appended += 1;
  });
  if (appended > 0) {
    initViewMore(reads);
    void syncBookmarkState(document);
  }
  return appended;
}

export function initFeedLoadMore(root = document) {
  const button = root.querySelector("[data-load-more]");
  if (!button) return;
  const feedPath = button.dataset.feedUrl || "/feed";
  const feedPathname = new URL(feedPath, window.location.origin).pathname;
  const isReads = feedPath === "/reads";
  const isSearch = feedPath === "/search";
  const isTag = feedPathname.startsWith("/tag/");
  const isNotifications = feedPath === "/notifications";
  const cursorFromHeaders = isReads || isSearch || isTag || isNotifications;
  const feed = isReads
    ? root.querySelector("[data-reads]")
    : root.querySelector("[data-feed]");
  if (!feed) return;
  if (boundLoadMoreButtons.has(button)) {
    const existingHandler = loadMoreHandlers.get(button);
    if (typeof existingHandler === "function") {
      tryConnectLoadMoreIntersection(feed, button, existingHandler);
    }
    return;
  }
  boundLoadMoreButtons.add(button);
  if (button.dataset.loading === "1") {
    delete button.dataset.loading;
    button.classList.remove("is-pressed");
    button.disabled = false;
    button.removeAttribute("aria-busy");
    button.textContent = button.textContent || "Load more";
  }
  let loading = false;
  const defaultLabel = button.textContent;

  const stopLoading = () => {
    disconnectLoadMoreIntersection(button);
  };

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
    button.removeAttribute("aria-busy");
  };

  const setNoMore = () => {
    setLoadingState(false);
    let doneLabel = "No more notes";
    if (isReads) doneLabel = "No more reads";
    else if (isNotifications) doneLabel = "No more mentions";
    button.textContent = doneLabel;
    button.disabled = true;
    stopLoading();
  };

  const loadMore = async () => {
    if (loading) return;
    if (feed.querySelector("[data-feed-loader]")) return;
    loading = true;
    setLoadingState(true);
    let reachedEnd = false;
    try {
      const url = feedPath;
      const previousCursor = button.dataset.cursor || "";
      const previousCursorID = button.dataset.cursorId || "";
      const params = new URLSearchParams({
        fragment: button.dataset.fragment || "1",
        cursor: previousCursor,
        cursor_id: previousCursorID,
      });
      // sort / tf / reads_tf / wot / wot_depth / relays now travel as
      // X-Ptxt-* request headers (see fetchWithSession in session.js), so
      // they no longer need to be threaded through the URL. We only keep
      // route-specific bits (search query/scope, tag scope, etc).
      if (isSearch) {
        const searchQuery = button.dataset.searchQuery || "";
        if (searchQuery) params.set("q", searchQuery);
        const searchScope = button.dataset.searchScope || "network";
        params.set("scope", searchScope);
      } else if (isTag) {
        const tagScope = button.dataset.tagScope || "network";
        params.set("scope", tagScope);
      }
      addAsciiWidthHint(params, feedPathname);
      const response = await fetchWithTimeout(`${url}?${params.toString()}`, loadMoreRequestTimeoutMs);
      if (!response.ok) throw new Error(`Load more failed: ${response.status}`);
      const hasMoreHeader = response.headers.get("X-Ptxt-Has-More") || "";
      const cursorHeader = response.headers.get("X-Ptxt-Cursor") || "";
      const cursorIDHeader = response.headers.get("X-Ptxt-Cursor-Id") || "";
      const html = await response.text();
      button.dataset.hasMore = hasMoreHeader;
      const hasMore = responseHasMore(html, button);
      if (!hasMore && !html.trim()) {
        reachedEnd = true;
        setNoMore();
        return;
      }
      const appended = isReads ? appendReadArticles(feed, html) : appendNewNotes(feed, html);
      if (appended > 0 && !isReads) {
        void refreshVisibleFeedNoteMetadata(document, new URL(window.location.href));
      }
      const sortMode = feedSortForSession(normalizedPubkey(), getFeedSortPref()) || "recent";
      const last = cursorFromHeaders ? null : feed.querySelector(".note:last-of-type");
      if (cursorFromHeaders) {
        button.dataset.cursor = cursorHeader || button.dataset.cursor || "";
        button.dataset.cursorId = cursorIDHeader || button.dataset.cursorId || "";
      } else if (sortMode === "recent") {
        button.dataset.cursor = last?.dataset.createdAt || cursorHeader || button.dataset.cursor || "";
        button.dataset.cursorId = last?.id?.replace(/^note-/, "") || cursorIDHeader || button.dataset.cursorId || "";
      } else {
        button.dataset.cursor = cursorHeader || button.dataset.cursor || "";
        button.dataset.cursorId = cursorIDHeader || "";
      }
      const cursorAdvanced = button.dataset.cursor !== previousCursor || button.dataset.cursorId !== previousCursorID;
      if (!appended) {
        if (!hasMore) {
          reachedEnd = true;
          setNoMore();
          return;
        }
        // Keep paging available even when a page overlaps existing notes.
        button.textContent = defaultLabel;
        if (cursorAdvanced) {
          queueMicrotask(() => {
            void loadMore();
          });
        }
        return;
      }
      if (!hasMore) {
        reachedEnd = true;
        setNoMore();
        return;
      }
      button.textContent = defaultLabel;
    } catch (error) {
      button.textContent = error?.message || "Load failed";
      return;
    } finally {
      loading = false;
      if (!reachedEnd) {
        setLoadingState(false);
        button.disabled = false;
      }
    }
  };

  loadMoreHandlers.set(button, loadMore);

  button.addEventListener("click", () => {
    void loadMore();
  });

  tryConnectLoadMoreIntersection(feed, button, loadMore);
}

if (!initialized) {
  initialized = true;
  initFeedLoadMore(document);
  bindProfileStatLinks(document);
}

function responseHasMore(html, button) {
  if (!button) return false;
  if (button.dataset.hasMore === "1") return true;
  if (button.dataset.hasMore === "0") return false;
  return html.trim().length > 0;
}

async function fetchWithTimeout(url, timeoutMs) {
  const controller = new AbortController();
  const timer = window.setTimeout(() => controller.abort(), timeoutMs);
  try {
    return await fetchWithSession(url, { signal: controller.signal });
  } catch (error) {
    if (error?.name === "AbortError") {
      throw new Error("Load more timed out");
    }
    throw error;
  } finally {
    window.clearTimeout(timer);
  }
}

function topNoteCursor(root, feedRootSelector) {
  const first = root.querySelector(`${feedRootSelector} .note:first-of-type`);
  if (!first) {
    return { cursor: "", cursorID: "" };
  }
  return {
    cursor: first.dataset.createdAt || "",
    cursorID: first.id?.replace(/^note-/, "") || "",
  };
}

export function feedTopCursor(root = document) {
  return topNoteCursor(root, "[data-feed]");
}

export function profilePostsTopCursor(root = document) {
  return topNoteCursor(root, "#user-panel-posts [data-feed]");
}

export function prependNewNotes(feed, html) {
  const template = document.createElement("template");
  template.innerHTML = html;
  const notes = [...template.content.querySelectorAll(".note")];
  let prepended = 0;
  for (let index = notes.length - 1; index >= 0; index -= 1) {
    const note = notes[index];
    if (note.id && document.getElementById(note.id)) continue;
    feed.prepend(note);
    prepended += 1;
  }
  if (prepended > 0) {
    initViewMore(feed);
    void syncBookmarkState(document);
    wireAvatarImageFallbacks(feed);
  }
  return prepended;
}

function appendNewNotes(feed, html) {
  const template = document.createElement("template");
  template.innerHTML = html;
  let appended = 0;
  template.content.querySelectorAll(".note").forEach((note) => {
    if (note.id && document.getElementById(note.id)) return;
    feed.append(note);
    appended += 1;
  });
  if (appended > 0) {
    initViewMore(feed);
    void syncBookmarkState(document);
    wireAvatarImageFallbacks(feed);
  }
  return appended;
}
