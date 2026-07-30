import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  buildThreadParentSkeletonText,
  buildThreadReplySkeletonText,
  buildThreadSelectedSkeletonText,
} from "./thread-skeleton-text.js";

function lineWidths(text) {
  return text.split("\n").map((line) => [...line].length);
}

describe("thread skeleton width", () => {
  it("fills measured width for reply skeletons", () => {
    const width = 58;
    assert.deepEqual(lineWidths(buildThreadReplySkeletonText(width, { isLast: true })), [
      width,
      width - 3,
      width - 3,
      width,
    ]);
    assert.deepEqual(lineWidths(buildThreadReplySkeletonText(width, { isLast: false })), [
      width,
      width - 3,
      width - 3,
      width,
      1,
    ]);
  });

  it("fills measured width for parent and selected skeletons", () => {
    const width = 58;
    assert.deepEqual(lineWidths(buildThreadParentSkeletonText(width)), [
      width,
      width - 3,
      width - 3,
      width - 3,
      width,
    ]);
    assert.deepEqual(lineWidths(buildThreadSelectedSkeletonText(width)), [
      width,
      width,
      width,
      width,
    ]);
  });
});
