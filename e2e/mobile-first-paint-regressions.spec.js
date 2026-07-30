import { expect, test } from "@playwright/test";

const IPHONE_UA = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Version/18.0 Mobile/15E148 Safari/604.1";

async function holdInitialAppBundle(page) {
  let release;
  const gate = new Promise((resolve) => {
    release = resolve;
  });
  await page.route("**/static/build/*.js*", async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    if (!/\/(?:entry|guest)\.js$/.test(pathname)) {
      await route.continue();
      return;
    }
    await gate;
    await route.continue();
  });
  return () => release();
}

async function waitForFirstPaintStyles(page) {
  await page.waitForFunction(() =>
    [...document.styleSheets].some((sheet) => sheet.href?.includes("/static/")));
  await page.evaluate(() => document.fonts?.ready);
}

test("server media grid reserves thread geometry across hydration", async ({ browser, request }) => {
  const noteID = "4".repeat(64);
  const authorID = "3".repeat(64);
  const port = Number(process.env.PTXT_E2E_PORT || 18080);
  const mediaURLs = ["a", "b", "c"].map(
    (key) => `http://127.0.0.1:${port}/static/img/ascritch.png?ssr-media=${key}`,
  );
  const content = `server rendered media grid ${mediaURLs.join(" ")}`;
  const seeded = await request.post(
    `/debug/seed-note?id=${noteID}&pubkey=${authorID}&content=${encodeURIComponent(content)}`,
  );
  expect(seeded.ok()).toBeTruthy();

  const context = await browser.newContext({
    userAgent: IPHONE_UA,
    viewport: { width: 393, height: 852 },
  });
  const page = await context.newPage();
  const mediaRequestCounts = new Map(mediaURLs.map((url) => [url, 0]));
  page.on("request", (requested) => {
    const url = requested.url();
    if (mediaRequestCounts.has(url)) {
      mediaRequestCounts.set(url, mediaRequestCounts.get(url) + 1);
    }
  });
  const releaseBundle = await holdInitialAppBundle(page);

  try {
    await page.goto(`/thread/${noteID}`, { waitUntil: "commit" });
    const note = page.locator(`#note-${noteID}`).first();
    const grid = note.locator(".ascii-ssr-media-on .note-media-grid-wrap");
    await expect(grid).toBeVisible();
    await expect(grid.locator("img")).toHaveCount(3);
    await waitForFirstPaintStyles(page);
    const before = await note.evaluate((node) => {
      const next = node.nextElementSibling;
      return {
        height: Math.round(node.getBoundingClientRect().height),
        nextTop: next ? Math.round(next.getBoundingClientRect().top) : null,
      };
    });

    releaseBundle();
    await page.waitForLoadState("domcontentloaded");
    await expect(note.locator(".ascii-ssr-media-mode")).toHaveCount(0);
    await expect(note.locator(".note-media-grid-wrap")).toBeVisible();
    await note.locator(".note-media-grid-wrap img").evaluateAll((images) =>
      Promise.all(images.map((image) => image.complete
        ? Promise.resolve()
        : new Promise((resolve) => {
          image.addEventListener("load", resolve, { once: true });
          image.addEventListener("error", resolve, { once: true });
        }))),
    );
    const after = await note.evaluate((node) => {
      const next = node.nextElementSibling;
      return {
        height: Math.round(node.getBoundingClientRect().height),
        nextTop: next ? Math.round(next.getBoundingClientRect().top) : null,
      };
    });
    expect(Math.abs(after.height - before.height)).toBeLessThanOrEqual(2);
    if (before.nextTop != null && after.nextTop != null) {
      expect(Math.abs(after.nextTop - before.nextTop)).toBeLessThanOrEqual(2);
    }
    expect([...mediaRequestCounts.values()].every((count) => count <= 1)).toBe(true);
  } finally {
    releaseBundle();
    await context.close();
  }
});

