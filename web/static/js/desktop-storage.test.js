import assert from "node:assert/strict";
import { describe, it } from "node:test";

globalThis.document = {
  documentElement: { dataset: {} },
};

const { bytesFromGB, formatBytes, formatGBInput } = await import("./desktop-storage.js");

describe("desktop storage", () => {
  it("formats storage sizes for settings", () => {
    assert.equal(formatBytes(0), "0 B");
    assert.equal(formatBytes(1024), "1.00 KB");
    assert.equal(formatBytes(10 * 1024 * 1024), "10.0 MB");
  });

  it("converts user-entered gigabytes to a byte limit", () => {
    assert.equal(bytesFromGB("2"), 2 * 1024 ** 3);
    assert.equal(formatGBInput(2 * 1024 ** 3), "2");
    assert.equal(bytesFromGB("0.01"), 0);
    assert.equal(bytesFromGB("not-a-number"), 0);
  });
});
