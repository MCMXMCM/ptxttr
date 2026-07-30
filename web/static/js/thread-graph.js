import { fetchEventsByIDs } from "./relay-reads.js";
import { relayFetch } from "./relay-pool.js";
import { readRelaysForViewer } from "./publish-plan.js";
import { KIND_COMMENT, KIND_NOTE } from "./nostr-kinds.js";
import {
  canonicalHex64,
  dedupeEventsByID,
  isCanonicalEventID,
  normalizePubkey,
  resolveEventID,
  uniqueNonEmpty,
} from "./relay-utils.js";
import { normalizeRelayList } from "./relay-config.js";
import { sortEventsOldestFirst } from "./feed-query.js";
import { putEvents, eventsByTag, getEvent } from "./event-store.js";
import { parentID, rootIDForEvent, effectiveThreadParentID } from "./thread-tags.js";
import { getThreadBundle, saveThreadBundle } from "./client-store.js";
import { fetchCachedQuery, primeQueryData, queryKeys } from "./query-client.js";

export { parentID, rootIDForEvent } from "./thread-tags.js";
export {
  directReplyEvents,
  focusParentEvent,
  threadExpectsFocusView,
} from "./thread-tags.js";

const MAX_DEPTH = 32;
const MAX_WARM_CACHE_ENTRIES = 96;
const threadWarmCache = new Map();
const threadParentWarmCache = new Map();

function mergedThreadReadRelays(preferred = []) {
  return normalizeRelayList([...(preferred || []), ...readRelaysForViewer()]);
}

function isRenderableThreadReplyEvent(event) {
  const kind = Number(event?.kind) || 0;
  return kind === KIND_NOTE || kind === KIND_COMMENT;
}

export function relayHintsForThreadReference(event, targetID = "") {
  const target = canonicalHex64(targetID);
  const hints = [];
  const add = (value) => {
    const relay = String(value || "").trim();
    if (!relay) return;
    hints.push(relay);
  };
  add(event?.relay_url);
  for (const tag of event?.tags || []) {
    if (!Array.isArray(tag) || tag.length < 3 || tag[0] !== "e") continue;
    const refID = canonicalHex64(tag[1]);
    if (target && refID !== target) continue;
    add(tag[2]);
  }
  return normalizeRelayList(uniqueNonEmpty(hints));
}

function authorHintForThreadReference(event, targetID = "") {
  const target = canonicalHex64(targetID);
  for (const tag of event?.tags || []) {
    if (!Array.isArray(tag) || tag.length < 5 || tag[0] !== "e") continue;
    const refID = canonicalHex64(tag[1]);
    if (target && refID !== target) continue;
    const author = normalizePubkey(tag[4]);
    if (author) return author;
  }
  return "";
}

export function resolveKnownThreadEndpoints(known = [], rootID = "", selectedID = "") {
  const root = canonicalHex64(rootID);
  const selected = canonicalHex64(selectedID);
  return {
    rootEvent: (known || []).find((event) => canonicalHex64(event.id) === root) || null,
    selectedEvent: selected
      ? (known || []).find((event) => canonicalHex64(event.id) === selected) || null
      : null,
  };
}

export function resolveBundleSelectedEvent(bundle = {}, fallbackSelected = null) {
  const root = bundle?.root || null;
  const events = Array.isArray(bundle?.events) ? bundle.events : [];
  const selectedID = canonicalHex64(bundle?.selectedID || bundle?.selected?.id || fallbackSelected?.id || root?.id);
  if (!selectedID) return root;
  return (
    events.find((event) => canonicalHex64(event?.id) === selectedID) ||
    (canonicalHex64(bundle?.selected?.id) === selectedID ? bundle.selected : null) ||
    (canonicalHex64(fallbackSelected?.id) === selectedID ? fallbackSelected : null) ||
    (canonicalHex64(root?.id) === selectedID ? root : null) ||
    root
  );
}

