import { isBeforeFeedCursor } from "./feed-pagination.js";
import { KIND_NOTE, KIND_LONG_FORM, KIND_PROFILE, KIND_REACTION, KIND_REPOST, KIND_ZAP_RECEIPT } from "./nostr-kinds.js";
import { canonicalHex64, dedupeEventsByID } from "./relay-utils.js";
import { hashtagsInContent, normalizeHashtag, eventHasHashtag } from "./hashtag-utils.js";
import { sortEventsNewestFirst } from "./feed-query.js";
import { zapAmountSats, zapTargetNoteID } from "./zap-utils.js";
import { displayName, parseProfile } from "./profile-parse.js";
import { appFeatures } from "./app/bootstrap.js";
import {
  isClientDBUnavailableError,
  openClientDB,
  requestResult,
  transactionDone,
  STORE_EVENTS as EVENTS,
  STORE_TAG_INDEX as TAG_INDEX,
} from "./client-store.js";

export const EVENT_CACHE_MAX_RECORDS = 20_000;
export const EVENT_CACHE_MAX_BYTES = 96 * 1024 * 1024;
const EVENT_CACHE_TARGET_RECORDS = Math.floor(EVENT_CACHE_MAX_RECORDS * 0.9);
const EVENT_CACHE_TARGET_BYTES = Math.floor(EVENT_CACHE_MAX_BYTES * 0.85);
const EVENT_CACHE_MAINTENANCE_INTERVAL_MS = 60_000;
const EVENT_TOUCH_INTERVAL_MS = 15 * 60 * 1000;
const EVENT_EVICT_BATCH_LIMIT = 5000;
const recentTouches = new Map();
let lastMaintenanceAt = 0;
let maintenancePromise = null;

function desktopEventStoreDisabled() {
  return Boolean(appFeatures().localFirst);
}

export function estimateEventRecordBytes(event) {
  try {
    return new Blob([JSON.stringify(event || {})]).size;
  } catch {
    return JSON.stringify(event || {}).length;
  }
}

function tagIndexKeys(event) {
  const keys = [];
  const id = String(event?.id || "");
  if (!id) return keys;
  const seen = new Set();
  const push = (name, rawValue) => {
    const value = String(rawValue || "").trim().toLowerCase();
    if (!name || !value) return;
    const dedupe = `${name}:${value}`;
    if (seen.has(dedupe)) return;
    seen.add(dedupe);
    keys.push({
      key: `${name}:${value}:${id}`,
      tag: name,
      value,
      event_id: id,
      kind: Number(event.kind) || 0,
      created_at: Number(event.created_at) || 0,
    });
  };
  for (const tag of event?.tags || []) {
    if (!Array.isArray(tag) || tag.length < 2) continue;
    push(String(tag[0] || ""), tag[1]);
  }
  for (const hashtag of hashtagsInContent(event?.content)) {
    push("ct", hashtag);
  }
  return keys;
}

export async function putEvents(events, { relayURL = "" } = {}) {
  if (desktopEventStoreDisabled()) return;
  if (!events?.length) return;
  try {
    const db = await openClientDB();
    const tx = db.transaction([EVENTS, TAG_INDEX], "readwrite");
    const eventStore = tx.objectStore(EVENTS);
    const tagStore = tx.objectStore(TAG_INDEX);
    const now = Date.now();
    for (const raw of events) {
      if (!raw?.id) continue;
      const record = {
        ...raw,
        id: String(raw.id).toLowerCase(),
        pubkey: String(raw.pubkey || "").toLowerCase(),
        fetched_at: now,
        last_used_at: now,
        relay_url: relayURL || raw.relay_url || "",
      };
      record.estimated_size = estimateEventRecordBytes(record);
      eventStore.put(record);
      for (const row of tagIndexKeys(record)) {
        tagStore.put(row);
      }
    }
    await transactionDone(tx);
    scheduleEventCacheMaintenance();
  } catch (error) {
    if (isClientDBUnavailableError(error)) return;
    throw error;
  }
}

