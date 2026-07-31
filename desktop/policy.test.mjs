import test from "node:test";
import assert from "node:assert/strict";
import {
  classifyNavigation,
  loopbackOrigin,
  shouldOpenInBackground,
  swipeNavigation,
} from "./policy.mjs";

test("navigation policy permits only the exact loopback origin", () => {
  const origin = loopbackOrigin();
  assert.equal(classifyNavigation(`${origin}/thread/abc`, origin).kind, "internal");
  assert.equal(classifyNavigation("https://example.com/", origin).kind, "external");
  assert.equal(classifyNavigation("http://localhost:24787/", origin).kind, "external");
  assert.equal(classifyNavigation("file:///etc/passwd", origin).kind, "blocked");
  assert.equal(classifyNavigation("javascript:alert(1)", origin).kind, "blocked");
});

test("swipes map right to back and left to forward", () => {
  const calls = [];
  const history = {
    canGoBack: () => true,
    canGoForward: () => true,
    goBack: () => calls.push("back"),
    goForward: () => calls.push("forward"),
  };
  assert.equal(swipeNavigation("right", history), "back");
  assert.equal(swipeNavigation("left", history), "forward");
  assert.deepEqual(calls, ["back", "forward"]);
});

test("only background-tab disposition stays in the background", () => {
  assert.equal(shouldOpenInBackground("background-tab"), true);
  assert.equal(shouldOpenInBackground("foreground-tab"), false);
});
