import { normalizeRelayList } from "./relay-config.js";
import { dedupeEventsByID } from "./relay-utils.js";
import { pageIsHidden, powerLimitedCount, powerLimitedTimeoutMs, powerSaverActive } from "./power-mode.js";
import { putEvents } from "./event-store.js";
import { getNostrPool } from "./nostr-provider.js";
import { fetchCachedQuery, queryKeys } from "./query-client.js";
import { desktopModeEnabled } from "./viewer-defaults.js";

const FETCH_TIMEOUT_MS = 8000;
const PUBLISH_TIMEOUT_MS = 10000;
const SAVER_FETCH_TIMEOUT_MS = 6500;
const SAVER_MAX_FETCH_RELAYS = 6;
const RELAY_IDLE_CLOSE_MS = 15000;
const RELAY_IDLE_SWEEP_MS = 5000;
const RELAY_FETCH_BATCH_SIZE = 50;
const RELAY_FAILURE_WINDOW_MS = 12000;
const RELAY_COOLDOWN_MS = 7000;

let pool = null;
let relayCooldownTimer = 0;
let relayIdleSweepTimer = 0;
const relayFetchBatchCollectors = new Map();
const relayReadQueue = [];
const relayReadActiveByRelay = new Map();
const relayReadCooldowns = new Map();
const relayLastUsedAt = new Map();
const relayLeaseCounts = new Map();
const activeRelayReadControllers = new Set();
let relayReadActiveTotal = 0;

function relayReadLimits() {
  if (powerSaverActive()) {
    return {
      maxGlobal: 3,
      maxPerRelay: 1,
    };
  }
  return {
    maxGlobal: 8,
    maxPerRelay: 2,
  };
}

function relayCooldownMs() {
  return RELAY_COOLDOWN_MS;
}

function relayCoolingUntil(relay) {
  const row = relayReadCooldowns.get(relay);
  if (!row) return 0;
  if (row.until <= Date.now()) {
    relayReadCooldowns.delete(relay);
    return 0;
  }
  return row.until;
}

function scheduleRelayCooldownWake() {
  if (typeof window === "undefined") return;
  window.clearTimeout(relayCooldownTimer);
  relayCooldownTimer = 0;
  let nextWakeAt = 0;
  relayReadCooldowns.forEach((row) => {
    if (!row?.until) return;
    if (!nextWakeAt || row.until < nextWakeAt) nextWakeAt = row.until;
  });
  if (!nextWakeAt) return;
  relayCooldownTimer = window.setTimeout(() => {
    relayCooldownTimer = 0;
    processRelayReadQueue();
  }, Math.max(25, nextWakeAt - Date.now()));
}

function noteRelayReadFailure(relay) {
  const now = Date.now();
  const previous = relayReadCooldowns.get(relay);
  const withinWindow = previous && now - previous.lastFailureAt <= RELAY_FAILURE_WINDOW_MS;
  const failures = withinWindow ? previous.failures + 1 : 1;
  const until = failures >= 2 ? now + relayCooldownMs() : 0;
  relayReadCooldowns.set(relay, { failures, lastFailureAt: now, until });
  scheduleRelayCooldownWake();
}

function noteRelayReadSuccess(relay) {
  relayReadCooldowns.delete(relay);
  scheduleRelayCooldownWake();
}

function cleanupQueuedRelayTask(task) {
  const index = relayReadQueue.indexOf(task);
  if (index >= 0) relayReadQueue.splice(index, 1);
  task.signal?.removeEventListener?.("abort", task.onAbort);
}

function processRelayReadQueue() {
  const limits = relayReadLimits();
  const now = Date.now();
  for (let index = 0; index < relayReadQueue.length; index += 1) {
    if (relayReadActiveTotal >= limits.maxGlobal) break;
    const task = relayReadQueue[index];
    if (!task) continue;
    if (task.signal?.aborted) {
      relayReadQueue.splice(index, 1);
      index -= 1;
      task.signal?.removeEventListener?.("abort", task.onAbort);
      task.reject(abortError(task.signal.reason));
      continue;
    }
    const coolingUntil = relayCoolingUntil(task.relay);
    if (coolingUntil > now) {
      scheduleRelayCooldownWake();
      continue;
    }
    const activeForRelay = relayReadActiveByRelay.get(task.relay) || 0;
    if (activeForRelay >= limits.maxPerRelay) continue;
    relayReadQueue.splice(index, 1);
    index -= 1;
    task.signal?.removeEventListener?.("abort", task.onAbort);
    relayReadActiveTotal += 1;
    relayReadActiveByRelay.set(task.relay, activeForRelay + 1);
    void Promise.resolve()
      .then(task.run)
      .then((value) => {
        noteRelayReadSuccess(task.relay);
        task.resolve(value);
      })
      .catch((error) => {
        noteRelayReadFailure(task.relay);
        task.reject(error);
      })
      .finally(() => {
        relayReadActiveTotal = Math.max(0, relayReadActiveTotal - 1);
        const remaining = Math.max(0, (relayReadActiveByRelay.get(task.relay) || 1) - 1);
        if (remaining > 0) relayReadActiveByRelay.set(task.relay, remaining);
        else relayReadActiveByRelay.delete(task.relay);
        processRelayReadQueue();
      });
  }
}

