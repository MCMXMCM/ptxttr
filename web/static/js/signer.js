import { finalizeEvent, nip19 } from "../lib/nostr-tools.js";
import { DEFAULT_RETRY_ATTEMPTS, sleepBackoff } from "./backoff.js";
import { getSession, getSessionSecretNsec, loginCapabilities, normalizedPubkey } from "./session.js";

export const CLIENT_METADATA_TAG = Object.freeze(["client", "Plain Text Nostr"]);

function decodeSessionSecret() {
  const nsec = getSessionSecretNsec();
  if (!nsec) return null;
  const decoded = nip19.decode(nsec).data;
  if (decoded instanceof Uint8Array) return decoded;
  if (Array.isArray(decoded)) return Uint8Array.from(decoded);
  return null;
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
  const draft = options.clientMetadata === false ? event : withClientMetadataTag(event);
  if (session.method === "nip07") {
    if (!window.nostr?.signEvent) {
      throw new Error("NIP-07 extension signing is unavailable.");
    }
    return signEventWithNIP07Retry(draft);
  }
  const secret = decodeSessionSecret();
  if (!secret) {
    throw new Error("Missing private key for this login method.");
  }
  return finalizeEvent(draft, secret);
}
