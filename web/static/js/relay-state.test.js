import assert from "node:assert/strict";
import { beforeEach, describe, it } from "node:test";

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

const relayState = await import("./relay-state.js");
const { DEFAULT_RELAYS } = await import("./relay-config.js");

const {
  LEGACY_RELAYS_KEY,
  RELAY_CONFIG_KEY,
  effectiveReadRelays,
  effectiveWriteRelays,
  loadRelayConfig,
  migrateLegacyRelayState,
} = relayState;

beforeEach(() => {
  localStorage.clear();
});

describe("relay-state", () => {
  it("migrates legacy selected relays into relay config", () => {
    localStorage.setItem(LEGACY_RELAYS_KEY, JSON.stringify(["wss://legacy.example/", "wss://other.example"]));

    const config = migrateLegacyRelayState();

    assert.equal(config.useAppRelays, false);
    assert.equal(config.useUserRelays, true);
    assert.deepEqual(config.userRelayMetadata.relays, [
      { url: "wss://legacy.example", read: true, write: true },
      { url: "wss://other.example", read: true, write: true },
    ]);
    assert.equal(localStorage.getItem(LEGACY_RELAYS_KEY), null);
    assert.ok(localStorage.getItem(RELAY_CONFIG_KEY));
  });

  it("returns fresh-install defaults when no relay state exists", () => {
    const config = loadRelayConfig();
    assert.equal(config.useAppRelays, true);
    assert.equal(config.useUserRelays, false);
    assert.deepEqual(config.userRelayMetadata.relays, []);
  });

  it("derives effective read and write relays from app and user sources", () => {
    const config = {
      useAppRelays: true,
      useUserRelays: true,
      userRelayMetadata: {
        updatedAt: 123,
        relays: [
          { url: DEFAULT_RELAYS[0], read: false, write: true },
          { url: "wss://user-read.example/", read: true, write: false },
          { url: "wss://user-write.example", read: false, write: true },
          { url: "wss://user-both.example", read: true, write: true },
        ],
      },
    };

    assert.deepEqual(effectiveReadRelays(config), [
      DEFAULT_RELAYS[0],
      DEFAULT_RELAYS[1],
      DEFAULT_RELAYS[2],
      "wss://user-read.example",
      "wss://user-both.example",
    ]);
    assert.deepEqual(effectiveWriteRelays(config), [
      DEFAULT_RELAYS[0],
      DEFAULT_RELAYS[1],
      DEFAULT_RELAYS[2],
      "wss://user-write.example",
      "wss://user-both.example",
    ]);
  });

  it("respects disabled app and user relay policies", () => {
    const config = {
      useAppRelays: false,
      useUserRelays: true,
      userRelayMetadata: {
        updatedAt: 0,
        relays: [{ url: "wss://user-only.example", read: true, write: true }],
      },
    };

    assert.deepEqual(effectiveReadRelays(config), ["wss://user-only.example"]);
    assert.deepEqual(effectiveWriteRelays(config), ["wss://user-only.example"]);
  });
});
