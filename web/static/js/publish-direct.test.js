import assert from "node:assert/strict";
import { afterEach, describe, it } from "node:test";

function makeStorage() {
  const data = new Map();
  return {
    getItem(key) {
      return data.has(key) ? data.get(key) : null;
    },
    setItem(key, value) {
      data.set(key, String(value));
    },
    removeItem(key) {
      data.delete(key);
    },
  };
}

globalThis.localStorage = makeStorage();
globalThis.sessionStorage = makeStorage();
globalThis.window = Object.assign(globalThis, {
  addEventListener() {},
  dispatchEvent() {},
  clearTimeout,
  setTimeout,
});
globalThis.document = {
  addEventListener() {},
  querySelectorAll() {
    return [];
  },
};

const { publishSignedEvent, setPublishTestHooks } = await import("./publish.js");

afterEach(() => {
  setPublishTestHooks();
  globalThis.fetch = undefined;
  globalThis.localStorage = makeStorage();
  globalThis.sessionStorage = makeStorage();
});

describe("publishSignedEvent", () => {
  it("publishes directly without falling back to fetch", async () => {
    let fetched = false;
    const invalidated = [];
    const inboxFanouts = [];
    globalThis.fetch = async () => {
      fetched = true;
      throw new Error("fetch should not run");
    };
    setPublishTestHooks({
      planPublishRelays: async () => ["wss://relay.example"],
      relayPublish: async () => ({
        accepted: 1,
        rejected: 0,
        planned_relays: ["wss://relay.example"],
        relay_stats: [{ relay_url: "wss://relay.example", accepted: true, message: "ok", error: "" }],
      }),
      sendEventToInboxRelays: async (event) => {
        inboxFanouts.push(event.id);
      },
      recordPublishedAt: () => {},
      putEvents: async () => {},
      invalidatePublishedQueries: async (event) => {
        invalidated.push(event.id);
      },
    });

    const payload = await publishSignedEvent({ id: "aa", kind: 1, pubkey: "bb" });
    assert.equal(payload.accepted, 1);
    assert.equal(fetched, false);
    assert.deepEqual(invalidated, ["aa"]);
    assert.deepEqual(inboxFanouts, ["aa"]);
  });

  it("rethrows direct publish failures instead of using server fallback", async () => {
    let fetched = false;
    let invalidated = false;
    globalThis.fetch = async () => {
      fetched = true;
      throw new Error("fetch should not run");
    };
    setPublishTestHooks({
      planPublishRelays: async () => ["wss://relay.example"],
      relayPublish: async () => ({
        accepted: 0,
        rejected: 1,
        planned_relays: ["wss://relay.example"],
        relay_stats: [{ relay_url: "wss://relay.example", accepted: false, message: "denied", error: "denied" }],
      }),
      sendEventToInboxRelays: async () => {
        throw new Error("should not fan out");
      },
      recordPublishedAt: () => {},
      putEvents: async () => {},
      invalidatePublishedQueries: async () => {
        invalidated = true;
      },
    });

    await assert.rejects(() => publishSignedEvent({ id: "aa", kind: 1, pubkey: "bb" }), /denied/);
    assert.equal(fetched, false);
    assert.equal(invalidated, false);
  });

  it("does not trigger inbox fan-out for non-directed publishes", async () => {
    let fanoutCount = 0;
    setPublishTestHooks({
      planPublishRelays: async () => ["wss://relay.example"],
      relayPublish: async () => ({
        accepted: 1,
        rejected: 0,
        planned_relays: ["wss://relay.example"],
        relay_stats: [{ relay_url: "wss://relay.example", accepted: true, message: "ok", error: "" }],
      }),
      sendEventToInboxRelays: async () => {
        fanoutCount += 1;
      },
      recordPublishedAt: () => {},
      putEvents: async () => {},
      invalidatePublishedQueries: async () => {},
    });

    await publishSignedEvent({ id: "aa", kind: 10002, pubkey: "bb" });
    assert.equal(fanoutCount, 0);
  });

  it("waits for local persistence and invalidation before reporting publish completion", async () => {
    let releasePersistence;
    let releaseInvalidation;
    const persistence = new Promise((resolve) => { releasePersistence = resolve; });
    const invalidation = new Promise((resolve) => { releaseInvalidation = resolve; });
    setPublishTestHooks({
      planPublishRelays: async () => ["wss://relay.example"],
      relayPublish: async () => ({
        accepted: 1,
        rejected: 0,
        planned_relays: ["wss://relay.example"],
        relay_stats: [{ relay_url: "wss://relay.example", accepted: true, message: "ok", error: "" }],
      }),
      recordPublishedAt: () => {},
      putEvents: async () => persistence,
      invalidatePublishedQueries: async () => invalidation,
    });

    let settled = false;
    const publishing = publishSignedEvent({ id: "aa", kind: 10000, pubkey: "bb" }).finally(() => {
      settled = true;
    });
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.equal(settled, false);

    releasePersistence();
    releaseInvalidation();
    await publishing;
    assert.equal(settled, true);
  });

  it("ignores inbox fan-out errors after a successful publish", async () => {
    setPublishTestHooks({
      planPublishRelays: async () => ["wss://relay.example"],
      relayPublish: async () => ({
        accepted: 1,
        rejected: 0,
        planned_relays: ["wss://relay.example"],
        relay_stats: [{ relay_url: "wss://relay.example", accepted: true, message: "ok", error: "" }],
      }),
      sendEventToInboxRelays: async () => {
        throw new Error("fan-out failed");
      },
      recordPublishedAt: () => {},
      putEvents: async () => {},
      invalidatePublishedQueries: async () => {},
    });

    const payload = await publishSignedEvent({ id: "aa", kind: 1, pubkey: "bb", tags: [["p", "cc".repeat(32)]] });
    assert.equal(payload.accepted, 1);
  });
});
