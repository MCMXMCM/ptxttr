/** Client-side fragment response cache shared with navigation (SPA route fetches). */
export const fragmentPrefetchCache = new Map();

/** LRU cap for in-memory fragment promises (hover + viewport prefetch). */
export const maxFragmentPrefetchEntries = 48;

export function clearFragmentPrefetch() {
  fragmentPrefetchCache.clear();
}

/** Insert or refresh a cache entry and evict the oldest when over capacity. */
export function setFragmentPrefetchCache(key, value) {
  if (fragmentPrefetchCache.has(key)) {
    fragmentPrefetchCache.delete(key);
  }
  fragmentPrefetchCache.set(key, value);
  while (fragmentPrefetchCache.size > maxFragmentPrefetchEntries) {
    const oldest = fragmentPrefetchCache.keys().next().value;
    if (oldest === undefined) break;
    fragmentPrefetchCache.delete(oldest);
  }
}

export function pathFromFragmentCacheKey(key) {
  const base = key.split("::")[0];
  const q = base.indexOf("?");
  return q === -1 ? base : base.slice(0, q);
}

export function invalidatePrefetchForPathname(pathname) {
  if (!pathname || !fragmentPrefetchCache.size) return;
  for (const key of [...fragmentPrefetchCache.keys()]) {
    if (pathFromFragmentCacheKey(key) === pathname) {
      fragmentPrefetchCache.delete(key);
    }
  }
}

/** Drop cached thread fragments so the next hydrate cannot reuse pre-reaction HTML. */
export function invalidateThreadFragmentPrefetch() {
  if (!fragmentPrefetchCache.size) return;
  for (const key of [...fragmentPrefetchCache.keys()]) {
    if (pathFromFragmentCacheKey(key).startsWith("/thread/")) {
      fragmentPrefetchCache.delete(key);
    }
  }
}
