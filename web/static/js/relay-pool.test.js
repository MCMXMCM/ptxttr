import assert from "node:assert/strict";
import { afterEach, describe, it } from "node:test";

import { cancelActiveRelayReads, closeRelayPool, relayFetch, relayPublish } from "./relay-pool.js";
import { resetQueryClientForTests } from "./query-client.js";

afterEach(() => {
  closeRelayPool();
  resetQueryClientForTests();
});

function mockPool({ query, event } = {}) {
  return {
    relay(relayURL) {
      return {
        query(filters, options = {}) {
          return query?.(relayURL, filters, options);
        },
        event(evt, options = {}) {
          return event?.(relayURL, evt, options);
        },
      };
    },
  };
}

function withBrowserGlobals(globals, fn) {
  const previous = {
    window: globalThis.window,
    document: globalThis.document,
    navigator: globalThis.navigator,
    localStorage: globalThis.localStorage,
    matchMedia: globalThis.matchMedia,
  };
  Object.entries(globals).forEach(([key, value]) => {
    Object.defineProperty(globalThis, key, {
      configurable: true,
      value,
    });
  });
  try {
    return fn();
  } finally {
    Object.entries(previous).forEach(([key, value]) => {
      if (value === undefined) {
        delete globalThis[key];
      } else {
        Object.defineProperty(globalThis, key, {
          configurable: true,
          value,
        });
      }
    });
  }
}

