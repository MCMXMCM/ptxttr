import { normalizedPubkey } from "./session.js";
import { activeSignerState, signEventDraft } from "./signer.js";
import { publishSignedEvent } from "./bookmarks.js";
import { clearThreadSnapshots } from "./thread-cache.js";
import { invalidateThreadFragmentPrefetch } from "./prefetch.js";

let delegated = false;
const reactionRequestSequence = new WeakMap();
let reactionsModalSeq = 0;

function findNoteShell(el) {
  const node = el?.closest?.(".note, .comment.thread-focus-parent, .note.thread-focus-selected, .comment");
  return node;
}

function formatThousandsSpaced(n, minLen) {
  let v = Math.max(0, Math.floor(Number(n) || 0));
  let s = String(v);
  if (s.length <= 3) {
    while (s.length < minLen) s = ` ${s}`;
    return s;
  }
  const parts = [];
  while (s.length > 3) {
    parts.unshift(s.slice(-3));
    s = s.slice(0, -3);
  }
  if (s) parts.unshift(s);
  const joined = parts.join(" ");
  let out = joined;
  while (out.length < minLen) out = ` ${out}`;
  return out;
}

function hex64Lower(s) {
  const t = String(s || "").trim();
  return /^[0-9a-fA-F]{64}$/.test(t) ? t.toLowerCase() : t;
}

/** Same note can render in multiple shells (e.g. root as focus parent vs root thread card); sync by target id. */
function shellsForReactionTarget(noteEl) {
  const tid = hex64Lower(noteEl?.dataset?.replyTargetId || "");
  if (!tid) return noteEl ? [noteEl] : [];
  const scope = document.querySelector("[data-nav-root]") || document.body;
  const out = [];
  scope.querySelectorAll("[data-reply-target-id]").forEach((el) => {
    if (hex64Lower(el.dataset.replyTargetId || "") === tid) out.push(el);
  });
  return out.length ? out : noteEl ? [noteEl] : [];
}

function buildReactionDraft(noteEl, polarity) {
  const targetId = hex64Lower(noteEl.dataset.replyTargetId || "");
  const author = hex64Lower(noteEl.dataset.replyPubkey || "");
  const kindStr = String(noteEl.dataset.asciiEventKind || "1").trim() || "1";
  const relay = String(noteEl.dataset.asciiRelay || "").trim();
  const tags = [];
  if (relay) {
    tags.push(["e", targetId, relay, author]);
  } else {
    tags.push(["e", targetId, "", author]);
  }
  if (relay) {
    tags.push(["p", author, relay]);
  } else {
    tags.push(["p", author]);
  }
  tags.push(["k", kindStr]);
  const content = polarity === "up" ? "+" : "-";
  return {
    kind: 7,
    content,
    tags,
    created_at: Math.floor(Date.now() / 1000),
  };
}

/** Same note in multiple shells: max total, last +/- viewer in document order (see shellsForReactionTarget). */
function aggregateReactionShellState(shells) {
  let total = 0;
  let prev = "";
  for (const shell of shells) {
    const parsed = Number.parseInt(String(shell.dataset.asciiReactionTotal || "0"), 10);
    if (Number.isFinite(parsed) && parsed > total) total = parsed;
    const v = String(shell.dataset.asciiReactionViewer || "").trim();
    if (v === "+" || v === "-") prev = v;
  }
  return { total, prev };
}

function optimisticBump(noteEl, nextPolarity) {
  const shells = shellsForReactionTarget(noteEl);
  if (!shells.length) return;
  const { total: baseTotal, prev } = aggregateReactionShellState(shells);
  let total = baseTotal;
  if (prev === "" && (nextPolarity === "+" || nextPolarity === "-")) total += 1;
  for (const shell of shells) {
    shell.dataset.asciiReactionViewer = nextPolarity;
    shell.dataset.asciiReactionTotal = String(total);
  }
}

function rollback(noteEl, snap) {
  for (const shell of shellsForReactionTarget(noteEl)) {
    shell.dataset.asciiReactionViewer = snap.viewer;
    shell.dataset.asciiReactionTotal = snap.total;
  }
}

function nextReactionSequence(noteEl) {
  const next = (reactionRequestSequence.get(noteEl) || 0) + 1;
  reactionRequestSequence.set(noteEl, next);
  return next;
}

function latestReactionSequence(noteEl) {
  return reactionRequestSequence.get(noteEl) || 0;
}

function refreshAsciiAround(noteEl) {
  void import("./ascii.js").then((m) => {
    const nav = document.querySelector("[data-nav-root]");
    m.refreshAscii(nav || noteEl);
  });
}

