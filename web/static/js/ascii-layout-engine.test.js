// @ts-check
import { describe, it } from "node:test";
import assert from "node:assert/strict";

import {
  displayWidth,
  fit,
  layoutGlyphsFromLines,
  takeColumns,
  wrapAsciiBody,
} from "./ascii-layout-engine.js";

describe("ascii-layout-engine", () => {
  it("counts wide graphemes as two columns", () => {
    assert.equal(displayWidth("abc"), 3);
    assert.equal(displayWidth("漢字"), 4);
  });

  it("wraps plain text within a column budget without Pretext font context", () => {
    const lines = wrapAsciiBody("hello world from ascii", 10);
    assert.ok(lines.length >= 2);
    lines.forEach((line) => assert.ok(displayWidth(line) <= 10));
  });

  it("fits truncated values to exact column width", () => {
    assert.equal(displayWidth(fit("hello", 8)), 8);
    assert.equal(takeColumns("hello world", 5), "hello");
  });

  it("places following glyphs after wide CJK columns", () => {
    const layout = { glyphs: [] };
    layoutGlyphsFromLines(["| 漢字 |"], {
      x: 0,
      y: 0,
      cellWidth: 10,
      lineHeight: 20,
    }, "note", "body", 0, layout);

    const pipes = layout.glyphs.filter((glyph) => glyph.char === "|");
    assert.equal(pipes.length, 2);
    assert.equal(pipes[1].x, 70);
  });

  it("keeps boxed CJK rows at their requested display width", () => {
    const row = `| ${fit("不要拿自己的幕后生活去和别人的精彩片段比较", 20)} |`;
    assert.equal(displayWidth(row), 24);

    const layout = { glyphs: [] };
    layoutGlyphsFromLines([row], {
      x: 0,
      y: 0,
      cellWidth: 1,
      lineHeight: 1,
    }, "note", "body", 0, layout);

    const rightPipe = layout.glyphs.filter((glyph) => glyph.char === "|").at(-1);
    assert.equal(rightPipe.x, 23);
  });
});
