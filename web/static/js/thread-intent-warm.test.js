import assert from "node:assert/strict";
import { beforeEach, describe, it } from "node:test";

function makeStorage() {
  const values = new Map();
  return {
    getItem(key) { return values.has(key) ? values.get(key) : null; },
    setItem(key, value) { values.set(key, String(value)); },
    removeItem(key) { values.delete(key); },
    clear() { values.clear(); },
  };
}

class FakeElement {
  constructor(href) {
    this.href = href;
  }

  closest(selector) {
    if (selector === "[data-ascii-select-href]" || selector === "a[href^='/thread/']") return this;
    return null;
  }

  getAttribute(name) {
    if (name === "data-ascii-select-href" || name === "href") return this.href;
    return null;
  }
}

class FakeRoot {
  constructor() {
    this.documentElement = { dataset: {} };
    this.listeners = new Map();
  }

  addEventListener(type, listener) {
    this.listeners.set(type, listener);
  }

  dispatch(type, target) {
    this.listeners.get(type)?.({ target });
  }
}

globalThis.localStorage = makeStorage();
globalThis.sessionStorage = makeStorage();
globalThis.Element = FakeElement;
globalThis.window = Object.assign(globalThis, {
  location: { origin: "https://example.com", href: "https://example.com/" },
  addEventListener() {},
  dispatchEvent() {},
});
globalThis.document = {
  visibilityState: "visible",
  documentElement: { dataset: { ptxtDesktopMode: "1" } },
  addEventListener() {},
  getElementById() { return null; },
  querySelector() { return null; },
  querySelectorAll() { return []; },
};

const scheduled = new Map();
let nextTimer = 1;
window.setTimeout = (callback, delay) => {
  const id = nextTimer++;
  scheduled.set(id, { callback, delay });
  return id;
};
window.clearTimeout = (id) => scheduled.delete(id);

const requests = [];
globalThis.fetch = async (input, init = {}) => {
  requests.push({ input: String(input), init });
  return new Response(null, { status: 202 });
};

const { setAppBootstrapForTests } = await import("./app/bootstrap.js");
const { initThreadIntentWarm, threadIntentWarmInternals } = await import("./thread-intent-warm.js");

const viewer = "f".repeat(64);
const firstID = "a".repeat(64);
const secondID = "b".repeat(64);
const thirdID = "c".repeat(64);

beforeEach(() => {
  requests.length = 0;
  scheduled.clear();
  localStorage.clear();
  sessionStorage.clear();
  localStorage.setItem("ptxt_nostr_session", JSON.stringify({ method: "readonly", pubkey: viewer }));
  document.documentElement.dataset = { ptxtDesktopMode: "1" };
  setAppBootstrapForTests({ features: { desktopShell: true }, viewer: { pubkey: viewer } });
  threadIntentWarmInternals.resetThreadIntentWarmForTests();
});

describe("thread intent warming", () => {
  it("debounces hover, sends immediate focus/pointer intent, and deduplicates", () => {
    const root = new FakeRoot();
    initThreadIntentWarm(root);
    const first = new FakeElement(`/thread/${firstID}?selected=${firstID}`);
    root.dispatch("pointerover", first);
    assert.equal(requests.length, 0);
    assert.equal(scheduled.size, 1);
    const [{ callback, delay }] = scheduled.values();
    assert.equal(delay, 120);
    callback();
    assert.equal(requests.length, 1);
    assert.deepEqual(JSON.parse(requests[0].init.body), { id: firstID, reason: "hover" });

    root.dispatch("focusin", first);
    assert.equal(requests.length, 1, "same note should be deduplicated for two minutes");

    root.dispatch("focusin", new FakeElement(`/thread/${secondID}#note-${secondID}`));
    root.dispatch("pointerdown", new FakeElement(`/thread/${thirdID}`));
    assert.equal(requests.length, 3);
    assert.equal(JSON.parse(requests[1].init.body).reason, "focus");
    assert.equal(JSON.parse(requests[2].init.body).reason, "pointer");
  });

  it("suppresses hover speculation in saver mode but keeps explicit intent", () => {
    localStorage.setItem("ptxt_power_mode", "saver");
    const root = new FakeRoot();
    const target = new FakeElement(`/thread/${firstID}`);
    initThreadIntentWarm(root);

    root.dispatch("pointerover", target);
    assert.equal(scheduled.size, 0);
    assert.equal(requests.length, 0);

    root.dispatch("focusin", target);
    assert.equal(requests.length, 1);
    assert.deepEqual(JSON.parse(requests[0].init.body), { id: firstID, reason: "focus" });
  });

  it("extracts selected, hash, and path IDs in priority order", () => {
    assert.equal(threadIntentWarmInternals.threadIDFromURL(`/thread/${firstID}?selected=${secondID}`), secondID);
    assert.equal(threadIntentWarmInternals.threadIDFromURL(`/thread/${firstID}#note-${thirdID}`), thirdID);
    assert.equal(threadIntentWarmInternals.threadIDFromURL(`/thread/${firstID}`), firstID);
  });
});
