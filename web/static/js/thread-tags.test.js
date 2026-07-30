import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  parentID,
  rootIDForEvent,
  threadExpectsFocusView,
  directReplyEvents,
  pageDirectReplies,
  effectiveThreadParentID,
  focusParentEvent,
} from "./thread-tags.js";

describe("thread-tags", () => {
  it("finds root id from nip-10 markers", () => {
    const root = "aa".repeat(32);
    const reply = "bb".repeat(32);
    const event = {
      id: reply,
      tags: [
        ["e", root, "", "root"],
        ["e", reply, "", "reply"],
      ],
    };
    assert.equal(rootIDForEvent(event), root);
    assert.equal(parentID(root, event), reply);
  });

  it("uses last e-tag as parent when positional markers are absent", () => {
    const root = "aa".repeat(32);
    const mention = "dd".repeat(32);
    const parent = "bb".repeat(32);
    const event = {
      id: "cc".repeat(32),
      tags: [
        ["e", root],
        ["e", mention],
        ["e", parent],
      ],
    };
    assert.equal(parentID(root, event), parent);
  });

  it("does not classify a mention-tagged quote as a thread reply", () => {
    const quoted = "aa".repeat(32);
    const quote = {
      id: "bb".repeat(32),
      kind: 1,
      tags: [
        ["e", quoted, "wss://relay.example", "mention", "cc".repeat(32)],
        ["q", quoted, "wss://relay.example", "cc".repeat(32)],
      ],
    };
    const quotedEvent = { id: quoted, kind: 1, tags: [] };
    assert.equal(rootIDForEvent(quote), quote.id);
    assert.equal(parentID("", quote), "");
    assert.equal(threadExpectsFocusView(quotedEvent, quote), false);
  });

  it("uses NIP-22 uppercase root and lowercase parent for comments", () => {
    const root = "aa".repeat(32);
    const parent = "bb".repeat(32);
    const event = {
      id: "cc".repeat(32),
      kind: 1111,
      tags: [
        ["E", root, "wss://root.example", "dd".repeat(32)],
        ["e", parent, "wss://parent.example", "ee".repeat(32)],
      ],
    };
    assert.equal(rootIDForEvent(event), root);
    assert.equal(parentID(root, event), parent);
  });

  it("detects focused reply view and direct children", () => {
    const root = { id: "aa".repeat(32), tags: [] };
    const reply = {
      id: "bb".repeat(32),
      created_at: 2,
      kind: 1,
      tags: [
        ["e", root.id, "", "root"],
        ["e", root.id, "", "reply"],
      ],
    };
    const sibling = {
      id: "cc".repeat(32),
      created_at: 3,
      kind: 1,
      tags: [
        ["e", root.id, "", "root"],
        ["e", root.id, "", "reply"],
      ],
    };
    assert.equal(threadExpectsFocusView(root, root), false);
    assert.equal(threadExpectsFocusView(root, reply), true);
    const parentByID = {
      [reply.id]: root.id,
      [sibling.id]: root.id,
    };
    const direct = directReplyEvents([root, reply, sibling], parentByID, root.id, root.id);
    assert.deepEqual(
      direct.map((event) => event.id),
      [reply.id, sibling.id],
    );
  });

  it("excludes reaction events from visible direct replies", () => {
    const root = { id: "aa".repeat(32), tags: [], kind: 1 };
    const reply = {
      id: "bb".repeat(32),
      created_at: 2,
      kind: 1,
      tags: [
        ["e", root.id, "", "root"],
        ["e", root.id, "", "reply"],
      ],
    };
    const reaction = {
      id: "cc".repeat(32),
      created_at: 3,
      kind: 7,
      content: "+",
      tags: [["e", root.id], ["p", "dd".repeat(32)]],
    };
    const parentByID = {
      [reply.id]: root.id,
      [reaction.id]: root.id,
    };
    const direct = directReplyEvents([root, reply, reaction], parentByID, root.id, root.id);
    assert.deepEqual(direct.map((event) => event.id), [reply.id]);
  });

  it("pages direct replies with stable cursors", () => {
    const replies = [
      { id: "b".repeat(64), created_at: 102 },
      { id: "a".repeat(64), created_at: 100 },
      { id: "c".repeat(64), created_at: 101 },
    ];
    const first = pageDirectReplies(replies, "", "", 2);
    assert.equal(first.items.length, 2);
    assert.equal(first.hasMore, true);
    const second = pageDirectReplies(replies, first.nextCursor, first.nextCursorId, 2);
    assert.equal(second.items.length, 1);
    assert.equal(second.hasMore, false);
  });

  it("treats single root e-tag replies as direct OP replies", () => {
    const root = { id: "aa".repeat(32) };
    const reply = {
      id: "bb".repeat(32),
      created_at: 2,
      kind: 1,
      tags: [["e", root.id]],
    };
    assert.equal(effectiveThreadParentID(root.id, reply, {}), root.id);
    assert.equal(threadExpectsFocusView(root, reply), true);
    const direct = directReplyEvents([root, reply], {}, root.id, root.id);
    assert.equal(direct.length, 1);
    assert.equal(direct[0].id, reply.id);
  });

  it("resolves the focus parent to the root for single-tag direct replies", () => {
    const root = { id: "aa".repeat(32), tags: [] };
    const reply = {
      id: "bb".repeat(32),
      created_at: 2,
      kind: 1,
      tags: [["e", root.id]],
    };
    assert.equal(focusParentEvent(root, reply, [root, reply], {}).id, root.id);
  });

  it("prefers repaired parent mapping when selected tags are ambiguous", () => {
    const root = { id: "aa".repeat(32), tags: [] };
    const actualParent = {
      id: "bb".repeat(32),
      created_at: 2,
      kind: 1,
      tags: [["e", root.id]],
    };
    const selected = {
      id: "cc".repeat(32),
      created_at: 3,
      kind: 1,
      tags: [["e", root.id]],
    };
    const parentByID = { [selected.id]: actualParent.id };
    assert.equal(
      focusParentEvent(root, selected, [root, actualParent, selected], parentByID).id,
      actualParent.id,
    );
  });
});
