// @ts-check
import { test, expect } from "@playwright/test";

import {
  AUTHOR_PK,
  FEED_NOTE_ID,
  MEDIA_FEED_NOTE_ID,
  ROOT_ID,
  buildAvatarThreadFixture,
  buildCombinedFixture,
} from "./helpers/nostr-fixtures.js";
import { installRelayNativeE2E, navigateToThreadFromFeed } from "./helpers/mock-nostr-relay.js";

test.use({
  viewport: { width: 390, height: 844 },
  isMobile: true,
  hasTouch: true,
});

test.describe("thread back navigation", () => {
  async function topPosition(locator) {
    return locator.evaluate((node) => node.getBoundingClientRect().top);
  }

  async function feedNoteTop(page, noteID) {
    return page.evaluate((id) => {
      const note = document.querySelector(`#feed #note-${id}`);
      return note?.getBoundingClientRect().top ?? Number.NaN;
    }, noteID);
  }

  async function feedNoteProfileState(page, noteID) {
    return page.evaluate((id) => {
      const note = document.querySelector(`#feed #note-${id}`);
      const img = note?.querySelector(".note-feed-avatar img");
      return {
        author: note?.dataset?.asciiAuthor || "",
        avatar: img?.getAttribute("src") || img?.currentSrc || "",
      };
    }, noteID);
  }

  test("back to home does not let a slow thread hydrate repaint the feed shell", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, {
      events: buildCombinedFixture(),
      wotEnabled: false,
      responseDelayMs: 1_500,
    });
    await page.goto("/feed");
    await expect(page.locator(`#note-${FEED_NOTE_ID}`)).toBeVisible({ timeout: 30_000 });

    await navigateToThreadFromFeed(page, ROOT_ID);
    await expect(page).toHaveURL(new RegExp(`/thread/${ROOT_ID}$`));
    await page.goBack();

    await expect(page).toHaveURL(/\/(feed)?$/);
    await expect(page.locator("#feed-heading")).toBeVisible({ timeout: 2_000 });
    await expect(page.locator(".feed-column[data-thread-root-id]")).toHaveCount(0);
    await expect(page.getByText("People in this thread")).toHaveCount(0);

    await page.waitForTimeout(1_800);
    await expect(page.locator(".feed-column[data-thread-root-id]")).toHaveCount(0);
    await expect(page.getByText("People in this thread")).toHaveCount(0);
  });

  test("back returns to the same feed viewport after opening a note from lower in the feed", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${MEDIA_FEED_NOTE_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, {
      events: buildCombinedFixture(),
      wotEnabled: false,
      responseDelayMs: 1_500,
    });
    await page.goto("/feed");
    await expect(page.locator(`#note-${FEED_NOTE_ID}`)).toBeVisible({ timeout: 30_000 });

    const target = page.locator(`#feed #note-${MEDIA_FEED_NOTE_ID}`);
    await target.scrollIntoViewIfNeeded();
    await page.waitForTimeout(120);

    const beforeTop = await feedNoteTop(page, MEDIA_FEED_NOTE_ID);

    await target.locator(".note-media-tile").first().tap();
    await expect(page).toHaveURL(new RegExp(`/thread/${MEDIA_FEED_NOTE_ID}$`), { timeout: 2_500 });

    await page.goBack();
    await expect(page).toHaveURL(/\/(feed)?$/);
    await expect(target).toBeVisible({ timeout: 2_500 });
    await expect(page.locator('[data-route-keepalive-layer="thread"]')).toHaveCount(0);
    await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(resolve)));

    const afterTop = await feedNoteTop(page, MEDIA_FEED_NOTE_ID);
    expect(Math.abs(afterTop - beforeTop)).toBeLessThanOrEqual(24);
  });

  test("back keeps hydrated feed display names and avatars", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${FEED_NOTE_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, {
      events: buildAvatarThreadFixture(),
      wotEnabled: false,
      responseDelayMs: 300,
    });
    await page.goto("/feed");
    const target = page.locator(`#feed #note-${FEED_NOTE_ID}`);
    await expect(target).toBeVisible({ timeout: 30_000 });
    await page.evaluate((id) => {
      const note = document.querySelector(`#feed #note-${id}`);
      if (!(note instanceof HTMLElement)) return;
      note.dataset.asciiAuthor = "Avatar Thread Author";
      note.dataset.asciiAvatar = "/static/img/ascritch.png";
      const avatar = note.querySelector(".note-feed-avatar");
      if (!(avatar instanceof HTMLElement)) return;
      avatar.replaceChildren();
      const img = document.createElement("img");
      img.alt = "";
      img.src = "/static/img/ascritch.png";
      avatar.append(img);
    }, FEED_NOTE_ID);
    await expect.poll(() => feedNoteProfileState(page, FEED_NOTE_ID)).toMatchObject({
      author: "Avatar Thread Author",
    });
    await expect.poll(async () => (await feedNoteProfileState(page, FEED_NOTE_ID)).avatar).not.toEqual("");

    await target.tap();
    await expect(page).toHaveURL(new RegExp(`/thread/${FEED_NOTE_ID}$`), { timeout: 2_500 });

    await page.goBack();
    await expect(page).toHaveURL(/\/(feed)?$/);
    await expect(target).toBeVisible({ timeout: 2_500 });
    await expect.poll(() => feedNoteProfileState(page, FEED_NOTE_ID)).toMatchObject({
      author: "Avatar Thread Author",
    });
    await expect.poll(async () => (await feedNoteProfileState(page, FEED_NOTE_ID)).avatar).not.toEqual("");
  });
});
