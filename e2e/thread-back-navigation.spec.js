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

  test("server thread handoff cannot downgrade a rendered author identity", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await page.goto("/feed");
    await page.evaluate(({ noteID, pubkey }) => {
      const feed = document.querySelector("#feed");
      if (!(feed instanceof HTMLElement)) return;
      const note = document.createElement("article");
      note.className = "note";
      note.id = `note-${noteID}`;
      note.dataset.asciiKind = "note";
      note.dataset.asciiAuthor = "Stable Local Author";
      note.dataset.asciiAvatar = "/static/img/ascritch.png";
      note.dataset.asciiUserHref = `/u/${pubkey}`;
      note.dataset.replyPubkey = pubkey;
      note.dataset.asciiSelectHref = `/thread/${noteID}`;
      note.innerHTML = `<a class="note-feed-avatar" href="/u/${pubkey}"><img src="/static/img/ascritch.png" alt=""></a><a href="/thread/${noteID}" data-relay-aware>open local note</a>`;
      feed.replaceChildren(note);
    }, { noteID: ROOT_ID, pubkey: AUTHOR_PK });

    await page.getByRole("link", { name: "open local note" }).tap();
    await expect(page).toHaveURL(new RegExp(`/thread/${ROOT_ID}$`), { timeout: 5_000 });
    const focused = page.locator("#thread-focus [data-ascii-kind]").last();
    await expect(focused).toBeVisible({ timeout: 10_000 });
    await expect.poll(() => focused.getAttribute("data-ascii-author")).toBe("Stable Local Author");
    await expect(focused.locator(".note-feed-avatar img, .note-avatar img, .comment-avatar img")).toBeVisible();
  });

  test("relay-native tree handoff keeps the identity painted by the local server", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, {
      events: buildAvatarThreadFixture().filter((event) => event.kind !== 0),
      wotEnabled: false,
      responseDelayMs: 1_200,
    });
    await page.goto("/feed");
    await expect(page.locator("#feed[data-relay-native-feed='1']")).toBeAttached({ timeout: 30_000 });
    await navigateToThreadFromFeed(page, ROOT_ID);
    await expect(page.locator("#thread-tree-view")).toBeAttached();
    await page.evaluate(({ rootID, pubkey }) => {
      const host = document.querySelector("#thread-tree-view");
      if (!(host instanceof HTMLElement)) return;
      host.innerHTML = `
        <section data-thread-tree-view data-thread-tree-root-id="${rootID}">
          <div class="thread-tree-root-note" data-thread-tree-note="note-${rootID}" data-reply-pubkey="${pubkey}">
            <a class="hn-tree-avatar" href="/u/${pubkey}"><img class="thread-tree-avatar" src="/static/img/ascritch.png" alt=""></a>
            <span class="hn-comhead"><a href="/u/${pubkey}">Stable Server Author</a></span>
          </div>
        </section>`;
    }, { rootID: ROOT_ID, pubkey: AUTHOR_PK });
    const serverTreeCard = page.locator(`#thread-tree-view [data-thread-tree-note="note-${ROOT_ID}"]`);
    await expect(serverTreeCard).toBeAttached();
    await page.evaluate(() => {
      window.dispatchEvent(new CustomEvent("ptxt:viewer-prefs-changed"));
    });

    await expect(page.locator(".feed-column[data-relay-native-thread='1']")).toBeAttached({ timeout: 10_000 });
    const settledTreeCard = page.locator(`#thread-tree-view [data-thread-tree-note="note-${ROOT_ID}"]`);
    await expect(settledTreeCard.locator(".hn-comhead a[href^='/u/']")).toHaveText("Stable Server Author");
    await expect(settledTreeCard.locator(".hn-tree-avatar img")).toBeAttached();
  });

  test("cosmetic session enrichment does not restart the settled feed loader", async ({ page }) => {
    await installRelayNativeE2E(page, {
      events: buildAvatarThreadFixture(),
      wotEnabled: false,
      responseDelayMs: 100,
    });
    await page.goto("/feed");
    await expect(page.locator("#feed[data-relay-native-feed='1'] .note").first()).toBeVisible({ timeout: 30_000 });
    await expect(page.locator("[data-retro-loader-type='feed']")).toHaveCount(0);

    await page.evaluate(() => {
      window.__ptxtFeedLoaderInsertions = 0;
      const feed = document.querySelector("#feed");
      if (!(feed instanceof HTMLElement)) return;
      const observer = new MutationObserver((records) => {
        for (const record of records) {
          for (const node of record.addedNodes) {
            if (!(node instanceof Element)) continue;
            if (node.matches("[data-retro-loader-type='feed']") || node.querySelector("[data-retro-loader-type='feed']")) {
              window.__ptxtFeedLoaderInsertions += 1;
            }
          }
        }
      });
      observer.observe(feed, { childList: true, subtree: true });
      const session = JSON.parse(localStorage.getItem("ptxt_nostr_session") || "{}");
      window.dispatchEvent(new CustomEvent("ptxt:session", {
        detail: { ...session, profileLabel: "Enriched Local Profile", picture: "/static/img/ascritch.png" },
      }));
    });
    await page.waitForTimeout(600);

    await expect(page.locator("[data-retro-loader-type='feed']")).toHaveCount(0);
    expect(await page.evaluate(() => window.__ptxtFeedLoaderInsertions)).toBe(0);
  });
});
