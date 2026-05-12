import { finalizeEvent, nip19 } from "../lib/nostr-tools.js";
import { DEFAULT_RETRY_ATTEMPTS, sleepBackoff } from "./backoff.js";
import { getSession, loginCapabilities, normalizedPubkey } from "./session.js";

function decodeSessionSecret() {
  const nsec = String(sessionStorage.getItem("ptxt_nsec") || "").trim();
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

export async function signEventDraft(event, session = getSession()) {
  const state = requireSigner("complete this action", session);
  if (session.method === "nip07") {
    if (!window.nostr?.signEvent) {
      throw new Error("NIP-07 extension signing is unavailable.");
    }
    return signEventWithNIP07Retry(event);
  }
  const secret = decodeSessionSecret();
  if (!secret) {
    throw new Error("Missing private key for this login method.");
  }
  return finalizeEvent(event, secret);
}
