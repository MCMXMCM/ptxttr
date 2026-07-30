import assert from "node:assert/strict";
import { describe, it } from "node:test";

function makeStorage() {
  const data = new Map();
  return {
    getItem(key) {
      return data.has(key) ? data.get(key) : null;
    },
    setItem(key, value) {
      data.set(key, String(value));
    },
    removeItem(key) {
      data.delete(key);
    },
    clear() {
      data.clear();
    },
  };
}

globalThis.localStorage = makeStorage();
globalThis.sessionStorage = makeStorage();
globalThis.CustomEvent = class CustomEvent {
  constructor(type, init = {}) {
    this.type = type;
    this.detail = init.detail;
  }
};
globalThis.window = Object.assign(globalThis, {
  addEventListener() {},
  dispatchEvent() {},
  clearTimeout,
  setTimeout,
  location: {
    origin: "https://example.com",
    href: "https://example.com/",
  },
});
globalThis.document = {
  addEventListener() {},
  querySelector() {
    return null;
  },
  querySelectorAll() {
    return [];
  },
};

const {
  feedLoaderMarkup,
  notificationsLoaderMarkup,
  profileFeedLoaderMarkup,
  profileListLoaderMarkup,
  staticRightRail,
  threadHeaderSkeletonMarkup,
  threadRouteLoaderMarkup,
} = await import("./shell.js");
const { setSession, clearSession } = await import("./session.js");
const { markBootstrapPending, markBootstrapComplete } = await import("./first-login-bootstrap.js");

const VIEWER = "ab".repeat(32);

describe("retro loader shell markup", () => {
  it("renders the home feed retro loader markup", () => {
    clearSession();
    const markup = feedLoaderMarkup();
    assert.match(markup, /data-retro-loader/);
    assert.match(markup, /data-retro-loader-activity/);
    assert.doesNotMatch(markup, /data-feed-loader-card="0"/);
    assert.doesNotMatch(markup, /building your network/);
  });

  it("can render a progress-only feed loader for in-place rehydration", () => {
    clearSession();
    const markup = feedLoaderMarkup({ showActivity: false });
    assert.match(markup, /data-retro-loader/);
    assert.doesNotMatch(markup, /data-retro-loader-activity/);
  });

  it("uses the richer onboarding copy only during pending signed-in bootstrap", () => {
    setSession({ method: "readonly", pubkey: VIEWER, npub: "npub-test" });
    markBootstrapPending(VIEWER);
    const pendingMarkup = feedLoaderMarkup();
    assert.match(pendingMarkup, /building your network/);
    assert.match(pendingMarkup, /server is building your Web of Trust/);
    assert.match(pendingMarkup, /discovering relay hints from contacts/);
    assert.match(pendingMarkup, /saving your reusable feed slice/);

    markBootstrapComplete(VIEWER);
    const completedMarkup = feedLoaderMarkup();
    assert.doesNotMatch(completedMarkup, /building your network/);
  });

  it("renders profile, notifications, and thread retro loaders", () => {
    assert.doesNotMatch(profileFeedLoaderMarkup("posts"), /data-feed-loader-card="0"/);
    assert.doesNotMatch(profileFeedLoaderMarkup("posts"), /data-retro-loader-quiet-after-ms="[1-9]/);
    assert.match(profileFeedLoaderMarkup("replies"), /data-retro-loader-type="profile-replies"/);
    assert.match(profileListLoaderMarkup("followers"), /data-retro-loader-type="profile-followers"/);
    assert.match(notificationsLoaderMarkup(), /data-retro-loader-type="notifications"/);
    assert.match(threadRouteLoaderMarkup(), /data-retro-loader-type="thread"/);
    assert.match(threadRouteLoaderMarkup(), /thread-telemetry-loader/);
    assert.doesNotMatch(threadRouteLoaderMarkup(), /thread-route-loader/);
    assert.doesNotMatch(threadRouteLoaderMarkup(), /loading thread/);
    assert.doesNotMatch(threadRouteLoaderMarkup(), /<p class="retro-loader-title"/);
    assert.match(threadRouteLoaderMarkup(), /data-retro-loader-quiet-after-ms=""/);
    assert.doesNotMatch(threadRouteLoaderMarkup(), /data-retro-loader-hide-progress-when-quiet/);
  });

  it("renders search mode controls in the static right rail", () => {
    const markup = staticRightRail("alice", { mode: "users", trending: false });
    assert.match(markup, /<strong class="search-mode-option is-active">User search<\/strong>/);
    assert.match(markup, /href="\/search\?q=alice&mode=notes"/);
    assert.match(markup, /placeholder="Search users"/);
  });

  it("renders a pending thread shell without a summary sub-header", () => {
    const markup = threadHeaderSkeletonMarkup();
    assert.equal(markup, "");
    assert.doesNotMatch(markup, /data-feed-loader/);
  });

  it("can render a progress-only thread loader when thread content is already visible", () => {
    const markup = threadRouteLoaderMarkup({ showActivity: false });
    assert.match(markup, /data-retro-loader-type="thread"/);
    assert.doesNotMatch(markup, /data-retro-loader-activity/);
  });
});
