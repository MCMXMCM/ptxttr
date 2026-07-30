import { normalizeRelayList } from "./relay-config.js";
import { effectiveReadRelays } from "./relay-state.js";

export const DB_NAME = "ptxt-nostr";
export const DB_VERSION = 6;

export const STORE_EVENTS = "events";
export const STORE_TAG_INDEX = "tag_index";
export const STORE_PROFILES = "profiles";
export const STORE_ROUTES = "routes";
export const STORE_FEED_PAGES = "feed_pages";
export const STORE_THREAD_BUNDLES = "thread_bundles";
export const STORE_METADATA = "metadata";
export const STORE_FRESHNESS = "freshness";
export const STORE_AVATARS = "avatars";
const OPEN_DB_TIMEOUT_MS = 3000;
const OPEN_DB_RETRY_BACKOFF_MS = 3_000;

let dbPromise = null;
let dbUnavailableUntil = 0;
const subscribers = new Map();
const inFlight = new Map();
const SESSION_KEY = "ptxt_nostr_session";
const FEED_SORT_KEY = "ptxt_feed_sort";
const READS_SORT_KEY = "ptxt_reads_sort";
const WOT_ENABLED_KEY = "ptxt_wot_enabled";
const WOT_DEPTH_KEY = "ptxt_wot_depth";
const WOT_SEED_KEY = "ptxt_wot_seed_pubkey";

export function openClientDB() {
  if (dbPromise) return dbPromise;
  if (dbUnavailableUntil > Date.now()) {
    return Promise.reject(new Error("IndexedDB temporarily unavailable"));
  }
  dbPromise = new Promise((resolve, reject) => {
    if (typeof indexedDB === "undefined") {
      dbUnavailableUntil = Date.now() + OPEN_DB_RETRY_BACKOFF_MS;
      reject(new Error("IndexedDB unavailable"));
      return;
    }
    let settled = false;
    const request = indexedDB.open(DB_NAME, DB_VERSION);
    const timer = globalThis.setTimeout?.(() => {
      if (settled) return;
      settled = true;
      dbPromise = null;
      dbUnavailableUntil = Date.now() + OPEN_DB_RETRY_BACKOFF_MS;
      reject(new Error("IndexedDB open timed out"));
    }, OPEN_DB_TIMEOUT_MS);
    const finish = (fn) => {
      if (settled) return;
      settled = true;
      if (timer) globalThis.clearTimeout?.(timer);
      fn();
    };
    request.onerror = () => finish(() => {
      dbPromise = null;
      reject(request.error || new Error("IndexedDB open failed"));
    });
    request.onblocked = () => finish(() => {
      dbPromise = null;
      reject(new Error("IndexedDB open blocked"));
    });
    request.onupgradeneeded = () => migrateClientDB(request.result, request.transaction);
    request.onsuccess = () => finish(() => {
      const db = request.result;
      dbUnavailableUntil = 0;
      if (db) {
        db.onversionchange = () => {
          try {
            db.close();
          } catch {
            // ignore
          }
          if (dbPromise) dbPromise = null;
        };
      }
      resolve(db);
    });
  });
  return dbPromise;
}

export function resetClientDBForTests() {
  dbPromise = null;
  dbUnavailableUntil = 0;
}

export function isClientDBUnavailableError(error) {
  const message = String(error?.message || "").toLowerCase();
  return message.includes("indexeddb");
}

