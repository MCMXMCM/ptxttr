import { canonicalHex64 } from "./relay-utils.js";
import { sortEventsOldestFirst } from "./feed-query.js";
import { KIND_COMMENT, KIND_NOTE } from "./nostr-kinds.js";

function isRenderableThreadReplyKind(kind) {
  const normalized = Number(kind) || 0;
  return normalized === KIND_NOTE || normalized === KIND_COMMENT;
}

export function parentID(rootID, event) {
  const root = canonicalHex64(rootID);
  const tags = event?.tags || [];
  if (Number(event?.kind) === KIND_COMMENT) {
    let commentParent = "";
    for (const tag of tags) {
      if (!Array.isArray(tag) || tag.length < 2 || tag[0] !== "e") continue;
      commentParent = canonicalHex64(tag[1]);
    }
    return commentParent;
  }
  let reply = "";
  let rootTag = "";
  let hasMarkedETag = false;
  for (const tag of tags) {
    if (!Array.isArray(tag) || tag.length < 2 || tag[0] !== "e") continue;
    const marker = tag.length >= 4 ? String(tag[3] || "").trim().toLowerCase() : "";
    if (marker) hasMarkedETag = true;
    if (marker === "root") rootTag = canonicalHex64(tag[1]);
    if (marker === "reply") reply = canonicalHex64(tag[1]);
  }
  if (reply) return reply;
  if (rootTag && rootTag !== root) return rootTag;
  if (hasMarkedETag) return "";
  const eTags = tags.filter((tag) => Array.isArray(tag) && tag[0] === "e");
  if (eTags.length >= 2) return canonicalHex64(eTags[eTags.length - 1][1]);
  if (eTags.length === 1) {
    const only = canonicalHex64(eTags[0][1]);
    return only === root ? "" : only;
  }
  return "";
}

/** Resolved parent for tree/linear assembly; empty parse defaults to root (matches Go thread.buildSelected). */
export function effectiveThreadParentID(rootID, event, parentByID) {
  const root = canonicalHex64(rootID);
  const eventID = canonicalHex64(event?.id);
  const mapped = canonicalHex64(parentByID?.[eventID] ?? parentByID?.[event?.id]);
  if (mapped) return mapped;
  const parsed = canonicalHex64(parentID(root, event));
  return parsed || root;
}

export function rootIDForEvent(event) {
  if (Number(event?.kind) === KIND_COMMENT) {
    for (const tag of event?.tags || []) {
      if (!Array.isArray(tag) || tag.length < 2 || tag[0] !== "E") continue;
      const root = canonicalHex64(tag[1]);
      if (root) return root;
    }
  }
  let hasMarkedETag = false;
  for (const tag of event?.tags || []) {
    if (!Array.isArray(tag) || tag.length < 2 || tag[0] !== "e") continue;
    const marker = tag.length >= 4 ? String(tag[3] || "").trim().toLowerCase() : "";
    if (marker) hasMarkedETag = true;
    if (marker === "root") return canonicalHex64(tag[1]);
  }
  if (hasMarkedETag) return canonicalHex64(event?.id);
  const eTags = (event?.tags || []).filter((tag) => Array.isArray(tag) && tag[0] === "e");
  if (eTags.length) return canonicalHex64(eTags[0][1]);
  return canonicalHex64(event?.id);
}

/** Matches server threadSelectedExpectsFocusView (handlers.go). */
export function threadExpectsFocusView(rootEvent, selectedEvent) {
  if (!rootEvent?.id || !selectedEvent?.id) return false;
  const rootID = canonicalHex64(rootEvent.id);
  const selectedID = canonicalHex64(selectedEvent.id);
  if (rootID === selectedID) return false;
  for (const tag of selectedEvent.tags || []) {
    if (!Array.isArray(tag) || tag.length < 2 || tag[0] !== "e") continue;
    if (tag.length >= 4 && String(tag[3] || "").toLowerCase() === "reply") return true;
  }
  const explicitRoot = rootIDForEvent(selectedEvent);
  if (explicitRoot && explicitRoot !== selectedID) return true;
  const parsedParent = parentID(rootID, selectedEvent);
  if (parsedParent && parsedParent !== selectedID) return true;
  return false;
}

export function directReplyEvents(events, parentByID, rootID, parentEventID) {
  const root = canonicalHex64(rootID);
  const parent = canonicalHex64(parentEventID);
  return (events || []).filter((event) => {
    const eventID = canonicalHex64(event.id);
    if (!eventID || eventID === root) return false;
    if (!isRenderableThreadReplyKind(event.kind)) return false;
    return effectiveThreadParentID(root, event, parentByID) === parent;
  });
}

/** Paginate direct thread replies (created_at asc, id asc) matching server threadRepliesPage cursors. */
export function pageDirectReplies(replies, cursor, cursorID, limit = 25) {
  const sorted = sortEventsOldestFirst(replies);
  const cur = Number(cursor) || 0;
  const curId = String(cursorID || "");
  let startIdx = 0;
  if (cur > 0 || curId) {
    startIdx = sorted.findIndex((event) => {
      const createdAt = Number(event.created_at);
      if (createdAt > cur) return true;
      if (createdAt === cur && String(event.id) > curId) return true;
      return false;
    });
    if (startIdx < 0) startIdx = sorted.length;
  }
  const slice = sorted.slice(startIdx, startIdx + limit + 1);
  const hasMore = slice.length > limit;
  const items = hasMore ? slice.slice(0, limit) : slice;
  const last = items[items.length - 1];
  return {
    items,
    hasMore,
    nextCursor: last ? String(last.created_at) : "",
    nextCursorId: last?.id || "",
  };
}

export function focusParentEvent(rootEvent, selectedEvent, events, parentByID) {
  const rootID = canonicalHex64(rootEvent.id);
  const parent = effectiveThreadParentID(rootID, selectedEvent, parentByID);
  if (!parent || parent === rootID) return rootEvent;
  return (events || []).find((event) => canonicalHex64(event.id) === parent) || rootEvent;
}
