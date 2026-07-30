import assert from "node:assert/strict";
import test from "node:test";

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
globalThis.MutationObserver = class MutationObserver {
  observe() {}
  disconnect() {}
};
globalThis.HTMLElement = class HTMLElement {};
globalThis.Element = globalThis.HTMLElement;
globalThis.Node = globalThis.HTMLElement;
globalThis.HTMLInputElement = class HTMLInputElement extends globalThis.HTMLElement {};
globalThis.HTMLFormElement = class HTMLFormElement extends globalThis.HTMLElement {};
globalThis.HTMLButtonElement = class HTMLButtonElement extends globalThis.HTMLElement {};
globalThis.HTMLSelectElement = class HTMLSelectElement extends globalThis.HTMLElement {};
globalThis.HTMLTextAreaElement = class HTMLTextAreaElement extends globalThis.HTMLElement {};
globalThis.window = Object.assign(globalThis, {
  addEventListener() {},
  removeEventListener() {},
  dispatchEvent() {},
  clearTimeout,
  setTimeout,
  location: {
    origin: "https://example.com",
    href: "https://example.com/search",
  },
  matchMedia() {
    return { matches: false, addEventListener() {}, removeEventListener() {} };
  },
});
globalThis.requestAnimationFrame = (cb) => {
  cb();
  return 1;
};
globalThis.cancelAnimationFrame = () => {};
globalThis.document = {
  addEventListener() {},
  body: {
    classList: {
      contains() {
        return false;
      },
    },
    style: {
      setProperty() {},
      removeProperty() {},
    },
  },
  documentElement: {
    classList: {
      add() {},
      remove() {},
    },
  },
  removeEventListener() {},
  querySelector() {
    return null;
  },
  querySelectorAll() {
    return [];
  },
  createElement() {
    return { className: "", textContent: "", append() {} };
  },
};

const {
  renderSearchHeading,
  searchModeFromURL,
  searchModeURLs,
} = await import("./client-render.js");

test("searchModeFromURL normalizes missing and invalid modes to notes", () => {
  assert.equal(searchModeFromURL("https://example.com/search?q=alice"), "notes");
  assert.equal(searchModeFromURL("https://example.com/search?q=alice&mode=bogus"), "notes");
  assert.equal(searchModeFromURL("https://example.com/search?q=alice&mode=users"), "users");
});

test("searchModeURLs preserves the query and drops note scope when switching to users", () => {
  const urls = searchModeURLs("alice", "https://example.com/search?q=alice&mode=notes&scope=all");
  assert.equal(urls.notesURL, "/search?q=alice&mode=notes&scope=all");
  assert.equal(urls.usersURL, "/search?q=alice&mode=users");
});

test("renderSearchHeading swaps copy for users mode", () => {
  const heading = { innerHTML: "" };
  const root = {
    querySelector(selector) {
      return selector === "[data-search-heading]" || selector === ".search-heading" ? heading : null;
    },
  };
  renderSearchHeading(root, "alice", "https://example.com/search?q=alice&mode=users");
  assert.match(heading.innerHTML, /Search cached profiles by display name, npub, hex pubkey, or nip05/);
  assert.doesNotMatch(heading.innerHTML, /Scope:/);
  assert.match(heading.innerHTML, /<strong class="search-mode-option is-active">User search<\/strong>/);
  assert.match(heading.innerHTML, /href="\/search\?q=alice&mode=notes"/);
});