export async function getEvent(id) {
  if (desktopEventStoreDisabled()) return null;
  const token = String(id || "").trim().toLowerCase();
  if (!token) return null;
  const db = await openClientDB();
  const tx = db.transaction(EVENTS, "readonly");
  const req = tx.objectStore(EVENTS).get(token);
  const event = await requestResult(req);
  if (event) scheduleEventTouches([event]);
  return event;
}

export async function getEvents(ids) {
  const out = new Map();
  if (desktopEventStoreDisabled()) return out;
  const db = await openClientDB();
  const tx = db.transaction(EVENTS, "readonly");
  const store = tx.objectStore(EVENTS);
  const requests = [];
  for (const raw of ids || []) {
    const id = String(raw || "").trim().toLowerCase();
    if (!id) continue;
    requests.push(requestResult(store.get(id)).then((event) => {
      if (event) out.set(id, event);
    }));
  }
  await Promise.all(requests);
  await transactionDone(tx);
  if (out.size) scheduleEventTouches([...out.values()]);
  return out;
}

function shouldTouchEvent(event, now) {
  const id = String(event?.id || "").toLowerCase();
  if (!id) return false;
  const priorMemory = Number(recentTouches.get(id) || 0);
  const priorStored = Number(event?.last_used_at || event?.fetched_at || 0);
  return now - Math.max(priorMemory, priorStored) >= EVENT_TOUCH_INTERVAL_MS;
}

function rememberTouch(id, now) {
  recentTouches.delete(id);
  recentTouches.set(id, now);
  while (recentTouches.size > 2000) {
    const oldest = recentTouches.keys().next().value;
    if (!oldest) break;
    recentTouches.delete(oldest);
  }
}

function scheduleEventTouches(events) {
  const now = Date.now();
  const touched = [];
  for (const event of events || []) {
    if (!shouldTouchEvent(event, now)) continue;
    const id = String(event.id || "").toLowerCase();
    rememberTouch(id, now);
    touched.push({ ...event, last_used_at: now });
  }
  if (!touched.length) return;
  void touchEventRecords(touched).catch(() => {});
}

async function touchEventRecords(events) {
  const db = await openClientDB();
  const tx = db.transaction(EVENTS, "readwrite");
  const store = tx.objectStore(EVENTS);
  for (const event of events) store.put(event);
  await transactionDone(tx);
}

function scheduleEventCacheMaintenance({ force = false } = {}) {
  const now = Date.now();
  if (!force && now - lastMaintenanceAt < EVENT_CACHE_MAINTENANCE_INTERVAL_MS) return maintenancePromise;
  if (maintenancePromise) return maintenancePromise;
  lastMaintenanceAt = now;
  maintenancePromise = pruneEventStore().catch(() => 0).finally(() => {
    maintenancePromise = null;
  });
  return maintenancePromise;
}

async function deleteTagRowsForEvent(tagStore, id) {
  await new Promise((resolve, reject) => {
    const req = tagStore.index("event_id").openCursor(IDBKeyRange.only(id));
    req.onsuccess = () => {
      const cursor = req.result;
      if (!cursor) {
        resolve();
        return;
      }
      cursor.delete();
      cursor.continue();
    };
    req.onerror = () => reject(req.error);
  });
}

