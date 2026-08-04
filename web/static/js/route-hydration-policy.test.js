import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { shouldHydrateClientRoute } from "./app/route-hydration-policy.js";

describe("client route hydration policy", () => {
  it("hydrates a desktop profile even when the server rendered an empty usable shell", () => {
    assert.equal(shouldHydrateClientRoute({
      route: "profile",
      features: { localFirst: true, directRelayReads: false },
      serverRenderedInitialRoute: true,
      serverRendered: true,
    }), true);
  });

  it("keeps hosted server-rendered profiles authoritative", () => {
    assert.equal(shouldHydrateClientRoute({
      route: "profile",
      features: { localFirst: false, directRelayReads: false },
      serverRenderedInitialRoute: true,
      serverRendered: true,
    }), false);
  });

  it("preserves direct-relay fallback for routes without a usable server render", () => {
    assert.equal(shouldHydrateClientRoute({
      route: "thread",
      features: { localFirst: false, directRelayReads: true },
      serverRenderedInitialRoute: false,
      serverRendered: false,
    }), true);
  });
});
