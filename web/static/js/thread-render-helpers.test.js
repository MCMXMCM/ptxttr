import assert from "node:assert/strict";
import { describe, it } from "node:test";

class FakeElement {
  constructor(tagName) {
    this.tagName = String(tagName || "").toUpperCase();
    this.children = [];
    this.dataset = {};
    this.attributes = {};
    this.className = "";
    this._text = "";
  }

  append(...items) {
    this.children.push(...items);
  }

  setAttribute(name, value) {
    this.attributes[name] = String(value);
  }

  getAttribute(name) {
    return this.attributes[name] || null;
  }

  set textContent(value) {
    this._text = String(value);
    this.children = [];
  }

  get textContent() {
    return `${this._text}${this.children.map((child) => (
      typeof child === "string" ? child : child?.textContent || ""
    )).join("")}`;
  }
}

globalThis.document = {
  createElement(tagName) {
    return new FakeElement(tagName);
  },
};

const {
  createThreadComhead,
  createThreadParticipantMeta,
} = await import("./thread-render-helpers.js");

describe("thread-render-helpers", () => {
  it("renders malicious profile labels as text in thread heads", () => {
    const label = '<img src=x onerror="alert(1)">';
    const head = createThreadComhead(
      { display_name: label, pubkey: "aa".repeat(32) },
      "aa".repeat(32),
      `/thread/${"bb".repeat(32)}`,
      "1m",
    );
    assert.equal(head.children[0].textContent, label);
  });

  it("renders malicious profile labels as text in participant meta", () => {
    const label = "<script>boom()</script>";
    const meta = createThreadParticipantMeta({ display_name: label, pubkey: "aa".repeat(32) });
    assert.equal(meta.children[0].textContent, label);
  });

  it("renders a truncated bio preview in participant meta", () => {
    const meta = createThreadParticipantMeta({
      display_name: "alice",
      pubkey: "aa".repeat(32),
      about: "one two three four five six seven eight nine ten eleven twelve thirteen fourteen",
    });
    assert.equal(meta.children[1].textContent, "one two three four five six seven eight nine ten eleven twelve...");
  });
});
