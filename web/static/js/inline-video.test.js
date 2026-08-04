import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { prepareInlineVideo } from "./inline-video.js";

class FakeVideo {
  constructor() {
    this.attributes = new Map();
    this.autoplay = false;
    this.dataset = {};
    this.listeners = [];
    this.ownerDocument = null;
    this.paused = true;
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

  dispatch(type) {
    this.listeners.filter((listener) => listener.type === type).forEach((listener) => listener.handler());
  }
}

globalThis.HTMLVideoElement = FakeVideo;

describe("prepareInlineVideo", () => {
  it("keeps idle feed videos from competing with active playback", () => {
    const video = new FakeVideo();

    prepareInlineVideo(video);

    assert.equal(video.playsInline, true);
    assert.equal(video.getAttribute("playsinline"), "");
    assert.equal(video.getAttribute("webkit-playsinline"), "");
    assert.equal(video.preload, "metadata");
    assert.equal(video.getAttribute("preload"), "metadata");

    video.paused = false;
    video.dispatch("play");

    assert.equal(video.preload, "auto");
    assert.equal(video.getAttribute("preload"), "auto");
  });

  it("pauses other playing media and binds playback coordination once", () => {
    const video = new FakeVideo();
    const other = { paused: false, pauseCalls: 0, pause() { this.pauseCalls += 1; } };
    video.ownerDocument = { querySelectorAll: () => [video, other] };

    prepareInlineVideo(video);
    prepareInlineVideo(video);
    video.paused = false;
    video.dispatch("play");

    assert.equal(other.pauseCalls, 1);
    assert.equal(video.listeners.filter((listener) => listener.type === "play").length, 1);
    assert.equal(video.listeners.filter((listener) => listener.type === "error").length, 1);
  });
});
