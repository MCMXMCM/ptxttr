import assert from "node:assert/strict";
import { afterEach, beforeEach, describe, it } from "node:test";

import {
  listHasTag,
  newerListEvent,
  nextReplaceableCreatedAt,
  setReplaceableListTestHooks,
  toggledListTags,
  updateReplaceableList,
} from "./replaceable-list.js";

const VIEWER = "a".repeat(64);
const FRIEND = "b".repeat(64);
const OTHER = "c".repeat(64);
const NEWCOMER = "d".repeat(64);
const NOW = 1_800_000_000;

function contactList(tags, extra = {}) {
  return { id: "1".repeat(64), pubkey: VIEWER, kind: 3, created_at: NOW - 60, tags, content: "", ...extra };
}

function installHooks({ latest = null, confirm } = {}) {
  const published = [];
  const fetched = [];
  let signed = 0;
  setReplaceableListTestHooks({
    fetchLatest: async (pubkey, kind) => {
      fetched.push({ pubkey, kind });
      return typeof latest === "function" ? latest() : latest;
    },
    sign: async (draft) => {
      signed += 1;
      return { ...draft, pubkey: VIEWER, id: String(signed).padStart(64, "0"), sig: "sig" };
    },
    publish: async (event) => {
      published.push(event);
    },
    now: () => NOW,
  });
  return { published, fetched, confirm };
}

beforeEach(() => setReplaceableListTestHooks());
afterEach(() => setReplaceableListTestHooks());

describe("updateReplaceableList", () => {
  it("adds one entry to the newest list and keeps every other tag and the content", async () => {
    const existing = contactList([
      ["p", FRIEND, "wss://relay.example", "bob"],
      ["t", "nostr"],
      ["client", "Another Client"],
      ["p", OTHER],
    ], { content: "{\"wss://relay.example\":{\"read\":true}}" });
    const { published, fetched } = installHooks({ latest: existing });

    const result = await updateReplaceableList({ pubkey: VIEWER, kind: 3, tag: ["p", NEWCOMER], enabled: true });

    assert.deepEqual(fetched, [{ pubkey: VIEWER, kind: 3 }]);
    assert.equal(result.changed, true);
    assert.equal(published.length, 1);
    assert.deepEqual(published[0].tags, [
      ["p", FRIEND, "wss://relay.example", "bob"],
      ["t", "nostr"],
      ["p", OTHER],
      ["p", NEWCOMER],
    ]);
    assert.equal(published[0].content, existing.content);
    assert.equal(published[0].kind, 3);
    assert.equal(published[0].created_at, NOW);
  });

  it("removes every row for the entry and leaves the rest untouched", async () => {
    const { published } = installHooks({
      latest: contactList([["p", FRIEND], ["p", OTHER, "wss://relay.example"], ["p", FRIEND.toUpperCase()]]),
    });

    await updateReplaceableList({ pubkey: VIEWER, kind: 3, tag: ["p", FRIEND], enabled: false });

    assert.deepEqual(published[0].tags, [["p", OTHER, "wss://relay.example"]]);
  });

  it("does not publish when the list already has the requested state", async () => {
    const existing = contactList([["p", FRIEND]]);
    const { published } = installHooks({ latest: existing });

    const followed = await updateReplaceableList({ pubkey: VIEWER, kind: 3, tag: ["p", FRIEND], enabled: true });
    const unfollowed = await updateReplaceableList({ pubkey: VIEWER, kind: 3, tag: ["p", OTHER], enabled: false });

    assert.equal(published.length, 0);
    assert.equal(followed.changed, false);
    assert.equal(followed.event, existing);
    assert.equal(unfollowed.changed, false);
  });

  it("asks before creating a new list and publishes nothing when declined", async () => {
    const { published } = installHooks({ latest: null });
    const asked = [];

    const result = await updateReplaceableList({
      pubkey: VIEWER,
      kind: 3,
      tag: ["p", FRIEND],
      enabled: true,
      confirm: (current) => {
        asked.push(current);
        return false;
      },
    });

    assert.deepEqual(asked, [null]);
    assert.equal(published.length, 0);
    assert.deepEqual(result, { event: null, changed: false });
  });

  it("creates a new list once the user confirms", async () => {
    const { published } = installHooks({ latest: null });

    await updateReplaceableList({ pubkey: VIEWER, kind: 3, tag: ["p", FRIEND], enabled: true, confirm: () => true });

    assert.deepEqual(published[0].tags, [["p", FRIEND]]);
    assert.equal(published[0].content, "");
  });

  it("does not ask or publish when removing from a list that does not exist", async () => {
    const { published } = installHooks({ latest: null });
    let asked = false;

    await updateReplaceableList({
      pubkey: VIEWER,
      kind: 3,
      tag: ["p", FRIEND],
      enabled: false,
      confirm: () => {
        asked = true;
        return true;
      },
    });

    assert.equal(asked, false);
    assert.equal(published.length, 0);
  });

  it("treats a list signed by someone else as missing", async () => {
    installHooks({ latest: { ...contactList([["p", FRIEND]]), pubkey: OTHER } });
    const asked = [];

    await updateReplaceableList({
      pubkey: VIEWER,
      kind: 3,
      tag: ["p", NEWCOMER],
      enabled: true,
      confirm: (current) => {
        asked.push(current);
        return false;
      },
    });

    assert.deepEqual(asked, [null]);
  });

  it("never truncates large follow lists", async () => {
    const tags = Array.from({ length: 800 }, (_, index) => ["p", index.toString(16).padStart(64, "0")]);
    const { published } = installHooks({ latest: contactList(tags) });

    await updateReplaceableList({ pubkey: VIEWER, kind: 3, tag: ["p", NEWCOMER], enabled: true });

    assert.equal(published[0].tags.length, 801);
    assert.deepEqual(published[0].tags.at(-1), ["p", NEWCOMER]);
  });

  it("keeps a/t/r tags, private content and entries past the display limit when bookmarking", async () => {
    const noteIDs = Array.from({ length: 520 }, (_, index) => (index + 1).toString(16).padStart(64, "0"));
    const bookmarks = {
      id: "2".repeat(64),
      pubkey: VIEWER,
      kind: 10003,
      created_at: NOW - 10,
      tags: [
        ["a", `30023:${OTHER}:article`],
        ["t", "nostr"],
        ["r", "https://example.com"],
        ...noteIDs.map((id) => ["e", id]),
      ],
      content: "nip04-encrypted-private-items",
    };
    const { published } = installHooks({ latest: bookmarks });
    const newNote = "f".repeat(64);

    await updateReplaceableList({ pubkey: VIEWER, kind: 10003, tag: ["e", newNote], enabled: true });

    assert.equal(published[0].tags.length, bookmarks.tags.length + 1);
    assert.deepEqual(published[0].tags.slice(0, 3), bookmarks.tags.slice(0, 3));
    assert.equal(published[0].content, "nip04-encrypted-private-items");
  });

  it("serializes writes so each one builds on the list the previous one published", async () => {
    // The relay read keeps returning the pre-write list, as a cached query would.
    const stale = contactList([["p", FRIEND]]);
    const { published } = installHooks({ latest: stale });

    await Promise.all([
      updateReplaceableList({ pubkey: VIEWER, kind: 3, tag: ["p", OTHER], enabled: true }),
      updateReplaceableList({ pubkey: VIEWER, kind: 3, tag: ["p", NEWCOMER], enabled: true }),
    ]);

    assert.equal(published.length, 2);
    assert.deepEqual(published[1].tags, [["p", FRIEND], ["p", OTHER], ["p", NEWCOMER]]);
    assert.ok(published[1].created_at > published[0].created_at);
  });

  it("enforces maxTags only when adding", async () => {
    const full = { ...contactList([["p", FRIEND], ["p", OTHER]]), kind: 10000 };
    const { published } = installHooks({ latest: full });

    await assert.rejects(
      updateReplaceableList({ pubkey: VIEWER, kind: 10000, tag: ["p", NEWCOMER], enabled: true, maxTags: 2 }),
      /tag limit/,
    );
    await updateReplaceableList({ pubkey: VIEWER, kind: 10000, tag: ["p", FRIEND], enabled: false, maxTags: 1 });

    assert.deepEqual(published.map((event) => event.tags), [[["p", OTHER]]]);
  });

  it("refuses to publish a list the signer signed as a different account", async () => {
    const { published } = installHooks({ latest: contactList([["p", FRIEND]]) });
    setReplaceableListTestHooks({
      fetchLatest: async () => contactList([["p", FRIEND]]),
      sign: async (draft) => ({ ...draft, pubkey: OTHER, id: "9".repeat(64), sig: "sig" }),
      publish: async (event) => published.push(event),
      now: () => NOW,
    });

    await assert.rejects(
      updateReplaceableList({ pubkey: VIEWER, kind: 3, tag: ["p", NEWCOMER], enabled: true }),
      /different account/,
    );
    assert.equal(published.length, 0);
  });

  it("does not publish when the latest list cannot be fetched", async () => {
    const { published } = installHooks({
      latest: () => {
        throw new Error("replaceable event request failed");
      },
    });

    await assert.rejects(
      updateReplaceableList({ pubkey: VIEWER, kind: 3, tag: ["p", FRIEND], enabled: true, confirm: () => true }),
      /request failed/,
    );
    assert.equal(published.length, 0);
  });
});