export function migrateClientDB(db, upgradeTx = null) {
  if (!db.objectStoreNames.contains(STORE_EVENTS)) {
    const store = db.createObjectStore(STORE_EVENTS, { keyPath: "id" });
    store.createIndex("kind", "kind", { unique: false });
    store.createIndex("pubkey", "pubkey", { unique: false });
    store.createIndex("created_at", "created_at", { unique: false });
    store.createIndex("fetched_at", "fetched_at", { unique: false });
    store.createIndex("last_used_at", "last_used_at", { unique: false });
    store.createIndex("pubkey_kind", ["pubkey", "kind"], { unique: false });
    store.createIndex("kind_created_at", ["kind", "created_at"], { unique: false });
    store.createIndex("pubkey_kind_created_at", ["pubkey", "kind", "created_at"], { unique: false });
  }
  const events = objectStoreFromUpgrade(upgradeTx, STORE_EVENTS);
  ensureIndex(events, "kind", "kind");
  ensureIndex(events, "pubkey", "pubkey");
  ensureIndex(events, "created_at", "created_at");
  ensureIndex(events, "fetched_at", "fetched_at");
  ensureIndex(events, "last_used_at", "last_used_at");
  ensureIndex(events, "pubkey_kind", ["pubkey", "kind"]);
  ensureIndex(events, "kind_created_at", ["kind", "created_at"]);
  ensureIndex(events, "pubkey_kind_created_at", ["pubkey", "kind", "created_at"]);

  if (!db.objectStoreNames.contains(STORE_TAG_INDEX)) {
    const tagStore = db.createObjectStore(STORE_TAG_INDEX, { keyPath: "key" });
    tagStore.createIndex("tag_value", ["tag", "value"], { unique: false });
    tagStore.createIndex("tag_value_kind_created_at", ["tag", "value", "kind", "created_at"], { unique: false });
    tagStore.createIndex("event_id", "event_id", { unique: false });
  }
  const tags = objectStoreFromUpgrade(upgradeTx, STORE_TAG_INDEX);
  ensureIndex(tags, "tag_value", ["tag", "value"]);
  ensureIndex(tags, "tag_value_kind_created_at", ["tag", "value", "kind", "created_at"]);
  ensureIndex(tags, "event_id", "event_id");

  createStore(db, upgradeTx, STORE_PROFILES, { keyPath: "pubkey" }, [
    ["updated_at", "updated_at"],
    ["fetched_at", "fetched_at"],
  ]);
  createStore(db, upgradeTx, STORE_ROUTES, { keyPath: "key" }, [
    ["canonical_key", "canonical_key"],
    ["saved_at", "saved_at"],
  ]);
  createStore(db, upgradeTx, STORE_FEED_PAGES, { keyPath: "key" }, [
    ["query_key", "query_key"],
    ["saved_at", "saved_at"],
  ]);
  createStore(db, upgradeTx, STORE_THREAD_BUNDLES, { keyPath: "key" }, [
    ["selected_id", "selected_id"],
    ["root_id", "root_id"],
    ["saved_at", "saved_at"],
  ]);
  createStore(db, upgradeTx, STORE_METADATA, { keyPath: "key" }, [
    ["kind", "kind"],
    ["updated_at", "updated_at"],
  ]);
  createStore(db, upgradeTx, STORE_FRESHNESS, { keyPath: "key" }, [
    ["scope", "scope"],
    ["updated_at", "updated_at"],
  ]);
  createStore(db, upgradeTx, STORE_AVATARS, { keyPath: "url" }, [
    ["saved_at", "saved_at"],
    ["last_used_at", "last_used_at"],
  ]);
}

function objectStoreFromUpgrade(db, name) {
  return db?.objectStore?.(name) || null;
}

function createStore(db, upgradeTx, name, options, indexes = []) {
  const store = db.objectStoreNames.contains(name)
    ? objectStoreFromUpgrade(upgradeTx, name)
    : db.createObjectStore(name, options);
  for (const [indexName, keyPath] of indexes) ensureIndex(store, indexName, keyPath);
  return store;
}

function ensureIndex(store, name, keyPath) {
  if (!store || store.indexNames.contains(name)) return;
  store.createIndex(name, keyPath, { unique: false });
}

export function transactionDone(tx) {
  return new Promise((resolve, reject) => {
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
    tx.onabort = () => reject(tx.error || new Error("transaction aborted"));
  });
}

export function requestResult(request) {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result ?? null);
    request.onerror = () => reject(request.error);
  });
}

