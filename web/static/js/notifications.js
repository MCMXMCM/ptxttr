import { refreshAscii } from "./ascii.js";
import { syncBookmarkState } from "./bookmarks.js";
import { eventsByTag, putEvents, recentTimelineEvents } from "./event-store.js";
import { initViewMore } from "./notes.js";
import { createNoteArticle } from "./note-event-render.js";
import { referencedEventIDs } from "./note-references.js";
import { fetchFeedNoteMetadataMaps } from "./feed-metadata.js";
import { relayFetch } from "./relay-pool.js";
import { readRelaysForViewer } from "./publish-plan.js";
import { shortNpubLabel } from "./profile-parse.js";
import { fetchEventsByIDs, fetchProfiles } from "./relay-reads.js";
import { KIND_NOTE, KIND_REACTION, KIND_REPOST, KIND_ZAP_RECEIPT } from "./nostr-kinds.js";
import {
  getEffectiveLoggedOutWebOfTrustSeed,
  getWebOfTrustDepthPref,
  getWebOfTrustEnabledPref,
  getWebOfTrustSeedPref,
} from "./sort-prefs.js";
import { normalizePubkey, canonicalHex64, dedupeEventsByID } from "./relay-utils.js";
import { normalizedPubkey } from "./session.js";
import { rootIDForEvent, parentID } from "./thread-tags.js";
import { expandWebOfTrust } from "./wot-service.js";
import { feedFetchModeFromPrefs } from "./feed-wot.js";
import { zapAmountSats, zapMessage, zapSenderPubkey, zapTargetNoteID } from "./zap-utils.js";
import { notificationsLoaderMarkup } from "./shell.js";
import { initRetroLoaders } from "./retro-loader.js";
import { fetchCachedQuery, queryKeys } from "./query-client.js";

export const NotificationCategory = Object.freeze({
  REPLY: "reply",
  LIKE: "like",
  REPOST: "repost",
  MENTION: "mention",
  ZAP: "zap",
});

const CATEGORY_ORDER = [
  NotificationCategory.REPLY,
  NotificationCategory.LIKE,
  NotificationCategory.REPOST,
  NotificationCategory.MENTION,
  NotificationCategory.ZAP,
];

const CATEGORY_LABELS = {
  [NotificationCategory.REPLY]: "Replies",
  [NotificationCategory.LIKE]: "Likes",
  [NotificationCategory.REPOST]: "Reposts",
  [NotificationCategory.MENTION]: "Mentions",
  [NotificationCategory.ZAP]: "Zaps",
};

const CATEGORY_ICONS = {
  [NotificationCategory.REPLY]: "↩",
  [NotificationCategory.LIKE]: "♥",
  [NotificationCategory.REPOST]: "↻",
  [NotificationCategory.MENTION]: "@",
  [NotificationCategory.ZAP]: "⚡",
};

const CATEGORY_EMPTY_MESSAGES = {
  [NotificationCategory.REPLY]: "No replies yet.",
  [NotificationCategory.LIKE]: "No likes yet.",
  [NotificationCategory.REPOST]: "No reposts yet.",
  [NotificationCategory.MENTION]: "No mentions yet.",
  [NotificationCategory.ZAP]: "No zaps yet.",
};

const AUTHORED_LIMIT = 100;
const REPLY_BATCH_SIZE = 20;
const REPLY_FETCH_LIMIT = 120;
const NOTIFICATION_PAGE_LIMIT = 40;
const NOTIFICATION_CANDIDATE_LIMIT = 160;

function newNotificationState(viewerPubkey = "") {
  return {
    viewerPubkey,
    requestGeneration: 0,
    loadedKey: "",
    isLoading: false,
    errorMessage: "",
    allItems: [],
    profiles: {},
    referencedByID: new Map(),
    replyCounts: {},
    reactionStats: {},
    zapTotals: {},
    filterCounts: notificationCounts([]),
    selectedFilter: "",
    webOfTrustFilterEnabled: true,
    webOfTrustReady: false,
    wotGloballyEnabled: false,
    trustedPubkeys: new Set(),
    pendingNewerItems: null,
    pendingNewItemCount: 0,
  };
}

let notificationsState = newNotificationState();
let notificationsRequestGeneration = 0;
let layoutModulePromise = null;
let noteProfilesModulePromise = null;

function notificationsRouteActive(root = document) {
  return window.location.pathname === "/notifications" &&
    Boolean(root.querySelector("[data-notifications-feed]") || root.querySelector("[data-feed]"));
}

