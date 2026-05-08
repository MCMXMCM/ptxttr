import { applyRelayAndThreadSessionToURL } from "./session.js";
import {
  replaceRouteOutletAndScroll,
  routeOutletInnerHTML,
  routeScrollTop,
} from "./shell-swap.js";

const maxSnapshots = 4;
const profileSnapshots = new Map();

export function profileSnapshotKey(urlLike) {
  const url = new URL(urlLike, window.location.origin);
  url.searchParams.delete("cursor");
  url.searchParams.delete("cursor_id");
  applyRelayAndThreadSessionToURL(url);
  return `${url.pathname}?${url.searchParams.toString()}`;
}

export function snapshotProfile(urlLike, mainNode) {
  if (!mainNode?.innerHTML) return;
  const postsFeed = mainNode.querySelector("#user-panel-posts [data-feed]");
  if (!postsFeed || postsFeed.classList.contains("profile-feed-skeleton")) {
    return;
  }
  const key = profileSnapshotKey(urlLike);
  profileSnapshots.delete(key);
  profileSnapshots.set(key, {
    html: routeOutletInnerHTML(mainNode),
    scrollTop: routeScrollTop(mainNode),
    savedAt: Date.now(),
  });
  while (profileSnapshots.size > maxSnapshots) {
    const oldest = profileSnapshots.keys().next().value;
    if (!oldest) break;
    profileSnapshots.delete(oldest);
  }
}

export function restoreProfile(urlLike, mainNode) {
  const key = profileSnapshotKey(urlLike);
  const snapshot = profileSnapshots.get(key) || null;
  if (!snapshot || !mainNode) return null;
  if (snapshot.html.includes("profile-feed-skeleton")) {
    profileSnapshots.delete(key);
    return null;
  }
  replaceRouteOutletAndScroll(mainNode, snapshot.html, snapshot.scrollTop ?? snapshot.scrollY ?? 0);
  return snapshot;
}

export function clearProfileSnapshots() {
  profileSnapshots.clear();
}
