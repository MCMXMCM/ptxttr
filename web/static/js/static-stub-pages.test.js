import assert from "node:assert/strict";
import { beforeEach, describe, it } from "node:test";

globalThis.document = {
  documentElement: {
    dataset: {},
  },
};

import { renderStaticStubMainContent } from "./static-stub-pages.js";

beforeEach(() => {
  document.documentElement.dataset = {};
});

describe("static-stub-pages", () => {
  it("renders settings markup from the client bundle", () => {
    const html = renderStaticStubMainContent("/settings");
    assert.match(html, /<h1>Settings<\/h1>/);
    assert.match(html, /data-relay-preferences-form/);
    assert.match(html, /data-relay-edit-section hidden/);
    assert.match(html, /data-account-switcher-list/);
    assert.match(html, /data-desktop-storage hidden/);
  });

  it("renders relays markup from the client bundle", () => {
    const html = renderStaticStubMainContent("/relays");
    assert.match(html, /<h1>Relays<\/h1>/);
    assert.match(html, /data-relay-preferences-form/);
    assert.match(html, /data-relay-insight-effective/);
  });

  it("renders about markup from the client bundle", () => {
    const html = renderStaticStubMainContent("/about");
    assert.match(html, /<h1>About<\/h1>/);
    assert.match(html, /keys and signing stay in your browser/);
    assert.match(html, /no app-server dependency/);
    assert.match(html, /Nostr NIPs \(specs and implementations\)/);
  });

  it("renders login markup from the client bundle", () => {
    const html = renderStaticStubMainContent("/login");
    assert.match(html, /<h1>Login<\/h1>/);
    assert.match(html, /data-login-readonly/);
    assert.match(html, /data-signup-generate/);
    assert.match(html, /data-session-state/);
    assert.match(html, /Browser Extension \(NIP-07\)/);
  });

  it("renders a local-only desktop login without extension or YOLO copy", () => {
    document.documentElement.dataset.ptxtDesktopMode = "1";

    const html = renderStaticStubMainContent("/login");

    assert.match(html, /<h2>Nsec Login<\/h2>/);
    assert.match(html, /stored locally on this device/);
    assert.doesNotMatch(html, /Browser Extension|NIP-07|YOLO|Dangerous:/);
  });

  it("returns an empty string for unknown stub paths", () => {
    assert.equal(renderStaticStubMainContent("/nope"), "");
  });
});
