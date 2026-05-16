import { canonicalHomeFeedURL } from "./viewer-pref-url.js";
import { lookupSnapshot, purgeStaleSnapshotKeys } from "./snapshot-keys.js";

export function feedSnapshotKey(urlLike) {
  const url = canonicalHomeFeedURL(urlLike);
  return url ? `${url.pathname}?${url.searchParams.toString()}` : "";
}

export function legacyFeedSnapshotKey(canonicalKey) {
  if (!canonicalKey.startsWith("/?")) return "";
  return `/feed?${canonicalKey.slice(2)}`;
}

export function lookupFeedSnapshot(map, urlLike) {
  return lookupSnapshot(map, urlLike, feedSnapshotKey, legacyFeedSnapshotKey);
}

export function purgeStaleFeedSnapshotKeys(map, canonicalKey) {
  purgeStaleSnapshotKeys(map, canonicalKey, feedSnapshotKey, legacyFeedSnapshotKey);
}
