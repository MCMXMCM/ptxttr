import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  retroLoaderActivityWindow,
  retroLoaderProgressState,
  retroLoaderProgressText,
  retroLoaderShouldQuiet,
  retroLoaderStatusWindow,
} from "./retro-loader.js";

describe("retro loader progress", () => {
  it("renders an initial progress bar state", () => {
    const state = retroLoaderProgressState({
      startedAt: 1000,
      now: 1000,
      progressWidth: 30,
    });
    const text = retroLoaderProgressText({
      units: state.units,
      percent: state.percent,
      progressWidth: 30,
    });
    assert.match(text, /^[█░]+ \d+%$/);
    assert.equal(state.percent, 0);
  });

  it("advances while pending without reaching 100 percent early", () => {
    const state = retroLoaderProgressState({
      startedAt: 0,
      now: 5000,
      progressWidth: 30,
    });
    assert.ok(state.percent > 0);
    assert.ok(state.percent < 100);
    assert.ok(state.units < 30);
  });

  it("continues advancing after an explicit progress milestone", () => {
    const initial = retroLoaderProgressState({
      startedAt: 1000,
      now: 1000,
      progressWidth: 30,
      explicitPercent: 18,
      explicitPercentAt: 1000,
    });
    const advanced = retroLoaderProgressState({
      startedAt: 1000,
      now: 5000,
      progressWidth: 30,
      explicitPercent: 18,
      explicitPercentAt: 1000,
    });
    assert.equal(initial.percent, 18);
    assert.ok(advanced.percent > initial.percent);
    assert.ok(advanced.percent <= 84);
    assert.ok(advanced.units > initial.units);
  });

  it("uses each explicit milestone as the new progress floor", () => {
    const state = retroLoaderProgressState({
      startedAt: 1000,
      now: 1400,
      progressWidth: 30,
      explicitPercent: 68,
      explicitPercentAt: 1400,
    });
    assert.equal(state.percent, 68);
  });

  it("snaps to 100 percent on completion", () => {
    const state = retroLoaderProgressState({
      startedAt: 0,
      now: 5000,
      progressWidth: 30,
      isComplete: true,
    });
    assert.equal(state.percent, 100);
    assert.equal(state.units, 30);
  });
});

describe("retro loader activity", () => {
  it("keeps only the latest activity window", () => {
    assert.deepEqual(
      retroLoaderActivityWindow(["one", "two", "three", "four", "five"], 4),
      ["two", "three", "four", "five"],
    );
  });

  it("reveals status lines over time and appends completion message", () => {
    const pending = retroLoaderStatusWindow({
      statusMessages: ["one", "two", "three"],
      startedAt: 0,
      now: 700,
      reduceMotion: false,
      windowSize: 4,
    });
    assert.deepEqual(pending, ["one", "two"]);

    const complete = retroLoaderStatusWindow({
      statusMessages: ["one", "two", "three"],
      completionMessage: "done.",
      startedAt: 0,
      now: 1200,
      reduceMotion: false,
      isComplete: true,
      windowSize: 4,
    });
    assert.deepEqual(complete, ["one", "two", "three", "done."]);
  });

  it("shows an explicitly reached stage immediately", () => {
    const pending = retroLoaderStatusWindow({
      statusMessages: ["one", "two", "profile details loaded"],
      explicitStatusMessage: "profile details loaded",
      startedAt: 0,
      now: 10,
      reduceMotion: false,
      windowSize: 4,
    });
    assert.deepEqual(pending, ["one", "profile details loaded"]);
  });

  it("goes quiet after the configured threshold while still pending", () => {
    assert.equal(retroLoaderShouldQuiet({
      startedAt: 0,
      now: 1499,
      quietAfterMs: 1500,
      isComplete: false,
    }), false);
    assert.equal(retroLoaderShouldQuiet({
      startedAt: 0,
      now: 1500,
      quietAfterMs: 1500,
      isComplete: false,
    }), true);
    assert.equal(retroLoaderShouldQuiet({
      startedAt: 0,
      now: 5000,
      quietAfterMs: 1500,
      isComplete: true,
    }), false);
  });
});
