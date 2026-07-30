import assert from "node:assert/strict";
import { describe, it } from "node:test";

if (typeof globalThis.Element === "undefined") {
  globalThis.Element = class Element {};
}
if (typeof globalThis.HTMLElement === "undefined") {
  globalThis.HTMLElement = class HTMLElement extends globalThis.Element {};
}
if (typeof globalThis.HTMLInputElement === "undefined") {
  globalThis.HTMLInputElement = class HTMLInputElement extends globalThis.HTMLElement {};
}
if (typeof globalThis.HTMLButtonElement === "undefined") {
  globalThis.HTMLButtonElement = class HTMLButtonElement extends globalThis.HTMLElement {};
}
if (typeof globalThis.MutationObserver === "undefined") {
  globalThis.MutationObserver = class MutationObserver {
    observe() {}
    disconnect() {}
  };
}

class FakeInput extends globalThis.HTMLInputElement {
  constructor(id) {
    super();
    this.id = id;
    this.checked = false;
    this.events = [];
  }

  dispatchEvent(event) {
    this.events.push(event.type);
    return true;
  }
}

class FakePanel extends globalThis.HTMLElement {
  constructor(id) {
    super();
    this.id = id;
    this.scrolled = false;
  }

  scrollIntoView() {
    this.scrolled = true;
  }
}

class FakeLink extends globalThis.HTMLElement {
  constructor(tabID) {
    super();
    this.dataset = {};
    this.listeners = new Map();
    this.tabID = tabID;
  }

  addEventListener(name, handler) {
    this.listeners.set(name, handler);
  }

  getAttribute(name) {
    return name === "data-profile-tab" ? this.tabID : "";
  }

  closest(selector) {
    return selector === "[data-profile-shell]" ? this.scope || null : null;
  }
}

class FakeScope extends globalThis.HTMLElement {
  constructor(map) {
    super();
    this.map = map;
  }

  querySelector(selector) {
    return this.map.get(selector) || null;
  }
}

const noopMatchMedia = {
  matches: false,
  addEventListener() {},
  removeEventListener() {},
};

globalThis.window ??= {};
globalThis.window.matchMedia = () => noopMatchMedia;
globalThis.window.setTimeout ??= setTimeout;
globalThis.window.clearTimeout ??= clearTimeout;
globalThis.window.addEventListener ??= () => {};
globalThis.window.dispatchEvent ??= () => {};
globalThis.navigator ??= {};
globalThis.localStorage ??= {
  getItem() { return null; },
  setItem() {},
  removeItem() {},
};
globalThis.sessionStorage ??= {
  getItem() { return null; },
  setItem() {},
  removeItem() {},
};
globalThis.document ??= {
  querySelectorAll: () => [],
  querySelector: () => null,
  addEventListener() {},
  documentElement: { classList: { add() {} } },
};

const { bindProfileStatLinks } = await import("./profile-tabs.js");

describe("profile-tabs", () => {
  it("resolves tab inputs from the profile shell when bound on a fragment", () => {
    const input = new FakeInput("user-tab-relays");
    const panel = new FakePanel("user-panel-relays");
    const scope = new FakeScope(new Map([
      ["#user-tab-relays", input],
      ["#user-panel-relays", panel],
    ]));
    const link = new FakeLink("user-tab-relays");
    link.scope = scope;

    const fragmentRoot = {
      querySelectorAll(selector) {
        return selector === "[data-profile-tab]" ? [link] : [];
      },
    };

    bindProfileStatLinks(fragmentRoot);
    const click = link.listeners.get("click");
    assert.equal(typeof click, "function");

    click({
      preventDefault() {},
    });

    assert.equal(input.checked, true);
    assert.deepEqual(input.events, ["change"]);
    assert.equal(panel.scrolled, true);
  });
});
