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

class FakeElement {
  constructor({ id = "", hidden = false, dataThreadTreeView = false } = {}) {
    this.id = id;
    this.hidden = hidden;
    this.dataset = {};
    this.attributes = new Map();
    this.innerHTML = "";
    this.textContent = "";
    this.children = [];
    this.nextElementSibling = null;
    this.scrollHeight = 0;
    this.style = {
      setProperty() {},
      removeProperty() {},
      maxHeight: "",
    };
    this.classList = {
      add() {},
      remove() {},
      contains() {
        return false;
      },
      toggle() {},
    };
    if (dataThreadTreeView) this.attributes.set("data-thread-tree-view", "");
  }

  matches(selector) {
    if (selector === "[data-thread-tree-view]") {
      return this.attributes.has("data-thread-tree-view");
    }
    if (selector === "button.view-more") return false;
    return false;
  }

  querySelector() {
    if (this.innerHTML.includes("data-thread-tree-view")) {
      return new FakeElement({ dataThreadTreeView: true });
    }
    return null;
  }

  querySelectorAll() {
    return [];
  }

  setAttribute(name, value) {
    this.attributes.set(name, String(value));
  }

  removeAttribute(name) {
    this.attributes.delete(name);
  }

  getAttribute(name) {
    return this.attributes.get(name) ?? null;
  }

  hasAttribute(name) {
    return this.attributes.has(name);
  }

  append(node) {
    this.children.push(node);
  }

  addEventListener() {}

  closest() {
    return null;
  }

  contains(node) {
    return this === node || this.children.includes(node);
  }
}

globalThis.Element = FakeElement;
globalThis.HTMLElement = FakeElement;
globalThis.HTMLButtonElement = class HTMLButtonElement extends FakeElement {};
globalThis.HTMLFormElement = class HTMLFormElement extends FakeElement {};
globalThis.HTMLInputElement = class HTMLInputElement extends FakeElement {};
globalThis.HTMLTextAreaElement = class HTMLTextAreaElement extends FakeElement {};
globalThis.HTMLSelectElement = class HTMLSelectElement extends FakeElement {};
globalThis.HTMLAnchorElement = class HTMLAnchorElement extends FakeElement {};
globalThis.HTMLImageElement = class HTMLImageElement extends FakeElement {};
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
globalThis.localStorage = makeStorage();
globalThis.sessionStorage = makeStorage();
globalThis.requestAnimationFrame = (cb) => {
  cb();
  return 1;
};
globalThis.window = Object.assign(globalThis, {
  addEventListener() {},
  removeEventListener() {},
  dispatchEvent() {},
  clearTimeout,
  matchMedia() {
    return {
      matches: false,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeListener() {},
    };
  },
  setTimeout,
  location: {
    origin: "http://localhost:8080",
    href: "http://localhost:8080/thread/" + "a".repeat(64),
    pathname: "/thread/" + "a".repeat(64),
    search: "",
    hash: "",
  },
  innerWidth: 1200,
});

const threadTreeSection = new FakeElement({ id: "thread-tree-view" });
const threadRepliesList = new FakeElement({ id: "thread-replies" });
const relayNativeColumn = new FakeElement();
let relayNativePresent = true;
let treeMediaNotes = [];
let fetchHandler = async () => {
  fetchCalls += 1;
  throw new Error("fetch should not run for relay-native tree rendering");
};

function resetThreadTreeSection() {
  threadTreeSection.innerHTML = "";
  threadTreeSection.textContent = "";
  threadTreeSection.children = [];
  threadTreeSection.attributes = new Map();
  threadTreeSection.querySelector = FakeElement.prototype.querySelector;
  threadTreeSection.querySelectorAll = FakeElement.prototype.querySelectorAll;
}

