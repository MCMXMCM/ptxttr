import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  profileWebsiteURL,
  renderProfileHeroMetadataHTML,
  renderProfileHeroWebsiteHTML,
  renderProfileIdentHTML,
  renderProfilePaymentHTML,
} from "./profile-render.js";

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

  it("shows a website with any other scheme as plain text instead of a link", () => {
    const html = renderProfileHeroWebsiteHTML("javascript:alert(document.cookie)");
    assert.doesNotMatch(html, /<a\b/);
    assert.doesNotMatch(html, /href=/);
    assert.match(html, /<span class="profile-website-value">javascript:alert\(document\.cookie\)<\/span>/);
  });

  it("escapes markup in a website that is not linked", () => {
    const html = renderProfileHeroWebsiteHTML("data:text/html,<script>alert(1)</script>");
    assert.doesNotMatch(html, /<a\b|<script/);
    assert.match(html, /data:text\/html,&lt;script&gt;alert\(1\)&lt;\/script&gt;/);
  });
});

describe("profileWebsiteURL", () => {
  it("keeps http and https links and adds https to bare domains", () => {
    assert.equal(profileWebsiteURL("https://example.com/a"), "https://example.com/a");
    assert.equal(profileWebsiteURL(" http://example.com "), "http://example.com");
    assert.equal(profileWebsiteURL("HTTPS://Example.com"), "HTTPS://Example.com");
    assert.equal(profileWebsiteURL("example.com/path"), "https://example.com/path");
    assert.equal(profileWebsiteURL(""), "");
  });

  it("refuses every other scheme, however it is spelled", () => {
    for (const website of [
      "javascript:alert(1)",
      "JavaScript:alert(1)",
      "java\tscript:alert(1)",
      "\u0001javascript:alert(1)",
      "data:text/html,<script>alert(1)</script>",
      "vbscript:msgbox(1)",
      "file:///etc/passwd",
      "mailto:me@example.com",
    ]) {
      assert.equal(profileWebsiteURL(website), "", JSON.stringify(website));
    }
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