export async function pruneEventStore({
  maxRecords = EVENT_CACHE_MAX_RECORDS,
  maxBytes = EVENT_CACHE_MAX_BYTES,
  targetRecords = EVENT_CACHE_TARGET_RECORDS,
  targetBytes = EVENT_CACHE_TARGET_BYTES,
} = {}) {
  if (desktopEventStoreDisabled()) return 0;
  const db = await openClientDB();
  const readTx = db.transaction(EVENTS, "readonly");
  const store = readTx.objectStore(EVENTS);
  const rows = [];
  let totalBytes = 0;
  await new Promise((resolve, reject) => {
    const req = store.openCursor();
    req.onsuccess = () => {
      const cursor = req.result;
      if (!cursor) {
        resolve();
        return;
      }
      const event = cursor.value;
      const size = Math.max(0, Number(event?.estimated_size || estimateEventRecordBytes(event)));
      totalBytes += size;
      rows.push({
        id: String(event?.id || cursor.primaryKey || "").toLowerCase(),
        size,
        lastUsedAt: Number(event?.last_used_at || event?.fetched_at || (Number(event?.created_at || 0) * 1000) || 0),
      });
      cursor.continue();
    };
    req.onerror = () => reject(req.error);
  });
  await transactionDone(readTx);

  if (rows.length <= maxRecords && totalBytes <= maxBytes) return 0;
  const keepRecords = Math.max(1, Math.min(Number(targetRecords) || maxRecords, maxRecords));
  const keepBytes = Math.max(1, Math.min(Number(targetBytes) || maxBytes, maxBytes));
  rows.sort((a, b) => a.lastUsedAt - b.lastUsedAt);
  const stale = [];
  let remainingRecords = rows.length;
  let remainingBytes = totalBytes;
  for (const row of rows) {
    if (!row.id) continue;
    if (stale.length >= EVENT_EVICT_BATCH_LIMIT) break;
    if (remainingRecords <= keepRecords && remainingBytes <= keepBytes) break;
    stale.push(row.id);
    remainingRecords -= 1;
    remainingBytes -= row.size;
  }
  if (!stale.length) return 0;

  const writeTx = db.transaction([EVENTS, TAG_INDEX], "readwrite");
  const eventStore = writeTx.objectStore(EVENTS);
  const tagStore = writeTx.objectStore(TAG_INDEX);
  const tagDeletes = [];
  for (const id of stale) {
    eventStore.delete(id);
    tagDeletes.push(deleteTagRowsForEvent(tagStore, id));
    recentTouches.delete(id);
  }
  await Promise.all(tagDeletes);
  await transactionDone(writeTx);
  if (remainingRecords > maxRecords || remainingBytes > maxBytes) {
    globalThis.setTimeout?.(() => scheduleEventCacheMaintenance({ force: true }), 0);
  }
  return stale.length;
}

export async function latestReplaceable(pubkey, kind) {
  if (desktopEventStoreDisabled()) return null;
  const pk = String(pubkey || "").trim().toLowerCase();
  if (!pk) return null;
  const db = await openClientDB();
  const tx = db.transaction(EVENTS, "readonly");
  const index = tx.objectStore(EVENTS).index("pubkey_kind");
  const range = IDBKeyRange.only([pk, Number(kind)]);
  const req = index.openCursor(range, "prev");
  let best = null;
  await new Promise((resolve, reject) => {
    req.onsuccess = () => {
      const cursor = req.result;
      if (!cursor) {
        resolve();
        return;
      }
      const value = cursor.value;
      if (!best || Number(value.created_at) > Number(best.created_at)) best = value;
      cursor.continue();
    };
    req.onerror = () => reject(req.error);
  });
  await transactionDone(tx);
  return best;
}

export async function eventsByTag(tagName, tagValue, { kind, limit = 200 } = {}) {
  if (desktopEventStoreDisabled()) return [];
  const tag = String(tagName || "");
  const value = String(tagValue || "").trim().toLowerCase();
  if (!tag || !value) return [];
  const db = await openClientDB();
  const tx = db.transaction(TAG_INDEX, "readonly");
  const tagStore = tx.objectStore(TAG_INDEX);
  const hasKind = kind != null;
  const index = hasKind ? tagStore.index("tag_value_kind_created_at") : tagStore.index("tag_value");
  const range = hasKind
    ? IDBKeyRange.bound([tag, value, Number(kind), 0], [tag, value, Number(kind), Number.MAX_SAFE_INTEGER])
    : IDBKeyRange.only([tag, value]);
  const req = index.openCursor(range, hasKind ? "prev" : "next");
  const ids = [];
  await new Promise((resolve, reject) => {
    req.onsuccess = () => {
      const cursor = req.result;
      if (!cursor) {
        resolve();
        return;
      }
      const row = cursor.value;
      if (!hasKind || Number(row.kind) === Number(kind)) {
        ids.push(row.event_id);
      }
      if (ids.length >= limit * 2) {
        resolve();
        return;
      }
      cursor.continue();
    };
    req.onerror = () => reject(req.error);
  });
  await transactionDone(tx);
  const byID = await getEvents(ids.slice(0, limit * 2));
  const eventResults = ids.map((id) => byID.get(String(id || "").toLowerCase()) || null);
  const events = eventResults.filter((event) => {
    if (!event) return false;
    return !hasKind || Number(event.kind) === Number(kind);
  });
  events.sort((a, b) => Number(b.created_at) - Number(a.created_at));
  return events.slice(0, limit);
}

