// @ts-check
import { test, expect } from "@playwright/test";

test.use({ viewport: { width: 1120, height: 820 } });

test("desktop thread title toggles below the unobstructed window drag region", async ({
  page,
  request,
}) => {
  const seed = await request.post("/debug/seed-thread-wot");
  expect(seed.ok()).toBeTruthy();
  const { root_id: rootID } = await seed.json();

  await page.goto(`/thread/${rootID}?wot=0`);
  await expect(page.locator(`#thread-focus #note-${rootID}`)).toBeVisible({ timeout: 30_000 });

  await page.evaluate(() => {
    document.documentElement.dataset.ptxtDesktopMode = "1";
    if (!document.querySelector("[data-desktop-window-drag-region]")) {
      const dragRegion = document.createElement("div");
      dragRegion.className = "desktop-window-drag-region";
      dragRegion.dataset.desktopWindowDragRegion = "";
      dragRegion.setAttribute("aria-hidden", "true");
      document.body.prepend(dragRegion);
    }
  });

  const title = page.locator(".thread-view-toggle-desktop [data-thread-view-toggle]");
  await expect(title).toBeVisible();
  await expect(title).toHaveText("thread");
  const titleBox = await title.boundingBox();
  expect(titleBox?.y).toBeGreaterThanOrEqual(52);
  expect(await page.evaluate(() => {
    const dragRegion = document.querySelector("[data-desktop-window-drag-region]");
    return document.elementFromPoint(window.innerWidth / 2, 20) === dragRegion;
  })).toBeTruthy();

  await title.click();
  await expect(title).toHaveText("tree");
  await expect(page.locator("#thread-tree-view")).toBeVisible();

  await title.click();
  await expect(title).toHaveText("thread");
  await expect(page.locator(`#thread-focus #note-${rootID}`)).toBeVisible();
});
