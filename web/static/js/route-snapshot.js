import {
  replaceRouteOutletAndScroll,
  routeOutletInnerHTML,
  routeScrollTop,
} from "./shell-swap.js";

export function trimSnapshotMap(map, maxSnapshots) {
  while (map.size > maxSnapshots) {
    const oldest = map.keys().next().value;
    if (!oldest) break;
    map.delete(oldest);
  }
}

/** @returns {object | null} */
export function restoreRouteSnapshot({
  map,
  urlLike,
  mainNode,
  lookup,
  isInvalidSnapshot,
}) {
  const hit = lookup(map, urlLike);
  if (!hit || !mainNode) return null;
  const { key: matchedKey, snapshot, canonicalKey } = hit;
  if (matchedKey !== canonicalKey) {
    map.delete(matchedKey);
    if (!map.has(canonicalKey)) {
      map.set(canonicalKey, snapshot);
    }
  }
  if (isInvalidSnapshot(snapshot)) {
    map.delete(canonicalKey);
    return null;
  }
  replaceRouteOutletAndScroll(mainNode, snapshot.html, snapshot.scrollTop ?? 0);
  return snapshot;
}

export function writeRouteSnapshot({
  map,
  urlLike,
  mainNode,
  maxSnapshots,
  toCanonicalKey,
  purgeStale,
  extraFields = {},
}) {
  const key = toCanonicalKey(urlLike);
  if (!key) return;
  purgeStale(map, key);
  map.set(key, {
    html: routeOutletInnerHTML(mainNode),
    scrollTop: routeScrollTop(mainNode),
    savedAt: Date.now(),
    ...extraFields,
  });
  trimSnapshotMap(map, maxSnapshots);
}