export function stableStringify(value) {
  if (Array.isArray(value)) return `[${value.map(stableStringify).join(",")}]`;
  if (value && typeof value === "object") {
    return `{${Object.keys(value)
      .sort()
      .map((key) => `${JSON.stringify(key)}:${stableStringify(value[key])}`)
      .join(",")}}`;
  }
  return JSON.stringify(value ?? null);
}

export function stableHash(value) {
  const input = typeof value === "string" ? value : stableStringify(value);
  let hash = 2166136261;
  for (let i = 0; i < input.length; i += 1) {
    hash ^= input.charCodeAt(i);
    hash = Math.imul(hash, 16777619);
  }
  return (hash >>> 0).toString(36);
}

export function runtimePreferenceKey(overrides = {}) {
  const relays = normalizeRelayList(overrides.relays || effectiveReadRelays());
  const payload = {
    viewer: String(overrides.viewerPubkey ?? readSessionPubkey()),
    relays,
    feedSort: String(overrides.feedSort ?? readStorageString(FEED_SORT_KEY)),
    readsSort: String(overrides.readsSort ?? readStorageString(READS_SORT_KEY)),
    wot: Boolean(overrides.wotEnabled ?? truthyStorage(WOT_ENABLED_KEY)),
    wotDepth: Number(overrides.wotDepth ?? readStorageString(WOT_DEPTH_KEY) ?? 0),
    wotSeed: String(overrides.wotSeed ?? readStorageString(WOT_SEED_KEY)),
  };
  return stableHash(payload);
}

export function makeFeedQueryKey(query = {}) {
  return `feed:${stableHash({
    route: query.route || "feed",
    viewer: query.viewerPubkey || "",
    sort: query.sort || "",
    wot: Boolean(query.wotEnabled),
    wotDepth: Number(query.wotDepth) || 0,
    wotSeed: query.wotSeed || "",
    relays: normalizeRelayList(query.relays || []),
    scope: query.scope || "",
    tag: query.tag || "",
    search: query.search || query.query || "",
  })}`;
}

export function makeThreadQueryKey(selectedID, options = {}) {
  return `thread:${String(selectedID || "").toLowerCase()}:${stableHash({
    relays: normalizeRelayList(options.relays || effectiveReadRelays()),
    wot: Boolean(options.wotEnabled ?? truthyStorage(WOT_ENABLED_KEY)),
    wotDepth: Number(options.wotDepth ?? readStorageString(WOT_DEPTH_KEY) ?? 0),
    wotSeed: String(options.wotSeed ?? readStorageString(WOT_SEED_KEY)),
  })}`;
}

export function makeProfileQueryKey(pubkey, options = {}) {
  return `profile:${String(pubkey || "").toLowerCase()}:${stableHash({
    tab: options.tab || "posts",
    relays: normalizeRelayList(options.relays || effectiveReadRelays()),
  })}`;
}

function readStorageString(key) {
  try {
    return String(localStorage.getItem(key) || "").trim();
  } catch {
    return "";
  }
}

function truthyStorage(key) {
  const raw = readStorageString(key).toLowerCase();
  return raw === "1" || raw === "true" || raw === "on" || raw === "yes";
}


function readSessionPubkey() {
  try {
    const parsed = JSON.parse(localStorage.getItem(SESSION_KEY) || "{}");
    return String(parsed?.pubkey || "").trim().toLowerCase();
  } catch {
    return "";
  }
}