function notificationsRequestIsCurrent(state, root = document) {
  return Boolean(
    state &&
    notificationsState === state &&
    notificationsState.requestGeneration === state.requestGeneration &&
    notificationsRouteActive(root),
  );
}

function notificationActionText(category) {
  switch (category) {
  case NotificationCategory.REPLY:
    return "replied to your note";
  case NotificationCategory.LIKE:
    return "liked your note";
  case NotificationCategory.REPOST:
    return "reposted your note";
  case NotificationCategory.MENTION:
    return "mentioned you";
  case NotificationCategory.ZAP:
    return "zapped your note";
  default:
    return "notified you";
  }
}

function wireNotificationAvatarFallbacks(root) {
  if (!root) return;
  if (!layoutModulePromise) {
    layoutModulePromise = import("./layout.js").catch(() => null);
  }
  void layoutModulePromise.then((module) => {
    module?.wireAvatarImageFallbacks?.(root);
  });
}

function refreshNotificationProfiles(root) {
  if (!root) return;
  if (!noteProfilesModulePromise) {
    noteProfilesModulePromise = import("./note-profiles.js").catch(() => null);
  }
  void noteProfilesModulePromise.then((module) => {
    module?.refreshVisibleNoteProfiles?.(root);
  });
}

function isNewer(left, right) {
  if (!left) return false;
  if (!right) return true;
  if (left.createdAt !== right.createdAt) return left.createdAt > right.createdAt;
  return String(left.id || "") > String(right.id || "");
}

function notificationItemSort(left, right) {
  if (left.createdAt !== right.createdAt) return right.createdAt - left.createdAt;
  return String(right.id || "").localeCompare(String(left.id || ""));
}

function mergeNotificationItems(items = []) {
  const byID = new Map();
  for (const item of items) {
    const id = String(item?.id || "");
    if (!id) continue;
    byID.set(id, item);
  }
  return [...byID.values()].sort(notificationItemSort);
}

export function notificationCounts(items) {
  const counts = Object.fromEntries(CATEGORY_ORDER.map((category) => [category, 0]));
  for (const item of items || []) {
    counts[item.category] = (counts[item.category] || 0) + 1;
  }
  return counts;
}

function notificationTagsViewer(event, viewerPubkey) {
  const viewer = normalizePubkey(viewerPubkey);
  if (!viewer) return false;
  for (const tag of event?.tags || []) {
    if (!Array.isArray(tag) || tag.length < 2 || tag[0] !== "p") continue;
    if (normalizePubkey(tag[1]) === viewer) return true;
  }
  return false;
}

function notificationReplyTarget(event) {
  if (Number(event?.kind) !== KIND_NOTE) return "";
  const rootID = rootIDForEvent(event);
  return canonicalHex64(parentID(rootID, event));
}

export function classifyNotificationEvent(
  event,
  viewerPubkey,
  referencedByID = new Map(),
  viewerOwnedEventIDs = new Set(),
) {
  if (referencedByID instanceof Set && !(viewerOwnedEventIDs instanceof Set && viewerOwnedEventIDs.size > 0)) {
    viewerOwnedEventIDs = referencedByID;
    referencedByID = new Map();
  }
  switch (Number(event?.kind)) {
  case KIND_ZAP_RECEIPT: {
    const target = zapTargetNoteID(event);
    return target && viewerOwnedEventIDs.has(target) ? NotificationCategory.ZAP : "";
  }
  case KIND_REACTION:
    return notificationTagsViewer(event, viewerPubkey) ? NotificationCategory.LIKE : "";
  case KIND_REPOST:
    return notificationTagsViewer(event, viewerPubkey) ? NotificationCategory.REPOST : "";
  case KIND_NOTE: {
    const parent = notificationReplyTarget(event);
    if (parent) {
      if (viewerOwnedEventIDs.has(parent)) return NotificationCategory.REPLY;
      const referenced = referencedByID instanceof Map
        ? referencedByID.get(parent)
        : referencedByID?.[parent];
      if (normalizePubkey(referenced?.pubkey) === normalizePubkey(viewerPubkey)) {
        return NotificationCategory.REPLY;
      }
    }
    if (notificationTagsViewer(event, viewerPubkey)) return NotificationCategory.MENTION;
    return "";
  }
  default:
    return "";
  }
}

