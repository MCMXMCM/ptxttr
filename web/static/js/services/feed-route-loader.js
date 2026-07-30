import { fetchFeedNotes, cachedFeedNotes, feedPageCursor } from "../feed-service.js";
import { runtimePreferenceKey } from "../client-store.js";
import { routeKind } from "../nav-routing.js";
import { normalizedPubkey } from "../session.js";
import { getFeedSortPref, feedSortForSession } from "../sort-prefs.js";

const DEFAULT_PAGE_LIMIT = 30;

function normalizedURL(urlLike = globalThis.location?.href || "http://localhost/") {
  return urlLike instanceof URL ? urlLike : new URL(String(urlLike || "/"), globalThis.location?.origin || "http://localhost");
}

function pageLimit(url, fallback = DEFAULT_PAGE_LIMIT) {
  const value = Number(url.searchParams.get("limit")) || 0;
  return value > 0 ? Math.min(200, value) : fallback;
}

function untilCursor(url) {
  return {
    until: Number(url.searchParams.get("cursor")) || 0,
    untilID: String(url.searchParams.get("cursor_id") || "").trim().toLowerCase(),
  };
}

function routeMeta(url) {
  const normalized = normalizedURL(url);
  return {
    route: routeKind(normalized.pathname),
    url: normalized.toString(),
    path: `${normalized.pathname}${normalized.search}${normalized.hash}`,
    preferenceKey: runtimePreferenceKey(),
  };
}

export async function loadFeed(urlLike, context = {}) {
  const url = normalizedURL(urlLike);
  const viewerPubkey = String(context.viewerPubkey || normalizedPubkey() || "").trim().toLowerCase();
  const sort = feedSortForSession(viewerPubkey, url.searchParams.get("sort") || getFeedSortPref()) || "recent";
  const { until, untilID } = untilCursor(url);
  const limit = Number(context.limit) > 0 ? Math.min(200, Number(context.limit)) : pageLimit(url, DEFAULT_PAGE_LIMIT);
  const forceFetch = context.forceFetch === true;
  let notes = await cachedFeedNotes({ viewerPubkey, sort, limit, until, untilID }).catch(() => []);
  let source = "cache";
  if (forceFetch || !notes.length) {
    notes = await fetchFeedNotes({ viewerPubkey, sort, limit, until, untilID, forceFetch }).catch(() => []);
    source = "network";
  }
  return {
    ...routeMeta(url),
    viewerPubkey,
    sort,
    notes,
    cursor: feedPageCursor(notes),
    hasMore: notes.length >= limit,
    source,
    status: source === "cache" ? "stale" : notes.length ? "fresh" : "empty",
  };
}
