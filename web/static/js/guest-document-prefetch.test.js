import assert from "node:assert/strict";
import { afterEach, beforeEach, describe, it, mock } from "node:test";

class FakeNode {
  constructor(parent = null) {
    this.parent = parent;
  }

  closest(selector) {
    assert.equal(selector, "a[href]");
    for (let node = this; node; node = node.parent) {
      if (node instanceof FakeAnchor) return node;
    }
    return null;
  }

  contains(other) {
    for (let node = other; node; node = node.parent) {
      if (node === this) return true;
    }
    return false;
  }
}

class FakeAnchor extends FakeNode {
  constructor(href, { target = "", download = false } = {}) {
    super();
    this.href = href;
    this.target = target;
    this.download = download;
  }

  hasAttribute(name) {
    return name === "download" && this.download;
  }
}

let speculationRulesSupported = false;
let listeners;
let appended;
let fetched;

globalThis.window = globalThis;
globalThis.location = { origin: "https://example.com", href: "https://example.com/" };
globalThis.HTMLAnchorElement = FakeAnchor;
globalThis.HTMLScriptElement = { supports: (type) => type === "speculationrules" && speculationRulesSupported };
globalThis.fetch = (url) => {
  fetched.push(url);
  return Promise.resolve();
};
globalThis.document = {
  body: { dataset: { guestV2: "1" } },
  head: { appendChild: (node) => appended.push(node) },
  createElement: () => ({}),
  addEventListener: (type, handler) => listeners.set(type, handler),
};

let moduleInstance = 0;
async function initPrefetch({ speculationRules, prerendering = speculationRules }) {
  speculationRulesSupported = speculationRules;
  if (prerendering) document.prerendering = false;
  else delete document.prerendering;
  moduleInstance += 1;
  const { initGuestDocumentPrefetch } = await import(`./guest-document-prefetch.js?instance=${moduleInstance}`);
  initGuestDocumentPrefetch();
}

function dispatch(type, target, relatedTarget = null) {
  listeners.get(type)?.({ target, relatedTarget });
}

describe("guest document prefetch", () => {
  beforeEach(() => {
    listeners = new Map();
    appended = [];
    fetched = [];
    mock.timers.enable({ apis: ["setTimeout"] });
  });

  afterEach(() => {
    mock.timers.reset();
  });

  it("adds one document rule covering every thread and profile link", async () => {
    await initPrefetch({ speculationRules: true });
    assert.equal(appended.length, 1);
    assert.equal(appended[0].type, "speculationrules");
    const [rule, ...rest] = JSON.parse(appended[0].textContent).prefetch;
    assert.deepEqual(rest, []);
    assert.equal(rule.source, "document");
    assert.equal(rule.eagerness, "moderate");
    assert.deepEqual(rule.where.and[0], { or: [{ href_matches: "/thread/*" }, { href_matches: "/u/*" }] });
    assert.deepEqual(rule.where.and[1], { not: { selector_matches: "[target], [download]" } });
    assert.equal(listeners.size, 0);
  });

  it("uses the hover fallback where the rules are accepted but never acted on", async () => {
    await initPrefetch({ speculationRules: true, prerendering: false });
    assert.deepEqual(appended, []);
    assert.deepEqual([...listeners.keys()].sort(), ["focusin", "focusout", "pointerout", "pointerover"]);
  });

  it("prefetches a link the pointer rests on once, after the hover delay", async () => {
    await initPrefetch({ speculationRules: false });
    const link = new FakeAnchor("/thread/abc?selected=def#note-def");
    const label = new FakeNode(link);
    dispatch("pointerover", label);
    mock.timers.tick(149);
    assert.deepEqual(fetched, []);
    mock.timers.tick(1);
    assert.deepEqual(fetched, ["https://example.com/thread/abc?selected=def"]);

    dispatch("pointerout", label);
    dispatch("pointerover", link);
    mock.timers.tick(150);
    assert.equal(fetched.length, 1);
  });

  it("keeps waiting while the pointer moves inside the link", async () => {
    await initPrefetch({ speculationRules: false });
    const link = new FakeAnchor("/u/abc");
    const label = new FakeNode(link);
    dispatch("pointerover", link);
    mock.timers.tick(100);
    dispatch("pointerout", link, label);
    dispatch("pointerover", label);
    mock.timers.tick(50);
    assert.deepEqual(fetched, ["https://example.com/u/abc"]);
  });

  it("skips links the pointer or focus only passes over", async () => {
    await initPrefetch({ speculationRules: false });
    const first = new FakeAnchor("/thread/first");
    const second = new FakeAnchor("/thread/second");
    dispatch("pointerover", first);
    mock.timers.tick(100);
    dispatch("pointerout", first, new FakeNode());
    dispatch("focusin", second);
    mock.timers.tick(100);
    dispatch("focusout", second, null);
    mock.timers.tick(500);
    assert.deepEqual(fetched, []);
  });

  it("ignores links to other origins, other routes or other windows", async () => {
    await initPrefetch({ speculationRules: false });
    for (const link of [
      new FakeAnchor("https://elsewhere.example/thread/abc"),
      new FakeAnchor("/about"),
      new FakeAnchor("/thread/abc", { target: "_blank" }),
      new FakeAnchor("/u/abc", { download: true }),
    ]) {
      dispatch("pointerover", link);
      mock.timers.tick(200);
      dispatch("pointerout", link);
    }
    assert.deepEqual(fetched, []);
  });
});