test("capped seven-image SSR grid keeps desktop geometry and requests", async ({ browser, request }) => {
  const noteID = "9".repeat(64);
  const authorID = "a".repeat(64);
  const port = Number(process.env.PTXT_E2E_PORT || 18080);
  const mediaURLs = Array.from(
    { length: 7 },
    (_, index) => `http://127.0.0.1:${port}/static/img/ascritch.png?capped-media=${index}`,
  );
  const seeded = await request.post(
    `/debug/seed-note?id=${noteID}&pubkey=${authorID}&content=${encodeURIComponent(mediaURLs.join(" "))}`,
  );
  expect(seeded.ok()).toBeTruthy();
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  await context.addCookies([{
    name: "ptxt_ascii_w_desktop",
    value: "64",
    url: `http://127.0.0.1:${port}`,
  }]);
  const page = await context.newPage();
  const requestCounts = new Map(mediaURLs.map((url) => [url, 0]));
  page.on("request", (requested) => {
    if (requestCounts.has(requested.url())) {
      requestCounts.set(requested.url(), requestCounts.get(requested.url()) + 1);
    }
  });
  const releaseBundle = await holdInitialAppBundle(page);
  try {
    await page.goto(`/thread/${noteID}`, { waitUntil: "commit" });
    const note = page.locator(`#note-${noteID}`).first();
    const grid = note.locator(".ascii-ssr-media-on .note-media-grid-wrap");
    await expect(grid.locator("img")).toHaveCount(5);
    await expect(grid.locator(".note-media-more-tile")).toHaveText("+");
    await waitForFirstPaintStyles(page);
    const noteState = (node) => ({
      height: node.getBoundingClientRect().height,
      gridHeight: node.querySelector(".note-media-grid")?.getBoundingClientRect().height,
      lines: [...node.querySelectorAll(".ascii-line")]
        .filter((line) => getComputedStyle(line).display !== "none")
        .map((line) => line.innerText),
    });
    const before = await note.evaluate(noteState);
    releaseBundle();
    await page.waitForLoadState("domcontentloaded");
    await expect(note.locator(".ascii-ssr-media-mode")).toHaveCount(0);
    await expect(note.locator(".note-media-grid")).toHaveAttribute("data-media-grid-bound", "1");
    const after = await note.evaluate(noteState);
    expect(
      Math.abs(after.height - before.height),
      JSON.stringify({ before, after }, null, 2),
    ).toBeLessThanOrEqual(2);
    expect([...requestCounts.values()].every((count) => count <= 1)).toBe(true);
  } finally {
    releaseBundle();
    await context.close();
  }
});

test("ASCII enhancement reuses a failed single-image grid at its 4:3 fallback", async ({ page }) => {
  await page.goto("/about");
  const reused = await page.evaluate(async () => {
    const url = `${location.origin}/static/img/missing-ssr-media.jpg?ssr-reuse=1`;
    const signature = `image:${url}`;
    const note = document.createElement("article");
    note.className = "note";
    note.dataset.asciiKind = "note";
    note.dataset.asciiAuthor = "SSR Author";
    note.dataset.asciiAge = "1m";
    note.dataset.asciiThreadHref = "/thread/test";
    note.dataset.asciiUserHref = "/";
    note.innerHTML = `
      <pre class="ascii-card">
        <span class="ascii-line note-image-boxed-row note-media-grid-row">
          <span class="note-media-grid-edge note-media-grid-edge-left"></span>
          <span class="note-media-grid-wrap ascii-inline-media" data-media-grid-signature="${signature}">
            <span class="note-media-grid note-media-grid-1">
              <button type="button" class="note-media-tile note-media-image-tile" data-media-grid-open="0">
                <img src="${url}" alt="" loading="lazy" decoding="async">
              </button>
            </span>
          </span>
          <span class="note-media-grid-edge note-media-grid-edge-right"></span>
        </span>
      </pre>
      <template class="ascii-source">${url}</template>
      <div class="note-media-drawer" data-note-image-mount hidden></div>
    `;
    document.querySelector("main")?.append(note);
    const originalGrid = note.querySelector(".note-media-grid-wrap");
    const originalImage = note.querySelector(".note-media-grid-wrap img");
    const before = note.querySelector(".note-media-grid").getBoundingClientRect();
    const { refreshAsciiSync } = await import("/static/js/ascii.js");
    refreshAsciiSync(note);
    const hydratedGrid = note.querySelector(".note-media-grid");
    const after = hydratedGrid.getBoundingClientRect();
    return {
      grid: note.querySelector(".note-media-grid-wrap") === originalGrid,
      image: note.querySelector(".note-media-grid-wrap img") === originalImage,
      bound: hydratedGrid?.dataset.mediaGridBound,
      aspectRatio: getComputedStyle(hydratedGrid).aspectRatio,
      heightDelta: Math.abs(after.height - before.height),
    };
  });
  expect(reused).toEqual({
    grid: true,
    image: true,
    bound: "1",
    aspectRatio: "4 / 3",
    heightDelta: 0,
  });
});

