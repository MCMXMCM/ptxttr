import { stripViewerPrefSearchParams } from "./viewer-pref-url.js";
import { lookupSnapshot, purgeStaleSnapshotKeys } from "./snapshot-keys.js";

export function profileSnapshotKey(urlLike) {
  const url = new URL(urlLike, window.location.origin);
  if (!url.pathname.startsWith("/u/")) {
    return "";
  }
  url.searchParams.delete("cursor");
  url.searchParams.delete("cursor_id");
  stripViewerPrefSearchParams(url);
  return `${url.pathname}?${url.searchParams.toString()}`;
}

export function lookupProfileSnapshot(map, urlLike) {
  return lookupSnapshot(map, urlLike, profileSnapshotKey);
}

export function purgeStaleProfileSnapshotKeys(map, canonicalKey) {
  purgeStaleSnapshotKeys(map, canonicalKey, profileSnapshotKey);
}
