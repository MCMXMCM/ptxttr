import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  ASCII_MAX_COLUMNS,
  ASCII_MIN_COLUMNS,
  buildAsciiRenderCacheKey,
  clampAsciiColumns,
  columnsFromWidth,
  shouldRenderAscii,
} from "./ascii-layout.js";

describe("ascii-layout", () => {
  it("clamps measured column counts into the ASCII renderer range", () => {
    assert.equal(clampAsciiColumns(0), ASCII_MIN_COLUMNS);
    assert.equal(clampAsciiColumns(48.9), 48);
    assert.equal(clampAsciiColumns(999), ASCII_MAX_COLUMNS);
  });

  it("maps widths to column counts from measured cell width", () => {
    assert.equal(columnsFromWidth(0, 8), 0);
    assert.equal(columnsFromWidth(480, 10), 48);
    assert.equal(columnsFromWidth(120, 10), ASCII_MIN_COLUMNS);
  });

  it("renders only when the layout key changes unless content is dirty", () => {
    const key = buildAsciiRenderCacheKey(48, false, false);
    assert.equal(shouldRenderAscii("", key, false), true);
    assert.equal(shouldRenderAscii(key, key, false), false);
    assert.equal(shouldRenderAscii(key, key, true), true);
  });
});
