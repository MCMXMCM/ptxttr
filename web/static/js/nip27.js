import { nip19 } from "../lib/nostr-tools.js";
import { displayName } from "./profile-parse.js";
import { profilePath } from "./relay-utils.js";

/** NIP-23 long-form article kind (matches nostrx.KindLongForm). */
const KIND_LONG_FORM = 30023;

function shortHex(value) {
  if (!value) return "";
  if (value.length <= 12) return value;
  return `${value.slice(0, 8)}…${value.slice(-4)}`;
}

const NIP19_PREFIXES = ["nprofile1", "nevent1", "npub1", "note1"];

// Token regexes for NIP-27 references. The composer only highlights pubkey
// references (so the @-mention overlay shows display names), while feed/ASCII
// rendering linkifies pubkey *and* event references.
export const MENTION_TOKEN_RE = /\b(?:nostr:)?(?:nprofile|npub)[a-z0-9]+\b/gi;
export const NOSTR_REF_PATTERN = /\b(?:nostr:)?(?:nevent|nprofile|npub|note)[a-z0-9]+\b/gi;

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
  const trimmed = String(raw).trim();
  const code = (trimmed.toLowerCase().startsWith("nostr:") ? trimmed.slice(6) : trimmed).toLowerCase();
  if (decodeCache.has(code)) return decodeCache.get(code);
  let result = null;
  try {
    const decoded = nip19.decode(code);
    switch (decoded.type) {
      case "npub":
        result = { kind: "npub", pubkey: String(decoded.data || "").toLowerCase() };
        break;
      case "nprofile":
        result = {
          kind: "nprofile",
          pubkey: String(decoded.data?.pubkey || "").toLowerCase(),
          relays: Array.isArray(decoded.data?.relays) ? decoded.data.relays : [],
        };
        break;
      case "nevent": {
        const k = decoded.data?.kind;
        const relays = Array.isArray(decoded.data?.relays) ? decoded.data.relays : [];
        result = {
          kind: "nevent",
          eventID: String(decoded.data?.id || "").toLowerCase(),
          eventKind: typeof k === "number" ? k : undefined,
          relays,
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
    return { href: profilePath(ref.pubkey, ref.relays || []), label: `@${shortHex(ref.pubkey)}` };
  }
  if (ref.eventID) {
    const href =
      ref.eventKind === KIND_LONG_FORM ? `/reads/${ref.eventID}` : `/thread/${ref.eventID}`;
    return { href, label: `note:${shortHex(ref.eventID)}` };
  }
  return null;
}

function indexFold(s, substr) {
  if (!substr) return 0;
  if (s.length < substr.length) return -1;
  for (let i = 0; i + substr.length <= s.length; i++) {
    let match = true;
    for (let j = 0; j < substr.length; j++) {
      let a = s[i + j];
      if (a >= "A" && a <= "Z") a = a.toLowerCase();
      if (a !== substr[j]) {
        match = false;
        break;
      }
    }
    if (match) return i;
  }
  return -1;
}

function isNIP27CodeRune(ch) {
  return /[a-zA-Z0-9]/.test(ch);
}

function nextBareNIP19Index(s) {
  let best = -1;
  for (const prefix of NIP19_PREFIXES) {
    let idx = 0;
    while (idx < s.length) {
      const match = indexFold(s.slice(idx), prefix);
      if (match < 0) break;
      const start = idx + match;
      if (start === 0 || !isNIP27CodeRune(s[start - 1])) {
        if (best < 0 || start < best) best = start;
        break;
      }
      idx = start + 1;
    }
  }
  return best;
}

function nextNIP19ReferenceIndex(s) {
  let best = -1;
  let prefixed = false;
  const nostrIdx = indexFold(s, "nostr:");
  if (nostrIdx >= 0) {
    best = nostrIdx;
    prefixed = true;
  }
  const bareIdx = nextBareNIP19Index(s);
  if (bareIdx >= 0 && (best < 0 || bareIdx < best)) {
    best = bareIdx;
    prefixed = false;
  }
  return { idx: best, prefixed };
}

/** All decoded NIP-27 references in `content` with byte offsets (mirrors nostrx.ExtractNIP27References). */
export function extractAllNIP27References(content) {
  const text = String(content || "");
  if (!text) return [];
  if (indexFold(text, "nostr:") < 0 && nextBareNIP19Index(text) < 0) return [];

  const refs = [];
  let index = 0;
  while (index < text.length) {
    const { idx, prefixed } = nextNIP19ReferenceIndex(text.slice(index));
    if (idx < 0) break;
    const start = index + idx;
    let cursor = start;
    if (prefixed) cursor += 6;
    while (cursor < text.length && isNIP27CodeRune(text[cursor])) cursor++;
    if (cursor <= start || (prefixed && cursor <= start + 6)) {
      index = start + 1;
      continue;
    }
    const raw = text.slice(start, cursor);
    const decoded = decodeNip19Ref(raw);
    if (decoded) {
      refs.push({
        raw,
        code: raw.toLowerCase().startsWith("nostr:") ? raw.slice(6) : raw,
        kind: decoded.kind,
        pubkey: decoded.pubkey || "",
        eventID: decoded.eventID || "",
        eventKind: decoded.eventKind,
        relays: decoded.relays || [],
        start,
        end: cursor,
      });
    }
    index = cursor;
  }
  return refs;
}

function mentionLabelHref(ref, profilesByPubkey = {}) {
  if (ref.pubkey) {
    const profile = profilesByPubkey[ref.pubkey];
    const name = profile ? displayName(profile) : shortHex(ref.pubkey);
    return { label: `@${name}`, href: profilePath(ref.pubkey, ref.relays || []), title: ref.code || ref.raw };
  }
  if (ref.eventID) {
    const href =
      ref.eventKind === KIND_LONG_FORM ? `/reads/${ref.eventID}` : `/thread/${ref.eventID}`;
    return {
      label: `note:${shortHex(ref.eventID)}`,
      href,
      title: ref.code || ref.raw,
    };
  }
  return null;
}

/**
 * Replace NIP-27 references with short labels for ASCII wrapping (mirrors
 * httpx.RewriteASCIIMentions / iOS NostrContentLinker.rewriteASCII).
 */
export function rewriteASCIIMentions(content, profilesByPubkey = {}) {
  const text = String(content || "");
  const refs = extractAllNIP27References(text);
  if (!refs.length) return { text, mentions: [] };

  let out = "";
  let cursor = 0;
  const seen = new Set();
  const mentions = [];
  for (const ref of refs) {
    if (ref.start < cursor) continue;
    const link = mentionLabelHref(ref, profilesByPubkey);
    if (!link?.label || !link.href) continue;
    out += text.slice(cursor, ref.start);
    out += link.label;
    cursor = ref.end;
    const key = `${link.label}\x00${link.href}`;
    if (seen.has(key)) continue;
    seen.add(key);
    mentions.push(link);
  }
  out += text.slice(cursor);
  return { text: out, mentions };
}

/** Merge mention records from multiple source strings for data-ascii-mentions. */
export function asciiMentionsJSONFor(profilesByPubkey, ...sources) {
  const merged = [];
  const seen = new Set();
  for (const source of sources) {
    const text = typeof source === "string" ? source : String(source?.content || "");
    const { mentions } = rewriteASCIIMentions(text, profilesByPubkey);
    for (const mention of mentions) {
      const key = `${mention.label}\x00${mention.href}`;
      if (seen.has(key)) continue;
      seen.add(key);
      merged.push(mention);
    }
  }
  return merged.length ? JSON.stringify(merged) : "";
}

/** Install rewritten ASCII source text and mention metadata on a note/reply shell. */
export function applyAsciiMentionsToShell(container, rawContent, profilesByPubkey = {}, extraSources = []) {
  if (!container) return;
  const { text } = rewriteASCIIMentions(String(rawContent || ""), profilesByPubkey);
  const mentionsJSON = asciiMentionsJSONFor(profilesByPubkey, rawContent, ...extraSources);

  let source = container.querySelector(":scope > .ascii-source");
  if (!source) {
    source = document.createElement("template");
    source.className = "ascii-source";
    container.append(source);
  }
  source.content.textContent = "";
  source.content.append(document.createTextNode(text));

  if (mentionsJSON) container.dataset.asciiMentions = mentionsJSON;
  else delete container.dataset.asciiMentions;
  delete container.__asciiMentionMap;
}
