import {
  lookupProfileSnapshot,
  profileSnapshotKey,
  purgeStaleProfileSnapshotKeys,
} from "./profile-snapshot-keys.js";
import { restoreRouteSnapshot, writeRouteSnapshot } from "./route-snapshot.js";

const maxSnapshots = 4;
const profileSnapshots = new Map();

export function snapshotProfile(urlLike, mainNode) {
  if (!mainNode?.innerHTML) return;
  const postsFeed = mainNode.querySelector("#user-panel-posts [data-feed]");
  if (!postsFeed || postsFeed.classList.contains("profile-feed-skeleton")) {
    return;
  }
  writeRouteSnapshot({
    map: profileSnapshots,
    urlLike,
    mainNode,
    maxSnapshots,
    toCanonicalKey: profileSnapshotKey,
    purgeStale: purgeStaleProfileSnapshotKeys,
  });
}

export function restoreProfile(urlLike, mainNode) {
  return restoreRouteSnapshot({
    map: profileSnapshots,
    urlLike,
    mainNode,
    lookup: lookupProfileSnapshot,
    isInvalidSnapshot: (snapshot) => snapshot.html.includes("profile-feed-skeleton"),
  });
}

export function clearProfileSnapshots() {
  profileSnapshots.clear();
}
