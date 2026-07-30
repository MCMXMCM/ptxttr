import assert from "node:assert/strict";
import { describe, it } from "node:test";

function makeStorage() {
  const data = new Map();
  return {
    getItem(key) {
      return data.has(key) ? data.get(key) : null;
    },
    setItem(key, value) {
      data.set(key, String(value));
    },
    removeItem(key) {
      data.delete(key);
    },
    clear() {
      data.clear();
    },
  };
}

globalThis.localStorage = makeStorage();
globalThis.sessionStorage = makeStorage();

const { shouldPreserveDeferredFeedLoader } = await import("./feed-deferred-loader.js");
const { markBootstrapPending } = await import("./first-login-bootstrap.js");

const VIEWER = "ab".repeat(32);

function feedWithLoader() {
  const feed = {
    querySelector(selector) {
      return selector === "[data-feed-loader]" ? { dataset: {} } : null;
    },
  };
  return feed;
}

describe("shouldPreserveDeferredFeedLoader", () => {
  it("keeps the deferred shell while a guest feed loader is still visible", () => {
    assert.equal(shouldPreserveDeferredFeedLoader(feedWithLoader(), [], ""), true);
  });

  it("allows empty feed paint once the loader is gone", () => {
    const feed = { querySelector() { return null; } };
    assert.equal(shouldPreserveDeferredFeedLoader(feed, [], ""), false);
  });

  it("allows note paint even when the loader is still visible", () => {
    assert.equal(shouldPreserveDeferredFeedLoader(feedWithLoader(), [{ id: "note-1" }], ""), false);
  });

  it("keeps the loader during pending first-login bootstrap", () => {
    markBootstrapPending(VIEWER);
    assert.equal(shouldPreserveDeferredFeedLoader(feedWithLoader(), [], VIEWER), true);
  });
});
