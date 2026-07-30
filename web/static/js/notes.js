import { markNoteExpanded, resetTruncatableViewMore } from "./note-expansion.js";

const collapsedHeight = 140;
export const interactiveSelector = "a, button, input, textarea, select, summary, [contenteditable='true']";
/** Feed / thread / tree: clicks on inline previews or native media controls must not open the note thread. */
export const embeddedMediaSelector =
  "video, audio, .note-media-preview, .note-media-drawer, [data-note-image-mount], [data-thread-tree-media-mount]";

function bindViewMoreButton(content) {
  const existing = content.nextElementSibling?.matches?.("button.view-more")
    ? content.nextElementSibling
    : null;
  if (existing?.dataset.ptxtViewMoreBound === "1") return;
  const button = existing ? existing.cloneNode(true) : document.createElement("button");
  button.type = "button";
  button.className = "link-button view-more";
  button.dataset.ptxtViewMoreBound = "1";
  button.textContent = "view more";
  button.addEventListener("click", () => {
    const note = content.closest(".note, .comment");
    const id = note?.id?.replace(/^note-/, "");
    if (id) markNoteExpanded(id);
    resetTruncatableViewMore(content);
  });
  if (existing) {
    existing.replaceWith(button);
    return;
  }
  content.insertAdjacentElement("afterend", button);
}

function addViewMore(content) {
  if (content.dataset.ptxtViewMoreBound === "1") {
    if (content.classList.contains("is-collapsed")) {
      bindViewMoreButton(content);
    }
    return;
  }
  if (content.scrollHeight <= collapsedHeight + 8) return;
  content.dataset.ptxtViewMoreBound = "1";
  content.classList.add("is-collapsed");
  content.style.maxHeight = `${collapsedHeight}px`;
  bindViewMoreButton(content);
}

export { resetTruncatableViewMore } from "./note-expansion.js";

/** Re-measure after DOM changes; call `resetTruncatableViewMore` first if content was rebuilt. */
export function applyTruncatableViewMore(el) {
  if (!(el instanceof Element)) return;
  addViewMore(el);
}

export function initViewMore(root = document) {
  root.querySelectorAll(".note-content").forEach((content) => {
    if (content.closest(".ascii-card")) return;
    addViewMore(content);
  });
  root.querySelectorAll(".thread-tree-text").forEach((content) => {
    addViewMore(content);
  });
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", () => {
    initViewMore();
  });
} else {
  initViewMore();
}
