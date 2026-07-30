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
  addEventListener() {},
  body: {
    append() {},
  },
};

const { seedRelayPreferenceDraft } = await import("./mutations.js");

describe("seedRelayPreferenceDraft", () => {
  it("prefers published kind-10002 relays", () => {
    const draft = seedRelayPreferenceDraft({
      publishedRelays: [{ url: "wss://published.example", usage: "read" }],
      userRelays: [{ url: "wss://user.example", usage: "write" }],
      effectiveDefaultRelays: ["wss://default.example"],
    });

    assert.deepEqual(draft, [{ url: "wss://published.example", usage: "read" }]);
  });

  it("falls back to user relays", () => {
    const draft = seedRelayPreferenceDraft({
      publishedRelays: [],
      userRelays: [{ url: "wss://user.example", usage: "write" }],
      effectiveDefaultRelays: ["wss://default.example"],
    });

    assert.deepEqual(draft, [{ url: "wss://user.example", usage: "any" }]);
  });

  it("uses legacy session relays as a compatibility fallback", () => {
    const draft = seedRelayPreferenceDraft({
      publishedRelays: [],
      sessionRelays: ["wss://session.example"],
      effectiveDefaultRelays: ["wss://default.example"],
    });

    assert.deepEqual(draft, [{ url: "wss://session.example", usage: "any" }]);
  });

  it("uses effective defaults when published and user relays are absent", () => {
    const draft = seedRelayPreferenceDraft({
      publishedRelays: [],
      userRelays: [],
      effectiveDefaultRelays: ["wss://default.example"],
    });

    assert.deepEqual(draft, [{ url: "wss://default.example", usage: "write" }]);
  });
});