function ingestThreadReply(event, rootID, events, replyIDs, parentByID) {
  const root = canonicalHex64(rootID);
  const eventID = canonicalHex64(event?.id);
  if (!isRenderableThreadReplyEvent(event)) return false;
  if (!eventID || eventID === root || replyIDs.has(eventID)) return false;
  replyIDs.add(eventID);
  parentByID[eventID] = canonicalHex64(parentID(root, event)) || root;
  events.push(event);
  return true;
}

async function fetchRepliesTaggedTo(tagValue, relays, { limit = 500, forceRelay = false } = {}) {
  const id = canonicalHex64(tagValue);
  if (!id) return [];
  const localKinds = await Promise.all([
    eventsByTag("e", id, { kind: KIND_NOTE, limit }),
    eventsByTag("e", id, { kind: KIND_COMMENT, limit }),
  ]);
  const local = dedupeEventsByID(localKinds.flat()).filter(isRenderableThreadReplyEvent);
  if (local.length && !forceRelay) return dedupeEventsByID(local);
  const relay = await relayFetch(relays, [{ kinds: [KIND_NOTE, KIND_COMMENT], "#e": [id], limit }]);
  await putEvents(relay);
  return dedupeEventsByID([...local, ...relay]).filter(isRenderableThreadReplyEvent);
}

async function fetchEventByID(eventID, { relayHints = [], authorHint = "" } = {}) {
  const resolved = resolveEventID(eventID);
  const id = resolved?.eventID || canonicalHex64(eventID);
  if (!isCanonicalEventID(id)) return null;
  const hints = normalizeRelayList([...(relayHints || []), ...(resolved?.relays || [])]);
  const author = normalizePubkey(authorHint || resolved?.author || "");
  const fetched = (await fetchEventsByIDs([id], {
    relayHintsByID: hints.length ? { [id]: hints } : {},
    authorHintsByID: author ? { [id]: author } : {},
  }))[0] || null;
  if (fetched) return fetched;
  return getEvent(id).catch(() => null);
}

export async function fetchDirectParentEvent(selectedEvent, { preferredRelays = [] } = {}) {
  const selectedID = canonicalHex64(selectedEvent?.id);
  if (!selectedID) return null;
  const rootHint = canonicalHex64(rootIDForEvent(selectedEvent));
  const directParent = canonicalHex64(parentID(rootHint, selectedEvent) || parentID("", selectedEvent));
  if (!directParent || directParent === selectedID) return null;
  return fetchEventByID(directParent, {
    relayHints: [...(preferredRelays || []), ...relayHintsForThreadReference(selectedEvent, directParent)],
    authorHint: authorHintForThreadReference(selectedEvent, directParent),
  });
}

/** Walk the reply chain to find the thread OP (mirrors handlers.resolveThreadRootID). */
export async function resolveThreadRootID(selectedEvent, { relayHints = [] } = {}) {
  if (!selectedEvent?.id) return "";
  let current = selectedEvent;
  let lastID = canonicalHex64(current.id);
  const seen = new Set([lastID]);
  for (let hop = 0; hop < MAX_DEPTH; hop++) {
    const parent = parentID("", current);
    if (!parent || parent === current.id || seen.has(parent)) break;
    lastID = parent;
    seen.add(parent);
    const parentEvent = await fetchEventByID(parent, {
      relayHints: [...(relayHints || []), ...relayHintsForThreadReference(current, parent)],
      authorHint: authorHintForThreadReference(current, parent),
    });
    if (!parentEvent) break;
    current = parentEvent;
  }
  return lastID;
}