async function indexedTimelineEvents({
  kinds,
  authors = null,
  limit = 50,
  beforeCreatedAt,
  beforeID,
  since,
} = {}) {
  const normalizedKinds = [...new Set((kinds || []).map((kind) => Number(kind)).filter((kind) => Number.isFinite(kind)))];
  if (!normalizedKinds.length) return [];
  const normalizedAuthors = authors
    ? [...new Set((authors || []).map((pk) => String(pk || "").trim().toLowerCase()).filter(Boolean))]
    : null;
  if (normalizedAuthors && !normalizedAuthors.length) return [];

  const sampleLimit = Math.max(1, Number(limit) || 50);
  const db = await openClientDB();
  const tx = db.transaction(EVENTS, "readonly");
  const store = tx.objectStore(EVENTS);
  const index = normalizedAuthors ? store.index("pubkey_kind_created_at") : store.index("kind_created_at");
  const out = [];
  const seen = new Set();
  const targetPerCursor = normalizedAuthors
    ? Math.max(sampleLimit, Math.ceil(sampleLimit / Math.max(1, Math.min(normalizedAuthors.length, sampleLimit))))
    : sampleLimit;

  const scan = (range) => new Promise((resolve, reject) => {
    const req = index.openCursor(range, "prev");
    let accepted = 0;
    req.onsuccess = () => {
      const cursor = req.result;
      if (!cursor) {
        resolve();
        return;
      }
      const event = cursor.value;
      const createdAt = Number(event.created_at) || 0;
      if (since && createdAt < since) {
        resolve();
        return;
      }
      if (!isBeforeFeedCursor(event, beforeCreatedAt, beforeID)) {
        cursor.continue();
        return;
      }
      const id = String(event.id || "").toLowerCase();
      if (id && !seen.has(id)) {
        seen.add(id);
        out.push(event);
        accepted += 1;
      }
      if (accepted >= targetPerCursor) {
        resolve();
        return;
      }
      cursor.continue();
    };
    req.onerror = () => reject(req.error);
  });

  const upperCreatedAt = Number(beforeCreatedAt) > 0 ? Number(beforeCreatedAt) : Number.MAX_SAFE_INTEGER;
  const lowerCreatedAt = Number(since) > 0 ? Number(since) : 0;
  const scans = [];
  for (const kind of normalizedKinds) {
    if (normalizedAuthors) {
      for (const pubkey of normalizedAuthors) {
        scans.push(scan(IDBKeyRange.bound([pubkey, kind, lowerCreatedAt], [pubkey, kind, upperCreatedAt])));
      }
    } else {
      scans.push(scan(IDBKeyRange.bound([kind, lowerCreatedAt], [kind, upperCreatedAt])));
    }
  }
  await Promise.all(scans);
  await transactionDone(tx);
  out.sort((a, b) => {
    const delta = Number(b.created_at) - Number(a.created_at);
    if (delta !== 0) return delta;
    return String(b.id || "").localeCompare(String(a.id || ""));
  });
  return out.slice(0, sampleLimit);
}

const HASHTAG_TIMELINE_KINDS = [KIND_NOTE, KIND_REPOST, KIND_LONG_FORM];

