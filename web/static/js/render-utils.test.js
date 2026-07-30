import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { escapeHTML } from "./render-utils.js";

describe("render-utils", () => {
  it("escapes html-sensitive characters for text and attribute contexts", () => {
    assert.equal(
      escapeHTML(`Tom & "<Jerry>"'`),
      "Tom &amp; &quot;&lt;Jerry&gt;&quot;&#39;",
    );
  });
});
