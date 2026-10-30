// @ts-check
import { test, expect } from "@playwright/test";

const NOTE_ID = "d".repeat(64);
const AUTHOR_ID = "e".repeat(64);

test("v2 guest feed sort loads the selected server document", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name === "chromium-no-js", "requires JavaScript to change the sort");
  await page.goto("/");
  test.skip(await page.locator("body").getAttribute("data-guest-v2") !== "1", "requires PTXT_GUEST_SLICE_V2=1");

  const documentRequest = page.waitForRequest((request) => {
    const url = new URL(request.url());
    return request.resourceType() === "document" && url.pathname === "/" && url.searchParams.get("sort") === "trend7d";
  });
  await page.locator("[data-feed-sort-select]").selectOption("trend7d");
  await documentRequest;

  await expect(page.locator("[data-feed-sort-select]")).toHaveValue("trend7d");
  await expect(page.locator("#feed[data-feed]")).toHaveAttribute("data-feed-sort", "trend7d");
  await expect(page).toHaveURL(/\/$/);
});

test("v2 guest eagerly loads a saved default tree on a new thread document", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name === "chromium-no-js", "requires JavaScript preference bootstrap");
  const seed = await page.request.post("/debug/seed-thread-wot");
  expect(seed.ok()).toBeTruthy();
  const payload = await seed.json();

  await page.addInitScript(() => {
    localStorage.setItem("ptxt_thread_render_mode", "tree");
  });
  await page.goto(`/thread/${payload.root_id}`);
  test.skip(await page.locator("body").getAttribute("data-guest-v2") !== "1", "requires PTXT_GUEST_SLICE_V2=1");

  await expect(page.locator("html")).toHaveAttribute("data-ptxt-thread-view", "tree");
  await expect(page.locator("#thread-tree-view [data-thread-tree-note]").first()).toBeVisible({ timeout: 15_000 });
  await expect(page.locator("[data-thread-view-toggle]:visible")).toHaveAttribute("data-thread-view-current", "tree");
  await expect(page.locator("#thread-focus")).toBeHidden();
});

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

test("v2 guest prefetches a thread page's profile link the viewer hovers", async ({ page, request }, testInfo) => {
  test.skip(testInfo.project.name === "chromium-no-js", "prefetch rules are added by JavaScript");
  test.skip(Boolean(testInfo.project.use.isMobile), "touch devices do not hover");
  const seeded = await request.post(`/debug/seed-note?id=${NOTE_ID}&pubkey=${AUTHOR_ID}`);
  expect(seeded.ok()).toBeTruthy();

  await page.goto(`/thread/${NOTE_ID}`);
  test.skip(await page.locator("body").getAttribute("data-guest-v2") !== "1", "requires PTXT_GUEST_SLICE_V2=1");

  const prefetch = page.waitForRequest((browserRequest) => {
    const headers = browserRequest.headers();
    return new URL(browserRequest.url()).pathname === `/u/${AUTHOR_ID}` &&
      (String(headers["sec-purpose"] || "").includes("prefetch") || headers["x-ptxt-prefetch"] === "1");
  });
  const profileLink = page.locator(`a[href="/u/${AUTHOR_ID}"]:visible`).first();
  await profileLink.hover();
  await prefetch;

  await profileLink.click();
  await expect(page).toHaveURL(new RegExp(`/u/${AUTHOR_ID}$`));
  // Chromium serves the click from the speculation-rules prefetch; WebKit uses
  // the hover fallback, whose fetch only warms the HTTP cache.
  if (await page.evaluate(() => "prerendering" in document)) {
    const deliveryType = await page.evaluate(() => {
      const navigation = /** @type {(PerformanceEntry & { deliveryType?: string }) | undefined} */ (
        performance.getEntriesByType("navigation")[0]
      );
      return navigation?.deliveryType;
    });
    expect(deliveryType).toBe("navigational-prefetch");
  }
});
