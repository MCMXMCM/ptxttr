import { expect, test } from "@playwright/test";

const NOTE_ID = "7".repeat(64);

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    window.__ptxtCopiedShareURL = "";
    Object.defineProperty(navigator, "share", {
      configurable: true,
      value: undefined,
    });
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: {
        writeText(value) {
          window.__ptxtCopiedShareURL = String(value);
          return Promise.resolve();
        },
      },
    });
  });
  await page.route("**/api/shares", async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 1500));
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ url: `${new URL(route.request().url()).origin}/s/prepared-share` }),
    });
  });
  await page.goto("/about");
  await page.evaluate(async (noteID) => {
    const note = document.createElement("article");
    note.className = "note";
    note.id = `note-${noteID}`;
    note.dataset.asciiKind = "note";
    note.dataset.asciiAuthor = "Menu Author";
    note.dataset.asciiAge = "1m";
    note.dataset.asciiThreadHref = `/thread/${noteID}`;
    note.dataset.asciiUserHref = "/";
    note.dataset.asciiNevent = `nevent-${noteID}`;
    note.dataset.asciiNpub = "npub-menu-author";
    note.innerHTML = `
      <pre class="ascii-card"><a class="note-feed-avatar" href="/"></a></pre>
      <template class="ascii-source">note menu fixture</template>
      <template class="ascii-reference-source"></template>
    `;
    document.querySelector("main")?.append(note);
    const { refreshAsciiSync } = await import("/static/js/ascii.js");
    refreshAsciiSync(note);
  }, NOTE_ID);
});

test("share copies immediately while the prepared URL is still loading", async ({ page }) => {
  const note = page.locator(`#note-${NOTE_ID}`);
  await note.locator("[data-ascii-action-menu-trigger]").click();
  const share = note.locator('[data-note-menu-action="share"]');
  await share.click();

  await expect(share).toHaveText("[copied]");
  await expect.poll(() => page.evaluate(() => window.__ptxtCopiedShareURL)).toBe(
    `http://127.0.0.1:${process.env.PTXT_E2E_PORT || 18080}/thread/${NOTE_ID}`,
  );
  await expect(note.locator("details.ascii-action-menu")).toHaveAttribute("open", "");
});

test("native Share opens immediately while the prepared URL is still loading", async ({ page }) => {
  await page.evaluate(() => {
    window.__ptxtNativeSharePayload = null;
    Object.defineProperty(navigator, "share", {
      configurable: true,
      value(payload) {
        window.__ptxtNativeSharePayload = payload;
        return Promise.resolve();
      },
    });
  });
  const note = page.locator(`#note-${NOTE_ID}`);
  await note.locator("[data-ascii-action-menu-trigger]").click();
  await note.locator('[data-note-menu-action="share"]').click();

  await expect.poll(() => page.evaluate(() => window.__ptxtNativeSharePayload)).toEqual({
    title: "Menu Author on ptxt",
    url: `http://127.0.0.1:${process.env.PTXT_E2E_PORT || 18080}/thread/${NOTE_ID}`,
  });
  await expect(note.locator("details.ascii-action-menu")).not.toHaveAttribute("open", "");
});

test("desktop Share copies visibly when Chromium exposes a no-op native Share", async ({ page }) => {
  await page.evaluate(() => {
    document.getElementById("ptxt-app-bootstrap")?.remove();
    document.documentElement.dataset.ptxtDesktopMode = "1";
    window.__ptxtNativeShareCalls = 0;
    Object.defineProperty(navigator, "share", {
      configurable: true,
      value() {
        window.__ptxtNativeShareCalls += 1;
        return Promise.resolve();
      },
    });
  });
  const note = page.locator(`#note-${NOTE_ID}`);
  await note.locator("[data-ascii-action-menu-trigger]").click();
  const share = note.locator('[data-note-menu-action="share"]');
  await share.click();

  await expect(share).toHaveText("[copied]");
  await expect.poll(() => page.evaluate(() => window.__ptxtCopiedShareURL)).toBe(
    `http://127.0.0.1:${process.env.PTXT_E2E_PORT || 18080}/thread/${NOTE_ID}`,
  );
  expect(await page.evaluate(() => window.__ptxtNativeShareCalls)).toBe(0);
  await expect(note.locator("details.ascii-action-menu")).toHaveAttribute("open", "");
});

test("every enabled note menu option underlines on hover", async ({ page }) => {
  const note = page.locator(`#note-${NOTE_ID}`);
  await note.locator("[data-ascii-action-menu-trigger]").click();
  const items = note.locator(".ascii-action-menu-list a, .ascii-action-menu-list button:not(:disabled)");
  const count = await items.count();
  expect(count).toBeGreaterThan(0);
  for (let index = 0; index < count; index += 1) {
    const item = items.nth(index);
    await item.hover();
    await expect(item).toHaveCSS("text-decoration-line", "underline");
  }
});
