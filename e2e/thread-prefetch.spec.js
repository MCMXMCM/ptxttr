// @ts-check
import { test, expect } from "@playwright/test";

import {
  FEED_NOTE_ID,
  REPLY_ID,
  ROOT_ID,
  buildCombinedFixture,
} from "./helpers/nostr-fixtures.js";
import { installRelayNativeE2E } from "./helpers/mock-nostr-relay.js";

async function installNoteTransitionProbe(page) {
  return page.evaluate(() => {
    if (typeof document.startViewTransition !== "function") return false;
    const startViewTransition = document.startViewTransition.bind(document);
    window.__ptxtNoteTransitionSnapshots = [];
    document.startViewTransition = (update) => {
      const names = () => [...document.querySelectorAll("[data-ptxt-view-transition-name]")]
        .map((node) => node.getAttribute("data-ptxt-view-transition-name"))
        .filter(Boolean);
      const before = names();
      const snapshot = { before, after: [], root: null };
      window.__ptxtNoteTransitionSnapshots.push(snapshot);
      const transition = startViewTransition(() => {
        const result = update();
        snapshot.after = names();
        return result;
      });
      void transition.ready.then(() => {
        snapshot.root = {
          oldOpacity: getComputedStyle(document.documentElement, "::view-transition-old(root)").opacity,
          newOpacity: getComputedStyle(document.documentElement, "::view-transition-new(root)").opacity,
        };
      });
      return transition;
    };
    return true;
  });
}

async function useServerPrimaryThreadNavigation(page) {
  await page.evaluate(() => {
    localStorage.setItem("ptxt_direct_relays", "0");
    localStorage.removeItem("ptxt_relay_native_routes");
    if (window.__ptxtAppBootstrap?.features) {
      window.__ptxtAppBootstrap.features.directRelayReads = false;
      window.__ptxtAppBootstrap.features.relayNativeRoutesPrimary = false;
    }
  });
}

async function installServerPrimaryFeedCard(page, event, href) {
  await page.addInitScript(({ viewerPubkey }) => {
    window.__ptxtE2EPageLoads = [];
    document.addEventListener("page:load", (event) => {
      window.__ptxtE2EPageLoads.push(event?.detail?.route || "");
    });
    localStorage.setItem("ptxt_direct_relays", "0");
    localStorage.removeItem("ptxt_relay_native_routes");
    localStorage.setItem("ptxt_nostr_session", JSON.stringify({
      method: "readonly",
      pubkey: viewerPubkey,
      npub: "npub1serverprimarye2e",
      canSign: false,
    }));
  }, { viewerPubkey: event.pubkey });
  await page.goto("/feed");
  await page.locator("#feed").waitFor();
  await page.waitForFunction(() => window.__ptxtE2EPageLoads?.includes("feed"));
  await page.evaluate(({ noteEvent, noteHref }) => {
    const feed = document.querySelector("#feed");
    if (!(feed instanceof HTMLElement)) throw new Error("feed host missing");
    const note = document.createElement("article");
    note.className = "note";
    note.id = `note-${noteEvent.id}`;
    note.dataset.asciiSelectHref = noteHref;
    note.dataset.asciiEvent = JSON.stringify(noteEvent);
    const avatar = document.createElement("span");
    avatar.className = "note-avatar";
    const body = document.createElement("pre");
    body.className = "ascii-card";
    body.textContent = noteEvent.content;
    note.append(avatar, body);
    feed.replaceChildren(note);
  }, { noteEvent: event, noteHref: href });
  await useServerPrimaryThreadNavigation(page);
  return page.locator(`#feed #note-${event.id}`);
}

