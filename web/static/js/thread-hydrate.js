/** Helpers for validating thread hydrate responses (parent + selected focus layout). */

import { canonicalHex64, isCanonicalEventID, resolveEventID } from "./relay-utils.js";

export function threadPathNoteID(urlLike) {
  const url = new URL(urlLike, window.location.origin);
  const selectedQuery = canonicalHex64(url.searchParams.get("selected") || "");
  if (isCanonicalEventID(selectedQuery)) {
    return selectedQuery;
  }
  const hashMatch = url.hash.match(/^#note-([0-9a-fA-F]{64})$/);
  if (hashMatch) {
    return canonicalHex64(hashMatch[1]);
  }
  const match = url.pathname.match(/^\/thread\/([^/]+)/);
  if (!match) return "";
  const resolved = resolveEventID(match[1]);
  if (resolved?.eventID) return resolved.eventID;
  const hex = canonicalHex64(match[1]);
  return isCanonicalEventID(hex) ? hex : match[1].toLowerCase();
}

export function threadServerHydrateHref(urlLike) {
  const url = new URL(urlLike, window.location.origin);
  const hashSelected = url.hash.match(/^#note-([0-9a-f]{64})$/i)?.[1]?.toLowerCase() || "";
  if (hashSelected && !url.searchParams.get("selected")) {
    url.searchParams.set("selected", hashSelected);
  }
  url.searchParams.set("fragment", "hydrate");
  url.searchParams.delete("cursor");
  url.searchParams.delete("cursor_id");
  return `${url.pathname}${url.search}${url.hash}`;
}

/**
 * True when hydrate HTML has enough context to show a feed reply in focus mode
 * (parent above selected). Root selections and non-reply notes always pass.
 */
export function isThreadHydrateComplete(html, selectedNoteID) {
  if (!selectedNoteID) return true;
  if (!html) return false;
  if (html.includes('data-relay-native-thread="1"')) {
    return (
      html.includes(`id="note-${selectedNoteID}"`) ||
      html.includes(`data-thread-selected-id="${selectedNoteID}"`)
    );
  }
  const expectsFocus =
    html.includes('data-thread-expects-focus="1"') ||
    html.includes("thread-header-op-depth") ||
    html.includes("thread-op-link");
  if (!expectsFocus) {
    return html.includes(`id="note-${selectedNoteID}"`);
  }
  if (html.includes("thread-focus-parent--skeleton")) {
    return false;
  }
  if (!html.includes("thread-focus-parent") || !html.includes("thread-focus-selected")) {
    return false;
  }
  return html.includes(`id="note-${selectedNoteID}"`);
}

/**
 * True when hydrate HTML is safe to paint for the selected thread route.
 * This is intentionally looser than isThreadHydrateComplete: a response can be
 * paintable while still incomplete for cache/finality purposes, such as when
 * focused reply context is still being filled.
 */
export function isThreadHydrateRenderable(html, selectedNoteID) {
  if (!selectedNoteID) return Boolean(String(html || "").trim());
  if (!html) return false;
  if (html.includes('data-relay-native-thread="1"')) {
    return (
      html.includes(`id="note-${selectedNoteID}"`) ||
      html.includes(`data-thread-selected-id="${selectedNoteID}"`)
    );
  }
  const expectsFocus =
    html.includes('data-thread-expects-focus="1"') ||
    html.includes("thread-header-op-depth") ||
    html.includes("thread-op-link");
  if (expectsFocus) {
    return (
      html.includes("thread-focus-selected") &&
      html.includes(`id="note-${selectedNoteID}"`)
    );
  }
  return html.includes(`id="note-${selectedNoteID}"`);
}

/** True when the hydrate response is not ready to paint into the thread shell. */
export function isThreadHydrateResponseIncomplete(_response, html, selectedNoteID) {
  if (!String(html || "").trim()) return true;
  // X-Ptxt-Thread-Incomplete can mean "do not cache yet" while still carrying
  // a renderable focus/root layout. Only block painting when the HTML itself
  // does not contain enough selected-thread context.
  return Boolean(selectedNoteID && !isThreadHydrateRenderable(html, selectedNoteID));
}

/**
 * Feed cards can advertise replies before those reply events have reached the
 * thread cache. Treat a root-only hydrate as provisional so document
 * navigation retries while the background warmer catches up.
 */
export function threadHydrateSatisfiesExpectedReplies(html, expectedReplyCount = 0) {
  if (Number(expectedReplyCount) <= 0) return true;
  const body = String(html || "");
  const repliesStart = body.indexOf('id="thread-replies"');
  if (repliesStart < 0) return false;
  const repliesMarkup = body.slice(repliesStart);
  return repliesMarkup.includes('id="note-') || repliesMarkup.includes("data-thread-filtered-replies-toggle");
}

/** True when thread focus still shows transition/preview placeholders instead of real content. */
export function threadFocusNeedsFullHydrate(root = document) {
  if (!root?.querySelector) return false;
  const focus = root.querySelector("#thread-focus");
  if (!focus?.querySelector) return false;
  return Boolean(
    focus.querySelector(
      ".thread-focus-parent--skeleton, .ptxt-carried-thread-note, .thread-focus-skeleton",
    ),
  );
}

/** True when hydrate fragment response is safe to cache or render. */
export function isHydrateBundleUsable(bundle, selectedNoteID) {
  if (!bundle?.body || bundle.navigate) return false;
  if (bundle.threadIncomplete === true) return false;
  return isThreadHydrateComplete(bundle.body, selectedNoteID);
}

/**
 * Whether a server hydrate response is safe to paint into the thread shell.
 * Incomplete hydrate bundles are still renderable when they contain a valid
 * focused/root layout; they just must not be treated as cache-complete.
 */
export function shouldRenderThreadHydrateBundle(bundle, selectedNoteID) {
  if (isHydrateBundleUsable(bundle, selectedNoteID)) return true;
  if (!bundle?.body || bundle.navigate) return false;
  return isThreadHydrateRenderable(bundle.body, selectedNoteID);
}

/** Same-document focus change between two /thread/... URLs. */
export function isStayingInThreadRoute(currentRoute, route) {
  return route === "thread" && currentRoute === "thread";
}

function threadPreviewColumn(root) {
  return root?.querySelector?.(".feed-column[data-thread-route-pending]")
    || root?.querySelector?.(".feed-column[data-thread-selected-id], .feed-column[data-thread-root-id]")
    || root?.querySelector?.(".feed-column");
}

export function canApplyThreadPreview(root, urlLike = "") {
  if (!root) return false;
  const selectedID = threadPathNoteID(urlLike || window.location.href);
  if (!selectedID) return false;
  const activeSelectedID = threadPathNoteID(window.location.href);
  if (!activeSelectedID || activeSelectedID.toLowerCase() !== selectedID.toLowerCase()) return false;
  const column = threadPreviewColumn(root);
  const focus = root.querySelector("#thread-focus");
  if (focus?.querySelector?.(".thread-focus-skeleton")) return true;
  if (threadFocusNeedsFullHydrate(root)) return true;
  const focusHTML = focus?.outerHTML || "";
  if (
    column?.dataset?.relayNativeThread === "1" &&
    column?.dataset?.threadSelectedId === selectedID &&
    !threadFocusNeedsFullHydrate(root)
  ) {
    return false;
  }
  return !isThreadHydrateComplete(focusHTML, selectedID);
}

/** Lowercase hex OP id from the loaded thread column, or "" if unavailable. */
export function threadColumnRootID(root = document) {
  const raw = root.querySelector(".feed-column[data-thread-root-id]")?.dataset?.threadRootId || "";
  return raw.toLowerCase();
}
