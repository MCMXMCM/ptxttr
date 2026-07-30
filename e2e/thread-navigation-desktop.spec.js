// @ts-check
import { test, expect } from "@playwright/test";

import {
  AUTHOR_PK,
  FEED_NOTE_ID,
  ROOT_ID,
  buildCombinedFixture,
  buildFreshTrendingFixture,
} from "./helpers/nostr-fixtures.js";
import { installRelayNativeE2E } from "./helpers/mock-nostr-relay.js";

test.describe("desktop thread navigation", () => {
  test.use({
    viewport: { width: 1280, height: 900 },
  });

  test("thread content stays in the center column on desktop", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, {
      events: buildCombinedFixture(),
      wotEnabled: false,
      responseDelayMs: 400,
    });
    await page.goto("/feed");
    await expect(page.locator(`#feed #note-${FEED_NOTE_ID}`)).toBeVisible({ timeout: 30_000 });

    await page.locator(`#feed #note-${FEED_NOTE_ID}`).click();
    await expect(page).toHaveURL(new RegExp(`/thread/${FEED_NOTE_ID}$`), { timeout: 5_000 });
    await expect(page.locator("#thread-focus .note, #thread-focus .comment")).toBeVisible({ timeout: 10_000 });

    const layout = await page.evaluate(() => {
      const shell = document.querySelector(".app-shell");
      const column = document.querySelector(".feed-column");
      const focus = document.querySelector("#thread-focus");
      const rail = document.querySelector('.right-rail[data-thread-fragment="participants"]');
      if (!(shell instanceof HTMLElement) || !(column instanceof HTMLElement) || !(focus instanceof HTMLElement) || !(rail instanceof HTMLElement)) {
        return null;
      }
      const shellRect = shell.getBoundingClientRect();
      const columnRect = column.getBoundingClientRect();
      const focusRect = focus.getBoundingClientRect();
      const railRect = rail.getBoundingClientRect();
      const columnDisplay = window.getComputedStyle(column).display;
      return {
        focusInsideColumn: column.contains(focus),
        columnDisplay,
        columnLeft: columnRect.left,
        focusLeft: focusRect.left,
        railLeft: railRect.left,
        shellWidth: shellRect.width,
      };
    });

    expect(layout).not.toBeNull();
    expect(layout?.focusInsideColumn).toBe(true);
    expect(layout?.columnDisplay).not.toBe("contents");
    expect(layout?.focusLeft).toBeGreaterThanOrEqual((layout?.columnLeft || 0) - 4);
    expect(layout?.railLeft).toBeGreaterThan(layout?.focusLeft || 0);
  });

  test("first paint keeps reply action and right border inside the desktop column", async ({ browser, request }) => {
    const seeded = await request.post("/debug/seed-thread-wot");
    expect(seeded.ok()).toBeTruthy();
    const fixture = await seeded.json();
    const context = await browser.newContext({
      javaScriptEnabled: false,
      viewport: { width: 1024, height: 900 },
    });
    const page = await context.newPage();

    try {
      const response = await page.goto(`/thread/${fixture.root_id}`);
      expect(response?.ok()).toBeTruthy();

      const card = page.locator(`#thread-focus #note-${fixture.root_id}`);
      await expect(card).toBeVisible();
      await expect(card.locator("a[data-reply-action]")).toBeVisible();
      await expect(card.locator(":scope > .ascii-card")).toContainText("[reply] ---+");

      const geometry = await card.evaluate((note) => {
        const column = note.closest(".feed-column");
        const lines = Array.from(note.querySelectorAll(":scope > .ascii-card > .ascii-line"));
        const footer = lines.find((line) => line.textContent?.includes("[reply]"));
        const borderedBodyLines = lines.filter((line) => (line.textContent || "").trimEnd().endsWith("|"));
        const columnRight = column?.getBoundingClientRect().right || 0;
        return {
          columnRight,
          footerRight: footer?.getBoundingClientRect().right || 0,
          maxBodyRight: Math.max(0, ...borderedBodyLines.map((line) => line.getBoundingClientRect().right)),
          footerText: (footer?.textContent || "").trim(),
          borderedBodyLines: borderedBodyLines.length,
        };
      });

      expect(geometry.borderedBodyLines).toBeGreaterThan(0);
      expect(geometry.footerText).toMatch(/\[reply\] ---\+$/);
      expect(geometry.footerRight).toBeLessThanOrEqual(geometry.columnRight + 1);
      expect(geometry.maxBodyRight).toBeLessThanOrEqual(geometry.columnRight + 1);
    } finally {
      await context.close();
    }
  });

  test("feed to thread updates the shareable URL", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, {
      events: buildCombinedFixture(),
      wotEnabled: false,
      responseDelayMs: 400,
    });
    await page.goto("/feed");
    await expect(page.locator(`#feed #note-${FEED_NOTE_ID}`)).toBeVisible({ timeout: 30_000 });

    await page.locator(`#feed #note-${FEED_NOTE_ID}`).click();
    await expect(page).toHaveURL(new RegExp(`/thread/${FEED_NOTE_ID}$`), { timeout: 5_000 });

    const shareHref = await page.locator("#thread-focus .note, #thread-focus .comment").first()
      .getAttribute("data-ascii-select-href");
    expect(shareHref || "").toMatch(new RegExp(`/thread/${FEED_NOTE_ID}`));
  });

  test("switching to trending still lets the first note click open immediately", async ({ page }) => {
    await installRelayNativeE2E(page, {
      events: buildFreshTrendingFixture(),
      wotEnabled: false,
      responseDelayMs: 400,
    });
    await page.goto("/feed");
    await expect(page.locator("#feed .note[id^='note-']").first()).toBeVisible({ timeout: 30_000 });

    await page.locator("[data-feed-sort-select]").selectOption("trend7d");
    const firstNote = page.locator("#feed .note[id^='note-']").first();
    await expect(firstNote).toBeVisible({ timeout: 10_000 });
    const firstNoteID = (await firstNote.getAttribute("id") || "").replace(/^note-/, "");
    expect(firstNoteID).toMatch(/^[0-9a-f]{64}$/);

    await firstNote.click();
    await expect(page).toHaveURL(new RegExp(`/thread/${firstNoteID}$`), { timeout: 5_000 });
    await expect(page.locator("#thread-focus .note, #thread-focus .comment")).toBeVisible({ timeout: 10_000 });
  });
});
