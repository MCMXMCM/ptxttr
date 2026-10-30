import { publishSignedEvent } from "./publish.js";
import { normalizedPubkey } from "./session.js";
import { activeSignerState } from "./signer.js";
import { fetchBookmarks } from "./relay-reads.js";
import { bookmarkEntries } from "./relay-utils.js";
import { updateReplaceableList } from "./replaceable-list.js";

export { publishSignedEvent };

const KIND_BOOKMARK_LIST = 10003;
const NEW_BOOKMARK_LIST_CONFIRM =
  "No existing bookmark list was found on your relays. Bookmarking now publishes a new list that contains only this note. Continue?";
const bookmarkState = {
  pubkey: "",
  entries: new Map(),
  loaded: false,
  loading: null,
};

function noteIDsInRoot(root = document) {
  const ids = [];
  const seen = new Set();
  root.querySelectorAll("[data-ascii-kind][id^='note-']").forEach((node) => {
    const id = node.id.replace(/^note-/, "");
    if (!id || seen.has(id)) return;
    seen.add(id);
    ids.push(id);
  });
  return ids;
}

function setBookmarkDecorations(root = document) {
  const pubkey = normalizedPubkey();
  const marks = bookmarkState.pubkey === pubkey ? bookmarkState.entries : new Map();
  root.querySelectorAll("[data-ascii-kind][id^='note-']").forEach((node) => {
    const id = node.id.replace(/^note-/, "");
    if (!id) {
      delete node.dataset.bookmarked;
      return;
    }
    node.dataset.bookmarked = marks.has(id) ? "1" : "0";
  });
}

/** Update bookmark toggle labels from `data-bookmarked` without re-rendering ASCII (preserves media playback). */
function syncBookmarkToggleLabels(root = document) {
  root.querySelectorAll("[data-bookmark-toggle][data-note-id]").forEach((node) => {
    if (!(node instanceof HTMLButtonElement)) return;
    const noteID = String(node.dataset.noteId || "").toLowerCase();
    if (!noteID) return;
    const noteEl = node.ownerDocument.getElementById(`note-${noteID}`);
    const bookmarked = noteEl?.dataset?.bookmarked === "1";
    node.textContent = bookmarked ? "[unbookmark]" : "[bookmark]";
  });
}

function entriesFromPayload(payload) {
  const out = new Map();
  const entries = Array.isArray(payload?.entries) ? payload.entries : [];
  entries.forEach((entry) => {
    const id = String(entry?.id || "").trim().toLowerCase();
    if (!/^[0-9a-f]{64}$/.test(id)) return;
    out.set(id, String(entry?.relay || "").trim());
  });
  return out;
}

async function loadBookmarks(pubkey) {
  if (!pubkey) {
    resetBookmarkState();
    return bookmarkState.entries;
  }
  if (bookmarkState.pubkey === pubkey && bookmarkState.loading) {
    return bookmarkState.loading;
  }
  if (bookmarkState.pubkey === pubkey && bookmarkState.loaded) {
    return bookmarkState.entries;
  }
  bookmarkState.pubkey = pubkey;
  // Relays are sent as X-Ptxt-Relays via fetchWithSession when falling back to server.
  bookmarkState.loading = fetchBookmarks(pubkey)
    .then(async (payload) => {
      bookmarkState.entries = entriesFromPayload(payload);
      bookmarkState.loaded = true;
      return bookmarkState.entries;
    })
    .finally(() => {
      bookmarkState.loading = null;
    });
  return bookmarkState.loading;
}

/**
 * Add or remove one bookmark. The list is refetched and edited in place, so
 * `a`/`t` tags, private (encrypted) entries and bookmarks past the display
 * limit survive the republish.
 */
async function setBookmarked(noteID, bookmarked) {
  const signer = activeSignerState();
  if (!signer.isLoggedIn || !signer.canSign) {
    throw new Error("A signing-capable login is required.");
  }
  const pubkey = normalizedPubkey();
  if (!pubkey) throw new Error("Log in first to manage bookmarks.");
  const { event } = await updateReplaceableList({
    pubkey,
    kind: KIND_BOOKMARK_LIST,
    tag: ["e", noteID],
    enabled: bookmarked,
    confirm: (current) => Boolean(current) || window.confirm(NEW_BOOKMARK_LIST_CONFIRM),
  });
  bookmarkState.pubkey = pubkey;
  bookmarkState.entries = entriesFromPayload({ entries: bookmarkEntries(event) });
  bookmarkState.loaded = true;
}

function bindBookmarkActions(root = document) {
  if (!root || root._ptxtBookmarkBound) return;
  root._ptxtBookmarkBound = true;
  root.addEventListener("click", async (event) => {
    const trigger = event.target.closest("[data-bookmark-toggle][data-note-id]");
    if (!(trigger instanceof HTMLButtonElement)) return;
    event.preventDefault();
    event.stopPropagation();
    const noteID = String(trigger.dataset.noteId || "").toLowerCase();
    if (!/^[0-9a-f]{64}$/.test(noteID)) return;
    if (trigger.dataset.loading === "1") return;
    const bookmarked = trigger.ownerDocument.getElementById(`note-${noteID}`)?.dataset?.bookmarked === "1";
    const previousLabel = trigger.textContent;
    trigger.dataset.loading = "1";
    trigger.disabled = true;
    trigger.textContent = "[saving...]";
    try {
      await setBookmarked(noteID, !bookmarked);
      setBookmarkDecorations(document);
      syncBookmarkToggleLabels(document);
    } catch (error) {
      window.alert(error instanceof Error ? error.message : "Bookmark update failed.");
      trigger.textContent = previousLabel;
    } finally {
      delete trigger.dataset.loading;
      trigger.disabled = false;
    }
  });
}

export async function syncBookmarkState(root = document) {
  const pubkey = normalizedPubkey();
  const hasNotes = noteIDsInRoot(root).length > 0;
  if (!pubkey || !hasNotes) {
    setBookmarkDecorations(root);
    syncBookmarkToggleLabels(root);
    return;
  }
  try {
    await loadBookmarks(pubkey);
  } catch {
    // Keep page usable even when bookmark sync fails.
  }
  setBookmarkDecorations(root);
  syncBookmarkToggleLabels(root);
}

export function initBookmarks(root = document) {
  bindBookmarkActions(root);
  void syncBookmarkState(root);
}

function resetBookmarkState() {
  bookmarkState.pubkey = "";
  bookmarkState.entries = new Map();
  bookmarkState.loaded = false;
}

if (typeof window !== "undefined") {
  window.addEventListener("ptxt:session", () => {
    resetBookmarkState();
    void syncBookmarkState(document);
  });

  window.addEventListener("ptxt:relays", () => {
    resetBookmarkState();
    void syncBookmarkState(document);
  });
}
