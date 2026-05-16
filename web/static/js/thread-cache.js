import { applyRelayParamsToURL } from "./session.js";
import {
  replaceRouteOutletAndScroll,
  routeOutletInnerHTML,
  routeScrollTop,
} from "./shell-swap.js";
import { isThreadHydrateComplete, threadPathNoteID } from "./thread-hydrate.js";

const maxSnapshots = 6;
const threadSnapshots = new Map();

export function threadSnapshotKey(urlLike) {
  const url = new URL(urlLike, window.location.origin);
  applyRelayParamsToURL(url);
  return `${url.pathname}?${url.searchParams.toString()}`;
}

export function snapshotThread(urlLike, mainNode) {
  if (!mainNode?.innerHTML) return;
  const selectedID = threadPathNoteID(urlLike);
  const html = routeOutletInnerHTML(mainNode);
  if (!isThreadHydrateComplete(html, selectedID)) return;
  const key = threadSnapshotKey(urlLike);
  threadSnapshots.delete(key);
  threadSnapshots.set(key, {
    html,
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
  const selectedID = threadPathNoteID(urlLike);
  if (!isThreadHydrateComplete(snapshot.html, selectedID)) {
    threadSnapshots.delete(key);
    return null;
  }
  replaceRouteOutletAndScroll(mainNode, snapshot.html, snapshot.scrollTop ?? 0);
  return snapshot;
}

export function clearThreadSnapshots() {
  threadSnapshots.clear();
}
