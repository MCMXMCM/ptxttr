import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { prepareInlineVideo } from "./inline-video.js";

class FakeVideo {
  constructor({ readyState = 0, paused = true, currentTime = 0 } = {}) {
    this.attributes = new Map();
    this.autoplay = false;
    this.dataset = {};
    this.listeners = [];
    this.ownerDocument = null;
    this.paused = paused;
    this.playsInline = false;
    this.preload = "";
    this.readyState = readyState;
    this.currentTime = currentTime;
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

  it("asks for the first frame once metadata has loaded", () => {
    const video = new FakeVideo();

    prepareInlineVideo(video);
    assert.equal(video.currentTime, 0);
    video.readyState = 1;
    video.dispatch("loadedmetadata");

    assert.equal(video.currentTime, 0.001);
  });

  it("asks right away when metadata is already loaded", () => {
    const video = new FakeVideo({ readyState: 1 });

    prepareInlineVideo(video);

    assert.equal(video.currentTime, 0.001);
  });

  it("leaves videos that already show a frame or are playing", () => {
    const withFrame = new FakeVideo({ readyState: 2 });
    const playing = new FakeVideo({ readyState: 1, paused: false });
    const watched = new FakeVideo({ readyState: 1, currentTime: 12 });

    [withFrame, playing, watched].forEach((video) => prepareInlineVideo(video));

    assert.equal(withFrame.currentTime, 0);
    assert.equal(playing.currentTime, 0);
    assert.equal(watched.currentTime, 12);
  });
});
