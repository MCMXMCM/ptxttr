import { publishSignedEvent } from "./bookmarks.js";
import { planPublishTargets } from "./publish.js";
import { normalizedPubkey, normalizePubkey } from "./session.js";
import { signEventDraft } from "./signer.js";
import { pendingPublishStatus, showPublishStatusSheet } from "./publish-status.js";

const DELETABLE_KINDS = new Set([1, 6, 30023]);

export function canDeleteNote(container, session) {
  const viewer = normalizedPubkey(session);
  const author = normalizePubkey(container?.dataset?.replyPubkey || "");
  if (!viewer || !author || viewer !== author) return false;
  const kind = Number.parseInt(String(container?.dataset?.asciiEventKind || "1"), 10);
  return DELETABLE_KINDS.has(kind);
}

function noteElementsForID(noteID) {
  const id = String(noteID || "").trim().toLowerCase();
  if (!id) return [];
  const scope = document.querySelector("[data-nav-root]") || document;
  return [...scope.querySelectorAll(`#note-${id}, [data-reply-target-id="${id}"]`)].filter(
    (el, index, all) => all.indexOf(el) === index,
  );
}

/** Animate and remove all DOM shells for a deleted note id. */
export function removeNoteFromDOM(noteID) {
  const nodes = noteElementsForID(noteID);
  const targets = new Set();
  nodes.forEach((node) => {
    if (!(node instanceof HTMLElement)) return;
    const removeTarget = node.matches(".note, .comment, [data-thread-tree-note]")
      ? node
      : node.closest(".note, .comment, [data-thread-tree-note]");
    if (!(removeTarget instanceof HTMLElement) || targets.has(removeTarget)) return;
    targets.add(removeTarget);
    removeTarget.classList.add("ptxt-note-removing");
    let removed = false;
    const done = () => {
      if (removed) return;
      removed = true;
      removeTarget.remove();
    };
    removeTarget.addEventListener("transitionend", done, { once: true });
    window.setTimeout(done, 400);
  });
}

export async function publishNoteDeletion(noteID, { showStatus = true } = {}) {
  const id = String(noteID || "").trim().toLowerCase();
  if (!/^[0-9a-f]{64}$/.test(id)) {
    throw new Error("Invalid note id.");
  }
  const draft = {
    kind: 5,
    created_at: Math.floor(Date.now() / 1000),
    tags: [["e", id]],
    content: "",
  };
  const signed = await signEventDraft(draft);
  const plannedRelays = showStatus ? await planPublishTargets(signed).catch(() => []) : [];
  const pendingState = showStatus ? pendingPublishStatus({
    phaseTitle: "Broadcasting delete",
    statusMessage: "Preparing delete broadcast...",
    plannedRelays,
    completionMessage: "delete published.",
  }) : null;
  if (showStatus) showPublishStatusSheet(null, { title: "Delete publish status", initialState: pendingState });
  const payload = await publishSignedEvent(signed);
  removeNoteFromDOM(id);
  if (showStatus) showPublishStatusSheet(payload, { title: "Delete publish status", initialState: pendingState });
  return payload;
}