/** Local hashtag timeline (mirrors iOS SQLiteEventStore.hashtagEvents: t + ct tags). */
export async function hashtagEvents({
  tag,
  limit = 50,
  authors = null,
  beforeCreatedAt,
  beforeID,
  kinds = HASHTAG_TIMELINE_KINDS,
} = {}) {
  if (desktopEventStoreDisabled()) return [];
  const normalized = normalizeHashtag(tag);
  if (!normalized) return [];
  const kindSet = new Set((kinds || HASHTAG_TIMELINE_KINDS).map((k) => Number(k)));
  const authorSet = authors
    ? new Set((authors || []).map((pk) => String(pk || "").trim().toLowerCase()).filter(Boolean))
    : null;
  if (authorSet && !authorSet.size) return [];

  const lookup = normalized.toLowerCase();
  const tagQueries = [...kindSet].flatMap((kind) => [
    eventsByTag("t", lookup, { kind, limit: limit * 2 }),
    eventsByTag("ct", lookup, { kind, limit: limit * 2 }),
  ]);
  const rows = await Promise.all(tagQueries);
  const [tRows, ctRows] = rows.reduce(
    (acc, list, index) => {
      acc[index % 2].push(...list);
      return acc;
    },
    [[], []],
  );
  const merged = dedupeEventsByID([...tRows, ...ctRows]);
  const filtered = merged.filter((event) => {
    if (!kindSet.has(Number(event.kind))) return false;
    if (!eventHasHashtag(event, normalized)) return false;
    const pk = String(event.pubkey || "").toLowerCase();
    if (authorSet && !authorSet.has(pk)) return false;
    return isBeforeFeedCursor(event, beforeCreatedAt, beforeID);
  });
  return sortEventsNewestFirst(filtered).slice(0, limit);
}

/**
 * Read timeline notes from IndexedDB ordered newest-first with stable (created_at, id) cursors.
 * Mirrors iOS SQLiteEventStore.recentEvents.
 */
export async function recentTimelineEvents({
  kinds = [],
  authors = null,
  limit = 50,
  beforeCreatedAt,
  beforeID,
  since,
} = {}) {
  if (desktopEventStoreDisabled()) return [];
  if (authors && !authors.length) return [];
  const kindSet = new Set((kinds || []).map((kind) => Number(kind)));
  if (!kindSet.size) return [];
  const authorSet = authors
    ? new Set((authors || []).map((pk) => String(pk || "").trim().toLowerCase()).filter(Boolean))
    : null;
  const sampleLimit = Math.max(1, Number(limit) || 50);

  return indexedTimelineEvents({
    kinds: [...kindSet],
    authors: authorSet ? [...authorSet] : null,
    limit: sampleLimit,
    beforeCreatedAt,
    beforeID,
    since,
  });
}

export async function eventsByAuthors(authors, {
  kinds = [],
  limit = 50,
  since,
  until,
} = {}) {
  if (desktopEventStoreDisabled()) return [];
  const pubkeys = (authors || []).map((pk) => String(pk || "").trim().toLowerCase()).filter(Boolean);
  if (!pubkeys.length) return [];
  const queryKinds = kinds?.length ? kinds : [KIND_NOTE, KIND_REPOST, KIND_LONG_FORM, KIND_REACTION];
  return indexedTimelineEvents({
    authors: pubkeys,
    kinds: queryKinds,
    limit,
    since,
    beforeCreatedAt: until,
  });
}

function rootIDFromReplyTags(event) {
  for (const tag of event?.tags || []) {
    if (!Array.isArray(tag) || tag.length < 2 || tag[0] !== "e") continue;
    const marker = tag.length >= 4 ? String(tag[3] || "") : "";
    if (marker === "root") return canonicalHex64(tag[1]);
  }
  const eTags = (event?.tags || []).filter((tag) => Array.isArray(tag) && tag[0] === "e");
  if (eTags.length) return canonicalHex64(eTags[0][1]);
  return "";
}

/** Windowed direct reply counts keyed by root note id. */
export async function replyCounts(noteIDs, { sinceCreatedAt } = {}) {
  return countTagReferences(noteIDs, KIND_NOTE, { sinceCreatedAt });
}

/** Reaction totals keyed by note id (reactions in store referencing #e). */
export async function reactionTotals(noteIDs, { sinceCreatedAt } = {}) {
  return countTagReferences(noteIDs, KIND_REACTION, { sinceCreatedAt });
}

