import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  ASCII_WIDTH_DESKTOP_COOKIE_NAME,
  asciiWidthCookie,
  asciiWidthCookieNameForViewport,
  asciiWidthFromCookie,
} from "./ascii-width-hint.js";

describe("ascii-width-hint", () => {
  it("serializes bounded measured widths for server-rendered documents", () => {
    assert.equal(
      asciiWidthCookie(54),
      "ptxt_ascii_w=54; Path=/; Max-Age=31536000; SameSite=Lax",
    );
    assert.match(asciiWidthCookie(58.9, true), /^ptxt_ascii_w=58;.*; Secure$/);
    assert.match(
      asciiWidthCookie(68, false, ASCII_WIDTH_DESKTOP_COOKIE_NAME),
      /^ptxt_ascii_w_desktop=68;/,
    );
    assert.equal(asciiWidthCookie(31), "");
    assert.equal(asciiWidthCookie(161), "");
  });

  it("reads only bounded exact-width cookies", () => {
    assert.equal(asciiWidthFromCookie("a=1; ptxt_ascii_w=58; b=2"), 58);
    assert.equal(asciiWidthFromCookie("ptxt_ascii_w=999"), 0);
    assert.equal(
      asciiWidthFromCookie("ptxt_ascii_w=45; ptxt_ascii_w_desktop=68", ASCII_WIDTH_DESKTOP_COOKIE_NAME),
      68,
    );
    assert.equal(asciiWidthFromCookie(""), 0);
  });

  it("keeps compact and desktop measurements in separate cookies", () => {
    assert.equal(asciiWidthCookieNameForViewport(1023), "ptxt_ascii_w");
    assert.equal(asciiWidthCookieNameForViewport(1024), ASCII_WIDTH_DESKTOP_COOKIE_NAME);
  });
});
