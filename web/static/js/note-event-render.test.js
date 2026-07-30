import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { relativeAge } from "./note-event-render.js";
import { displayName } from "./profile-parse.js";

describe("note-event-render", () => {
  it("formats relative age", () => {
    const now = Math.floor(Date.now() / 1000);
    assert.match(relativeAge(now - 30), /^\d+s$/);
    assert.match(relativeAge(now - 7200), /^\d+h$/);
  });

  it("builds note metadata fields", () => {
    const pk = "aa".repeat(32);
    const id = "bb".repeat(32);
    const profile = { pubkey: pk, display_name: "Alice", avatar_url: "" };
    const age = relativeAge(Math.floor(Date.now() / 1000) - 120);
    assert.match(age, /^\d+[smhdw]$/);
    assert.equal(displayName(profile), "Alice");
    assert.equal(id.length, 64);
  });
});
