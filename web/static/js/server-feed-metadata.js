const serverReplyCounts = new Map();
const SERVER_REPLY_COUNT_LIMIT = 2_000;

function normalizedEventID(value) {
  return String(value || "").trim().toLowerCase();
}

function normalizedCount(value) {
  const count = Number.parseInt(`${value ?? 0}`, 10);
  return Number.isFinite(count) && count > 0 ? count : 0;
}

/** Remember authoritative metadata returned alongside `/api/feed-notes`. */
export function rememberServerFeedMetadata(payload) {
  const counts = payload?.reply_counts;
  if (!counts || typeof counts !== "object") return;
  for (const [rawID, rawCount] of Object.entries(counts)) {
    const id = normalizedEventID(rawID);
    if (!id) continue;
    serverReplyCounts.delete(id);
    serverReplyCounts.set(id, normalizedCount(rawCount));
    while (serverReplyCounts.size > SERVER_REPLY_COUNT_LIMIT) {
      serverReplyCounts.delete(serverReplyCounts.keys().next().value);
    }
  }
}

/** Return cached server reply counts for these events, omitting unknown ids. */
export function serverReplyCountsForEvents(events = []) {
  const out = {};
  for (const event of events || []) {
    const id = normalizedEventID(event?.id);
    if (id && serverReplyCounts.has(id)) out[id] = serverReplyCounts.get(id);
  }
  return out;
}

/** Keep cached server projections as a floor when relay/local reads are incomplete. */
export function mergeServerReplyCounts(noteIDs = [], counts = {}) {
  const out = { ...(counts || {}) };
  for (const rawID of noteIDs || []) {
    const id = normalizedEventID(rawID);
    if (!id || !serverReplyCounts.has(id)) continue;
    out[id] = Math.max(normalizedCount(out[id]), serverReplyCounts.get(id));
  }
  return out;
}

export function clearServerFeedMetadataForTests() {
  serverReplyCounts.clear();
}
