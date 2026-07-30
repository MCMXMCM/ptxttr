const STORAGE_KEY = "ptxt_note_expansion_v1";

function readExpandedIDs() {
  try {
    const raw = sessionStorage.getItem(STORAGE_KEY);
    if (!raw) return new Set();
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return new Set();
    return new Set(parsed.map((id) => String(id || "").trim().toLowerCase()).filter(Boolean));
  } catch {
    return new Set();
  }
}

function writeExpandedIDs(set) {
  sessionStorage.setItem(STORAGE_KEY, JSON.stringify([...set]));
}

/** Remember that the user expanded this note in the feed before opening thread. */
export function markNoteExpanded(eventID) {
  const id = String(eventID || "").trim().toLowerCase();
  if (!/^[0-9a-f]{64}$/.test(id)) return;
  const set = readExpandedIDs();
  set.add(id);
  writeExpandedIDs(set);
}

/** Cleared when leaving thread so feed cards return to truncated. */
export function clearNoteExpansion() {
  sessionStorage.removeItem(STORAGE_KEY);
}

function truncatableContentForNote(noteEl) {
  if (!(noteEl instanceof Element)) return null;
  // Feed/focus notes use `.note-content`; never expand `.reply-content` in the
  // linear reply list — that breaks ascii reply layout before enhancement.
  return noteEl.querySelector(":scope .note-content, :scope .thread-tree-text");
}

/** Clears view-more state so truncation can be re-measured after content changes. */
export function resetTruncatableViewMore(el) {
  if (!(el instanceof Element)) return;
  el.classList.remove("is-collapsed");
  el.style.maxHeight = "";
  delete el.dataset.ptxtViewMoreBound;
  const next = el.nextElementSibling;
  if (next?.matches?.("button.view-more")) next.remove();
}

/** Apply feed carry-over expansion to matching note shells in thread (or feed). */
export function applyCarriedNoteExpansion(root = document) {
  const expanded = readExpandedIDs();
  if (expanded.size === 0) return;
  root.querySelectorAll(".note[id^='note-'], .comment[id^='note-']").forEach((noteEl) => {
    const id = noteEl.id.replace(/^note-/, "").toLowerCase();
    if (!expanded.has(id)) return;
    const content = truncatableContentForNote(noteEl);
    if (content) resetTruncatableViewMore(content);
  });
}