export async function resolveThreadFromPath(pathNoteID, { preferredRelays = [] } = {}) {
  const preferred = normalizeRelayList(preferredRelays);
  const warmKey = warmCacheKey(pathNoteID);
  const warmed = warmKey ? threadWarmCache.get(warmKey) : null;
  if (warmed?.status === "fulfilled" && warmed.value?.root) return warmed.value;
  if (warmed?.promise) {
    try {
      const warmedValue = await warmed.promise;
      if (warmedValue?.root) return warmedValue;
    } catch {
      threadWarmCache.delete(warmKey);
    }
  }
  const persisted = warmKey ? await getThreadBundle(warmKey).catch(() => null) : null;
  if (persisted?.root) {
    const value = threadBundleFromRecord(persisted);
    primeQueryData(queryKeys.threadBundle(
      value.rootID,
      value.selectedID,
      preferred,
      false,
    ), value, {
      updatedAt: persisted.saved_at,
    });
    setThreadWarmCache(warmKey, {
      status: "fulfilled",
      value,
      promise: Promise.resolve(value),
    });
    return value;
  }

  const selected = await fetchEventByID(pathNoteID, { relayHints: preferred });
  if (!selected) return null;
  const rootID = await resolveThreadRootID(selected, { relayHints: preferred });
  if (!rootID) return null;
  const bundle = await fetchThreadEvents(rootID, selected.id, { preferredRelays: preferred });
  if (!bundle.root) return null;
  const selectedEvent = resolveBundleSelectedEvent(bundle, selected);
  const out = {
    ...bundle,
    selected: selectedEvent,
    rootID,
    selectedID: canonicalHex64(selectedEvent?.id || selected.id),
  };
  primeQueryData(queryKeys.threadBundle(rootID, out.selectedID, preferred, false), out);
  void saveThreadBundle(warmKey, out).catch(() => {});
  return out;
}

async function fetchThreadEventsUncached(rootID, selectedID = "", options = {}) {
  const forceRelayReplies = options.forceRelayReplies === true;
  const preferredRelays = normalizeRelayList(options.preferredRelays || []);
  const root = canonicalHex64(rootID);
  if (!root) return { root: null, selected: null, events: [], parentByID: {} };
  const ids = [root];
  if (selectedID) ids.push(canonicalHex64(selectedID));
  const relayHintsByID = preferredRelays.length
    ? Object.fromEntries(ids.map((id) => [id, preferredRelays]))
    : {};
  let known = await fetchEventsByIDs(ids, { relayHintsByID });
  let { rootEvent, selectedEvent } = resolveKnownThreadEndpoints(known, root, selectedID);
  if (!rootEvent) {
    rootEvent = await fetchEventByID(root, { relayHints: preferredRelays });
  }
  if (!rootEvent) {
    return { root: null, selected: null, events: [], parentByID: {} };
  }
  if (selectedID && canonicalHex64(selectedID) !== root && !selectedEvent) {
    selectedEvent = await fetchEventByID(selectedID, { relayHints: preferredRelays });
  }
  if (selectedEvent && canonicalHex64(selectedEvent.id) !== root && !isRenderableThreadReplyEvent(selectedEvent)) {
    selectedEvent = rootEvent;
  }
  if (!selectedEvent) selectedEvent = rootEvent;

  const relays = mergedThreadReadRelays(preferredRelays);
  const rootReplies = await fetchRepliesTaggedTo(root, relays, { forceRelay: forceRelayReplies });
  const events = [rootEvent];
  const parentByID = {};
  const replyIDs = new Set();
  for (const event of rootReplies) ingestThreadReply(event, root, events, replyIDs, parentByID);

  const selectedCanonical = canonicalHex64(selectedEvent.id);
  if (selectedCanonical && selectedCanonical !== root) {
    ingestThreadReply(selectedEvent, root, events, replyIDs, parentByID);

    const selectedChildren = await fetchRepliesTaggedTo(selectedCanonical, relays, { forceRelay: forceRelayReplies });
    for (const event of selectedChildren) ingestThreadReply(event, root, events, replyIDs, parentByID);

    let current = selectedEvent;
    const seen = new Set([selectedCanonical]);
    for (let hop = 0; hop < MAX_DEPTH; hop++) {
      const currentID = canonicalHex64(current.id);
      const parent = canonicalHex64(parentByID[currentID] || parentID(root, current));
      if (!parent || parent === root || seen.has(parent)) break;
      seen.add(parent);

      let parentEvent = events.find((row) => canonicalHex64(row.id) === parent);
      if (!parentEvent) {
        parentEvent = await fetchEventByID(parent, {
          relayHints: [...preferredRelays, ...relayHintsForThreadReference(current, parent)],
          authorHint: authorHintForThreadReference(current, parent),
        });
      }
      if (!parentEvent) break;
      ingestThreadReply(parentEvent, root, events, replyIDs, parentByID);

      const pathReplies = await fetchRepliesTaggedTo(parent, relays, { forceRelay: forceRelayReplies });
      for (const event of pathReplies) ingestThreadReply(event, root, events, replyIDs, parentByID);

      current = parentEvent;
    }
  }

  return {
    root: rootEvent,
    selected: selectedEvent,
    events: dedupeEventsByID(events),
    parentByID,
  };
}

