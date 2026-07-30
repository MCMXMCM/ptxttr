import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { bundleHasVisibleThreadReplies } from "./thread-view.js";

const ROOT_ID = "11".repeat(32);
const SELECTED_ID = "22".repeat(32);
const TRUSTED_REPLY_ID = "33".repeat(32);
const EXCLUDED_REPLY_ID = "44".repeat(32);
const AUTHOR_A = "aa".repeat(32);
const AUTHOR_B = "bb".repeat(32);
const AUTHOR_TRUSTED = "cc".repeat(32);
const AUTHOR_STRANGER = "dd".repeat(32);

function event(id, pubkey, tags = []) {
  return { id, pubkey, kind: 1, content: "", created_at: 1, tags, sig: "sig" };
}

function bundle(root, selected, replies, parentByID = {}) {
  const events = [root, ...(selected.id !== root.id ? [selected] : []), ...replies];
  return {
    root,
    selected,
    rootID: root.id,
    selectedID: selected.id,
    events,
    parentByID,
  };
}

describe("thread-replies-skeleton", () => {
  it("returns false when WoT filters away all direct replies", () => {
    const root = event(ROOT_ID, AUTHOR_A);
    const selected = root;
    const trustedReply = event(TRUSTED_REPLY_ID, AUTHOR_TRUSTED, [["e", ROOT_ID, "", "root"]]);
    const excludedReply = event(EXCLUDED_REPLY_ID, AUTHOR_STRANGER, [["e", ROOT_ID, "", "root"]]);
    const full = bundle(root, selected, [trustedReply, excludedReply], {
      [TRUSTED_REPLY_ID]: ROOT_ID,
      [EXCLUDED_REPLY_ID]: ROOT_ID,
    });
    const membership = new Set([AUTHOR_A, AUTHOR_TRUSTED]);

    assert.equal(
      bundleHasVisibleThreadReplies(full, ROOT_ID, { wotEnabled: false }),
      true,
    );
    assert.equal(
      bundleHasVisibleThreadReplies(full, ROOT_ID, { wotEnabled: true, membership }),
      true,
    );

    const onlyExcluded = bundle(root, selected, [excludedReply], { [EXCLUDED_REPLY_ID]: ROOT_ID });
    assert.equal(
      bundleHasVisibleThreadReplies(onlyExcluded, ROOT_ID, { wotEnabled: true, membership }),
      false,
    );
  });

  it("returns false when focus has no direct replies", () => {
    const root = event(ROOT_ID, AUTHOR_A);
    const selected = event(SELECTED_ID, AUTHOR_B, [
      ["e", ROOT_ID, "", "root"],
      ["e", SELECTED_ID, "", "reply"],
    ]);
    const empty = bundle(root, selected, [], { [SELECTED_ID]: ROOT_ID });
    const membership = new Set([AUTHOR_A, AUTHOR_B]);

    assert.equal(
      bundleHasVisibleThreadReplies(empty, SELECTED_ID, { wotEnabled: true, membership }),
      false,
    );
  });
});
