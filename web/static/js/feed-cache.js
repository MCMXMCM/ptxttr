import { feedTopCursor } from "./feed.js";
import {
  feedSnapshotKey,
  lookupFeedSnapshot,
  purgeStaleFeedSnapshotKeys,
} from "./feed-snapshot-keys.js";
import { restoreRouteSnapshot, writeRouteSnapshot } from "./route-snapshot.js";

const maxSnapshots = 4;
const feedSnapshots = new Map();

export function snapshotFeed(urlLike, mainNode) {
  if (!mainNode?.innerHTML) return;
  if (mainNode.querySelector("[data-feed] [data-feed-loader]")) {
    return;
  }
  const top = feedTopCursor(mainNode);
  writeRouteSnapshot({
    map: feedSnapshots,
    urlLike,
    mainNode,
    maxSnapshots,
    toCanonicalKey: feedSnapshotKey,
    purgeStale: purgeStaleFeedSnapshotKeys,
    extraFields: { topCursor: top.cursor, topCursorID: top.cursorID },
  });
}

export function restoreFeed(urlLike, mainNode) {
  const snapshot = restoreRouteSnapshot({
    map: feedSnapshots,
    urlLike,
    mainNode,
    lookup: lookupFeedSnapshot,
    isInvalidSnapshot: (s) => s.html.includes("data-feed-loader"),
  });
  if (snapshot && mainNode?.querySelector("[data-feed] [data-feed-loader]")) {
    const key = feedSnapshotKey(urlLike);
    if (key) feedSnapshots.delete(key);
    return null;
  }
  return snapshot;
}

export function clearFeedSnapshots() {
  feedSnapshots.clear();
}