function targetEventIDFor(event, category) {
  if (category === NotificationCategory.ZAP) return zapTargetNoteID(event);
  if (category === NotificationCategory.LIKE || category === NotificationCategory.REPOST) {
    for (const tag of event?.tags || []) {
      if (!Array.isArray(tag) || tag.length < 2 || tag[0] !== "e") continue;
      return canonicalHex64(tag[1]);
    }
  }
  return canonicalHex64(event?.id);
}

export function notificationItemsFromEvents(
  events,
  { viewerPubkey, referencedByID = new Map(), viewerOwnedEventIDs = new Set() } = {},
) {
  return (events || []).flatMap((event) => {
    const category = classifyNotificationEvent(event, viewerPubkey, referencedByID, viewerOwnedEventIDs);
    if (!category) return [];
    return [{
      id: canonicalHex64(event.id),
      createdAt: Number(event.created_at || 0),
      actorPubkey: category === NotificationCategory.ZAP
        ? (zapSenderPubkey(event) || normalizePubkey(event.pubkey))
        : normalizePubkey(event.pubkey),
      category,
      targetEventID: targetEventIDFor(event, category),
      event,
    }];
  }).sort(notificationItemSort);
}

function currentNotificationsLoadKey(viewerPubkey) {
  return [
    normalizePubkey(viewerPubkey),
    getWebOfTrustEnabledPref() ? "wot-on" : "wot-off",
    String(getWebOfTrustDepthPref()),
    getWebOfTrustSeedPref() || "",
    readRelaysForViewer().join("|"),
  ].join(":");
}

async function notificationContext(viewerPubkey) {
  const viewer = normalizePubkey(viewerPubkey);
  if (!viewer) return { authoredEvents: [], authoredIDs: new Set() };
  const authoredEvents = await recentTimelineEvents({
    authors: [viewer],
    kinds: [KIND_NOTE],
    limit: AUTHORED_LIMIT,
  });
  return {
    authoredEvents,
    authoredIDs: new Set(authoredEvents.map((event) => canonicalHex64(event.id)).filter(Boolean)),
  };
}

function directReplyToViewer(event, authoredIDs) {
  const parent = notificationReplyTarget(event);
  return Boolean(parent && authoredIDs.has(parent));
}

async function cachedMentionEvents(viewerPubkey, limit = NOTIFICATION_CANDIDATE_LIMIT) {
  const viewer = normalizePubkey(viewerPubkey);
  if (!viewer) return [];
  const rows = await Promise.all([
    eventsByTag("p", viewer, { kind: KIND_NOTE, limit }),
    eventsByTag("p", viewer, { kind: KIND_REPOST, limit }),
    eventsByTag("p", viewer, { kind: KIND_REACTION, limit }),
    eventsByTag("p", viewer, { kind: KIND_ZAP_RECEIPT, limit }),
  ]);
  return dedupeEventsByID(rows.flat());
}

async function cachedDirectReplyEvents(authoredIDs) {
  const ids = [...authoredIDs].slice(0, AUTHORED_LIMIT);
  if (!ids.length) return [];
  const rows = await Promise.all(
    ids.map((id) => eventsByTag("e", id, { kind: KIND_NOTE, limit: REPLY_FETCH_LIMIT })),
  );
  return dedupeEventsByID(rows.flat()).filter((event) => directReplyToViewer(event, authoredIDs));
}

async function fetchMentionEvents(viewerPubkey, limit = NOTIFICATION_CANDIDATE_LIMIT) {
  const viewer = normalizePubkey(viewerPubkey);
  if (!viewer) return [];
  const events = await relayFetch(readRelaysForViewer(), [{
    kinds: [KIND_NOTE, KIND_REPOST, KIND_REACTION, KIND_ZAP_RECEIPT],
    "#p": [viewer],
    limit,
  }]);
  await putEvents(events);
  return dedupeEventsByID(events);
}

async function fetchDirectReplyEvents(context, limit = REPLY_FETCH_LIMIT) {
  const ids = [...context.authoredIDs].slice(0, AUTHORED_LIMIT);
  if (!ids.length) return [];
  const relays = readRelaysForViewer();
  const filters = [];
  for (let index = 0; index < ids.length; index += REPLY_BATCH_SIZE) {
    filters.push({
      kinds: [KIND_NOTE],
      "#e": ids.slice(index, index + REPLY_BATCH_SIZE),
      limit,
    });
  }
  const events = await relayFetch(relays, filters);
  await putEvents(events);
  return dedupeEventsByID(events).filter((event) => directReplyToViewer(event, context.authoredIDs));
}

