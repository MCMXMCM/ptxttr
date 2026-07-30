import assert from "node:assert/strict";
import { afterEach, beforeEach, describe, it } from "node:test";

let visibilityState = "visible";
let intervalID = 0;
const activeIntervals = new Set();
const originalWindow = globalThis.window;
const originalDocument = globalThis.document;
const originalNavigator = globalThis.navigator;
const originalLocalStorage = globalThis.localStorage;
const originalMatchMedia = globalThis.matchMedia;
const originalMutationObserver = globalThis.MutationObserver;
const originalElement = globalThis.Element;
const originalHTMLElement = globalThis.HTMLElement;
const originalHTMLInputElement = globalThis.HTMLInputElement;
const originalHTMLFormElement = globalThis.HTMLFormElement;
const originalHTMLButtonElement = globalThis.HTMLButtonElement;
const originalHTMLAnchorElement = globalThis.HTMLAnchorElement;
const originalHTMLTextAreaElement = globalThis.HTMLTextAreaElement;
const originalRequestAnimationFrame = globalThis.requestAnimationFrame;
const originalCancelAnimationFrame = globalThis.cancelAnimationFrame;

function fakeRoot() {
  return {
    querySelector(selector) {
      if (selector === "[data-nav-root]") return this;
      if (selector === "[data-new-notes]") return null;
      if (selector === "#feed[data-feed]" || selector === ".feed-column [data-feed]" || selector === "[data-feed]") {
        return { querySelector: () => null };
      }
      return null;
    },
    querySelectorAll() {
      return [];
    },
  };
}

beforeEach(() => {
  visibilityState = "visible";
  intervalID = 0;
  activeIntervals.clear();
  const root = fakeRoot();
  Object.defineProperty(globalThis, "document", {
    configurable: true,
    value: {
      get visibilityState() {
        return visibilityState;
      },
      readyState: "loading",
      documentElement: {
        classList: { add() {}, remove() {} },
        querySelector: () => null,
        querySelectorAll: () => [],
      },
      body: {
        classList: { contains: () => false, add() {}, remove() {}, toggle() {} },
        style: { setProperty() {}, removeProperty() {} },
      },
      querySelector: root.querySelector.bind(root),
      querySelectorAll: root.querySelectorAll.bind(root),
      addEventListener() {},
      removeEventListener() {},
    },
  });
  Object.defineProperty(globalThis, "window", {
    configurable: true,
    value: {
      location: { href: "http://localhost/feed", origin: "http://localhost", pathname: "/feed", search: "" },
      matchMedia: () => ({ matches: false, addEventListener() {}, removeEventListener() {} }),
      setInterval() {
        const id = ++intervalID;
        activeIntervals.add(id);
        return id;
      },
      clearInterval(id) {
        activeIntervals.delete(id);
      },
      addEventListener() {},
      removeEventListener() {},
    },
  });
  Object.defineProperty(globalThis, "navigator", {
    configurable: true,
    value: { connection: null },
  });
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: {
      getItem() {
        return "auto";
      },
    },
  });
  Object.defineProperty(globalThis, "matchMedia", {
    configurable: true,
    value: () => ({ matches: false }),
  });
  Object.defineProperty(globalThis, "MutationObserver", {
    configurable: true,
    value: class {
      observe() {}
      disconnect() {}
    },
  });
  class FakeElement {}
  Object.defineProperty(globalThis, "Element", { configurable: true, value: FakeElement });
  Object.defineProperty(globalThis, "HTMLElement", { configurable: true, value: FakeElement });
  Object.defineProperty(globalThis, "HTMLInputElement", { configurable: true, value: FakeElement });
  Object.defineProperty(globalThis, "HTMLFormElement", { configurable: true, value: FakeElement });
  Object.defineProperty(globalThis, "HTMLButtonElement", { configurable: true, value: FakeElement });
  Object.defineProperty(globalThis, "HTMLAnchorElement", { configurable: true, value: FakeElement });
  Object.defineProperty(globalThis, "HTMLTextAreaElement", { configurable: true, value: FakeElement });
  Object.defineProperty(globalThis, "requestAnimationFrame", {
    configurable: true,
    value: (callback) => {
      if (typeof callback === "function") callback();
      return 1;
    },
  });
  Object.defineProperty(globalThis, "cancelAnimationFrame", { configurable: true, value: () => {} });
});

