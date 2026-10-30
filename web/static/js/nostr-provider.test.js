import assert from "node:assert/strict";
import { afterEach, describe, it } from "node:test";

import { finalizeEvent, generateSecretKey, verifyEvent } from "../lib/nostr-tools.js";
import {
  closeNostrPool,
  createRelayEventVerifier,
  getNostrPool,
  routeFiltersToRelayMap,
  routePublishEventToRelays,
  verifyRelayEvent,
} from "./nostr-provider.js";

function makeStorage(initial = {}) {
  const values = new Map(Object.entries(initial));
  return {
    getItem(key) {
      return values.has(key) ? values.get(key) : null;
    },
    setItem(key, value) {
      values.set(key, String(value));
    },
    removeItem(key) {
      values.delete(key);
    },
  };
}

afterEach(() => {
  delete globalThis.localStorage;
});

describe("nostr-provider routing", () => {
  it("routes authorless filters to effective read relays", async () => {
    globalThis.localStorage = makeStorage();
    const routes = await routeFiltersToRelayMap([{ kinds: [1], limit: 20 }]);
    assert.deepEqual([...routes.keys()], [
      "wss://relay.primal.net",
      "wss://relay.damus.io",
      "wss://nos.lol",
    ]);
  });

  it("routes publishes to app and effective write relays when no hints are cached", async () => {
    globalThis.localStorage = makeStorage();
    const relays = await routePublishEventToRelays({ pubkey: "f".repeat(64), kind: 1, id: "a".repeat(64) });
    assert.deepEqual(relays, [
      "wss://relay.primal.net",
      "wss://relay.damus.io",
      "wss://nos.lol",
    ]);
  });
});

/** A relay message parses into a fresh object, without nostr-tools' cached verdict. */
function relayCopy(event, overrides = {}) {
  return { ...JSON.parse(JSON.stringify(event)), ...overrides };
}

function countingVerifier(options = {}) {
  const counter = { checks: 0 };
  counter.verifyRelayEvent = createRelayEventVerifier({
    ...options,
    verify(event) {
      counter.checks += 1;
      return verifyEvent(event);
    },
  });
  return counter;
}

describe("relay event verification", () => {
  const secret = generateSecretKey();
  const signed = finalizeEvent({ kind: 1, created_at: 1_700_000_000, tags: [], content: "hello" }, secret);

  it("checks a signature once and only rehashes later relay copies", () => {
    const counter = countingVerifier();
    assert.equal(counter.verifyRelayEvent(relayCopy(signed)), true);
    assert.equal(counter.verifyRelayEvent(relayCopy(signed)), true);
    assert.equal(counter.verifyRelayEvent(relayCopy(signed)), true);
    assert.equal(counter.checks, 1);
  });

  it("rejects a copy whose content no longer hashes to the verified id", () => {
    const counter = countingVerifier();
    assert.equal(counter.verifyRelayEvent(relayCopy(signed)), true);
    assert.equal(counter.verifyRelayEvent(relayCopy(signed, { content: "tampered" })), false);
    assert.equal(counter.verifyRelayEvent(relayCopy(signed, { pubkey: "f".repeat(64) })), false);
    assert.equal(counter.checks, 1);
  });

  it("fully checks a known id that arrives with another signature", () => {
    const counter = countingVerifier();
    assert.equal(counter.verifyRelayEvent(relayCopy(signed)), true);
    assert.equal(counter.verifyRelayEvent(relayCopy(signed, { sig: "0".repeat(128) })), false);
    assert.equal(counter.checks, 2);
    assert.equal(counter.verifyRelayEvent(relayCopy(signed)), true);
    assert.equal(counter.checks, 2);
  });

  it("does not remember failed checks", () => {
    const counter = countingVerifier();
    assert.equal(counter.verifyRelayEvent(relayCopy(signed, { sig: "0".repeat(128) })), false);
    assert.equal(counter.verifyRelayEvent(relayCopy(signed, { sig: "0".repeat(128) })), false);
    assert.equal(counter.checks, 2);
  });

  it("forgets the oldest verified ids past its limit", () => {
    const counter = countingVerifier({ limit: 2 });
    const events = [1, 2, 3].map((n) => finalizeEvent({ kind: 1, created_at: n, tags: [], content: `note ${n}` }, secret));
    events.forEach((event) => assert.equal(counter.verifyRelayEvent(relayCopy(event)), true));
    assert.equal(counter.verifyRelayEvent(relayCopy(events[2])), true);
    assert.equal(counter.checks, 3);
    assert.equal(counter.verifyRelayEvent(relayCopy(events[0])), true);
    assert.equal(counter.checks, 4);
  });

  it("is the check pool relays run on incoming events", () => {
    const previousSocket = Object.getOwnPropertyDescriptor(globalThis, "WebSocket");
    Object.defineProperty(globalThis, "WebSocket", {
      configurable: true,
      writable: true,
      value: class extends EventTarget {
        readyState = 0;
        send() {}
        close() {
          this.readyState = 3;
        }
      },
    });
    try {
      assert.equal(getNostrPool().relay("wss://relay.example").opts.verifyEvent, verifyRelayEvent);
    } finally {
      closeNostrPool();
      if (previousSocket) Object.defineProperty(globalThis, "WebSocket", previousSocket);
      else delete globalThis.WebSocket;
    }
  });
});
