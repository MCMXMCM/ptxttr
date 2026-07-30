import assert from "node:assert/strict";
import { afterEach, describe, it } from "node:test";

import { estimateEventRecordBytes, putEvents } from "./event-store.js";
import { resetClientDBForTests } from "./client-store.js";

afterEach(() => {
  resetClientDBForTests();
  delete globalThis.indexedDB;
});

describe("event-store", () => {
  it("estimates persisted event size from serialized payload bytes", () => {
    const small = estimateEventRecordBytes({ id: "a", content: "hello" });
    const large = estimateEventRecordBytes({ id: "a", content: "hello".repeat(200) });
    assert.ok(small > 0);
    assert.ok(large > small);
  });

  it("treats IndexedDB unavailability as a best-effort cache write miss", async () => {
    await assert.doesNotReject(putEvents([{
      id: "aa".repeat(32),
      pubkey: "bb".repeat(32),
      kind: 1,
      created_at: 1,
      content: "hello",
      tags: [],
    }]));
  });
});