function collectNotificationReferenceIDs(events) {
  const out = [];
  const seen = new Set();
  const add = (value) => {
    const id = canonicalHex64(value);
    if (!id || seen.has(id)) return;
    seen.add(id);
    out.push(id);
  };
  for (const event of events || []) {
    for (const id of referencedEventIDs(event)) add(id);
    add(notificationReplyTarget(event));
    add(rootIDForEvent(event));
    add(zapTargetNoteID(event));
  }
  return out;
}

async function hydrateReferencedEvents(events) {
  const ids = collectNotificationReferenceIDs(events);
  if (!ids.length) return new Map();
  const loaded = await fetchEventsByIDs(ids);
  return new Map(loaded.map((event) => [canonicalHex64(event.id), event]));
}

function pageNotificationItems(items, { limit = NOTIFICATION_PAGE_LIMIT, beforeCreatedAt, beforeID } = {}) {
  const before = Number(beforeCreatedAt || 0);
  const cursorID = String(beforeID || "").toLowerCase();
  const filtered = (items || []).filter((item) => {
    if (!before) return true;
    if (item.createdAt < before) return true;
    if (item.createdAt > before) return false;
    return String(item.id || "").toLowerCase() < cursorID;
  });
  const pageItems = filtered.slice(0, limit);
  const last = pageItems[pageItems.length - 1];
  return {
    items: pageItems,
    hasMore: filtered.length > limit,
    nextCursor: last ? String(last.createdAt) : "",
    nextCursorId: last?.id || "",
  };
}

async function buildNotificationFeed({
  viewerPubkey,
  limit = NOTIFICATION_PAGE_LIMIT,
  beforeCreatedAt,
  beforeID,
  fetchNetwork = false,
} = {}) {
  const viewer = normalizePubkey(viewerPubkey);
  const relays = readRelaysForViewer();
  return fetchCachedQuery({
    queryKey: queryKeys.notifications({
      viewerPubkey: viewer,
      relays,
      limit,
      beforeCreatedAt,
      beforeID,
    }),
    cacheMode: fetchNetwork ? "refresh" : "cache-first",
    staleTime: fetchNetwork ? 0 : 15_000,
    queryFn: async () => {
      if (!viewer) {
        return {
          items: [],
          profiles: {},
          referencedByID: new Map(),
          replyCounts: {},
          reactionStats: {},
          hasMore: false,
          nextCursor: "",
          nextCursorId: "",
        };
      }

      const context = await notificationContext(viewer);
      let mentionEvents = await cachedMentionEvents(viewer);
      let directReplies = await cachedDirectReplyEvents(context.authoredIDs);

      if (fetchNetwork) {
        const [freshMentions, freshReplies] = await Promise.all([
          fetchMentionEvents(viewer),
          fetchDirectReplyEvents(context),
        ]);
        mentionEvents = dedupeEventsByID([...mentionEvents, ...freshMentions]);
        directReplies = dedupeEventsByID([...directReplies, ...freshReplies]);
      }

      const candidates = dedupeEventsByID([...mentionEvents, ...directReplies]).sort((left, right) => {
        const delta = Number(right.created_at || 0) - Number(left.created_at || 0);
        if (delta !== 0) return delta;
        return String(right.id || "").localeCompare(String(left.id || ""));
      });
      const referencedByID = await hydrateReferencedEvents(candidates);
      const items = notificationItemsFromEvents(candidates, {
        viewerPubkey: viewer,
        referencedByID,
        viewerOwnedEventIDs: context.authoredIDs,
      });
      const page = pageNotificationItems(items, { limit, beforeCreatedAt, beforeID });

      const pageEvents = page.items.map((item) => (
        item.category === NotificationCategory.ZAP
          ? (referencedByID.get(item.targetEventID) || item.event)
          : item.event
      ));
      const actorPubkeys = [...new Set(page.items.map((item) => normalizePubkey(item.actorPubkey)).filter(Boolean))];
      const referencedAuthors = [...new Set([...referencedByID.values()].map((event) => normalizePubkey(event.pubkey)).filter(Boolean))];
      const [profiles, metadata] = await Promise.all([
        fetchProfiles([...new Set([...actorPubkeys, ...referencedAuthors])]),
        pageEvents.length
          ? fetchFeedNoteMetadataMaps(pageEvents.map((event) => canonicalHex64(event.id)).filter(Boolean), {
            viewerPubkey: viewer,
            sort: "recent",
          })
          : Promise.resolve({ replyCounts: {}, reactionStats: {}, zapTotals: {} }),
      ]);

      return {
        items: page.items,
        profiles,
        referencedByID,
        replyCounts: metadata.replyCounts || {},
        reactionStats: metadata.reactionStats || {},
        zapTotals: metadata.zapTotals || {},
        hasMore: page.hasMore,
        nextCursor: page.nextCursor,
        nextCursorId: page.nextCursorId,
      };
    },
  });
}