describe("replaceable list helpers", () => {
  it("toggledListTags drops client metadata tags and appends missing entries", () => {
    const event = contactList([["client", "Other"], ["p", FRIEND]]);
    assert.deepEqual(toggledListTags(event, ["p", OTHER], true), [["p", FRIEND], ["p", OTHER]]);
    assert.deepEqual(toggledListTags(event, ["p", FRIEND], true), [["p", FRIEND]]);
    assert.deepEqual(toggledListTags(null, ["p", FRIEND], true), [["p", FRIEND]]);
  });

  it("listHasTag matches case-insensitively", () => {
    assert.equal(listHasTag(contactList([["p", FRIEND.toUpperCase()]]), "p", FRIEND), true);
    assert.equal(listHasTag(null, "p", FRIEND), false);
  });

  it("nextReplaceableCreatedAt always moves past the replaced list", () => {
    assert.equal(nextReplaceableCreatedAt(null, NOW), NOW);
    assert.equal(nextReplaceableCreatedAt({ created_at: NOW - 5 }, NOW), NOW);
    assert.equal(nextReplaceableCreatedAt({ created_at: NOW }, NOW), NOW + 1);
  });

  it("newerListEvent prefers the newer copy and the lower id on ties", () => {
    const older = { id: "9", created_at: 10 };
    const newer = { id: "8", created_at: 11 };
    assert.equal(newerListEvent(older, newer), newer);
    assert.equal(newerListEvent(newer, null), newer);
    assert.equal(newerListEvent({ id: "b", created_at: 10 }, { id: "a", created_at: 10 }).id, "a");
  });
});
