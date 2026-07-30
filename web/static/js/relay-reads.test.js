import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { resetClientDBForTests } from "./client-store.js";
import { INDEXER_NIP50_RELAYS } from "./relay-config.js";
import { chunkValues, eventFetchRelayStages, fetchEventsByIDs, groupedRelayHintBatches, nip50Search, relayPreferencesFromKind10002Event } from "./relay-reads.js";

describe("relay-reads batching helpers", () => {
  it("chunks values into bounded groups", () => {
    assert.deepEqual(chunkValues(["a", "b", "c", "d", "e"], 2), [
      ["a", "b"],
      ["c", "d"],
      ["e"],
    ]);
  });

  it("groups ids by normalized relay-hint sets", () => {
    const groups = groupedRelayHintBatches(
      ["id-1", "id-2", "id-3", "id-4"],
      {
        "id-1": ["wss://relay.damus.io/", "wss://relay.primal.net"],
        "id-2": ["wss://relay.primal.net/", "wss://relay.damus.io"],
        "id-3": ["wss://nos.lol"],
        "id-4": [],
      },
    );

    assert.deepEqual(groups, [
      {
        ids: ["id-1", "id-2"],
        hints: ["wss://relay.damus.io", "wss://relay.primal.net"],
      },
      {
        ids: ["id-3"],
        hints: ["wss://nos.lol"],
      },
      {
        ids: ["id-4"],
        hints: [],
      },
    ]);
  });

  it("routes NIP-50 search only to dedicated search relays", async () => {
    let capturedRelays = [];
    let capturedFilters = [];

    const results = await nip50Search("hello", {
      limit: 5,
      kinds: [1],
      relayFetchImpl: async (relays, filters) => {
        capturedRelays = relays;
        capturedFilters = filters;
        return [];
      },
    });

    assert.deepEqual(results, []);
    assert.deepEqual(capturedRelays, [...INDEXER_NIP50_RELAYS]);
    assert.deepEqual(capturedFilters, [{ search: "hello", kinds: [1], limit: 5 }]);
  });

  it("plans event fetch relays as hints, author outbox, then fallback relays", () => {
    assert.deepEqual(
      eventFetchRelayStages(
        ["wss://hint.example", "wss://shared.example"],
        ["wss://outbox.example", "wss://shared.example"],
        ["wss://fallback.example"],
      ),
      [
        ["wss://hint.example", "wss://shared.example"],
        ["wss://outbox.example", "wss://shared.example"],
        ["wss://fallback.example"],
      ],
    );
    assert.deepEqual(
      eventFetchRelayStages(["wss://same.example"], ["wss://same.example/"], ["wss://fallback.example"]),
      [["wss://same.example"], ["wss://fallback.example"]],
    );
  });

  it("flattens kind-10002 relay tags into editable preferences", () => {
    assert.deepEqual(
      relayPreferencesFromKind10002Event({
        kind: 10002,
        tags: [
          ["r", "wss://write.example/", "write"],
          ["r", "wss://read.example", "read"],
          ["r", "wss://both.example", "read"],
          ["r", "wss://both.example", "write"],
          ["r", "wss://any.example"],
        ],
      }),
      [
        { url: "wss://any.example", usage: "any" },
        { url: "wss://write.example", usage: "write" },
        { url: "wss://both.example", usage: "any" },
        { url: "wss://read.example", usage: "read" },
      ],
    );
  });

  it("treats temporary IndexedDB failures as relay cache misses", async () => {
    const id = "a".repeat(64);
    const event = {
      id,
      pubkey: "b".repeat(64),
      kind: 1,
      created_at: 1,
      tags: [],
      content: "hello",
    };
    const priorIndexedDB = globalThis.indexedDB;
    delete globalThis.indexedDB;
    try {
      const result = await fetchEventsByIDs([id], {
        relayFetchImpl: async (_relays, filters) => {
          assert.deepEqual(filters, [{ ids: [id], limit: 1 }]);
          return [event];
        },
      });

      assert.deepEqual(result, [event]);
    } finally {
      if (priorIndexedDB === undefined) delete globalThis.indexedDB;
      else globalThis.indexedDB = priorIndexedDB;
      resetClientDBForTests();
    }
  });
});
