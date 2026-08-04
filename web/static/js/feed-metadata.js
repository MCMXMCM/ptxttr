import { refreshAscii } from "./ascii.js";
import { fetchReplyCounts, fetchReactionStats, fetchZapReceipts } from "./relay-reads.js";
import { normalizedPubkey } from "./session.js";
import { replyLabelForCount } from "./reply-label.js";
import { isTrendingSort, trendingWindowStart } from "./trending-service.js";
import {
  replyCounts as localReplyCounts,
  reactionTotals as localReactionTotals,
  zapTotals as localZapTotals,
} from "./event-store.js";
import { canonicalHex64 } from "./relay-utils.js";
import { powerLimitedCount, powerSaverActive } from "./power-mode.js";
import { isSafariWebKit } from "./browser-capabilities.js";
import { fetchCachedQuery, peekQueryData, queryKeys } from "./query-client.js";
import { mergeServerReplyCounts } from "./server-feed-metadata.js";
import { zapTotalsForEvents } from "./zap-utils.js";

const visibleFeedMetadataInflight = new Map();
const latestFeedMetadataTokenBySelector = new Map();

function feedMetadataKey(feedSelector = "[data-feed]", refreshToken = "") {
  return `${feedSelector}::${refreshToken || "default"}`;
}

function metadataRefreshStale(feedSelector = "[data-feed]", refreshToken = "") {
  if (!refreshToken) return false;
  return latestFeedMetadataTokenBySelector.get(feedSelector) !== refreshToken;
}

function intValue(value) {
  return Number.parseInt(`${value ?? 0}`, 10) || 0;
}

function collectedFeedNotes(root, feedSelector, opts = {}) {
  return opts.ids
    ? { ids: opts.ids, noteByID: opts.noteByID || new Map() }
    : collectVisibleFeedNotes(root, feedSelector);
}

async function refreshFeedMetadataSet(root, feedSelector, opts, load, applyChange) {
  const collected = collectedFeedNotes(root, feedSelector, opts);
  const ids = collected.ids;
  if (!ids.length) return;
  try {
    const payload = await load(ids);
    if (metadataRefreshStale(feedSelector, opts.refreshToken)) return [];
    return applyChangedFeedNoteDatasets(root, ids, collected.noteByID, (note, id) => applyChange(note, id, payload));
  } catch {
    // keep SSR values
  }
  return [];
}

/** Collect up to 50 visible note hex ids under a feed column (newest-first scan). */
export function collectVisibleFeedNotes(root, feedSelector = "[data-feed]") {
  const feed = root.querySelector(feedSelector);
  if (!feed) return { ids: [], noteByID: new Map() };
  const notes = [...feed.querySelectorAll(".note[id^='note-']")];
  const ids = [];
  const seen = new Set();
  const noteByID = new Map();
  for (const note of notes) {
    const id = note.id.replace(/^note-/, "");
    if (!id || seen.has(id)) continue;
    seen.add(id);
    noteByID.set(id, note);
    ids.push(id);
    if (ids.length >= powerLimitedCount(50, 20)) break;
  }
  return { ids, noteByID };
}

/** Collect up to 50 visible note hex ids under a feed column (newest-first scan). */
export function collectVisibleFeedNoteIds(root, feedSelector = "[data-feed]") {
  return collectVisibleFeedNotes(root, feedSelector).ids;
}