function notificationDisplayState() {
  const baseItems = (
    notificationsState.wotGloballyEnabled &&
    notificationsState.webOfTrustFilterEnabled &&
    notificationsState.webOfTrustReady
  )
    ? notificationsState.allItems.filter((item) => notificationsState.trustedPubkeys.has(normalizePubkey(item.actorPubkey)))
    : notificationsState.allItems;

  const filterCounts = notificationCounts(baseItems);
  const selectedFilter = notificationsState.selectedFilter;
  const visibleItems = selectedFilter
    ? baseItems.filter((item) => item.category === selectedFilter)
    : baseItems;

  return { baseItems, filterCounts, visibleItems };
}

async function refreshWebOfTrustFilter() {
  const viewer = normalizePubkey(notificationsState.viewerPubkey);
  notificationsState.wotGloballyEnabled = getWebOfTrustEnabledPref();
  if (!notificationsState.wotGloballyEnabled || !viewer) {
    notificationsState.webOfTrustReady = false;
    notificationsState.trustedPubkeys = new Set();
    return;
  }

  const mode = feedFetchModeFromPrefs(viewer, {
    wotEnabled: getWebOfTrustEnabledPref(),
    seedPref: getWebOfTrustSeedPref(),
    loggedOutDefaultSeed: getEffectiveLoggedOutWebOfTrustSeed(),
    depth: getWebOfTrustDepthPref(),
  });
  if (mode.kind !== "wot") {
    notificationsState.webOfTrustReady = false;
    notificationsState.trustedPubkeys = new Set();
    return;
  }

  const authors = await expandWebOfTrust(mode.seed, mode.depth);
  notificationsState.trustedPubkeys = new Set(authors.map(normalizePubkey).filter(Boolean));
  notificationsState.webOfTrustReady = true;
}

function mergeMaps(target, source) {
  return { ...target, ...source };
}

function mergeReferencedEvents(target, source) {
  const merged = new Map(target);
  for (const [id, event] of source.entries()) {
    merged.set(id, event);
  }
  return merged;
}

function applyFeed(feed, { replace = true } = {}) {
  notificationsState.profiles = mergeMaps(notificationsState.profiles, feed.profiles || {});
  notificationsState.referencedByID = mergeReferencedEvents(notificationsState.referencedByID, feed.referencedByID || new Map());
  notificationsState.replyCounts = mergeMaps(notificationsState.replyCounts, feed.replyCounts || {});
  notificationsState.reactionStats = mergeMaps(notificationsState.reactionStats, feed.reactionStats || {});
  notificationsState.zapTotals = mergeMaps(notificationsState.zapTotals, feed.zapTotals || {});
  notificationsState.allItems = replace
    ? mergeNotificationItems(feed.items || [])
    : mergeNotificationItems([...(notificationsState.allItems || []), ...(feed.items || [])]);
}

function emptyMessage(display) {
  if (
    notificationsState.webOfTrustFilterEnabled &&
    notificationsState.wotGloballyEnabled &&
    notificationsState.webOfTrustReady &&
    display.baseItems.length === 0 &&
    notificationsState.allItems.length > 0
  ) {
    return "No notifications in your web of trust.";
  }
  if (notificationsState.selectedFilter) {
    return CATEGORY_EMPTY_MESSAGES[notificationsState.selectedFilter] || "No notifications yet.";
  }
  return "No notifications found in the local cache yet.";
}

function notificationLead(item, profile) {
  const label = String(profile?.display_name || profile?.name || shortNpubLabel(item.actorPubkey)).trim();
  if (item.category === NotificationCategory.ZAP) {
    const sats = Number(zapAmountSats(item.event) || 0);
    const msg = zapMessage(item.event);
    const amount = sats > 0 ? ` ${sats} sats` : "";
    return `${label} zapped your note${amount}${msg ? `: ${msg}` : ""}`;
  }
  return `${label} ${notificationActionText(item.category)}`;
}

