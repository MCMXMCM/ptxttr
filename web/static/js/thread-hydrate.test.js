import assert from "node:assert/strict";
import { describe, it } from "node:test";

globalThis.window ??= { location: { origin: "http://localhost" } };
import {
  isHydrateBundleUsable,
  isStayingInThreadRoute,
  isThreadHydrateComplete,
  isThreadHydrateRenderable,
  isThreadHydrateResponseIncomplete,
  shouldRenderThreadHydrateBundle,
  threadHydrateSatisfiesExpectedReplies,
  threadPathNoteID,
  threadServerHydrateHref,
} from "./thread-hydrate.js";

describe("threadPathNoteID", () => {
  it("lowercases the path note id", () => {
    const id = "a".repeat(64).toUpperCase();
    assert.equal(
      threadPathNoteID(`http://localhost/thread/${id}`),
      id.toLowerCase(),
    );
  });

  it("decodes nevent path segments", () => {
    const nevent =
      "nevent1qgsp4lsvwn3aw7zwh2f6tcl6249xa6cpj2x3yuu6azaysvncdqywxmgqyz4xtntx4fe9sn9v2406e9a9g5gga0kucusc3f4hfl466rqkr7sh63tqv8h";
    assert.equal(
      threadPathNoteID(`http://localhost/thread/${nevent}`),
      "aa65cd66aa72584cac555fac97a545108ebedcc72188a6b74febad0c161fa17d",
    );
  });

  it("prefers hash-selected reply ids over the thread path root", () => {
    const root = "a".repeat(64);
    const selected = "b".repeat(64).toUpperCase();
    assert.equal(
      threadPathNoteID(`http://localhost/thread/${root}#note-${selected}`),
      selected.toLowerCase(),
    );
  });

  it("prefers selected query ids over the thread path root", () => {
    const root = "a".repeat(64);
    const selected = "b".repeat(64).toUpperCase();
    assert.equal(
      threadPathNoteID(`http://localhost/thread/${root}?selected=${selected}`),
      selected.toLowerCase(),
    );
  });
});

describe("threadServerHydrateHref", () => {
  it("promotes hash-selected tree notes to selected query for server hydrate", () => {
    const root = "a".repeat(64);
    const selected = "b".repeat(64).toUpperCase();
    assert.equal(
      threadServerHydrateHref(`http://localhost/thread/${root}?cursor=1#note-${selected}`),
      `/thread/${root}?selected=${selected.toLowerCase()}&fragment=hydrate#note-${selected}`,
    );
  });

  it("keeps explicit selected query values and strips pagination cursors", () => {
    const root = "a".repeat(64);
    const selected = "b".repeat(64);
    const hash = "c".repeat(64);
    assert.equal(
      threadServerHydrateHref(`http://localhost/thread/${root}?selected=${selected}&cursor=1&cursor_id=x#note-${hash}`),
      `/thread/${root}?selected=${selected}&fragment=hydrate#note-${hash}`,
    );
  });
});

