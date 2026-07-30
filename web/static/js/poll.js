import { planPublishTargets, publishSignedEvent } from "./publish.js";
import { putEvents, eventsByTag } from "./event-store.js";
import { relayFetch } from "./relay-pool.js";
import { readRelaysForViewer } from "./publish-plan.js";
import { KIND_POLL, KIND_POLL_RESPONSE } from "./nostr-kinds.js";
import { canonicalHex64, dedupeEventsByID, normalizePubkey } from "./relay-utils.js";
import { normalizedPubkey } from "./session.js";
import { signEventDraft } from "./signer.js";
import { pendingPublishStatus, showPublishStatusSheet } from "./publish-status.js";

const pollDraftSelections = new Map();
let pollDelegatesBound = false;

export const PollType = Object.freeze({
  SINGLE: "singlechoice",
  MULTIPLE: "multiplechoice",
});

export function parsePollEvent(event) {
  if (Number(event?.kind) !== KIND_POLL) return null;
  const options = [];
  const relays = [];
  let pollType = PollType.SINGLE;
  let endsAt = 0;
  for (const tag of event?.tags || []) {
    if (!Array.isArray(tag) || !tag.length) continue;
    switch (String(tag[0] || "")) {
    case "option": {
      if (tag.length < 3) break;
      const id = String(tag[1] || "").trim();
      const label = String(tag[2] || "").trim();
      if (!id || !label) break;
      options.push({ id, label });
      break;
    }
    case "relay": {
      const relay = String(tag[1] || "").trim();
      if (relay) relays.push(relay);
      break;
    }
    case "polltype":
      pollType = String(tag[1] || "").trim().toLowerCase() === PollType.MULTIPLE
        ? PollType.MULTIPLE
        : PollType.SINGLE;
      break;
    case "endsAt": {
      const next = Number.parseInt(String(tag[1] || "").trim(), 10);
      if (Number.isFinite(next) && next > 0) endsAt = next;
      break;
    }
    default:
      break;
    }
  }
  if (!options.length) return null;
  return {
    id: canonicalHex64(event.id),
    event,
    question: String(event.content || "").trim(),
    options,
    pollType,
    endsAt,
    relays: [...new Set(relays)],
    isExpired: endsAt > 0 && endsAt < Math.floor(Date.now() / 1000),
  };
}

export function dedupePollVotes(votes = []) {
  const latestByPubkey = new Map();
  votes.forEach((event) => {
    const pk = normalizePubkey(event?.pubkey);
    if (!pk) return;
    const current = latestByPubkey.get(pk);
    if (!current || Number(event?.created_at || 0) > Number(current?.created_at || 0)) {
      latestByPubkey.set(pk, event);
    }
  });
  return [...latestByPubkey.values()];
}

export function selectedOptionIDs(vote, pollType = PollType.SINGLE) {
  if (!vote) return new Set();
  const responseTags = (vote.tags || []).filter((tag) => Array.isArray(tag) && tag[0] === "response" && tag[1]);
  if (pollType !== PollType.MULTIPLE) {
    const id = String(responseTags[0]?.[1] || "").trim();
    return id ? new Set([id]) : new Set();
  }
  return new Set(responseTags.map((tag) => String(tag[1] || "").trim()).filter(Boolean));
}

export function tallyPollVotes(votes = [], pollType = PollType.SINGLE) {
  const counts = {};
  votes.forEach((event) => {
    const picked = [...selectedOptionIDs(event, pollType)];
    const seen = new Set();
    picked.forEach((id) => {
      if (!id || seen.has(id)) return;
      seen.add(id);
      counts[id] = (counts[id] || 0) + 1;
    });
  });
  return counts;
}

function pollContainerState(container) {
  try {
    return JSON.parse(String(container?.dataset?.asciiPollState || "{}"));
  } catch {
    return {};
  }
}

export function pollDescriptorForContainer(container) {
  try {
    const parsed = JSON.parse(String(container?.dataset?.asciiPoll || ""));
    if (!parsed || typeof parsed !== "object") return null;
    const endsAt = Number.parseInt(`${parsed.endsAt ?? 0}`, 10) || 0;
    return {
      ...parsed,
      endsAt,
      isExpired: endsAt > 0 && endsAt < Math.floor(Date.now() / 1000),
    };
  } catch {
    return null;
  }
}

function draftKey(container) {
  return canonicalHex64(container?.id?.replace(/^note-/, "")) || "";
}

export function pollDraftSelection(container) {
  const key = draftKey(container);
  return key ? new Set(pollDraftSelections.get(key) || []) : new Set();
}

function setPollDraftSelection(container, value) {
  const key = draftKey(container);
  if (!key) return;
  const next = [...new Set(value)].filter(Boolean);
  if (next.length) pollDraftSelections.set(key, next);
  else pollDraftSelections.delete(key);
}

