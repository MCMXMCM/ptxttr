import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { buildThreadSelected, buildThreadDirectReplyCounts, linearThreadReplyEvents, linearThreadReplyNodes } from "./thread-view.js";

function event(id, created_at = 1, tags = []) {
  return { id, pubkey: "aa".repeat(32), created_at, kind: 1, tags, content: id };
}

describe("thread-view", () => {
  it("shows top-level OP replies in root thread view (iOS thread.nodes)", () => {
    const root = event("aa".repeat(32), 1);
    const reply = event("bb".repeat(32), 2, [
      ["e", root.id, "", "root"],
      ["e", root.id, "", "reply"],
    ]);
    const nested = event("cc".repeat(32), 3, [
      ["e", root.id, "", "root"],
      ["e", reply.id, "", "reply"],
    ]);
    const parentByID = {
      [reply.id]: root.id,
      [nested.id]: reply.id,
    };
    const view = buildThreadSelected(root, root, [reply, nested], parentByID);
    assert.equal(view.focusMode, false);
    const linear = linearThreadReplyNodes(view);
    assert.deepEqual(
      linear.map((node) => node.event.id),
      [reply.id],
    );
    assert.deepEqual(
      linearThreadReplyEvents(root, root, [root, reply, nested], parentByID).map((ev) => ev.id),
      [reply.id],
    );
  });

  it("shows selected descendants in focus mode (iOS selectedNode.children)", () => {
    const root = event("aa".repeat(32), 1);
    const selected = event("bb".repeat(32), 2, [
      ["e", root.id, "", "root"],
      ["e", root.id, "", "reply"],
    ]);
    const child = event("cc".repeat(32), 3, [
      ["e", root.id, "", "root"],
      ["e", selected.id, "", "reply"],
    ]);
    const parentByID = {
      [selected.id]: root.id,
      [child.id]: selected.id,
    };
    const view = buildThreadSelected(root, selected, [selected, child], parentByID);
    assert.equal(view.focusMode, true);
    const linear = linearThreadReplyNodes(view);
    assert.deepEqual(
      linear.map((node) => node.event.id),
      [child.id],
    );
  });

  it("counts direct replies from the built tree", () => {
    const root = event("aa".repeat(32), 1);
    const reply = event("bb".repeat(32), 2, [
      ["e", root.id, "", "root"],
      ["e", root.id, "", "reply"],
    ]);
    const nested = event("cc".repeat(32), 3, [
      ["e", root.id, "", "root"],
      ["e", reply.id, "", "reply"],
    ]);
    const parentByID = {
      [reply.id]: root.id,
      [nested.id]: reply.id,
    };
    const all = [root, reply, nested];
    const view = buildThreadSelected(root, root, [reply, nested], parentByID);
    const counts = buildThreadDirectReplyCounts(view, all);
    assert.equal(counts[root.id], 1);
    assert.equal(counts[reply.id], 1);
    assert.equal(counts[nested.id], 0);
  });

  it("falls back to direct replies when selected is missing from the tree", () => {
    const root = event("aa".repeat(32), 1);
    const selected = event("bb".repeat(32), 2);
    const child = event("cc".repeat(32), 3, [
      ["e", root.id, "", "root"],
      ["e", selected.id, "", "reply"],
    ]);
    const parentByID = { [child.id]: selected.id };
    const events = linearThreadReplyEvents(root, selected, [root, child], parentByID);
    assert.deepEqual(
      events.map((ev) => ev.id),
      [child.id],
    );
  });

  it("shows direct children in focus mode when selected is in the tree", () => {
    const root = event("aa".repeat(32), 1);
    const selected = event("bb".repeat(32), 2, [
      ["e", root.id, "", "root"],
      ["e", root.id, "", "reply"],
    ]);
    const child = event("cc".repeat(32), 3, [["e", selected.id, "", "reply"]]);
    const parentByID = {
      [selected.id]: root.id,
      [child.id]: selected.id,
    };
    const all = [root, selected, child];
    const linear = linearThreadReplyEvents(root, selected, all, parentByID);
    assert.deepEqual(
      linear.map((ev) => ev.id),
      [child.id],
    );
  });
});
