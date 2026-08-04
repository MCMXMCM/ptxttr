import { addAsciiWidthHint } from "./ascii-width-hint.js";
import {
  appendClientFeedPage,
  appendClientNotificationsPage,
  appendClientProfilePage,
  appendClientReadsPage,
  appendClientSearchPage,
  isRelayNativeFeed,
  isRelayNativeProfile,
  appendClientTagPage,
  isRelayNativeTag,
} from "./client-render.js";
import { wireAvatarImageFallbacks } from "./layout.js";
import { fetchWithSession, normalizedPubkey } from "./session.js";
import { refreshVisibleFeedNoteMetadata } from "./feed-metadata.js";
import { shouldUseClientProfilePagination } from "./feed-pagination.js";
import { bindProfileStatLinks } from "./profile-tabs.js";
import { initViewMore } from "./notes.js";
import { syncBookmarkState } from "./bookmarks.js";
import { feedSortForSession, getFeedSortPref } from "./sort-prefs.js";
import { refreshVisibleNoteProfiles } from "./note-profiles.js";
import { powerSaverActive } from "./power-mode.js";
import {
  hideInlineRetroLoader,
  initRetroLoaders,
  setRetroLoaderProgress,
  settleRetroLoader,
  showInlineRetroLoader,
} from "./retro-loader.js";

let initialized = false;
const loadMoreRequestTimeoutMs = 12000;
const boundLoadMoreButtons = new WeakSet();
const loadMoreIntersectionObservers = new WeakMap();
const loadMoreHandlers = new WeakMap();

function startButtonLoader(button, options) {
  const loader = showInlineRetroLoader(button, options);
  if (loader) {
    setRetroLoaderProgress(loader, {
      percent: 8,
      statusMessage: "starting request...",
    });
  }
  return loader;
}

function loadMoreContextIsLive(feed, button) {
  return Boolean(feed?.isConnected && button?.isConnected);
}

