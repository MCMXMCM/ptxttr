import assert from "node:assert/strict";
import { describe, it } from "node:test";

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
globalThis.window = Object.assign(globalThis, {
  dispatchEvent() {},
});
globalThis.CustomEvent = class CustomEvent {
  constructor(type, init = {}) {
    this.type = type;
    this.detail = init.detail;
  }
};

const { inboxReadRelays, sendEventToInboxRelays } = await import("./inbox-relays.js");

describe("inbox relay helpers", () => {
  it("extracts read-capable relays from kind 10002", () => {
    const relays = inboxReadRelays({
      kind: 10002,
      tags: [
        ["r", "wss://read.example", "read"],
        ["r", "wss://both.example"],
        ["r", "wss://write.example", "write"],
      ],
    });

    assert.deepEqual(relays, ["wss://read.example", "wss://both.example"]);
  });

  it("publishes to tagged users inbox relays", async () => {
    const published = [];
    const inboxRelays = await sendEventToInboxRelays({
      id: "aa",
      kind: 1,
      pubkey: "11".repeat(32),
      tags: [["p", "22".repeat(32)]],
    }, {
      readRelays: ["wss://reader.example"],
      relayFetchFn: async () => [{
        kind: 10002,
        tags: [["r", "wss://inbox.example", "read"]],
      }],
      relayPublishFn: async (relays, event) => {
        published.push({ relays, event });
      },
    });

    assert.deepEqual(inboxRelays, ["wss://inbox.example"]);
    assert.equal(published.length, 1);
    assert.deepEqual(published[0].relays, ["wss://inbox.example"]);
    assert.equal(published[0].event.id, "aa");
  });

  it("ignores the author pubkey when building inbox targets", async () => {
    let fetchCount = 0;
    const inboxRelays = await sendEventToInboxRelays({
      id: "aa",
      kind: 1,
      pubkey: "11".repeat(32),
      tags: [["p", "11".repeat(32)]],
    }, {
      readRelays: ["wss://reader.example"],
      relayFetchFn: async () => {
        fetchCount += 1;
        return [];
      },
      relayPublishFn: async () => {},
    });

    assert.deepEqual(inboxRelays, []);
    assert.equal(fetchCount, 0);
  });
});
