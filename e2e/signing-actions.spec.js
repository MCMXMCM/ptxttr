// @ts-check
import { test, expect } from "@playwright/test";

import { finalizeEvent, getPublicKey, nip19 } from "../web/static/lib/nostr-tools.js";
import { AUTHOR_PK, ROOT_ID, buildThreadFixture } from "./helpers/nostr-fixtures.js";
import { installMockNostrRelay, installRelayNativeE2E } from "./helpers/mock-nostr-relay.js";

const RELAY_URL = "wss://signing-actions.ptxt.test";
const SIGNER_SECRET = new Uint8Array(32).fill(0x44);
const SIGNER_PUBKEY = getPublicKey(SIGNER_SECRET);
const SIGNER_NPUB = nip19.npubEncode(SIGNER_PUBKEY);
const EXISTING_FOLLOW_PUBKEY = "ab".repeat(32);
const EXISTING_FOLLOW = finalizeEvent({
  kind: 3,
  created_at: 1_700_000_050,
  tags: [["p", EXISTING_FOLLOW_PUBKEY, "wss://existing.example", "friend"], ["t", "nostr"]],
  content: JSON.stringify({ "wss://legacy.example": { read: true, write: true } }),
}, SIGNER_SECRET);

async function installSigningSession(page, { directRelayReads = true } = {}) {
  await page.addInitScript(
    ({ relayURL, secret, pubkey, npub, directRelayReads: direct }) => {
      localStorage.setItem("ptxt_direct_relays", direct ? "1" : "0");
      localStorage.setItem("ptxt_direct_relays_fallback", "0");
      localStorage.setItem("ptxt_wot_enabled", "0");
      localStorage.setItem("ptxt_feed_sort", "recent");
      localStorage.setItem("ptxt_nostr_session", JSON.stringify({
        method: "nip07",
        pubkey,
        npub,
      }));
      localStorage.setItem("ptxt_nip07_relay_sync_pubkey", pubkey);
      localStorage.setItem("ptxt_relay_config", JSON.stringify({
        useAppRelays: false,
        useUserRelays: true,
        userRelayMetadata: {
          updatedAt: Date.now(),
          relays: [{ url: relayURL, read: true, write: true }],
        },
      }));
      window.confirm = () => true;
      window.nostr = {
        getPublicKey: async () => pubkey,
        getRelays: async () => ({ [relayURL]: { read: true, write: true } }),
        signEvent: async (draft) => {
          const { finalizeEvent: sign } = await import("/static/lib/nostr-tools.js");
          return sign(draft, Uint8Array.from(secret));
        },
      };
    },
    {
      relayURL: RELAY_URL,
      secret: [...SIGNER_SECRET],
      pubkey: SIGNER_PUBKEY,
      npub: SIGNER_NPUB,
      directRelayReads,
    },
  );
}

function latestEvent(events, kind, content) {
  return [...events]
    .reverse()
    .find((event) => event.pubkey === SIGNER_PUBKEY
      && event.kind === kind
      && (content === undefined || event.content === content));
}

