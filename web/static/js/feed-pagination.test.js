import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { isBeforeFeedCursor } from "./feed-pagination.js";
import {
  countUnseenFeedEvents,
  feedPageCursor,
  feedPageMayHaveMore,
  feedPaginationCursorFromDatasets,
  homeFeedLoadMoreHidden,
} from "./feed-pagination.js";

describe("feed pagination cursors", () => {
  it("maps load-more datasets to composite created_at/id cursor", () => {
    const id = "aa".repeat(32);
    assert.deepEqual(feedPaginationCursorFromDatasets({ until: 99, untilID: id }), {
      beforeCreatedAt: 100,
      beforeID: id,
    });
    assert.deepEqual(feedPaginationCursorFromDatasets({ until: 0, untilID: "" }), {
      beforeCreatedAt: undefined,
      beforeID: undefined,
    });
  });

  it("round-trips feedPageCursor through dataset conversion", () => {
    const id = "bb".repeat(32);
    const page = [{ id, created_at: 100 }];
    const cursor = feedPageCursor(page);
    const composite = feedPaginationCursorFromDatasets({
      until: cursor.until,
      untilID: cursor.cursorId,
    });
    assert.equal(composite.beforeCreatedAt, 100);
    assert.equal(composite.beforeID, id);
  });

  it("keeps client pagination open for thin pages with an older cursor", () => {
    assert.equal(feedPageMayHaveMore([]), false);
    assert.equal(feedPageMayHaveMore([{ id: "aa".repeat(32), created_at: 0 }]), false);
    assert.equal(feedPageMayHaveMore([{ id: "bb".repeat(32), created_at: 100 }]), true);
  });

  it("keeps the home load-more button hidden while a feed refresh is pending", () => {
    assert.equal(homeFeedLoadMoreHidden({ hasMore: false }), true);
    assert.equal(homeFeedLoadMoreHidden({ hasMore: true, isPending: true }), true);
    assert.equal(homeFeedLoadMoreHidden({ hasMore: true, isLoading: true }), true);
    assert.equal(homeFeedLoadMoreHidden({ hasMore: true, hasLoader: true }), true);
    assert.equal(homeFeedLoadMoreHidden({ hasMore: true }), false);
  });

  it("counts unseen notes in a staged ranked-feed refresh", () => {
    const visible = ["aa".repeat(32), "bb".repeat(32)];
    const refreshed = [
      { id: "AA".repeat(32) },
      { id: "cc".repeat(32) },
      { id: "dd".repeat(32) },
      { id: "" },
    ];
    assert.equal(countUnseenFeedEvents(refreshed, visible), 2);
  });

  it("applies stable composite ordering for equal created_at", () => {
    const older = { id: "aa".repeat(32), created_at: 100 };
    const newer = { id: "bb".repeat(32), created_at: 100 };
    assert.equal(isBeforeFeedCursor(older, 100, newer.id), true);
    assert.equal(isBeforeFeedCursor(newer, 100, older.id), false);
    assert.equal(isBeforeFeedCursor(newer, 101, older.id), true);
  });
});
