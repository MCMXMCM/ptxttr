import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { normalizeRelayList } from "./relay-config.js";
import { participantPubkeys } from "./relay-utils.js";

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
    clear() {
      data.clear();
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

const { planPublishRelays } = await import("./publish-plan.js");

describe("publish-plan helpers", () => {
  it("normalizes and caps relay lists", () => {
    const relays = normalizeRelayList([
      "wss://a.example",
      "wss://a.example/",
      "ftp://bad",
      "wss://b.example",
    ], 8);
    assert.deepEqual(relays, ["wss://a.example", "wss://b.example"]);
  });

  it("collects participant pubkeys from p tags", () => {
    const author = "aa".repeat(32);
    const peer = "bb".repeat(32);
    const pubkeys = participantPubkeys({
      pubkey: author,
      kind: 1,
      tags: [["p", peer]],
    });
    assert.deepEqual(pubkeys.sort(), [author, peer].sort());
  });

  it("uses effective write relays as the publish base set", async () => {
    localStorage.clear();
    localStorage.setItem("ptxt_relay_config", JSON.stringify({
      useAppRelays: false,
      useUserRelays: true,
      userRelayMetadata: {
        updatedAt: 0,
        relays: [
          { url: "wss://write-only.example", read: false, write: true },
          { url: "wss://read-only.example", read: true, write: false },
        ],
      },
    }));

    const relays = await planPublishRelays({ kind: 1, pubkey: "", tags: [] });

    assert.equal(relays[0], "wss://write-only.example");
    assert.ok(!relays.includes("wss://read-only.example"));
  });
});
