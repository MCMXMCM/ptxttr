import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { createNavigationRouteState } from "./navigation-route-state.js";

describe("navigation-route-state", () => {
  it("tracks route changes and refresh tokens via an explicit store", () => {
    const runtime = createNavigationRouteState("feed");
    const seen = [];
    runtime.store.subscribe((state) => seen.push(state.currentRoute));

    runtime.setCurrentRoute("thread");
    const token = runtime.nextRefreshToken("thread", "https://example.com/thread/abc?selected=1");

    assert.equal(runtime.getCurrentRoute(), "thread");
    assert.equal(token, "thread:1:/thread/abc?selected=1");
    assert.deepEqual(seen, ["thread", "thread"]);
  });

  it("marks only the newest navigation request as current", () => {
    const runtime = createNavigationRouteState("feed");
    const first = runtime.beginNavigation();
    const second = runtime.beginNavigation();

    assert.equal(runtime.navigationRequestIsCurrent(first), false);
    assert.equal(runtime.navigationRequestIsCurrent(second), true);
  });

  it("still executes queued work when used as a plain async wrapper", async () => {
    const runtime = createNavigationRouteState("feed");
    const order = [];
    await Promise.all([
      runtime.enqueueNavigation(async () => {
        order.push("first:start");
        await Promise.resolve();
        order.push("first:end");
      }),
      runtime.enqueueNavigation(async () => {
        order.push("second");
      }),
    ]);
    assert.deepEqual(order, ["first:start", "second", "first:end"]);
  });
});