export async function clearLegacyRouteRecords(canonicalKey = "") {
  const key = String(canonicalKey || "").trim();
  const db = await openClientDB();
  if (!key) {
    const tx = db.transaction(STORE_ROUTES, "readwrite");
    tx.objectStore(STORE_ROUTES).clear();
    await transactionDone(tx);
    return 0;
  }
  const readTx = db.transaction(STORE_ROUTES, "readonly");
  const index = readTx.objectStore(STORE_ROUTES).index("canonical_key");
  const rows = await requestResult(index.getAll(IDBKeyRange.only(key)));
  await transactionDone(readTx);
  const snapshots = Array.isArray(rows) ? rows : [];
  if (!snapshots.length) return 0;
  const writeTx = db.transaction(STORE_ROUTES, "readwrite");
  const store = writeTx.objectStore(STORE_ROUTES);
  snapshots.forEach((row) => {
    if (row?.key) store.delete(row.key);
  });
  await transactionDone(writeTx);
  return snapshots.length;
}

export async function loadFeedPage(queryKey, cursor = "") {
  const key = feedPageStorageKey(queryKey, cursor);
  if (!key) return null;
  const db = await openClientDB();
  const tx = db.transaction(STORE_FEED_PAGES, "readonly");
  const record = await requestResult(tx.objectStore(STORE_FEED_PAGES).get(key));
  if (record && queryKey) {
    void import("./query-client.js")
      .then(({ primeQueryDataFromPersisted, queryKeys }) => {
        primeQueryDataFromPersisted(record, queryKeys.feedPage({
          viewerPubkey: record.viewer_pubkey || "",
          sort: record.sort || "",
          relays: record.relays || effectiveReadRelays(),
          since: record.since || 0,
          until: record.until || 0,
          untilID: record.until_id || "",
          limit: record.limit || Math.max(record.note_ids?.length || 0, 1),
        }));
      })
      .catch(() => {});
  }
  return record;
}

export async function saveFeedPage(queryKey, cursor = "", page = {}) {
  const key = feedPageStorageKey(queryKey, cursor);
  if (!key) return null;
  const record = {
    ...page,
    key,
    query_key: queryKey,
    cursor: String(cursor || ""),
    note_ids: (page.note_ids || page.noteIDs || []).map((id) => String(id || "").toLowerCase()).filter(Boolean),
    viewer_pubkey: String(page.viewer_pubkey || page.viewerPubkey || "").toLowerCase(),
    sort: String(page.sort || ""),
    relays: normalizeRelayList(page.relays || [], Number.MAX_SAFE_INTEGER),
    since: Number(page.since) || 0,
    until: Number(page.until) || 0,
    until_id: String(page.until_id || page.untilID || "").toLowerCase(),
    limit: Number(page.limit) || 0,
    saved_at: Date.now(),
  };
  const db = await openClientDB();
  const tx = db.transaction(STORE_FEED_PAGES, "readwrite");
  tx.objectStore(STORE_FEED_PAGES).put(record);
  await transactionDone(tx);
  void pruneSavedRecords(STORE_FEED_PAGES, 80).catch(() => {});
  void import("./query-client.js")
    .then(({ primeQueryDataFromPersisted, queryKeys }) => {
      primeQueryDataFromPersisted(record, queryKeys.feedPage({
        viewerPubkey: record.viewer_pubkey,
        sort: record.sort,
        relays: record.relays,
        since: record.since,
        until: record.until,
        untilID: record.until_id,
        limit: record.limit || Math.max(record.note_ids.length, 1),
      }));
    })
    .catch(() => {});
  notify(queryKey, record);
  return record;
}

function feedPageStorageKey(queryKey, cursor = "") {
  const q = String(queryKey || "");
  return q ? `${q}::${String(cursor || "")}` : "";
}

export async function getThreadBundle(selectedID, options = {}) {
  const key = makeThreadQueryKey(selectedID, options);
  const db = await openClientDB();
  const tx = db.transaction(STORE_THREAD_BUNDLES, "readonly");
  const record = await requestResult(tx.objectStore(STORE_THREAD_BUNDLES).get(key));
  if (record?.root) {
    void import("./query-client.js")
      .then(({ primeQueryDataFromPersisted, queryKeys }) => {
        primeQueryDataFromPersisted(record, queryKeys.threadBundle(
          record.root_id,
          record.selected_id,
          normalizeRelayList(options.relays || effectiveReadRelays(), Number.MAX_SAFE_INTEGER),
          false,
        ));
      })
      .catch(() => {});
  }
  return record;
}

