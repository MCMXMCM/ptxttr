import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  clearFragmentPrefetch,
  fragmentPrefetchCache,
  maxFragmentPrefetchEntries,
  setFragmentPrefetchCache,
} from "./prefetch.js";

describe("fragmentPrefetchCache LRU", () => {
  it("evicts oldest entries when over maxFragmentPrefetchEntries", () => {
    clearFragmentPrefetch();
    for (let i = 0; i < maxFragmentPrefetchEntries + 3; i += 1) {
      setFragmentPrefetchCache(`key-${i}`, Promise.resolve(i));
    }
    assert.equal(fragmentPrefetchCache.size, maxFragmentPrefetchEntries);
    assert.equal(fragmentPrefetchCache.has("key-0"), false);
    assert.equal(fragmentPrefetchCache.has("key-1"), false);
    assert.equal(fragmentPrefetchCache.has("key-2"), false);
    assert.equal(fragmentPrefetchCache.has(`key-${maxFragmentPrefetchEntries + 2}`), true);
    clearFragmentPrefetch();
  });

  it("refreshes LRU order on touch", () => {
    clearFragmentPrefetch();
    setFragmentPrefetchCache("keep", Promise.resolve(1));
    for (let i = 0; i < maxFragmentPrefetchEntries; i += 1) {
      setFragmentPrefetchCache(`fill-${i}`, Promise.resolve(i));
    }
    assert.equal(fragmentPrefetchCache.has("keep"), false);
    setFragmentPrefetchCache("keep", Promise.resolve(1));
    for (let i = 0; i < maxFragmentPrefetchEntries - 1; i += 1) {
      setFragmentPrefetchCache(`more-${i}`, Promise.resolve(i));
    }
    assert.equal(fragmentPrefetchCache.has("keep"), true);
    clearFragmentPrefetch();
  });
});
