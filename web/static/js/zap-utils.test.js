import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { formatCompactSats, zapTotalsForEvents } from "./zap-utils.js";

describe("formatCompactSats", () => {
  it("keeps amounts below one thousand exact", () => {
    assert.equal(formatCompactSats(0), "0");
    assert.equal(formatCompactSats(999), "999");
  });

  it("shortens thousands with a lowercase k", () => {
    assert.equal(formatCompactSats(1000), "1k");
    assert.equal(formatCompactSats(1132), "1.1k");
    assert.equal(formatCompactSats(12_500), "12.5k");
  });
});

describe("zapTotalsForEvents", () => {
  it("sums returned receipts directly without relying on a browser event database", () => {
    const first = "aa".repeat(32);
    const second = "bb".repeat(32);
    const totals = zapTotalsForEvents([first, second], [
      { created_at: 10, tags: [["e", first], ["amount", "21000"]] },
      { created_at: 11, tags: [["e", first], ["amount", "9000"]] },
      { created_at: 9, tags: [["e", second], ["amount", "5000"]] },
    ], { sinceCreatedAt: 10 });
    assert.equal(totals.get(first), 30);
    assert.equal(totals.get(second), 0);
  });
});