function scheduleRelayRead(relay, run, signal = null) {
  if (signal?.aborted) return Promise.reject(abortError(signal.reason));
  return new Promise((resolve, reject) => {
    const task = {
      relay,
      run,
      resolve,
      reject,
      signal,
      onAbort() {
        cleanupQueuedRelayTask(task);
        reject(abortError(signal?.reason));
      },
    };
    if (signal) signal.addEventListener("abort", task.onAbort, { once: true });
    relayReadQueue.push(task);
    processRelayReadQueue();
  });
}

function sharedPool() {
  if (!pool) pool = getNostrPool();
  return pool;
}

function noteRelayUsed(relay) {
  relayLastUsedAt.set(relay, Date.now());
  scheduleRelayIdleSweep();
}

function leaseRelay(relay) {
  noteRelayUsed(relay);
  relayLeaseCounts.set(relay, (relayLeaseCounts.get(relay) || 0) + 1);
}

function releaseRelay(relay) {
  const next = Math.max(0, (relayLeaseCounts.get(relay) || 1) - 1);
  if (next > 0) relayLeaseCounts.set(relay, next);
  else relayLeaseCounts.delete(relay);
  noteRelayUsed(relay);
}

function scheduleRelayIdleSweep() {
  if (typeof window === "undefined") return;
  if (relayIdleSweepTimer) return;
  relayIdleSweepTimer = window.setTimeout(() => {
    relayIdleSweepTimer = 0;
    sweepIdleRelays();
  }, RELAY_IDLE_SWEEP_MS);
}

function sweepIdleRelays() {
  if (!pool) return;
  const now = Date.now();
  const idleRelays = [];
  relayLastUsedAt.forEach((lastUsedAt, relay) => {
    if ((relayLeaseCounts.get(relay) || 0) > 0) return;
    if (now - lastUsedAt < RELAY_IDLE_CLOSE_MS) return;
    idleRelays.push(relay);
  });
  if (idleRelays.length) {
    idleRelays.forEach((relay) => {
      try {
        pool?.relay?.(relay)?.close?.();
      } catch {
        // ignore
      }
    });
    idleRelays.forEach((relay) => {
      relayLastUsedAt.delete(relay);
      relayLeaseCounts.delete(relay);
    });
  }
  if (relayLastUsedAt.size) scheduleRelayIdleSweep();
}

function withTimeout(promise, ms, label = "operation") {
  if (!promise || typeof promise.then !== "function") {
    return Promise.reject(new Error(`${label} did not return a promise`));
  }
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`${label} timed out`)), ms);
    promise.then(
      (value) => {
        clearTimeout(timer);
        resolve(value);
      },
      (error) => {
        clearTimeout(timer);
        reject(error);
      },
    );
  });
}

function abortReason(message = "operation aborted") {
  if (typeof DOMException === "function") {
    return new DOMException(message, "AbortError");
  }
  const error = new Error(message);
  error.name = "AbortError";
  return error;
}

function composedAbortSignal(signals = [], timeoutMs = 0) {
  const liveSignals = (signals || []).filter(Boolean);
  const timeout = Math.max(0, Number(timeoutMs) || 0);
  if (typeof AbortSignal?.any === "function" && typeof AbortSignal?.timeout === "function") {
    const timeoutSignal = timeout > 0 ? AbortSignal.timeout(timeout) : null;
    const merged = timeoutSignal ? [...liveSignals, timeoutSignal] : liveSignals;
    return {
      signal: merged.length > 1 ? AbortSignal.any(merged) : (merged[0] || null),
      dispose() {},
    };
  }

  const controller = new AbortController();
  const cleanup = [];
  const setTimer = globalThis.setTimeout?.bind(globalThis);
  const clearTimer = globalThis.clearTimeout?.bind(globalThis);
  const abort = (reason) => {
    if (!controller.signal.aborted) controller.abort(reason || abortReason());
  };

  liveSignals.forEach((signal) => {
    if (signal.aborted) {
      abort(signal.reason);
      return;
    }
    const onAbort = () => abort(signal.reason);
    signal.addEventListener("abort", onAbort, { once: true });
    cleanup.push(() => signal.removeEventListener("abort", onAbort));
  });

  let timer = 0;
  if (!controller.signal.aborted && timeout > 0 && typeof setTimer === "function") {
    timer = setTimer(() => abort(abortReason("signal timed out")), timeout);
  }

  return {
    signal: controller.signal,
    dispose() {
      cleanup.forEach((fn) => fn());
      if (timer && typeof clearTimer === "function") clearTimer(timer);
    },
  };
}

