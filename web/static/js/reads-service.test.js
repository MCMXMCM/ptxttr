if (typeof globalThis.localStorage === "undefined") {
  const store = new Map();
  globalThis.localStorage = {
    getItem: (key) => store.get(key) ?? null,
    setItem: (key, value) => store.set(key, String(value)),
    removeItem: (key) => store.delete(key),
  };
}
if (typeof globalThis.window === "undefined") {
  globalThis.window = { addEventListener() {} };
}
if (typeof globalThis.document === "undefined") {
  globalThis.document = {
    querySelectorAll: () => [],
    addEventListener: () => {},
  };
}

const assert = (await import("node:assert/strict")).default;
const { describe, it } = await import("node:test");
const { parseReadCard, fetchReadsPage, fetchReadDetail } = await import("./reads-service.js");
const { resetClientDBForTests } = await import("./client-store.js");
const { readsRightRail } = await import("./shell.js");

describe("reads-service", () => {
  it("parses nip-23 title summary and image tags", () => {
    const event = {
      id: "aa".repeat(32),
      pubkey: "bb".repeat(32),
      kind: 30023,
      created_at: 100,
      content: "# Heading\n\nBody paragraph.",
      tags: [
        ["title", "Custom title"],
        ["summary", "Short summary"],
        ["image", "https://example.com/cover.jpg"],
        ["published_at", "200"],
      ],
    };
    const card = parseReadCard(event);
    assert.equal(card.title, "Custom title");
    assert.equal(card.summary, "Short summary");
    assert.equal(card.publishedAt, 200);
    assert.equal(card.imageURL, "https://example.com/cover.jpg");
  });

  it("falls back cleanly when IndexedDB is unavailable for reads pages", async () => {
    delete globalThis.indexedDB;
    globalThis.localStorage.setItem("ptxt_relay_config", JSON.stringify({
      useAppRelays: false,
      useUserRelays: false,
      userRelayMetadata: { relays: [], updatedAt: 0 },
    }));
    resetClientDBForTests();

    await assert.doesNotReject(fetchReadsPage({ limit: 5 }));
    await assert.doesNotReject(fetchReadDetail("aa".repeat(32)));
    assert.deepEqual(await fetchReadsPage({ limit: 5 }), []);
    assert.equal(await fetchReadDetail("aa".repeat(32)), null);
  });

  it("renders a hydrate target for Trending Reads", () => {
    const html = readsRightRail("24h", "");
    assert.match(html, /data-trending-target/);
  });
});