/** Zap totals keyed by note id (sum of zap receipt sats). */
export async function zapTotals(noteIDs, { sinceCreatedAt } = {}) {
  const totals = new Map();
  const ids = [...new Set((noteIDs || []).map(canonicalHex64).filter(Boolean))];
  ids.forEach((id) => totals.set(id, 0));
  if (desktopEventStoreDisabled()) return totals;
  if (!ids.length) return totals;
  const lowerBound = Number(sinceCreatedAt) > 0 ? Number(sinceCreatedAt) : 0;
  await Promise.all(ids.map(async (id) => {
    const receipts = await eventsByTag("e", id, { kind: KIND_ZAP_RECEIPT, limit: 500 }).catch(() => []);
    let sum = 0;
    receipts.forEach((receipt) => {
      if (zapTargetNoteID(receipt) !== id) return;
      if (lowerBound > 0 && Number(receipt?.created_at || 0) < lowerBound) return;
      sum += Math.max(0, Number(zapAmountSats(receipt) || 0));
    });
    totals.set(id, sum);
  }));
  return totals;
}

async function countTagReferences(noteIDs, kind, { sinceCreatedAt } = {}) {
  const totals = new Map();
  for (const raw of noteIDs || []) {
    const id = canonicalHex64(raw);
    if (id) totals.set(id, 0);
  }
  if (!totals.size) return totals;
  if (desktopEventStoreDisabled()) return totals;

  const db = await openClientDB();
  const tx = db.transaction(TAG_INDEX, "readonly");
  const index = tx.objectStore(TAG_INDEX).index("tag_value_kind_created_at");
  const lowerCreatedAt = Number(sinceCreatedAt) > 0 ? Number(sinceCreatedAt) : 0;
  const requests = [...totals.keys()].map((noteID) => new Promise((resolve, reject) => {
    const range = IDBKeyRange.bound(["e", noteID, Number(kind), lowerCreatedAt], ["e", noteID, Number(kind), Number.MAX_SAFE_INTEGER]);
    const req = index.count(range);
    req.onsuccess = () => {
      totals.set(noteID, Number(req.result) || 0);
      resolve();
    };
    req.onerror = () => reject(req.error);
  }));
  await Promise.all(requests);
  await transactionDone(tx);
  return totals;
}

async function scoreTrendingCandidates(events, sinceCreatedAt) {
  const ids = events.map((event) => event.id).filter(Boolean);
  const [replies, reactions] = await Promise.all([
    replyCounts(ids, { sinceCreatedAt }),
    reactionTotals(ids, { sinceCreatedAt }),
  ]);
  return events.map((event) => {
    const id = String(event.id || "").toLowerCase();
    const score = (replies.get(id) || 0) + (reactions.get(id) || 0);
    return { event, score };
  });
}

/**
 * Local engagement-ranked trending (mirrors iOS SQLiteEventStore.trendingEvents).
 */
export async function localTrendingEvents({
  since,
  authors = null,
  kinds = [KIND_NOTE, KIND_LONG_FORM],
  limit = 50,
  beforeCreatedAt,
  beforeID,
  kindFilter = null,
} = {}) {
  if (desktopEventStoreDisabled()) return [];
  const kindSet = new Set(
    (kindFilter != null ? [kindFilter] : kinds || [KIND_NOTE, KIND_LONG_FORM]).map((k) => Number(k)),
  );
  if (!kindSet.size) return [];

  const authorSet = authors
    ? new Set((authors || []).map((pk) => String(pk || "").trim().toLowerCase()).filter(Boolean))
    : null;

  const candidates = (await indexedTimelineEvents({
    kinds: [...kindSet],
    authors: authorSet ? [...authorSet] : null,
    limit: Math.max(limit * 4, 80),
    beforeCreatedAt,
    beforeID,
    since,
  })).filter((event) => Number(event.kind) !== KIND_NOTE || !rootIDFromReplyTags(event));

  const scored = await scoreTrendingCandidates(candidates, since);
  scored.sort((a, b) => {
    if (b.score !== a.score) return b.score - a.score;
    const delta = Number(b.event.created_at) - Number(a.event.created_at);
    if (delta !== 0) return delta;
    return String(b.event.id).localeCompare(String(a.event.id));
  });
  return scored.slice(0, limit).map((row) => row.event);
}