/** Fetch reply/reaction maps for a feed page (relay + local cache, windowed for trending). */
export async function fetchFeedNoteMetadataMaps(noteIDs, { viewerPubkey = "", sort = "recent" } = {}) {
  const ids = (noteIDs || []).map(canonicalHex64).filter(Boolean);
  if (!ids.length) return { replyCounts: {}, reactionStats: {}, zapTotals: {} };
  const queryKey = queryKeys.feedMetadata({
    noteIDs: ids,
    viewerPubkey,
    sort,
  });
  const cached = peekQueryData(queryKey);
  if (cached) {
    return {
      ...cached,
      replyCounts: mergeServerReplyCounts(ids, cached?.replyCounts),
    };
  }

  const metadata = await fetchCachedQuery({
    queryKey,
    cacheMode: "cache-first",
    staleTime: 20_000,
    gcTime: 5 * 60_000,
    queryFn: async () => {
      const since = isTrendingSort(sort) ? trendingWindowStart(sort) : undefined;
      const localOpts = since ? { sinceCreatedAt: since } : {};
      const [localReplies, localReactions, localZaps] = await Promise.all([
        localReplyCounts(ids, localOpts).catch(() => new Map()),
        localReactionTotals(ids, localOpts).catch(() => new Map()),
        localZapTotals(ids, localOpts).catch(() => new Map()),
      ]);
      if (powerSaverActive()) {
        return buildMetadataMaps(ids, localReplies, localReactions, localZaps);
      }

      const [remoteReplies, remoteReactions, remoteZaps] = await Promise.all([
        fetchReplyCounts(ids).catch(() => ({})),
        fetchReactionStats(ids, viewerPubkey).catch(() => ({})),
        fetchZapReceipts(ids).catch(() => []),
      ]);
      const remoteZapTotals = zapTotalsForEvents(ids, remoteZaps, localOpts);

      const replyCounts = {};
      const reactionStats = {};
      const zapTotals = {};
      for (const id of ids) {
        const remoteReply = intValue(remoteReplies[id]);
        const localReply = intValue(localReplies.get(id));
        replyCounts[id] = Math.max(remoteReply, localReply);

        const remoteRow = remoteReactions[id] || {};
        const remoteTotal = intValue(remoteRow.total);
        const localTotal = intValue(localReactions.get(id));
        reactionStats[id] = {
          total: Math.max(remoteTotal, localTotal),
          viewer: typeof remoteRow.viewer === "string" ? remoteRow.viewer : "",
        };
        zapTotals[id] = Math.max(intValue(localZaps.get(id)), intValue(remoteZapTotals.get(id)));
      }
      return { replyCounts, reactionStats, zapTotals };
    },
  });
  return {
    ...metadata,
    replyCounts: mergeServerReplyCounts(ids, metadata?.replyCounts),
  };
}

function buildMetadataMaps(ids, localReplies, localReactions, localZaps) {
  const replyCounts = {};
  const reactionStats = {};
  const zapTotals = {};
  for (const id of ids) {
    replyCounts[id] = intValue(localReplies.get(id));
    reactionStats[id] = {
      total: intValue(localReactions.get(id)),
      viewer: "",
    };
    zapTotals[id] = intValue(localZaps.get(id));
  }
  return { replyCounts, reactionStats, zapTotals };
}

function applyChangedFeedNoteDatasets(root, ids, noteByID, applyChange) {
  const changedNotes = [];
  for (const id of ids) {
    const note = noteByID.get(id) || root.querySelector(`#note-${id}`);
    if (!note) continue;
    if (applyChange(note, id) === true) changedNotes.push(note);
  }
  return changedNotes;
}

export async function refreshVisibleFeedReplyCounts(root, _baseURL, feedSelector = "[data-feed]", opts = {}) {
  return refreshFeedMetadataSet(root, feedSelector, opts, async (ids) => {
    const localOnly = powerSaverActive() || opts.localOnly;
    const localCounts = localOnly ? await localReplyCounts(ids) : null;
    const fetchedCounts = localOnly ? Object.fromEntries(localCounts.entries()) : await fetchReplyCounts(ids);
    const counts = mergeServerReplyCounts(ids, fetchedCounts);
    return { counts, localCounts, localOnly };
  }, (note, id, { counts, localCounts, localOnly }) => {
    if (localOnly && !localCounts.has(id)) return;
    const next = intValue(counts[id]);
    const nextLabel = replyLabelForCount(next);
    if (
      note.dataset.asciiReplyCount === `${next}` &&
      note.dataset.asciiReplyLabel === nextLabel
    ) {
      return false;
    }
    note.dataset.asciiReplyCount = `${next}`;
    note.dataset.asciiReplyLabel = nextLabel;
    return true;
  });
}

