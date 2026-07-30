import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  authorMembershipSet,
  chunkAuthors,
  clampQueryAuthors,
  filterEventsByAuthorMembership,
  sortEventsNewestFirst,
  sortEventsOldestFirst,
} from "./feed-query.js";

describe("feed-query", () => {
  it("chunks authors for relay queries", () => {
    const authors = Array.from({ length: 70 }, (_, index) => String(index).padStart(64, "a"));
    const batches = chunkAuthors(authors, 64);
    assert.equal(batches.length, 2);
    assert.equal(batches[0].length, 64);
    assert.equal(batches[1].length, 6);
  });

  it("filters events to WoT membership", () => {
    const allowed = authorMembershipSet(["aa".repeat(32), "bb".repeat(32)]);
    const events = [
      { id: "1".repeat(64), pubkey: "aa".repeat(32), created_at: 2 },
      { id: "2".repeat(64), pubkey: "cc".repeat(32), created_at: 3 },
      { id: "3".repeat(64), pubkey: "bb".repeat(32), created_at: 1 },
    ];
    const filtered = filterEventsByAuthorMembership(events, allowed);
    assert.equal(filtered.length, 2);
    assert.deepEqual(
      filtered.map((event) => event.id),
      ["1".repeat(64), "3".repeat(64)],
    );
  });

  it("sorts events newest-first with id tie-break", () => {
    const sorted = sortEventsNewestFirst([
      { id: "a".repeat(64), created_at: 1 },
      { id: "b".repeat(64), created_at: 2 },
      { id: "c".repeat(64), created_at: 2 },
    ]);
    assert.equal(sorted[0].id, "c".repeat(64));
    assert.equal(sorted[1].id, "b".repeat(64));
  });

  it("sorts events oldest-first with id tie-break", () => {
    const sorted = sortEventsOldestFirst([
      { id: "c".repeat(64), created_at: 2 },
      { id: "a".repeat(64), created_at: 1 },
      { id: "b".repeat(64), created_at: 2 },
    ]);
    assert.equal(sorted[0].id, "a".repeat(64));
    assert.equal(sorted[1].id, "b".repeat(64));
    assert.equal(sorted[2].id, "c".repeat(64));
  });

  it("clamps relay author query lists", () => {
    const authors = Array.from({ length: 300 }, (_, index) => `${index}`.padStart(64, "d"));
    assert.equal(clampQueryAuthors(authors, 240).length, 240);
  });
});
