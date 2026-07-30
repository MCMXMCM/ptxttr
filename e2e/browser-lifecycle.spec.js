// @ts-check
import { test, expect } from "@playwright/test";

import {
  AUTHOR_PK,
  FEED_NOTE_ID,
  ROOT_ID,
  buildCombinedFixture,
} from "./helpers/nostr-fixtures.js";
import { installRelayNativeE2E } from "./helpers/mock-nostr-relay.js";

test.describe("browser lifecycle", () => {
  test("pageshow bfcache restore does not stack polling timers", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await page.addInitScript(() => {
      const nativeSetInterval = window.setInterval.bind(window);
      const nativeClearInterval = window.clearInterval.bind(window);
      const active = new Set();
      let created = 0;
      window.setInterval = (handler, timeout, ...args) => {
        const id = nativeSetInterval(handler, timeout, ...args);
        active.add(id);
        created += 1;
        return id;
      };
      window.clearInterval = (id) => {
        active.delete(id);
        return nativeClearInterval(id);
      };
      window.__ptxtIntervalStats = () => ({ active: active.size, created });
    });

    await installRelayNativeE2E(page, {
      events: buildCombinedFixture(),
      wotEnabled: false,
      responseDelayMs: 200,
    });

    await page.goto("/feed");
    await expect(page.locator(`#feed #note-${FEED_NOTE_ID}`)).toBeVisible({ timeout: 30_000 });
    const before = await page.evaluate(() => window.__ptxtIntervalStats());

    await page.evaluate(() => {
      const event = new Event("pageshow");
      Object.defineProperty(event, "persisted", { value: true });
      window.dispatchEvent(event);
      window.dispatchEvent(event);
    });

    const after = await page.evaluate(() => window.__ptxtIntervalStats());
    expect(after.active).toBe(before.active);
  });
});