function abortError(reason) {
  if (reason instanceof Error) return reason;
  return new Error(reason ? String(reason) : "operation aborted");
}

function relayClientFor(p, relay) {
  return p?.relay?.(relay) || null;
}

class RelayFetchBatchCollector {
  constructor(executeBatch) {
    this.executeBatch = executeBatch;
    this.pending = [];
    this.scheduled = false;
  }

  request(key) {
    return new Promise((resolve, reject) => {
      this.pending.push({ key, resolve, reject });
      if (this.scheduled) return;
      this.scheduled = true;
      queueMicrotask(() => {
        void this.flush();
      });
    });
  }

  async flush() {
    const batch = this.pending;
    this.pending = [];
    this.scheduled = false;
    if (!batch.length) return;
    const uniqueKeys = [...new Set(batch.map((item) => item.key))];
    try {
      const out = new Map();
      for (let index = 0; index < uniqueKeys.length; index += RELAY_FETCH_BATCH_SIZE) {
        const chunk = uniqueKeys.slice(index, index + RELAY_FETCH_BATCH_SIZE);
        const rows = await this.executeBatch(chunk);
        rows.forEach((value, key) => out.set(key, value));
      }
      batch.forEach(({ key, resolve }) => resolve(out.get(key) || []));
    } catch (error) {
      batch.forEach(({ reject }) => reject(error));
    }
  }
}

function filterKeys(filter = {}) {
  return Object.keys(filter).sort();
}

function isIDsOnlyFilter(filter = {}) {
  const keys = filterKeys(filter);
  return keys.every((key) => key === "ids" || key === "limit")
    && Array.isArray(filter.ids)
    && filter.ids.length === 1;
}

function isReplaceableFilter(filter = {}) {
  const keys = filterKeys(filter);
  return keys.every((key) => key === "authors" || key === "kinds" || key === "limit")
    && Array.isArray(filter.authors)
    && filter.authors.length === 1
    && Array.isArray(filter.kinds)
    && filter.kinds.length === 1;
}

function isSingleETagFilter(filter = {}) {
  const keys = filterKeys(filter);
  return keys.every((key) => key === "#e" || key === "kinds" || key === "limit")
    && !filter.authors
    && Array.isArray(filter.kinds)
    && filter.kinds.length > 0
    && Array.isArray(filter["#e"])
    && filter["#e"].length === 1;
}

function isSingleAuthorETagFilter(filter = {}) {
  const keys = filterKeys(filter);
  return keys.every((key) => key === "#e" || key === "authors" || key === "kinds" || key === "limit")
    && Array.isArray(filter.authors)
    && filter.authors.length === 1
    && Array.isArray(filter.kinds)
    && filter.kinds.length > 0
    && Array.isArray(filter["#e"])
    && filter["#e"].length === 1;
}

function relaySetKey(relays = []) {
  return normalizeRelayList(relays).join("|");
}

async function queryRelayFilters(p, relay, filters, signal) {
  const client = relayClientFor(p, relay);
  if (typeof client?.query !== "function") {
    throw new Error(`relay ${relay} does not expose query()`);
  }
  return client.query(filters, { signal, relays: [relay] });
}

async function querySyncPerRelay(p, normalized, filters, effectiveTimeout, signal = null) {
  const tasks = normalized.map(async (relay) => {
    const relayController = new AbortController();
    activeRelayReadControllers.add(relayController);
    const relayRequest = composedAbortSignal(
      signal ? [signal, relayController.signal] : [relayController.signal],
      effectiveTimeout,
    );
    try {
      noteRelayUsed(relay);
      return await scheduleRelayRead(
        relay,
        async () => {
          leaseRelay(relay);
          const scopedFilters =
            powerSaverActive() && filters.length > 2
              ? filters.slice(0, 2)
              : filters;
          try {
            return await queryRelayFilters(p, relay, scopedFilters, relayRequest.signal);
          } finally {
            releaseRelay(relay);
          }
        },
        relayRequest.signal,
      );
    } catch {
      return [];
    } finally {
      relayRequest.dispose();
      activeRelayReadControllers.delete(relayController);
    }
  });
  const batches = await Promise.all(tasks);
  return dedupeEventsByID(batches.flat());
}

