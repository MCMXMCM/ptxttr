// @ts-check
import { test, expect } from "@playwright/test";

import {
  AUTHOR_PK,
  FEED_NOTE_ID,
  ROOT_ID,
  buildCombinedFixture,
} from "./helpers/nostr-fixtures.js";
import { installRelayNativeE2E, navigateToThreadFromFeed } from "./helpers/mock-nostr-relay.js";

test.describe("desktop home sidebar navigation", () => {
  test.use({
    viewport: { width: 1280, height: 900 },
    isMobile: false,
    hasTouch: false,
  });

  test("home from /feed after opening a thread restores the feed instead of a blank page", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, {
      events: buildCombinedFixture(),
      wotEnabled: false,
      responseDelayMs: 400,
    });
    await page.goto("/feed");
    await expect(page.locator(`#feed #note-${FEED_NOTE_ID}`)).toBeVisible({ timeout: 30_000 });

    await navigateToThreadFromFeed(page, ROOT_ID);
    await expect(page).toHaveURL(new RegExp(`/thread/${ROOT_ID}$`));

    const homeLink = page.getByRole("link", { name: "Home" });
    await homeLink.click();
    await expect(page).toHaveURL(/\/(feed)?$/);
    await expect(page.locator("#feed-heading")).toBeVisible({ timeout: 5_000 });
    await expect(page.locator(`#feed #note-${FEED_NOTE_ID}`)).toBeVisible({ timeout: 5_000 });

    await homeLink.click();
    await expect(page.locator(`#feed #note-${FEED_NOTE_ID}`)).toBeVisible({ timeout: 2_000 });
  });
});