function renderToolbar(root, display) {
  const toolbar = root.querySelector("[data-notifications-toolbar]");
  if (!(toolbar instanceof HTMLElement)) return;
  toolbar.replaceChildren();

  const chips = document.createElement("div");
  chips.className = "notifications-filter-bar";
  chips.setAttribute("role", "tablist");
  for (const category of CATEGORY_ORDER) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "notifications-filter-chip";
    button.dataset.category = category;
    const isActive = notificationsState.selectedFilter === category;
    if (isActive) button.dataset.active = "1";
    button.setAttribute("role", "tab");
    button.setAttribute("aria-selected", isActive ? "true" : "false");
    button.setAttribute("aria-label", `${CATEGORY_LABELS[category]} ${display.filterCounts[category] || 0}`);
    const icon = document.createElement("span");
    icon.className = "notifications-filter-chip-icon";
    icon.textContent = CATEGORY_ICONS[category] || "?";
    const count = document.createElement("span");
    count.className = "notifications-filter-chip-count";
    count.textContent = String(display.filterCounts[category] || 0);
    button.append(icon, count);
    button.addEventListener("click", () => {
      notificationsState.selectedFilter = notificationsState.selectedFilter === category ? "" : category;
      renderNotificationsPage(root);
    });
    chips.append(button);
  }
  toolbar.append(chips);

  if (notificationsState.pendingNewItemCount > 0) {
    const newer = document.createElement("button");
    newer.type = "button";
    newer.className = "notifications-new-items";
    newer.textContent = `${notificationsState.pendingNewItemCount} new notifications`;
    newer.addEventListener("click", async () => {
      if (!notificationsState.pendingNewerItems?.length) return;
      notificationsState.allItems = mergeNotificationItems([
        ...notificationsState.pendingNewerItems,
        ...notificationsState.allItems,
      ]);
      notificationsState.pendingNewerItems = null;
      notificationsState.pendingNewItemCount = 0;
      await refreshWebOfTrustFilter();
      renderNotificationsPage(root);
    });
    toolbar.append(newer);
  }
}

function renderFeed(root, display) {
  const feed = root.querySelector("[data-notifications-feed]") || root.querySelector("[data-feed]");
  if (!(feed instanceof HTMLElement)) return;
  feed.replaceChildren();

  if (notificationsState.isLoading && notificationsState.allItems.length === 0) {
    const stage = document.createElement("div");
    stage.innerHTML = notificationsLoaderMarkup().trim();
    feed.append(...stage.childNodes);
    initRetroLoaders(feed);
    return;
  }

  if (notificationsState.errorMessage && notificationsState.allItems.length === 0) {
    const error = document.createElement("p");
    error.className = "muted";
    error.textContent = notificationsState.errorMessage;
    feed.append(error);
    return;
  }

  if (display.visibleItems.length === 0) {
    const empty = document.createElement("p");
    empty.className = "muted";
    empty.dataset.notificationsEmpty = "";
    empty.textContent = emptyMessage(display);
    feed.append(empty);
    return;
  }

  const fragment = document.createDocumentFragment();
  for (const item of display.visibleItems) {
    const wrapper = document.createElement("div");
    wrapper.className = "notification-item";
    wrapper.dataset.notificationEntry = "";
    wrapper.dataset.notificationId = item.id;
    wrapper.dataset.notificationCategory = item.category;
    wrapper.dataset.notificationTargetId = item.targetEventID || "";
    wrapper.dataset.createdAt = String(item.createdAt || 0);

    const lead = document.createElement("p");
    lead.className = "note-feed-context note-feed-context--notification";
    const actorProfile = notificationsState.profiles[normalizePubkey(item.actorPubkey)] || {};
    lead.textContent = notificationLead(item, actorProfile);
    wrapper.append(lead);

    const displayEvent = item.category === NotificationCategory.ZAP
      ? (notificationsState.referencedByID.get(item.targetEventID) || item.event)
      : item.event;
    const displayEventID = canonicalHex64(displayEvent.id);
    const displayProfile = notificationsState.profiles[normalizePubkey(displayEvent.pubkey)] || {};
    const reactionRow = notificationsState.reactionStats[displayEventID];
    wrapper.append(createNoteArticle(displayEvent, displayProfile, {
      referencedByID: notificationsState.referencedByID,
      profilesByPubkey: notificationsState.profiles,
      replyCount: notificationsState.replyCounts[displayEventID],
      reactionTotal: reactionRow?.total,
      reactionViewer: reactionRow?.viewer,
      zapTotal: notificationsState.zapTotals[displayEventID],
    }));
    fragment.append(wrapper);
  }
  feed.append(fragment);
  refreshAscii(feed);
  initViewMore(feed);
  void syncBookmarkState(document);
  wireNotificationAvatarFallbacks(feed);
  refreshNotificationProfiles(feed);
}

