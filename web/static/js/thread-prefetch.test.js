import assert from "node:assert/strict";
import { describe, it } from "node:test";

globalThis.window ??= { location: { origin: "http://localhost" } };

import {
  cardSelectPrefetchSelector,
  maxViewportThreadPrefetches,
  prefetchTargetFromInteraction,
  visibleThreadHrefs,
} from "./thread-prefetch.js";

function withRelays(href) {
  const u = new URL(href, "http://localhost");
  return `${u.pathname}${u.search}${u.hash}`;
}

function routeKind(pathname) {
  if (pathname.startsWith("/thread/")) return "thread";
  if (pathname.startsWith("/u/")) return "profile";
  if (pathname === "/feed") return "feed";
  return "";
}

describe("prefetchTargetFromInteraction", () => {
  it("prefers link target when inside a card", () => {
    const link = { href: "http://localhost/u/abc" };
    const target = {
      closest() {
        return null;
      },
    };
    const closestLinkFn = () => link;
    assert.deepEqual(
      prefetchTargetFromInteraction(closestLinkFn, target, withRelays, routeKind),
      { href: "/u/abc", route: "profile" },
    );
  });

  it("uses data-ascii-select-href when not on a link", () => {
    const noteID = "a".repeat(64);
    const target = {
      closest(sel) {
        if (sel === cardSelectPrefetchSelector) return this;
        return null;
      },
      getAttribute(name) {
        if (name === "data-ascii-select-href") return `/thread/${noteID}`;
        return "";
      },
    };
    const hit = prefetchTargetFromInteraction(() => null, target, withRelays, routeKind);
    assert.equal(hit?.route, "thread");
    assert.equal(hit?.href, `/thread/${noteID}`);
  });
});

describe("visibleThreadHrefs", () => {
  it("caps at maxViewportThreadPrefetches", () => {
    const notes = [];
    for (let i = 0; i < maxViewportThreadPrefetches + 2; i += 1) {
      const id = `${i}`.repeat(64);
      notes.push({ id: `note-${id}` });
    }
    const root = {
      matches(sel) {
        return sel === "[data-feed]";
      },
      querySelector(sel) {
        if (sel === "[data-feed]") return this;
        return null;
      },
      querySelectorAll(sel) {
        if (sel === "[data-feed]") return [this];
        if (sel === ".note[id^='note-']") {
          return notes.map(({ id }) => ({ id }));
        }
        return [];
      },
    };
    const hrefs = visibleThreadHrefs(root, withRelays);
    assert.equal(hrefs.length, maxViewportThreadPrefetches);
    assert.match(hrefs[0], /^\/thread\//);
  });
});
