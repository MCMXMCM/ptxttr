// @ts-check
import { test, expect } from "@playwright/test";

import {
  AUTHOR_PK,
  REPLY_ID,
  ROOT_ID,
  buildCombinedFixture,
} from "./helpers/nostr-fixtures.js";
import { installRelayNativeE2E } from "./helpers/mock-nostr-relay.js";

test.describe("thread transition", () => {
  test.use({
    viewport: { width: 390, height: 844 },
    isMobile: true,
    hasTouch: true,
  });

  test("uses document navigation and settles thread focus", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, {
      events: buildCombinedFixture(),
      wotEnabled: false,
      responseDelayMs: 400,
    });
    await page.goto("/feed");
    await expect(page.locator(`#feed #note-${ROOT_ID}`)).toBeVisible({ timeout: 30_000 });

    const feedCard = page.locator(`#feed #note-${ROOT_ID}`);
    await feedCard.tap();

    await expect(page).toHaveURL(new RegExp(`/thread/${ROOT_ID}$`), { timeout: 10_000 });
    await expect.poll(async () => {
      return page.evaluate(() => document.querySelector("#ptxt-ascii-paper") !== null);
    }).toBe(false);
    await expect(page.locator('[data-route-keepalive-layer]')).toHaveCount(0);
    await expect(page.locator("#thread-focus .note, #thread-focus .comment")).toBeVisible({ timeout: 10_000 });
    await expect.poll(async () => page.evaluate(() => !document.documentElement.classList.contains("ptxt-thread-route-transition"))).toBe(true);
  });

  test("reduced motion skips lightweight feed to thread motion", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await page.emulateMedia({ reducedMotion: "reduce" });
    await installRelayNativeE2E(page, {
      events: buildCombinedFixture(),
      wotEnabled: false,
      responseDelayMs: 100,
    });
    await page.goto("/feed");
    await expect(page.locator(`#feed #note-${ROOT_ID}`)).toBeVisible({ timeout: 30_000 });

    await page.locator(`#feed #note-${ROOT_ID}`).tap();
    await expect(page).toHaveURL(new RegExp(`/thread/${ROOT_ID}$`), { timeout: 10_000 });
    await expect(page.locator("#thread-focus .note, #thread-focus .comment")).toBeVisible({ timeout: 10_000 });
    await expect.poll(async () => {
      return page.evaluate(() => document.documentElement.classList.contains("ptxt-thread-route-transition"));
    }).toBe(false);
    await expect.poll(async () => {
      return page.evaluate(() => document.querySelector("#ptxt-ascii-paper") !== null);
    }).toBe(false);
  });

  test("in-thread focus changes do not create a hidden feed copy", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, {
      events: buildCombinedFixture(),
      wotEnabled: false,
      responseDelayMs: 400,
    });
    await page.goto("/feed");
    await expect(page.locator(`#feed #note-${ROOT_ID}`)).toBeVisible({ timeout: 30_000 });

    await page.locator(`#feed #note-${ROOT_ID}`).tap();
    await expect(page).toHaveURL(new RegExp(`/thread/${ROOT_ID}$`), { timeout: 10_000 });
    await expect(page.locator("#thread-focus .note, #thread-focus .comment")).toBeVisible({ timeout: 10_000 });
    await expect.poll(async () => page.evaluate(() => !document.documentElement.classList.contains("ptxt-thread-route-transition"))).toBe(true);

    const reply = page.locator(".thread-replies .comment, .thread-replies .note").first();
    await expect(reply).toBeVisible({ timeout: 10_000 });
    await reply.tap();

    await expect(page).toHaveURL(new RegExp(`/thread/${ROOT_ID}\\?selected=${REPLY_ID}#note-${REPLY_ID}$`), { timeout: 10_000 });
    await expect.poll(async () => page.evaluate(() => !document.documentElement.classList.contains("ptxt-thread-route-transition"))).toBe(true);
    await expect(page.locator('[data-route-keepalive-layer]')).toHaveCount(0);
  });

  test("browser back restores the feed after a thread transition", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, {
      events: buildCombinedFixture(),
      wotEnabled: false,
      responseDelayMs: 400,
    });
    await page.goto("/feed");
    await expect(page.locator(`#feed #note-${ROOT_ID}`)).toBeVisible({ timeout: 30_000 });

    const beforeTop = await page.evaluate((id) => {
      const note = document.querySelector(`#feed #note-${id}`);
      return note?.getBoundingClientRect().top ?? Number.NaN;
    }, ROOT_ID);

    await page.locator(`#feed #note-${ROOT_ID}`).tap();
    await expect(page).toHaveURL(new RegExp(`/thread/${ROOT_ID}$`), { timeout: 10_000 });

    await page.goBack();
    await expect(page).toHaveURL(/\/(feed)?$/);
    await expect(page.locator(`#feed #note-${ROOT_ID}`)).toBeVisible({ timeout: 10_000 });
    await expect.poll(async () => {
      return page.evaluate(() => document.querySelector("#ptxt-ascii-paper") !== null);
    }).toBe(false);

    const afterTop = await page.evaluate((id) => {
      const note = document.querySelector(`#feed #note-${id}`);
      return note?.getBoundingClientRect().top ?? Number.NaN;
    }, ROOT_ID);
    expect(Math.abs(afterTop - beforeTop)).toBeLessThan(80);
  });
});