export async function saveThreadBundle(selectedID, bundle = {}, options = {}) {
  const key = makeThreadQueryKey(selectedID, options);
  if (!selectedID || !bundle?.root) return null;
  const record = {
    key,
    selected_id: String(selectedID || bundle.selectedID || "").toLowerCase(),
    root_id: String(bundle.rootID || bundle.root?.id || "").toLowerCase(),
    root: bundle.root || null,
    selected: bundle.selected || null,
    events: bundle.events || [],
    parentByID: bundle.parentByID || {},
    relays: normalizeRelayList(options.relays || effectiveReadRelays(), Number.MAX_SAFE_INTEGER),
    force_relay_replies: options.forceRelayReplies === true,
    saved_at: Date.now(),
  };
  const db = await openClientDB();
  const tx = db.transaction(STORE_THREAD_BUNDLES, "readwrite");
  tx.objectStore(STORE_THREAD_BUNDLES).put(record);
  await transactionDone(tx);
  void pruneSavedRecords(STORE_THREAD_BUNDLES, 40).catch(() => {});
  void import("./query-client.js")
    .then(({ primeQueryDataFromPersisted, queryKeys }) => {
      primeQueryDataFromPersisted(record, queryKeys.threadBundle(
        record.root_id,
        record.selected_id,
        record.relays || [],
        record.force_relay_replies === true,
      ));
    })
    .catch(() => {});
  notify(key, record);
  return record;
}

async function pruneSavedRecords(storeName, maxRecords) {
  const limit = Math.max(1, Number(maxRecords) || 1);
  const db = await openClientDB();
  const readTx = db.transaction(storeName, "readonly");
  const rows = await requestResult(readTx.objectStore(storeName).getAll());
  await transactionDone(readTx);
  const records = Array.isArray(rows) ? rows : [];
  if (records.length <= limit) return 0;
  records.sort((a, b) => Number(b?.saved_at || 0) - Number(a?.saved_at || 0));
  const stale = records.slice(limit);
  const writeTx = db.transaction(storeName, "readwrite");
  const store = writeTx.objectStore(storeName);
  stale.forEach((row) => {
    if (row?.key) store.delete(row.key);
  });
  await transactionDone(writeTx);
  return stale.length;
}

export function refreshInBackground(key, task) {
  const k = String(key || "");
  if (!k || typeof task !== "function") return Promise.resolve(null);
  if (inFlight.has(k)) return inFlight.get(k);
  const promise = Promise.resolve()
    .then(task)
    .catch((error) => {
      void markFreshness(k, { last_failed_at: Date.now(), stale_reason: error?.message || "refresh failed" });
      throw error;
    })
    .finally(() => {
      inFlight.delete(k);
    });
  inFlight.set(k, promise);
  return promise;
}

export async function markFreshness(key, patch = {}) {
  const record = {
    key: String(key || ""),
    scope: String(patch.scope || "").trim(),
    updated_at: Date.now(),
    ...patch,
  };
  if (!record.key) return null;
  const db = await openClientDB();
  const tx = db.transaction(STORE_FRESHNESS, "readwrite");
  tx.objectStore(STORE_FRESHNESS).put(record);
  await transactionDone(tx);
  notify(record.key, record);
  return record;
}

export function subscribe(key, callback) {
  const k = String(key || "");
  if (!k || typeof callback !== "function") return () => {};
  if (!subscribers.has(k)) subscribers.set(k, new Set());
  subscribers.get(k).add(callback);
  return () => subscribers.get(k)?.delete(callback);
}

function notify(key, payload) {
  const set = subscribers.get(String(key || ""));
  if (!set?.size) return;
  for (const callback of [...set]) {
    try {
      callback(payload);
    } catch {
      // observer errors should not break cache writes
    }
  }
}
