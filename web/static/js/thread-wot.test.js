import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  buildFilteredReplyNodes,
  excludeVisibleReplies,
  filteredReplyRailDepth,
  isDirectThreadReply,
  partitionRepliesByWoT,
  partitionThreadRepliesByWoT,
} from "./thread-wot-partition.js";
import { threadWoTDepthForViewer } from "./thread-wot-prefs.js";

const ROOT_ID = "11".repeat(32);
const SELECTED_ID = "22".repeat(32);
const TRUSTED_ID = "33".repeat(32);
const EXCLUDED_ID = "44".repeat(32);
const AUTHOR_A = "aa".repeat(32);
const AUTHOR_B = "bb".repeat(32);
const AUTHOR_TRUSTED = "cc".repeat(32);
const AUTHOR_STRANGER = "dd".repeat(32);

function event(id, pubkey, tags = []) {
  return { id, pubkey, kind: 1, content: "", created_at: 1, tags, sig: "sig" };
}

describe("thread-wot", () => {
  it("uses three hops for guests without changing signed-in thread depth", () => {
    assert.equal(threadWoTDepthForViewer("", 1), 3);
    assert.equal(threadWoTDepthForViewer(AUTHOR_A, 2), 2);
  });

  it("preserves root, selected, and ancestor context in trusted replies", () => {
    const root = event(ROOT_ID, AUTHOR_A);
    const selected = event(SELECTED_ID, AUTHOR_B, [
      ["e", ROOT_ID, "", "root"],
      ["e", SELECTED_ID, "", "reply"],
    ]);
    const trustedReply = event(TRUSTED_ID, AUTHOR_TRUSTED, [
      ["e", SELECTED_ID, "", "reply"],
      ["e", ROOT_ID, "", "root"],
    ]);
    const excludedReply = event(EXCLUDED_ID, AUTHOR_STRANGER, [
      ["e", SELECTED_ID, "", "reply"],
      ["e", ROOT_ID, "", "root"],
    ]);
    const membership = new Set([AUTHOR_A, AUTHOR_B, AUTHOR_TRUSTED]);
    const { trusted, excluded } = partitionRepliesByWoT(
      [trustedReply, excludedReply, selected],
      ROOT_ID,
      root,
      selected,
      null,
      membership,
      null,
    );
    assert.equal(trusted.length, 2);
    assert.equal(excluded.length, 1);
    assert.equal(excluded[0].id, EXCLUDED_ID);
  });

  it("bridges untrusted parents needed for trusted tree paths", () => {
    const bridgeID = "55".repeat(32);
    const childID = "66".repeat(32);
    const trustedChild = event(childID, AUTHOR_TRUSTED, [
      ["e", bridgeID, "", "reply"],
      ["e", ROOT_ID, "", "root"],
    ]);
    const bridgeParent = event(bridgeID, AUTHOR_STRANGER, [["e", ROOT_ID, "", "root"]]);
    const partition = partitionThreadRepliesByWoT(
      [trustedChild, bridgeParent],
      ROOT_ID,
      event(ROOT_ID, AUTHOR_A),
      event(ROOT_ID, AUTHOR_A),
      null,
      { [childID]: bridgeID, [bridgeID]: ROOT_ID },
      new Set([AUTHOR_TRUSTED]),
      null,
    );
    assert.equal(partition.treeReplies.length, 2);
    assert.ok(partition.treeReplies.some((row) => row.id === bridgeID));
  });

  it("removes visible bridge replies from the filtered reveal list", () => {
    const bridgeID = "55".repeat(32);
    const childID = "66".repeat(32);
    const bridgeParent = event(bridgeID, AUTHOR_STRANGER, [["e", ROOT_ID, "", "root"]]);
    const trustedChild = event(childID, AUTHOR_TRUSTED, [
      ["e", bridgeID, "", "reply"],
      ["e", ROOT_ID, "", "root"],
    ]);
    const hidden = excludeVisibleReplies([bridgeParent], [trustedChild, bridgeParent]);
    assert.deepEqual(hidden, []);
  });

  it("keeps filtered replies limited to direct replies to the focus", () => {
    const root = event(ROOT_ID, AUTHOR_A);
    const selected = event(SELECTED_ID, AUTHOR_B);
    const directExcluded = event(EXCLUDED_ID, AUTHOR_STRANGER, [
      ["e", SELECTED_ID, "", "reply"],
      ["e", ROOT_ID, "", "root"],
    ]);
    const nestedExcluded = event("77".repeat(32), "ee".repeat(32), [
      ["e", EXCLUDED_ID, "", "reply"],
      ["e", ROOT_ID, "", "root"],
    ]);
    const partition = partitionThreadRepliesByWoT(
      [directExcluded, nestedExcluded],
      ROOT_ID,
      root,
      selected,
      null,
      { [nestedExcluded.id]: EXCLUDED_ID, [EXCLUDED_ID]: SELECTED_ID },
      new Set([AUTHOR_A, AUTHOR_B]),
      null,
    );
    assert.equal(partition.filteredReplies.length, 1);
    assert.equal(partition.filteredReplies[0].id, EXCLUDED_ID);
  });

  it("detects direct thread replies", () => {
    const reply = event("88".repeat(32), AUTHOR_B, [
      ["e", SELECTED_ID, "", "reply"],
      ["e", ROOT_ID, "", "root"],
    ]);
    assert.equal(isDirectThreadReply(reply, SELECTED_ID, ROOT_ID, null), true);
    assert.equal(isDirectThreadReply(reply, ROOT_ID, ROOT_ID, null), false);
  });

  it("builds filtered reply nodes with depth and parent", () => {
    const nodes = buildFilteredReplyNodes([event(TRUSTED_ID, AUTHOR_TRUSTED)], 2, SELECTED_ID);
    assert.equal(nodes.length, 1);
    assert.equal(nodes[0].depth, 2);
    assert.equal(nodes[0].parentID, SELECTED_ID);
  });

  it("aligns focused filtered replies with visible direct replies", () => {
    assert.equal(filteredReplyRailDepth(true, 2, false), 1);
    assert.equal(filteredReplyRailDepth(true, 5, false), 1);
    assert.equal(filteredReplyRailDepth(false, 2, false), 2);
  });
});
