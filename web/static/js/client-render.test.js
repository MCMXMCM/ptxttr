import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { cachedThreadRoutePreviewState } from "./thread-preview-state.js";
import { relayNativeThreadMissingBundleAction } from "./thread-route-fallback.js";
import {
  buildProfileHydrationRelayStages,
  fetchProfileFollowGraphAcrossRelayStages,
  fetchInitialProfilePostsAcrossRelayStages,
} from "./profile-hydration.js";

import { renderedFeedSort, renderedFeedSortChanged } from "./feed-render-state.js";

function event(id, tags = []) {
  return { id, pubkey: "aa".repeat(32), kind: 1, created_at: 1, tags, content: id };
}

describe("cachedThreadRoutePreviewState", () => {
  it("does not render a preview when the selected note is missing", () => {
    assert.deepEqual(cachedThreadRoutePreviewState(null), {
      rendered: false,
      selectedID: "",
      rootID: "",
      isReply: false,
      parentLoaded: false,
      showsParentSkeleton: false,
    });
  });

  it("renders a cached selected note without a parent skeleton for root notes", () => {
    const root = event("a".repeat(64));
    assert.deepEqual(cachedThreadRoutePreviewState(root), {
      rendered: true,
      selectedID: root.id,
      rootID: root.id,
      isReply: false,
      parentLoaded: false,
      showsParentSkeleton: false,
    });
  });

  it("marks missing parents for cached reply previews", () => {
    const rootID = "a".repeat(64);
    const reply = event("b".repeat(64), [
      ["e", rootID, "", "root"],
      ["e", rootID, "", "reply"],
    ]);
    const state = cachedThreadRoutePreviewState(reply);
    assert.equal(state.rendered, true);
    assert.equal(state.selectedID, reply.id);
    assert.equal(state.rootID, rootID);
    assert.equal(state.isReply, true);
    assert.equal(state.parentLoaded, false);
    assert.equal(state.showsParentSkeleton, true);
  });

  it("suppresses the parent skeleton when the direct parent is already cached", () => {
    const rootID = "a".repeat(64);
    const parent = event("c".repeat(64), [
      ["e", rootID, "", "root"],
      ["e", rootID, "", "reply"],
    ]);
    const reply = event("b".repeat(64), [
      ["e", rootID, "", "root"],
      ["e", parent.id, "", "reply"],
    ]);
    const state = cachedThreadRoutePreviewState(reply, parent);
    assert.equal(state.rendered, true);
    assert.equal(state.parentLoaded, true);
    assert.equal(state.showsParentSkeleton, false);
  });
});

describe("relayNativeThreadMissingBundleAction", () => {
  it("shows a not-found state when no full thread render exists", () => {
    assert.equal(
      relayNativeThreadMissingBundleAction({
        serverRendered: false,
      }),
      "not-found",
    );
  });

  it("keeps an already rendered server thread instead of replacing it", () => {
    assert.equal(
      relayNativeThreadMissingBundleAction({
        serverRendered: true,
      }),
      "keep-rendered",
    );
  });

  it("keeps any rendered preview instead of replacing it with not found", () => {
    assert.equal(
      relayNativeThreadMissingBundleAction({
        previewComplete: true,
        serverRendered: false,
      }),
      "keep-rendered",
    );
    assert.equal(
      relayNativeThreadMissingBundleAction({
        previewRendered: true,
        previewComplete: false,
        serverRendered: false,
      }),
      "keep-rendered",
    );
  });
});

describe("renderedFeedSortChanged", () => {
  it("detects hard-reload SSR feed sort mismatch from data-feed-sort", () => {
    const feed = { dataset: { feedSort: "recent" } };
    assert.equal(renderedFeedSort(feed), "recent");
    assert.equal(renderedFeedSortChanged(feed, "trend7d"), true);
    assert.equal(renderedFeedSortChanged(feed, "recent"), false);
  });

  it("prefers client-rendered feed sort once present", () => {
    const feed = { dataset: { feedSort: "recent", relayNativeFeedSort: "trend7d" } };
    assert.equal(renderedFeedSort(feed), "trend7d");
    assert.equal(renderedFeedSortChanged(feed, "trend7d"), false);
  });
});

