import { getEvent, longFormEvents, putEvents } from "./event-store.js";
import { feedPaginationCursorFromDatasets } from "./feed-pagination.js";
import { KIND_LONG_FORM } from "./nostr-kinds.js";
import { relayFetch } from "./relay-pool.js";
import { readRelaysForViewer } from "./publish-plan.js";
import { dedupeEventsByID, firstTagValue } from "./relay-utils.js";
import { loadTrendingFeed, isTrendingSort } from "./trending-service.js";
import { sortEventsNewestFirst } from "./feed-query.js";
import { isClientDBUnavailableError } from "./client-store.js";

function truncate(text, max) {
  const value = String(text || "").trim();
  if (value.length <= max) return value;
  return `${value.slice(0, max - 1)}…`;
}

export function parseReadCard(event) {
  const titleTag = firstTagValue(event, "title");
  const firstLine = String(event?.content || "")
    .split("\n")[0]
    .trim();
  const title = titleTag || firstLine || "Untitled";

  const publishedRaw = firstTagValue(event, "published_at");
  const publishedAt = Number(publishedRaw) > 0 ? Number(publishedRaw) : Number(event?.created_at) || 0;

  let summary = firstTagValue(event, "summary");
  if (!summary) {
    const paragraphs = String(event?.content || "").split("\n\n");
    for (const paragraph of paragraphs) {
      const line = paragraph.trim();
      if (!line || line.startsWith("#")) continue;
      summary = truncate(line, 240);
      break;
    }
  } else {
    summary = truncate(summary, 240);
  }

  let imageURL = firstTagValue(event, "image");
  if (imageURL && !/^https?:\/\//i.test(imageURL)) imageURL = "";

  return {
    event,
    title: truncate(title, 80),
    publishedAt,
    summary,
    imageURL,
  };
}

async function fetchRecentReadsFromRelays({ limit = 50, until, untilID } = {}) {
  const relays = readRelaysForViewer();
  const filter = { kinds: [KIND_LONG_FORM], limit: Math.min(200, Math.max(limit, 50)) };
  if (until) filter.until = until;
  const events = await relayFetch(relays, [filter]);
  await putEvents(events);
  return sortEventsNewestFirst(dedupeEventsByID(events)).slice(0, limit);
}

async function safeLongFormEvents(options) {
  try {
    return await longFormEvents(options);
  } catch (error) {
    if (isClientDBUnavailableError(error)) return null;
    throw error;
  }
}

async function safeGetEvent(id) {
  try {
    return await getEvent(id);
  } catch (error) {
    if (isClientDBUnavailableError(error)) return null;
    throw error;
  }
}

export async function fetchReadsPage({
  sort = "recent",
  limit = 50,
  until,
  untilID,
  forceFetch = false,
  viewerPubkey = "",
} = {}) {
  const { beforeCreatedAt, beforeID } = feedPaginationCursorFromDatasets({ until, untilID });

  if (isTrendingSort(sort)) {
    const events = await loadTrendingFeed({
      sort,
      viewerPubkey,
      limit,
      until,
      untilID,
      forceFetch,
      kindFilter: KIND_LONG_FORM,
    });
    return events.map(parseReadCard);
  }

  let events = [];
  if (!forceFetch) {
    events = (await safeLongFormEvents({ limit, beforeCreatedAt, beforeID })) || [];
  }
  if (forceFetch || events.length < limit) {
    const fetched = await fetchRecentReadsFromRelays({ limit, until, untilID });
    const cached = await safeLongFormEvents({ limit, beforeCreatedAt, beforeID });
    events = cached || fetched;
  }
  return events.map(parseReadCard);
}

export async function fetchReadDetail(id = "") {
  const noteID = String(id || "").trim().toLowerCase();
  if (!noteID) return null;
  let event = await safeGetEvent(noteID);
  if (!event || Number(event.kind) !== KIND_LONG_FORM) {
    const relays = readRelaysForViewer();
    const events = await relayFetch(relays, [{ ids: [noteID], kinds: [KIND_LONG_FORM], limit: 1 }]);
    await putEvents(events);
    event = await safeGetEvent(noteID);
    if ((!event || Number(event.kind) !== KIND_LONG_FORM) && events.length) {
      event = dedupeEventsByID(events).find((candidate) =>
        String(candidate?.id || "").toLowerCase() === noteID && Number(candidate.kind) === KIND_LONG_FORM,
      ) || null;
    }
  }
  if (!event || Number(event.kind) !== KIND_LONG_FORM) return null;
  return parseReadCard(event);
}
