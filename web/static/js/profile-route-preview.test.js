import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { nip19 } from "../lib/nostr-tools.js";

import { rememberProfileRoutePreviewFromLink, profileRoutePreview } from "./profile-route-preview.js";

describe("profile route preview", () => {
  it("captures author metadata from a clicked note link", () => {
    globalThis.Element = class {};
    class FakeAnchor extends Element {
      constructor(href, text, card) {
        super();
        this.href = href;
        this._href = href;
        this.textContent = text;
        this._card = card;
      }
      getAttribute(name) {
        return name === "href" ? this._href : null;
      }
      closest(selector) {
        if (selector === ".note, .comment, [data-thread-tree-note]") return this._card;
        if (selector === "a[href^='/u/']") return this;
        return null;
      }
    }
    const pubkey = "ab".repeat(32);
    const event = {
      id: "cd".repeat(32),
      pubkey,
      kind: 1,
      created_at: 123,
      tags: [["e", "ef".repeat(32), "", "reply"]],
      content: "reply body",
    };
    const card = {
      dataset: {
        asciiAuthor: "Alice",
        asciiAvatar: "https://cdn.example.com/alice.png",
        asciiRelay: "wss://relay.example",
        asciiEvent: JSON.stringify(event),
      },
      querySelector() {
        return null;
      },
    };
    const link = new FakeAnchor(`/u/${nip19.nprofileEncode({ pubkey, relays: ["wss://relay.example"] })}`, "Alice", card);

    const preview = rememberProfileRoutePreviewFromLink(link);

    assert.equal(preview?.pubkey, pubkey);
    assert.equal(preview?.display_name, "Alice");
    assert.equal(preview?.avatar_url, "https://cdn.example.com/alice.png");
    assert.deepEqual(preview?.relay_hints, ["wss://relay.example"]);
    assert.deepEqual(preview?.timeline_event, event);
    assert.equal(profileRoutePreview(pubkey)?.display_name, "Alice");
  });

  it("does not treat arbitrary profile-link prose as an author identity", () => {
    globalThis.Element = class {};
    class FakeAnchor extends Element {
      constructor(href) {
        super();
        this.href = href;
        this.textContent = "open cached thread participant profile";
      }
      getAttribute(name) {
        return name === "href" ? this.href : null;
      }
      closest() {
        return null;
      }
    }
    const pubkey = "ef".repeat(32);
    const link = new FakeAnchor(`/u/${pubkey}`);

    assert.equal(rememberProfileRoutePreviewFromLink(link), null);
    assert.equal(profileRoutePreview(pubkey), null);
  });
});