function syncPollState(container, poll, votes = []) {
  const deduped = dedupePollVotes(votes);
  const viewerVote = deduped.find((vote) => normalizePubkey(vote.pubkey) === normalizedPubkey()) || null;
  const tally = tallyPollVotes(deduped, poll.pollType);
  const totalVotes = Object.values(tally).reduce((sum, count) => sum + count, 0);
  container.dataset.asciiPollState = JSON.stringify({
    loaded: true,
    totalVotes,
    tally,
    voted: Boolean(viewerVote),
    selected: [...selectedOptionIDs(viewerVote, poll.pollType)],
  });
  delete container.dataset.asciiPollLoading;
}

async function fetchPollVotesFor(poll) {
  const pollID = canonicalHex64(poll?.id);
  if (!pollID) return [];
  const cached = await eventsByTag("e", pollID, { kind: KIND_POLL_RESPONSE, limit: 500 }).catch(() => []);
  const relays = [...new Set([...(poll.relays || []), ...readRelaysForViewer()])];
  if (!relays.length) return dedupePollVotes(cached);
  const fetched = await relayFetch(relays, [{
    kinds: [KIND_POLL_RESPONSE],
    "#e": [pollID],
    limit: 500,
  }]).catch(() => []);
  if (fetched.length) await putEvents(fetched);
  return dedupePollVotes([...cached, ...fetched]);
}

export async function hydrateVisiblePolls(root = document, { force = false } = {}) {
  bindPollDelegates();
  const nodes = [...root.querySelectorAll("[data-ascii-poll]")];
  await Promise.all(nodes.map(async (container) => {
    if (!(container instanceof HTMLElement)) return;
    if (!force && container.dataset.asciiPollLoaded === "1") return;
    const poll = pollDescriptorForContainer(container);
    if (!poll) return;
    container.dataset.asciiPollLoading = "1";
    const votes = await fetchPollVotesFor(poll);
    syncPollState(container, poll, votes);
    container.dataset.asciiPollLoaded = "1";
    window.dispatchEvent(new CustomEvent("ptxt:poll-updated", { detail: { noteId: poll.id } }));
  }));
}

async function publishPollVote(container, poll, optionIDs) {
  const viewer = normalizedPubkey();
  if (!viewer) throw new Error("Login to vote in polls.");
  const picked = [...new Set((optionIDs || []).map((id) => String(id || "").trim()).filter(Boolean))];
  if (!picked.length) throw new Error("Choose at least one option.");
  const tags = [["e", poll.id], ["p", poll.event.pubkey]];
  picked.forEach((id) => tags.push(["response", id]));
  const signed = await signEventDraft({
    kind: KIND_POLL_RESPONSE,
    created_at: Math.floor(Date.now() / 1000),
    tags,
    content: "",
  });
  const plannedRelays = await planPublishTargets(signed).catch(() => []);
  const pendingState = pendingPublishStatus({
    phaseTitle: "Broadcasting to relays",
    statusMessage: "Preparing poll vote broadcast...",
    plannedRelays,
    completionMessage: "vote published.",
  });
  showPublishStatusSheet(null, { title: "Poll vote status", initialState: pendingState });
  const payload = await publishSignedEvent(signed);
  showPublishStatusSheet(payload, { title: "Poll vote status", initialState: pendingState });
  await putEvents([signed]);
  setPollDraftSelection(container, []);
  container.dataset.asciiPollLoaded = "";
  await hydrateVisiblePolls(container.closest("[data-shell-main], [data-nav-root], main, body") || document, { force: true });
  return payload;
}

export function bindPollDelegates() {
  if (pollDelegatesBound || typeof document === "undefined") return;
  pollDelegatesBound = true;
  document.addEventListener("click", (event) => {
    const toggle = event.target.closest("[data-poll-toggle-option]");
    if (toggle) {
      event.preventDefault();
      event.stopPropagation();
      const container = toggle.closest("[data-ascii-poll]");
      const poll = pollDescriptorForContainer(container);
      if (!container || !poll) return;
      const optionID = String(toggle.getAttribute("data-poll-toggle-option") || "").trim();
      if (!optionID) return;
      if (poll.pollType !== PollType.MULTIPLE) {
        void publishPollVote(container, poll, [optionID]).catch((error) => {
          window.alert(error instanceof Error ? error.message : "Poll vote failed.");
        });
        return;
      }
      const draft = pollDraftSelection(container);
      if (draft.has(optionID)) draft.delete(optionID);
      else draft.add(optionID);
      setPollDraftSelection(container, draft);
      window.dispatchEvent(new CustomEvent("ptxt:poll-updated", { detail: { noteId: poll.id } }));
      return;
    }
    const submit = event.target.closest("[data-poll-submit]");
    if (!submit) return;
    event.preventDefault();
    event.stopPropagation();
    const container = submit.closest("[data-ascii-poll]");
    const poll = pollDescriptorForContainer(container);
    if (!container || !poll) return;
    void publishPollVote(container, poll, [...pollDraftSelection(container)]).catch((error) => {
      window.alert(error instanceof Error ? error.message : "Poll vote failed.");
    });
  });
}

if (typeof window !== "undefined") {
  window.addEventListener("ptxt:route-loaded", () => {
    void hydrateVisiblePolls(document).catch(() => {});
  });
}