test("media-off first paint keeps URLs and does not reserve a visible grid", async ({ browser, request }) => {
  const noteID = "8".repeat(64);
  const authorID = "7".repeat(64);
  const port = Number(process.env.PTXT_E2E_PORT || 18080);
  const imageURL = `http://127.0.0.1:${port}/static/img/ascritch.png?media-off=1`;
  const seeded = await request.post(
    `/debug/seed-note?id=${noteID}&pubkey=${authorID}&content=${encodeURIComponent(`media off ${imageURL}`)}`,
  );
  expect(seeded.ok()).toBeTruthy();
  const context = await browser.newContext({
    userAgent: IPHONE_UA,
    viewport: { width: 393, height: 852 },
  });
  await context.addInitScript(() => localStorage.setItem("ptxt_image_mode", "0"));
  const page = await context.newPage();
  const releaseBundle = await holdInitialAppBundle(page);
  try {
    await page.goto(`/thread/${noteID}`, { waitUntil: "commit" });
    const note = page.locator(`#note-${noteID}`).first();
    await expect(page.locator("html")).toHaveAttribute("data-ptxt-image-mode", "off");
    await expect(note.locator(".note-media-grid-wrap:visible")).toHaveCount(0);
    await expect(note).toContainText("media off");
    await expect(note).toContainText("media-off=1");
    await waitForFirstPaintStyles(page);
    const beforeState = await note.evaluate((node) => ({
      height: Math.round(node.getBoundingClientRect().height),
      lines: [...node.querySelectorAll(".ascii-line")]
        .filter((line) => getComputedStyle(line).display !== "none")
        .map((line) => line.innerText),
    }));
    releaseBundle();
    await page.waitForLoadState("domcontentloaded");
    await expect(note.locator(".ascii-ssr-media-mode")).toHaveCount(0);
    await expect(note.locator(".note-media-grid-wrap:visible")).toHaveCount(0);
    await expect(note).toContainText("media off");
    await expect(note).toContainText("media-off=1");
    const afterState = await note.evaluate((node) => ({
      height: Math.round(node.getBoundingClientRect().height),
      lines: [...node.querySelectorAll(".ascii-line")]
        .filter((line) => getComputedStyle(line).display !== "none")
        .map((line) => line.innerText),
    }));
    expect(
      Math.abs(afterState.height - beforeState.height),
      JSON.stringify({ beforeState, afterState }, null, 2),
    ).toBeLessThanOrEqual(2);
  } finally {
    releaseBundle();
    await context.close();
  }
});

test("long feed note first paint matches its hydrated truncated height", async ({ browser, request }) => {
  const noteID = "6".repeat(64);
  const authorID = "5".repeat(64);
  const content = "server rendered long note content ".repeat(24);
  const seeded = await request.post(
    `/debug/seed-note?id=${noteID}&pubkey=${authorID}&content=${encodeURIComponent(content)}`,
  );
  expect(seeded.ok()).toBeTruthy();
  const href = `/u/${authorID}`;
  const contextOptions = {
    userAgent: IPHONE_UA,
    viewport: { width: 393, height: 852 },
  };

  const noteState = async (page) => {
    const note = page.locator(`#note-${noteID}`);
    await expect(note).toBeVisible();
    await expect(note.locator("button:visible", { hasText: "view more" })).toHaveCount(1);
    return note.locator(":scope > .ascii-card").evaluate((card) => ({
      height: Math.round(card.getBoundingClientRect().height),
      visibleBodyRows: Array.from(card.querySelectorAll(".note-content > .ascii-line"))
        .filter((line) => line.getClientRects().length > 0).length,
    }));
  };

  const firstPaintContext = await browser.newContext({
    ...contextOptions,
    javaScriptEnabled: false,
  });
  const firstPaintPage = await firstPaintContext.newPage();
  await firstPaintPage.goto(href);
  const firstPaint = await noteState(firstPaintPage);
  await firstPaintContext.close();

  const hydratedContext = await browser.newContext(contextOptions);
  const hydratedPage = await hydratedContext.newPage();
  await hydratedPage.goto(href);
  const hydrated = await noteState(hydratedPage);
  await hydratedContext.close();

  expect(firstPaint.visibleBodyRows).toBe(hydrated.visibleBodyRows);
  expect(Math.abs(firstPaint.height - hydrated.height)).toBeLessThanOrEqual(3);
});

