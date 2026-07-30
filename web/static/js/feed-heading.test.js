import assert from "node:assert/strict";
import { beforeEach, describe, it } from "node:test";

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
globalThis.window = Object.assign(globalThis, {
  addEventListener() {},
  dispatchEvent() {},
  location: { href: "https://example.com/" },
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
  feedHeadingNeedsRefresh,
  feedHeadingPrefSignature,
  feedHeadingSummaryText,
  loggedOutSeedDisplayName,
  renderFeedHeadingMarkup,
} = await import("./feed-heading.js");
const {
  ensureDefaultViewerPrefs,
  setWebOfTrustEnabledPref,
  setWebOfTrustDepthPref,
  setWebOfTrustSeedPref,
} = await import("./sort-prefs.js");
const { clearSession, setSession } = await import("./session.js");

const GIGI_NPUB = "npub1dergggklka99wwrs92yz8wdjs952h2ux2ha2ed598ngwu9w7a6fsh9xzpc";
const JACK_NPUB = "npub1sg6plzptd64u62a878hep2kev88swjh3tw00gjsfl8f237lmu63q0uf63m";

beforeEach(() => {
  localStorage.clear();
  clearSession();
  ensureDefaultViewerPrefs();
});

describe("loggedOutSeedDisplayName", () => {
  it("always resolves the guest seed label to Gigi", () => {
    assert.equal(loggedOutSeedDisplayName(GIGI_NPUB), "Gigi");
    assert.equal(loggedOutSeedDisplayName(JACK_NPUB), "Gigi");
  });
});

describe("feedHeadingSummaryText", () => {
  it("links Gigi's profile for logged-out guests", () => {
    const summary = feedHeadingSummaryText({
      loggedOut: true,
      seedDisplayName: "Gigi",
      seedNpub: GIGI_NPUB,
    });
    assert.match(summary, /seeded from <a href="\/u\/npub1dergggklka99wwrs92yz8wdjs952h2ux2ha2ed598ngwu9w7a6fsh9xzpc">Gigi's<\/a> web of trust/);
    assert.match(summary, /<a href="\/login">Login<\/a> to use your own/);
    assert.doesNotMatch(summary, /settings/);
  });

  it("returns empty copy for logged-in users", () => {
    const summary = feedHeadingSummaryText({
      loggedOut: false,
      seedDisplayName: "Gigi",
      seedNpub: GIGI_NPUB,
    });
    assert.equal(summary, "");
  });
});

describe("feedHeadingNeedsRefresh", () => {
  it("ignores legacy logged-out seed changes in the signature", () => {
    setWebOfTrustEnabledPref(true);
    setWebOfTrustDepthPref(3);
    setWebOfTrustSeedPref(JACK_NPUB);
    const url = new URL("https://example.com/");
    const node = { dataset: {} };

    assert.equal(feedHeadingNeedsRefresh(node, url), true);

    node.dataset.feedHeadingSignature = feedHeadingPrefSignature(url);
    assert.equal(feedHeadingNeedsRefresh(node, url), false);

    setWebOfTrustSeedPref(GIGI_NPUB);
    assert.equal(feedHeadingNeedsRefresh(node, url), false);
  });
});

describe("renderFeedHeadingMarkup", () => {
  it("omits WOT depth controls for logged-out guests with stale preferences", () => {
    setWebOfTrustEnabledPref(false);
    setWebOfTrustDepthPref(2);
    const html = renderFeedHeadingMarkup("https://example.com/");
    assert.doesNotMatch(html, /data-feed-wot-controls/);
    assert.doesNotMatch(html, /data-feed-wot-depth-select/);
  });

  it("omits New Note for logged-out guests", () => {
    const html = renderFeedHeadingMarkup("https://example.com/");
    assert.doesNotMatch(html, /data-post-trigger/);
    assert.match(html, /data-feed-sort-select/);
  });

  it("includes New Note for logged-in users", () => {
    setSession({ method: "readonly", pubkey: "a".repeat(64), npub: "npub-test" });
    const html = renderFeedHeadingMarkup("https://example.com/");
    assert.match(html, /data-post-trigger/);
    assert.match(html, /New Note/);
    assert.match(html, /data-feed-wot-depth-select/);
  });
});
