import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { renderedFeedSessionChanged } from "./feed-render-state.js";

function feed(dataset) {
  return { dataset };
}

describe("rendered feed session state", () => {
  it("accepts a server feed rendered for the active guest scope", () => {
    assert.equal(renderedFeedSessionChanged(feed({
      feedSort: "recent",
      feedViewer: "",
      feedWotEnabled: "1",
      feedWotDepth: "1",
    }), {
      viewer: "",
      sort: "recent",
      wotEnabled: true,
      wotDepth: 1,
    }), false);
  });

  it("rejects cursors rendered for another WoT depth", () => {
    assert.equal(renderedFeedSessionChanged(feed({
      feedSort: "recent",
      feedViewer: "",
      feedWotEnabled: "1",
      feedWotDepth: "1",
    }), {
      viewer: "",
      sort: "recent",
      wotEnabled: true,
      wotDepth: 3,
    }), true);
  });

  it("rejects guest rows after a browser-local viewer is restored", () => {
    assert.equal(renderedFeedSessionChanged(feed({
      feedSort: "recent",
      feedViewer: "",
      feedWotEnabled: "1",
      feedWotDepth: "1",
    }), {
      viewer: "aa".repeat(32),
      sort: "recent",
      wotEnabled: true,
      wotDepth: 1,
    }), true);
  });
});
