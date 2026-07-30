import assert from "node:assert/strict";
import { beforeEach, describe, it } from "node:test";

import {
  clearServerFeedMetadataForTests,
  mergeServerReplyCounts,
  rememberServerFeedMetadata,
  serverReplyCountsForEvents,
} from "./server-feed-metadata.js";

beforeEach(() => clearServerFeedMetadataForTests());

describe("server feed metadata", () => {
  it("preserves projected reply counts as a floor for incomplete relay reads", () => {
    const id = "a".repeat(64);
    rememberServerFeedMetadata({ reply_counts: { [id]: 15 } });

    assert.deepEqual(serverReplyCountsForEvents([{ id }]), { [id]: 15 });
    assert.deepEqual(mergeServerReplyCounts([id], { [id]: 0 }), { [id]: 15 });
    assert.deepEqual(mergeServerReplyCounts([id], { [id]: 18 }), { [id]: 18 });
  });
});