test("focused thread first paint is complete and stays inside an iPhone viewport", async ({ browser, request }) => {
  const seeded = await request.post("/debug/seed-thread-wot");
  expect(seeded.ok()).toBeTruthy();
  const fixture = await seeded.json();
  const context = await browser.newContext({
    javaScriptEnabled: false,
    userAgent: IPHONE_UA,
    viewport: { width: 393, height: 852 },
  });
  const page = await context.newPage();

  try {
    const response = await page.goto(
      `/thread/${fixture.root_id}?selected=${fixture.trusted_reply_id}#note-${fixture.trusted_reply_id}`,
    );
    expect(response?.ok()).toBeTruthy();

    const parent = page.locator(`#thread-focus .thread-focus-parent#note-${fixture.root_id}`);
    const selected = page.locator(`#thread-focus .thread-focus-selected#note-${fixture.trusted_reply_id}`);
    await expect(parent).toHaveCount(1);
    await expect(selected).toHaveCount(1);
    await expect(parent.locator(".ascii-reply > .ascii-line").filter({ hasText: "[...]" })).toHaveCount(1);
    await expect(selected.locator(".ascii-reply > .ascii-line").filter({ hasText: "[...]" })).toHaveCount(1);
    await expect(parent.locator(".ascii-reply")).toContainText("[reply] ---+");
    await expect(selected.locator(".ascii-reply")).toContainText("[reply] ---+");

    const geometry = await page.evaluate(() => ({
      clientWidth: document.documentElement.clientWidth,
      scrollWidth: document.documentElement.scrollWidth,
      headers: Array.from(document.querySelectorAll("#thread-focus .ascii-reply > .ascii-line:first-child"))
        .map((line) => Math.ceil(line.getBoundingClientRect().right)),
    }));
    expect(geometry.scrollWidth).toBe(geometry.clientWidth);
    expect(geometry.headers.every((right) => right <= geometry.clientWidth)).toBe(true);
  } finally {
    await context.close();
  }
});

test("focused thread parent matches its hydrated iPhone rendering", async ({ browser, request }) => {
  const seeded = await request.post("/debug/seed-thread-wot");
  expect(seeded.ok()).toBeTruthy();
  const fixture = await seeded.json();
  const href = `/thread/${fixture.root_id}?selected=${fixture.trusted_reply_id}#note-${fixture.trusted_reply_id}`;
  const contextOptions = {
    userAgent: IPHONE_UA,
    viewport: { width: 393, height: 852 },
  };

  const parentState = async (page) => page.locator(
    `#thread-focus .thread-focus-parent#note-${fixture.root_id}`,
  ).evaluate((parent) => {
    const header = parent.querySelector(":scope > .ascii-reply > .ascii-line:first-child");
    const pre = parent.querySelector(":scope > .ascii-reply");
    const headerRect = header?.getBoundingClientRect();
    const parentRect = parent.getBoundingClientRect();
    const parentStyle = getComputedStyle(parent);
    const headerStyle = header ? getComputedStyle(header) : null;
    const firstLine = (pre?.innerText || "").split("\n")[0] || "";
    return {
      text: pre?.innerText || "",
      columns: Array.from(firstLine).length + 7,
      headerLeft: Math.round(headerRect?.left || 0),
      headerRight: Math.round(headerRect?.right || 0),
      parentLeft: Math.round(parentRect.left),
      parentPaddingLeft: parentStyle.paddingLeft,
      parentMarginLeft: parentStyle.marginLeft,
      avatarSize: parentStyle.getPropertyValue("--avatar-size").trim(),
      headerPaddingLeft: headerStyle?.paddingLeft || "",
    };
  });

  const hydratedContext = await browser.newContext(contextOptions);
  const hydratedPage = await hydratedContext.newPage();
  await hydratedPage.goto(href);
  await expect(hydratedPage.locator(
    `#thread-focus .thread-focus-selected#note-${fixture.trusted_reply_id}`,
  )).toBeVisible();
  await hydratedPage.waitForTimeout(250);
  const hydrated = await parentState(hydratedPage);
  const measuredCookies = await hydratedContext.cookies();
  await hydratedContext.close();
  expect(measuredCookies.some((cookie) => cookie.name === "ptxt_ascii_w")).toBe(true);
  const firstPaintCookies = measuredCookies.map((cookie) => (
    cookie.name === "ptxt_ascii_w"
      ? { ...cookie, value: String(hydrated.columns) }
      : cookie
  ));

  const firstPaintContext = await browser.newContext({
    ...contextOptions,
    javaScriptEnabled: false,
    storageState: { cookies: firstPaintCookies, origins: [] },
  });
  const firstPaintPage = await firstPaintContext.newPage();
  await firstPaintPage.goto(href);
  const firstPaint = await parentState(firstPaintPage);
  await firstPaintContext.close();

  expect(firstPaint).toEqual(hydrated);
});