function eventsMatchingTag(events = [], tagName, value) {
  return events.filter((event) => (event.tags || []).some((tag) => Array.isArray(tag) && tag[0] === tagName && tag[1] === value));
}

function latestEventByAuthor(events = [], author = "") {
  return events
    .filter((event) => event.pubkey === author)
    .sort((left, right) => Number(right.created_at || 0) - Number(left.created_at || 0))[0];
}

function collectorForKey(key, executeBatch) {
  let collector = relayFetchBatchCollectors.get(key);
  if (!collector) {
    collector = new RelayFetchBatchCollector(executeBatch);
    relayFetchBatchCollectors.set(key, collector);
  }
  return collector;
}

async function maybeRelayFetchBatched(p, normalized, filter, effectiveTimeout) {
  const relaysKey = relaySetKey(normalized);
  if (!relaysKey) return null;

  if (isIDsOnlyFilter(filter)) {
    const collector = collectorForKey(`ids:${relaysKey}`, async (ids) => {
      const events = await querySyncPerRelay(p, normalized, [{ ids, limit: ids.length }], effectiveTimeout);
      const out = new Map();
      ids.forEach((id) => {
        out.set(id, events.filter((event) => event.id === id));
      });
      return out;
    });
    return collector.request(filter.ids[0]);
  }

  if (isReplaceableFilter(filter)) {
    const kind = filter.kinds[0];
    const limit = Math.max(1, Number(filter.limit) || 1);
    const collector = collectorForKey(`replaceable:${relaysKey}:${kind}:${limit}`, async (authors) => {
      const events = await querySyncPerRelay(
        p,
        normalized,
        [{ authors, kinds: [kind], limit: authors.length * limit }],
        effectiveTimeout,
      );
      const out = new Map();
      authors.forEach((author) => {
        const event = latestEventByAuthor(events, author);
        out.set(author, event ? [event] : []);
      });
      return out;
    });
    return collector.request(filter.authors[0]);
  }

  if (isSingleETagFilter(filter)) {
    const kindsKey = [...filter.kinds].sort((left, right) => left - right).join(",");
    const limit = Math.max(1, Number(filter.limit) || 1);
    const collector = collectorForKey(`etag:${relaysKey}:${kindsKey}:${limit}`, async (ids) => {
      const events = await querySyncPerRelay(
        p,
        normalized,
        [{ kinds: filter.kinds, "#e": ids, limit: ids.length * limit }],
        effectiveTimeout,
      );
      const out = new Map();
      ids.forEach((id) => {
        out.set(id, eventsMatchingTag(events, "e", id));
      });
      return out;
    });
    return collector.request(filter["#e"][0]);
  }

  if (isSingleAuthorETagFilter(filter)) {
    const author = filter.authors[0];
    const kindsKey = [...filter.kinds].sort((left, right) => left - right).join(",");
    const limit = Math.max(1, Number(filter.limit) || 1);
    const collector = collectorForKey(`author-etag:${relaysKey}:${author}:${kindsKey}:${limit}`, async (ids) => {
      const events = await querySyncPerRelay(
        p,
        normalized,
        [{ authors: [author], kinds: filter.kinds, "#e": ids, limit: ids.length * limit }],
        effectiveTimeout,
      );
      const out = new Map();
      ids.forEach((id) => {
        out.set(id, eventsMatchingTag(events, "e", id));
      });
      return out;
    });
    return collector.request(filter["#e"][0]);
  }

  return null;
}

/**
 * Fetch events from relays (parallel fan-out, dedupe by id).
 * Mirrors iOS WebSocketRelayClient.fetch(from:filters:).
 */
