import assert from "node:assert/strict";
import { afterEach, describe, it } from "node:test";

import {
  fetchCachedQuery,
  peekQueryData,
  primeQueryData,
  primeQueryDataFromPersisted,
  queryTimingForKey,
  queryKeys,
  resetQueryClientForTests,
} from "./query-client.js";

afterEach(() => {
  resetQueryClientForTests();
});

describe("query-client", () => {
  it("reuses cached data in cache-first mode", async () => {
    let calls = 0;
    const queryKey = queryKeys.profile("abc");

    const first = await fetchCachedQuery({
      queryKey,
      cacheMode: "cache-first",
      queryFn: async () => {
        calls += 1;
        return { value: 1 };
      },
    });

    const second = await fetchCachedQuery({
      queryKey,
      cacheMode: "cache-first",
      queryFn: async () => {
        calls += 1;
        return { value: 2 };
      },
    });

    assert.deepEqual(first, { value: 1 });
    assert.deepEqual(second, { value: 1 });
    assert.equal(calls, 1);
  });

  it("forces a refetch in refresh mode", async () => {
    let calls = 0;
    const queryKey = queryKeys.profile("def");

    await fetchCachedQuery({
      queryKey,
      queryFn: async () => {
        calls += 1;
        return { value: calls };
      },
    });

    const refreshed = await fetchCachedQuery({
      queryKey,
      cacheMode: "refresh",
      queryFn: async () => {
        calls += 1;
        return { value: calls };
      },
    });

    assert.deepEqual(refreshed, { value: 2 });
  });

  it("can prime and peek cached query data directly", () => {
    const queryKey = queryKeys.feedPage({ viewerPubkey: "abc", sort: "recent", limit: 5 });
    primeQueryData(queryKey, ["note-1"]);
    assert.deepEqual(peekQueryData(queryKey), ["note-1"]);
  });

  it("can prime query data from persisted records", () => {
    const queryKey = queryKeys.threadBundle("a".repeat(64), "b".repeat(64), ["wss://relay.example"], false);
    const record = {
      root_id: "a".repeat(64),
      selected_id: "b".repeat(64),
      root: { id: "a".repeat(64) },
      selected: { id: "b".repeat(64) },
      events: [{ id: "a".repeat(64) }, { id: "b".repeat(64) }],
      saved_at: Date.now(),
    };
    primeQueryDataFromPersisted(record, queryKey);
    assert.deepEqual(peekQueryData(queryKey), record);
  });

  it("keeps route query families warm long enough for stale-while-revalidate paints", () => {
    assert.deepEqual(queryTimingForKey(queryKeys.feedPage({})), {
      staleTime: 60_000,
      gcTime: 30 * 60_000,
    });
    assert.deepEqual(queryTimingForKey(queryKeys.threadBundle("a".repeat(64))), {
      staleTime: 60_000,
      gcTime: 30 * 60_000,
    });
    assert.deepEqual(queryTimingForKey(queryKeys.profile("abc")), {
      staleTime: 2 * 60_000,
      gcTime: 30 * 60_000,
    });
  });
});
