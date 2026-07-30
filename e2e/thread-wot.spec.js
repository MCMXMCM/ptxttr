// @ts-check
import { test, expect } from "@playwright/test";

test.describe("thread WoT disclosure", () => {
  test("filtered replies toggle expands hidden block after hydrate", async ({ page }) => {
    const seed = await page.request.post("/debug/seed-thread-wot");
    expect(seed.ok()).toBeTruthy();
    const payload = await seed.json();
    const rootID = payload.root_id;
    const viewerNpub = payload.viewer_npub;

    await page.addInitScript(({ pubkey, npub }) => {
      localStorage.setItem(
        "ptxt_nostr_session",
        JSON.stringify({ method: "readonly", pubkey, npub, canSign: false }),
      );
      localStorage.setItem("ptxt_wot_enabled", "1");
      localStorage.setItem("ptxt_wot_depth", "1");
    }, { pubkey: payload.viewer_pubkey, npub: viewerNpub });

    await page.goto(`/thread/${rootID}`);
    const toggle = page.locator("[data-thread-filtered-replies-toggle]");
    await expect(toggle).toBeVisible({ timeout: 15000 });

    const block = page.locator("[data-thread-filtered-replies]");
    await expect(block).toBeHidden();
    const collapsedParticipants = page.locator("[data-thread-collapsed-participants]");
    const expandedParticipants = page.locator("[data-thread-expanded-participants]");
    await expect(collapsedParticipants).not.toHaveAttribute("hidden", "");
    await expect(expandedParticipants).toHaveAttribute("hidden", "");
    await expect(collapsedParticipants.locator(`a[href="/u/${payload.stranger_pubkey}"]`)).toHaveCount(0);
    await expect(expandedParticipants.locator(`a[href="/u/${payload.stranger_pubkey}"]`)).toHaveCount(1);

    await toggle.click();
    await expect(block).toBeVisible();
    await expect(toggle).toContainText(/hide/i);
    await expect(collapsedParticipants).toHaveAttribute("hidden", "");
    await expect(expandedParticipants).not.toHaveAttribute("hidden", "");
  });

  test("guest desktop participant rail follows the reply disclosure", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    const seed = await page.request.post("/debug/seed-thread-wot");
    expect(seed.ok()).toBeTruthy();
    const payload = await seed.json();

    await page.goto(`/thread/${payload.root_id}`);
    const toggle = page.locator("[data-thread-filtered-replies-toggle]");
    await expect(toggle).toBeVisible({ timeout: 15000 });

    const rail = page.locator('.right-rail[data-thread-fragment="participants"]');
    const collapsedParticipants = rail.locator("[data-thread-collapsed-participants]");
    const expandedParticipants = rail.locator("[data-thread-expanded-participants]");
    await expect(rail).toBeVisible();
    await expect(collapsedParticipants.locator("a.thread-person").first()).toBeVisible();
    await expect(collapsedParticipants.locator(`a[href="/u/${payload.stranger_pubkey}"]`)).toHaveCount(0);
    await expect(expandedParticipants).toHaveAttribute("hidden", "");

    await toggle.click();
    await expect(collapsedParticipants).toHaveAttribute("hidden", "");
    await expect(expandedParticipants).not.toHaveAttribute("hidden", "");
    await expect(expandedParticipants.locator(`a[href="/u/${payload.stranger_pubkey}"]`)).toBeVisible();
  });

  test("tree view can reveal replies filtered out of the Web-of-Trust graph", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const seed = await page.request.post("/debug/seed-thread-wot");
    expect(seed.ok()).toBeTruthy();
    const payload = await seed.json();

    await page.addInitScript(({ pubkey, npub }) => {
      localStorage.setItem(
        "ptxt_nostr_session",
        JSON.stringify({ method: "readonly", pubkey, npub, canSign: false }),
      );
      localStorage.setItem("ptxt_wot_enabled", "1");
      localStorage.setItem("ptxt_wot_depth", "1");
      localStorage.setItem("ptxt_thread_render_mode", "tree");
    }, { pubkey: payload.viewer_pubkey, npub: payload.viewer_npub });

    await page.goto(`/thread/${payload.root_id}`);
    const tree = page.locator("[data-thread-tree-view]");
    await expect(tree).toBeVisible({ timeout: 15000 });

    const toggle = tree.locator("[data-thread-tree-filtered-replies-toggle]");
    const filteredReplies = tree.locator("[data-thread-tree-filtered-replies]");
    await expect(toggle).toBeVisible();
    await expect(toggle).toHaveText(/show \d+ more/i);
    await expect(filteredReplies).toBeHidden();

    await toggle.click();
    await expect(filteredReplies).toBeVisible();
    await expect(filteredReplies.locator("[data-thread-tree-note]")).toHaveCount(1);
    await expect(toggle).toHaveText(/hide/i);
    await expect(toggle).toHaveAttribute("aria-expanded", "true");

    const connector = await tree.evaluate((treeElement) => {
      const filteredTree = treeElement.querySelector("[data-thread-tree-filtered-replies]");
      const visibleTail = filteredTree?.previousElementSibling?.querySelector(
        ":scope > .hn-tree-ul > li.hn-comtr:last-child",
      );
      if (!filteredTree || !visibleTail) return null;
      const bridgeStyle = getComputedStyle(filteredTree, "::before");
      const tailStyle = getComputedStyle(visibleTail, "::after");
      return {
        bridgeContent: bridgeStyle.content,
        bridgeHeight: Number.parseFloat(bridgeStyle.height),
        filteredContinuesTree: filteredTree.classList.contains("continues-thread-tree"),
        visibleTreeContinues: filteredTree.previousElementSibling?.classList.contains(
          "has-expanded-filtered-replies",
        ),
        tailBottom: tailStyle.bottom,
      };
    });
    expect(connector).not.toBeNull();
    expect(connector.bridgeContent).not.toBe("none");
    expect(connector.bridgeHeight).toBeGreaterThan(0);
    expect(connector.filteredContinuesTree).toBe(true);
    expect(connector.visibleTreeContinues).toBe(true);
    expect(connector.tailBottom).toBe("0px");
  });
});