export async function relayFetch(relays, filters, { timeoutMs = FETCH_TIMEOUT_MS, poolOverride = null, signal = null } = {}) {
  if (pageIsHidden()) return [];
  const normalized = normalizeRelayList(relays, powerLimitedCount(undefined, SAVER_MAX_FETCH_RELAYS));
  if (!normalized.length || !filters?.length) return [];
  const list = Array.isArray(filters) ? filters : [filters];
  const effectiveTimeout = powerLimitedTimeoutMs(timeoutMs, Math.min(timeoutMs, SAVER_FETCH_TIMEOUT_MS));
  return fetchCachedQuery({
    queryKey: queryKeys.relayFetch(normalized, list),
    staleTime: Math.max(1_000, Math.min(effectiveTimeout, 15_000)),
    queryFn: async () => {
      if (desktopModeEnabled()) {
        const composed = composedAbortSignal([signal], effectiveTimeout + 2_000);
        try {
          const response = await fetch("/__ptxt/desktop/relay-fetch", {
            method: "POST",
            headers: { "Content-Type": "application/json", Accept: "application/json" },
            body: JSON.stringify({
              relays: normalized,
              filters: list,
              timeout_ms: effectiveTimeout,
            }),
            signal: composed.signal || undefined,
          });
          if (!response.ok) throw new Error(`desktop relay fetch failed (${response.status})`);
          const events = await response.json();
          return Array.isArray(events) ? dedupeEventsByID(events) : [];
        } finally {
          composed.dispose();
        }
      }
      const p = poolOverride || sharedPool();
      let events;
      if (list.length === 1) {
        const batched = await maybeRelayFetchBatched(p, normalized, list[0], effectiveTimeout);
        events = batched ? dedupeEventsByID(batched) : null;
      }
      if (!events) {
        events = await querySyncPerRelay(p, normalized, list, effectiveTimeout, signal);
      }
      if (events?.length) {
        await putEvents(events).catch(() => {});
      }
      return events || [];
    },
  });
}

/**
 * Publish to all relays, waiting for every attempt (iOS parity for DMs).
 * Returns payload compatible with publish-status.js / server publishEventResponse.
 */
export async function relayPublish(relays, event, { onRelayComplete, timeoutMs = PUBLISH_TIMEOUT_MS, poolOverride = null } = {}) {
  const normalized = normalizeRelayList(relays);
  if (desktopModeEnabled()) {
    const composed = composedAbortSignal([], timeoutMs + 2_000);
    try {
      const response = await fetch("/api/events", {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify({ event, relays: normalized }),
        signal: composed.signal || undefined,
      });
      const payload = await response.json().catch(() => ({}));
      (payload?.relay_stats || []).forEach((row) => onRelayComplete?.(row));
      if (!response.ok) {
        const error = new Error(String(payload?.error || `desktop relay publish failed (${response.status})`));
        error.payload = payload;
        throw error;
      }
      return payload;
    } finally {
      composed.dispose();
    }
  }
  const p = poolOverride || sharedPool();
  const attempts = await Promise.all(
    normalized.map(async (relayURL) => {
      const attempt = { relay_url: relayURL, accepted: false, message: "", error: "" };
      leaseRelay(relayURL);
      try {
        const publishResult = relayClientFor(p, relayURL)?.event?.(event, { relays: [relayURL] });
        const relayPromise = Array.isArray(publishResult) ? publishResult[0] : publishResult;
        await withTimeout(relayPromise, timeoutMs, "relay publish");
        attempt.accepted = true;
        attempt.message = "ok";
      } catch (err) {
        attempt.error = err instanceof Error ? err.message : String(err);
        attempt.message = attempt.error;
      } finally {
        releaseRelay(relayURL);
      }
      onRelayComplete?.(attempt);
      return attempt;
    }),
  );
  const accepted = attempts.filter((row) => row.accepted).length;
  if (accepted > 0) {
    void putEvents([event]).catch(() => {});
  }
  return {
    event_id: event.id,
    kind: event.kind,
    pubkey: event.pubkey,
    accepted,
    rejected: attempts.length - accepted,
    persisted: false,
    planned_relays: normalized,
    relay_stats: attempts,
  };
}

export function cancelActiveRelayReads(reason = "relay reads cancelled") {
  activeRelayReadControllers.forEach((controller) => controller.abort(reason));
  relayReadQueue.splice(0).forEach((task) => {
    task.signal?.removeEventListener?.("abort", task.onAbort);
    task.reject(abortError(reason));
  });
}

export function closeRelayPool() {
  if (typeof window !== "undefined") {
    window.clearTimeout(relayCooldownTimer);
    relayCooldownTimer = 0;
    window.clearTimeout(relayIdleSweepTimer);
    relayIdleSweepTimer = 0;
  }
  cancelActiveRelayReads("relay pool closed");
  try {
    pool?.close?.();
  } catch {
    // ignore
  }
  pool = null;
  relayFetchBatchCollectors.clear();
  relayReadActiveByRelay.clear();
  relayReadCooldowns.clear();
  relayLastUsedAt.clear();
  relayLeaseCounts.clear();
  relayReadActiveTotal = 0;
}

if (typeof document !== "undefined") {
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "hidden") closeRelayPool();
  });
}
