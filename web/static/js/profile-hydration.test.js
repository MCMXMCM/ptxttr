import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  hasRenderableProfileMetadata,
  renderableProfileFieldCount,
  shouldPromoteProfileMetadata,
} from "./profile-hydration.js";

const pubkey = "ab".repeat(32);

describe("profile metadata promotion", () => {
  it("counts renderable metadata fields", () => {
    assert.equal(renderableProfileFieldCount({
      display_name: "Alice",
      about: "hello",
      nip05: "alice@example.com",
    }), 3);
    assert.equal(renderableProfileFieldCount({ pubkey }), 0);
  });

  it("detects when a profile has user-visible metadata", () => {
    assert.equal(hasRenderableProfileMetadata({ pubkey }), false);
    assert.equal(hasRenderableProfileMetadata({ pubkey, picture: "https://example.com/a.png" }), true);
  });

  it("promotes authoritative metadata over fallback preview data", () => {
    assert.equal(shouldPromoteProfileMetadata(
      { pubkey, display_name: "npub1abc", avatar_url: "" },
      { pubkey, display_name: "Alice", event_id: "f".repeat(64), created_at: 10 },
    ), true);
  });

  it("promotes newer metadata events", () => {
    assert.equal(shouldPromoteProfileMetadata(
      { pubkey, display_name: "Alice", event_id: "a".repeat(64), created_at: 10 },
      { pubkey, display_name: "Alice v2", event_id: "b".repeat(64), created_at: 20 },
    ), true);
  });

  it("promotes same-timestamp metadata when the new profile is richer", () => {
    assert.equal(shouldPromoteProfileMetadata(
      { pubkey, display_name: "Alice", event_id: "a".repeat(64), created_at: 10 },
      { pubkey, display_name: "Alice", nip05: "alice@example.com", event_id: "b".repeat(64), created_at: 10 },
    ), true);
  });

  it("does not replace a richer current profile with older or thinner data", () => {
    assert.equal(shouldPromoteProfileMetadata(
      { pubkey, display_name: "Alice", about: "bio", event_id: "b".repeat(64), created_at: 20 },
      { pubkey, display_name: "Alice", event_id: "a".repeat(64), created_at: 10 },
    ), false);
  });
});
