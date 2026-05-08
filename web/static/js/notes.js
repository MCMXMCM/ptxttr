import { withRelayParams } from "./session.js";

const collapsedHeight = 140;
const clickSelectCardSelector = ".note[data-ascii-select-href], .comment[data-ascii-select-href]";
const clickSelectReferenceSelector = "[data-ascii-ref-select-href]";
export const interactiveSelector = "a, button, input, textarea, select, summary, [contenteditable='true']";
/** Feed / thread / tree: clicks on inline previews or native media controls must not open the note thread. */
export const embeddedMediaSelector =
  "video, audio, .note-media-preview, .note-media-drawer, [data-note-image-mount], [data-thread-tree-media-mount]";
let pointerState = null;

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
    content.classList.remove("is-collapsed");
    content.style.maxHeight = "";
    button.remove();
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

/** Clears view-more state so `addViewMore` can re-run after replacing inner content (e.g. thread tree text). */
export function resetTruncatableViewMore(el) {
  if (!(el instanceof Element)) return;
  el.classList.remove("is-collapsed");
  el.style.maxHeight = "";
  delete el.dataset.ptxtViewMoreBound;
  const next = el.nextElementSibling;
  if (next?.matches?.("button.view-more")) next.remove();
}

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

function hasTextSelection() {
  const selection = window.getSelection();
  return Boolean(selection && !selection.isCollapsed && selection.toString().trim());
}

function isPrimaryActivation(event) {
  if (event.defaultPrevented) return false;
  if (event.button !== 0) return false;
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return false;
  return true;
}

function installCardSelectHandlers() {
  document.addEventListener("pointerdown", (event) => {
    if (event.button !== 0) return;
    const card = event.target.closest(clickSelectCardSelector);
    if (!card) return;
    pointerState = {
      card,
      x: event.clientX,
      y: event.clientY,
      moved: false,
    };
  });

  document.addEventListener("pointermove", (event) => {
    if (!pointerState) return;
    if (Math.abs(event.clientX - pointerState.x) > 6 || Math.abs(event.clientY - pointerState.y) > 6) {
      pointerState.moved = true;
    }
  });

  document.addEventListener("pointerup", () => {
    // Keep state until click fires so we can suppress drag-originated clicks.
    setTimeout(() => {
      pointerState = null;
    }, 0);
  });

  document.addEventListener("pointercancel", () => {
    pointerState = null;
  });

  document.addEventListener("click", (event) => {
    if (!isPrimaryActivation(event)) return;
    if (!(event.target instanceof Element)) return;
    if (event.target.closest(embeddedMediaSelector)) return;
    const referenced = event.target.closest(clickSelectReferenceSelector);
    if (referenced) {
      if (hasTextSelection()) return;
      if (pointerState && pointerState.moved) return;
      const href = withRelayParams(referenced.dataset.asciiRefSelectHref || "");
      if (!href) return;
      event.preventDefault();
      dispatchNavigate(href);
      return;
    }
    if (event.target.closest(interactiveSelector)) return;
    const card = event.target.closest(clickSelectCardSelector);
    if (!card) return;
    if (hasTextSelection()) return;
    if (pointerState && pointerState.card === card && pointerState.moved) return;
    const href = withRelayParams(card.dataset.asciiSelectHref || "");
    if (!href) return;
    dispatchNavigate(href);
  });
}

function dispatchNavigate(href) {
  window.dispatchEvent(new CustomEvent("ptxt:navigate", { detail: { href } }));
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", () => {
    initViewMore();
    installCardSelectHandlers();
  });
} else {
  initViewMore();
  installCardSelectHandlers();
}
