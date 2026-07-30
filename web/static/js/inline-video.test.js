import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { prepareInlineVideo } from "./inline-video.js";

class FakeVideo {
  constructor() {
    this.attributes = new Map();
    this.listeners = [];
    this.playsInline = false;
    this.preload = "";
  }

  setAttribute(name, value) {
    this.attributes.set(name, value);
  }

  getAttribute(name) {
    return this.attributes.get(name);
  }

  addEventListener(type, handler, options) {
    this.listeners.push({ type, handler, options });
  }
}

globalThis.HTMLVideoElement = FakeVideo;

describe("prepareInlineVideo", () => {
  it("loads enough video data to display a preview frame before playback", () => {
    const video = new FakeVideo();

    prepareInlineVideo(video);

    assert.equal(video.playsInline, true);
    assert.equal(video.getAttribute("playsinline"), "");
    assert.equal(video.getAttribute("webkit-playsinline"), "");
    assert.equal(video.preload, "auto");
    assert.equal(video.getAttribute("preload"), "auto");
  });
});
