import assert from "node:assert/strict";
import { describe, it } from "node:test";

class FakeElement {
  constructor() {
    this.children = [];
    this.dataset = {};
    this.id = "";
    this.className = "";
    this.outerHTML = "";
  }

  querySelector(selector) {
    const sel = String(selector || "");
    if (sel.includes(".ptxt-carried-thread-note")) {
      return this.children.find((child) => child.className.includes("ptxt-carried-thread-note")) || null;
    }
    if (sel.includes(".thread-focus-skeleton")) {
      return this.children.find((child) => child.className.includes("thread-focus-skeleton")) || null;
    }
    if (sel.includes(".thread-focus-parent--skeleton")) {
      return this.children.find((child) => child.className.includes("thread-focus-parent--skeleton")) || null;
    }
    return null;
  }
}

globalThis.window ??= {};
globalThis.window.location = {
  pathname: "/thread/abc",
  href: "http://localhost/thread/abc",
  origin: "http://localhost",
};

const { canApplyThreadPreview } = await import("./thread-hydrate.js");

describe("canApplyThreadPreview", () => {
  it("allows preview upgrades when a carried thread note is still mounted", () => {
    const carried = new FakeElement();
    carried.className = "ptxt-carried-thread-note";
    const focus = new FakeElement();
    focus.id = "thread-focus";
    focus.children.push(carried);
    const root = {
      querySelector(selector) {
        if (selector === "#thread-focus") return focus;
        if (String(selector).includes("feed-column")) return { dataset: {} };
        return null;
      },
    };

    assert.equal(canApplyThreadPreview(root, "/thread/abc"), true);
  });
});
