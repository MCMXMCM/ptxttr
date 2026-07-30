import { nip19 } from "../lib/nostr-tools.js";
import { normalizeRelayList } from "./relay-config.js";

const HEX64 = /^[0-9a-f]{64}$/;

/** True when value is a lowercase 64-char hex event/note id. */
export function isCanonicalEventID(value) {
  const token = String(value || "").trim();
  return token.length === 64 && HEX64.test(token);
}

/**
 * Resolve a path segment or NIP-19 code to a canonical hex event id.
 * Returns null when the input is not a recognizable event reference.
 */
export function resolveEventID(raw) {
  let token = String(raw || "").trim();
  if (!token) return null;
  if (token.toLowerCase().startsWith("nostr:")) {
    token = token.slice(6).trim();
    if (!token) return null;
  }
  if (token.length === 64 && HEX64.test(token.toLowerCase())) {
    return { eventID: token.toLowerCase(), relays: [] };
  }
  try {
    const decoded = nip19.decode(token);
    if (decoded.type === "note") {
      const eventID = canonicalHex64(String(decoded.data || ""));
      return isCanonicalEventID(eventID) ? { eventID, relays: [] } : null;
    }
    if (decoded.type === "nevent") {
      const data = decoded.data || {};
      const eventID = canonicalHex64(String(data.id || ""));
      if (!isCanonicalEventID(eventID)) return null;
      const relays = normalizeRelayList(Array.isArray(data.relays) ? data.relays : []);
      const eventKind = typeof data.kind === "number" ? data.kind : undefined;
      const author = normalizePubkey(data.author || "");
      return { eventID, eventKind, relays, author };
    }
  } catch {
    // not a NIP-19 event code
  }
  return null;
}

/** Canonical `/thread/{hex}` URL when the path uses note/nevent/bech32. */
export function canonicalThreadURL(urlLike) {
  const url = new URL(urlLike, window.location.origin);
  const match = url.pathname.match(/^\/thread\/([^/]+)/);
  if (!match) return url;
  const resolved = resolveEventID(match[1]);
  if (!resolved?.eventID || match[1].toLowerCase() === resolved.eventID) return url;
  const next = new URL(url);
  next.pathname = `/thread/${resolved.eventID}`;
  return next;
}

export function canonicalHex64(value) {
  const token = String(value || "").trim();
  if (token.length !== 64 || !HEX64.test(token.toLowerCase())) return token;
  return token.toLowerCase();
}

export function normalizePubkey(value) {
  const raw = String(value || "").trim();
  if (!raw) return "";
  if (HEX64.test(raw.toLowerCase())) return raw.toLowerCase();
  try {
    const decoded = nip19.decode(raw);
    if (decoded.type === "npub") {
      if (typeof decoded.data === "string") {
        return canonicalHex64(decoded.data);
      }
      if (decoded.data instanceof Uint8Array) {
        return canonicalHex64([...decoded.data].map((b) => b.toString(16).padStart(2, "0")).join(""));
      }
    }
    if (decoded.type === "nprofile") {
      return canonicalHex64(decoded.data?.pubkey || "");
    }
  } catch {
    // ignore
  }
  return "";
}

export function profilePath(pubkey, relays = []) {
  const pk = normalizePubkey(pubkey);
  if (!pk) return "/u/";
  const relayList = normalizeRelayList(Array.isArray(relays) ? relays : [relays]);
  try {
    if (relayList.length) {
      return `/u/${encodeURIComponent(nip19.nprofileEncode({ pubkey: pk, relays: relayList }))}`;
    }
    return `/u/${encodeURIComponent(nip19.npubEncode(pk))}`;
  } catch {
    return `/u/${encodeURIComponent(pk)}`;
  }
}

export function dedupeEventsByID(events) {
  const out = new Map();
  for (const event of events || []) {
    const id = canonicalHex64(event?.id);
    if (!id) continue;
    const existing = out.get(id);
    if (!existing || Number(event.created_at || 0) >= Number(existing.created_at || 0)) {
      out.set(id, { ...event, id });
    }
  }
  return [...out.values()];
}

export function latestReplaceableEvent(events, kind) {
  let best = null;
  for (const event of events || []) {
    if (Number(event?.kind) !== kind) continue;
    if (!best || Number(event.created_at || 0) > Number(best.created_at || 0)) {
      best = event;
    }
  }
  return best;
}

export function tagValues(event, name) {
  const out = [];
  for (const tag of event?.tags || []) {
    if (!Array.isArray(tag) || tag.length < 2 || tag[0] !== name) continue;
    const value = String(tag[1] || "").trim();
    if (value) out.push(value);
  }
  return out;
}

export function firstTagValue(event, name) {
  return tagValues(event, name)[0] || "";
}

