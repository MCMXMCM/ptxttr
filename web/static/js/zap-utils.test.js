import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { formatCompactSats } from "./zap-utils.js";

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
