import { relayHintsFromNoteElement } from "./dom-relay-hints.js";
import { routeKind } from "./nav-routing.js";
import { threadPathNoteID } from "./thread-hydrate.js";

let pendingThreadTransition = null;

function clearSelectedMarker(note) {
  if (!(note instanceof HTMLElement)) return;
  note.classList.remove("ptxt-note-transition-selected");
}

function clearSelectedMarkers(root) {
  if (!(root instanceof HTMLElement)) return;
  root.classList.remove("ptxt-note-transition-selected");
  root.querySelectorAll?.(".ptxt-note-transition-selected").forEach((node) => {
    node.classList.remove("ptxt-note-transition-selected");
  });
}

function startTransitionBackdrop(note) {
  document.documentElement.classList.add("ptxt-note-transition-cleanup");
  document.documentElement.classList.add("ptxt-thread-route-transition");
  if (note instanceof HTMLElement) {
    note.classList.add("ptxt-note-transition-selected");
  }
}

function stopTransitionBackdrop(note) {
  document.documentElement.classList.remove("ptxt-note-transition-cleanup");
  document.documentElement.classList.remove("ptxt-thread-route-transition");
  clearSelectedMarker(note);
}

function transitionName(kind, noteID) {
  const suffix = String(noteID || "").trim().toLowerCase();
  return suffix ? `ptxt-note-${kind}-${suffix}` : `ptxt-note-${kind}`;
}

function clearTransitionName(node) {
  if (!(node instanceof HTMLElement)) return;
  if (node.dataset.ptxtViewTransitionName) {
    node.style.viewTransitionName = "";
    delete node.dataset.ptxtViewTransitionName;
  }
}

function applyTransitionName(node, name, kind = "") {
  if (!(node instanceof HTMLElement) || !name) return;
  node.style.viewTransitionName = name;
  node.dataset.ptxtViewTransitionName = name;
  if (kind) {
    node.dataset.ptxtViewTransitionKind = kind;
    if ("viewTransitionClass" in node.style) {
      node.style.viewTransitionClass = `ptxt-note-${kind}`;
    }
  }
}

function threadDestinationNote(root, noteID = "") {
  const id = String(noteID || "").trim().toLowerCase();
  if (!id || !root?.querySelector) return null;
  const selectors = [
    `#thread-focus > #note-${id}`,
    `#thread-focus > .comment#note-${id}`,
  ];
  for (const selector of selectors) {
    const node = root.querySelector(selector);
    if (node instanceof HTMLElement) return node;
  }
  return null;
}

function noteTransitionTargets(note) {
  if (!(note instanceof Element)) {
    return { avatar: null, author: null, meta: null, body: null };
  }
  const avatarHost =
    note.querySelector(":scope > .note-avatar, :scope > .comment-avatar, :scope .note-feed-avatar") || null;
  const body = note.querySelector(":scope > pre.ascii-card, :scope > pre.ascii-reply") || null;
  const author =
    note.querySelector(":scope .ascii-line-feed-header a[href^='/u/'], :scope .ascii-reply > .ascii-line:first-child a[href^='/u/'], :scope .hn-comhead a[href^='/u/']") ||
    null;
  return {
    avatar: avatarHost?.querySelector?.("img") || avatarHost,
    author,
    meta: author ? null : (
      note.querySelector(":scope .ascii-line-feed-header, :scope .ascii-reply > .ascii-line:first-child, :scope .hn-comhead") ||
      null
    ),
    body,
  };
}

export function clearThreadTransitionNames(root = document) {
  clearTransitionName(root);
  root.querySelectorAll?.("[data-ptxt-view-transition-name]").forEach((node) => {
    clearTransitionName(node);
  });
}

export function clearThreadTransitionArtifacts(root = document) {
  globalThis.document?.documentElement?.classList?.remove?.("ptxt-note-transition-cleanup");
  globalThis.document?.documentElement?.classList?.remove?.("ptxt-thread-route-transition");
  clearThreadTransitionNames(root);
  clearSelectedMarkers(root);
}

