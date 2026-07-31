import test from "node:test";
import assert from "node:assert/strict";
import preload from "./preload.cjs";

const { createTrackpadHistoryGesture, trackpadHistoryAction } = preload;

function wheel(deltaX, deltaY = 0, extras = {}) {
  return { deltaMode: 0, deltaX, deltaY, ...extras };
}

test("precise horizontal wheel deltas accumulate into one history action", () => {
  const gesture = createTrackpadHistoryGesture();
  assert.equal(trackpadHistoryAction(gesture, wheel(-35), 1_000), null);
  assert.equal(gesture.claimed, true);
  assert.equal(trackpadHistoryAction(gesture, wheel(-50), 1_025), "back");
  assert.equal(trackpadHistoryAction(gesture, wheel(-80), 1_050), null);
  assert.equal(trackpadHistoryAction(gesture, wheel(35), 1_500), null);
  assert.equal(trackpadHistoryAction(gesture, wheel(50), 1_525), "forward");
});

test("momentum reversal cannot trigger another action in the same gesture", () => {
  const gesture = createTrackpadHistoryGesture();
  assert.equal(trackpadHistoryAction(gesture, wheel(-90), 1_000), "back");
  assert.equal(trackpadHistoryAction(gesture, wheel(100), 1_040), null);
  assert.equal(trackpadHistoryAction(gesture, wheel(100), 1_400), "forward");
});

test("vertical, modified, and non-pixel wheel input never claims history", () => {
  for (const input of [
    wheel(20, 60),
    wheel(-200, 0, { deltaMode: 1 }),
    wheel(-200, 0, { shiftKey: true }),
  ]) {
    const gesture = createTrackpadHistoryGesture();
    assert.equal(trackpadHistoryAction(gesture, input, 1_000), null);
    assert.equal(gesture.claimed, false);
  }
});

test("a vertical event is not blocked after a horizontal gesture", () => {
  const gesture = createTrackpadHistoryGesture();
  assert.equal(trackpadHistoryAction(gesture, wheel(-35), 1_000), null);
  assert.equal(gesture.claimed, true);
  assert.equal(trackpadHistoryAction(gesture, wheel(0, 40), 1_025), null);
  assert.equal(gesture.claimed, false);
});
