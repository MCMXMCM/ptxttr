import { feedTopCursor } from "./feed.js";
import {
  replaceRouteOutletAndScroll,
  routeOutletInnerHTML,
  routeScrollTop,
} from "./shell-swap.js";
import { applyRelayParamsToURL, isFeedLikePath } from "./session.js";

const maxSnapshots = 4;
const feedSnapshots = new Map();

export function feedSnapshotKey(urlLike) {
  const url = new URL(urlLike, window.location.origin);
  if (isFeedLikePath(url.pathname)) {
    applyRelayParamsToURL(url);
  }
  return `${url.pathname}?${url.searchParams.toString()}`;
}

export function snapshotFeed(urlLike, mainNode, root = document) {
  if (!mainNode?.innerHTML) return;
  if (mainNode.querySelector("[data-feed] [data-feed-loader]")) {
    return;
  }
  const key = feedSnapshotKey(urlLike);
  const top = feedTopCursor(root);
  feedSnapshots.delete(key);
  feedSnapshots.set(key, {
    html: routeOutletInnerHTML(mainNode),
    scrollTop: routeScrollTop(mainNode),
    topCursor: top.cursor,
    topCursorID: top.cursorID,
    savedAt: Date.now(),
  });
  while (feedSnapshots.size > maxSnapshots) {
    const oldest = feedSnapshots.keys().next().value;
    if (!oldest) break;
    feedSnapshots.delete(oldest);
  }
}

export function restoreFeed(urlLike, mainNode) {
  const key = feedSnapshotKey(urlLike);
  const snapshot = feedSnapshots.get(key) || null;
  if (!snapshot || !mainNode) return null;
  if (snapshot.html.includes("data-feed-loader")) {
    feedSnapshots.delete(key);
    return null;
  }
  replaceRouteOutletAndScroll(mainNode, snapshot.html, snapshot.scrollTop ?? snapshot.scrollY ?? 0);
  return snapshot;
}

export function clearFeedSnapshots() {
  feedSnapshots.clear();
}
