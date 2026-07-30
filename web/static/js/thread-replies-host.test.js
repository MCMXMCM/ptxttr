import assert from "node:assert/strict";
import { describe, it } from "node:test";

class FakeNode {
  constructor(tagName = "div") {
    this.tagName = String(tagName).toUpperCase();
    this.children = [];
    this.dataset = {};
    this.id = "";
    this.className = "";
    this.attributes = {};
  }

  append(...nodes) {
    this.children.push(...nodes);
  }

  remove() {
    const parent = this.parent;
    if (!parent) return;
    parent.children = parent.children.filter((child) => child !== this);
  }

  replaceChildren(...nodes) {
    this.children = nodes;
  }

  removeAttribute(name) {
    delete this.attributes[name];
  }

  setAttribute(name, value) {
    this.attributes[name] = String(value);
  }

  querySelector(selector) {
    const sel = String(selector || "");
    const walk = (node) => {
      if (node.matches?.(sel)) return node;
      for (const child of node.children || []) {
        const hit = walk(child);
        if (hit) return hit;
      }
      return null;
    };
    return walk(this);
  }

  querySelectorAll(selector) {
    const sel = String(selector || "");
    const hits = [];
    const walk = (node) => {
      if (node !== this && node.matches?.(sel)) hits.push(node);
      for (const child of node.children || []) walk(child);
    };
    walk(this);
    return hits;
  }

  matches(selector) {
    const sel = String(selector || "");
    if (sel.startsWith("#")) return this.id === sel.slice(1);
    if (sel.startsWith(".")) return this.className.split(/\s+/).includes(sel.slice(1));
    if (sel.startsWith("[")) {
      const name = sel.slice(1, -1);
      return name in this.attributes;
    }
    return this.tagName.toLowerCase() === sel.toLowerCase();
  }
}

globalThis.document = {
  createElement(tagName) {
    return new FakeNode(tagName);
  },
};

const { ensureThreadRepliesHost } = await import("./thread-replies-host.js");

describe("ensureThreadRepliesHost", () => {
  it("reuses the app-shell placeholder host and clears its skeleton", () => {
    const section = new FakeNode("section");
    section.className = "thread-replies";
    const legacyHost = new FakeNode("div");
    legacyHost.attributes["data-thread-replies-host"] = "";
    legacyHost.parent = section;
    const skeleton = new FakeNode("div");
    skeleton.className = "text-skeleton-stack";
    legacyHost.children = [skeleton];
    const loadMore = new FakeNode("button");
    loadMore.className = "load-more";
    section.children = [legacyHost, loadMore];

    const host = ensureThreadRepliesHost(section);

    assert.equal(host, legacyHost);
    assert.equal(host.id, "thread-replies");
    assert.equal(host.dataset.threadFragment, "replies");
    assert.equal(host.className, "comments");
    assert.equal(section.querySelector("[data-thread-replies-host]"), null);
    assert.equal(section.querySelector(".text-skeleton-stack"), null);
    assert.equal(section.querySelectorAll("#thread-replies").length, 1);
  });

  it("removes a stale placeholder when #thread-replies already exists", () => {
    const section = new FakeNode("section");
    section.className = "thread-replies";
    const legacyHost = new FakeNode("div");
    legacyHost.attributes["data-thread-replies-host"] = "";
    legacyHost.parent = section;
    const skeleton = new FakeNode("div");
    skeleton.className = "text-skeleton-stack";
    legacyHost.children = [skeleton];
    const repliesHost = new FakeNode("div");
    repliesHost.id = "thread-replies";
    repliesHost.className = "comments";
    repliesHost.dataset.threadFragment = "replies";
    section.children = [legacyHost, repliesHost];

    const host = ensureThreadRepliesHost(section);

    assert.equal(host, repliesHost);
    assert.equal(section.querySelector("[data-thread-replies-host]"), null);
    assert.equal(section.querySelector(".text-skeleton-stack"), null);
  });
});
