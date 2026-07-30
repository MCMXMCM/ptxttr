// @ts-check
import { test, expect } from "@playwright/test";

import {
  AUTHOR_PK,
  BOOKMARK_NOTE_ID,
  FEED_NOTE_ID,
  REPLY_ID,
  ROOT_ID,
  VIEWER_PK,
  buildAvatarThreadFixture,
  buildBrokenAvatarProfileFixture,
  buildBookmarkFixture,
  buildCombinedFixture,
  buildFeedFixture,
  buildReplyOnlyFixture,
  buildThreadFixture,
} from "./helpers/nostr-fixtures.js";
import {
  blockServerFeedFragments,
  blockServerReadAPI,
  installMockNostrRelay,
  installRelayNativeE2E,
  installRelayNativePrefs,
  navigateToThreadFromFeed,
} from "./helpers/mock-nostr-relay.js";

test.describe("relay-native client reads", () => {
  test("home feed hydrates notes from the mock relay", async ({ page }) => {
    await installRelayNativeE2E(page, { events: buildFeedFixture(), wotEnabled: false });
    await page.goto("/feed");

    const feed = page.locator("#feed[data-relay-native-feed='1']");
    await expect(feed).toBeVisible({ timeout: 30_000 });
    await expect(feed.locator(`#note-${FEED_NOTE_ID}`)).toBeVisible({ timeout: 30_000 });
    await expect(feed.locator(`#note-${FEED_NOTE_ID} .note-feed-avatar`)).toBeVisible();
    await expect(feed.locator(`#note-${FEED_NOTE_ID} .note-feed-avatar img`)).toHaveCount(0);
    await expect(page.getByText("e2e-relay-native-feed-note")).toBeVisible({ timeout: 15_000 });
  });

  test("profile header falls back when the avatar image fails", async ({ page }) => {
    await installRelayNativeE2E(page, { events: buildBrokenAvatarProfileFixture(), wotEnabled: false });
    await page.goto(`/u/${AUTHOR_PK}`);

    const header = page.locator("#user-header");
    await expect(header.getByText("Broken Avatar Author")).toBeVisible({ timeout: 30_000 });
    await expect(header.locator(".profile-npub-block--skeleton")).toHaveCount(0);
    await expect(header.locator(".profile-npub-grid")).toBeVisible();
    await expect(header.locator(".profile-avatar-fallback")).toBeVisible({ timeout: 30_000 });
    await expect(header.locator("img.profile-avatar")).toHaveCount(0);
  });

  test("thread route hydrates root and replies from the mock relay", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, { events: buildThreadFixture(), wotEnabled: false });
    await navigateToThreadFromFeed(page, ROOT_ID);

    const column = page.locator(".feed-column[data-relay-native-thread='1']");
    await expect(column).toBeVisible({ timeout: 30_000 });
    await expect(page.locator("#thread-focus").getByText("e2e-relay-native-thread-root")).toBeVisible({
      timeout: 15_000,
    });
    await expect(page.locator("#thread-replies").getByText("e2e-relay-native-thread-reply")).toBeVisible({
      timeout: 15_000,
    });
    await expect(page.locator("#thread-replies").locator(`#note-${REPLY_ID}`)).toBeVisible();
  });

  test("bookmarks route loads bookmarked notes from the mock relay", async ({ page }) => {
    await installRelayNativeE2E(page, {
      events: buildBookmarkFixture(),
      viewerPubkey: VIEWER_PK,
      wotEnabled: false,
    });
    await page.goto("/feed");
    await page.getByRole("link", { name: "Bookmarks" }).click();

    const feed = page.locator("[data-feed]");
    await expect(feed).toBeVisible({ timeout: 30_000 });
    await expect(feed.locator(`#note-${BOOKMARK_NOTE_ID}`)).toBeVisible({ timeout: 30_000 });
    await expect(page.getByText("e2e-relay-native-bookmark-note")).toBeVisible({ timeout: 15_000 });
  });

  test("document navigation from feed to thread stays on relay-native data", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, { events: buildCombinedFixture(), wotEnabled: false });
    await page.goto("/feed");
    await expect(page.getByText("e2e-relay-native-feed-note")).toBeVisible({ timeout: 30_000 });

    await navigateToThreadFromFeed(page, ROOT_ID);
    await expect(page.locator(".feed-column[data-relay-native-thread='1']")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.locator("#thread-focus").getByText("e2e-relay-native-thread-root")).toBeVisible({
      timeout: 15_000,
    });
    await expect(page.locator("#thread-replies").getByText("e2e-relay-native-thread-reply")).toBeVisible({
      timeout: 15_000,
    });
  });

  test("feed click uses the note relay hint when current relays do not have the thread", async ({ page }) => {
    const viewerRelay = "wss://viewer-only.ptxt.test";
    const noteRelay = "wss://note-hint.ptxt.test";
    const events = buildThreadFixture();
    const root = events.find((event) => event.id === ROOT_ID);
    expect(root).toBeTruthy();

    await blockServerReadAPI(page);
    await blockServerFeedFragments(page);
    await installRelayNativePrefs(page, { relayURL: viewerRelay, wotEnabled: false });
    await installMockNostrRelay(page, { relayURL: noteRelay, events });

    await page.goto("/feed");
    await page.evaluate(({ rootEvent, relayURL }) => {
      const note = document.createElement("article");
      note.className = "note";
      note.id = `note-${rootEvent.id}`;
      note.dataset.asciiSelectHref = `/thread/${rootEvent.id}`;
      note.dataset.asciiRelay = relayURL;
      note.dataset.asciiEvent = JSON.stringify({ ...rootEvent, relay_url: relayURL });
      note.textContent = "hinted feed note";
      document.body.append(note);
    }, { rootEvent: root, relayURL: noteRelay });

    await page.locator(`#note-${ROOT_ID}`).click();

    await expect(page).toHaveURL(new RegExp(`/thread/${ROOT_ID}$`), { timeout: 5_000 });
    await expect(page.locator("#thread-focus").getByText("e2e-relay-native-thread-root")).toBeVisible({
      timeout: 15_000,
    });
    await expect(page.getByText("Thread not found on relays.")).toHaveCount(0);
  });

  test("feed tap opens a cached reply immediately and uses the nicer missing-parent skeleton", async ({ page }) => {
    await installRelayNativeE2E(page, {
      events: buildReplyOnlyFixture(),
      wotEnabled: false,
      responseDelayMs: 1500,
    });
    await page.goto("/feed");
    await expect(page.getByText("e2e-relay-native-thread-reply")).toBeVisible({ timeout: 30_000 });

    await page.evaluate(({ rootID, replyID }) => {
      const link = document.createElement("a");
      link.href = `/thread/${rootID}?selected=${replyID}#note-${replyID}`;
      link.setAttribute("data-relay-aware", "");
      document.body.append(link);
      link.click();
    }, { rootID: ROOT_ID, replyID: REPLY_ID });

    await expect(page.locator("#thread-focus").getByText("e2e-relay-native-thread-reply")).toBeVisible({
      timeout: 2_000,
    });
    await expect(page.locator("#thread-focus .thread-focus-parent--skeleton")).toBeVisible({
      timeout: 2_000,
    });
    await expect(page.locator("#thread-focus .thread-focus-skeleton")).toHaveCount(0);
    await page.waitForTimeout(5_000);
    await expect(page.locator("#thread-focus").getByText("e2e-relay-native-thread-reply")).toBeVisible();
    await expect(page.getByText("Thread not found on relays.")).toHaveCount(0);
  });

  test("first-ever guest thread visit stays on the authoritative cached document", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, {
      events: buildThreadFixture(),
      wotEnabled: false,
      responseDelayMs: 1200,
    });
    const response = await page.goto(`/thread/${ROOT_ID}`);

    expect(response?.headers()["x-ptxt-thread-status"]).toMatch(/^(ready|partial)$/);
    await expect(page.locator("#thread-focus .note")).toBeVisible({
      timeout: 2_000,
    });
    await expect(page.locator("#thread-focus .thread-focus-skeleton")).toHaveCount(0);
    await expect(page.locator("#thread-focus").getByText("e2e seeded note")).toBeVisible();
    await expect(page.locator("#thread-focus .thread-focus-parent--skeleton")).toHaveCount(0);
  });

  test("avatars survive feed to thread navigation", async ({ page, request }) => {
    const seed = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
    expect(seed.ok()).toBeTruthy();

    await installRelayNativeE2E(page, { events: buildAvatarThreadFixture(), wotEnabled: false });
    await page.goto("/feed");
    await expect(page.locator(`#note-${FEED_NOTE_ID} .note-feed-avatar img`)).toBeVisible({
      timeout: 30_000,
    });

    await navigateToThreadFromFeed(page, ROOT_ID);
    await expect(page.locator(".feed-column[data-relay-native-thread='1']")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.locator("#thread-focus .note-avatar img, #thread-focus .comment-avatar img")).toBeVisible({
      timeout: 30_000,
    });
    await expect(page.locator("#thread-replies .comment-avatar img")).toBeVisible({
      timeout: 30_000,
    });
  });
});