globalThis.document = {
  documentElement: {
    dataset: {},
    classList: {
      add() {},
      remove() {},
      contains() {
        return false;
      },
      toggle() {},
    },
  },
  body: {
    classList: {
      add() {},
      remove() {},
      contains() {
        return false;
      },
      toggle() {},
    },
    style: {
      setProperty() {},
      removeProperty() {},
    },
  },
  createElement(tagName = "") {
    return new FakeElement({ id: String(tagName || "") });
  },
  addEventListener() {},
  removeEventListener() {},
  querySelector(selector) {
    if (selector === "#thread-tree-view") return threadTreeSection;
    if (selector === "#thread-replies") return threadRepliesList;
    if (selector === ".feed-column[data-relay-native-thread='1']") return relayNativePresent ? relayNativeColumn : null;
    return null;
  },
  querySelectorAll(selector) {
    if (selector === "[data-thread-tree-note]") return treeMediaNotes;
    return [];
  },
};

let fetchCalls = 0;
globalThis.fetch = (...args) => fetchHandler(...args);

const { ensureTreeFragmentForFocus, loadMoreReplies } = await import("./thread.js");

describe("loadMoreReplies", () => {
  it("paginates a server-rendered guest thread through the bounded replies fragment", async () => {
    relayNativePresent = false;
    const selectedID = "b".repeat(64);
    const button = new FakeElement();
    button.dataset.cursor = "123";
    button.dataset.cursorId = "c".repeat(64);
    button.dataset.selectedId = selectedID;
    button.dataset.loadLabel = "Load more direct replies";
    let fetchedURL = "";
    fetchHandler = async (url) => {
      fetchedURL = String(url);
      return {
        ok: true,
        status: 200,
        headers: {
          get(name) {
            if (name === "X-Ptxt-Has-More") return "0";
            return "";
          },
        },
        async text() {
          return "";
        },
      };
    };

    await loadMoreReplies(button);

    const url = new URL(fetchedURL, window.location.origin);
    assert.equal(url.pathname, window.location.pathname);
    assert.equal(url.searchParams.get("fragment"), "replies");
    assert.equal(url.searchParams.get("cursor"), "123");
    assert.equal(url.searchParams.get("cursor_id"), "c".repeat(64));
    assert.equal(url.searchParams.get("selected"), selectedID);
    assert.equal(url.searchParams.get("ascii_w"), "52");
    assert.equal(button.textContent, "No more replies");
    assert.equal(button.disabled, true);
    assert.equal(button.dataset.loading, "0");
  });

  it("shows safe retry copy when the guest pagination request is rate limited", async () => {
    relayNativePresent = false;
    const button = new FakeElement();
    fetchHandler = async () => ({
      ok: false,
      status: 429,
      headers: { get() { return ""; } },
      async text() { return "server detail that must not be shown"; },
    });

    await loadMoreReplies(button);

    assert.equal(button.textContent, "Too many requests. Try again shortly.");
    assert.equal(button.disabled, false);
    assert.equal(button.dataset.loading, "0");
  });

  it("disables pagination when the server cannot advance the cursor", async () => {
    relayNativePresent = false;
    const button = new FakeElement();
    button.dataset.cursor = "123";
    button.dataset.cursorId = "c".repeat(64);
    fetchHandler = async () => ({
      ok: true,
      status: 200,
      headers: {
        get(name) {
          if (name === "X-Ptxt-Has-More") return "1";
          if (name === "X-Ptxt-Cursor") return "123";
          if (name === "X-Ptxt-Cursor-Id") return "c".repeat(64);
          return "";
        },
      },
      async text() { return ""; },
    });

    await loadMoreReplies(button);

    assert.equal(button.textContent, "No new replies to show");
    assert.equal(button.disabled, true);
    assert.equal(button.dataset.loading, "0");
  });
});

