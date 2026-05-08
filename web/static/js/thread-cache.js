import { applyRelayAndThreadSessionToURL } from "./session.js";
import {
  replaceRouteOutletAndScroll,
  routeOutletInnerHTML,
  routeScrollTop,
} from "./shell-swap.js";

const maxSnapshots = 6;
const threadSnapshots = new Map();

export function threadSnapshotKey(urlLike) {
  const url = new URL(urlLike, window.location.origin);
  applyRelayAndThreadSessionToURL(url);
  return `${url.pathname}?${url.searchParams.toString()}`;
}

export function snapshotThread(urlLike, mainNode) {
  if (!mainNode?.innerHTML) return;
  const key = threadSnapshotKey(urlLike);
  threadSnapshots.delete(key);
  threadSnapshots.set(key, {
    html: routeOutletInnerHTML(mainNode),
    scrollTop: routeScrollTop(mainNode),
  });
  while (threadSnapshots.size > maxSnapshots) {
    const oldest = threadSnapshots.keys().next().value;
    if (!oldest) break;
    threadSnapshots.delete(oldest);
  }
}

export function restoreThread(urlLike, mainNode) {
  const key = threadSnapshotKey(urlLike);
  const snapshot = threadSnapshots.get(key);
  if (!snapshot?.html || !mainNode) return null;
  replaceRouteOutletAndScroll(mainNode, snapshot.html, snapshot.scrollTop ?? 0);
  return snapshot;
}

export function clearThreadSnapshots() {
  threadSnapshots.clear();
}