export function clearTransitionStateInRoot(root = document) {
  clearThreadTransitionNames(root);
  clearSelectedMarkers(root);
}

export function clearNoteTransitionNames(note) {
  if (!(note instanceof Element)) return;
  const targets = noteTransitionTargets(note);
  Object.values(targets).forEach((target) => clearTransitionName(target));
}

export function applyThreadTransitionNames(note, noteID = "") {
  const id = String(noteID || "").trim().toLowerCase();
  if (!id) return;
  const targets = noteTransitionTargets(note);
  for (const [kind, node] of Object.entries(targets)) {
    applyTransitionName(node, transitionName(kind, id), kind);
  }
}

function inferSourceList(card) {
  if (card?.closest?.("#user-panel-replies")) return "profile-replies";
  if (card?.closest?.("#user-panel-posts")) return "profile-posts";
  if (routeKind(window.location.pathname) === "thread") return "thread";
  return "feed";
}

function currentThreadSelectedNote(root = document) {
  return root.querySelector("#thread-focus > .note[id^='note-'], #thread-focus > .comment[id^='note-']") || null;
}

export function prepareThreadTransition(card, href) {
  const selectedNoteID = threadPathNoteID(href);
  if (!selectedNoteID || !(card instanceof Element)) return null;
  const note = card.closest(".note, .comment, [data-thread-tree-note]");
  if (!(note instanceof Element)) return null;
  const sourceRoute = routeKind(window.location.pathname) || "";
  // In-thread focus changes use relay-native re-render, not feed carry-over transitions.
  // Running the feed transition here briefly exposes the keepalive feed layer.
  if (sourceRoute === "thread") return null;
  const sourceList = inferSourceList(card);
  pendingThreadTransition = {
    href: String(href || ""),
    note,
    selectedNoteID,
    sourceRoute,
    sourceList,
    relayHints: relayHintsFromNoteElement(note),
    sharedElement: true,
  };
  applyThreadTransitionNames(note, selectedNoteID);
  return {
    selectedNoteID,
    sourceRoute,
    sourceList,
    relayHints: pendingThreadTransition.relayHints,
    sharedElement: true,
  };
}

export function prepareHistoryThreadBackTransition(root = document) {
  const note = currentThreadSelectedNote(root);
  const selectedNoteID = note?.id?.replace(/^note-/, "").toLowerCase() || "";
  if (!selectedNoteID) return null;
  pendingThreadTransition = {
    href: window.location.href,
    note,
    selectedNoteID,
    sourceRoute: "thread",
    sourceList: "thread",
    sharedElement: true,
    historyBack: true,
  };
  applyThreadTransitionNames(note, selectedNoteID);
  return {
    selectedNoteID,
    sourceRoute: "thread",
    sourceList: "thread",
    sharedElement: true,
    historyBack: true,
  };
}

function profileDestinationNote(root, noteID = "") {
  const id = String(noteID || "").trim().toLowerCase();
  if (!id || !root?.querySelector) return null;
  const selectors = [`#user-panel-posts #note-${id}`];
  for (const selector of selectors) {
    const node = root.querySelector(selector);
    if (node instanceof HTMLElement) return node;
  }
  return null;
}

export function prepareThreadToProfileTransition(source = document, href = "") {
  const sourceRoute = routeKind(window.location.pathname) || "";
  const note = sourceRoute === "thread"
    ? currentThreadSelectedNote(source)
    : source?.closest?.(".note, .comment, [data-thread-tree-note]");
  const selectedNoteID = note?.id?.replace(/^note-/, "").toLowerCase() || "";
  if (!selectedNoteID) return null;
  pendingThreadTransition = {
    href: String(href || window.location.href),
    note,
    selectedNoteID,
    sourceRoute,
    sourceList: inferSourceList(note),
    relayHints: relayHintsFromNoteElement(note),
    sharedElement: true,
  };
  applyThreadTransitionNames(note, selectedNoteID);
  return {
    selectedNoteID,
    sourceRoute,
    sourceList: pendingThreadTransition.sourceList,
    relayHints: pendingThreadTransition.relayHints,
    sharedElement: true,
    historyBack: false,
  };
}

