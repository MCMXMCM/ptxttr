// @ts-check
import { test, expect } from "@playwright/test";

const NOTE_ID = "a".repeat(64);

function minimalHydrateHTML() {
  return `
<section class="feed-column">
  <section id="thread-summary" data-thread-fragment="summary">
    <section class="thread-header">
      <p class="thread-back thread-back-primary">&lt;-- feed</p>
    </section>
    <div class="thread-summary"><p>e2e thread</p></div>
  </section>
  <section id="thread-tree-view" data-thread-fragment="tree" hidden></section>
  <section id="thread-ancestors" data-thread-fragment="ancestors"></section>
  <section id="thread-focus" data-thread-fragment="focus">
    <article class="note" id="note-${NOTE_ID}">selected</article>
  </section>
  <section class="thread-replies">
    <div class="comments" id="thread-replies" data-thread-fragment="replies"></div>
  </section>
</section>
<aside class="right-rail" data-thread-fragment="participants">
  <section class="thread-people-panel"><h2>People in this thread</h2></section>
</aside>`;
}

function feedNoteHTML() {
  return `<article class="note" id="note-${NOTE_ID}" data-ascii-select-href="/thread/${NOTE_ID}">
<pre class="ascii-card"><span class="ascii-line">e2e prefetch note</span></pre>
</article>`;
}

async function installFragmentMocks(page) {
  await page.route("**/*", async (route) => {
    const url = route.request().url();
    if (!url.includes("127.0.0.1") && !url.includes("localhost")) {
      await route.continue();
      return;
    }
    const parsed = new URL(url);
    const fragment = parsed.searchParams.get("fragment");
    if (fragment === "1" && (parsed.pathname === "/" || parsed.pathname === "/feed")) {
      await route.fulfill({
        status: 200,
        contentType: "text/html; charset=utf-8",
        body: feedNoteHTML(),
        headers: {
          "X-Ptxt-Cursor": "0",
          "X-Ptxt-Has-More": "0",
        },
      });
      return;
    }
    if (fragment === "heading") {
      await route.fulfill({
        status: 200,
        contentType: "text/html; charset=utf-8",
        body: "<h1>E2E feed</h1>",
      });
      return;
    }
    if (fragment === "hydrate" && parsed.pathname === `/thread/${NOTE_ID}`) {
      await route.fulfill({
        status: 200,
        contentType: "text/html; charset=utf-8",
        body: minimalHydrateHTML(),
        headers: { "X-Ptxt-Has-More": "0" },
      });
      return;
    }
    await route.continue();
  });
}

async function ensureFeedNote(page) {
  const feed = page.locator("[data-feed]");
  await expect(feed).toBeVisible();
  const note = feed.locator(`#note-${NOTE_ID}`);
  if ((await note.count()) === 0) {
    await feed.evaluate(
      (el, html) => {
        el.innerHTML = html;
      },
      feedNoteHTML(),
    );
  }
  await expect(feed.locator(`#note-${NOTE_ID}`)).toBeVisible();
}

function trackHydrateRequests(page) {
  /** @type {string[]} */
  const hydrateHits = [];
  page.on("request", (req) => {
    if (req.url().includes("fragment=hydrate")) hydrateHits.push(req.url());
  });
  return hydrateHits;
}

test.describe("thread prefetch", () => {
  test.beforeEach(async ({ page }) => {
    await installFragmentMocks(page);
  });

  test("viewport idle prefetch requests thread hydrate", async ({ page }) => {
    const hydrateHits = trackHydrateRequests(page);
    await page.goto("/");
    await ensureFeedNote(page);
    await page.locator(`#note-${NOTE_ID}`).scrollIntoViewIfNeeded();
    await expect.poll(
      () => hydrateHits.some((u) => u.includes(`/thread/${NOTE_ID}`)),
      { timeout: 12_000 },
    ).toBe(true);
  });

  test("card body hover targets thread hydrate (card path, not only links)", async ({ page }) => {
    const hydrateHits = trackHydrateRequests(page);
    await page.goto("/");
    await ensureFeedNote(page);
    // Clear viewport-driven prefetches so hover is the observable trigger.
    await page.evaluate(() => {
      window.__ptxtClearPrefetchForE2E?.();
    });
    await page.locator(`#note-${NOTE_ID}`).hover({ position: { x: 8, y: 8 } });
    await expect
      .poll(() => hydrateHits.some((u) => u.includes(`/thread/${NOTE_ID}`)), { timeout: 10_000 })
      .toBe(true);
  });

  test("card click opens thread without a second hydrate fetch", async ({ page }) => {
    const hydrateHits = trackHydrateRequests(page);
    await page.goto("/");
    await ensureFeedNote(page);
    await page.locator(`#note-${NOTE_ID}`).hover({ position: { x: 8, y: 8 } });
    await expect
      .poll(() => hydrateHits.some((u) => u.includes(`/thread/${NOTE_ID}`)), { timeout: 10_000 })
      .toBe(true);
    await page.waitForFunction(() =>
      (window.__ptxtPrefetchKeys?.() ?? []).some((k) => k.endsWith("::hydrate")),
    );
    const afterPrefetch = hydrateHits.length;
    await page.locator(`#note-${NOTE_ID}`).click({ position: { x: 8, y: 8 } });
    await expect(page.locator("#thread-summary .thread-header")).toContainText("feed");
    await page.waitForTimeout(300);
    expect(hydrateHits.length).toBe(afterPrefetch);
  });
});
