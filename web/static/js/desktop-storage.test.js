import assert from "node:assert/strict";
import { describe, it } from "node:test";

globalThis.document = {
  documentElement: { dataset: {} },
};

const { formatBytes } = await import("./desktop-storage.js");

describe("desktop storage", () => {
  it("formats storage sizes for settings", () => {
    assert.equal(formatBytes(0), "0 B");
    assert.equal(formatBytes(1024), "1.00 KB");
    assert.equal(formatBytes(10 * 1024 * 1024), "10.0 MB");
  });
});
