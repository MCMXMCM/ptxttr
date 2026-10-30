import assert from "node:assert/strict";
import { afterEach, describe, it } from "node:test";

import { setAppBootstrapForTests } from "./app/bootstrap.js";

/** Page visibility the relay pool follows; it starts listening when the module loads. */
const page = new (class extends EventTarget {
  visibilityState = "visible";
})();
globalThis.document = page;

const { cancelActiveRelayReads, closeRelayPool, relayFetch, relayPublish } = await import("./relay-pool.js");
const { resetQueryClientForTests } = await import("./query-client.js");

function setPageVisibility(state) {
  page.visibilityState = state;
  page.dispatchEvent(new Event("visibilitychange"));
}

afterEach(() => {
  if (page.visibilityState !== "visible") setPageVisibility("visible");
  closeRelayPool();
  resetQueryClientForTests();
  setAppBootstrapForTests({ features: { localFirst: false } });
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

async function withBrowserGlobals(globals, fn) {
  const previous = {
    window: globalThis.window,
    document: globalThis.document,
    navigator: globalThis.navigator,
    localStorage: globalThis.localStorage,
    matchMedia: globalThis.matchMedia,
    fetch: globalThis.fetch,
  };
  Object.entries(globals).forEach(([key, value]) => {
    Object.defineProperty(globalThis, key, {
      configurable: true,
      value,
    });
  });
  try {
    return await fn();
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
  it("routes desktop relay reads through the authenticated sidecar", async () => {
    const requests = [];
    setAppBootstrapForTests({ features: { localFirst: true, desktopShell: true } });
    const events = await withBrowserGlobals({
      document: {
        visibilityState: "visible",
        documentElement: { dataset: { ptxtDesktopMode: "1" } },
      },
      fetch: async (url, init) => {
        requests.push({ url, init });
        return new Response(JSON.stringify([{ id: "sidecar-event", pubkey: "aa", kind: 1, created_at: 1 }]), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      },
    }, () => relayFetch(["wss://relay.example"], [{ kinds: [1], limit: 10 }]));

    assert.deepEqual(events.map((event) => event.id), ["sidecar-event"]);
    assert.equal(requests.length, 1);
    assert.equal(requests[0].url, "/__ptxt/desktop/relay-fetch");
    assert.deepEqual(JSON.parse(requests[0].init.body), {
      relays: ["wss://relay.example"],
      filters: [{ kinds: [1], limit: 10 }],
      timeout_ms: 8000,
    });
  });

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

  it("reuses a complete read for a repeat call", async () => {
    let calls = 0;
    const poolOverride = mockPool({
      async query(_relay, filters) {
        calls += 1;
        return [{ id: filters[0].ids[0], kind: 1, pubkey: "pk-aa" }];
      },
    });

    await relayFetch(["wss://relay.example"], [{ ids: ["aa"], limit: 1 }], { poolOverride });
    const repeat = await relayFetch(["wss://relay.example"], [{ ids: ["aa"], limit: 1 }], { poolOverride });

    assert.deepEqual(repeat.map((event) => event.id), ["aa"]);
    assert.equal(calls, 1);
  });

  it("reads the relays again when the cache is skipped", async () => {
    let calls = 0;
    const poolOverride = mockPool({
      async query(_relay, filters) {
        calls += 1;
        return [{ id: `list-${calls}`, kind: 3, pubkey: filters[0].authors[0], created_at: calls }];
      },
    });
    const filters = [{ authors: ["viewer"], kinds: [3], limit: 3 }];

    const cached = await relayFetch(["wss://relay.example"], filters, { poolOverride });
    const fresh = await relayFetch(["wss://relay.example"], filters, { poolOverride, cache: false });

    assert.deepEqual(cached.map((event) => event.id), ["list-1"]);
    assert.deepEqual(fresh.map((event) => event.id), ["list-2"]);
    assert.equal(calls, 2);
  });

  it("does not cache a read that failed on one of its relays", async () => {
    const calls = [];
    const poolOverride = mockPool({
      async query(relay, filters) {
        calls.push(relay);
        if (relay === "wss://flaky.example" && calls.filter((row) => row === relay).length === 1) {
          throw new Error("relay error");
        }
        return [{ id: filters[0].ids[0], kind: 1, pubkey: "pk-aa" }];
      },
    });
    const read = () => relayFetch(["wss://relay.example", "wss://flaky.example"], [{ ids: ["aa"], limit: 1 }], { poolOverride });

    assert.deepEqual((await read()).map((event) => event.id), ["aa"]);
    assert.deepEqual((await read()).map((event) => event.id), ["aa"]);
    assert.equal(calls.length, 4, "the partial read should not satisfy the retry");
    await read();
    assert.equal(calls.length, 4, "the complete retry should be reused");
  });

  it("does not cache a read aborted mid-flight", async () => {
    let calls = 0;
    const poolOverride = mockPool({
      query(_relay, filters, { signal }) {
        calls += 1;
        if (calls > 1) return Promise.resolve(filters.map((filter) => ({ id: filter.ids[0], kind: 1, pubkey: "pk" })));
        return new Promise((_resolve, reject) => {
          signal?.addEventListener("abort", () => reject(signal.reason), { once: true });
        });
      },
    });
    const filters = [{ ids: ["aa"], limit: 1 }, { ids: ["ab"], limit: 1 }];

    const pending = relayFetch(["wss://relay.example"], filters, { poolOverride, timeoutMs: 500 });
    await new Promise((resolve) => setTimeout(resolve, 20));
    cancelActiveRelayReads("page hidden");
    assert.deepEqual(await pending, []);

    const retry = await relayFetch(["wss://relay.example"], filters, { poolOverride, timeoutMs: 500 });
    assert.deepEqual(retry.map((event) => event.id), ["aa", "ab"]);
    assert.equal(calls, 2);
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

/** In-memory relay socket that accepts every published event. */
class FakeRelaySocket extends EventTarget {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;
  static instances = [];

  CONNECTING = 0;
  OPEN = 1;
  CLOSING = 2;
  CLOSED = 3;

  constructor(url) {
    super();
    this.url = url;
    this.readyState = FakeRelaySocket.CONNECTING;
    FakeRelaySocket.instances.push(this);
    setTimeout(() => {
      if (this.readyState !== FakeRelaySocket.CONNECTING) return;
      this.readyState = FakeRelaySocket.OPEN;
      this.dispatchEvent(new Event("open"));
    }, 0);
  }

  send(data) {
    const message = JSON.parse(data);
    if (message[0] !== "EVENT") return;
    setTimeout(() => {
      this.dispatchEvent(new MessageEvent("message", { data: JSON.stringify(["OK", message[1].id, true, ""]) }));
    }, 0);
  }

  close() {
    if (this.readyState === FakeRelaySocket.CLOSED) return;
    this.readyState = FakeRelaySocket.CLOSED;
    setTimeout(() => this.dispatchEvent(new Event("close")), 0);
  }
}

function signedReaction(index) {
  return {
    id: String(index).padStart(64, "0"),
    pubkey: "b".repeat(64),
    kind: 7,
    created_at: 1,
    tags: [],
    content: "+",
    sig: "c".repeat(128),
  };
}

/**
 * Runs `fn` against the real nostrify pool with fake sockets and a window
 * whose timers are captured instead of scheduled.
 */
async function withFakeRelaySockets(fn) {
  const previousSocket = Object.getOwnPropertyDescriptor(globalThis, "WebSocket");
  const previousWindow = Object.getOwnPropertyDescriptor(globalThis, "window");
  const timers = [];
  FakeRelaySocket.instances = [];
  Object.defineProperty(globalThis, "WebSocket", { configurable: true, writable: true, value: FakeRelaySocket });
  Object.defineProperty(globalThis, "window", {
    configurable: true,
    value: {
      setTimeout(callback, ms) {
        timers.push({ callback, ms });
        return timers.length;
      },
      clearTimeout() {},
    },
  });
  try {
    return await fn({ sockets: FakeRelaySocket.instances, timers });
  } finally {
    closeRelayPool();
    if (previousWindow) Object.defineProperty(globalThis, "window", previousWindow);
    else delete globalThis.window;
    if (previousSocket) Object.defineProperty(globalThis, "WebSocket", previousSocket);
    else delete globalThis.WebSocket;
  }
}

describe("relay connection lifecycle", () => {
  it("opens a fresh relay connection after the idle sweep closes one", async (t) => {
    await withFakeRelaySockets(async ({ sockets, timers }) => {
      const first = await relayPublish(["wss://relay.example"], signedReaction(1));
      assert.equal(first.accepted, 1);

      const sweep = timers.find((timer) => timer.ms === 5000);
      assert.ok(sweep, "publishing should schedule the idle sweep");
      const start = Date.now();
      const now = t.mock.method(Date, "now", () => start + 30_000);
      sweep.callback();
      assert.equal(sockets[0].readyState, FakeRelaySocket.OPEN, "a relay idle for 30 s should stay open");

      now.mock.mockImplementation(() => start + 61_000);
      timers.filter((timer) => timer.ms === 5000).at(-1).callback();
      assert.equal(sockets[0].readyState, FakeRelaySocket.CLOSED);

      const second = await relayPublish(["wss://relay.example"], signedReaction(2));
      assert.equal(second.accepted, 1, JSON.stringify(second.relay_stats));
      assert.equal(sockets.length, 2, "the swept relay should reconnect with a new socket");
    });
  });

  it("keeps relay sockets open through a short hidden period", async () => {
    await withFakeRelaySockets(async ({ sockets, timers }) => {
      const first = await relayPublish(["wss://relay.example"], signedReaction(5));
      assert.equal(first.accepted, 1);

      setPageVisibility("hidden");
      const hiddenClose = timers.find((timer) => timer.ms === 60_000);
      assert.ok(hiddenClose, "hiding the page should schedule the relay cleanup");
      assert.equal(sockets[0].readyState, FakeRelaySocket.OPEN);

      setPageVisibility("visible");
      // A cleanup timer that still fires after the page is back must not close it.
      hiddenClose.callback();
      const second = await relayPublish(["wss://relay.example"], signedReaction(6));
      assert.equal(second.accepted, 1, JSON.stringify(second.relay_stats));
      assert.equal(sockets.length, 1, "a short hide should reuse the open socket");
    });
  });

  it("closes every relay after a minute hidden and reconnects when visible", async () => {
    await withFakeRelaySockets(async ({ sockets, timers }) => {
      const first = await relayPublish(["wss://relay.example"], signedReaction(7));
      assert.equal(first.accepted, 1);

      setPageVisibility("hidden");
      timers.find((timer) => timer.ms === 60_000).callback();
      assert.equal(sockets[0].readyState, FakeRelaySocket.CLOSED);

      setPageVisibility("visible");
      const second = await relayPublish(["wss://relay.example"], signedReaction(8));
      assert.equal(second.accepted, 1, JSON.stringify(second.relay_stats));
      assert.equal(sockets.length, 2);
    });
  });

  it("starts over with fresh connections when a hidden page's timer was frozen", async (t) => {
    await withFakeRelaySockets(async ({ sockets }) => {
      const first = await relayPublish(["wss://relay.example"], signedReaction(9));
      assert.equal(first.accepted, 1);

      setPageVisibility("hidden");
      const later = Date.now() + 61_000;
      t.mock.method(Date, "now", () => later);
      setPageVisibility("visible");
      assert.equal(sockets[0].readyState, FakeRelaySocket.CLOSED);

      const second = await relayPublish(["wss://relay.example"], signedReaction(10));
      assert.equal(second.accepted, 1, JSON.stringify(second.relay_stats));
      assert.equal(sockets.length, 2);
    });
  });

  it("opens a fresh pool after the hidden-page cleanup closes every relay", async () => {
    await withFakeRelaySockets(async ({ sockets }) => {
      const first = await relayPublish(["wss://relay.example"], signedReaction(3));
      assert.equal(first.accepted, 1);

      closeRelayPool();

      const second = await relayPublish(["wss://relay.example"], signedReaction(4));
      assert.equal(second.accepted, 1, JSON.stringify(second.relay_stats));
      assert.equal(sockets.length, 2);
    });
  });
});