function renderLoadMore(root, hasMore, nextCursor = "", nextCursorId = "") {
  const button = root.querySelector("[data-load-more]");
  if (!(button instanceof HTMLButtonElement)) return;
  button.dataset.cursor = nextCursor;
  button.dataset.cursorId = nextCursorId;
  button.dataset.hasMore = hasMore ? "1" : "0";
  button.hidden = !hasMore;
}

function renderNotificationsPage(root = document, pagination = null) {
  const display = notificationDisplayState();
  notificationsState.filterCounts = display.filterCounts;
  renderToolbar(root, display);
  renderFeed(root, display);
  if (pagination) {
    renderLoadMore(root, pagination.hasMore, pagination.nextCursor, pagination.nextCursorId);
  }
}

async function checkForNewItems(root, { forceReload = false } = {}) {
  const state = notificationsState;
  const viewer = normalizePubkey(notificationsState.viewerPubkey);
  if (!viewer) return;
  const feed = await buildNotificationFeed({
    viewerPubkey: viewer,
    limit: NOTIFICATION_PAGE_LIMIT,
    fetchNetwork: true,
  });
  if (!notificationsRequestIsCurrent(state, root)) return;

  if (forceReload) {
    notificationsState.profiles = {};
    notificationsState.referencedByID = new Map();
    notificationsState.replyCounts = {};
    notificationsState.reactionStats = {};
    notificationsState.zapTotals = {};
    applyFeed(feed, { replace: true });
    await refreshWebOfTrustFilter();
    if (!notificationsRequestIsCurrent(state, root)) return;
    notificationsState.errorMessage = "";
    notificationsState.isLoading = false;
    notificationsState.pendingNewerItems = null;
    notificationsState.pendingNewItemCount = 0;
    renderNotificationsPage(root, feed);
    return;
  }

  notificationsState.profiles = mergeMaps(notificationsState.profiles, feed.profiles || {});
  notificationsState.referencedByID = mergeReferencedEvents(notificationsState.referencedByID, feed.referencedByID || new Map());
  notificationsState.replyCounts = mergeMaps(notificationsState.replyCounts, feed.replyCounts || {});
  notificationsState.reactionStats = mergeMaps(notificationsState.reactionStats, feed.reactionStats || {});
  notificationsState.zapTotals = mergeMaps(notificationsState.zapTotals, feed.zapTotals || {});

  const merged = mergeNotificationItems(feed.items || []);
  const firstCurrent = notificationsState.allItems[0];
  if (!firstCurrent) {
    notificationsState.allItems = merged;
    await refreshWebOfTrustFilter();
    if (!notificationsRequestIsCurrent(state, root)) return;
    renderNotificationsPage(root, feed);
    return;
  }

  const currentIDs = new Set(notificationsState.allItems.map((item) => item.id));
  const newer = merged.filter((item) => !currentIDs.has(item.id) && isNewer(item, firstCurrent));
  if (newer.length) {
    notificationsState.pendingNewerItems = newer;
    notificationsState.pendingNewItemCount = newer.length;
    renderNotificationsPage(root, {
      hasMore: root.querySelector("[data-load-more]")?.dataset?.hasMore === "1",
      nextCursor: root.querySelector("[data-load-more]")?.dataset?.cursor || "",
      nextCursorId: root.querySelector("[data-load-more]")?.dataset?.cursorId || "",
    });
    return;
  }

  const oldKey = notificationsState.allItems.map((item) => item.id).join("|");
  const newKey = merged.map((item) => item.id).join("|");
  if (oldKey !== newKey) {
    notificationsState.allItems = merged;
    await refreshWebOfTrustFilter();
    if (!notificationsRequestIsCurrent(state, root)) return;
    renderNotificationsPage(root, feed);
  }
}

