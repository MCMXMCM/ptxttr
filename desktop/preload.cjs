// Sandboxed, context-isolated renderer hook for macOS fluid trackpad history.
// Electron's main-process `input-event` omits wheel deltas, while the DOM wheel
// event retains them. No renderer API or Node capability is exposed here.
const TRACKPAD_HISTORY_SWIPE_THRESHOLD = 80;
const TRACKPAD_HISTORY_SWIPE_RESET_MS = 280;

function createTrackpadHistoryGesture() {
  return { claimed: false, committed: false, deltaX: 0, direction: 0, lastAt: 0 };
}

function trackpadHistoryAction(gesture, input, now = Date.now()) {
  gesture.claimed = false;
  if (
    input?.deltaMode !== 0 ||
    input.shiftKey ||
    input.ctrlKey ||
    input.altKey ||
    input.metaKey
  ) {
    return null;
  }

  const deltaX = Number(input.deltaX) || 0;
  const deltaY = Number(input.deltaY) || 0;
  if (Math.abs(deltaX) < 1 || Math.abs(deltaX) <= Math.abs(deltaY) * 1.25) return null;

  const direction = Math.sign(deltaX);
  if (now - gesture.lastAt > TRACKPAD_HISTORY_SWIPE_RESET_MS) {
    gesture.claimed = false;
    gesture.committed = false;
    gesture.deltaX = 0;
    gesture.direction = direction;
  }
  gesture.lastAt = now;
  gesture.claimed = true;
  if (gesture.committed) return null;
  if (direction !== gesture.direction) {
    gesture.deltaX = 0;
    gesture.direction = direction;
  }

  gesture.deltaX += deltaX;
  if (Math.abs(gesture.deltaX) < TRACKPAD_HISTORY_SWIPE_THRESHOLD) return null;
  gesture.committed = true;
  // With macOS natural scrolling, a rightward finger swipe has negative X
  // wheel delta and maps to Back; a leftward swipe maps to Forward.
  return gesture.deltaX < 0 ? "back" : "forward";
}

if (typeof window !== "undefined") {
  const gesture = createTrackpadHistoryGesture();
  window.addEventListener("wheel", (event) => {
    const action = trackpadHistoryAction(gesture, event);
    // Claim a horizontal gesture from its first event so Chromium's native
    // history swiper cannot commit the same gesture a second time.
    if (gesture.claimed) event.preventDefault();
    if (action === "back") window.history.back();
    if (action === "forward") window.history.forward();
  }, { capture: true, passive: false });
}

module.exports = { createTrackpadHistoryGesture, trackpadHistoryAction };
