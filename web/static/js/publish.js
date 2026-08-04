import { relayPublish } from "./relay-pool.js";
import {
  bookmarkPublishFallbackRelays,
  planPublishRelays,
} from "./publish-plan.js";
import { sendEventToInboxRelays } from "./inbox-relays.js";
import { summarizeRelayFailures } from "./relay-utils.js";
import { putEvents } from "./event-store.js";
import { fetchWithSession, recordPublishedAt } from "./session.js";
import { desktopModeEnabled } from "./viewer-defaults.js";
import {
  KIND_BOOKMARK,
  KIND_FOLLOW,
  KIND_MUTE,
  KIND_NOTE,
  KIND_POLL_RESPONSE,
  KIND_PROFILE,
  KIND_REACTION,
  KIND_RELAY_LIST,
  KIND_REPOST,
} from "./nostr-kinds.js";
import { canonicalHex64, normalizePubkey } from "./relay-utils.js";
import { invalidateNostrQueries, removeNostrQueries } from "./query-client.js";

const publishDeps = {
  relayPublish,
  planPublishRelays,
  bookmarkPublishFallbackRelays,
  sendEventToInboxRelays,
  recordPublishedAt,
  putEvents,
  invalidatePublishedQueries,
  publishViaSidecar,
  desktopModeEnabled,
};

function shouldFanoutInboxRelays(event) {
  const kind = Number(event?.kind) || 0;
  return kind === KIND_NOTE
    || kind === KIND_REPOST
    || kind === KIND_REACTION
    || kind === KIND_POLL_RESPONSE;
}

function tagValues(event, name) {
  return (event?.tags || [])
    .filter((tag) => Array.isArray(tag) && tag[0] === name && tag.length >= 2)
    .map((tag) => String(tag[1] || "").trim())
    .filter(Boolean);
}

function invalidateQueryPrefix(prefix) {
  return invalidateNostrQueries({ queryKey: prefix, exact: false });
}

function removeQueryPrefix(prefix) {
  return removeNostrQueries({ queryKey: prefix, exact: false });
}

async function invalidatePublishedQueries(event) {
  const kind = Number(event?.kind) || 0;
  const author = normalizePubkey(event?.pubkey);
  const referencedEventIDs = [...new Set(tagValues(event, "e").map(canonicalHex64).filter(Boolean))];
  const ops = [];
  const queue = (promise) => {
    ops.push(Promise.resolve(promise).catch(() => {}));
  };

  switch (kind) {
  case KIND_PROFILE:
    if (author) {
      queue(removeQueryPrefix(["nostr", "replaceable", author, KIND_PROFILE]));
      queue(invalidateQueryPrefix(["nostr", "profile", author]));
    }
    queue(invalidateQueryPrefix(["nostr", "profiles"]));
    break;
  case KIND_FOLLOW:
    if (author) {
      queue(removeQueryPrefix(["nostr", "replaceable", author, KIND_FOLLOW]));
      queue(invalidateQueryPrefix(["nostr", "followContacts", author]));
      queue(invalidateQueryPrefix(["nostr", "followGraph", author]));
    }
    queue(invalidateQueryPrefix(["nostr", "feedPage"]));
    break;
  case KIND_MUTE:
    if (author) {
      queue(removeQueryPrefix(["nostr", "replaceable", author, KIND_MUTE]));
      queue(invalidateQueryPrefix(["nostr", "muteList", author]));
    }
    queue(invalidateQueryPrefix(["nostr", "feedPage"]));
    break;
  case KIND_RELAY_LIST:
    if (author) queue(removeQueryPrefix(["nostr", "replaceable", author, KIND_RELAY_LIST]));
    queue(invalidateNostrQueries());
    break;
  case KIND_BOOKMARK:
    if (author) queue(removeQueryPrefix(["nostr", "replaceable", author, KIND_BOOKMARK]));
    break;
  case KIND_NOTE:
  case KIND_REPOST:
  case KIND_REACTION:
  case KIND_POLL_RESPONSE:
    queue(invalidateQueryPrefix(["nostr", "feedPage"]));
    queue(invalidateQueryPrefix(["nostr", "notifications"]));
    referencedEventIDs.forEach((id) => queue(invalidateQueryPrefix(["nostr", "threadBundle", id])));
    break;
  default:
    queue(invalidateNostrQueries());
    break;
  }

  await Promise.all(ops);
}