test.describe("instant thread intent and carried-note paint", () => {
  test.use({ viewport: { width: 1280, height: 900 } });

  test("an immediate feed click keeps the selected note visible while context is cold", async ({ page }) => {
    await installRelayNativeE2E(page, {
      events: buildCombinedFixture(),
      wotEnabled: false,
      responseDelayMs: 1_500,
    });
    await page.route((url) => (
      url.pathname.startsWith("/thread/") && url.searchParams.get("fragment") === "hydrate"
    ), async (route) => {
      await new Promise((resolve) => setTimeout(resolve, 1_500));
      await route.fulfill({ status: 503, contentType: "text/plain", body: "delayed cold hydrate" });
    });

    await page.goto("/feed");
    const source = page.locator(`#feed #note-${FEED_NOTE_ID}`);
    await expect(source).toBeVisible({ timeout: 30_000 });
    expect(await installNoteTransitionProbe(page)).toBe(true);

    await source.click();
    await expect(page).toHaveURL(new RegExp(`/thread/${FEED_NOTE_ID}$`), { timeout: 1_000 });
    await expect(page.locator(`#thread-focus #note-${FEED_NOTE_ID}`)).toBeVisible({ timeout: 500 });
    await expect(page.locator("[data-retro-loader-type='thread']")).toHaveCount(0);
    await expect.poll(async () => page.evaluate(() => (
      window.__ptxtNoteTransitionSnapshots?.[0]?.root || null
    ))).toEqual({ oldOpacity: "0", newOpacity: "1" });
    const transition = await page.evaluate(() => window.__ptxtNoteTransitionSnapshots?.[0] || null);
    for (const kind of ["avatar", "author", "chrome", "content", "actions"]) {
      expect(transition?.before).toContain(`ptxt-note-${kind}-${FEED_NOTE_ID}`);
      expect(transition?.after).toContain(`ptxt-note-${kind}-${FEED_NOTE_ID}`);
    }
  });

  test("a renderable partial thread is painted once instead of entering the full-route retry loop", async ({ page }) => {
    const event = buildCombinedFixture().find((candidate) => candidate.id === REPLY_ID);
    expect(event).toBeTruthy();
    let hydrateRequests = 0;
    let lastHydrateURL = "";
    await page.route((url) => (
      url.pathname.startsWith("/thread/") && url.searchParams.get("fragment") === "hydrate"
    ), async (route) => {
      hydrateRequests += 1;
      lastHydrateURL = route.request().url();
      await route.fulfill({
        status: 200,
        contentType: "text/html",
        headers: { "X-Ptxt-Thread-Incomplete": "1" },
        body: `<section class="feed-column" data-thread-expects-focus="1" data-thread-root-id="${ROOT_ID}" data-thread-selected-id="${REPLY_ID}">
          <section id="thread-summary" data-thread-fragment="summary"></section>
          <section id="thread-tree-view" data-thread-fragment="tree"></section>
          <section id="thread-ancestors" data-thread-fragment="ancestors"></section>
          <section id="thread-focus" data-thread-fragment="focus">
            <div class="comment thread-focus-parent thread-focus-parent--skeleton" aria-hidden="true">
              <span class="comment-avatar thread-parent-skeleton-avatar"></span>
              <pre class="ascii-reply text-skeleton-note">   ░░░░░░░░ -- ░░░░░ ---[...]</pre>
            </div>
            <article id="note-${REPLY_ID}" class="note is-focused thread-focus-selected" data-ascii-kind="selected" data-test-server-partial="1">
              <span class="note-avatar"></span><pre class="ascii-reply">server selected reply</pre>
            </article>
          </section>
          <section class="thread-replies"><div class="comments" id="thread-replies" data-thread-fragment="replies"></div></section>
        </section>`,
      });
    });

    const source = await installServerPrimaryFeedCard(
      page,
      event,
      `/thread/${ROOT_ID}?selected=${REPLY_ID}#note-${REPLY_ID}`,
    );
    await expect(source).toBeVisible();
    await source.evaluate((note) => {
      note.dataset.asciiReplyCount = "12";
    });
    await source.click();

    await expect.poll(() => hydrateRequests).toBeGreaterThan(0);
    expect(lastHydrateURL).toContain(REPLY_ID);
    await expect(page.locator(`#thread-focus #note-${REPLY_ID}[data-test-server-partial="1"]`)).toBeVisible({ timeout: 1_000 });
    await expect(page.locator("#thread-focus .thread-focus-parent--skeleton")).toBeVisible({ timeout: 1_000 });
    await expect(page.locator(".feed-column[data-thread-route-pending]")).toHaveCount(0);
    await expect(page.locator("[data-retro-loader-type='thread']")).toHaveCount(0);
    expect(hydrateRequests).toBe(1);
  });

  test("a failed foreground hydrate settles the carried note without remounting the route loader", async ({ page }) => {
    const event = buildCombinedFixture().find((candidate) => candidate.id === REPLY_ID);
    expect(event).toBeTruthy();
    await page.route((url) => (
      url.pathname.startsWith("/thread/") && url.searchParams.get("fragment") === "hydrate"
    ), async (route) => {
      await route.fulfill({ status: 503, contentType: "text/plain", body: "still materializing" });
    });

    const source = await installServerPrimaryFeedCard(
      page,
      event,
      `/thread/${ROOT_ID}?selected=${REPLY_ID}#note-${REPLY_ID}`,
    );
    await expect(source).toBeVisible();
    await source.click();

    const selected = page.locator(`#thread-focus #note-${REPLY_ID}`);
    await expect(selected).toBeVisible({ timeout: 1_000 });
    await expect(page.locator("#thread-focus .thread-focus-parent--skeleton")).toBeVisible();
    await expect(page.locator(".feed-column[data-thread-route-partial='1']")).toBeVisible({ timeout: 1_000 });
    await expect(page.locator(".feed-column[data-thread-route-pending]")).toHaveCount(0);
    await page.waitForTimeout(3_000);
    await expect(selected).toBeVisible();
    await expect(page.locator("[data-retro-loader-type='thread']")).toHaveCount(0);
  });
});
