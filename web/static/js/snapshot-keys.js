/**
 * Shared snapshot map key lookup/purge. Route modules supply a canonical key fn
 * and optional legacy alias (home feed: `/feed?` ↔ `/?`).
 */

/** @returns {{ key: string, snapshot: object, canonicalKey: string } | null} */
export function lookupSnapshot(map, urlLike, toCanonicalKey, legacyKeyFromCanonical = null) {
  const canonicalKey = toCanonicalKey(urlLike);
  if (!canonicalKey) return null;

  const direct = map.get(canonicalKey);
  if (direct) return { key: canonicalKey, snapshot: direct, canonicalKey };

  const legacyKey = legacyKeyFromCanonical?.(canonicalKey) || "";
  if (legacyKey) {
    const legacy = map.get(legacyKey);
    if (legacy) return { key: legacyKey, snapshot: legacy, canonicalKey };
  }

  for (const storedKey of map.keys()) {
    if (storedKey === canonicalKey || storedKey === legacyKey) continue;
    if (toCanonicalKey(storedKey) !== canonicalKey) continue;
    const snapshot = map.get(storedKey);
    if (snapshot) return { key: storedKey, snapshot, canonicalKey };
  }
  return null;
}

export function purgeStaleSnapshotKeys(map, canonicalKey, toCanonicalKey, legacyKeyFromCanonical = null) {
  if (!canonicalKey) return;
  const legacyKey = legacyKeyFromCanonical?.(canonicalKey) || "";
  for (const storedKey of [...map.keys()]) {
    if (storedKey === canonicalKey) continue;
    if (storedKey === legacyKey || toCanonicalKey(storedKey) === canonicalKey) {
      map.delete(storedKey);
    }
  }
}
