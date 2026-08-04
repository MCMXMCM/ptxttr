import assert from "node:assert/strict";
import { beforeEach, describe, it } from "node:test";

import {
  PROFILE_MEMORY_CACHE_UPDATED_EVENT,
  cachedProfile,
  clearProfileMemoryCache,
  mergeCachedProfilesByPubkey,
  rememberProfile,
  rememberProfiles,
} from "./profile-memory-cache.js";

const pk = "ab".repeat(32);

beforeEach(() => {
  clearProfileMemoryCache();
});

describe("profile memory cache", () => {
  it("keeps richer cached metadata instead of replacing it with blanks", () => {
    rememberProfile({ pubkey: pk, display_name: "Alice", picture: "https://example.com/a.jpg", created_at: 10 });
    rememberProfile({ pubkey: pk, display_name: "", picture: "", created_at: 10 });
    assert.equal(cachedProfile(pk).display_name, "Alice");
    assert.equal(cachedProfile(pk).picture, "https://example.com/a.jpg");
  });

  it("lets a newer authoritative metadata event clear old fields", () => {
    rememberProfile({
      pubkey: pk,
      event_id: "11".repeat(32),
      display_name: "Alice",
      picture: "https://example.com/a.jpg",
      about: "old bio",
      created_at: 10,
    });
    rememberProfile({
      pubkey: pk,
      event_id: "22".repeat(32),
      display_name: "Alice Updated",
      picture: "",
      about: "",
      created_at: 11,
    });

    assert.equal(cachedProfile(pk).display_name, "Alice Updated");
    assert.equal(cachedProfile(pk).picture, "");
    assert.equal(cachedProfile(pk).about, "");
  });

  it("accepts pubkey-keyed API maps that omit pubkey in the value", () => {
    rememberProfiles({
      [pk]: { display_name: "Bob", avatar_url: "/avatar/bob", created_at: 5 },
    });
    assert.equal(cachedProfile(pk).display_name, "Bob");
  });

  it("announces promoted profiles so visible placeholders can repaint", () => {
    const events = [];
    const previousWindow = globalThis.window;
    const target = new EventTarget();
    target.addEventListener(PROFILE_MEMORY_CACHE_UPDATED_EVENT, (event) => {
      events.push(event.detail.profile);
    });
    globalThis.window = target;
    try {
      rememberProfile({ pubkey: pk, display_name: "", created_at: 10 });
      rememberProfile({ pubkey: pk, display_name: "Alice", created_at: 10 });
      rememberProfile({ pubkey: pk, display_name: "", created_at: 10 });
    } finally {
      if (previousWindow === undefined) delete globalThis.window;
      else globalThis.window = previousWindow;
    }

    assert.equal(events.length, 2);
    assert.equal(events[1].display_name, "Alice");
    assert.equal(cachedProfile(pk).display_name, "Alice");
  });

  it("preserves cached thread avatars when a later profile map is thin", () => {
    rememberProfile({
      pubkey: pk,
      display_name: "Alice",
      picture: "https://example.com/a.jpg",
      avatar_url: "/avatar/alice?v=one",
      created_at: 10,
    });

    const merged = mergeCachedProfilesByPubkey(
      [pk],
      { [pk]: { pubkey: pk, display_name: "Alice", picture: "https://example.com/a.jpg" } },
      { [pk]: { pubkey: pk, display_name: "", picture: "", avatar_url: "" } },
    );

    assert.equal(merged[pk].display_name, "Alice");
    assert.equal(merged[pk].picture, "https://example.com/a.jpg");
    assert.equal(merged[pk].avatar_url, "/avatar/alice?v=one");
  });

  it("normalizes server profile JSON and derives the local avatar proxy", () => {
    const pubkey = "55".repeat(32);
    const remembered = rememberProfiles({
      [pubkey]: {
        PubKey: pubkey,
        Display: "Server Author",
        Picture: "https://images.example/avatar.png",
      },
    });

    assert.equal(remembered[pubkey].display_name, "Server Author");
    assert.equal(remembered[pubkey].picture, "https://images.example/avatar.png");
    assert.equal(remembered[pubkey].avatar_url, `/avatar/${pubkey}`);
  });
});
