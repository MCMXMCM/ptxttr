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

class TestElement {
  constructor(tagName) {
    this.tagName = tagName.toUpperCase();
    this.children = [];
    this.dataset = {};
    this.className = "";
    this.textContent = "";
    this.href = "";
  }

  append(...children) {
    this.children.push(...children);
  }

  replaceChildren(...children) {
    this.children = children;
  }

  querySelector(selector) {
    if (selector === "[data-trending-target]") {
      return this.dataset.trendingTarget != null ? this : null;
    }
    if (selector === ".trending-list") {
      return this.find((node) => node.className === "trending-list");
    }
    return null;
  }

  find(predicate) {
    if (predicate(this)) return this;
    for (const child of this.children) {
      if (typeof child?.find === "function") {
        const found = child.find(predicate);
        if (found) return found;
      }
    }
    return null;
  }
}

globalThis.localStorage = makeStorage();
globalThis.sessionStorage = makeStorage();
Object.defineProperty(globalThis, "navigator", {
  value: { userAgent: "" },
  configurable: true,
});
globalThis.window = Object.assign(globalThis, {
  location: { origin: "https://example.com", href: "https://example.com/" },
  addEventListener() {},
  dispatchEvent() {},
  setTimeout,
  clearTimeout,
});
globalThis.document = {
  createElement(tagName) {
    return new TestElement(tagName);
  },
  querySelector() {
    return null;
  },
  querySelectorAll() {
    return [];
  },
  addEventListener() {},
};

const { hydrateTrendingSidebar } = await import("./trending-render.js");
const { clearServerFeedMetadataForTests } = await import("./server-feed-metadata.js");

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  clearServerFeedMetadataForTests();
});

describe("hydrateTrendingSidebar", () => {
  it("falls back beyond the server feed cache when one-week sidebar trends are empty", async () => {
    const target = new TestElement("div");
    target.dataset.trendingTarget = "";
    const root = {
      querySelector(selector) {
        return selector === "[data-trending-target]" ? target : null;
      },
    };
    const requested = [];
    globalThis.fetch = async (input) => {
      const url = new URL(String(input), window.location.origin);
      requested.push(url);
      return {
        ok: true,
        async json() {
          return { notes: [] };
        },
      };
    };

    await hydrateTrendingSidebar(root, { sort: "trend7d", force: true });

    assert.equal(requested.length, 2);
    assert.equal(requested[0].pathname, "/api/feed-notes");
    assert.equal(requested[0].searchParams.get("sort"), "trend7d");
    assert.equal(target.querySelector(".trending-list")?.tagName, "OL");
    assert.equal(target.find((node) => node.className === "muted")?.textContent, "No trending notes yet.");
  });

  it("keeps cached API reply counts when relay metadata is empty", async () => {
    const target = new TestElement("div");
    target.dataset.trendingTarget = "";
    const root = {
      querySelector(selector) {
        return selector === "[data-trending-target]" ? target : null;
      },
    };
    const noteID = "a".repeat(64);
    globalThis.fetch = async (input) => {
      const url = new URL(String(input), window.location.origin);
      if (url.pathname === "/api/feed-notes") {
        return {
          ok: true,
          async json() {
            return {
              notes: [{ id: noteID, pubkey: "b".repeat(64), kind: 1, content: "cached trend" }],
              reply_counts: { [noteID]: 15 },
              profiles: { ["b".repeat(64)]: { pubkey: "b".repeat(64), name: "Trend Author" } },
            };
          },
        };
      }
      return {
        ok: true,
        async json() { return {}; },
      };
    };

    await hydrateTrendingSidebar(root, { sort: "trend7d", force: true });

    assert.equal(target.find((node) => node.tagName === "EM")?.textContent, "↳ 15 replies");
  });
});
