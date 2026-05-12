import { DEFAULT_RETRY_ATTEMPTS, sleepBackoff } from "./backoff.js";
import { fetchWithSession, getSession, normalizedPubkey, recordPublishedAt, selectedRelays } from "./session.js";
import { activeSignerState, signEventDraft } from "./signer.js";

const KIND_BOOKMARK_LIST = 10003;
const bookmarkState = {
  pubkey: "",
  entries: new Map(),
  loaded: false,
  loading: null,
};

/** Serialize bookmark writes so parallel clicks cannot interleave list state. */
let bookmarkWriteChain = Promise.resolve();

function runSerializedBookmarkWrite(fn) {
  const prev = bookmarkWriteChain;
  let done;
  bookmarkWriteChain = new Promise((resolve) => {
    done = resolve;
  });
  return prev
    .catch(() => {
      /* keep queue moving if a prior bookmark write failed */
    })
    .then(fn)
    .finally(() => {
      done();
    });
}

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

const RETRIABLE_PUBLISH_STATUSES = new Set([502, 503, 504, 429]);

export async function publishSignedEvent(event) {
  for (let attempt = 0; attempt < DEFAULT_RETRY_ATTEMPTS; attempt++) {
    let response;
    try {
      response = await fetchWithSession("/api/events", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          event,
          relays: selectedRelays(),
        }),
      });
    } catch (err) {
      if (attempt + 1 >= DEFAULT_RETRY_ATTEMPTS) {
        throw err instanceof Error ? err : new Error("Publish failed.");
      }
      await sleepBackoff(attempt, 200, 200);
      continue;
    }
    let payload = {};
    try {
      payload = await response.json();
    } catch {
      payload = {};
    }
    if (response.ok) {
      recordPublishedAt();
      return payload;
    }
    const retriable = RETRIABLE_PUBLISH_STATUSES.has(response.status);
    const fallback = response.status === 502 ? "No relay accepted this event." : "Publish failed.";
    const err = new Error(payload.error || fallback);
    if (!retriable || attempt + 1 >= DEFAULT_RETRY_ATTEMPTS) {
      throw err;
    }
    await sleepBackoff(attempt, 200, 200);
  }
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
  // Relays are sent as X-Ptxt-Relays via fetchWithSession.
  bookmarkState.loading = fetchWithSession("/api/bookmarks")
    .then(async (response) => {
      if (!response.ok) throw new Error("bookmark list request failed");
      const payload = await response.json();
      bookmarkState.entries = entriesFromPayload(payload);
      bookmarkState.loaded = true;
      return bookmarkState.entries;
    })
    .finally(() => {
      bookmarkState.loading = null;
    });
  return bookmarkState.loading;
}

function buildBookmarkTags(entries) {
  const tags = [];
  entries.forEach((relay, id) => {
    if (relay) {
      tags.push(["e", id, relay]);
      return;
    }
    tags.push(["e", id]);
  });
  return tags;
}

async function toggleBookmark(noteID) {
  return runSerializedBookmarkWrite(() => toggleBookmarkOnce(noteID));
}

async function toggleBookmarkOnce(noteID) {
  const signer = activeSignerState();
  if (!signer.isLoggedIn || !signer.canSign) {
    throw new Error("A signing-capable login is required.");
  }
  const pubkey = normalizedPubkey();
  if (!pubkey) throw new Error("Log in first to manage bookmarks.");
  await loadBookmarks(pubkey);
  const hadBookmark = bookmarkState.entries.has(noteID);
  const previousRelay = bookmarkState.entries.get(noteID) || "";
  if (hadBookmark) {
    bookmarkState.entries.delete(noteID);
  } else {
    bookmarkState.entries.set(noteID, "");
  }
  const draft = {
    kind: KIND_BOOKMARK_LIST,
    created_at: Math.floor(Date.now() / 1000),
    tags: buildBookmarkTags(bookmarkState.entries),
    content: "",
  };
  try {
    const signed = await signEventDraft(draft, getSession());
    await publishSignedEvent(signed);
  } catch (error) {
    if (hadBookmark) {
      bookmarkState.entries.set(noteID, previousRelay);
    } else {
      bookmarkState.entries.delete(noteID);
    }
    throw error;
  }
}

function bindBookmarkActions(root = document) {
  if (!root || root._ptxtBookmarkBound) return;
  root._ptxtBookmarkBound = true;
  root.addEventListener("click", async (event) => {
    const trigger = event.target.closest("[data-bookmark-toggle][data-note-id]");
    if (!(trigger instanceof HTMLButtonElement)) return;
    event.preventDefault();
    event.stopPropagation();
    const noteID = String(trigger.dataset.noteId || "");
    if (!/^[0-9a-f]{64}$/i.test(noteID)) return;
    if (trigger.dataset.loading === "1") return;
    const previousLabel = trigger.textContent;
    trigger.dataset.loading = "1";
    trigger.disabled = true;
    trigger.textContent = "[saving...]";
    try {
      await toggleBookmark(noteID.toLowerCase());
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

window.addEventListener("ptxt:session", () => {
  resetBookmarkState();
  void syncBookmarkState(document);
});

window.addEventListener("ptxt:relays", () => {
  resetBookmarkState();
  void syncBookmarkState(document);
});
