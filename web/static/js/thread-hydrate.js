/** Helpers for validating SPA thread hydrate responses (parent + selected focus layout). */

export function threadPathNoteID(urlLike) {
  const url = new URL(urlLike, window.location.origin);
  const match = url.pathname.match(/^\/thread\/([^/]+)/);
  return match ? match[1].toLowerCase() : "";
}

/**
 * True when hydrate HTML has enough context to show a feed reply in focus mode
 * (parent above selected). Root selections and non-reply notes always pass.
 */
export function isThreadHydrateComplete(html, selectedNoteID) {
  if (!html || !selectedNoteID) return true;
  const expectsFocus =
    html.includes('data-thread-expects-focus="1"') || html.includes("thread-op-link");
  if (!expectsFocus) return true;
  if (!html.includes("thread-focus-parent") || !html.includes("thread-focus-selected")) {
    return false;
  }
  return html.includes(`id="note-${selectedNoteID}"`);
}

/** True when a hydrate fragment response is safe to cache or render. */
export function isHydrateBundleUsable(bundle, selectedNoteID) {
  if (!bundle?.body || bundle.navigate) return false;
  if (bundle.threadIncomplete === true) return false;
  return isThreadHydrateComplete(bundle.body, selectedNoteID);
}
