import { expect, test } from "@playwright/test";

test("desktop media cards put view more between truncated text and media", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto("/about");

  const ids = await page.evaluate(async () => {
    const definitions = [
      {
        id: "1".repeat(64),
        source: `${"media note segment ".repeat(80)}MEDIA-END https://media.example.test/large.jpg`,
      },
      {
        id: "2".repeat(64),
        source: `${"quote note segment ".repeat(80)}QUOTE-END`,
        refMode: "quote",
        referenceSource: "quoted content",
      },
      {
        id: "3".repeat(64),
        source: `${"plain note segment ".repeat(80)}PLAIN-END`,
      },
    ];

    for (const definition of definitions) {
      const note = document.createElement("article");
      note.className = "note";
      note.id = `note-${definition.id}`;
      note.dataset.asciiKind = "note";
      note.dataset.asciiAuthor = "Desktop Author";
      note.dataset.asciiAge = "1m";
      note.dataset.asciiThreadHref = `/thread/${definition.id}`;
      note.dataset.asciiUserHref = "/";
      if (definition.refMode) note.dataset.asciiRefMode = definition.refMode;
      note.innerHTML = `
        <pre class="ascii-card"><a class="note-feed-avatar" href="/"></a></pre>
        <template class="ascii-source">${definition.source}</template>
        <template class="ascii-reference-source">${definition.referenceSource || ""}</template>
        <div class="note-media-drawer" data-note-image-mount hidden></div>
      `;
      document.querySelector("main")?.append(note);
    }

    const { refreshAsciiSync } = await import("/static/js/ascii.js");
    definitions.forEach(({ id }) => refreshAsciiSync(document.getElementById(`note-${id}`)));
    return definitions.map(({ id }) => id);
  });

  const [media, quote, plain] = ids.map((id) => page.locator(`#note-${id}`));

  const mediaHeader = media.locator(":scope > .ascii-card > .ascii-line-feed-header");
  const mediaBody = media.locator(":scope > .ascii-card > .note-content");
  const mediaViewMore = mediaBody.getByRole("button", { name: "view more", exact: true });
  const mediaGridRow = mediaBody.locator(":scope > .note-media-grid-row");
  await expect(mediaHeader.getByRole("button", { name: "view more", exact: true })).toHaveCount(0);
  await expect(mediaViewMore).toHaveCount(1);
  await expect(mediaGridRow).toHaveCount(1);
  expect(await mediaViewMore.evaluate((button) =>
    Boolean(button.closest(".ascii-line-note-view-more")?.nextElementSibling?.matches(".note-media-grid-row")),
  )).toBe(true);

  const quoteHeader = quote.locator(":scope > .ascii-card > .ascii-line-feed-header");
  const quoteFooter = quote.locator(":scope > .ascii-card > .ascii-line").last();
  await expect(quoteHeader.getByRole("button", { name: "view more", exact: true })).toHaveCount(1);
  await expect(quoteFooter.getByRole("button", { name: "view more", exact: true })).toHaveCount(0);

  const plainHeader = plain.locator(":scope > .ascii-card > .ascii-line-feed-header");
  const plainFooter = plain.locator(":scope > .ascii-card > .ascii-line").last();
  await expect(plainHeader.getByRole("button", { name: "view more", exact: true })).toHaveCount(0);
  await expect(plainFooter.getByRole("button", { name: "view more", exact: true })).toHaveCount(1);

  await mediaViewMore.click();
  await expect(media).toContainText("MEDIA-END");
  const overflow = mediaHeader.locator("[data-ascii-action-menu-trigger]");
  await expect(overflow).toHaveText("[...]");
  const [headerBox, overflowBox, overflowLineHeight] = await Promise.all([
    mediaHeader.boundingBox(),
    overflow.boundingBox(),
    overflow.evaluate((element) => Number.parseFloat(getComputedStyle(element).lineHeight)),
  ]);
  expect(headerBox).not.toBeNull();
  expect(overflowBox).not.toBeNull();
  // `<summary>` must stay in the ASCII header's line box. Its native
  // list-item display otherwise places `[...]` on the following line.
  expect(Math.abs((overflowBox?.y || 0) - (headerBox?.y || 0))).toBeLessThan(2);
  // The entire line-height, including the brackets, should activate the menu.
  expect(overflowBox?.height || 0).toBeGreaterThanOrEqual(overflowLineHeight - 1);
});
