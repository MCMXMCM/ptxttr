import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { lookupProfileSnapshot, profileSnapshotKey } from "./profile-snapshot-keys.js";

globalThis.window ??= { location: { origin: "http://localhost" } };

describe("profileSnapshotKey", () => {
  it("strips cursor and viewer-pref params", () => {
    const pubkey = "npub1abc";
    assert.equal(
      profileSnapshotKey(`http://localhost/u/${pubkey}?cursor=1&sort=recent&pubkey=x`),
      `/u/${pubkey}?`,
    );
  });

  it("returns empty string for non-profile paths", () => {
    assert.equal(profileSnapshotKey("http://localhost/"), "");
  });
});

describe("lookupProfileSnapshot", () => {
  it("finds entries stored under legacy pref-heavy keys", () => {
    const map = new Map();
    const snapshot = { html: "<div id=\"user-panel-posts\"></div>" };
    map.set("/u/alice?sort=recent", snapshot);

    const hit = lookupProfileSnapshot(map, "http://localhost/u/alice");
    assert.ok(hit);
    assert.equal(hit.snapshot, snapshot);
    assert.equal(hit.key, "/u/alice?sort=recent");
  });
});
