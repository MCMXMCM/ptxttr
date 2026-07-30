import { displayName } from "./profile-parse.js";
import { normalizePubkey } from "./relay-utils.js";
import { isSafariWebKit } from "./browser-capabilities.js";
import { rootIDForEvent } from "./thread-tags.js";
import {
  mergeServerReplyCounts,
  rememberServerFeedMetadata,
} from "./server-feed-metadata.js";

const trendingSidebarInflight = new Map();
export const TRENDING_SIDEBAR_FEED_TIMEOUT_MS = 4_500;
export const TRENDING_SIDEBAR_METADATA_TIMEOUT_MS = 2_000;

export function softTimeout(promise, ms, fallback) {
  const timeout = Math.max(0, Number(ms) || 0);
  if (!timeout) return Promise.resolve(promise).catch(() => fallback);
  return new Promise((resolve) => {
    let settled = false;
    const timer = setTimeout(() => {
      if (settled) return;
      settled = true;
      resolve(fallback);
    }, timeout);
    Promise.resolve(promise).then(
      (value) => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        resolve(value);
      },
      () => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        resolve(fallback);
      },
    );
  });
}

export function trendingThreadHref(event) {
  const eventID = String(event?.id || "").trim().toLowerCase();
  if (!eventID) return "/thread/";
  const rootID = String(rootIDForEvent(event) || eventID).trim().toLowerCase();
  if (!rootID || rootID === eventID) return `/thread/${eventID}`;
  return `/thread/${rootID}?selected=${eventID}#note-${eventID}`;
}

function replyCountText(count) {
  const n = Number(count) || 0;
  if (n === 1) return "1 reply";
  return `${n} replies`;
}

function previewText(content, max = 120) {
  const text = String(content || "").replace(/\s+/g, " ").trim();
  if (text.length <= max) return text;
  return `${text.slice(0, max - 1)}…`;
}

/**
 * Render compact trending sidebar list (matches internal/templates/partials.html trending_list).
 */
export function renderTrendingList(events, profilesByPubkey = {}, replyCounts = {}) {
  const list = document.createElement("ol");
  list.className = "trending-list";
  if (!events?.length) {
    const empty = document.createElement("li");
    empty.className = "muted";
    empty.textContent = "No trending notes yet.";
    list.append(empty);
    return list;
  }

  events.forEach((event) => {
    const id = String(event.id || "");
    const pk = normalizePubkey(event.pubkey);
    const profile = profilesByPubkey[pk] || {};
    const li = document.createElement("li");
    const link = document.createElement("a");
    link.href = trendingThreadHref(event);
    link.dataset.relayAware = "";

    const strong = document.createElement("strong");
    strong.textContent = displayName(profile);

    const span = document.createElement("span");
    span.textContent = previewText(event.content);

    const em = document.createElement("em");
    em.textContent = `↳ ${replyCountText(replyCounts[id] || 0)}`;

    link.append(strong, span, em);
    li.append(link);
    list.append(li);
  });
  return list;
}

export async function hydrateTrendingSidebar(root = document, { sort, kindFilter = null, force = false } = {}) {
  const target = root.querySelector("[data-trending-target]");
  if (!target) return;
  const inflightKey = `${sort || ""}:${kindFilter || ""}`;
  if (!force && trendingSidebarInflight.has(inflightKey)) {
    return trendingSidebarInflight.get(inflightKey);
  }

  const work = (async () => {
    const { loadTrendingFeed, trendingSortFromTimeframe } = await import("./trending-service.js");
    const { getTrendingTimeframePref } = await import("./sort-prefs.js");
    const { fetchProfiles, fetchReplyCounts } = await import("./relay-reads.js");
    const { fetchWithSession, normalizedPubkey } = await import("./session.js");

    const feedSort = sort || trendingSortFromTimeframe(getTrendingTimeframePref());
    try {
      const viewerPubkey = normalizedPubkey();
      let serverLoaded = false;
      let serverReplyCounts = {};
      let serverProfiles = {};
      let events = await softTimeout((async () => {
        const url = new URL("/api/feed-notes", window.location.origin);
        url.searchParams.set("limit", "12");
        url.searchParams.set("sort", feedSort);
        const response = await fetchWithSession(url.pathname + url.search, {
          headers: { Accept: "application/json" },
        });
        if (!response.ok) return null;
        const payload = await response.json();
        rememberServerFeedMetadata(payload);
        serverReplyCounts = payload?.reply_counts && typeof payload.reply_counts === "object"
          ? payload.reply_counts
          : {};
        serverProfiles = payload?.profiles && typeof payload.profiles === "object"
          ? payload.profiles
          : {};
        serverLoaded = true;
        return Array.isArray(payload?.notes) ? payload.notes : null;
      })(), TRENDING_SIDEBAR_FEED_TIMEOUT_MS, null);
      if (Array.isArray(events) && kindFilter != null) {
        events = events.filter((event) => Number(event?.kind) === Number(kindFilter));
      }
      if (!serverLoaded || !Array.isArray(events) || events.length === 0) {
        events = await softTimeout(
          loadTrendingFeed({
            sort: feedSort,
            viewerPubkey,
            limit: 12,
            kindFilter,
          }),
          TRENDING_SIDEBAR_FEED_TIMEOUT_MS,
          events,
        );
      }
      if (!Array.isArray(events)) {
        throw new Error("trending sidebar timed out");
      }
      const pubkeys = [...new Set(events.map((event) => normalizePubkey(event.pubkey)).filter(Boolean))];
      const fallbackProfiles = Object.fromEntries(pubkeys.map((pk) => [pk, { pubkey: pk }]));
      const hasServerReplyCount = (event) => Object.hasOwn(serverReplyCounts, String(event?.id || "").toLowerCase());
      const [profiles, fetchedReplyCounts] = await Promise.all([
        Object.keys(serverProfiles).length
          ? serverProfiles
          : softTimeout(fetchProfiles(pubkeys), TRENDING_SIDEBAR_METADATA_TIMEOUT_MS, fallbackProfiles),
        events.every(hasServerReplyCount)
          ? serverReplyCounts
          : softTimeout(fetchReplyCounts(events.map((event) => event.id)), TRENDING_SIDEBAR_METADATA_TIMEOUT_MS, {}),
      ]);
      const replyCounts = mergeServerReplyCounts(
        events.map((event) => event.id),
        { ...serverReplyCounts, ...fetchedReplyCounts },
      );
      target.replaceChildren(renderTrendingList(events, profiles, replyCounts));
    } catch {
      target.replaceChildren();
      const empty = document.createElement("p");
      empty.className = "muted";
      empty.textContent = "Trending unavailable.";
      target.append(empty);
    }
  })();
  trendingSidebarInflight.set(inflightKey, work);
  try {
    if (isSafariWebKit()) {
      await new Promise((resolve) => setTimeout(resolve, 150));
    }
    return await work;
  } finally {
    trendingSidebarInflight.delete(inflightKey);
  }
}
