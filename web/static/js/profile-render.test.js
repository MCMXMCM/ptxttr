import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { renderProfileHeroMetadataHTML, renderProfileHeroWebsiteHTML, renderProfileIdentHTML, renderProfilePaymentHTML } from "./profile-render.js";

describe("renderProfileHeroMetadataHTML", () => {
  it("includes the metadata event link and published date", () => {
    const html = renderProfileHeroMetadataHTML({
      event_id: "a".repeat(64),
      created_at: 1719360000,
    });

    assert.match(html, /metadata from <a href="\/thread\/a{64}"/);
    assert.match(html, />aaaaaaaaaaaa</);
    assert.match(html, /<time datetime="2024-06">June 2024<\/time>/);
  });
});

describe("renderProfileHeroWebsiteHTML", () => {
  it("renders an inline website row without action buttons", () => {
    const html = renderProfileHeroWebsiteHTML("example.com/path");
    assert.match(html, /<p class="profile-website-line">/);
    assert.match(html, />↗</);
    assert.match(html, /<a href="https:\/\/example\.com\/path"/);
    assert.match(html, />example\.com\/path<\/a>/);
    assert.doesNotMatch(html, />\[copy\]</);
    assert.doesNotMatch(html, />\[pay\]</);
  });

  it("returns empty output when website is missing", () => {
    assert.equal(renderProfileHeroWebsiteHTML(""), "");
  });
});

describe("renderProfilePaymentHTML", () => {
  it("renders the inline lightning row", () => {
    const html = renderProfilePaymentHTML({ lud16: "user@example.com" });
    assert.match(html, /class="profile-payment-line"/);
    assert.match(html, />user@example\.com</);
    assert.match(html, /href="lightning:user@example\.com"/);
    assert.match(html, /class="link-button profile-payment-copy-icon"/);
    assert.match(html, />⧉</);
  });
});

describe("renderProfileIdentHTML", () => {
  it("includes bio and metadata when present", () => {
    const html = renderProfileIdentHTML({
      about: "hello world",
      website: "example.com/path",
      lud16: "user@example.com",
      event_id: "a".repeat(64),
      created_at: 1719360000,
    });

    assert.match(html, /<p>hello world<\/p>/);
    assert.doesNotMatch(html, /example\.com\/path/);
    assert.doesNotMatch(html, /user@example\.com/);
    assert.match(html, /profile-hero-metadata/);
    assert.match(html, /metadata from <a href="\/thread\/a{64}"/);
    assert.match(html, /<time datetime="2024-06">June 2024<\/time>/);
  });

  it("includes lightning metadata when present", () => {
    const html = renderProfileIdentHTML({
      about: "hello world",
      lud16: "user@example.com",
    });

    assert.match(html, /<p>hello world<\/p>/);
    assert.doesNotMatch(html, /profile-payment-line/);
  });

  it("uses lud06 when lud16 is absent and omits the pay link", () => {
    const html = renderProfilePaymentHTML({
      lud06: "lnurl1example",
    });

    assert.match(html, /lnurl1example/);
    assert.doesNotMatch(html, /href="lightning:/);
  });
});
