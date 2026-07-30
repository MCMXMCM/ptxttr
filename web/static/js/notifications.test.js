if (typeof globalThis.localStorage === "undefined") {
  const store = new Map();
  globalThis.localStorage = {
    getItem: (key) => store.get(key) ?? null,
    setItem: (key, value) => store.set(key, String(value)),
    removeItem: (key) => store.delete(key),
  };
}
if (typeof globalThis.sessionStorage === "undefined") {
  const store = new Map();
  globalThis.sessionStorage = {
    getItem: (key) => store.get(key) ?? null,
    setItem: (key, value) => store.set(key, String(value)),
    removeItem: (key) => store.delete(key),
  };
}
if (typeof globalThis.CustomEvent === "undefined") {
  globalThis.CustomEvent = class CustomEvent {
    constructor(type, init = {}) {
      this.type = type;
      this.detail = init.detail;
    }
  };
}
if (typeof globalThis.Element === "undefined") {
  globalThis.Element = class Element {};
}
if (typeof globalThis.HTMLElement === "undefined") {
  globalThis.HTMLElement = class HTMLElement extends globalThis.Element {};
}
if (typeof globalThis.HTMLButtonElement === "undefined") {
  globalThis.HTMLButtonElement = class HTMLButtonElement extends globalThis.HTMLElement {};
}
if (typeof globalThis.HTMLAnchorElement === "undefined") {
  globalThis.HTMLAnchorElement = class HTMLAnchorElement extends globalThis.HTMLElement {};
}
if (typeof globalThis.MutationObserver === "undefined") {
  globalThis.MutationObserver = class MutationObserver {
    observe() {}
    disconnect() {}
  };
}
if (typeof globalThis.window === "undefined") {
  globalThis.window = {
    addEventListener() {},
    dispatchEvent() {},
    location: { origin: "https://example.com", pathname: "/notifications" },
    matchMedia() {
      return {
        matches: false,
        addEventListener() {},
        removeEventListener() {},
      };
    },
  };
}
if (typeof globalThis.requestAnimationFrame === "undefined") {
  globalThis.requestAnimationFrame = (callback) => setTimeout(() => callback(0), 0);
}
if (typeof globalThis.cancelAnimationFrame === "undefined") {
  globalThis.cancelAnimationFrame = (id) => clearTimeout(id);
}
if (typeof globalThis.document === "undefined") {
  globalThis.document = {
    addEventListener() {},
    documentElement: { classList: { add() {} } },
    querySelector() { return null; },
    querySelectorAll() { return []; },
    createElement() { return { dataset: {}, append() {}, appendChild() {}, setAttribute() {}, addEventListener() {}, replaceChildren() {} }; },
  };
}

import assert from "node:assert/strict";
import { describe, it } from "node:test";

const {
  NotificationCategory,
  classifyNotificationEvent,
  notificationCounts,
  notificationItemsFromEvents,
} = await import("./notifications.js");

function event(id, kind, tags = [], content = "", createdAt = 1, pubkey = "b".repeat(64)) {
  return { id, kind, tags, content, created_at: createdAt, pubkey };
}

describe("notifications", () => {
  it("classifies direct replies to viewer-owned notes as replies without a p tag", () => {
    const viewerOwned = new Set(["a".repeat(64)]);
    const reply = event(
      "c".repeat(64),
      1,
      [["e", "a".repeat(64), "", "root"], ["e", "a".repeat(64), "", "reply"]],
    );
    assert.equal(classifyNotificationEvent(reply, "f".repeat(64), new Map(), viewerOwned), NotificationCategory.REPLY);
  });

  it("prefers reply over mention when a reply also tags the viewer", () => {
    const viewerOwned = new Set(["a".repeat(64)]);
    const reply = event(
      "d".repeat(64),
      1,
      [["p", "f".repeat(64)], ["e", "a".repeat(64), "", "root"], ["e", "a".repeat(64), "", "reply"]],
    );
    assert.equal(classifyNotificationEvent(reply, "f".repeat(64), new Map(), viewerOwned), NotificationCategory.REPLY);
  });

  it("builds sorted typed items and counts categories", () => {
    const viewer = "f".repeat(64);
    const viewerOwned = new Set(["a".repeat(64)]);
    const items = notificationItemsFromEvents([
      event("1".repeat(64), 1, [["p", viewer]], "mention", 10),
      event("2".repeat(64), 6, [["p", viewer], ["e", "9".repeat(64)]], "repost", 12),
      event("3".repeat(64), 1, [["e", "a".repeat(64), "", "root"], ["e", "a".repeat(64), "", "reply"]], "reply", 11),
    ], { viewerPubkey: viewer, viewerOwnedEventIDs: viewerOwned });

    assert.deepEqual(items.map((item) => item.category), [
      NotificationCategory.REPOST,
      NotificationCategory.REPLY,
      NotificationCategory.MENTION,
    ]);
    assert.deepEqual(notificationCounts(items), {
      reply: 1,
      like: 0,
      repost: 1,
      mention: 1,
      zap: 0,
    });
  });

  it("classifies zap receipts and records zap counts", () => {
    const viewerOwned = new Set(["a".repeat(64)]);
    const zap = event(
      "4".repeat(64),
      9735,
      [
        ["p", "f".repeat(64)],
        ["e", "a".repeat(64)],
        ["amount", "21000"],
        ["description", JSON.stringify({ pubkey: "c".repeat(64), tags: [["e", "a".repeat(64)], ["p", "f".repeat(64)]], content: "nice post" })],
      ],
      "",
      13,
    );
    assert.equal(classifyNotificationEvent(zap, "f".repeat(64), new Map(), viewerOwned), NotificationCategory.ZAP);
    const counts = notificationCounts(notificationItemsFromEvents([zap], {
      viewerPubkey: "f".repeat(64),
      viewerOwnedEventIDs: viewerOwned,
    }));
    assert.equal(counts.zap, 1);
  });
});
