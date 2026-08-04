/** True when `event` is strictly newer than a feed top cursor (created_at, id). */
export function isNewerThanFeedCursor(event, sinceCreatedAt, sinceID) {
  const sinceAt = Number(sinceCreatedAt) || 0;
  if (!sinceAt) return false;
  const createdAt = Number(event?.created_at) || 0;
  if (createdAt > sinceAt) return true;
  if (createdAt < sinceAt) return false;
  const id = String(event?.id || "").toLowerCase();
  const topID = String(sinceID || "").toLowerCase();
  if (!topID) return false;
  return id > topID;
}

/** True when `event` sorts before the composite (created_at, id) feed cursor. */
export function isBeforeFeedCursor(event, beforeCreatedAt, beforeID) {
  if (beforeCreatedAt == null || beforeCreatedAt <= 0) return true;
  const createdAt = Number(event?.created_at) || 0;
  if (createdAt < beforeCreatedAt) return true;
  if (createdAt > beforeCreatedAt) return false;
  const id = String(event?.id || "").toLowerCase();
  const cursorID = String(beforeID || "").toLowerCase();
  if (!cursorID) return false;
  return id < cursorID;
}

/** Cursor for the next older feed page (mirrors server cursor/until semantics). */
export function feedPageCursor(events) {
  if (!events?.length) return { until: 0, cursorId: "" };
  const oldest = events[events.length - 1];
  const createdAt = Number(oldest.created_at) || 0;
  return {
    until: createdAt > 0 ? createdAt - 1 : 0,
    cursorId: String(oldest.id || ""),
  };
}

/**
 * Client relay pages can legitimately be thinner than the requested page size
 * when relays overlap poorly or WoT filtering removes many rows. Keep paging
 * open while a valid older cursor exists; an empty follow-up page will close it.
 */
export function feedPageMayHaveMore(events) {
  const cursor = feedPageCursor(events);
  return Boolean(events?.length && cursor.until > 0);
}

/** Relay-native profiles page through the local client even though hosted profiles use server fragments. */
export function shouldUseClientProfilePagination({ isProfileRoute = false, relayNativeProfile = false } = {}) {
  return Boolean(isProfileRoute && relayNativeProfile);
}

/** Every accumulated profile post remains visible as older pages are appended. */
export function profilePostsForRender(posts) {
  return Array.isArray(posts) ? posts : [];
}

/** Select only immutable events that are new to an already-rendered profile timeline. */
export function profilePageEventsToAppend(currentEvents, pageEvents) {
  const seen = new Set(
    (Array.isArray(currentEvents) ? currentEvents : [])
      .map((event) => String(event?.id || "").trim().toLowerCase())
      .filter(Boolean),
  );
  return (Array.isArray(pageEvents) ? pageEvents : []).filter((event) => {
    const id = String(event?.id || "").trim().toLowerCase();
    if (!id || seen.has(id)) return false;
    seen.add(id);
    return true;
  });
}

/** Restore a profile viewport after a full repaint while keeping the same note at the same offset. */
export function profileScrollTopAfterRender(snapshot, currentAnchorOffsetTop) {
  const savedScrollTop = Math.max(0, Number(snapshot?.scrollTop) || 0);
  const savedAnchorOffsetTop = Number(snapshot?.offsetTop);
  const nextAnchorOffsetTop = Number(currentAnchorOffsetTop);
  if (!Number.isFinite(savedAnchorOffsetTop) || !Number.isFinite(nextAnchorOffsetTop)) {
    return savedScrollTop;
  }
  return Math.max(0, savedScrollTop + nextAnchorOffsetTop - savedAnchorOffsetTop);
}

/** Home feed `Load more` stays hidden while pagination is unavailable or a feed refresh is pending. */
export function homeFeedLoadMoreHidden({
  hasMore = false,
  isPending = false,
  isLoading = false,
  hasLoader = false,
} = {}) {
  if (!hasMore) return true;
  if (isPending) return true;
  if (hasLoader) return true;
  return Boolean(isLoading);
}

/** Count refreshed feed events that are not already represented by visible note ids. */
export function countUnseenFeedEvents(events = [], visibleIds = []) {
  const visible = new Set(
    [...visibleIds].map((id) => String(id || "").trim().toLowerCase()).filter(Boolean),
  );
  return (events || []).filter((event) => {
    const id = String(event?.id || "").trim().toLowerCase();
    return Boolean(id) && !visible.has(id);
  }).length;
}

/** Convert load-more datasets (until = created_at - 1) to composite (created_at, id) cursor. */
export function feedPaginationCursorFromDatasets({ until, untilID } = {}) {
  const cursorUntil = Number(until) || 0;
  const cursorId = String(untilID || "").trim().toLowerCase();
  if (cursorId && cursorUntil > 0) {
    return { beforeCreatedAt: cursorUntil + 1, beforeID: cursorId };
  }
  if (cursorUntil > 0) {
    return { beforeCreatedAt: cursorUntil, beforeID: "" };
  }
  return { beforeCreatedAt: undefined, beforeID: undefined };
}
