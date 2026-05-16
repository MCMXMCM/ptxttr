import assert from "node:assert/strict";
import { describe, it } from "node:test";

globalThis.window ??= { location: { origin: "http://localhost" } };
import {
  feedSnapshotKey,
  legacyFeedSnapshotKey,
  lookupFeedSnapshot,
  purgeStaleFeedSnapshotKeys,
} from "./feed-snapshot-keys.js";

describe("feedSnapshotKey", () => {
  it("canonicalizes / and /feed to the same key", () => {
    assert.equal(feedSnapshotKey("http://localhost/"), "/?");
    assert.equal(feedSnapshotKey("http://localhost/feed"), "/?");
  });

  it("strips viewer-pref query params", () => {
    assert.equal(
      feedSnapshotKey("http://localhost/?sort=recent&pubkey=abc"),
      "/?",
    );
    assert.equal(feedSnapshotKey("http://localhost/feed?sort=trend7d&wot=1"), "/?");
  });

  it("returns empty string for non-home paths", () => {
    assert.equal(feedSnapshotKey("http://localhost/reads"), "");
    assert.equal(feedSnapshotKey("http://localhost/u/npub1"), "");
  });
});

describe("legacyFeedSnapshotKey", () => {
  it("maps canonical /? to /feed?", () => {
    assert.equal(legacyFeedSnapshotKey("/?"), "/feed?");
    assert.equal(legacyFeedSnapshotKey("/?q=note"), "/feed?q=note");
  });
});

describe("lookupFeedSnapshot", () => {
  it("finds entries stored under legacy /feed and pref-heavy keys", () => {
    const map = new Map();
    const snapshot = { html: "<section data-feed></section>" };
    map.set("/feed?sort=recent", snapshot);

    const hit = lookupFeedSnapshot(map, "http://localhost/");
    assert.ok(hit);
    assert.equal(hit.snapshot, snapshot);
    assert.equal(hit.key, "/feed?sort=recent");
  });

  it("prefers the canonical key when present", () => {
    const map = new Map();
    map.set("/?", { html: "canonical" });
    map.set("/feed?", { html: "legacy" });

    const hit = lookupFeedSnapshot(map, "http://localhost/feed");
    assert.equal(hit.snapshot.html, "canonical");
    assert.equal(hit.key, "/?");
  });
});

describe("purgeStaleFeedSnapshotKeys", () => {
  it("removes alias keys before saving under the canonical key", () => {
    const map = new Map();
    map.set("/feed?sort=recent", { html: "old" });
    map.set("/?sort=recent", { html: "older" });

    purgeStaleFeedSnapshotKeys(map, "/?");
    assert.equal(map.size, 0);

    map.set("/?", { html: "fresh" });
    assert.equal(map.get("/?").html, "fresh");
  });
});
