import { describe, it } from "node:test";
import assert from "node:assert/strict";
import {
  normalizeHashtag,
  hashtagsInContent,
  eventHasHashtag,
  parseTagFromPath,
} from "./hashtag-utils.js";

describe("hashtag-utils", () => {
  it("normalizes hashtag tokens", () => {
    assert.equal(normalizeHashtag("#Nostr"), "Nostr");
    assert.equal(normalizeHashtag(" golang "), "golang");
    assert.equal(normalizeHashtag("bad tag"), "");
    assert.equal(normalizeHashtag(""), "");
  });

  it("extracts hashtags from content", () => {
    assert.deepEqual(hashtagsInContent("hello #Nostr and #go"), ["Nostr", "go"]);
    assert.deepEqual(hashtagsInContent("edge#inline"), []);
  });

  it("matches t tags and body hashtags", () => {
    const tagged = { kind: 1, content: "hi", tags: [["t", "nostr"]] };
    const body = { kind: 1, content: "hello #nostr", tags: [] };
    assert.equal(eventHasHashtag(tagged, "nostr"), true);
    assert.equal(eventHasHashtag(body, "nostr"), true);
    assert.equal(eventHasHashtag(body, "missing"), false);
  });

  it("parses tag paths", () => {
    assert.equal(parseTagFromPath("/tag/nostr"), "nostr");
    assert.equal(parseTagFromPath("/tag/Nostr"), "Nostr");
    assert.equal(parseTagFromPath("/feed"), "");
  });
});