async function loadNotifications(root = document, { forceRefresh = false } = {}) {
  const viewer = normalizedPubkey();
  const loadKey = currentNotificationsLoadKey(viewer);
  if (notificationsState.loadedKey === loadKey && notificationsState.viewerPubkey === viewer && notificationsState.allItems.length && !forceRefresh) {
    renderNotificationsPage(root, {
      hasMore: root.querySelector("[data-load-more]")?.dataset?.hasMore === "1",
      nextCursor: root.querySelector("[data-load-more]")?.dataset?.cursor || "",
      nextCursorId: root.querySelector("[data-load-more]")?.dataset?.cursorId || "",
    });
    void checkForNewItems(root, { forceReload: false }).catch(() => {});
    return;
  }

  notificationsState = newNotificationState(viewer);
  notificationsState.requestGeneration = ++notificationsRequestGeneration;
  notificationsState.loadedKey = loadKey;
  notificationsState.isLoading = true;
  const state = notificationsState;
  renderNotificationsPage(root);

  try {
    const cachedFeed = await buildNotificationFeed({
      viewerPubkey: viewer,
      limit: NOTIFICATION_PAGE_LIMIT,
      fetchNetwork: false,
    });
    if (!notificationsRequestIsCurrent(state, root)) return;
    if (!cachedFeed.items.length) {
      const networkFeed = await buildNotificationFeed({
        viewerPubkey: viewer,
        limit: NOTIFICATION_PAGE_LIMIT,
        fetchNetwork: true,
      });
      if (!notificationsRequestIsCurrent(state, root)) return;
      applyFeed(networkFeed, { replace: true });
      notificationsState.isLoading = false;
      notificationsState.errorMessage = "";
      await refreshWebOfTrustFilter();
      if (!notificationsRequestIsCurrent(state, root)) return;
      renderNotificationsPage(root, networkFeed);
      return;
    }

    applyFeed(cachedFeed, { replace: true });
    notificationsState.isLoading = false;
    notificationsState.errorMessage = "";
    await refreshWebOfTrustFilter();
    if (!notificationsRequestIsCurrent(state, root)) return;
    renderNotificationsPage(root, cachedFeed);
    void checkForNewItems(root, { forceReload: false }).catch(() => {});
  } catch (error) {
    if (!notificationsRequestIsCurrent(state, root)) return;
    notificationsState.isLoading = false;
    notificationsState.errorMessage = error?.message || String(error || "Notification load failed");
    renderNotificationsPage(root);
  }
}

export async function hydrateNotificationsPage(root = document, options = {}) {
  initRetroLoaders(root);
  const viewer = normalizedPubkey();
  const feed = root.querySelector("[data-notifications-feed]") || root.querySelector("[data-feed]");
  if (!(feed instanceof HTMLElement)) return;
  feed.dataset.relayNativeNotifications = "1";
  if (!viewer) {
    feed.replaceChildren();
    const prompt = document.createElement("p");
    prompt.innerHTML = '<a href="/login" data-relay-aware>Login to view notifications</a>';
    feed.append(prompt);
    return;
  }
  await loadNotifications(root, { forceRefresh: Boolean(options.forceRefresh) });
}

export async function appendClientNotificationsPage(root = document) {
  const viewer = normalizedPubkey();
  if (!viewer) return { appended: 0, hasMore: false, cursorAdvanced: false };
  if (!notificationsState.allItems.length) {
    await loadNotifications(root);
  }
  const state = notificationsState;
  if (!notificationsRequestIsCurrent(state, root)) {
    return { appended: 0, hasMore: false, cursorAdvanced: false };
  }
  const last = notificationsState.allItems[notificationsState.allItems.length - 1];
  const previousCursor = root.querySelector("[data-load-more]")?.dataset?.cursor || "";
  const previousCursorID = root.querySelector("[data-load-more]")?.dataset?.cursorId || "";
  const older = await buildNotificationFeed({
    viewerPubkey: viewer,
    limit: NOTIFICATION_PAGE_LIMIT,
    fetchNetwork: true,
    beforeCreatedAt: last?.createdAt,
    beforeID: last?.id,
  });
  if (!notificationsRequestIsCurrent(state, root)) {
    return { appended: 0, hasMore: false, cursorAdvanced: false };
  }
  const beforeCount = notificationsState.allItems.length;
  applyFeed(older, { replace: false });
  await refreshWebOfTrustFilter();
  if (!notificationsRequestIsCurrent(state, root)) {
    return { appended: 0, hasMore: false, cursorAdvanced: false };
  }
  renderNotificationsPage(root, older);
  const appended = notificationsState.allItems.length - beforeCount;
  const button = root.querySelector("[data-load-more]");
  return {
    appended,
    hasMore: older.hasMore,
    cursorAdvanced: (
      (button?.dataset?.cursor || "") !== previousCursor ||
      (button?.dataset?.cursorId || "") !== previousCursorID
    ),
  };
}
