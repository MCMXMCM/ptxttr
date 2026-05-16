/** Legacy viewer-pref keys that travel as X-Ptxt-* headers, not in the URL. */
export const VIEWER_PREF_QUERY_KEYS = [
  "pubkey",
  "seed_pubkey",
  "sort",
  "tf",
  "reads_tf",
  "wot",
  "wot_depth",
];

/** Strips stale relay query params (relays now use X-Ptxt-Relays). */
export function applyRelayParamsToURL(url) {
  if (!url?.searchParams) return;
  url.searchParams.delete("relays");
  url.searchParams.delete("relay");
}

/** Removes relay + legacy viewer-pref query keys from `url` in place. */
export function stripViewerPrefSearchParams(url) {
  if (!url?.searchParams) return;
  applyRelayParamsToURL(url);
  for (const k of VIEWER_PREF_QUERY_KEYS) {
    url.searchParams.delete(k);
  }
}

/** Canonical home feed URL object (`/` path, prefs stripped), or null if not home feed. */
export function canonicalHomeFeedURL(urlLike) {
  const url = new URL(urlLike, window.location.origin);
  if (url.pathname !== "/" && url.pathname !== "/feed") {
    return null;
  }
  stripViewerPrefSearchParams(url);
  url.pathname = "/";
  return url;
}
