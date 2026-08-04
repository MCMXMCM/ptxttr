// @ts-check
import { test, expect } from "@playwright/test";
import { nip19 } from "../web/static/lib/nostr-tools.js";

import {
  AUTHOR_PK,
  OLDER_PROFILE_NOTE_ID,
  REPLY_AUTHOR_PK,
  REPLY_ID,
  ROOT_ID,
  buildOlderProfileNoteFixture,
  buildThreadFixture,
} from "./helpers/nostr-fixtures.js";
import { installRelayNativeE2E, navigateToThreadFromFeed } from "./helpers/mock-nostr-relay.js";

test.describe("profile note thread navigation", () => {
  test("profile posts expose canonical thread document links once rendered", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, {
      events: buildThreadFixture(),
      wotEnabled: false,
      responseDelayMs: 1500,
    });
    await page.goto(`/u/${AUTHOR_PK}`);
    const note = page.locator(`#user-panel-posts #note-${ROOT_ID}`);
    await expect(note).toBeVisible({ timeout: 30_000 });
    await note.scrollIntoViewIfNeeded();

    const threadLink = note.locator(`a[href="/thread/${ROOT_ID}"]`).first();
    await expect(threadLink).toHaveAttribute("href", `/thread/${ROOT_ID}`);
    await expect(page.locator('[data-route-keepalive-layer]')).toHaveCount(0);
  });

  test("clicking a profile post opens the same relay-native thread view as feed notes", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, {
      events: buildThreadFixture(),
      wotEnabled: false,
      responseDelayMs: 1500,
    });
    await page.goto(`/u/${AUTHOR_PK}`);
    const profileCard = page.locator(`#user-panel-posts #note-${ROOT_ID}`);
    await expect(profileCard).toBeVisible({ timeout: 30_000 });
    await expect.poll(async () => profileCard.evaluate((note) => (
      note instanceof HTMLElement && note.dataset.asciiKind === "note" && Boolean(note.querySelector(":scope > pre.ascii-card"))
    ))).toBe(true);

    await profileCard.locator(".note-content").click();

    await expect(page).toHaveURL(new RegExp(`/thread/${ROOT_ID}$`), { timeout: 5_000 });
    await expect(page.locator("#thread-focus .note, #thread-focus .comment")).toBeVisible({ timeout: 5_000 });
    await expect(page.locator("#thread-focus .thread-focus-skeleton")).toHaveCount(0);
    await expect(page.locator("#thread-focus").getByText("e2e-relay-native-thread-root")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.locator(".feed-column[data-relay-native-thread='1']")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.locator("#thread-replies").getByText("e2e-relay-native-thread-reply")).toBeVisible({
      timeout: 15_000,
    });
  });

  test("tapping a profile post uses the feed-style thread morph and keeps the selected note intact", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, {
      events: buildThreadFixture(),
      wotEnabled: false,
      responseDelayMs: 400,
    });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(`/u/${AUTHOR_PK}`);
    const profileCard = page.locator(`#user-panel-posts #note-${ROOT_ID}`);
    await expect(profileCard).toBeVisible({ timeout: 30_000 });
    await expect.poll(async () => page.evaluate((noteID) => {
      const note = document.querySelector(`#user-panel-posts #note-${noteID}`);
      if (!(note instanceof HTMLElement)) return false;
      return note.dataset.asciiKind === "note" && Boolean(note.querySelector(":scope > pre.ascii-card"));
    }, ROOT_ID)).toBe(true);

    await profileCard.click();

    await expect.poll(async () => {
      return page.evaluate(() => document.querySelector("#ptxt-ascii-paper") !== null);
    }).toBe(false);

    await expect(page).toHaveURL(new RegExp(`/thread/${ROOT_ID}$`), { timeout: 10_000 });
    await expect(page.locator('[data-route-keepalive-layer]')).toHaveCount(0);
    await expect(page.locator("#thread-focus").getByText("e2e-relay-native-thread-root")).toBeVisible({
      timeout: 30_000,
    });
    await expect.poll(async () => page.evaluate((noteID) => {
      const note = document.querySelector(`#thread-focus #note-${noteID}`);
      if (!(note instanceof HTMLElement)) return false;
      return Boolean(note.querySelector(":scope > .note-avatar, :scope > .comment-avatar"))
        && !note.classList.contains("ptxt-carried-thread-note")
        && !document.documentElement.classList.contains("ptxt-thread-route-transition");
    }, ROOT_ID)).toBe(true);
  });

  test("profile reply avatar stays bounded while its parent loads", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${"d".repeat(64)}&pubkey=${REPLY_AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();
    await installRelayNativeE2E(page, {
      events: buildThreadFixture(),
      wotEnabled: false,
      responseDelayMs: 400,
    });
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.goto(`/u/${REPLY_AUTHOR_PK}`);
    await page.locator('label[for="user-tab-replies"]').click();

    const replyCard = page.locator(`#user-panel-replies #note-${REPLY_ID}`);
    await expect(replyCard).toBeVisible({ timeout: 30_000 });
    await replyCard.evaluate((note) => {
      const avatar = note.querySelector(":scope > pre .note-feed-avatar");
      if (!(avatar instanceof HTMLElement)) throw new Error("Missing profile reply avatar");
      const image = document.createElement("img");
      image.alt = "";
      image.src = "data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='500' height='500'%3E%3Crect width='500' height='500' fill='%23555'/%3E%3C/svg%3E";
      avatar.replaceChildren(image);
    });

    await replyCard.locator(".note-content").click();

    await expect(page).toHaveURL(new RegExp(`/thread/${ROOT_ID}\\?selected=${REPLY_ID}#note-${REPLY_ID}$`), {
      timeout: 5_000,
    });
    await expect(page.locator("#thread-focus .thread-focus-parent--skeleton")).toBeVisible({ timeout: 1_000 });
    const selectedAvatar = page.locator(`#thread-focus #note-${REPLY_ID} > .note-avatar img`);
    await expect(selectedAvatar).toBeVisible({ timeout: 1_000 });
    const avatarBox = await selectedAvatar.evaluate((image) => {
      const rect = image.getBoundingClientRect();
      return { width: rect.width, height: rect.height };
    });
    expect(avatarBox.width).toBeLessThanOrEqual(48);
    expect(Math.abs(avatarBox.width - avatarBox.height)).toBeLessThanOrEqual(1);
  });

  test("home click interrupts a slow thread navigation instead of getting stuck behind it", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, {
      events: buildThreadFixture(),
      wotEnabled: false,
      responseDelayMs: 4_000,
    });
    await page.goto(`/u/${AUTHOR_PK}`);
    await expect(page.locator(`#user-panel-posts #note-${ROOT_ID}`)).toBeVisible({ timeout: 30_000 });

    await page.locator(`#user-panel-posts #note-${ROOT_ID}`).click();
    await expect(page).toHaveURL(new RegExp(`/thread/${ROOT_ID}$`));
    await expect(page.locator("#thread-focus")).toBeVisible();

    const beforeHomeClick = Date.now();
    await page.locator('a[data-main-menu-link][href="/"]').first().click();
    await expect(page).toHaveURL(/\/$/);
    await expect(page.locator("[data-feed]")).toBeVisible();
    expect(Date.now() - beforeHomeClick).toBeLessThan(1_500);

    await page.waitForTimeout(4_300);
    await expect(page).toHaveURL(/\/$/);
  });

  test("feed to profile navigation reuses the rendered identity before canonical data finishes", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    const knownAvatar = "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw==";
    const knownNpub = nip19.npubEncode(AUTHOR_PK);
    await page.goto("/feed");
    await page.locator("[data-feed-heading]").waitFor({ state: "visible" });
    await page.evaluate(({ pubkey, avatar }) => {
      const card = document.createElement("article");
      card.className = "note";
      card.dataset.asciiAuthor = "Tim Bouma";
      card.dataset.asciiAvatar = avatar;
      const link = document.createElement("a");
      link.href = `/u/${pubkey}`;
      link.textContent = "Tim Bouma";
      card.append(link);
      document.querySelector("#feed")?.prepend(card);
    }, { pubkey: AUTHOR_PK, avatar: knownAvatar });

    const fullProfileDocumentRoute = (url) => {
      const parsed = new URL(url);
      return parsed.pathname === `/u/${AUTHOR_PK}` && !parsed.searchParams.has("fragment");
    };
    const profileHeaderRoute = (url) => {
      const parsed = new URL(url);
      return parsed.pathname === `/u/${AUTHOR_PK}` && parsed.searchParams.get("fragment") === "header";
    };
    const delayedFullProfileDocument = async (route) => {
      const response = await route.fetch();
      await new Promise((resolve) => setTimeout(resolve, 2_000));
      await route.fulfill({ response });
    };
    let profileDocumentRequests = 0;
    page.on("request", (profileRequest) => {
      const url = new URL(profileRequest.url());
      if (url.pathname === `/u/${AUTHOR_PK}` && profileRequest.resourceType() === "document") {
        profileDocumentRequests += 1;
      }
    });
    await page.route(profileHeaderRoute, async (route) => {
      await new Promise((resolve) => setTimeout(resolve, 150));
      await route.fulfill({
        status: 200,
        contentType: "text/html; charset=utf-8",
        body: `<section class="profile profile-modern">
          <h1 class="profile-display-name">Tim Bouma</h1>
          <div class="profile-avatar-wrap"><div class="profile-avatar-fallback">@</div></div>
          <div class="profile-hero-side">
            <p class="profile-payment-line"><span class="profile-payment-value">tim@getalby.com</span></p>
            <div class="profile-npub-block profile-npub-copy" data-profile-npub-copy data-npub="${knownNpub}"></div>
          </div>
          <div class="profile-main"><div class="profile-ident"><p>Known profile bio</p></div></div>
        </section>`,
      });
    });
    await page.route(fullProfileDocumentRoute, delayedFullProfileDocument);

    try {
      await page.locator(`#feed a[href="/u/${AUTHOR_PK}"]`).first().click();

      await expect(page).toHaveURL(new RegExp(`/u/${AUTHOR_PK}$`));
      await expect(page.locator("#user-header")).toBeVisible({ timeout: 800 });
      await expect(page.locator("#user-header .profile-display-name")).toHaveText("Tim Bouma", { timeout: 800 });
      await expect(page.locator("#user-header .profile-display-name")).not.toHaveClass(/text-skeleton/);
      await expect(page.locator("#user-header img.profile-avatar")).toHaveAttribute(
        "data-ptxt-avatar-original-src",
        knownAvatar,
        { timeout: 800 },
      );
      await expect(page.locator("#user-header [data-profile-npub-copy]")).toHaveAttribute(
        "data-npub",
        knownNpub,
        { timeout: 800 },
      );
      await expect(page.locator("#user-header .profile-ident")).toContainText("Known profile bio", { timeout: 800 });
      await expect(page.locator("#user-header .profile-payment-value")).toHaveText("tim@getalby.com", { timeout: 800 });
      await expect(page.locator('[data-retro-loader-type="profile-posts"] [data-retro-loader-progress]')).not.toHaveText("");
      await expect(page.locator('[data-retro-loader-type="profile-posts"] [data-retro-loader-activity]')).toContainText(
        "profile details loaded; waiting for posts...",
      );
      await expect(page.locator("#user-panel-posts #note-" + ROOT_ID)).toHaveCount(0);
      expect(profileDocumentRequests).toBe(0);

      await expect(page.locator("#user-panel-posts #note-" + ROOT_ID)).toBeVisible({ timeout: 30_000 });
    } finally {
      await page.unroute(profileHeaderRoute);
      await page.unroute(fullProfileDocumentRoute, delayedFullProfileDocument);
    }
  });

  test("thread reply author profile renders that author's reply timeline", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, {
      events: buildThreadFixture(),
      wotEnabled: false,
      responseDelayMs: 400,
    });
    await page.goto(`/u/${AUTHOR_PK}`);
    await expect(page.locator(`#user-panel-posts #note-${ROOT_ID}`)).toBeVisible({ timeout: 30_000 });
    await page.locator(`#user-panel-posts #note-${ROOT_ID}`).click();
    await expect(page).toHaveURL(new RegExp(`/thread/${ROOT_ID}$`), { timeout: 10_000 });
    await expect(page.locator(`#thread-replies #note-${REPLY_ID}`).getByText("e2e-relay-native-thread-reply")).toBeVisible({
      timeout: 30_000,
    });

    await page.locator(`#thread-replies #note-${REPLY_ID} a[href="/u/${REPLY_AUTHOR_PK}"]`).first().click();
    await expect(page).toHaveURL(new RegExp(`/u/${REPLY_AUTHOR_PK}$`));
    await expect(page.locator("#user-header").getByText("Reply Author")).toBeVisible({ timeout: 30_000 });

    await page.locator('label[for="user-tab-replies"]').click();
    await expect(page.locator(`#user-panel-replies #note-${REPLY_ID}`).getByText("e2e-relay-native-thread-reply")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.locator(`#user-panel-posts #note-${REPLY_ID}`)).toHaveCount(0);
  });

  test("feed author click navigates to profile and keeps the post in feed layout", async ({ page }) => {
    await installRelayNativeE2E(page, {
      events: buildThreadFixture(),
      wotEnabled: false,
      responseDelayMs: 400,
    });
    await page.goto("/feed");
    await expect(page.locator(`#feed #note-${ROOT_ID}`)).toBeVisible({ timeout: 30_000 });

    await page.locator(`#feed #note-${ROOT_ID} a[href^="/u/"]`).first().click();
    await expect(page).toHaveURL(/\/u\//);
    await expect(page.locator(`#user-panel-posts #note-${ROOT_ID}`)).toBeVisible({ timeout: 30_000 });

    await expect.poll(async () => page.evaluate((noteID) => {
      const note = document.querySelector(`#user-panel-posts #note-${noteID}`);
      if (!(note instanceof HTMLElement)) return false;
      return note.dataset.asciiKind === "note"
        && Boolean(note.querySelector(":scope > pre.ascii-card"))
        && !note.classList.contains("thread-focus-selected")
        && !note.classList.contains("ptxt-carried-profile-note");
    }, ROOT_ID)).toBe(true);
  });

  test("thread to profile removes carried notes absent from the profile posts page", async ({ page }) => {
    await installRelayNativeE2E(page, {
      events: buildOlderProfileNoteFixture(),
      wotEnabled: false,
      responseDelayMs: 400,
      omitProfilePostIds: [OLDER_PROFILE_NOTE_ID],
    });
    await navigateToThreadFromFeed(page, OLDER_PROFILE_NOTE_ID);
    await expect(page).toHaveURL(new RegExp(`/thread/${OLDER_PROFILE_NOTE_ID}$`), { timeout: 10_000 });
    await expect(page.locator("#thread-focus").getByText("e2e-older-profile-thread-root")).toBeVisible({
      timeout: 30_000,
    });

    await page.locator(`#thread-focus a[href="/u/${AUTHOR_PK}"]`).first().click();
    await expect(page).toHaveURL(new RegExp(`/u/${AUTHOR_PK}$`));
    await expect(page.locator("#user-panel-posts").getByText("e2e-newer-profile-note-26")).toBeVisible({
      timeout: 30_000,
    });

    await expect(page.locator(`#user-panel-posts #note-${OLDER_PROFILE_NOTE_ID}`)).toHaveCount(0);
    await expect(page.locator("#user-panel-posts .ptxt-carried-profile-note")).toHaveCount(0);
  });

  test("profile to thread to profile keeps the post in feed layout", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, {
      events: buildThreadFixture(),
      wotEnabled: false,
      responseDelayMs: 400,
    });
    await page.goto(`/u/${AUTHOR_PK}`);
    await expect(page.locator(`#user-panel-posts #note-${ROOT_ID}`)).toBeVisible({ timeout: 30_000 });

    await page.locator(`#user-panel-posts #note-${ROOT_ID}`).click();
    await expect(page).toHaveURL(new RegExp(`/thread/${ROOT_ID}$`), { timeout: 10_000 });
    await expect(page.locator("#thread-focus").getByText("e2e-relay-native-thread-root")).toBeVisible({
      timeout: 30_000,
    });

    await page.locator(`#thread-focus a[href="/u/${AUTHOR_PK}"]`).first().click();
    await expect(page).toHaveURL(new RegExp(`/u/${AUTHOR_PK}$`));
    await expect(page.locator(`#user-panel-posts #note-${ROOT_ID}`)).toBeVisible({ timeout: 30_000 });

    await expect.poll(async () => page.evaluate((noteID) => {
      const note = document.querySelector(`#user-panel-posts #note-${noteID}`);
      if (!(note instanceof HTMLElement)) return false;
      return note.dataset.asciiKind === "note"
        && Boolean(note.querySelector(":scope > pre.ascii-card"))
        && !note.classList.contains("thread-focus-selected")
        && !note.classList.contains("ptxt-carried-profile-note");
    }, ROOT_ID)).toBe(true);
  });

  test("browser back from thread restores the profile post in feed layout", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, {
      events: buildThreadFixture(),
      wotEnabled: false,
      responseDelayMs: 400,
    });
    await page.goto(`/u/${AUTHOR_PK}`);
    await expect(page.locator(`#user-panel-posts #note-${ROOT_ID}`)).toBeVisible({ timeout: 30_000 });

    const beforeTop = await page.evaluate((id) => {
      const note = document.querySelector(`#user-panel-posts #note-${id}`);
      return note?.getBoundingClientRect().top ?? Number.NaN;
    }, ROOT_ID);

    await page.locator(`#user-panel-posts #note-${ROOT_ID}`).click();
    await expect(page).toHaveURL(new RegExp(`/thread/${ROOT_ID}$`), { timeout: 10_000 });

    await page.goBack();
    await expect(page).toHaveURL(new RegExp(`/u/${AUTHOR_PK}$`));
    await expect(page.locator(`#user-panel-posts #note-${ROOT_ID}`)).toBeVisible({ timeout: 30_000 });

    await expect.poll(async () => page.evaluate((noteID) => {
      const note = document.querySelector(`#user-panel-posts #note-${noteID}`);
      if (!(note instanceof HTMLElement)) return false;
      return note.dataset.asciiKind === "note"
        && Boolean(note.querySelector(":scope > pre.ascii-card"))
        && !note.classList.contains("thread-focus-selected");
    }, ROOT_ID)).toBe(true);

    const afterTop = await page.evaluate((id) => {
      const note = document.querySelector(`#user-panel-posts #note-${id}`);
      return note?.getBoundingClientRect().top ?? Number.NaN;
    }, ROOT_ID);
    expect(Math.abs(afterTop - beforeTop)).toBeLessThan(120);
  });
});