afterEach(() => {
  activeIntervals.clear();
  Object.defineProperty(globalThis, "window", { configurable: true, value: originalWindow });
  Object.defineProperty(globalThis, "document", { configurable: true, value: originalDocument });
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: originalNavigator });
  Object.defineProperty(globalThis, "localStorage", { configurable: true, value: originalLocalStorage });
  Object.defineProperty(globalThis, "matchMedia", { configurable: true, value: originalMatchMedia });
  Object.defineProperty(globalThis, "MutationObserver", { configurable: true, value: originalMutationObserver });
  Object.defineProperty(globalThis, "Element", { configurable: true, value: originalElement });
  Object.defineProperty(globalThis, "HTMLElement", { configurable: true, value: originalHTMLElement });
  Object.defineProperty(globalThis, "HTMLInputElement", { configurable: true, value: originalHTMLInputElement });
  Object.defineProperty(globalThis, "HTMLFormElement", { configurable: true, value: originalHTMLFormElement });
  Object.defineProperty(globalThis, "HTMLButtonElement", { configurable: true, value: originalHTMLButtonElement });
  Object.defineProperty(globalThis, "HTMLAnchorElement", { configurable: true, value: originalHTMLAnchorElement });
  Object.defineProperty(globalThis, "HTMLTextAreaElement", { configurable: true, value: originalHTMLTextAreaElement });
  Object.defineProperty(globalThis, "requestAnimationFrame", { configurable: true, value: originalRequestAnimationFrame });
  Object.defineProperty(globalThis, "cancelAnimationFrame", { configurable: true, value: originalCancelAnimationFrame });
});

describe("syncRoutePolling lifecycle", () => {
  it("does not start feed polling while the page is hidden", async () => {
    visibilityState = "hidden";
    const { syncRoutePolling } = await import("./route-polling.js");
    activeIntervals.clear();

    syncRoutePolling("feed", new URL("http://localhost/feed"));

    assert.equal(activeIntervals.size, 0);
  });

  it("replaces an existing feed poll timer instead of stacking timers", async () => {
    const { syncRoutePolling } = await import("./route-polling.js");
    activeIntervals.clear();

    syncRoutePolling("feed", new URL("http://localhost/feed"));
    syncRoutePolling("feed", new URL("http://localhost/feed"));

    assert.equal(activeIntervals.size, 1);
    assert.deepEqual([...activeIntervals], [2]);
  });
});

describe("profile follow pagination", () => {
  it("intercepts server-rendered follow load-more buttons inside the profile panel", async () => {
    class FakePanel extends HTMLElement {
      constructor() {
        super();
        this.dataset = {};
        this.listeners = new Map();
        this.html = "";
      }
      addEventListener(type, listener) {
        this.listeners.set(type, listener);
      }
      contains(node) {
        return node === this.link;
      }
      querySelector() {
        return null;
      }
      querySelectorAll() {
        return [];
      }
      set innerHTML(value) {
        this.html = value;
      }
      get innerHTML() {
        return this.html;
      }
    }
    class FakeInput extends HTMLElement {
      constructor() {
        super();
        this.listeners = new Map();
      }
      addEventListener(type, listener) {
        this.listeners.set(type, listener);
      }
    }
    class FakeButton extends HTMLElement {
      constructor() {
        super();
        this.dataset = {};
        this.textContent = "Load more";
        this.attrs = new Map();
      }
      closest(selector) {
        return selector === "[data-follow-load-more]" ? this : null;
      }
      getAttribute(name) {
        if (name === "data-follow-fragment") return "following";
        if (name === "data-follow-next-url") return "/u/alice?following_page=2";
        return this.attrs.get(name) || "";
      }
      setAttribute(name, value) {
        this.attrs.set(name, String(value));
      }
      removeAttribute(name) {
        this.attrs.delete(name);
      }
    }

    const panel = new FakePanel();
    const button = new FakeButton();
    panel.link = button;
    const input = new FakeInput();
    const root = {
      querySelector(selector) {
        if (selector === "#user-tab-following") return input;
        if (selector === "#user-panel-following") return panel;
        return null;
      },
      querySelectorAll() {
        return [];
      },
    };

    const { bindProfileLazyTabs } = await import("./route-polling.js");
    bindProfileLazyTabs(new URL("http://localhost/u/alice"), root);

    let prevented = false;
    panel.listeners.get("click")({
      target: button,
      preventDefault() {
        prevented = true;
      },
    });
    await new Promise((resolve) => setTimeout(resolve, 0));

    assert.equal(prevented, true);
    assert.equal(panel.dataset.boundFollowFragmentPanel, "1");
  });
});