describe("isThreadHydrateComplete", () => {
  it("rejects empty focus html while a note is selected", () => {
    assert.equal(isThreadHydrateComplete("", "abc"), false);
  });

  it("accepts root-style hydrate without focus markers", () => {
    const body = '<article id="note-abc">root</article>';
    assert.equal(isThreadHydrateComplete(body, "abc"), true);
  });

  it("rejects root-style hydrate when the selected note is missing", () => {
    const body = '<article id="note-abc">root</article>';
    assert.equal(isThreadHydrateComplete(body, "def"), false);
  });

  it("rejects stale focus HTML when switching to another reply", () => {
    const selected = "c".repeat(64);
    const other = "d".repeat(64);
    const body = `<section class="thread-focus">
      <div class="thread-focus-parent" id="note-${"a".repeat(64)}"></div>
      <article class="thread-focus-selected" id="note-${selected}"></article>
    </section>`;
    assert.equal(isThreadHydrateComplete(body, other), false);
  });

  it("rejects mis-rooted reply when server expects focus", () => {
    const selected = "c".repeat(64);
    const body = `<section class="feed-column" data-thread-expects-focus="1">
      <span class="thread-op-label">op</span>
      <article id="note-${selected}">reply shown as root</article>
    </section>`;
    assert.equal(isThreadHydrateComplete(body, selected), false);
  });

  it("rejects focus layout while the parent is still a skeleton", () => {
    const root = "a".repeat(64);
    const selected = "c".repeat(64);
    const body = `<span class="thread-header-op-depth">2</span>
      <div class="thread-focus-parent thread-focus-parent--skeleton"></div>
      <article class="thread-focus-selected" id="note-${selected}"></article>`;
    assert.equal(isThreadHydrateComplete(body, selected), false);
  });

  it("accepts focused reply layout with parent and selected", () => {
    const root = "a".repeat(64);
    const selected = "c".repeat(64);
    const body = `<section data-thread-expects-focus="1">
      <span class="thread-header-op-depth">2</span>
      <div class="thread-focus-parent" id="note-${root}"></div>
      <article class="thread-focus-selected" id="note-${selected}"></article>
    </section>`;
    assert.equal(isThreadHydrateComplete(body, selected), true);
  });

  it("falls back to thread-header-op-depth when expects-focus attribute is absent", () => {
    const root = "a".repeat(64);
    const selected = "c".repeat(64);
    const body = `<span class="thread-header-op-depth">2</span>
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

describe("isThreadHydrateResponseIncomplete", () => {
  it("allows renderable HTML even when the server marks it incomplete for caching", () => {
    const response = {
      headers: new Map([["X-Ptxt-Thread-Incomplete", "1"]]),
    };
    assert.equal(
      isThreadHydrateResponseIncomplete(response, '<article id="note-abc">root</article>', "abc"),
      false,
    );
  });

  it("allows focused selected-note HTML while parent context is still incomplete", () => {
    const selected = "c".repeat(64);
    const body = `<section class="feed-column" data-thread-expects-focus="1">
      <section id="thread-focus">
        <div class="thread-focus-parent thread-focus-parent--skeleton"></div>
        <article class="thread-focus-selected" id="note-${selected}"></article>
      </section>
    </section>`;
    assert.equal(isThreadHydrateComplete(body, selected), false);
    assert.equal(isThreadHydrateRenderable(body, selected), true);
    assert.equal(isThreadHydrateResponseIncomplete({ headers: new Map() }, body, selected), false);
  });

  it("keeps retrying focused replies rendered as plain root notes", () => {
    const selected = "c".repeat(64);
    const body = `<section class="feed-column" data-thread-expects-focus="1">
      <article id="note-${selected}">reply shown as root</article>
    </section>`;
    assert.equal(isThreadHydrateRenderable(body, selected), false);
    assert.equal(isThreadHydrateResponseIncomplete({ headers: new Map() }, body, selected), true);
  });

  it("falls back to hydrate HTML completeness", () => {
    const response = { headers: new Map() };
    assert.equal(
      isThreadHydrateResponseIncomplete(response, '<article id="note-abc">root</article>', "abc"),
      false,
    );
    assert.equal(
      isThreadHydrateResponseIncomplete(response, '<article id="note-abc">root</article>', "def"),
      true,
    );
  });
});

describe("shouldRenderThreadHydrateBundle", () => {
  it("renders complete bundles", () => {
    const body = '<article id="note-abc">root</article>';
    assert.equal(
      shouldRenderThreadHydrateBundle({ body, threadIncomplete: false }, "abc"),
      true,
    );
  });

  it("renders incomplete but focused server HTML", () => {
    const selected = "c".repeat(64);
    const body = `<section class="feed-column" data-thread-expects-focus="1">
      <section id="thread-focus">
        <div class="thread-focus-parent thread-focus-parent--skeleton"></div>
        <article class="thread-focus-selected" id="note-${selected}"></article>
      </section>
    </section>`;
    assert.equal(
      shouldRenderThreadHydrateBundle({ body, threadIncomplete: true }, selected),
      true,
    );
  });
});

describe("threadHydrateSatisfiesExpectedReplies", () => {
  it("accepts root-only hydrate when the source card advertised no replies", () => {
    const body = '<div id="thread-replies"></div>';
    assert.equal(threadHydrateSatisfiesExpectedReplies(body, 0), true);
  });

  it("rejects root-only hydrate when the source card advertised replies", () => {
    const body = '<article id="note-root"></article><div id="thread-replies"></div>';
    assert.equal(threadHydrateSatisfiesExpectedReplies(body, 64), false);
  });

  it("accepts a hydrate after reply rows arrive", () => {
    const body = '<div id="thread-replies"><article class="comment" id="note-reply"></article></div>';
    assert.equal(threadHydrateSatisfiesExpectedReplies(body, 64), true);
  });
});

describe("isStayingInThreadRoute", () => {
  it("is true only when navigating between thread URLs", () => {
    assert.equal(isStayingInThreadRoute("thread", "thread"), true);
    assert.equal(isStayingInThreadRoute("feed", "thread"), false);
    assert.equal(isStayingInThreadRoute("thread", "feed"), false);
  });
});
