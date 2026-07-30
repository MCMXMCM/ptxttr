import { searchNotes } from "../feed-service.js";
import { latestReplaceable, searchLocalProfiles } from "../event-store.js";
import { KIND_PROFILE } from "../nostr-kinds.js";
import { parseProfile, profileAPIEntry } from "../profile-parse.js";
import { runtimePreferenceKey } from "../client-store.js";
import { routeKind } from "../nav-routing.js";
import { normalizedPubkey } from "../session.js";

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

export async function loadSearch(urlLike, context = {}) {
  const url = normalizedURL(urlLike);
  const query = String(url.searchParams.get("q") || "").trim();
  const mode = String(url.searchParams.get("mode") || "").trim().toLowerCase() === "users" ? "users" : "notes";
  const scope = String(url.searchParams.get("scope") || "network").trim().toLowerCase() === "all" ? "all" : "network";
  const limit = pageLimit(url, DEFAULT_PAGE_LIMIT);
  const { until, untilID } = untilCursor(url);
  if (mode === "users") {
    const pubkeys = query ? await searchLocalProfiles(query, { limit: Math.min(limit, 20) }).catch(() => []) : [];
    const profiles = {};
    for (const pubkey of pubkeys) {
      const event = await latestReplaceable(pubkey, KIND_PROFILE).catch(() => null);
      profiles[pubkey] = profileAPIEntry(parseProfile(pubkey, event));
    }
    return {
      ...routeMeta(url),
      query,
      mode,
      scope,
      pubkeys,
      profiles,
    };
  }
  return {
    ...routeMeta(url),
    query,
    mode,
    scope,
    notes: query
      ? await searchNotes(query, {
        limit,
        viewerPubkey: String(context.viewerPubkey || normalizedPubkey() || "").trim().toLowerCase(),
        scope,
        until,
        untilID,
      }).catch(() => [])
      : [],
  };
}
