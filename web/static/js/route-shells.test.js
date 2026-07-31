import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { renderShellForRoute } from "./app/route-shells.js";

describe("profile route shell", () => {
  it("renders relationship controls before relay hydration finishes", () => {
    const html = renderShellForRoute(
      "profile",
      new URL(`https://example.com/u/${"ab".repeat(32)}`),
    );

    assert.match(html, /data-profile-tab="user-tab-following"/);
    assert.match(html, /data-profile-following-count>\.\.\.<\/span>/);
    assert.match(html, /data-profile-tab="user-tab-followers"/);
    assert.match(html, /data-profile-followers-count>\.\.\.<\/span>/);
  });
});