async function publishViaRelays(event) {
  let relays = await publishDeps.planPublishRelays(event);
  let payload = await publishDeps.relayPublish(relays, event);
  if (Number(event.kind) === KIND_BOOKMARK && payload.accepted === 0) {
    const fallback = await publishDeps.bookmarkPublishFallbackRelays(event.pubkey, relays);
    if (fallback.length) {
      const retry = await publishDeps.relayPublish(fallback, event);
      const relayStats = [...payload.relay_stats, ...retry.relay_stats];
      payload = {
        ...retry,
        relay_stats: relayStats,
        accepted: relayStats.filter((row) => row.accepted).length,
        rejected: relayStats.filter((row) => !row.accepted).length,
        planned_relays: [...new Set([...payload.planned_relays, ...retry.planned_relays])],
      };
    }
  }
  if (payload.accepted > 0) {
    publishDeps.recordPublishedAt();
    // A caller may navigate as soon as this resolves. Finish the local write and
    // cache invalidation first so that navigation cannot repaint stale list state.
    await Promise.allSettled([
      Promise.resolve().then(() => publishDeps.putEvents([event])),
      Promise.resolve().then(() => publishDeps.invalidatePublishedQueries(event)),
    ]);
    if (shouldFanoutInboxRelays(event)) {
      void publishDeps.sendEventToInboxRelays(event).catch(() => {});
    }
    return payload;
  }
  const err = new Error(summarizeRelayFailures(payload.relay_stats));
  err.payload = payload;
  throw err;
}

async function publishViaSidecar(event) {
  const response = await fetchWithSession("/api/events", {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify({ event }),
  });
  const body = await response.json().catch(() => ({}));
  const payload = {
    ...body,
    accepted: Number(body?.accepted || 0),
    rejected: Number(body?.rejected || 0),
    planned_relays: Array.isArray(body?.planned_relays) ? body.planned_relays : [],
    relay_stats: Array.isArray(body?.relay_stats) ? body.relay_stats : [],
  };
  if (!response.ok) {
    const error = new Error(String(payload.error || "publish request failed"));
    error.payload = payload;
    error.partialSuccess = payload.accepted > 0;
    throw error;
  }
  if (payload.accepted <= 0) {
    throw new Error(String(payload.error || summarizeRelayFailures(payload.relay_stats)));
  }
  if (payload.persisted !== true) {
    const error = new Error("The event reached a relay but was not saved to the local database.");
    error.payload = payload;
    error.partialSuccess = true;
    throw error;
  }
  // The sidecar validated and durably persisted the event. Retaining a
  // second IndexedDB write here would reintroduce the competing desktop cache.
  publishDeps.recordPublishedAt();
  await publishDeps.invalidatePublishedQueries(event);
  return payload;
}

/** Publish signed events through the sidecar on desktop and directly elsewhere. */
export async function publishSignedEvent(event) {
  if (publishDeps.desktopModeEnabled()) return publishDeps.publishViaSidecar(event);
  return publishViaRelays(event);
}

export async function planPublishTargets(event) {
  return publishDeps.planPublishRelays(event);
}

export function setPublishTestHooks(overrides = null) {
  Object.assign(publishDeps, {
    relayPublish,
    planPublishRelays,
    bookmarkPublishFallbackRelays,
    sendEventToInboxRelays,
    recordPublishedAt,
    putEvents,
    invalidatePublishedQueries,
    publishViaSidecar,
    desktopModeEnabled,
  }, overrides || {});
}
