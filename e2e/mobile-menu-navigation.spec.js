// @ts-check
import { test, expect } from "@playwright/test";

test.use({
  viewport: { width: 390, height: 844 },
  isMobile: true,
  hasTouch: true,
});

test.describe("mobile menu navigation", () => {
  test("hides the web of trust selector from logged-out guests", async ({ page }) => {
    await page.goto("/feed");

    await expect(page.locator(".mobile-bar-wot")).toBeHidden();
  });

  test("places the signed-in web of trust selector directly before the close glyph", async ({ page }) => {
    await page.addInitScript(() => {
      localStorage.setItem("ptxt_nostr_session", JSON.stringify({
        method: "readonly",
        pubkey: "a".repeat(64),
        npub: "npub-test",
      }));
    });
    await page.goto("/feed");

    await page.locator("[data-mobile-menu-trigger]").tap();
    const wot = page.locator(".mobile-bar-wot");
    const close = page.locator("[data-mobile-menu-trigger]");
    await expect(wot).toBeVisible();

    const wotBox = await wot.boundingBox();
    const closeBox = await close.boundingBox();
    expect(wotBox).not.toBeNull();
    expect(closeBox).not.toBeNull();

    const gap = closeBox.x - (wotBox.x + wotBox.width);
    expect(gap).toBeGreaterThanOrEqual(0);
    expect(gap).toBeLessThanOrEqual(8);
  });

  test("keeps the guest close glyph on the right when a thread menu is open", async ({ page, request }) => {
    const noteID = "b".repeat(64);
    const seed = await request.post(`/debug/seed-note?id=${noteID}`);
    expect(seed.ok()).toBeTruthy();

    await page.goto(`/thread/${noteID}`);
    const trigger = page.locator("[data-mobile-menu-trigger]");
    await trigger.tap();

    await expect(trigger).toHaveAttribute("aria-label", "Close menu");
    const closeBox = await trigger.boundingBox();
    const toggleBox = await page.locator(".mobile-bar-center").boundingBox();
    expect(closeBox).not.toBeNull();
    expect(toggleBox).not.toBeNull();
    expect(closeBox.x + closeBox.width).toBeGreaterThan(370);
    expect(closeBox.x).toBeGreaterThan(toggleBox.x + toggleBox.width);

    await trigger.tap();
    await expect(page.locator("[data-mobile-menu]")).toBeHidden();
  });

  test("selecting a route closes the menu overlay", async ({ page }) => {
    await page.goto("/feed");

    await page.locator("[data-mobile-menu-trigger]").tap();
    const menu = page.locator("[data-mobile-menu]");
    await expect(menu).toHaveClass(/is-open/);
    await expect(menu).toBeVisible();

    await menu.getByRole("link", { name: "Reads" }).tap();

    await expect(page).toHaveURL(/\/reads$/);
    await expect(menu).not.toHaveClass(/is-open/);
    await expect(menu).toBeHidden();
  });
});
