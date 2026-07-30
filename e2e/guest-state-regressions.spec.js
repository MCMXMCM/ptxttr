// @ts-check
import { test, expect } from "@playwright/test";

const ROOT_ID = "a".repeat(64);
const REPLY_ID = "b".repeat(64);
const OUTSIDE_THREAD_ID = "8".repeat(64);
const OUTSIDE_AUTHOR_ID = "7".repeat(64);
const OUTSIDE_PROFILE_NOTE_ID = "9".repeat(64);

test.describe("guest state regressions", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/about");
    await page.evaluate(() => localStorage.clear());
  });

  test("chronological load more keeps the server-aligned guest scope", async ({ page }) => {
    let requestedDepth = "";
	await page.addInitScript(() => {
	  window.IntersectionObserver = class {
		observe() {}
		unobserve() {}
		disconnect() {}
		takeRecords() { return []; }
	  };
	});
    await page.route("**/feed?**", async (route) => {
      const url = new URL(route.request().url());
      if (url.searchParams.get("fragment") !== "1" || url.searchParams.get("cursor") !== "100") {
        await route.continue();
        return;
      }
      requestedDepth = route.request().headers()["x-ptxt-wot-depth"] || "";
      await route.fulfill({
        status: 200,
        contentType: "text/html; charset=utf-8",
        headers: {
          "X-Ptxt-Has-More": "0",
          "X-Ptxt-Cursor": "90",
          "X-Ptxt-Cursor-Id": REPLY_ID,
        },
        body: `<article class="note" id="note-${REPLY_ID}" data-created-at="90"></article>`,
      });
    });

    await page.goto("/feed");
    await page.locator("[data-feed-heading]").waitFor({ state: "visible" });
    await page.evaluate(({ rootID }) => {
      const feed = document.querySelector("#feed");
      const button = document.querySelector('[data-load-more][data-feed-url="/feed"]');
      if (!(feed instanceof HTMLElement) || !(button instanceof HTMLButtonElement)) return;
      feed.replaceChildren();
      const note = document.createElement("article");
      note.className = "note";
      note.id = `note-${rootID}`;
      note.dataset.createdAt = "101";
      feed.append(note);
      button.dataset.cursor = "100";
      button.dataset.cursorId = rootID;
      button.dataset.hasMore = "1";
      button.hidden = false;
      button.disabled = false;
    }, { rootID: ROOT_ID });

    const button = page.locator('[data-load-more][data-feed-url="/feed"]');
    await expect(button).toBeVisible();
    await button.click();

    await expect(page.locator(`#note-${REPLY_ID}`)).toHaveCount(1);
    expect(requestedDepth).toBe("1");
  });

  test("guest-v2 thread clicks use one authoritative SSR document", async ({ page, request }) => {
    const seeded = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${"6".repeat(64)}`);
    expect(seeded.ok()).toBeTruthy();
    let documentRequests = 0;
    let hydrateRequests = 0;
    page.on("request", (request) => {
      const url = new URL(request.url());
      if (url.pathname !== `/thread/${ROOT_ID}`) return;
      if (request.resourceType() === "document") documentRequests += 1;
      if (url.searchParams.get("fragment") === "hydrate") hydrateRequests += 1;
    });

    await page.goto("/feed");
    test.skip(await page.locator("body").getAttribute("data-guest-v2") !== "1", "requires PTXT_GUEST_SLICE_V2=1");
    await page.locator("[data-feed-heading]").waitFor({ state: "visible" });
    await page.evaluate(({ rootID }) => {
      const feed = document.querySelector("#feed");
      if (!(feed instanceof HTMLElement)) return;
      feed.replaceChildren();
      const note = document.createElement("article");
      note.className = "note";
      note.id = `note-${rootID}`;
      note.dataset.asciiReplyCount = "64";
      note.dataset.asciiSelectHref = `/thread/${rootID}`;
      note.textContent = "trending note with replies";
      feed.append(note);
    }, { rootID: ROOT_ID });

    await page.locator(`#note-${ROOT_ID}`).click();

    await expect(page).toHaveURL(new RegExp(`/thread/${ROOT_ID}$`));
    await expect(page.locator(`#note-${ROOT_ID}`)).toContainText("e2e seeded note");
    expect(documentRequests).toBe(1);
    expect(hydrateRequests).toBe(0);
    await expect(page.locator("[data-thread-route-pending]")).toHaveCount(0);
  });

  test("guest thread load more uses the bounded server reply fragment", async ({ page, request }) => {
    const seeded = await request.post(`/debug/seed-note?id=${ROOT_ID}&pubkey=${"6".repeat(64)}`);
    expect(seeded.ok()).toBeTruthy();
    let paginationRequests = 0;
    let requestedDepth = "";
    await page.route(`**/thread/${ROOT_ID}?**`, async (route) => {
      const url = new URL(route.request().url());
      if (url.searchParams.get("fragment") !== "replies") {
        await route.continue();
        return;
      }
      paginationRequests += 1;
      requestedDepth = route.request().headers()["x-ptxt-wot-depth"] || "";
      expect(url.searchParams.get("cursor")).toBe("100");
      expect(url.searchParams.get("cursor_id")).toBe(ROOT_ID);
      await route.fulfill({
        status: 200,
        contentType: "text/html; charset=utf-8",
        headers: {
          "X-Ptxt-Has-More": "0",
          "X-Ptxt-Cursor": "90",
          "X-Ptxt-Cursor-Id": REPLY_ID,
        },
        body: `<article class="comment" id="note-${REPLY_ID}">paged guest reply</article>`,
      });
    });

    await page.goto(`/thread/${ROOT_ID}`);
    await page.evaluate(({ rootID }) => {
      const section = document.querySelector(".thread-replies");
      if (!(section instanceof HTMLElement)) return;
      const button = document.createElement("button");
      button.type = "button";
      button.className = "load-more";
      button.dataset.threadLoadMore = "";
      button.dataset.loadLabel = "Load more thread replies";
      button.dataset.cursor = "100";
      button.dataset.cursorId = rootID;
      button.dataset.rootId = rootID;
      button.dataset.selectedId = rootID;
      button.textContent = "Load more thread replies";
      section.append(button);
    }, { rootID: ROOT_ID });

    await page.locator("[data-thread-load-more]").click();

    await expect(page.locator(`#note-${REPLY_ID}`)).toContainText("paged guest reply");
    await expect(page.getByText("Thread replies require client hydration", { exact: true })).toHaveCount(0);
    expect(paginationRequests).toBe(1);
    expect(requestedDepth).toBe("1");
  });

  test("guest profile clicks use one authoritative cached response", async ({ page, request }) => {
    const seeded = await request.post(
      `/debug/seed-note?id=${OUTSIDE_PROFILE_NOTE_ID}&pubkey=${OUTSIDE_AUTHOR_ID}&anonymous_scope=outside`,
    );
    expect(seeded.ok()).toBeTruthy();
    const knownAvatar = "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw==";
    let documentRequests = 0;
    let profileFetchRequests = 0;
    page.on("request", (request) => {
      const url = new URL(request.url());
      if (url.pathname !== `/u/${OUTSIDE_AUTHOR_ID}` || url.searchParams.has("fragment")) return;
      if (request.resourceType() === "document") documentRequests += 1;
      if (request.resourceType() === "fetch") profileFetchRequests += 1;
    });
    await page.route((url) => (
      url.pathname === `/u/${OUTSIDE_AUTHOR_ID}` && !url.searchParams.has("fragment")
    ), async (route) => {
      const response = await route.fetch();
      await new Promise((resolve) => setTimeout(resolve, 1_500));
      await route.fulfill({ response });
    });

    await page.goto("/feed");
    await page.locator("[data-feed-heading]").waitFor({ state: "visible" });
    const guestV2 = await page.locator("body").getAttribute("data-guest-v2") === "1";
    await page.evaluate(({ pubkey, avatar }) => {
      const card = document.createElement("article");
      card.className = "note";
      card.dataset.asciiAuthor = "Known Feed Author";
      card.dataset.asciiAvatar = avatar;
      const link = document.createElement("a");
      link.href = `/u/${pubkey}`;
      link.textContent = "Known Feed Author";
      card.append(link);
      document.querySelector("main")?.append(card);
    }, { pubkey: OUTSIDE_AUTHOR_ID, avatar: knownAvatar });

    await page.getByText("Known Feed Author").click();

    await expect(page).toHaveURL(new RegExp(`/u/${OUTSIDE_AUTHOR_ID}$`));
    await expect(page).toHaveTitle("User | Plain Text Nostr");
    await expect(page.locator("#user-header .profile-display-name")).not.toHaveClass(/text-skeleton/);
    if (!guestV2) {
      await expect(page.locator("#user-header .profile-display-name")).toHaveText("Known Feed Author", { timeout: 800 });
      await expect(page.locator("#user-header img.profile-avatar")).toHaveAttribute(
        "data-ptxt-avatar-original-src",
        knownAvatar,
        { timeout: 800 },
      );
    }
    await expect(page.locator(`#note-${OUTSIDE_PROFILE_NOTE_ID}`)).toContainText("e2e seeded note");
    await expect(page.locator('[data-retro-loader-type="profile-posts"]')).toHaveCount(0);
    if (guestV2) {
      expect(documentRequests).toBe(1);
      expect(profileFetchRequests).toBe(0);
    } else {
      expect(documentRequests).toBe(0);
      expect(profileFetchRequests).toBe(1);
    }
  });

  test("direct cached thread renders even when its root author is outside guest WoT", async ({ page, request }) => {
    const seeded = await request.post(
      `/debug/seed-note?id=${OUTSIDE_THREAD_ID}&pubkey=${OUTSIDE_AUTHOR_ID}&anonymous_scope=outside`,
    );
    expect(seeded.ok()).toBeTruthy();

    await page.goto(`/thread/${OUTSIDE_THREAD_ID}`);

    await expect(page.locator(`#note-${OUTSIDE_THREAD_ID}`)).toContainText("e2e seeded note");
    await expect(page.getByText("Server thread render stopped before HTML was ready.", { exact: true })).toHaveCount(0);
  });

  test("new guest notes are fully staged before the button reveals them", async ({ page }) => {
    await page.goto("/feed");
    test.skip(await page.locator("body").getAttribute("data-guest-v2") !== "1", "requires PTXT_GUEST_SLICE_V2=1");
    await page.locator("[data-feed-heading]").waitFor({ state: "visible" });

    const generation = Number(await page.locator("body").getAttribute("data-guest-generation") || 0) + 1;
    const oldID = "1".repeat(64);
    const newID = "2".repeat(64);
    const avatar = "data:image/gif;base64,R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw==";
    let stagedDocumentRequests = 0;

    await page.route("**/api/guest-feed-status**", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ generation, new_count: 1, top_created_at: 200, top_id: newID }),
      });
    });
    await page.route((url) => url.pathname === "/" && url.searchParams.get("generation") === String(generation), async (route) => {
      stagedDocumentRequests += 1;
      await new Promise((resolve) => setTimeout(resolve, 700));
      await route.fulfill({
        status: 200,
        contentType: "text/html; charset=utf-8",
        body: `<!doctype html><html><body data-guest-v2="1" data-guest-generation="${generation}">
          <section id="feed" data-feed>
            <article class="note" id="note-${newID}" data-created-at="200" data-ascii-author="Ready Author" data-ascii-avatar="${avatar}">
              <div class="ascii-card"><a class="note-feed-avatar"><img src="${avatar}" data-ptxt-avatar-original-src="${avatar}" alt=""></a><strong>Ready Author</strong></div>
            </article>
          </section>
          <button data-load-more data-feed-url="/feed" type="button">Load more</button>
        </body></html>`,
      });
    });

    await page.evaluate(({ oldID }) => {
      const feed = document.querySelector("#feed[data-feed]");
      if (!(feed instanceof HTMLElement)) return;
      feed.innerHTML = `<article class="note" id="note-${oldID}" data-created-at="100"></article>`;
      document.body.dataset.visibleGuestGeneration = document.body.dataset.guestGeneration || "0";
      document.dispatchEvent(new Event("visibilitychange"));
    }, { oldID });

    await page.waitForTimeout(200);
    expect(await page.locator("[data-new-notes]").isHidden()).toBe(true);
    await expect(page.locator("[data-new-notes]")).toHaveText("Load 1 new note", { timeout: 5_000 });
    await expect(page.locator("[data-new-notes]")).toBeVisible();
    expect(stagedDocumentRequests).toBe(1);

    await page.locator("[data-new-notes]").click();

    await expect(page).toHaveURL(/\/feed$/);
    const note = page.locator(`#note-${newID}`);
    await expect(note).toBeVisible();
    await expect(note).toHaveAttribute("data-ascii-author", "Ready Author");
    await expect(note.locator("img")).toHaveAttribute("data-ptxt-avatar-original-src", avatar);
    await expect(page.locator("[data-retro-loader-type='feed-newer']")).toHaveCount(0);
    expect(stagedDocumentRequests).toBe(1);
  });
});
