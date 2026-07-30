import { relayFetch, relayPublish } from "./relay-pool.js";
import { KIND_RELAY_LIST, MAX_RELAYS } from "./nostr-kinds.js";
import { effectiveReadRelays } from "./relay-state.js";
import { normalizePubkey, relayHintsFromKind10002 } from "./relay-utils.js";
import { normalizeRelayList } from "./relay-config.js";

const INBOX_RELAY_LIMIT = 10;
const INBOX_PUBLISH_TIMEOUT_MS = 5_000;

function targetPubkeys(event) {
  const author = normalizePubkey(event?.pubkey);
  const out = [];
  const seen = new Set();
  for (const tag of event?.tags || []) {
    if (!Array.isArray(tag) || tag.length < 2 || tag[0] !== "p") continue;
    const pubkey = normalizePubkey(tag[1]);
    if (!pubkey || pubkey === author || seen.has(pubkey)) continue;
    seen.add(pubkey);
    out.push(pubkey);
  }
  return out;
}

export function inboxReadRelays(event) {
  const hints = relayHintsFromKind10002(event);
  return normalizeRelayList([...(hints.read || []), ...(hints.any || [])], MAX_RELAYS * 2);
}

export async function sendEventToInboxRelays(event, {
  relayFetchFn = relayFetch,
  relayPublishFn = relayPublish,
  readRelays = effectiveReadRelays(),
} = {}) {
  const pubkeys = targetPubkeys(event);
  if (!pubkeys.length) return [];
  const relayListEvents = await relayFetchFn(readRelays, [{
    authors: pubkeys,
    kinds: [KIND_RELAY_LIST],
    limit: pubkeys.length,
  }], {
    timeoutMs: INBOX_PUBLISH_TIMEOUT_MS,
  }).catch(() => []);
  const inboxRelays = normalizeRelayList(
    relayListEvents.flatMap((relayEvent) => inboxReadRelays(relayEvent)),
    INBOX_RELAY_LIMIT,
  );
  if (!inboxRelays.length) return [];
  await relayPublishFn(inboxRelays, event, { timeoutMs: INBOX_PUBLISH_TIMEOUT_MS });
  return inboxRelays;
}
