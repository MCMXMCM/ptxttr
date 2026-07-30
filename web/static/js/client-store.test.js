import assert from "node:assert/strict";
import { afterEach, describe, it } from "node:test";

import {
  clearLegacyRouteRecords,
  DB_VERSION,
  makeFeedQueryKey,
  openClientDB,
  makeProfileQueryKey,
  makeThreadQueryKey,
  resetClientDBForTests,
  stableHash,
  stableStringify,
} from "./client-store.js";

afterEach(() => {
  resetClientDBForTests();
  delete globalThis.indexedDB;
});

describe("client-store keys", () => {
  it("uses the indexed event-store schema version", () => {
    assert.equal(DB_VERSION, 6);
  });

  it("stableStringify ignores object insertion order", () => {
    assert.equal(
      stableStringify({ b: 2, a: { d: 4, c: 3 } }),
      stableStringify({ a: { c: 3, d: 4 }, b: 2 }),
    );
    assert.equal(stableHash({ b: 2, a: 1 }), stableHash({ a: 1, b: 2 }));
  });

  it("feed query keys are stable and preference-sensitive", () => {
    const base = {
      route: "feed",
      viewerPubkey: "aa".repeat(32),
      sort: "recent",
      wotEnabled: true,
      wotDepth: 2,
      wotSeed: "seed",
      relays: ["wss://relay.damus.io/", "wss://relay.primal.net"],
    };
    assert.equal(makeFeedQueryKey(base), makeFeedQueryKey({ ...base }));
    assert.notEqual(makeFeedQueryKey(base), makeFeedQueryKey({ ...base, sort: "trend24h" }));
    assert.notEqual(makeFeedQueryKey(base), makeFeedQueryKey({ ...base, wotDepth: 3 }));
  });

  it("thread and profile keys include route-specific identity", () => {
    const id = "bb".repeat(32);
    assert.equal(makeThreadQueryKey(id, { relays: ["wss://nos.lol/"] }), makeThreadQueryKey(id, { relays: ["wss://nos.lol"] }));
    assert.notEqual(makeThreadQueryKey(id), makeThreadQueryKey("cc".repeat(32)));
    assert.notEqual(makeProfileQueryKey("aa".repeat(32), { tab: "posts" }), makeProfileQueryKey("aa".repeat(32), { tab: "media" }));
  });

  it("exports a legacy route record clearer", () => {
    assert.equal(typeof clearLegacyRouteRecords, "function");
  });

  it("rejects blocked IndexedDB opens so callers can retry", async () => {
    globalThis.indexedDB = {
      open() {
        const request = {};
        queueMicrotask(() => request.onblocked?.());
        return request;
      },
    };

    await assert.rejects(openClientDB(), /blocked/);
  });

  it("clears failed IndexedDB opens so a later retry can succeed", async () => {
    let attempts = 0;
    const db = { close() {}, onversionchange: null };
    globalThis.indexedDB = {
      open() {
        attempts += 1;
        const request = {};
        queueMicrotask(() => {
          if (attempts === 1) request.onblocked?.();
          else {
            request.result = db;
            request.onsuccess?.();
          }
        });
        return request;
      },
    };

    await assert.rejects(openClientDB(), /blocked/);
    await assert.doesNotReject(openClientDB());
    assert.equal(attempts, 2);
  });

  it("backs off after a timed out IndexedDB open", async () => {
    let attempts = 0;
    globalThis.indexedDB = {
      open() {
        attempts += 1;
        return {};
      },
    };

    await assert.rejects(openClientDB(), /timed out/);
    await assert.rejects(openClientDB(), /temporarily unavailable/);
    assert.equal(attempts, 1);
  });
});