/** Search cached events (kinds 1, 6, 30023) by tokenized substring match. */
export async function searchLocalEvents(query, {
  limit = 50,
  kinds = [1, 6, 30023],
  beforeCreatedAt,
  beforeID,
} = {}) {
  if (desktopEventStoreDisabled()) return [];
  const q = String(query || "").trim().toLowerCase();
  if (!q) return [];
  const tokens = q.split(/\s+/).filter(Boolean);
  const kindSet = new Set((kinds || []).map((k) => Number(k)));
  const db = await openClientDB();
  const tx = db.transaction(EVENTS, "readonly");
  const store = tx.objectStore(EVENTS);
  const out = [];
  const req = store.openCursor();
  await new Promise((resolve, reject) => {
    req.onsuccess = () => {
      const cursor = req.result;
      if (!cursor) {
        resolve();
        return;
      }
      const event = cursor.value;
      if (!kindSet.has(Number(event.kind))) {
        cursor.continue();
        return;
      }
      if (!isBeforeFeedCursor(event, beforeCreatedAt, beforeID)) {
        cursor.continue();
        return;
      }
      const haystack = String(event.content || "").toLowerCase();
      if (tokens.every((token) => haystack.includes(token))) {
        out.push(event);
      }
      if (out.length >= limit * 3) {
        resolve();
        return;
      }
      cursor.continue();
    };
    req.onerror = () => reject(req.error);
  });
  await transactionDone(tx);
  out.sort((a, b) => Number(b.created_at) - Number(a.created_at));
  return out.slice(0, limit);
}

export async function searchLocalNotes(query, options = {}) {
  return searchLocalEvents(query, { ...options, kinds: [KIND_NOTE] });
}

export async function searchLocalProfiles(query, { limit = 20 } = {}) {
  if (desktopEventStoreDisabled()) return [];
  const trimmed = String(query || "").trim();
  const max = Math.max(1, Number(limit) || 20);
  if (!trimmed) return [];
  const exactPubkey = canonicalHex64(trimmed);
  const lowered = trimmed.toLowerCase();
  const prefix = lowered;
  const events = await indexedTimelineEvents({ kinds: [KIND_PROFILE], limit: Math.max(max * 6, 60) });
  const ranked = events.map((event) => {
    const pubkey = String(event?.pubkey || "").trim().toLowerCase();
    const profile = parseProfile(pubkey, event);
    const fields = [
      pubkey,
      displayName(profile),
      String(profile?.name || ""),
      String(profile?.display_name || ""),
      String(profile?.nip05 || ""),
    ].map((value) => String(value || "").trim().toLowerCase());
    const contains = fields.some((value) => value.includes(lowered));
    if (!contains && exactPubkey !== pubkey) {
      return null;
    }
    let rank = 2;
    if (exactPubkey && pubkey === exactPubkey) {
      rank = 0;
    } else if (fields.some((value) => value.startsWith(prefix))) {
      rank = 1;
    }
    return { pubkey, event, rank };
  }).filter(Boolean);
  ranked.sort((a, b) => {
    if (a.rank !== b.rank) return a.rank - b.rank;
    const createdDelta = Number(b.event?.created_at || 0) - Number(a.event?.created_at || 0);
    if (createdDelta !== 0) return createdDelta;
    return String(a.pubkey).localeCompare(String(b.pubkey));
  });
  return ranked.slice(0, max).map((row) => row.pubkey);
}

export async function longFormEvents({ limit = 50, beforeCreatedAt, beforeID, since } = {}) {
  return recentTimelineEvents({
    kinds: [KIND_LONG_FORM],
    limit,
    beforeCreatedAt,
    beforeID,
    since,
  });
}

export async function clearEventStore() {
  const db = await openClientDB();
  const tx = db.transaction([EVENTS, TAG_INDEX], "readwrite");
  tx.objectStore(EVENTS).clear();
  tx.objectStore(TAG_INDEX).clear();
  await transactionDone(tx);
}