describe("profile relay-stage hydration", () => {
  it("builds progressively wider relay stages without duplicates", () => {
    const target = "ab".repeat(32);
    const followHints = new Map([[target, "wss://follow.example"]]);
    assert.deepEqual(
      buildProfileHydrationRelayStages(
        ["wss://profile-link.example"],
        {
          read: ["wss://hint-read.example"],
          write: ["wss://hint-write.example"],
          any: ["wss://shared.example"],
        },
        followHints,
        target,
        ["wss://base.example"],
      ),
      [
        ["wss://profile-link.example"],
        ["wss://hint-write.example", "wss://shared.example"],
        ["wss://follow.example"],
        ["wss://base.example"],
      ],
    );
  });

  it("tries wider relay stages until profile posts are found", async () => {
    const attempted = [];
    const result = await fetchInitialProfilePostsAcrossRelayStages(
      "ab".repeat(32),
      [
        ["wss://base.example"],
        ["wss://hint.example"],
        ["wss://fallback.example"],
      ],
      {
        fetchPosts: async (_pubkey, { relays }) => {
          attempted.push(relays);
          if (relays[0] === "wss://fallback.example") {
            return [{ id: "note-1" }];
          }
          return [];
        },
      },
    );

    assert.deepEqual(attempted, [
      ["wss://base.example"],
      ["wss://hint.example"],
      ["wss://fallback.example"],
    ]);
    assert.deepEqual(result, {
      posts: [{ id: "note-1" }],
      relaysUsed: ["wss://fallback.example"],
    });
  });

  it("merges fresh posts from default and author outbox relays while tolerating a failed stage", async () => {
    const result = await fetchInitialProfilePostsAcrossRelayStages(
      "ab".repeat(32),
      [
        ["wss://base.example"],
        ["wss://outbox.example"],
        ["wss://offline.example"],
      ],
      {
        fetchPosts: async (_pubkey, { relays }) => {
          if (relays[0] === "wss://offline.example") throw new Error("offline");
          if (relays[0] === "wss://outbox.example") {
            return [{ id: "new-note", created_at: 20 }];
          }
          return [{ id: "old-note", created_at: 10 }];
        },
      },
    );

    assert.deepEqual(result.posts.map((post) => post.id), ["new-note", "old-note"]);
    assert.deepEqual(result.relaysUsed, ["wss://base.example", "wss://outbox.example"]);
  });

  it("merges follow data across relay stages using the newest follow list and unioned followers", async () => {
    const pubkey = "ab".repeat(32);
    const attempted = [];
    const result = await fetchProfileFollowGraphAcrossRelayStages(
      pubkey,
      [
        ["wss://base.example"],
        ["wss://hint.example"],
        ["wss://fallback.example"],
      ],
      {
        fetchFollowGraph: async (_pubkey, { relays }) => {
          attempted.push(relays);
          if (relays[0] === "wss://base.example") {
            return {
              pubkey,
              following: [],
              followers: ["11".repeat(32)],
              followEvent: null,
              relayHints: new Map(),
            };
          }
          if (relays[0] === "wss://hint.example") {
            return {
              pubkey,
              following: ["22".repeat(32)],
              followers: ["33".repeat(32)],
              followEvent: { id: "follow-1", created_at: 10 },
              relayHints: new Map([[pubkey, "wss://hint.example"]]),
            };
          }
          return {
            pubkey,
            following: ["44".repeat(32)],
            followers: ["33".repeat(32), "55".repeat(32)],
            followEvent: { id: "follow-2", created_at: 20 },
            relayHints: new Map([[pubkey, "wss://fallback.example"]]),
          };
        },
      },
    );

    assert.deepEqual(attempted, [
      ["wss://base.example"],
      ["wss://hint.example"],
      ["wss://fallback.example"],
    ]);
    assert.deepEqual(result, {
      pubkey,
      following: ["44".repeat(32)],
      followers: ["11".repeat(32), "33".repeat(32), "55".repeat(32)],
      followEvent: { id: "follow-2", created_at: 20 },
      relayHints: new Map([[pubkey, "wss://fallback.example"]]),
      relaysUsed: ["wss://fallback.example"],
    });
  });

  it("keeps relationship data when one relay stage fails", async () => {
    const pubkey = "ab".repeat(32);
    const result = await fetchProfileFollowGraphAcrossRelayStages(
      pubkey,
      [["wss://offline.example"], ["wss://outbox.example"]],
      {
        fetchFollowGraph: async (_pubkey, { relays }) => {
          if (relays[0] === "wss://offline.example") throw new Error("offline");
          return {
            pubkey,
            following: ["22".repeat(32)],
            followers: ["33".repeat(32)],
            followEvent: { id: "follow-1", created_at: 10 },
            relayHints: new Map(),
          };
        },
      },
    );

    assert.deepEqual(result.following, ["22".repeat(32)]);
    assert.deepEqual(result.followers, ["33".repeat(32)]);
  });
});
