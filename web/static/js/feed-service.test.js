if (typeof globalThis.localStorage === "undefined") {
  const store = new Map();
  globalThis.localStorage = {
    getItem: (key) => store.get(key) ?? null,
    setItem: (key, value) => store.set(key, String(value)),
    removeItem: (key) => store.delete(key),
  };
}
if (typeof globalThis.window === "undefined") {
  globalThis.window = { addEventListener() {}, location: { origin: "http://localhost" } };
} else if (!globalThis.window.location) {
  globalThis.window.location = { origin: "http://localhost" };
}
if (typeof globalThis.document === "undefined") {
  globalThis.document = {
    querySelectorAll: () => [],
    addEventListener: () => {},
  };
}

const assert = (await import("node:assert/strict")).default;
const { describe, it } = await import("node:test");
const { preferServerFeedOnThisBrowser, visibleFeedNoteIDs } = await import("./feed-service.js");

describe("feed-service", () => {
  it("collects visible feed note ids in DOM order", () => {
    const feed = {
      querySelectorAll: () => [
        { id: "note-AA" },
        { id: "note-bb" },
        { id: "note-aa" },
      ],
    };
    assert.deepEqual(visibleFeedNoteIDs(feed), ["aa", "bb"]);
  });

  it("prefers the server feed read path for all browsers", () => {
    assert.equal(preferServerFeedOnThisBrowser(), true);
  });
});