test("playing media does not collapse an expanded repost after note replacement", async ({ page }) => {
  await page.setViewportSize({ width: 393, height: 852 });
  await page.goto("/about");
  const noteID = "9".repeat(64);
  await page.evaluate(async (id) => {
    const note = document.createElement("article");
    note.id = `note-${id}`;
    note.dataset.asciiKind = "note";
    note.dataset.asciiAuthor = "Reposter";
    note.dataset.asciiAge = "1m";
    note.dataset.asciiRefMode = "repost";
    note.dataset.asciiRefAuthor = "Referenced Author";
    note.dataset.asciiRefAge = "2m";
    note.dataset.asciiThreadHref = `/thread/${id}`;
    note.dataset.asciiUserHref = "/";
    const referenceSource = `${"reference segment ".repeat(80)}EXPANDED-END https://media.example.test/clip.mp4`;
    note.innerHTML = `
      <pre class="ascii-card"></pre>
      <template class="ascii-source"></template>
      <template class="ascii-reference-source">${referenceSource}</template>
      <div class="note-media-drawer" data-note-image-mount hidden></div>
    `;
    document.querySelector("main")?.append(note);
    const { refreshAsciiSync } = await import("/static/js/ascii.js");
    refreshAsciiSync(note);
  }, noteID);

  const note = page.locator(`#note-${noteID}`);
  const viewMore = note.getByRole("button", { name: "view more" });
  await expect(viewMore).toHaveCount(1);
  await viewMore.click();
  await expect(note).toContainText("EXPANDED-END");
  await expect(viewMore).toHaveCount(0);

  const video = note.locator("video");
  await expect(video).toHaveCount(1);
  await video.click({ force: true });

  await page.evaluate(async (id) => {
    const current = document.getElementById(`note-${id}`);
    const replacement = current?.cloneNode(true);
    if (!(replacement instanceof HTMLElement) || !current) return;
    current.replaceWith(replacement);
    const { refreshAsciiSync } = await import("/static/js/ascii.js");
    refreshAsciiSync(replacement);
  }, noteID);

  const replacement = page.locator(`#note-${noteID}`);
  await expect(replacement).toContainText("EXPANDED-END");
  await expect(replacement.getByRole("button", { name: "view more" })).toHaveCount(0);
});

test("tapping a reposted note opens the referenced thread instead of the reposter thread", async ({ page }) => {
  await page.setViewportSize({ width: 393, height: 852 });
  await page.goto("/about");
  const outerID = "7".repeat(64);
  const referencedID = "8".repeat(64);
  await page.evaluate(async ({ outerID: outer, referencedID: referenced }) => {
    const note = document.createElement("article");
    note.id = `note-${outer}`;
    note.dataset.asciiKind = "note";
    note.dataset.asciiAuthor = "Reposter";
    note.dataset.asciiAge = "1m";
    note.dataset.asciiRefMode = "repost";
    note.dataset.asciiRefAuthor = "Original Author";
    note.dataset.asciiRefAge = "2m";
    note.dataset.asciiRefThreadHref = `/thread/${referenced}`;
    note.dataset.asciiThreadHref = `/thread/${outer}`;
    note.dataset.asciiSelectHref = `/thread/${outer}`;
    note.dataset.asciiUserHref = "/";
    note.innerHTML = `
      <pre class="ascii-card"></pre>
      <template class="ascii-source"></template>
      <template class="ascii-reference-source">referenced note body</template>
      <div class="note-media-drawer" data-note-image-mount hidden></div>
    `;
    document.querySelector("main")?.append(note);
    const { refreshAsciiSync } = await import("/static/js/ascii.js");
    refreshAsciiSync(note);
  }, { outerID, referencedID });

  const referenceCard = page.locator(`#note-${outerID} .ascii-reference-card`);
  await expect(referenceCard).toHaveAttribute("data-ascii-ref-select-href", `/thread/${referencedID}`);
  const referenceHeader = referenceCard.locator(":scope > .ascii-line").filter({ hasText: "Original Author" });
  await expect(referenceHeader).toHaveCount(1);
  const referenceBody = referenceCard.locator(".ascii-reference-line-link").filter({ hasText: "referenced note body" });
  await expect(referenceBody).toHaveCount(1);
  const referenceColors = await referenceCard.evaluate((card) => {
    const header = card.querySelector(":scope > .ascii-line");
    const body = card.querySelector(".ascii-reference-line-link");
    return {
      card: getComputedStyle(card).color,
      header: header ? getComputedStyle(header).color : "",
      body: body ? getComputedStyle(body).color : "",
    };
  });
  expect(referenceColors.header).toBe(referenceColors.card);
  expect(referenceColors.body).toBe(referenceColors.card);
  await referenceHeader.click();
  await expect(page).toHaveURL(new RegExp(`/thread/${referencedID}`));
  expect(new URL(page.url()).pathname).not.toBe(`/thread/${outerID}`);
});