describe("relayFetch batching", () => {
  it("coalesces same-tick single-id lookups into one query", async () => {
    const calls = [];
    const poolOverride = mockPool({
      async query(_relay, filters) {
        const [filter] = filters;
        calls.push(filter);
        return (filter.ids || []).map((id) => ({ id, kind: 1, pubkey: `pk-${id}` }));
      },
    });

    const [first, second] = await Promise.all([
      relayFetch(["wss://relay.example"], [{ ids: ["aa"], limit: 1 }], { poolOverride }),
      relayFetch(["wss://relay.example"], [{ ids: ["bb"], limit: 1 }], { poolOverride }),
    ]);

    assert.equal(calls.length, 1);
    assert.deepEqual(calls[0], { ids: ["aa", "bb"], limit: 2 });
    assert.deepEqual(first.map((event) => event.id), ["aa"]);
    assert.deepEqual(second.map((event) => event.id), ["bb"]);
  });

  it("coalesces same-author e-tag lookups into one query", async () => {
    const calls = [];
    const poolOverride = mockPool({
      async query(_relay, filters) {
        const [filter] = filters;
        calls.push(filter);
        return [
          { id: "r1", kind: 7, pubkey: "viewer", tags: [["e", "note-a"]] },
          { id: "r2", kind: 7, pubkey: "viewer", tags: [["e", "note-b"]] },
        ];
      },
    });

    const [first, second] = await Promise.all([
      relayFetch(["wss://relay.example"], [{ kinds: [7], authors: ["viewer"], "#e": ["note-a"], limit: 1 }], { poolOverride }),
      relayFetch(["wss://relay.example"], [{ kinds: [7], authors: ["viewer"], "#e": ["note-b"], limit: 1 }], { poolOverride }),
    ]);

    assert.equal(calls.length, 1);
    assert.deepEqual(calls[0], { kinds: [7], authors: ["viewer"], "#e": ["note-a", "note-b"], limit: 2 });
    assert.deepEqual(first.map((event) => event.id), ["r1"]);
    assert.deepEqual(second.map((event) => event.id), ["r2"]);
  });

  it("closes the underlying subscription when a relay fetch times out", async () => {
    let closeCalls = 0;
    const poolOverride = mockPool({
      query(_relay, _filters, { signal }) {
        return new Promise((resolve, reject) => {
          if (signal?.aborted) {
            closeCalls += 1;
            reject(signal.reason);
            return;
          }
          signal?.addEventListener("abort", () => {
            closeCalls += 1;
            reject(signal.reason);
          }, { once: true });
          void resolve;
        });
      },
    });

    const events = await relayFetch(["wss://relay.example"], [{ ids: ["aa"], limit: 1 }], {
      poolOverride,
      timeoutMs: 1,
    });

    assert.deepEqual(events, []);
    assert.equal(closeCalls, 1);
  });

  it("uses one subscription per relay for multi-filter fetches", async () => {
    const calls = [];
    const poolOverride = mockPool({
      async query(_relay, filters) {
        calls.push(filters);
        return [
          { id: "one", kind: 1, pubkey: "pk-one" },
          { id: "two", kind: 1, pubkey: "pk-two" },
        ];
      },
    });

    const events = await relayFetch(
      ["wss://relay.example"],
      [
        { ids: ["one"], limit: 1 },
        { ids: ["two"], limit: 1 },
      ],
      { poolOverride },
    );

    assert.equal(calls.length, 1);
    assert.deepEqual(calls[0], [
      { ids: ["one"], limit: 1 },
      { ids: ["two"], limit: 1 },
    ]);
    assert.deepEqual(events.map((event) => event.id), ["one", "two"]);
  });

  it("caps concurrent reads per relay and queues overflow", async () => {
    await withBrowserGlobals(
      {
        window: { setTimeout, clearTimeout },
        document: { visibilityState: "visible" },
        navigator: { userAgent: "Mozilla/5.0 Chrome/126.0.0.0 Safari/537.36", connection: {}, maxTouchPoints: 0 },
        localStorage: { getItem: () => "full" },
        matchMedia: () => ({ matches: false }),
      },
      async () => {
        let active = 0;
        let maxActive = 0;
        let callCount = 0;
        const poolOverride = mockPool({
          query(_relay, filters) {
            callCount += 1;
            active += 1;
            maxActive = Math.max(maxActive, active);
            const [first] = filters;
            return new Promise((resolve) => {
              setTimeout(() => {
                active -= 1;
                resolve([{ id: first.ids?.[0] || `event-${callCount}`, kind: 1, pubkey: `pk-${callCount}` }]);
              }, 20);
            });
          },
        });

        const fetches = [
          relayFetch(["wss://relay.example"], [{ ids: ["aa"], limit: 1 }, { ids: ["ab"], limit: 1 }], { poolOverride }),
          relayFetch(["wss://relay.example"], [{ ids: ["bb"], limit: 1 }, { ids: ["bc"], limit: 1 }], { poolOverride }),
          relayFetch(["wss://relay.example"], [{ ids: ["cc"], limit: 1 }, { ids: ["cd"], limit: 1 }], { poolOverride }),
        ];

        await Promise.all(fetches);

        assert.equal(callCount, 3);
        assert.equal(maxActive, 2);
      },
    );
  });

  it("cancels queued relay reads before they start", async () => {
    await withBrowserGlobals(
      {
        window: { setTimeout, clearTimeout },
        document: { visibilityState: "visible" },
        navigator: { userAgent: "Mozilla/5.0 AppleWebKit/605.1.15 Version/17.0 Safari/605.1.15", connection: {}, maxTouchPoints: 0 },
        localStorage: { getItem: () => "full" },
        matchMedia: () => ({ matches: false }),
      },
      async () => {
        let callCount = 0;
        const resolvers = [];
        const poolOverride = mockPool({
          query(_relay, filters) {
            callCount += 1;
            return new Promise((resolve) => {
              resolvers.push(() => {
                const [first] = filters;
                resolve([{ id: first.ids?.[0] || `event-${callCount}`, kind: 1, pubkey: `pk-${callCount}` }]);
              });
            });
          },
        });

        const thirdController = new AbortController();
        const first = relayFetch(["wss://relay.example"], [{ ids: ["aa"], limit: 1 }, { ids: ["ab"], limit: 1 }], { poolOverride });
        const second = relayFetch(["wss://relay.example"], [{ ids: ["bb"], limit: 1 }, { ids: ["bc"], limit: 1 }], { poolOverride });
        const third = relayFetch(["wss://relay.example"], [{ ids: ["cc"], limit: 1 }, { ids: ["cd"], limit: 1 }], {
          poolOverride,
          signal: thirdController.signal,
        });

        await Promise.resolve();
        assert.equal(callCount, 2);
        thirdController.abort("cancelled");
        resolvers.shift()?.();
        await first;
        await Promise.resolve();
        assert.equal(callCount, 2);
        resolvers.shift()?.();
        await second;
        const thirdResult = await third;
        assert.deepEqual(thirdResult, []);
        assert.equal(callCount, 2);
      },
    );
  });

  it("aborts active relay reads when cancelled", async () => {
    await withBrowserGlobals(
      {
        window: { setTimeout, clearTimeout },
        document: { visibilityState: "visible" },
        navigator: { userAgent: "Mozilla/5.0 Version/17.0 Safari/605.1.15", connection: {}, maxTouchPoints: 0 },
        localStorage: { getItem: () => "full" },
        matchMedia: () => ({ matches: false }),
      },
      async () => {
        let closeCalls = 0;
        let finished = false;
        const poolOverride = mockPool({
          query(_relay, _filters, { signal }) {
            return new Promise((resolve, reject) => {
              const timer = setTimeout(() => {
                finished = true;
                resolve([{ id: "late", kind: 1, pubkey: "pk-late" }]);
              }, 200);
              signal?.addEventListener("abort", () => {
                closeCalls += 1;
                clearTimeout(timer);
                reject(signal.reason);
              }, { once: true });
            });
          },
        });

        const pending = relayFetch(["wss://relay.example"], [{ ids: ["aa"], limit: 1 }], {
          poolOverride,
          timeoutMs: 500,
        });
        await new Promise((resolve) => setTimeout(resolve, 20));
        cancelActiveRelayReads("route changed");
        const events = await pending;

        assert.deepEqual(events, []);
        assert.equal(closeCalls, 1);
        assert.equal(finished, false);
      },
    );
  });

  it("falls back when AbortSignal.any and AbortSignal.timeout are unavailable", async () => {
    await withBrowserGlobals(
      {
        window: { setTimeout, clearTimeout },
        document: { visibilityState: "visible" },
        navigator: { userAgent: "Mozilla/5.0 Version/16.6 Safari/605.1.15", connection: {}, maxTouchPoints: 0 },
        localStorage: { getItem: () => "full" },
        matchMedia: () => ({ matches: false }),
      },
      async () => {
        const originalAny = AbortSignal.any;
        const originalTimeout = AbortSignal.timeout;
        AbortSignal.any = undefined;
        AbortSignal.timeout = undefined;
        try {
          const events = await relayFetch(["wss://relay.example"], [{ ids: ["aa"], limit: 1 }], {
            poolOverride: mockPool({
              async query(_relay, filters) {
                const [filter] = filters;
                return [{ id: filter.ids[0], kind: 1, pubkey: "pk-aa" }];
              },
            }),
            timeoutMs: 25,
          });

          assert.deepEqual(events.map((event) => event.id), ["aa"]);
        } finally {
          AbortSignal.any = originalAny;
          AbortSignal.timeout = originalTimeout;
        }
      },
    );
  });
});

describe("relayPublish", () => {
  it("accepts nostr-tools publish arrays of per-relay promises", async () => {
    const payload = await relayPublish(["wss://relay.example"], { id: "aa", kind: 1, pubkey: "bb" }, {
      poolOverride: mockPool({
        event() {
          return Promise.resolve("ok");
        },
      }),
    });

    assert.equal(payload.accepted, 1);
    assert.equal(payload.rejected, 0);
    assert.equal(payload.relay_stats[0].accepted, true);
    assert.equal(payload.relay_stats[0].message, "ok");
  });

  it("records failures when a relay publish result is not promise-like", async () => {
    const payload = await relayPublish(["wss://relay.example"], { id: "aa", kind: 1, pubkey: "bb" }, {
      poolOverride: mockPool({
        event() {
          return {};
        },
      }),
    });

    assert.equal(payload.accepted, 0);
    assert.equal(payload.rejected, 1);
    assert.match(payload.relay_stats[0].error, /did not return a promise/);
  });
});