export async function fetchThreadEvents(rootID, selectedID = "", options = {}) {
  const forceRelayReplies = options.forceRelayReplies === true;
  const preferredRelays = normalizeRelayList(options.preferredRelays || []);
  const root = canonicalHex64(rootID);
  if (!root) return { root: null, selected: null, events: [], parentByID: {} };
  return fetchCachedQuery({
    queryKey: queryKeys.threadBundle(root, selectedID, preferredRelays, forceRelayReplies),
    cacheMode: forceRelayReplies ? "refresh" : "cache-first",
    staleTime: forceRelayReplies ? 0 : 60_000,
    queryFn: async () => {
      const bundle = await fetchThreadEventsUncached(root, selectedID, options);
      if (bundle?.root) {
        const selectedCanonical = canonicalHex64(bundle.selected?.id || selectedID || root);
        const enriched = {
          ...bundle,
          rootID: root,
          selectedID: selectedCanonical,
        };
        primeQueryData(queryKeys.threadBundle(root, selectedCanonical, preferredRelays, forceRelayReplies), enriched);
        if (!forceRelayReplies) {
          void saveThreadBundle(selectedCanonical, enriched, {
            relays: preferredRelays,
            forceRelayReplies,
          }).catch(() => {});
        }
        return enriched;
      }
      return bundle;
    },
  });
}

function warmCacheKey(pathNoteID) {
  const resolved = resolveEventID(pathNoteID);
  return canonicalHex64(resolved?.eventID || pathNoteID);
}

function threadBundleFromRecord(record) {
  if (!record?.root) return null;
  const bundle = {
    root: record.root,
    selected: record.selected || null,
    events: record.events || [record.root],
    parentByID: record.parentByID || {},
    rootID: canonicalHex64(record.root_id || record.root?.id),
    selectedID: canonicalHex64(record.selected_id || record.selected?.id || record.root?.id),
  };
  bundle.selected = resolveBundleSelectedEvent(bundle, record.selected || record.root);
  return bundle;
}

function setThreadWarmCache(key, record) {
  if (!key) return;
  if (threadWarmCache.has(key)) threadWarmCache.delete(key);
  threadWarmCache.set(key, record);
  while (threadWarmCache.size > MAX_WARM_CACHE_ENTRIES) {
    const oldest = threadWarmCache.keys().next().value;
    if (!oldest) break;
    threadWarmCache.delete(oldest);
  }
}

function setThreadParentWarmCache(key, record) {
  if (!key) return;
  if (threadParentWarmCache.has(key)) threadParentWarmCache.delete(key);
  threadParentWarmCache.set(key, record);
  while (threadParentWarmCache.size > MAX_WARM_CACHE_ENTRIES) {
    const oldest = threadParentWarmCache.keys().next().value;
    if (!oldest) break;
    threadParentWarmCache.delete(oldest);
  }
}

function warmDirectParentForEvent(selectedEvent, preferredRelays = []) {
  const selectedID = canonicalHex64(selectedEvent?.id);
  if (!selectedID) return Promise.resolve(null);
  const cached = threadParentWarmCache.get(selectedID);
  if (cached) return cached.promise;
  const record = {
    status: "pending",
    value: null,
    promise: null,
  };
  record.promise = fetchDirectParentEvent(selectedEvent, { preferredRelays })
    .then((parentEvent) => {
      record.status = "fulfilled";
      record.value = parentEvent || null;
      return record.value;
    })
    .catch((error) => {
      record.status = "rejected";
      threadParentWarmCache.delete(selectedID);
      throw error;
    });
  setThreadParentWarmCache(selectedID, record);
  return record.promise;
}

