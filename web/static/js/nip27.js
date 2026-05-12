import { nip19 } from "../lib/nostr-tools.js";

/** NIP-23 long-form article kind (matches nostrx.KindLongForm). */
const KIND_LONG_FORM = 30023;

// Token regexes for NIP-27 references. The composer only highlights pubkey
// references (so the @-mention overlay shows display names), while feed/ASCII
// rendering linkifies pubkey *and* event references.
export const MENTION_TOKEN_RE = /\bnostr:(nprofile|npub)[a-z0-9]+\b/gi;
export const NOSTR_REF_PATTERN = /\bnostr:(?:nevent|nprofile|npub|note)[a-z0-9]+\b/gi;

const decodeCache = new Map();
const DECODE_CACHE_LIMIT = 256;

function cachePut(key, value) {
  if (decodeCache.size >= DECODE_CACHE_LIMIT) decodeCache.clear();
  decodeCache.set(key, value);
  return value;
}

// decodeNip19Ref decodes a bech32 reference (with or without the `nostr:`
// prefix) into `{ kind, pubkey?, eventID? }`. Returns null on failure.
export function decodeNip19Ref(raw) {
  if (!raw) return null;
  const code = (raw.startsWith("nostr:") ? raw.slice(6) : raw).toLowerCase();
  if (decodeCache.has(code)) return decodeCache.get(code);
  let result = null;
  try {
    const decoded = nip19.decode(code);
    switch (decoded.type) {
      case "npub":
        result = { kind: "npub", pubkey: String(decoded.data || "").toLowerCase() };
        break;
      case "nprofile":
        result = { kind: "nprofile", pubkey: String(decoded.data?.pubkey || "").toLowerCase() };
        break;
      case "nevent": {
        const k = decoded.data?.kind;
        result = {
          kind: "nevent",
          eventID: String(decoded.data?.id || "").toLowerCase(),
          eventKind: typeof k === "number" ? k : undefined,
        };
        break;
      }
      case "note":
        result = { kind: "note", eventID: String(decoded.data || "").toLowerCase() };
        break;
      default:
        result = null;
    }
  } catch {
    result = null;
  }
  return cachePut(code, result);
}

// mentionPubKey returns the lowercase hex pubkey for an `npub` / `nprofile`
// reference, or "" when the ref is not a profile.
export function mentionPubKey(raw) {
  const ref = decodeNip19Ref(raw);
  return ref?.pubkey || "";
}

// nostrRefLink resolves a NIP-27 reference into `{ href, label }` for an
// in-app link, or null when the reference cannot be decoded.
export function nostrRefLink(raw) {
  const ref = decodeNip19Ref(raw);
  if (!ref) return null;
  if (ref.pubkey) {
    return { href: `/u/${ref.pubkey}`, label: `@${ref.pubkey.slice(0, 12)}` };
  }
  if (ref.eventID) {
    const href =
      ref.eventKind === KIND_LONG_FORM ? `/reads/${ref.eventID}` : `/thread/${ref.eventID}`;
    return { href, label: `note:${ref.eventID.slice(0, 12)}` };
  }
  return null;
}
