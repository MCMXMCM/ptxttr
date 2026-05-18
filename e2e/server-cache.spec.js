// @ts-check
import { test, expect } from "@playwright/test";

async function fetchMetrics(request) {
  const res = await request.get("/debug/metrics");
  expect(res.ok()).toBeTruthy();
  return res.json();
}

function counter(metrics, name) {
  return metrics?.app?.counters?.[name] ?? 0;
}

async function waitForStoreFirstHydrate(request, noteID, timeoutMs = 30_000) {
  const deadline = Date.now() + timeoutMs;
  let delay = 100;
  let storeFirstBefore = counter(await fetchMetrics(request), "thread.hydrate.store_first");
  while (Date.now() < deadline) {
    const res = await request.get(`/thread/${noteID}?fragment=hydrate`);
    expect(res.ok()).toBeTruthy();
    const storeFirstAfter = counter(await fetchMetrics(request), "thread.hydrate.store_first");
    if (storeFirstAfter > storeFirstBefore) {
      return;
    }
    await new Promise((r) => setTimeout(r, delay));
    delay = Math.min(delay * 2, 1000);
  }
  throw new Error("timed out waiting for thread.hydrate.store_first");
}

test.describe("server thread cache", () => {
  test("second hydrate load increases cache hits without extra sync fetches", async ({
    request,
  }) => {
    const noteID = "e".repeat(64);
    const seed = await request.post(`/debug/seed-note?id=${noteID}`);
    expect(seed.ok()).toBeTruthy();

    const before = await fetchMetrics(request);
    const cacheBefore = counter(before, "event.cache_hit");
    const syncBefore = counter(before, "event.sync_fetch");

    const first = await request.get(`/thread/${noteID}?fragment=hydrate`);
    expect(first.ok()).toBeTruthy();

    await waitForStoreFirstHydrate(request, noteID);

    const mid = await fetchMetrics(request);
    const cacheMid = counter(mid, "event.cache_hit");
    const syncMid = counter(mid, "event.sync_fetch");

    const second = await request.get(`/thread/${noteID}?fragment=hydrate`);
    expect(second.ok()).toBeTruthy();

    const after = await fetchMetrics(request);
    const cacheAfter = counter(after, "event.cache_hit");
    const syncAfter = counter(after, "event.sync_fetch");
    const storeFirst = counter(after, "thread.hydrate.store_first");

    expect(cacheAfter).toBeGreaterThan(cacheBefore);
    expect(storeFirst).toBeGreaterThanOrEqual(1);
    expect(syncAfter - syncMid).toBeLessThanOrEqual(syncMid - syncBefore + 2);
    expect(cacheAfter - cacheMid).toBeGreaterThan(0);
  });
});
