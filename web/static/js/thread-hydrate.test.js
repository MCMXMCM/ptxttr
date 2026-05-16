import assert from "node:assert/strict";
import { describe, it } from "node:test";

globalThis.window ??= { location: { origin: "http://localhost" } };
import {
  isHydrateBundleUsable,
  isThreadHydrateComplete,
  threadPathNoteID,
} from "./thread-hydrate.js";

describe("threadPathNoteID", () => {
  it("lowercases the path note id", () => {
    const id = "a".repeat(64).toUpperCase();
    assert.equal(
      threadPathNoteID(`http://localhost/thread/${id}`),
      id.toLowerCase(),
    );
  });
});

describe("isThreadHydrateComplete", () => {
  it("accepts root-style hydrate without focus markers", () => {
    const body = '<article id="note-abc">root</article>';
    assert.equal(isThreadHydrateComplete(body, "abc"), true);
  });

  it("rejects mis-rooted reply when server expects focus", () => {
    const selected = "c".repeat(64);
    const body = `<section class="feed-column" data-thread-expects-focus="1">
      <span class="thread-op-label">[OP]</span>
      <article id="note-${selected}">reply shown as root</article>
    </section>`;
    assert.equal(isThreadHydrateComplete(body, selected), false);
  });

  it("accepts focused reply layout with parent and selected", () => {
    const root = "a".repeat(64);
    const selected = "c".repeat(64);
    const body = `<section data-thread-expects-focus="1">
      <a class="thread-op-link" href="/thread/${root}">[OP]</a>
      <div class="thread-focus-parent" id="note-${root}"></div>
      <article class="thread-focus-selected" id="note-${selected}"></article>
    </section>`;
    assert.equal(isThreadHydrateComplete(body, selected), true);
  });

  it("falls back to thread-op-link when expects-focus attribute is absent", () => {
    const root = "a".repeat(64);
    const selected = "c".repeat(64);
    const body = `<a class="thread-op-link" href="/thread/${root}">[OP]</a>
      <div class="thread-focus-parent" id="note-${root}"></div>
      <article class="thread-focus-selected" id="note-${selected}"></article>`;
    assert.equal(isThreadHydrateComplete(body, selected), true);
  });
});

describe("isHydrateBundleUsable", () => {
  it("rejects incomplete server flag and empty body", () => {
    assert.equal(isHydrateBundleUsable({ body: "", threadIncomplete: false }, "abc"), false);
    assert.equal(isHydrateBundleUsable({ body: "x", threadIncomplete: true }, "abc"), false);
    assert.equal(isHydrateBundleUsable({ body: "x", navigate: "/login" }, "abc"), false);
  });

  it("accepts root-style hydrate without focus markers", () => {
    const body = '<article id="note-abc">root</article>';
    assert.equal(isHydrateBundleUsable({ body, threadIncomplete: false }, "abc"), true);
  });
});
