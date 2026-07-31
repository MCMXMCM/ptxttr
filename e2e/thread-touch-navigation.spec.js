// @ts-check
import { test, expect } from "@playwright/test";
import { getPublicKey, nip19 } from "../web/static/lib/nostr-tools.js";

import {
  AUTHOR_PK,
  FEED_NOTE_ID,
  LEGACY_PARENT_ID,
  LEGACY_ROOT_ID,
  LEGACY_SELECTED_ID,
  MEDIA_FEED_NOTE_ID,
  REPLY_ID,
  ROOT_ID,
  VIDEO_FEED_NOTE_ID,
  buildCombinedFixture,
  buildLegacyPositionalThreadFixture,
} from "./helpers/nostr-fixtures.js";
import { installRelayNativeE2E } from "./helpers/mock-nostr-relay.js";

const VIEWER_SK = new Uint8Array(32).fill(0x11);
const VIEWER_PK = getPublicKey(VIEWER_SK);
const VIEWER_NPUB = nip19.npubEncode(VIEWER_PK);
const VIEWER_NSEC = nip19.nsecEncode(VIEWER_SK);

async function installSigningSession(page) {
  await page.addInitScript(({ pubkey, npub, nsec }) => {
    const session = {
      method: "yolo",
      pubkey,
      npub,
      canSign: true,
      readOnly: false,
      needsExtension: false,
      profileLabel: "E2E Viewer",
    };
    sessionStorage.setItem("ptxt_nsec", nsec);
    localStorage.setItem("ptxt_nostr_session", JSON.stringify(session));
    localStorage.setItem(
      "ptxt_nostr_signing_accounts",
      JSON.stringify([{ ...session, nsec, lastUsedAt: Date.now() }]),
    );
  }, { pubkey: VIEWER_PK, npub: VIEWER_NPUB, nsec: VIEWER_NSEC });
}

test.use({
  viewport: { width: 390, height: 844 },
  isMobile: true,
  hasTouch: true,
});

