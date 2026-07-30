import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  isReferenceExpanded,
  markReferenceExpanded,
} from "./ascii-reference-expansion.js";

describe("ASCII reference expansion", () => {
  it("survives replacement of a thread note with the same stable note id", () => {
    const noteID = `note-${"ab".repeat(32)}`;
    const original = { id: noteID };
    const replacement = { id: noteID };

    markReferenceExpanded(original, "nested");

    assert.equal(isReferenceExpanded(original, "nested"), true);
    assert.equal(isReferenceExpanded(replacement, "nested"), true);
  });

  it("does not leak expansion to a different note", () => {
    const expanded = { id: `note-${"cd".repeat(32)}` };
    const other = { id: `note-${"ef".repeat(32)}` };

    markReferenceExpanded(expanded, "nested");

    assert.equal(isReferenceExpanded(other, "nested"), false);
  });
});