describe("ensureTreeFragmentForFocus", () => {
  it("keeps relay-native tree mode on the client path", async () => {
    fetchCalls = 0;
    treeMediaNotes = [];
    relayNativePresent = true;
    resetThreadTreeSection();
    threadTreeSection.innerHTML = "<p>existing thread shell</p>";

    await ensureTreeFragmentForFocus("b".repeat(64));

    assert.equal(fetchCalls, 0);
    assert.equal(threadTreeSection.innerHTML, "<p>existing thread shell</p>");
    assert.equal(threadTreeSection.getAttribute("aria-busy"), null);
  });

  it("loads server-rendered tree fragments before falling back to client hydration", async () => {
    fetchCalls = 0;
    treeMediaNotes = [];
    relayNativePresent = false;
    resetThreadTreeSection();
    const focusID = "c".repeat(64);
    let fetchedURL = "";
    fetchHandler = async (url) => {
      fetchCalls += 1;
      fetchedURL = String(url);
      return {
        ok: true,
        async text() {
          return `<section class="thread-tree-mode" data-thread-tree-view data-thread-tree-root-id="${"a".repeat(64)}"></section>`;
        },
      };
    };

    const loaded = await ensureTreeFragmentForFocus(focusID);

    assert.equal(loaded, true);
    assert.equal(fetchCalls, 1);
    assert.match(fetchedURL, /[?&]fragment=tree\b/);
    assert.match(fetchedURL, new RegExp(`[?&]tree_note=${focusID}\\b`));
    assert.match(threadTreeSection.innerHTML, /data-thread-tree-view/);
    assert.equal(threadTreeSection.getAttribute("aria-busy"), null);
  });

  it("reports failure when no usable tree can be rendered", async () => {
    fetchCalls = 0;
    treeMediaNotes = [];
    relayNativePresent = false;
    resetThreadTreeSection();
    fetchHandler = async () => {
      fetchCalls += 1;
      return {
        ok: true,
        async text() {
          return "";
        },
      };
    };

    const loaded = await ensureTreeFragmentForFocus("e".repeat(64));

    assert.equal(loaded, false);
    assert.equal(fetchCalls, 1);
    assert.equal(threadTreeSection.getAttribute("aria-busy"), null);
    assert.equal(threadTreeSection.children.at(-1)?.textContent, "Could not load tree view.");
  });

  it("applies media mode to lazily loaded tree fragments", async () => {
    fetchCalls = 0;
    relayNativePresent = false;
    resetThreadTreeSection();

    const text = new FakeElement();
    const mediaWrap = new FakeElement({ hidden: true });
    const mediaButton = new FakeElement();
    const mediaMount = new FakeElement({ hidden: true });
    const mediaItem = new FakeElement();
    mediaItem.dataset.threadTreeSource = "caption\nhttps://cdn.example.com/photo.jpg";
    mediaItem.dataset.threadTreeMedia = JSON.stringify([
      { url: "https://cdn.example.com/photo.jpg", type: "image" },
    ]);
    mediaItem.setAttribute("data-thread-tree-display-source", "caption");
    mediaItem.setAttribute("data-thread-tree-media", mediaItem.dataset.threadTreeMedia);
    mediaItem.querySelector = (selector) => {
      if (selector === ".thread-tree-text") return text;
      if (selector === "[data-thread-tree-media-wrap]") return mediaWrap;
      if (selector === "[data-thread-tree-media-toggle]") return mediaButton;
      if (selector === "[data-thread-tree-media-mount]") return mediaMount;
      return null;
    };
    treeMediaNotes = [mediaItem];
    fetchHandler = async () => ({
      ok: true,
      async text() {
        return `<section class="thread-tree-mode" data-thread-tree-view data-thread-tree-root-id="${"a".repeat(64)}"></section>`;
      },
    });

    const loaded = await ensureTreeFragmentForFocus("d".repeat(64));

    assert.equal(loaded, true);
    assert.equal(text.children.length, 1);
    assert.equal(text.children[0].textContent, "caption");
    assert.equal(mediaButton.getAttribute("aria-expanded"), "true");
    assert.equal(mediaMount.hidden, false);
    assert.equal(mediaMount.children.length, 1);
  });
});