test("follow, unfollow, mute, and unmute preserve lists and publish", async ({ page, request }) => {
  /** @type {string[]} */
  const browserMessages = [];
  page.on("console", (message) => browserMessages.push(message.text()));
  const seeded = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
  expect(seeded.ok()).toBeTruthy();

  const relayEvents = await installRelayNativeE2E(page, {
    relayURL: RELAY_URL,
    events: [...buildThreadFixture(), EXISTING_FOLLOW],
    viewerPubkey: SIGNER_PUBKEY,
    wotEnabled: false,
  });
  await installSigningSession(page);

  await page.goto(`/u/${AUTHOR_PK}`);
  await expect(page.locator("#user-header .profile-display-name")).not.toBeEmpty({ timeout: 30_000 });
  await page.getByLabel("Profile options").click();

  const follow = page.locator(`[data-follow-toggle][data-pubkey="${AUTHOR_PK}"]`).first();
  await expect(follow).toBeVisible();
  await follow.click();
  await expect.poll(() => latestEvent(relayEvents, 3)?.tags.some((tag) => tag[0] === "p" && tag[1] === AUTHOR_PK)).toBe(true);
  expect(latestEvent(relayEvents, 3)?.tags).toContainEqual(["p", EXISTING_FOLLOW_PUBKEY, "wss://existing.example", "friend"]);
  expect(latestEvent(relayEvents, 3)?.tags).toContainEqual(["t", "nostr"]);
  expect(latestEvent(relayEvents, 3)?.content).toBe(EXISTING_FOLLOW.content);
  await expect(follow).toHaveText("Unfollow");

  await page.getByLabel("Profile options").click();
  await follow.click();
  await expect.poll(() => latestEvent(relayEvents, 3)?.tags.some((tag) => tag[0] === "p" && tag[1] === AUTHOR_PK)).toBe(false);
  const followWrites = relayEvents.filter((event) => event.pubkey === SIGNER_PUBKEY && event.kind === 3 && event.id !== EXISTING_FOLLOW.id);
  expect(followWrites).toHaveLength(2);
  expect(followWrites[1].created_at).toBeGreaterThan(followWrites[0].created_at);
  expect(followWrites[1].tags).toContainEqual(["p", EXISTING_FOLLOW_PUBKEY, "wss://existing.example", "friend"]);
  expect(followWrites[1].tags).toContainEqual(["t", "nostr"]);
  await expect(follow).toHaveText("Follow");

  const mute = page.locator(`[data-mute-toggle][data-pubkey="${AUTHOR_PK}"]`).first();
  await page.getByLabel("Profile options").click();
  await expect(mute).toBeVisible();
  await mute.click();
  await expect.poll(() => {
    if (latestEvent(relayEvents, 10000)?.tags.some((tag) => tag[0] === "p" && tag[1] === AUTHOR_PK)) return "published";
    return browserMessages.find((message) => message.includes("mute:")) || "pending";
  }).toBe("published");
  await expect(mute).toHaveText("Unmute");

  await page.getByLabel("Profile options").click();
  await mute.click();
  await expect.poll(() => latestEvent(relayEvents, 10000)?.tags.some((tag) => tag[0] === "p" && tag[1] === AUTHOR_PK)).toBe(false);
  const muteWrites = relayEvents.filter((event) => event.pubkey === SIGNER_PUBKEY && event.kind === 10000);
  expect(muteWrites).toHaveLength(2);
  expect(muteWrites[1].created_at).toBeGreaterThan(muteWrites[0].created_at);
  await expect(mute).toHaveText("Mute");

  for (const event of relayEvents.filter((row) => row.pubkey === SIGNER_PUBKEY && row.id !== EXISTING_FOLLOW.id)) {
    expect(event.tags).toContainEqual(["client", "Plain Text Nostr"]);
    expect(finalizeEvent({
      kind: event.kind,
      created_at: event.created_at,
      tags: event.tags,
      content: event.content,
    }, SIGNER_SECRET).id).toBe(event.id);
  }
});

test("reply and post sign and publish with the correct event shape", async ({ page, request }) => {
  const seeded = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${AUTHOR_PK}`);
  expect(seeded.ok()).toBeTruthy();

  const relayEvents = await installMockNostrRelay(page, {
    relayURL: RELAY_URL,
    events: buildThreadFixture(),
  });
  await installSigningSession(page, { directRelayReads: false });

  await page.goto(`/thread/${ROOT_ID}?wot=0`);
  const rootCard = page.locator(`#thread-focus #note-${ROOT_ID}`);
  await expect(rootCard).toBeVisible({ timeout: 30_000 });
  await rootCard.locator(`[data-reply-action][data-reply-target-id="${ROOT_ID}"]`).last().click();
  const replyForm = page.locator(".thread-inline-reply [data-composer-form]");
  await expect(replyForm).toBeVisible();
  await replyForm.locator("[data-composer-content]").fill("signed action reply");
  await page.locator(".thread-inline-reply [data-inline-composer-publish]").click();
  await expect.poll(() => Boolean(latestEvent(relayEvents, 1, "signed action reply"))).toBe(true);
  const reply = latestEvent(relayEvents, 1, "signed action reply");
  expect(reply?.tags).toContainEqual(["e", ROOT_ID, "", "root"]);
  expect(reply?.tags).toContainEqual(["e", ROOT_ID, "", "reply"]);
  expect(reply?.tags).toContainEqual(["p", AUTHOR_PK]);

  await page.goto("/feed");
  await page.locator("[data-post-trigger]").first().click();
  const postForm = page.locator("[data-composer-dialog] [data-composer-form]");
  await expect(postForm).toBeVisible();
  await postForm.locator("[data-composer-content]").fill("signed action post");
  await postForm.locator("[data-composer-submit]").click();
  await expect.poll(() => Boolean(latestEvent(relayEvents, 1, "signed action post"))).toBe(true);

  for (const event of relayEvents.filter((row) => row.pubkey === SIGNER_PUBKEY)) {
    expect(event.tags).toContainEqual(["client", "Plain Text Nostr"]);
    expect(finalizeEvent({
      kind: event.kind,
      created_at: event.created_at,
      tags: event.tags,
      content: event.content,
    }, SIGNER_SECRET).id).toBe(event.id);
  }
});
