import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  mediaGridImageAspectRatio,
  mediaGridItemAspectRatio,
  mediaGridSignature,
  mediaGridVideoAspectRatio,
} from "./media-grid.js";

describe("mediaGridImageAspectRatio", () => {
  it("returns a CSS aspect-ratio value from natural dimensions", () => {
    assert.equal(mediaGridImageAspectRatio(1200, 800), "1200 / 800");
    assert.equal(mediaGridImageAspectRatio(1200.4, 799.6), "1200 / 800");
  });

  it("ignores missing or invalid dimensions", () => {
    assert.equal(mediaGridImageAspectRatio(0, 800), "");
    assert.equal(mediaGridImageAspectRatio(1200, 0), "");
    assert.equal(mediaGridImageAspectRatio(Number.NaN, 800), "");
  });
});

describe("mediaGridVideoAspectRatio", () => {
  it("returns a CSS aspect-ratio value from video metadata", () => {
    const video = { videoWidth: 1080, videoHeight: 1920 };
    assert.equal(mediaGridVideoAspectRatio(video), "1080 / 1920");
  });

  it("ignores non-video values and missing metadata", () => {
    assert.equal(mediaGridVideoAspectRatio(null), "");
    const video = { videoWidth: 0, videoHeight: 720 };
    assert.equal(mediaGridVideoAspectRatio(video), "");
  });
});

describe("mediaGridItemAspectRatio", () => {
  it("uses only declared metadata dimensions", () => {
    assert.equal(mediaGridItemAspectRatio({ width: 1200, height: 800 }), "1200 / 800");
    assert.equal(mediaGridItemAspectRatio({ naturalWidth: 1200, naturalHeight: 800 }), "");
    assert.equal(mediaGridItemAspectRatio({ width: 0, height: 800 }), "");
  });
});

describe("mediaGridSignature", () => {
  it("builds a stable signature for a media list", () => {
    assert.equal(mediaGridSignature([
      { type: "image", url: "https://cdn.example/a.jpg" },
      { type: "video", url: "https://cdn.example/b.mp4" },
    ]), "image:https://cdn.example/a.jpg|video:https://cdn.example/b.mp4");
  });

  it("returns an empty signature for missing media", () => {
    assert.equal(mediaGridSignature([]), "");
    assert.equal(mediaGridSignature(null), "");
  });
});
