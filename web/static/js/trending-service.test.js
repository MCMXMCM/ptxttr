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
const {
  isTrendingSort,
  trendingWindowStart,
  trendingSortFromTimeframe,
  mergeTrendingResults,
  filterRelayTrendingBatch,
} = await import("./trending-service.js");

describe("trending-service", () => {
  it("detects trending sort modes", () => {
    assert.equal(isTrendingSort("trend24h"), true);
    assert.equal(isTrendingSort("trend7d"), true);
    assert.equal(isTrendingSort("recent"), false);
  });

  it("uses shorter window for trend24h than trend7d", () => {
    const now = 1_700_000_000;
    const day = trendingWindowStart("trend24h", now);
    const week = trendingWindowStart("trend7d", now);
    assert.equal(day, now - 86_400);
    assert.equal(week, now - 604_800);
    assert.ok(week < day);
  });

  it("maps timeframe prefs to sort modes", () => {
    assert.equal(trendingSortFromTimeframe("24h"), "trend24h");
    assert.equal(trendingSortFromTimeframe("1w"), "trend7d");
  });

  it("merges relay results before local fallback", () => {
    const relay = [{ id: "aa".repeat(32) }, { id: "bb".repeat(32) }];
    const local = [{ id: "cc".repeat(32) }, { id: "aa".repeat(32) }];
    const merged = mergeTrendingResults(relay, local, 3);
    assert.deepEqual(
      merged.map((event) => event.id),
      [relay[0].id, relay[1].id, local[0].id],
    );
  });

  it("filters relay batch to allowed authors and preserves order", async () => {
    const allowedAuthor = "aa".repeat(32);
    const otherAuthor = "bb".repeat(32);
    const now = Math.floor(Date.now() / 1000);
    const hotFirst = { id: "1".repeat(64), pubkey: allowedAuthor, kind: 1, created_at: now - 100, tags: [] };
    const hotSecond = { id: "2".repeat(64), pubkey: otherAuthor, kind: 1, created_at: now - 200, tags: [] };
    const hotThird = { id: "3".repeat(64), pubkey: allowedAuthor, kind: 1, created_at: now - 300, tags: [] };
    const filtered = await filterRelayTrendingBatch([hotFirst, hotSecond, hotThird], {
      allowedAuthors: [allowedAuthor],
      since: now - 86_400,
    });
    assert.deepEqual(
      filtered.map((event) => event.id),
      [hotFirst.id, hotThird.id],
    );
  });
});
