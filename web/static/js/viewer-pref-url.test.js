import assert from "node:assert/strict";
import test from "node:test";

import { guestFeedSortDocumentHref } from "./viewer-pref-url.js";

test("guest feed sort navigation sends trending choice on the document request", () => {
  assert.equal(
    guestFeedSortDocumentHref(
      "https://plaintextnostr.com/?_ptxt_ascii_w=120&sort=recent&wot_depth=2#top",
      "trend7d",
    ),
    "/?_ptxt_ascii_w=120&sort=trend7d#top",
  );
});

test("guest feed chronological navigation returns to the canonical document", () => {
  assert.equal(
    guestFeedSortDocumentHref("https://plaintextnostr.com/feed?sort=trend7d", "recent"),
    "/feed",
  );
});