export function eventsWithTag(events, tagName, tagValue, { kind } = {}) {
  const want = canonicalHex64(tagValue) || String(tagValue || "").trim().toLowerCase();
  return (events || []).filter((event) => {
    if (kind != null && Number(event.kind) !== kind) return false;
    return (event.tags || []).some((tag) => {
      if (!Array.isArray(tag) || tag.length < 2 || tag[0] !== tagName) return false;
      const value = canonicalHex64(tag[1]) || String(tag[1] || "").trim().toLowerCase();
      return value === want;
    });
  });
}

export function bookmarkEntries(event, max = 500) {
  if (!event || Number(event.kind) !== 10003) return [];
  const seen = new Set();
  const out = [];
  for (const tag of event.tags || []) {
    if (!Array.isArray(tag) || tag.length < 2 || tag[0] !== "e") continue;
    const id = canonicalHex64(tag[1]);
    if (!id || seen.has(id)) continue;
    seen.add(id);
    const relay = tag.length >= 3 ? String(tag[2] || "").trim() : "";
    out.push({ id, relay });
    if (max > 0 && out.length >= max) break;
  }
  return out;
}

export function mutePubkeys(event) {
  if (!event || Number(event.kind) !== 10000) return [];
  const seen = new Set();
  const out = [];
  for (const tag of event.tags || []) {
    if (!Array.isArray(tag) || tag.length < 2 || tag[0] !== "p") continue;
    const pk = normalizePubkey(tag[1]);
    if (!pk || seen.has(pk)) continue;
    seen.add(pk);
    out.push(pk);
  }
  return out;
}

export function followPubkeys(event) {
  if (!event || Number(event.kind) !== 3) return [];
  const seen = new Set();
  const out = [];
  for (const tag of event.tags || []) {
    if (!Array.isArray(tag) || tag.length < 2 || tag[0] !== "p") continue;
    const pk = normalizePubkey(tag[1]);
    if (!pk || seen.has(pk)) continue;
    seen.add(pk);
    out.push(pk);
  }
  return out;
}

export function followRelayHints(event, max = 600) {
  if (!event || Number(event.kind) !== 3) return new Map();
  const out = new Map();
  for (const tag of event.tags || []) {
    if (!Array.isArray(tag) || tag.length < 3 || tag[0] !== "p") continue;
    const pk = normalizePubkey(tag[1]);
    const relay = String(tag[2] || "").trim();
    if (!pk || !relay || out.has(pk)) continue;
    out.set(pk, relay);
    if (max > 0 && out.size >= max) break;
  }
  return out;
}

export function relayHintsFromKind10002(event) {
  const write = [];
  const read = [];
  const any = [];
  if (!event || Number(event.kind) !== 10002) {
    return { write, read, any };
  }
  for (const tag of event.tags || []) {
    if (!Array.isArray(tag) || tag.length < 2 || tag[0] !== "r") continue;
    const url = String(tag[1] || "").trim();
    if (!url) continue;
    const marker = tag.length >= 3 ? String(tag[2] || "").trim() : "";
    if (marker === "write") write.push(url);
    else if (marker === "read") read.push(url);
    else any.push(url);
  }
  return {
    write: normalizeRelayList(write),
    read: normalizeRelayList(read),
    any: normalizeRelayList(any),
  };
}

export function authorWriteRelaysFromKind10002(event) {
  const hints = relayHintsFromKind10002(event);
  return normalizeRelayList([...(hints.write || []), ...(hints.any || [])]);
}

export function authorReadRelaysFromKind10002(event) {
  const hints = relayHintsFromKind10002(event);
  return normalizeRelayList([...(hints.read || []), ...(hints.any || [])]);
}

export function uniqueNonEmpty(values) {
  const out = [];
  const seen = new Set();
  for (const value of values || []) {
    const token = String(value || "").trim();
    if (!token || seen.has(token)) continue;
    seen.add(token);
    out.push(token);
  }
  return out;
}

export function participantPubkeys(event) {
  const out = [];
  const seen = new Set();
  const add = (value) => {
    const pk = normalizePubkey(value);
    if (!pk || seen.has(pk)) return;
    seen.add(pk);
    out.push(pk);
  };
  add(event?.pubkey);
  for (const tag of event?.tags || []) {
    if (!Array.isArray(tag) || tag.length < 2) continue;
    if (tag[0] === "p") add(tag[1]);
  }
  return out;
}

export function pubkeyFromProfilePath(pathname) {
  const match = String(pathname || "").match(/^\/u\/([^/?#]+)/);
  if (!match) return "";
  try {
    return normalizePubkey(decodeURIComponent(match[1]));
  } catch {
    return normalizePubkey(match[1]);
  }
}

export function summarizeRelayFailures(results) {
  const failed = (results || []).filter((row) => !row.accepted);
  if (!failed.length) return "No relay accepted this event.";
  const notes = failed.slice(0, 3).map((row) => {
    const reason = String(row.error || row.message || "rejected without reason").trim();
    return `${row.relay_url}: ${reason}`;
  });
  return `No relay accepted this event. ${notes.join("; ")}`;
}