export async function warmThreadParentFromPath(pathNoteID, { preferredRelays = [] } = {}) {
  const preferred = normalizeRelayList(preferredRelays);
  const key = warmCacheKey(pathNoteID);
  if (!key) return null;
  const selected = await fetchEventByID(key, { relayHints: preferred });
  if (!selected) return null;
  return warmDirectParentForEvent(selected, preferred).catch(() => null);
}

/**
 * Relay-native background warmup for feed/hover prefetch.
 *
 * This intentionally walks through relays even when IndexedDB has a partial
 * local match, so the browser cache converges toward a server-style assembled
 * thread before the user clicks the note.
 */
export async function warmThreadFromPath(pathNoteID, { preferredRelays = [] } = {}) {
  const preferred = normalizeRelayList(preferredRelays);
  const key = warmCacheKey(pathNoteID);
  if (!key) return null;
  const cached = threadWarmCache.get(key);
  if (cached?.value?.root || cached?.status === "pending") return cached.promise;
  if (cached) threadWarmCache.delete(key);
  const persisted = await getThreadBundle(key).catch(() => null);
  if (persisted?.root) {
    const value = threadBundleFromRecord(persisted);
    primeQueryData(queryKeys.threadBundle(
      value.rootID,
      value.selectedID,
      preferred,
      false,
    ), value, {
      updatedAt: persisted.saved_at,
    });
    const record = { status: "fulfilled", value, promise: Promise.resolve(value) };
    setThreadWarmCache(key, record);
    return value;
  }

  const record = {
    status: "pending",
    value: null,
    promise: null,
  };
  record.promise = (async () => {
    const selected = await fetchEventByID(key, { relayHints: preferred });
    if (!selected) return null;
    const parentWarm = warmDirectParentForEvent(selected, preferred);
    const rootID = await resolveThreadRootID(selected, { relayHints: preferred });
    await parentWarm.catch(() => null);
    if (!rootID) return null;
    const bundle = await fetchThreadEvents(rootID, selected.id, {
      forceRelayReplies: true,
      preferredRelays: preferred,
    });
    if (!bundle.root) return null;
    const value = {
      ...bundle,
      rootID,
      selectedID: canonicalHex64(bundle.selected?.id || selected.id),
    };
    primeQueryData(queryKeys.threadBundle(rootID, value.selectedID, preferred, true), value);
    return value;
  })()
    .then((value) => {
      record.status = "fulfilled";
      record.value = value;
      if (value?.root) void saveThreadBundle(key, value).catch(() => {});
      return value;
    })
    .catch((error) => {
      record.status = "rejected";
      threadWarmCache.delete(key);
      throw error;
    });
  setThreadWarmCache(key, record);
  try {
    return await record.promise;
  } catch (error) {
    threadWarmCache.delete(key);
    throw error;
  }
}

export function clearThreadWarmCache() {
  threadWarmCache.clear();
  threadParentWarmCache.clear();
}

export function threadWarmCacheKeys() {
  return [...threadWarmCache.keys()];
}

/** Synchronous warm-cache lookup for skeleton / placeholder decisions. */
export function peekThreadWarmBundle(pathNoteID) {
  const key = warmCacheKey(pathNoteID);
  if (!key) return null;
  const cached = threadWarmCache.get(key);
  if (cached?.status === "fulfilled" && cached.value?.root) return cached.value;
  return null;
}

export function buildThreadChildren(events, parentByID, rootID) {
  const root = canonicalHex64(rootID);
  const byParent = new Map();
  for (const event of events || []) {
    if (canonicalHex64(event.id) === root) continue;
    const parent = effectiveThreadParentID(root, event, parentByID);
    if (!byParent.has(parent)) byParent.set(parent, []);
    byParent.get(parent).push(event);
  }
  for (const [parent, list] of byParent.entries()) {
    byParent.set(parent, sortEventsOldestFirst(list));
  }
  return byParent;
}

export function threadParticipantPubkeys(events) {
  const out = new Set();
  for (const event of events || []) {
    const pk = normalizePubkey(event.pubkey);
    if (pk) out.add(pk);
  }
  return [...out];
}