async function publishReaction(noteEl, polarity) {
  const me = normalizedPubkey();
  if (!me) {
    window.alert("Sign in to react.");
    return;
  }
  const signer = activeSignerState();
  if (!signer?.canSign) {
    window.alert("Use a signing-capable session to react.");
    return;
  }
  const draft = buildReactionDraft(noteEl, polarity);
  const signed = await signEventDraft(draft);
  const snap = {
    viewer: noteEl.dataset.asciiReactionViewer || "",
    total: noteEl.dataset.asciiReactionTotal || "0",
  };
  const requestSeq = nextReactionSequence(noteEl);
  const next = polarity === "up" ? "+" : "-";
  optimisticBump(noteEl, next);
  refreshAsciiAround(noteEl);
  try {
    const payload = await publishSignedEvent(signed);
    const accepted = Number(payload?.accepted || 0);
    if (!payload?.persisted && accepted <= 0) {
      throw new Error(payload?.error || "Publish failed.");
    }
    clearThreadSnapshots();
    invalidateThreadFragmentPrefetch();
  } catch (err) {
    if (requestSeq === latestReactionSequence(noteEl)) {
      rollback(noteEl, snap);
      refreshAsciiAround(noteEl);
    }
    const msg = err instanceof Error ? err.message : "Publish failed.";
    window.alert(msg);
  }
}

function onReactionClick(event) {
  const btn = event.target.closest("[data-ascii-reaction-vote]");
  if (!btn) return;
  event.preventDefault();
  event.stopPropagation();
  const vote = btn.getAttribute("data-ascii-reaction-vote");
  if (vote !== "up" && vote !== "down") return;
  const shell = findNoteShell(btn);
  if (!shell) return;
  const shells = shellsForReactionTarget(shell);
  if (vote === "up" && shells.some((s) => (s.dataset.asciiReactionViewer || "").trim() === "+")) return;
  if (vote === "down" && shells.some((s) => (s.dataset.asciiReactionViewer || "").trim() === "-")) return;
  void publishReaction(shell, vote);
}

export function ensureNoteReactionsDelegated() {
  if (delegated) return;
  delegated = true;
  document.addEventListener("click", onReactionClick);
}

function ensureReactionsDialog() {
  let dialog = document.querySelector("[data-reactions-dialog]");
  if (dialog) return dialog;
  dialog = document.createElement("dialog");
  dialog.className = "reactions-dialog";
  dialog.dataset.reactionsDialog = "";
  dialog.setAttribute("aria-labelledby", "reactions-dialog-title");
  dialog.innerHTML = `
    <form method="dialog" class="reactions-dialog-close-row">
      <button type="submit" class="composer-close-button" aria-label="Close reactions">X</button>
    </form>
    <h2 id="reactions-dialog-title" class="reactions-dialog-title">Reactions</h2>
    <p class="reactions-dialog-status" data-reactions-status></p>
    <ul class="reactions-dialog-list" data-reactions-list hidden></ul>
    <p class="reactions-dialog-truncated muted" data-reactions-truncated hidden></p>
  `;
  dialog.addEventListener("click", (event) => {
    if (event.target === dialog) dialog.close();
  });
  document.body.append(dialog);
  return dialog;
}

/** Opens a modal listing reactors (display name and up/down) for a note id. */
export async function openReactionsModal(noteId) {
  const id = String(noteId || "").trim();
  if (!id) return;
  const seq = ++reactionsModalSeq;
  const dialog = ensureReactionsDialog();
  const status = dialog.querySelector("[data-reactions-status]");
  const list = dialog.querySelector("[data-reactions-list]");
  const truncated = dialog.querySelector("[data-reactions-truncated]");
  list.hidden = true;
  list.replaceChildren();
  truncated.hidden = true;
  truncated.textContent = "";
  status.textContent = "Loading…";
  if (!dialog.open) {
    if (typeof dialog.showModal === "function") {
      dialog.showModal();
    } else {
      dialog.setAttribute("open", "");
    }
  }
  const url = new URL("/api/reactions", window.location.origin);
  url.searchParams.set("note_id", id);
  try {
    const res = await fetch(url.toString(), { credentials: "same-origin" });
    const data = await res.json().catch(() => ({}));
    if (seq !== reactionsModalSeq) return;
    if (!res.ok) {
      const msg = typeof data.error === "string" && data.error ? data.error : res.statusText || "Could not load reactions.";
      status.textContent = msg;
      return;
    }
    const rows = Array.isArray(data.reactions) ? data.reactions : [];
    if (rows.length === 0) {
      status.textContent = "No reactions yet.";
      return;
    }
    status.textContent = "";
    for (const row of rows) {
      const pk = String(row.pubkey || "").trim();
      const li = document.createElement("li");
      li.className = "reactions-dialog-row";
      const nameLink = document.createElement("a");
      nameLink.href = pk ? `/u/${encodeURIComponent(pk)}` : "#";
      if (pk) nameLink.dataset.relayAware = "";
      nameLink.textContent = typeof row.display_name === "string" && row.display_name ? row.display_name : pk.slice(0, 12);
      const vote = document.createElement("span");
      vote.className = "reactions-dialog-vote";
      const isDown = row.vote === "down";
      vote.textContent = isDown ? "▼" : "▲";
      vote.setAttribute("aria-label", isDown ? "Downvote" : "Upvote");
      li.append(nameLink, document.createTextNode(" "), vote);
      list.append(li);
    }
    list.hidden = false;
    if (data.truncated) {
      truncated.hidden = false;
      const lim = Number.parseInt(String(data.limit || ""), 10);
      truncated.textContent = Number.isFinite(lim) && lim > 0 ? `Showing the first ${lim} reactors.` : "List truncated.";
    }
  } catch {
    if (seq !== reactionsModalSeq) return;
    status.textContent = "Network error.";
  }
}

export { formatThousandsSpaced };
