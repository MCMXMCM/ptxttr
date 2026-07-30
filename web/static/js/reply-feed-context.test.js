import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { profilePath } from "./relay-utils.js";

import {
  isFeedThreadReply,
  replyContextHTML,
  replyContextTargets,
  replyContextVisible,
  repostContextHTML,
} from "./reply-feed-context.js";

function hex64(repeated2) {
  return repeated2.repeat(32);
}

describe("reply-feed-context", () => {
  it("detects thread replies and hides quote posts", () => {
    const root = hex64("aa");
    const parent = hex64("bb");
    const replyID = hex64("cc");
    const author = hex64("01");

    const reply = {
      id: replyID,
      pubkey: author,
      kind: 1,
      tags: [
        ["e", root, "", "root"],
        ["e", parent, "", "reply"],
        ["p", hex64("02"), ""],
      ],
      content: "hi",
    };
    assert.equal(isFeedThreadReply(reply), true);
    assert.equal(replyContextVisible(reply), true);

    const rootNote = {
      id: root,
      pubkey: author,
      kind: 1,
      tags: [],
      content: "root",
    };
    assert.equal(isFeedThreadReply(rootNote), false);

    const quote = {
      ...reply,
      tags: [...reply.tags, ["q", parent]],
    };
    assert.equal(isFeedThreadReply(quote), true);
    assert.equal(replyContextVisible(quote), false);
  });

  it("dedupes reply targets and skips author", () => {
    const author = hex64("01");
    const other = hex64("02");
    const targets = replyContextTargets({
      pubkey: author,
      tags: [
        ["p", other, ""],
        ["p", author, ""],
        ["p", other, ""],
      ],
    });
    assert.deepEqual(targets, [other]);
  });

  it("builds reply context HTML with mentions", () => {
    const root = hex64("aa");
    const parent = hex64("bb");
    const bob = hex64("02");
    const carol = hex64("03");
    const profiles = {
      [bob]: { pubkey: bob, display_name: "Bob" },
      [carol]: { pubkey: carol, display_name: "Carol" },
    };
    const html = replyContextHTML(
      {
        id: hex64("cc"),
        pubkey: hex64("01"),
        kind: 1,
        tags: [
          ["e", root, "", "root"],
          ["e", parent, "", "reply"],
          ["p", bob, ""],
          ["p", carol, ""],
        ],
      },
      profiles,
    );
    assert.match(html, /Replying to/);
    assert.match(html, /@Bob/);
    assert.match(html, /@Carol/);
    assert.match(html, new RegExp(`href="${profilePath(bob).replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}"`));
  });

  it("falls back to thread link without p tags", () => {
    const root = hex64("aa");
    const parent = hex64("bb");
    const html = replyContextHTML({
      id: hex64("cc"),
      pubkey: hex64("01"),
      kind: 1,
      tags: [
        ["e", root, "", "root"],
        ["e", parent, "", "reply"],
      ],
    });
    assert.match(html, new RegExp(`href="/thread/${parent}"`));
    assert.match(html, />thread</);
  });

  it("builds repost banner HTML", () => {
    const alice = hex64("0a");
    const html = repostContextHTML(
      {
        id: hex64("f0"),
        pubkey: alice,
        kind: 6,
        tags: [["e", hex64("aa"), ""]],
        content: "",
      },
      { [alice]: { pubkey: alice, display_name: "Alice" } },
    );
    assert.match(html, /Alice reposted/);
    assert.match(html, /note-feed-context-repost-inner/);
  });
});
