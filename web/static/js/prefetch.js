/** Client-side fragment response cache shared with navigation (SPA route fetches). */
export const fragmentPrefetchCache = new Map();

export function clearFragmentPrefetch() {
  fragmentPrefetchCache.clear();
}

export function invalidatePrefetchForPathname(pathname) {
  if (!pathname || !fragmentPrefetchCache.size) return;
  for (const key of [...fragmentPrefetchCache.keys()]) {
    const base = key.split("::")[0];
    const q = base.indexOf("?");
    const pathOnly = q === -1 ? base : base.slice(0, q);
    if (pathOnly === pathname) {
      fragmentPrefetchCache.delete(key);
    }
  }
}

/** Drop cached thread fragments so the next hydrate cannot reuse pre-reaction HTML. */
export function invalidateThreadFragmentPrefetch() {
  if (!fragmentPrefetchCache.size) return;
  for (const key of [...fragmentPrefetchCache.keys()]) {
    const base = key.split("::")[0];
    const q = base.indexOf("?");
    const pathOnly = q === -1 ? base : base.slice(0, q);
    if (pathOnly.startsWith("/thread/")) {
      fragmentPrefetchCache.delete(key);
    }
  }
}
