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

test.describe("server thread cache", () => {
  test("repeat anonymous hydrate loads stay store-backed without sync relay fetches", async ({
    request,
  }) => {
    // Keep this fixture disjoint from the c/d/e IDs used by thread WoT tests;
    // Playwright workers share one debug server and therefore one event store.
    const noteID = "9".repeat(64);
    const seed = await request.post(`/debug/seed-note?id=${noteID}`);
    expect(seed.ok()).toBeTruthy();

    const before = await fetchMetrics(request);
    const storeOnlyBefore = counter(
      before,
      "thread.anonymous_hydrate.store_only",
    );
    const syncBefore = counter(before, "event.sync_fetch");

    const first = await request.get(`/thread/${noteID}?fragment=hydrate`);
    expect(first.ok()).toBeTruthy();
    const firstBody = await first.text();

    const mid = await fetchMetrics(request);
    const storeOnlyMid = counter(
      mid,
      "thread.anonymous_hydrate.store_only",
    );
    const syncMid = counter(mid, "event.sync_fetch");

    const second = await request.get(`/thread/${noteID}?fragment=hydrate`);
    expect(second.ok()).toBeTruthy();
    const secondBody = await second.text();

    const after = await fetchMetrics(request);
    const storeOnlyAfter = counter(
      after,
      "thread.anonymous_hydrate.store_only",
    );
    const syncAfter = counter(after, "event.sync_fetch");

    expect(storeOnlyMid).toBeGreaterThan(storeOnlyBefore);
    expect(storeOnlyAfter).toBeGreaterThan(storeOnlyMid);
    expect(secondBody).toBe(firstBody);
    expect(syncMid).toBe(syncBefore);
    expect(syncAfter).toBe(syncMid);
  });
});
