import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  avatarURLFor,
  avatarRetryURL,
  displayName,
  nip05DisplayText,
  parseProfile,
  preferredAvatarURL,
  profileAvatarURLsMatch,
  profileAPIEntry,
  shortNpubLabel,
} from "./profile-parse.js";

describe("profile-parse", () => {
  it("parses kind-0 JSON content", () => {
    const pk = "aa".repeat(32);
    const profile = parseProfile(pk, {
      id: "bb".repeat(32),
      content: JSON.stringify({
        name: "name",
        display_name: "Display",
        picture: "https://example.com/a.png",
        lud16: "user@example.com",
      }),
    });
    assert.equal(profile.display_name, "Display");
    assert.equal(profile.name, "name");
    assert.equal(profile.lud16, "user@example.com");
    assert.equal(displayName(profile), "Display");
  });

  it("falls back to short npub label", () => {
    const pk = "cc".repeat(32);
    assert.match(displayName(parseProfile(pk, null)), /^npub1[a-z0-9]{3}\.\.[a-z0-9]{4}$/);
  });

  it("formats short npub labels consistently", () => {
    const pk = "cc".repeat(32);
    const label = shortNpubLabel(pk);
    assert.match(label, /^npub1[a-z0-9]{3}\.\.[a-z0-9]{4}$/);
    assert.equal(label, displayName(parseProfile(pk, null)));
  });

  it("routes http avatars through the local avatar proxy", () => {
    const pk = "dd".repeat(32);
    assert.equal(
      avatarURLFor(pk, "https://example.com/avatar.png"),
      `/avatar/${pk}`,
    );
  });

  it("preserves data URLs without proxying", () => {
    const pk = "ee".repeat(32);
    assert.equal(
      avatarURLFor(pk, "data:image/png;base64,abc123"),
      "data:image/png;base64,abc123",
    );
  });

  it("keeps the raw picture but prefers the stable proxied avatar URL for rendering", () => {
    const pk = "ff".repeat(32);
    const parsed = parseProfile(pk, {
      id: "11".repeat(32),
      content: JSON.stringify({
        picture: "https://cdn.example.com/avatar.png",
      }),
    });
    const apiProfile = profileAPIEntry(parsed);
    assert.equal(apiProfile.picture, "https://cdn.example.com/avatar.png");
    assert.equal(apiProfile.avatar_url, `/avatar/${pk}`);
    assert.equal(preferredAvatarURL(apiProfile), `/avatar/${pk}`);
    assert.equal(avatarRetryURL(apiProfile), "https://cdn.example.com/avatar.png");
  });

  it("matches bare avatar proxy URLs against their fingerprinted canonical URLs", () => {
    const pk = "aa".repeat(32);
    const base = "https://ptxt.example";

    assert.equal(profileAvatarURLsMatch(`/avatar/${pk}?v=fresh`, `/avatar/${pk}`, base), true);
    assert.equal(profileAvatarURLsMatch(`/avatar/${pk}`, `/avatar/${pk}?v=fresh`, base), true);
    assert.equal(profileAvatarURLsMatch(`/avatar/${pk}?v=stale`, `/avatar/${pk}?v=fresh`, base), false);
    assert.equal(
      profileAvatarURLsMatch("https://cdn.example.com/a.png", "https://cdn.example.com/b.png", base),
      false,
    );
  });

  it("keeps profile bios in API entries for thread participant rails", () => {
    const pk = "aa".repeat(32);
    const apiProfile = profileAPIEntry(parseProfile(pk, {
      id: "bb".repeat(32),
      content: JSON.stringify({ about: "Building things on Nostr." }),
    }));
    assert.equal(apiProfile.about, "Building things on Nostr.");
  });

  it("hides the domain-root NIP-05 local part for display", () => {
    assert.equal(nip05DisplayText("_@example.com"), "example.com");
  });

  it("keeps non-root NIP-05 identifiers unchanged", () => {
    assert.equal(nip05DisplayText("alice@example.com"), "alice@example.com");
  });
});
