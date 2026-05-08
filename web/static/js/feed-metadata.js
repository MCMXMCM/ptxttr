import { refreshAscii } from "./ascii.js";
import { ensureFeedURLHasSessionPubkey, normalizedPubkey, relayParam } from "./session.js";
import { replyLabelForCount } from "./reply-label.js";

/** Collect up to 50 visible note hex ids under a feed column (newest-first scan). */
export function collectVisibleFeedNoteIds(root, feedSelector = "[data-feed]") {
  const feed = root.querySelector(feedSelector);
  if (!feed) return [];
  const notes = [...feed.querySelectorAll(".note[id^='note-']")];
  const ids = [];
  const seen = new Set();
  for (const note of notes) {
    const id = note.id.replace(/^note-/, "");
    if (!id || seen.has(id)) continue;
    seen.add(id);
    ids.push(id);
    if (ids.length >= 50) break;
  }
  return ids;
}

export async function refreshVisibleFeedReplyCounts(root, baseURL, feedSelector = "[data-feed]", opts = {}) {
  const ids = collectVisibleFeedNoteIds(root, feedSelector);
  if (!ids.length) return;
  const requestURL = new URL("/api/reply-counts", window.location.origin);
  ids.forEach((id) => requestURL.searchParams.append("id", id));
  const relays = baseURL.searchParams.get("relays") || relayParam();
  if (relays) requestURL.searchParams.set("relays", relays);
  try {
    const response = await fetch(requestURL.toString());
    if (!response.ok) return;
    const counts = await response.json();
    ids.forEach((id) => {
      const note = root.querySelector(`#note-${id}`);
      if (!note) return;
      const next = Number.parseInt(`${counts[id] ?? 0}`, 10) || 0;
      note.dataset.asciiReplyCount = `${next}`;
      note.dataset.asciiReplyLabel = replyLabelForCount(next);
    });
    if (!opts.skipAsciiRefresh) refreshAscii(root);
  } catch {
    // keep SSR values
  }
}

export async function refreshVisibleFeedReactionStats(root, baseURL, feedSelector = "[data-feed]", opts = {}) {
  const ids = collectVisibleFeedNoteIds(root, feedSelector);
  if (!ids.length) return;
  const requestURL = new URL("/api/reaction-stats", window.location.origin);
  ids.forEach((id) => requestURL.searchParams.append("id", id));
  const relays = baseURL.searchParams.get("relays") || relayParam();
  if (relays) requestURL.searchParams.set("relays", relays);
  const url = new URL(baseURL.toString());
  ensureFeedURLHasSessionPubkey(url);
  const pk = url.searchParams.get("pubkey") || normalizedPubkey();
  if (pk) requestURL.searchParams.set("pubkey", pk);
  try {
    const response = await fetch(requestURL.toString());
    if (!response.ok) return;
    const payload = await response.json();
    ids.forEach((id) => {
      const note = root.querySelector(`#note-${id}`);
      if (!note) return;
      const row = payload[id];
      const total = row && typeof row.total === "number" ? row.total : Number.parseInt(`${row?.total ?? 0}`, 10) || 0;
      const viewer = row && typeof row.viewer === "string" ? row.viewer : "";
      note.dataset.asciiReactionTotal = `${total}`;
      note.dataset.asciiReactionViewer = viewer;
    });
    if (!opts.skipAsciiRefresh) refreshAscii(root);
  } catch {
    // keep SSR values
  }
}

export async function refreshVisibleFeedNoteMetadata(root, baseURL, options = {}) {
  const feedSelector = options.feedSelector || "[data-feed]";
  const metaOpts = { skipAsciiRefresh: true };
  await Promise.all([
    refreshVisibleFeedReplyCounts(root, baseURL, feedSelector, metaOpts),
    refreshVisibleFeedReactionStats(root, baseURL, feedSelector, metaOpts),
  ]);
  refreshAscii(root);
}
