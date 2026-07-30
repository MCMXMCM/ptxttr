// @ts-check
import { test, expect } from "@playwright/test";

const NOTE_ID = "d".repeat(64);
const AUTHOR_ID = "e".repeat(64);

test("v2 guest thread-to-profile navigation stays complete and relay-free", async ({ page, request }) => {
  const seeded = await request.post(`/debug/seed-note?id=${NOTE_ID}&pubkey=${AUTHOR_ID}`);
  expect(seeded.ok()).toBeTruthy();

  const metadataFanout = [];
  let webSockets = 0;
  page.on("request", (browserRequest) => {
    const url = new URL(browserRequest.url());
    if (url.pathname === "/api/profiles" || url.pathname === "/api/reaction-stats" || url.pathname === "/api/reply-counts") {
      metadataFanout.push(url.pathname);
    }
  });
  page.on("websocket", () => { webSockets += 1; });

  const threadResponse = await page.goto(`/thread/${NOTE_ID}`);
  test.skip(await page.locator("body").getAttribute("data-guest-v2") !== "1", "requires PTXT_GUEST_SLICE_V2=1");
  expect(threadResponse?.headers()["x-ptxt-route-status"]).toBe("ready");
  await expect(page.locator(`#note-${NOTE_ID}`)).toContainText("e2e seeded note");
  await expect(page.locator("[data-thread-route-pending], [data-retro-loader-type='thread']")).toHaveCount(0);

  const profileLink = page.locator(`a[href="/u/${AUTHOR_ID}"]:visible`).first();
  await expect(profileLink).toBeVisible();
  const profileResponsePromise = page.waitForResponse((response) => (
    new URL(response.url()).pathname === `/u/${AUTHOR_ID}` && response.request().resourceType() === "document"
  ));
  await profileLink.click();
  const profileResponse = await profileResponsePromise;
  expect(profileResponse.headers()["x-ptxt-route-status"]).toBe("ready");
  await expect(page).toHaveURL(new RegExp(`/u/${AUTHOR_ID}$`));
  await expect(page.locator("#user-header .profile-display-name")).not.toHaveClass(/text-skeleton/);
  await expect(page.locator(`#note-${NOTE_ID}`)).toContainText("e2e seeded note");
  await expect(page.locator("[data-retro-loader-type='profile-posts']")).toHaveCount(0);
  expect(metadataFanout).toEqual([]);
  expect(webSockets).toBe(0);
});