export function applyDestinationProfileTransition(root = document, noteID = "") {
  const id = String(noteID || "").trim().toLowerCase();
  if (!id || !root?.querySelectorAll) return null;
  root.querySelectorAll(
    `#thread-focus #note-${id}, .ptxt-carried-profile-note#note-${id}, .ptxt-carried-thread-note#note-${id}`,
  ).forEach((node) => {
    if (node instanceof HTMLElement) clearNoteTransitionNames(node);
  });
  const note = profileDestinationNote(root, id);
  if (!note) return null;
  applyThreadTransitionNames(note, id);
  return note;
}

export function clearCarriedProfileTransitionNote(root = document) {
  if (!root?.querySelectorAll) return;
  root.querySelectorAll(".ptxt-carried-profile-note, .ptxt-carried-thread-note").forEach((node) => {
    if (node instanceof HTMLElement && !node.closest("#thread-focus")) {
      node.remove();
    }
  });
}

export function applyDestinationThreadTransition(root = document, noteID = "") {
  const id = String(noteID || "").trim().toLowerCase();
  if (!id || !root?.querySelectorAll) return null;
  root.querySelectorAll(`#feed #note-${id}`).forEach((node) => {
    if (node instanceof HTMLElement) clearNoteTransitionNames(node);
  });
  const note = threadDestinationNote(root, id);
  if (!note) return null;
  applyThreadTransitionNames(note, id);
  return note;
}

export function takeCarriedThreadNote(noteID = "") {
  const id = String(noteID || "").trim().toLowerCase();
  if (!pendingThreadTransition || pendingThreadTransition.selectedNoteID !== id) return null;
  const note = pendingThreadTransition.note;
  if (!(note instanceof Element)) return null;
  try {
    return note.cloneNode(true);
  } catch {
    return null;
  }
}

export function currentThreadTransition(noteID = "") {
  const id = String(noteID || "").trim().toLowerCase();
  if (!pendingThreadTransition || (id && pendingThreadTransition.selectedNoteID !== id)) return null;
  return { ...pendingThreadTransition };
}

export function clearThreadTransition(noteID = "") {
  if (!pendingThreadTransition) return;
  const id = String(noteID || "").trim().toLowerCase();
  if (id && pendingThreadTransition.selectedNoteID !== id) return;
  const note = pendingThreadTransition.note;
  clearTransitionName(note);
  clearSelectedMarker(note);
  if (note instanceof Element) {
    const targets = noteTransitionTargets(note);
    Object.values(targets).forEach((target) => clearTransitionName(target));
  }
  clearThreadTransitionArtifacts(document);
  pendingThreadTransition = null;
}

export async function runNoteViewTransition(transition, update, options = {}) {
  if (typeof update !== "function") return;
  const awaitUpdate = options.awaitUpdate !== false;
  const reducedMotion = globalThis.matchMedia?.("(prefers-reduced-motion: reduce)")?.matches === true;
  if (reducedMotion || !transition?.sharedElement || typeof document.startViewTransition !== "function") {
    await update();
    return;
  }
  const sourceNote = pendingThreadTransition?.note instanceof HTMLElement ? pendingThreadTransition.note : null;
  startTransitionBackdrop(sourceNote);
  let updateResult;
  let updatePromise = null;
  let updateError = null;
  const viewTransition = document.startViewTransition(() => {
    document.documentElement.classList.remove("ptxt-note-transition-cleanup");
    try {
      updateResult = update();
      updatePromise = Promise.resolve(updateResult).catch((error) => {
        updateError = error;
        throw error;
      });
    } catch (error) {
      updateError = error;
      throw error;
    }
    if (awaitUpdate) {
      return updatePromise;
    }
    return undefined;
  });
  viewTransition.ready?.catch?.(() => {});
  viewTransition.updateCallbackDone?.catch?.(() => {});
  try {
    await viewTransition.finished;
    if (!awaitUpdate && updatePromise) {
      await updatePromise;
    }
  } catch {
    // Ignore transition failures; the DOM update already ran.
  } finally {
    stopTransitionBackdrop(sourceNote);
  }
}
