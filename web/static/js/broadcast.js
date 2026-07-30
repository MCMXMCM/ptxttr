import { planPublishTargets, publishSignedEvent } from "./publish.js";
import { pendingPublishStatus, showPublishStatusSheet } from "./publish-status.js";

const inFlightBroadcasts = new Set();
let broadcastDelegatesBound = false;

function eventFromContainer(container) {
  try {
    const parsed = JSON.parse(String(container?.dataset?.asciiEvent || ""));
    return parsed && typeof parsed === "object" ? parsed : null;
  } catch {
    return null;
  }
}

function canBroadcast(event) {
  const kind = Number(event?.kind || 0);
  return Boolean(
    event?.id &&
    event?.pubkey &&
    event?.sig &&
    [1, 6, 1068].includes(kind),
  );
}

function setButtonsDisabled(noteId, disabled) {
  document.querySelectorAll(`[data-broadcast-event][data-note-id="${noteId}"]`).forEach((button) => {
    if (button instanceof HTMLButtonElement) button.disabled = disabled;
  });
}

async function publishBroadcast(container) {
  const event = eventFromContainer(container);
  if (!canBroadcast(event)) throw new Error("This note cannot be broadcast.");
  const noteId = String(event.id || "").toLowerCase();
  if (!noteId || inFlightBroadcasts.has(noteId)) return null;
  inFlightBroadcasts.add(noteId);
  setButtonsDisabled(noteId, true);
  try {
    const plannedRelays = await planPublishTargets(event).catch(() => []);
    const pendingState = pendingPublishStatus({
      phaseTitle: "Broadcasting to relays",
      statusMessage: "Preparing relay broadcast...",
      plannedRelays,
      completionMessage: "broadcast complete.",
    });
    showPublishStatusSheet(null, { title: "Broadcast status", initialState: pendingState });
    const payload = await publishSignedEvent(event);
    showPublishStatusSheet(payload, { title: "Broadcast status", initialState: pendingState });
    return payload;
  } finally {
    inFlightBroadcasts.delete(noteId);
    setButtonsDisabled(noteId, false);
  }
}

export function bindBroadcastDelegates() {
  if (broadcastDelegatesBound || typeof document === "undefined") return;
  broadcastDelegatesBound = true;
  document.addEventListener("click", (event) => {
    const button = event.target.closest("[data-broadcast-event]");
    if (!button) return;
    event.preventDefault();
    event.stopPropagation();
    const container = button.closest("[data-ascii-kind]");
    if (!container) return;
    void publishBroadcast(container).catch((error) => {
      window.alert(error instanceof Error ? error.message : "Broadcast failed.");
    });
  });
}