export async function refreshVisibleFeedReactionStats(root, _baseURL, feedSelector = "[data-feed]", opts = {}) {
  return refreshFeedMetadataSet(root, feedSelector, opts, async (ids) => {
    const localOnly = powerSaverActive() || opts.localOnly;
    const localTotals = localOnly ? await localReactionTotals(ids) : null;
    const payload = localOnly
      ? Object.fromEntries([...localTotals.entries()].map(([id, total]) => [id, { total, viewer: "" }]))
      : await fetchReactionStats(ids, normalizedPubkey());
    return { payload, localOnly, localTotals };
  }, (note, id, { payload, localOnly, localTotals }) => {
    if (localOnly && !localTotals.has(id)) return;
    const row = payload[id];
    const total = row && typeof row.total === "number" ? row.total : intValue(row?.total);
    const viewer = row && typeof row.viewer === "string" ? row.viewer : "";
    if (
      note.dataset.asciiReactionTotal === `${total}` &&
      note.dataset.asciiReactionViewer === viewer
    ) {
      return false;
    }
    note.dataset.asciiReactionTotal = `${total}`;
    note.dataset.asciiReactionViewer = viewer;
    return true;
  });
}

export async function refreshVisibleFeedNoteMetadata(root, baseURL, options = {}) {
  if (typeof document !== "undefined" && document.visibilityState === "hidden") return;
  const feedSelector = options.feedSelector || "[data-feed]";
  const refreshToken = String(options.refreshToken || "");
  if (refreshToken) latestFeedMetadataTokenBySelector.set(feedSelector, refreshToken);
  const inflightKey = feedMetadataKey(feedSelector, refreshToken);
  if (visibleFeedMetadataInflight.has(inflightKey)) {
    return visibleFeedMetadataInflight.get(inflightKey);
  }
  const { ids, noteByID } = collectVisibleFeedNotes(root, feedSelector);
  if (!ids.length) return;
  const metaOpts = {
    ids,
    noteByID,
    refreshToken,
    localOnly: Boolean(options.localOnly || (isSafariWebKit() && options.routeRestore === true)),
  };
  const work = (async () => {
    const changed = await Promise.all([
      refreshVisibleFeedReplyCounts(root, baseURL, feedSelector, metaOpts),
      refreshVisibleFeedReactionStats(root, baseURL, feedSelector, metaOpts),
      refreshVisibleFeedZapTotals(root, baseURL, feedSelector, metaOpts),
    ]);
    if (metadataRefreshStale(feedSelector, refreshToken)) return [];
    const changedNotes = new Set(changed.flat().filter(Boolean));
    if (changedNotes.size) changedNotes.forEach((note) => refreshAscii(note));
    return [...changedNotes];
  })();
  visibleFeedMetadataInflight.set(inflightKey, work);
  try {
    return await work;
  } finally {
    visibleFeedMetadataInflight.delete(inflightKey);
  }
}

export async function refreshVisibleFeedZapTotals(root, _baseURL, feedSelector = "[data-feed]", opts = {}) {
  return refreshFeedMetadataSet(root, feedSelector, opts, async (ids) => {
    const receipts = !opts.localOnly && !powerSaverActive()
      ? await fetchZapReceipts(ids).catch(() => [])
      : [];
    const [local, remote] = await Promise.all([
      localZapTotals(ids).catch(() => new Map()),
      Promise.resolve(zapTotalsForEvents(ids, receipts)),
    ]);
    return new Map(ids.map((id) => [id, Math.max(intValue(local.get(id)), intValue(remote.get(id)))]));
  }, (note, id, totals) => {
    const next = intValue(totals.get(id));
    if (note.dataset.asciiZapTotal === `${next}`) return false;
    note.dataset.asciiZapTotal = `${next}`;
    return true;
  });
}