test.describe("thread touch navigation", () => {
  async function topAndHeight(locator) {
    return locator.evaluate((node) => {
      const rect = node.getBoundingClientRect();
      return { top: rect.top, height: rect.height };
    });
  }

  test("inline reply composer uses native textarea editing without scroll drift", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, {
      events: buildCombinedFixture(),
      viewerPubkey: VIEWER_PK,
      wotEnabled: false,
    });
    await installSigningSession(page);

    await page.goto(`/thread/${ROOT_ID}?wot=0`);
    const rootCard = page.locator(`#thread-focus #note-${ROOT_ID}`);
    await expect(rootCard).toBeVisible({ timeout: 30_000 });

    await rootCard.locator("[data-reply-action]").last().tap();
    const textarea = page.locator(".thread-inline-reply textarea[data-composer-content]");
    await expect(textarea).toBeVisible({ timeout: 8_000 });
    await expect(textarea).toBeFocused();
    await expect(page.locator("#thread-focus > .thread-inline-reply")).toBeVisible();
    await expect(page.locator("#thread-focus .ascii-card .thread-inline-reply")).toHaveCount(0);

    const inputPaint = await textarea.evaluate((node) => {
      const style = window.getComputedStyle(node);
      return {
        color: style.color,
        textFillColor: style.webkitTextFillColor,
        overlayHidden: node.closest("[data-composer-input-wrap]")?.querySelector("[data-composer-overlay]")?.hidden === true,
      };
    });
    expect(inputPaint.color).not.toBe("rgba(0, 0, 0, 0)");
    expect(inputPaint.textFillColor).not.toBe("rgba(0, 0, 0, 0)");
    expect(inputPaint.overlayHidden).toBeTruthy();

    await textarea.fill("alpha beta gamma");
    await textarea.evaluate((node) => {
      node.setSelectionRange(5, 10);
    });
    await page.keyboard.press("Backspace");
    await expect(textarea).toHaveValue("alpha gamma");

    await textarea.evaluate((node) => {
      node.setSelectionRange(0, 5);
    });
    await page.keyboard.type("omega");
    await expect(textarea).toHaveValue("omega gamma");

    const afterEditScrollY = await page.evaluate(() => Math.round(window.scrollY));
    await page.waitForTimeout(900);
    const laterScrollY = await page.evaluate(() => Math.round(window.scrollY));
    expect(Math.abs(laterScrollY - afterEditScrollY)).toBeLessThanOrEqual(4);
  });

  test("first tap on a reply card opens the thread on mobile after scrolling", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, {
      events: buildCombinedFixture(),
      wotEnabled: false,
      responseDelayMs: 1_500,
    });

    await page.goto("/feed");
    const replyCard = page.locator(`#note-${REPLY_ID}`);
    await expect(replyCard).toBeVisible({ timeout: 30_000 });

    await page.evaluate(() => {
      window.scrollTo(0, Math.max(120, Math.floor(window.innerHeight * 0.35)));
    });
    await page.waitForTimeout(100);
    await replyCard.scrollIntoViewIfNeeded();
    await page.waitForTimeout(100);

    await replyCard.tap();

    await expect(page).toHaveURL(new RegExp(`/thread/${ROOT_ID}\\?selected=${REPLY_ID}#note-${REPLY_ID}$`), {
      timeout: 2_500,
    });
    await expect(page.locator("#thread-focus .thread-focus-parent")).toBeVisible({
      timeout: 500,
    });
    await expect(page.locator("#thread-focus").getByText("e2e-relay-native-thread-reply")).toBeVisible({
      timeout: 2_500,
    });
  });

  test("feed selection shows the immediate parent for positional NIP-10 replies", async ({ page }) => {
    await installRelayNativeE2E(page, {
      events: buildLegacyPositionalThreadFixture(),
      wotEnabled: false,
    });

    await page.goto("/feed");
    const selectedCard = page.locator(`#note-${LEGACY_SELECTED_ID}`);
    await expect(selectedCard).toBeVisible({ timeout: 30_000 });
    await selectedCard.tap();

    await expect(page).toHaveURL(
      new RegExp(`/thread/${LEGACY_ROOT_ID}\\?selected=${LEGACY_SELECTED_ID}#note-${LEGACY_SELECTED_ID}$`),
      { timeout: 5_000 },
    );
    await expect(page.locator(`#thread-focus .thread-focus-parent#note-${LEGACY_PARENT_ID}`)).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.locator(`#thread-focus #note-${LEGACY_SELECTED_ID}`)).toBeVisible({
      timeout: 30_000,
    });
  });

  test("mobile thread header does not cover the focused note", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, {
      events: buildCombinedFixture(),
      wotEnabled: false,
      responseDelayMs: 1_500,
    });

    await page.goto("/feed");
    const rootCard = page.locator(`#note-${FEED_NOTE_ID}`);
    await expect(rootCard).toBeVisible({ timeout: 30_000 });
    await rootCard.tap();

    await expect(page).toHaveURL(new RegExp(`/thread/${FEED_NOTE_ID}$`), {
      timeout: 2_500,
    });
    const bar = page.locator(".mobile-bar");
    const toggle = page.locator(".mobile-bar [data-thread-view-toggle]");
    const focused = page.locator(`#thread-focus #note-${FEED_NOTE_ID}`);
    await expect(toggle).toBeVisible({ timeout: 8_000 });
    await expect(focused).toBeVisible({ timeout: 8_000 });

    const barBox = await topAndHeight(bar);
    const focusedBox = await topAndHeight(focused);
    expect(focusedBox.top + 2).toBeGreaterThanOrEqual(barBox.top + barBox.height);
  });

  test("mobile thread view toggle is not intercepted by the home brand link", async ({ page, request }) => {
    const seed = await request.post("/debug/seed-thread-wot");
    expect(seed.ok()).toBeTruthy();
    const payload = await seed.json();
    const rootID = payload.root_id;

    await page.goto(`/thread/${rootID}?wot=0`);
    await expect(page.locator(`#thread-focus #note-${rootID}`)).toBeVisible({ timeout: 30_000 });

    const toggle = page.locator(".mobile-bar [data-thread-view-toggle]");
    const menuTrigger = page.locator(".mobile-bar .mobile-menu-trigger");
    await expect(toggle).toHaveText("thread", { timeout: 8_000 });
    await expect(menuTrigger).toBeVisible({ timeout: 8_000 });
    await toggle.tap();
    await expect(toggle).toHaveText("tree", { timeout: 8_000 });
    await expect(page.locator("#thread-tree-view")).toBeVisible({ timeout: 8_000 });

    const headerTargets = await toggle.evaluate((node) => {
      const toggleRect = node.getBoundingClientRect();
      const brandRect = document.querySelector(".mobile-bar .mobile-brand")?.getBoundingClientRect();
      const menuRect = document.querySelector(".mobile-bar .mobile-menu-trigger")?.getBoundingClientRect();
      const viewportWidth = window.innerWidth;
      return {
        x: Math.floor(toggleRect.left + toggleRect.width * 0.25),
        y: Math.floor(toggleRect.top + toggleRect.height / 2),
        tag: document.elementFromPoint(toggleRect.left + toggleRect.width * 0.25, toggleRect.top + toggleRect.height / 2)?.tagName,
        toggle: Boolean(
          document
            .elementFromPoint(toggleRect.left + toggleRect.width * 0.25, toggleRect.top + toggleRect.height / 2)
            ?.closest("[data-thread-view-toggle]"),
        ),
        brandRight: brandRect?.right ?? 0,
        menuRight: menuRect?.right ?? 0,
        menuLeft: menuRect?.left ?? 0,
        toggleLeft: toggleRect.left,
        viewportWidth,
      };
    });
    expect(headerTargets.tag).toBe("BUTTON");
    expect(headerTargets.toggle).toBeTruthy();
    expect(headerTargets.brandRight).toBeLessThan(headerTargets.toggleLeft);
    expect(headerTargets.menuLeft).toBeGreaterThan(headerTargets.viewportWidth * 0.75);
    expect(headerTargets.viewportWidth - headerTargets.menuRight).toBeLessThan(24);

    await page.touchscreen.tap(headerTargets.x, headerTargets.y);
    await expect(toggle).toHaveText("thread", { timeout: 8_000 });
    await expect(page.locator(`#thread-focus #note-${rootID}`)).toBeVisible({ timeout: 8_000 });
    await expect(page).toHaveURL(new RegExp(`/thread/${rootID}(\\?wot=0)?(#note-${rootID})?$`));
  });

  test("tapping notes inside a thread changes the focused note", async ({ page, request }) => {
    const seed = await request.post("/debug/seed-thread-wot");
    expect(seed.ok()).toBeTruthy();
    const payload = await seed.json();
    const rootID = payload.root_id;
    const replyID = payload.trusted_reply_id;

    await page.goto(`/thread/${rootID}?wot=0`);
    await expect(page.locator(`#thread-focus #note-${rootID}`)).toBeVisible({ timeout: 30_000 });

    const replyCard = page.locator(`#thread-replies #note-${replyID}, .thread-replies #note-${replyID}`).first();
    await expect(replyCard).toBeVisible({ timeout: 30_000 });
    await replyCard.tap();

    await expect(page).toHaveURL(new RegExp(`/thread/${rootID}\\?selected=${replyID}#note-${replyID}$`), {
      timeout: 5_000,
    });
    await expect(page.locator(`#thread-focus #note-${replyID}.is-focused`)).toBeVisible({ timeout: 30_000 });
    await expect(page.locator("#thread-focus .thread-focus-parent")).toBeVisible({ timeout: 30_000 });

    await page.route("**/thread/**", async (route) => {
      const url = new URL(route.request().url());
      if (url.searchParams.get("fragment") === "hydrate" && !url.searchParams.has("selected")) {
        await new Promise((resolve) => setTimeout(resolve, 1_500));
      }
      await route.continue();
    });
    const rootHydrateResponse = page.waitForResponse((response) => {
      const url = new URL(response.url());
      return url.searchParams.get("fragment") === "hydrate" && !url.searchParams.has("selected");
    });
    await page.locator(`#thread-focus #note-${rootID}`).tap();

    await expect(page).toHaveURL(new RegExp(`/thread/${rootID}$`), { timeout: 5_000 });
    await expect(page.locator(`#thread-focus #note-${rootID}.is-focused`)).toBeVisible({ timeout: 30_000 });
    const optimisticReply = page.locator(`#thread-replies #note-${replyID}.comment`);
    await expect(optimisticReply).toBeVisible({ timeout: 500 });
    const avatarBox = await optimisticReply.evaluate((reply) => {
      const avatar = reply.querySelector(":scope > .comment-avatar");
      if (!(avatar instanceof HTMLElement)) return null;
      const image = document.createElement("img");
      image.alt = "";
      image.src = "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw==";
      avatar.replaceChildren(image);
      const rect = image.getBoundingClientRect();
      return { width: rect.width, height: rect.height };
    });
    expect(avatarBox).not.toBeNull();
    expect(avatarBox.width).toBeLessThanOrEqual(48);
    expect(Math.abs(avatarBox.width - avatarBox.height)).toBeLessThanOrEqual(1);
    await rootHydrateResponse;
    await expect(page.locator(`#thread-replies #note-${replyID}`)).toHaveCount(1);
  });

  test("desktop click on a child reply changes the focused note", async ({ browser, request }) => {
    const seed = await request.post("/debug/seed-thread-wot");
    expect(seed.ok()).toBeTruthy();
    const payload = await seed.json();
    const rootID = payload.root_id;
    const replyID = payload.trusted_reply_id;

    const context = await browser.newContext({
      viewport: { width: 1440, height: 1000 },
      isMobile: false,
      hasTouch: false,
    });
    const page = await context.newPage();

    await page.goto(`/thread/${rootID}?wot=0`);
    await expect(page.locator(`#thread-focus #note-${rootID}`)).toBeVisible({ timeout: 30_000 });

    const replyCard = page.locator(`#thread-replies #note-${replyID}, .thread-replies #note-${replyID}`).first();
    await expect(replyCard).toBeVisible({ timeout: 30_000 });
    await replyCard.click();

    await expect(page).toHaveURL(new RegExp(`/thread/${rootID}\\?selected=${replyID}#note-${replyID}$`), {
      timeout: 5_000,
    });
    await expect(page.locator(`#thread-focus #note-${replyID}.is-focused`)).toBeVisible({ timeout: 30_000 });
    await expect(page.locator(`#thread-focus .thread-focus-parent#note-${rootID}`)).toBeVisible({ timeout: 30_000 });
    await expect(page.locator(`#thread-replies #note-${replyID}`)).toHaveCount(0);

    await context.close();
  });

  test("desktop inline reply composer renders as a sibling reply card", async ({ browser, request }) => {
    const seeded = await request.post("/debug/seed-thread-wot");
    expect(seeded.ok()).toBeTruthy();
    const fixture = await seeded.json();

    const context = await browser.newContext({
      viewport: { width: 1440, height: 900 },
      isMobile: false,
      hasTouch: false,
    });
    const page = await context.newPage();
    await installRelayNativeE2E(page, {
      events: buildCombinedFixture(),
      viewerPubkey: VIEWER_PK,
      wotEnabled: false,
      responseDelayMs: 3_000,
    });
    await installSigningSession(page);

    await page.goto(`/thread/${fixture.root_id}?wot=0`);
    const replyCard = page.locator(`#thread-replies #note-${fixture.trusted_reply_id}`);
    await expect(replyCard).toBeVisible({ timeout: 30_000 });
    await replyCard.evaluate((card) => {
      const sibling = card.cloneNode(true);
      sibling.removeAttribute("id");
      sibling.setAttribute("aria-hidden", "true");
      card.parentElement?.append(sibling);
    });

    const clickedAt = Date.now();
    await replyCard.locator("[data-reply-action]").last().click();

    const composer = page.locator("#thread-replies > .thread-inline-reply");
    await expect(composer).toBeVisible({ timeout: 1_500 });
    expect(Date.now() - clickedAt).toBeLessThan(1_500);
    await expect(composer.locator("textarea[data-composer-content]")).toBeFocused();
    await expect(replyCard.locator(".ascii-reply .thread-inline-reply")).toHaveCount(0);

    const placement = await composer.evaluate((node) => {
      const card = node.previousElementSibling;
      const cardBox = card?.getBoundingClientRect();
      const composerBox = node.getBoundingClientRect();
      const replyRail = card?.querySelector(":scope > .ascii-reply");
      const composerRail = node.querySelector(".thread-inline-reply__rail-pre");
      return {
        followsReply: card?.matches(".comment") === true,
        verticalGap: cardBox ? composerBox.top - cardBox.bottom : Number.POSITIVE_INFINITY,
        replyRailX: replyRail?.getBoundingClientRect().left || 0,
        composerRailX: composerRail?.getBoundingClientRect().left || 0,
      };
    });
    expect(placement.followsReply).toBeTruthy();
    expect(placement.verticalGap).toBeGreaterThanOrEqual(0);
    expect(placement.verticalGap).toBeLessThan(40);
    expect(Math.abs(placement.composerRailX - placement.replyRailX)).toBeLessThanOrEqual(1);

    await context.close();
  });

  test("desktop text selection on the already focused note does not reload focus", async ({ browser, request }) => {
    const seed = await request.post("/debug/seed-thread-wot");
    expect(seed.ok()).toBeTruthy();
    const payload = await seed.json();
    const rootID = payload.root_id;
    const replyID = payload.trusted_reply_id;

    const context = await browser.newContext({
      viewport: { width: 1440, height: 1000 },
      isMobile: false,
      hasTouch: false,
    });
    const page = await context.newPage();
    let focusHydrateRequests = 0;
    await page.route("**/thread/**", async (route) => {
      const url = new URL(route.request().url());
      if (url.searchParams.get("fragment") === "hydrate") {
        focusHydrateRequests += 1;
      }
      await route.continue();
    });

    await page.goto(`/thread/${rootID}?wot=0`);
    await expect(page.locator(`#thread-focus #note-${rootID}`)).toBeVisible({ timeout: 30_000 });

    const replyCard = page.locator(`#thread-replies #note-${replyID}, .thread-replies #note-${replyID}`).first();
    await expect(replyCard).toBeVisible({ timeout: 30_000 });
    await replyCard.click();
    await expect(page.locator(`#thread-focus #note-${replyID}.is-focused`)).toBeVisible({ timeout: 30_000 });

    const contentLine = page.locator(`#thread-focus #note-${replyID} .note-content .ascii-line`).filter({ hasText: "trusted reply" }).first();
    await expect(contentLine).toBeVisible({ timeout: 5_000 });
    // The click paints immediately, then the hydrate response replaces the
    // focused card. Wait for that expected replacement before taking mouse
    // coordinates so this assertion exercises the stable interactive node.
    await page.waitForTimeout(300);
    await expect(contentLine).toBeVisible({ timeout: 5_000 });
    const focusedURL = page.url();
    focusHydrateRequests = 0;
    const box = await contentLine.boundingBox();
    expect(box).toBeTruthy();
    await page.mouse.move(box.x + 8, box.y + box.height / 2);
    await page.mouse.down();
    await page.mouse.move(box.x + Math.min(box.width - 8, 180), box.y + box.height / 2, { steps: 8 });
    await page.mouse.up();

    await expect.poll(async () => page.evaluate(() => window.getSelection()?.toString() || "")).toContain("reply");
    await page.waitForTimeout(300);
    expect(page.url()).toBe(focusedURL);
    expect(focusHydrateRequests).toBe(0);

    await context.close();
  });

  test("selected query wins when the thread hash still points at the root", async ({ page, request }) => {
    const seed = await request.post("/debug/seed-thread-wot");
    expect(seed.ok()).toBeTruthy();
    const payload = await seed.json();
    const rootID = payload.root_id;
    const replyID = payload.trusted_reply_id;

    await page.goto(`/thread/${rootID}?selected=${replyID}&wot=0#note-${rootID}`);

    await expect(page.locator(`#thread-focus #note-${replyID}.is-focused`)).toBeVisible({ timeout: 30_000 });
    await expect(page).toHaveURL(new RegExp(`/thread/${rootID}\\?selected=${replyID}&wot=0#note-${replyID}$`), {
      timeout: 5_000,
    });
  });

  test("clicking a reply canonicalizes stale selected hashes before painting focus", async ({ page, request }) => {
    const seed = await request.post("/debug/seed-thread-wot");
    expect(seed.ok()).toBeTruthy();
    const payload = await seed.json();
    const rootID = payload.root_id;
    const replyID = payload.trusted_reply_id;

    await page.goto(`/thread/${rootID}?wot=0`);
    await expect(page.locator(`#thread-focus #note-${rootID}`)).toBeVisible({ timeout: 30_000 });

    const replyCard = page.locator(`#thread-replies #note-${replyID}, .thread-replies #note-${replyID}`).first();
    await expect(replyCard).toBeVisible({ timeout: 30_000 });
    await replyCard.evaluate((node, { rootID, replyID }) => {
      node.setAttribute("data-ascii-select-href", `/thread/${rootID}?selected=${replyID}#note-${rootID}`);
    }, { rootID, replyID });
    await replyCard.click();

    await expect(page).toHaveURL(new RegExp(`/thread/${rootID}\\?selected=${replyID}#note-${replyID}$`), {
      timeout: 5_000,
    });
    await expect(page.locator(`#thread-focus #note-${replyID}.is-focused`)).toBeVisible({ timeout: 30_000 });
    await expect(page.locator(`#thread-focus .thread-focus-parent#note-${rootID}`)).toBeVisible({ timeout: 30_000 });
    await expect(page.locator(`#thread-replies #note-${replyID}`)).toHaveCount(0);
  });

  test("mobile tap on reply body text changes the focused note", async ({ page, request }) => {
    const seed = await request.post("/debug/seed-thread-wot");
    expect(seed.ok()).toBeTruthy();
    const payload = await seed.json();
    const rootID = payload.root_id;
    const replyID = payload.trusted_reply_id;

    await page.goto(`/thread/${rootID}?wot=0`);
    await expect(page.locator(`#thread-focus #note-${rootID}`)).toBeVisible({ timeout: 30_000 });

    const replyCard = page.locator(`#thread-replies #note-${replyID}, .thread-replies #note-${replyID}`).first();
    await expect(replyCard).toBeVisible({ timeout: 30_000 });
    await replyCard.getByText("trusted reply").tap();

    await expect(page).toHaveURL(new RegExp(`/thread/${rootID}\\?selected=${replyID}(&wot=0)?#note-${replyID}$`), {
      timeout: 5_000,
    });
    await expect(page.locator(`#thread-focus #note-${replyID}.is-focused`)).toBeVisible({ timeout: 30_000 });
    await expect(page.locator(`#thread-focus .thread-focus-parent#note-${rootID}`)).toBeVisible({ timeout: 30_000 });
  });

  test("reply body tap paints selection before a cold hydrate response returns", async ({ page, request }) => {
    let releaseHydrate;
    const hydrateReleased = new Promise((resolve) => {
      releaseHydrate = resolve;
    });
    const seed = await request.post("/debug/seed-thread-wot");
    expect(seed.ok()).toBeTruthy();
    const payload = await seed.json();
    const rootID = payload.root_id;
    const replyID = payload.trusted_reply_id;

    await page.goto(`/thread/${rootID}?wot=0`);
    await expect(page.locator(`#thread-focus #note-${rootID}`)).toBeVisible({ timeout: 30_000 });

    const replyCard = page.locator(`#thread-replies #note-${replyID}, .thread-replies #note-${replyID}`).first();
    await expect(replyCard).toBeVisible({ timeout: 30_000 });
    await page.route("**/thread/**", async (route) => {
      const url = new URL(route.request().url());
      if (url.searchParams.get("fragment") === "hydrate") {
        await hydrateReleased;
      }
      await route.continue();
    });
    await replyCard.getByText("trusted reply").tap();

    await expect(page).toHaveURL(new RegExp(`/thread/${rootID}\\?selected=${replyID}(&wot=0)?#note-${replyID}$`), {
      timeout: 1_000,
    });
    await expect(page.locator(`#thread-focus #note-${replyID}.is-focused`)).toBeVisible({ timeout: 1_000 });
    await expect(page.locator(`#thread-focus .thread-focus-parent#note-${rootID}`)).toBeVisible({ timeout: 1_000 });
    await expect(page.locator(`#thread-replies #note-${replyID}`)).toHaveCount(0);

    releaseHydrate();
    await expect(page.locator(`#thread-focus #note-${replyID}.is-focused`)).toBeVisible({ timeout: 30_000 });
  });

  test("selecting a tree reply writes the canonical selected URL", async ({ page, request }) => {
    const seed = await request.post("/debug/seed-thread-wot");
    expect(seed.ok()).toBeTruthy();
    const payload = await seed.json();
    const rootID = payload.root_id;
    const replyID = payload.trusted_reply_id;

    await page.goto(`/thread/${rootID}?wot=0`);
    await expect(page.locator(`#thread-focus #note-${rootID}`)).toBeVisible({ timeout: 30_000 });

    await page.locator(".mobile-bar [data-thread-view-toggle], .thread-view-toggle-desktop [data-thread-view-toggle]").first().click();
    await expect(page.locator("#thread-tree-view")).toBeVisible({ timeout: 8_000 });
    await page.locator(`#thread-tree-view [data-thread-tree-note="note-${replyID}"]`).tap();

    await expect(page).toHaveURL(new RegExp(`/thread/${rootID}\\?wot=0&selected=${replyID}#note-${replyID}$|/thread/${rootID}\\?selected=${replyID}&wot=0#note-${replyID}$`), {
      timeout: 5_000,
    });
    await expect(page.locator(`#thread-focus #note-${replyID}.is-focused`)).toBeVisible({ timeout: 30_000 });
  });

  test("tapping feed note media opens the note thread on the first tap", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${MEDIA_FEED_NOTE_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, {
      events: buildCombinedFixture(),
      wotEnabled: false,
      responseDelayMs: 1_500,
    });

    await page.goto("/feed");
    const mediaCard = page.locator(`#note-${MEDIA_FEED_NOTE_ID}`);
    await expect(mediaCard).toBeVisible({ timeout: 30_000 });

    const mediaTile = mediaCard.locator(".note-media-tile").first();
    await expect(mediaTile).toBeVisible({ timeout: 8_000 });
    await mediaTile.tap();

    await expect(page).toHaveURL(new RegExp(`/thread/${MEDIA_FEED_NOTE_ID}$`), {
      timeout: 2_500,
    });
    await expect(page.locator(`#thread-focus #note-${MEDIA_FEED_NOTE_ID}`)).toBeVisible({
      timeout: 8_000,
    });
  });

  test("guest video controls do not reload the selected note", async ({ page, request }) => {
    const port = Number(process.env.PTXT_E2E_PORT || 18080);
    const content = `e2e-feed-video-note http://127.0.0.1:${port}/static/missing-video.mp4`;
    const seed = await request.post(
      `/debug/seed-note?id=${VIDEO_FEED_NOTE_ID}&pubkey=${AUTHOR_PK}&content=${encodeURIComponent(content)}`,
    );
    expect(seed.ok()).toBeTruthy();

    await page.goto(`/thread/${VIDEO_FEED_NOTE_ID}`);
    const mediaCard = page.locator(`#thread-focus #note-${VIDEO_FEED_NOTE_ID}`);
    await expect(mediaCard).toBeVisible({ timeout: 30_000 });
    const video = mediaCard.locator(".note-media-video-tile video");
    await expect(video).toBeVisible({ timeout: 8_000 });
    await video.evaluate((control) => {
      control.dataset.playbackControl = "guest";
    });

    const beforeURL = page.url();
    const clickAllowed = await video.evaluate((control) =>
      control.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true })),
    );

    expect(clickAllowed).toBe(true);
    await expect(page).toHaveURL(beforeURL);
    await expect(mediaCard.locator("video[data-playback-control='guest']")).toBeVisible();
  });

  test("tapping a feed video control does not re-select the note", async ({ page, request }) => {
    const port = Number(process.env.PTXT_E2E_PORT || 18080);
    const content = `e2e-feed-video-note http://127.0.0.1:${port}/static/missing-video.mp4`;
    const seed = await request.post(
      `/debug/seed-note?id=${VIDEO_FEED_NOTE_ID}&pubkey=${AUTHOR_PK}&content=${encodeURIComponent(content)}`,
    );
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, {
      events: buildCombinedFixture(),
      wotEnabled: false,
      responseDelayMs: 1_500,
    });

    await page.goto("/feed");
    const mediaCard = page.locator(`#note-${VIDEO_FEED_NOTE_ID}`);
    await expect(mediaCard).toBeVisible({ timeout: 30_000 });
    const video = mediaCard.locator(".note-media-video-tile video");
    await expect(video).toBeVisible({ timeout: 8_000 });
    await video.evaluate((control) => {
      control.dataset.playbackControl = "1";
    });

    const beforeURL = page.url();
    await mediaCard.locator("video").tap();

    await expect(page).toHaveURL(beforeURL);
    await expect(mediaCard.locator("video[data-playback-control='1']")).toBeVisible();
    const clickAllowed = await mediaCard.locator("video").evaluate((control) =>
      control.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true })),
    );
    expect(clickAllowed).toBe(true);
    await expect(page).toHaveURL(beforeURL);
  });

  test("tapping media inside the selected thread note opens the image viewer instead of re-navigating the note", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${MEDIA_FEED_NOTE_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, {
      events: buildCombinedFixture(),
      wotEnabled: false,
      responseDelayMs: 1_500,
    });

    await page.goto("/feed");
    const mediaCard = page.locator(`#note-${MEDIA_FEED_NOTE_ID}`);
    await expect(mediaCard).toBeVisible({ timeout: 30_000 });

    await mediaCard.locator(".note-media-tile").first().tap();
    await expect(page).toHaveURL(new RegExp(`/thread/${MEDIA_FEED_NOTE_ID}$`), {
      timeout: 2_500,
    });

    const focused = page.locator(`#thread-focus #note-${MEDIA_FEED_NOTE_ID}`);
    await expect(focused).toBeVisible({ timeout: 8_000 });
    const beforeURL = page.url();

    await focused.locator(".note-media-tile").first().tap();

    await expect(page.locator("[data-image-viewer-dialog]")).toBeVisible({ timeout: 5_000 });
    await expect(page).toHaveURL(beforeURL);
  });

  test("desktop click from a deep feed scroll shows the thread at the top of the viewport", async ({ browser, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    const context = await browser.newContext({
      viewport: { width: 1440, height: 900 },
      isMobile: false,
      hasTouch: false,
    });
    const page = await context.newPage();
    await installRelayNativeE2E(page, {
      events: buildCombinedFixture(),
      wotEnabled: false,
      responseDelayMs: 1_500,
    });

    await page.goto("/feed");
    const replyCard = page.locator(`#note-${REPLY_ID}`);
    await expect(replyCard).toBeVisible({ timeout: 30_000 });

    await page.evaluate(() => {
      const feed = document.querySelector("#feed");
      if (!feed || document.querySelector("[data-e2e-scroll-spacer]")) return;
      const spacer = document.createElement("div");
      spacer.dataset.e2eScrollSpacer = "1";
      spacer.style.height = "1100px";
      spacer.style.pointerEvents = "none";
      feed.prepend(spacer);
      window.scrollTo(0, 950);
    });
    await expect.poll(() => page.evaluate(() => Math.round(window.scrollY))).toBeGreaterThan(800);
    await expect(replyCard).toBeVisible({ timeout: 5_000 });

    await replyCard.click();

    await expect(page).toHaveURL(new RegExp(`/thread/${ROOT_ID}\\?selected=${REPLY_ID}#note-${REPLY_ID}$`), {
      timeout: 2_500,
    });
    await expect(page.locator(`#thread-focus #note-${REPLY_ID}`)).toBeVisible({ timeout: 8_000 });
    await expect(page.locator("#feed-heading")).not.toBeVisible();
    await expect.poll(() => page.evaluate(() => Math.round(window.scrollY))).toBeLessThanOrEqual(4);
    const focusedTop = await page.locator(`#thread-focus #note-${REPLY_ID}`).evaluate((node) => {
      return Math.round(node.getBoundingClientRect().top);
    });
    expect(focusedTop).toBeGreaterThanOrEqual(0);
    expect(focusedTop).toBeLessThan(500);

    await context.close();
  });
});
