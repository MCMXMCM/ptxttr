import assert from "node:assert/strict";
import { afterEach, describe, it } from "node:test";

import { estimateEventRecordBytes, getEvent, putEvents, recentTimelineEvents } from "./event-store.js";
import { resetClientDBForTests } from "./client-store.js";
import { setAppBootstrapForTests } from "./app/bootstrap.js";

afterEach(() => {
  resetClientDBForTests();
  setAppBootstrapForTests({ features: { localFirst: false } });
  delete globalThis.indexedDB;
  delete globalThis.document;
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

  it("does not open the renderer event database in desktop mode", async () => {
    globalThis.document = {
      documentElement: { dataset: { ptxtDesktopMode: "1" } },
      getElementById: () => null,
    };
    Object.defineProperty(globalThis, "indexedDB", {
      configurable: true,
      get() {
        throw new Error("desktop event reads must use the sidecar");
      },
    });
    await assert.doesNotReject(putEvents([{ id: "aa".repeat(32) }]));
    assert.equal(await getEvent("aa".repeat(32)), null);
    assert.deepEqual(await recentTimelineEvents({ kinds: [1] }), []);
  });
});