function scheduleLoadMoreRetry(feed, button, loadMoreFn) {
  window.setTimeout(() => {
    if (!loadMoreContextIsLive(feed, button)) return;
    void loadMoreFn();
  }, 0);
}

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
  if (powerSaverActive()) return;
  if (button.dataset.ptxtLoadMoreIo === "1") return;
  if (feed.querySelector("[data-feed-loader]")) return;
  const observer = new IntersectionObserver(
    (entries) => {
      if (entries.some((entry) => entry.isIntersecting)) void loadMoreFn();
    },
    {
      rootMargin: "250px 0px",
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

function initFeedLoadMoreButton(root, button) {
  initRetroLoaders(root);
  const feedPath = button.dataset.feedUrl || "/feed";
  const feedPathname = new URL(feedPath, window.location.origin).pathname;
  const isHomeFeed = feedPath === "/feed" || feedPathname === "/";
  const isReads = feedPath === "/reads";
  const isSearch = feedPath === "/search";
  const isTag = feedPathname.startsWith("/tag/");
  const isNotifications = feedPath === "/notifications";
  const isProfile = feedPathname.startsWith("/u/");
  const serverFragmentRoute = isHomeFeed || isReads || isSearch || isTag || isProfile;
  const cursorFromHeaders = isReads || isSearch || isTag || isNotifications || isProfile;
  const profilePanel = isProfile ? button.closest(".user-tab-panel") : null;
  const feed = isReads
    ? root.querySelector("[data-reads]")
    : profilePanel?.querySelector("[data-profile-feed]") || root.querySelector("[data-feed]");
  if (!feed) return;
  if (!isReads) void refreshVisibleNoteProfiles(feed);
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
    else if (isNotifications) doneLabel = "No more notifications";
    else if (isProfile && button.dataset.fragment === "replies") doneLabel = "No more replies";
    else if (isProfile) doneLabel = "No more posts";
    button.textContent = doneLabel;
    button.disabled = true;
    stopLoading();
  };

  const loadMore = async () => {
    if (loading) return;
    if (feed.querySelector("[data-feed-loader]")) return;
    loading = true;
    setLoadingState(true);
    const loader = startButtonLoader(button, {
      loaderType: "feed-page",
      title: isReads ? "loading reads" : isProfile ? "loading profile posts" : "loading notes",
      summary: isReads
        ? "pulling the next batch of reads."
        : isProfile
          ? "pulling older notes from this user's relays."
          : "pulling the next batch of notes.",
      statusMessages: ["starting request..."],
      completionMessage: isReads ? "reads loaded." : isProfile ? "profile posts loaded." : "notes loaded.",
      progressWidth: 24,
      statusWindow: 3,
    });
    let reachedEnd = false;
    try {
      if (!serverFragmentRoute && isHomeFeed && isRelayNativeFeed(root)) {
        if (loader) {
          setRetroLoaderProgress(loader, {
            percent: 18,
            statusMessage: "requesting relay page...",
          });
        }
        const result = await appendClientFeedPage(root);
        if (!loadMoreContextIsLive(feed, button)) return;
        const hasMore = result.hasMore;
        button.dataset.hasMore = hasMore ? "1" : "0";
        if (!result.appended) {
          if (!hasMore) {
            reachedEnd = true;
            setNoMore();
            await settleRetroLoader(loader, { completionMessage: "feed is caught up." });
            hideInlineRetroLoader(button, { keepTargetHidden: true });
            return;
          }
          button.textContent = defaultLabel;
          if (result.cursorAdvanced) {
            if (loader) {
              setRetroLoaderProgress(loader, {
                percent: 66,
                statusMessage: "advancing to the next cursor...",
              });
            }
            scheduleLoadMoreRetry(feed, button, loadMore);
          } else {
            hideInlineRetroLoader(button);
          }
          return;
        }
        if (!hasMore) {
          reachedEnd = true;
          setNoMore();
          await settleRetroLoader(loader, { completionMessage: "feed is caught up." });
          hideInlineRetroLoader(button, { keepTargetHidden: true });
          return;
        }
        if (loader) {
          setRetroLoaderProgress(loader, {
            percent: 86,
            statusMessage: "rendering page...",
          });
        }
        await settleRetroLoader(loader);
        hideInlineRetroLoader(button);
        button.textContent = defaultLabel;
        return;
      }

      if (!serverFragmentRoute && isTag && isRelayNativeTag(root)) {
        if (loader) {
          setRetroLoaderProgress(loader, {
            percent: 18,
            statusMessage: "requesting tagged notes...",
          });
        }
        const result = await appendClientTagPage(root);
        if (!loadMoreContextIsLive(feed, button)) return;
        const hasMore = result.hasMore;
        button.dataset.hasMore = hasMore ? "1" : "0";
        if (!result.appended) {
          if (!hasMore) {
            reachedEnd = true;
            setNoMore();
            await settleRetroLoader(loader, { completionMessage: "tag feed is caught up." });
            hideInlineRetroLoader(button, { keepTargetHidden: true });
            return;
          }
          button.textContent = defaultLabel;
          if (result.cursorAdvanced) {
            if (loader) {
              setRetroLoaderProgress(loader, {
                percent: 66,
                statusMessage: "advancing to the next cursor...",
              });
            }
            scheduleLoadMoreRetry(feed, button, loadMore);
          } else {
            hideInlineRetroLoader(button);
          }
          return;
        }
        if (!hasMore) {
          reachedEnd = true;
          setNoMore();
          await settleRetroLoader(loader, { completionMessage: "tag feed is caught up." });
          hideInlineRetroLoader(button, { keepTargetHidden: true });
          return;
        }
        if (loader) {
          setRetroLoaderProgress(loader, {
            percent: 86,
            statusMessage: "rendering notes...",
          });
        }
        await settleRetroLoader(loader);
        hideInlineRetroLoader(button);
        button.textContent = defaultLabel;
        return;
      }

      if (shouldUseClientProfilePagination({
        isProfileRoute: isProfile,
        relayNativeProfile: isRelayNativeProfile(root),
      })) {
        if (loader) {
          setRetroLoaderProgress(loader, {
            percent: 18,
            statusMessage: "requesting profile posts...",
          });
        }
        const result = await appendClientProfilePage(root);
        if (!loadMoreContextIsLive(feed, button)) return;
        const hasMore = result.hasMore;
        button.dataset.hasMore = hasMore ? "1" : "0";
        if (!result.appended) {
          if (!hasMore) {
            reachedEnd = true;
            setNoMore();
            await settleRetroLoader(loader, { completionMessage: "profile is caught up." });
            hideInlineRetroLoader(button);
            return;
          }
          button.textContent = defaultLabel;
          if (result.cursorAdvanced) {
            if (loader) {
              setRetroLoaderProgress(loader, {
                percent: 66,
                statusMessage: "advancing to the next cursor...",
              });
            }
            scheduleLoadMoreRetry(feed, button, loadMore);
          } else {
            hideInlineRetroLoader(button);
          }
          return;
        }
        if (!hasMore) {
          reachedEnd = true;
          setNoMore();
          await settleRetroLoader(loader, { completionMessage: "profile is caught up." });
          hideInlineRetroLoader(button);
          return;
        }
        if (loader) {
          setRetroLoaderProgress(loader, {
            percent: 86,
            statusMessage: "rendering posts...",
          });
        }
        await settleRetroLoader(loader);
        hideInlineRetroLoader(button);
        button.textContent = defaultLabel;
        return;
      }

      if (isNotifications && directRelayNativeNotifications(feed)) {
        if (loader) {
          setRetroLoaderProgress(loader, {
            percent: 18,
            statusMessage: "requesting notifications...",
          });
        }
        const result = await appendClientNotificationsPage(root);
        if (!loadMoreContextIsLive(feed, button)) return;
        const hasMore = result.hasMore;
        button.dataset.hasMore = hasMore ? "1" : "0";
        if (!result.appended) {
          if (!hasMore) {
            reachedEnd = true;
            setNoMore();
            await settleRetroLoader(loader, { completionMessage: "notifications are caught up." });
            hideInlineRetroLoader(button, { keepTargetHidden: true });
            return;
          }
          button.textContent = defaultLabel;
          if (result.cursorAdvanced) {
            if (loader) {
              setRetroLoaderProgress(loader, {
                percent: 66,
                statusMessage: "advancing to the next cursor...",
              });
            }
            scheduleLoadMoreRetry(feed, button, loadMore);
          } else {
            hideInlineRetroLoader(button);
          }
          return;
        }
        if (!hasMore) {
          reachedEnd = true;
          setNoMore();
          await settleRetroLoader(loader, { completionMessage: "notifications are caught up." });
          hideInlineRetroLoader(button, { keepTargetHidden: true });
          return;
        }
        if (loader) {
          setRetroLoaderProgress(loader, {
            percent: 86,
            statusMessage: "rendering notifications...",
          });
        }
        await settleRetroLoader(loader);
        hideInlineRetroLoader(button);
        button.textContent = defaultLabel;
        return;
      }

      if (!serverFragmentRoute && isReads) {
        if (loader) {
          setRetroLoaderProgress(loader, {
            percent: 18,
            statusMessage: "requesting reads...",
          });
        }
        const result = await appendClientReadsPage(root);
        if (!loadMoreContextIsLive(feed, button)) return;
        const hasMore = result.hasMore;
        button.dataset.hasMore = hasMore ? "1" : "0";
        if (!result.appended) {
          if (!hasMore) {
            reachedEnd = true;
            setNoMore();
            await settleRetroLoader(loader, { completionMessage: "reads are caught up." });
            hideInlineRetroLoader(button, { keepTargetHidden: true });
            return;
          }
          button.textContent = defaultLabel;
          if (result.cursorAdvanced) {
            if (loader) {
              setRetroLoaderProgress(loader, {
                percent: 66,
                statusMessage: "advancing to the next cursor...",
              });
            }
            scheduleLoadMoreRetry(feed, button, loadMore);
          } else {
            hideInlineRetroLoader(button);
          }
          return;
        }
        if (!hasMore) {
          reachedEnd = true;
          setNoMore();
          await settleRetroLoader(loader, { completionMessage: "reads are caught up." });
          hideInlineRetroLoader(button, { keepTargetHidden: true });
          return;
        }
        if (loader) {
          setRetroLoaderProgress(loader, {
            percent: 86,
            statusMessage: "rendering reads...",
          });
        }
        await settleRetroLoader(loader);
        hideInlineRetroLoader(button);
        button.textContent = defaultLabel;
        return;
      }

      if (!serverFragmentRoute && isSearch) {
        if (loader) {
          setRetroLoaderProgress(loader, {
            percent: 18,
            statusMessage: "requesting search results...",
          });
        }
        const result = await appendClientSearchPage(root);
        if (!loadMoreContextIsLive(feed, button)) return;
        const hasMore = result.hasMore;
        button.dataset.hasMore = hasMore ? "1" : "0";
        if (!result.appended) {
          if (!hasMore) {
            reachedEnd = true;
            setNoMore();
            await settleRetroLoader(loader, { completionMessage: "search results are caught up." });
            hideInlineRetroLoader(button, { keepTargetHidden: true });
            return;
          }
          button.textContent = defaultLabel;
          if (result.cursorAdvanced) {
            if (loader) {
              setRetroLoaderProgress(loader, {
                percent: 66,
                statusMessage: "advancing to the next cursor...",
              });
            }
            scheduleLoadMoreRetry(feed, button, loadMore);
          } else {
            hideInlineRetroLoader(button);
          }
          return;
        }
        if (!hasMore) {
          reachedEnd = true;
          setNoMore();
          await settleRetroLoader(loader, { completionMessage: "search results are caught up." });
          hideInlineRetroLoader(button, { keepTargetHidden: true });
          return;
        }
        if (loader) {
          setRetroLoaderProgress(loader, {
            percent: 86,
            statusMessage: "rendering results...",
          });
        }
        await settleRetroLoader(loader);
        hideInlineRetroLoader(button);
        button.textContent = defaultLabel;
        return;
      }

      if (loader) {
        setRetroLoaderProgress(loader, {
          percent: 18,
          statusMessage: "requesting next page...",
        });
      }
      const url = feedPath;
      const previousCursor = button.dataset.cursor || "";
      const previousCursorID = button.dataset.cursorId || "";
      const params = new URLSearchParams({
        cursor: previousCursor,
        cursor_id: previousCursorID,
      });
      const fragment = button.dataset.fragment || "1";
      if (fragment) params.set("fragment", fragment);
      // sort / tf / reads_tf / wot / wot_depth / relays now travel as
      // X-Ptxt-* request headers (see fetchWithSession in session.js), so
      // they no longer need to be threaded through the URL. We only keep
      // route-specific bits (search query/scope, tag scope, etc).
      if (isSearch) {
        const searchQuery = button.dataset.searchQuery || "";
        if (searchQuery) params.set("q", searchQuery);
        const searchScope = button.dataset.searchScope || "network";
        params.set("scope", searchScope);
        const searchMode = button.dataset.searchMode || "";
        if (searchMode) params.set("mode", searchMode);
      } else if (isTag) {
        const tagScope = button.dataset.tagScope || "network";
        params.set("scope", tagScope);
      }
      addAsciiWidthHint(params, feedPathname);
      const response = await fetchWithTimeout(`${url}?${params.toString()}`, loadMoreRequestTimeoutMs);
      if (!response.ok) throw new Error(`Load more failed: ${response.status}`);
      if (loader) {
        setRetroLoaderProgress(loader, {
          percent: 54,
          statusMessage: "response received...",
        });
      }
      const hasMoreHeader = response.headers.get("X-Ptxt-Has-More") || "";
      const cursorHeader = response.headers.get("X-Ptxt-Cursor") || "";
      const cursorIDHeader = response.headers.get("X-Ptxt-Cursor-Id") || "";
      const html = await response.text();
      if (!loadMoreContextIsLive(feed, button)) return;
      if (loader) {
        setRetroLoaderProgress(loader, {
          percent: 72,
          statusMessage: "processing notes...",
        });
      }
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
      if (loader) {
        setRetroLoaderProgress(loader, {
          percent: 88,
          statusMessage: "updating feed...",
        });
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
          await settleRetroLoader(loader, {
            completionMessage: isReads ? "reads are caught up." : "feed is caught up.",
          });
          hideInlineRetroLoader(button, { keepTargetHidden: true });
          return;
        }
        // Keep paging available even when a page overlaps existing notes.
        button.textContent = defaultLabel;
        if (cursorAdvanced) {
          if (loader) {
            setRetroLoaderProgress(loader, {
              percent: 66,
              statusMessage: "advancing to the next cursor...",
            });
          }
          scheduleLoadMoreRetry(feed, button, loadMore);
        } else {
          hideInlineRetroLoader(button);
        }
        return;
      }
      if (!hasMore) {
        reachedEnd = true;
        setNoMore();
        await settleRetroLoader(loader, {
          completionMessage: isReads ? "reads are caught up." : "feed is caught up.",
        });
        hideInlineRetroLoader(button, { keepTargetHidden: true });
        return;
      }
      await settleRetroLoader(loader, {
        completionMessage: isReads ? "reads loaded." : "notes loaded.",
      });
      hideInlineRetroLoader(button);
      button.textContent = defaultLabel;
    } catch (error) {
      hideInlineRetroLoader(button);
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

export function initFeedLoadMore(root = document) {
  initRetroLoaders(root);
  root.querySelectorAll("[data-load-more]").forEach((button) => {
    initFeedLoadMoreButton(root, button);
  });
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
  const insertedNotes = [];
  let prepended = 0;
  for (let index = notes.length - 1; index >= 0; index -= 1) {
    const note = notes[index];
    if (note.id && document.getElementById(note.id)) continue;
    feed.prepend(note);
    insertedNotes.push(note);
    prepended += 1;
  }
  if (prepended > 0) {
    initViewMore(feed);
    void syncBookmarkState(document);
    wireAvatarImageFallbacks(feed);
    void refreshVisibleNoteProfiles(insertedNotes);
  }
  return prepended;
}

function appendNewNotes(feed, html) {
  const template = document.createElement("template");
  template.innerHTML = html;
  const insertedNotes = [];
  let appended = 0;
  template.content.querySelectorAll(".note").forEach((note) => {
    if (note.id && document.getElementById(note.id)) return;
    feed.append(note);
    insertedNotes.push(note);
    appended += 1;
  });
  if (appended > 0) {
    initViewMore(feed);
    void syncBookmarkState(document);
    wireAvatarImageFallbacks(feed);
    void refreshVisibleNoteProfiles(insertedNotes);
  }
  return appended;
}

function directRelayNativeNotifications(feed) {
  return feed?.dataset?.relayNativeNotifications === "1";
}
