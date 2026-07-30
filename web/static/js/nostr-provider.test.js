import assert from "node:assert/strict";
import { afterEach, describe, it } from "node:test";

import { routeFiltersToRelayMap, routePublishEventToRelays } from "./nostr-provider.js";

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
