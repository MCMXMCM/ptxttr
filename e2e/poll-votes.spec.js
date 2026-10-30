// @ts-check
import { test, expect } from "@playwright/test";

import { AUTHOR_PK, POLL_ID, VIEWER_PK, buildPollThreadFixture } from "./helpers/nostr-fixtures.js";
import { installRelayNativeE2E, navigateToThreadFromFeed } from "./helpers/mock-nostr-relay.js";

// Poll votes load the publishing and signing modules only once someone votes,
// so make sure a vote still reaches the signer through those lazy imports.
test.describe("poll votes", () => {
  test("a read-only viewer's poll vote reaches the signer", async ({ page, request }) => {
    expect((await request.post(`/debug/seed-note?id=${POLL_ID}&pubkey=${AUTHOR_PK}`)).ok()).toBeTruthy();
    await installRelayNativeE2E(page, {
      events: buildPollThreadFixture(),
      viewerPubkey: VIEWER_PK,
      wotEnabled: false,
    });
    await navigateToThreadFromFeed(page, POLL_ID);

    const alerts = [];
    page.on("dialog", async (dialog) => {
      alerts.push(dialog.message());
      await dialog.dismiss();
    });
    const option = page.locator(`#thread-focus #note-${POLL_ID} [data-poll-toggle-option="yes"]:visible`).first();
    // Late relay metadata can re-render the card under the first click.
    await expect(async () => {
      if (!alerts.length) await option.click({ timeout: 5_000 });
      expect(alerts).toContain("Your current login method cannot sign complete this action.");
    }).toPass({ timeout: 30_000 });
  });
});
