import { finalizeEvent, nip19, verifyEvent } from "../lib/nostr-tools.js";
import { DEFAULT_RETRY_ATTEMPTS, sleepBackoff } from "./backoff.js";
import { getSession, getSessionSecretNsec, loginCapabilities, normalizedPubkey } from "./session.js";

export const CLIENT_METADATA_TAG = Object.freeze(["client", "Plain Text Nostr"]);

// Replaceable events are ordered at one-second resolution. Keep writes from this
// tab monotonic so a rapid toggle cannot lose to its immediately previous value.
const lastReplaceableCreatedAt = new Map();

function decodeSessionSecret() {
  const nsec = getSessionSecretNsec();
  if (!nsec) return null;
  try {
    const decoded = nip19.decode(nsec).data;
    if (decoded instanceof Uint8Array) return decoded;
    if (Array.isArray(decoded)) return Uint8Array.from(decoded);
  } catch {
    return null;
  }
  return null;
}

function replaceableAddress(event, pubkey) {
  const kind = Number(event?.kind);
  if (!Number.isInteger(kind)) return "";
  if (kind === 0 || kind === 3 || (kind >= 10000 && kind < 20000)) {
    return `${pubkey}:${kind}`;
  }
  if (kind < 30000 || kind >= 40000) return "";
  const identifier = (event?.tags || []).find((tag) => Array.isArray(tag) && tag[0] === "d")?.[1] || "";
  return `${pubkey}:${kind}:${identifier}`;
}

function withMonotonicReplaceableTimestamp(event, pubkey) {
  const address = replaceableAddress(event, pubkey);
  if (!address) return event;
  const requested = Math.max(0, Math.floor(Number(event?.created_at) || 0));
  const previous = lastReplaceableCreatedAt.get(address) || 0;
  const createdAt = Math.max(requested, previous + 1);
  lastReplaceableCreatedAt.set(address, createdAt);
  return createdAt === event.created_at ? event : { ...event, created_at: createdAt };
}

function validateSignedResult(signed, draft, expectedPubkey, signerLabel) {
  if (!signed || typeof signed !== "object" || !verifyEvent(signed)) {
    throw new Error(`${signerLabel} returned an invalid event signature.`);
  }
  if (String(signed.pubkey || "").toLowerCase() !== expectedPubkey) {
    throw new Error(`${signerLabel} signed with a different account. Reconnect the signer and try again.`);
  }
  const sameDraft = Number(signed.kind) === Number(draft.kind)
    && Number(signed.created_at) === Number(draft.created_at)
    && String(signed.content ?? "") === String(draft.content ?? "")
    && JSON.stringify(signed.tags || []) === JSON.stringify(draft.tags || []);
  if (!sameDraft) {
    throw new Error(`${signerLabel} changed the event before signing.`);
  }
  return signed;
}

export function activeSignerState(session = getSession()) {
  const capabilities = loginCapabilities(session);
  const pubkey = normalizedPubkey(session);
  const hasSecret = Boolean(decodeSessionSecret());
  return {
    ...capabilities,
    pubkey,
    hasSecret,
    canSign: capabilities.canSign && (session.method === "nip07" || hasSecret),
  };
}

export function requireSigner(action, session = getSession()) {
  const state = activeSignerState(session);
  if (!state.isLoggedIn) {
    throw new Error(`Login required to ${action}.`);
  }
  if (!state.canSign) {
    throw new Error(`Your current login method cannot sign ${action}.`);
  }
  return state;
}

function nip07SignLooksRejected(err) {
  const m = (err instanceof Error ? err.message : String(err || "")).toLowerCase();
  return /denied|reject|cancel|dismiss|closed|user\s+abort|not\s+now/i.test(m);
}

// NIP-07 signers can fail transiently (extension limits, busy signer); retry with backoff.
async function signEventWithNIP07Retry(unsigned) {
  for (let i = 0; i < DEFAULT_RETRY_ATTEMPTS; i++) {
    try {
      return await window.nostr.signEvent(unsigned);
    } catch (err) {
      if (nip07SignLooksRejected(err) || i === DEFAULT_RETRY_ATTEMPTS - 1) {
        throw err;
      }
      await sleepBackoff(i, 120, 120);
    }
  }
}

export function withClientMetadataTag(event) {
  const tags = Array.isArray(event?.tags)
    ? event.tags.map((tag) => (Array.isArray(tag) ? [...tag] : tag))
    : [];
  const hasClientTag = tags.some((tag) => Array.isArray(tag)
    && tag[0] === CLIENT_METADATA_TAG[0]
    && tag[1] === CLIENT_METADATA_TAG[1]);
  return {
    ...event,
    tags: hasClientTag ? tags : [...tags, [...CLIENT_METADATA_TAG]],
  };
}

export async function signEventDraft(event, session = getSession(), options = {}) {
  const state = requireSigner("complete this action", session);
  const tagged = options.clientMetadata === false ? event : withClientMetadataTag(event);
  const draft = withMonotonicReplaceableTimestamp(tagged, state.pubkey);
  if (session.method === "nip07") {
    if (!window.nostr?.signEvent) {
      throw new Error("NIP-07 extension signing is unavailable.");
    }
    const signed = await signEventWithNIP07Retry(draft);
    return validateSignedResult(signed, draft, state.pubkey, "NIP-07 extension");
  }
  const secret = decodeSessionSecret();
  if (!secret) {
    throw new Error("Missing private key for this login method.");
  }
  const signed = finalizeEvent(draft, secret);
  return validateSignedResult(signed, draft, state.pubkey, "Local signer");
}
