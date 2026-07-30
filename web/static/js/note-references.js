import { KIND_COMMENT, KIND_NOTE, KIND_REPOST } from "./nostr-kinds.js";
import { extractAllNIP27References, applyAsciiMentionsToShell, rewriteASCIIMentions } from "./nip27.js";
import { displayName } from "./profile-parse.js";
import { relativeAge } from "./relative-time.js";
import {
  canonicalHex64,
  isCanonicalEventID,
  uniqueNonEmpty,
} from "./relay-utils.js";

/** Primary repost/quote tag reference (mirrors handlers.referencedEventRef). */
export function referencedEventRef(event) {
  let tagName = "";
  if (Number(event?.kind) === KIND_REPOST) tagName = "e";
  else if (Number(event?.kind) === KIND_NOTE) tagName = "q";
  else return { id: "", relay: "" };

  for (const tag of event?.tags || []) {
    if (!Array.isArray(tag) || tag.length < 2 || tag[0] !== tagName) continue;
    const id = canonicalHex64(String(tag[1] || "").trim());
    const relay = tag.length >= 3 ? String(tag[2] || "").trim() : "";
    return { id, relay };
  }
  return { id: "", relay: "" };
}

export function referencedEventID(event) {
  return referencedEventRef(event).id;
}

export function isSimpleRepost(event) {
  return Number(event?.kind) === KIND_REPOST;
}

export function isQuotePost(event) {
  return Number(event?.kind) === KIND_NOTE && referencedEventID(event) !== "";
}

export function referencedEventIDs(event) {
  let tagName = "";
  if (Number(event?.kind) === KIND_REPOST) tagName = "e";
  else if (Number(event?.kind) === KIND_NOTE) tagName = "q";
  else return [];

  const out = [];
  const seen = new Set();
  for (const tag of event?.tags || []) {
    if (!Array.isArray(tag) || tag.length < 2 || tag[0] !== tagName) continue;
    const id = canonicalHex64(String(tag[1] || "").trim());
    if (!isCanonicalEventID(id) || seen.has(id)) continue;
    seen.add(id);
    out.push(id);
  }
  return out;
}

/** NIP-27 event references in content with byte offsets. */
export function extractNIP27References(content) {
  return extractAllNIP27References(content)
    .filter((ref) => ref.eventID)
    .map((ref) => ({
      raw: ref.raw,
      start: ref.start,
      end: ref.end,
      eventID: ref.eventID,
      relays: ref.relays,
    }));
}

export function collectReferencedEventIDs(events) {
  const out = [];
  const seen = new Set();
  const add = (id) => {
    const hex = canonicalHex64(id);
    if (!isCanonicalEventID(hex) || seen.has(hex)) return;
    seen.add(hex);
    out.push(hex);
  };
  for (const event of events || []) {
    add(referencedEventID(event));
    for (const ref of extractNIP27References(event?.content)) {
      add(ref.eventID);
    }
  }
  return out;
}

export function relayHintsByReferencedID(events) {
  const hints = {};
  const add = (id, relay) => {
    const hex = canonicalHex64(id);
    const url = String(relay || "").trim();
    if (!isCanonicalEventID(hex) || !url) return;
    hints[hex] = uniqueNonEmpty([...(hints[hex] || []), url]);
  };
  for (const event of events || []) {
    const { id, relay } = referencedEventRef(event);
    add(id, relay);
    for (const ref of extractNIP27References(event?.content)) {
      for (const r of ref.relays || []) add(ref.eventID, r);
    }
  }
  return hints;
}

export function stripNIP27EventReferences(content, excludeIDs) {
  const excluded = new Set(
    (excludeIDs || []).map(canonicalHex64).filter((id) => isCanonicalEventID(id)),
  );
  if (!excluded.size) return String(content || "");
  const refs = extractNIP27References(content).filter((ref) => excluded.has(ref.eventID));
  if (!refs.length) return String(content || "");

  let out = "";
  let cursor = 0;
  let trimLeading = false;
  for (const ref of refs) {
    let segment = content.slice(cursor, ref.start);
    if (trimLeading) {
      segment = segment.replace(/^[ \t]+/, "");
      trimLeading = false;
    }
    out += segment;
    cursor = ref.end;
    trimLeading = true;
  }
  let tail = content.slice(cursor);
  if (trimLeading) tail = tail.replace(/^[ \t]+/, "");
  out += tail;
  return out.trim();
}

export function noteMainBodySourceText(event) {
  const content = String(event?.content || "");
  if (isSimpleRepost(event)) return "";
  return stripNIP27EventReferences(content, referencedEventIDs(event));
}

/** Decode NIP-18 embedded note JSON from a kind-6 repost content field. */
export function parseEmbeddedRepostEvent(content, expectedID = "") {
  const raw = String(content || "").trim();
  if (!raw.startsWith("{")) return null;
  try {
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object") return null;
    const kind = Number(parsed.kind);
    if (kind !== KIND_NOTE && kind !== KIND_COMMENT) return null;
    const id = canonicalHex64(String(parsed.id || "").trim());
    if (!isCanonicalEventID(id)) return null;
    const want = canonicalHex64(expectedID);
    if (want && isCanonicalEventID(want) && id !== want) return null;
    return {
      id,
      pubkey: canonicalHex64(String(parsed.pubkey || "").trim()),
      created_at: Number(parsed.created_at || 0),
      kind,
      tags: Array.isArray(parsed.tags) ? parsed.tags : [],
      content: String(parsed.content || ""),
      sig: String(parsed.sig || ""),
    };
  } catch {
    return null;
  }
}

export function imetaMediaItemsJSON(tags) {
  const out = [];
  const seen = new Set();
  for (const tag of tags || []) {
    if (!Array.isArray(tag) || tag[0] !== "imeta") continue;
    let url = "";
    let type = "";
    let width = 0;
    let height = 0;
    for (const part of tag.slice(1)) {
      const value = typeof part === "string" ? part.trim() : "";
      if (!value) continue;
      if (!url && value.startsWith("url ")) {
        const candidate = value.slice(4).trim();
        if (/^https?:\/\//i.test(candidate)) url = candidate;
        continue;
      }
      if (!type && value.startsWith("m ")) {
        type = imetaMediaTypeForURL(url, value.slice(2).trim());
        continue;
      }
      if (value.startsWith("dim ")) {
        const match = /^([1-9][0-9]{0,5})x([1-9][0-9]{0,5})$/.exec(value.slice(4).trim());
        if (match) {
          width = Number.parseInt(match[1], 10);
          height = Number.parseInt(match[2], 10);
        }
      }
    }
    if (!type) type = imetaMediaTypeForURL(url, "");
    if (!url || (type !== "image" && type !== "video") || seen.has(url)) continue;
    seen.add(url);
    out.push({
      url,
      type,
      ...(width > 0 && height > 0 ? { width, height } : {}),
    });
  }
  return out.length ? JSON.stringify(out) : "";
}

function imetaMediaTypeForURL(url, mime) {
  const value = String(mime || "").trim().toLowerCase();
  if (value.startsWith("image/")) return "image";
  if (value.startsWith("video/")) return "video";
  if (["jpg", "jpeg", "png", "gif", "webp", "avif", "svg", "svg+xml", "heic", "heif"].includes(value)) {
    return "image";
  }
  if (["mp4", "webm", "m4v", "mov", "quicktime", "ogv", "ogg"].includes(value)) {
    return "video";
  }
  const cleanURL = String(url || "").trim().toLowerCase().split(/[?#]/, 1)[0];
  if (/\.(png|jpe?g|gif|webp|avif|svg)$/.test(cleanURL)) return "image";
  if (/\.(mp4|webm|m4v|mov|ogv|ogg)$/.test(cleanURL)) return "video";
  return "";
}

export function resolveReferencedEvent(event, referencedByID) {
  const map = referencedByID instanceof Map ? referencedByID : new Map(Object.entries(referencedByID || {}));
  const { id: referenceID } = referencedEventRef(event);
  if (!referenceID) return { referenceID: "", reference: null };
  const loaded = map.get(referenceID);
  if (loaded?.id) return { referenceID, reference: loaded };
  if (isSimpleRepost(event)) {
    const embedded = parseEmbeddedRepostEvent(event.content, referenceID);
    if (embedded) return { referenceID, reference: embedded };
  }
  return { referenceID, reference: null };
}

export function inlineReferenceEvents(content, referencedByID) {
  const map = referencedByID instanceof Map ? referencedByID : new Map(Object.entries(referencedByID || {}));
  if (!map.size) return [];
  const out = [];
  const seen = new Set();
  for (const ref of extractNIP27References(content)) {
    if (!ref.eventID || seen.has(ref.eventID)) continue;
    const event = map.get(ref.eventID);
    if (!event) continue;
    seen.add(ref.eventID);
    out.push(event);
  }
  return out;
}

export async function hydrateReferencedEvents(events) {
  const ids = collectReferencedEventIDs(events);
  if (!ids.length) return new Map();
  const { fetchEventsByIDs } = await import("./relay-reads.js");
  const hints = relayHintsByReferencedID(events);
  const loaded = await fetchEventsByIDs(ids, { relayHintsByID: hints });
  const out = new Map();
  for (const event of loaded) {
    const id = canonicalHex64(event?.id);
    if (isCanonicalEventID(id)) out.set(id, event);
  }
  for (const event of events || []) {
    if (!isSimpleRepost(event)) continue;
    const { id } = referencedEventRef(event);
    if (!isCanonicalEventID(id) || out.has(id)) continue;
    const embedded = parseEmbeddedRepostEvent(event.content, id);
    if (embedded) out.set(id, embedded);
  }
  return out;
}

function appendReferenceTemplate(container, text, profilesByPubkey = {}) {
  const { text: rewritten } = rewriteASCIIMentions(String(text || ""), profilesByPubkey);
  const source = document.createElement("template");
  source.className = "ascii-reference-source";
  source.content.append(document.createTextNode(rewritten));
  container.append(source);
}

function appendInlineReferenceTemplates(container, content, referencedByID, profilesByPubkey) {
  for (const event of inlineReferenceEvents(content, referencedByID)) {
    const id = canonicalHex64(event.id);
    const pk = String(event.pubkey || "").toLowerCase();
    const profile = profilesByPubkey?.[pk] || { pubkey: pk };
    const { text: rewritten } = rewriteASCIIMentions(String(event.content || ""), profilesByPubkey);
    const tmpl = document.createElement("template");
    tmpl.className = "ascii-inline-reference-source";
    tmpl.dataset.asciiRefId = id;
    tmpl.dataset.asciiRefAuthor = displayName(profile);
    tmpl.dataset.asciiRefAge = relativeAge(event.created_at);
    tmpl.dataset.asciiRefThreadHref = `/thread/${id}`;
    tmpl.dataset.asciiRefReplyLabel = "0 replies";
    tmpl.content.append(document.createTextNode(rewritten));
    container.append(tmpl);
  }
}

/** Apply quote/repost datasets and reference templates to a note/reply shell. */
export function enrichNoteShell(container, event, referencedByID, profilesByPubkey = {}) {
  if (!container || !event) return;
  const refMode = isSimpleRepost(event) ? "repost" : isQuotePost(event) ? "quote" : "";
  if (!refMode) return;

  const { referenceID, reference } = resolveReferencedEvent(event, referencedByID);
  const hasReference = Boolean(reference?.id);

  container.dataset.asciiRefMode = refMode;
  if (hasReference) {
    const refProfile =
      profilesByPubkey[String(reference.pubkey || "").toLowerCase()] || { pubkey: reference.pubkey };
    container.dataset.asciiRefAuthor = displayName(refProfile);
    container.dataset.asciiRefAge = relativeAge(reference.created_at);
    container.dataset.asciiRefThreadHref = `/thread/${canonicalHex64(reference.id)}`;
  } else if (referenceID) {
    container.dataset.asciiRefAuthor = referenceID.slice(0, 12);
    container.dataset.asciiRefAge = "?";
    container.dataset.asciiRefThreadHref = `/thread/${referenceID}`;
  }
  container.dataset.asciiRefReplyLabel = "0 replies";
  container.dataset.asciiRefReplyCount = "0";
  if (hasReference) {
    const refImeta = imetaMediaItemsJSON(reference.tags);
    if (refImeta) container.dataset.asciiRefImetaMedia = refImeta;
    else delete container.dataset.asciiRefImetaMedia;
  } else {
    delete container.dataset.asciiRefImetaMedia;
  }

  container.querySelector(":scope > .ascii-reference-source")?.remove();
  const refText = hasReference
    ? String(reference.content || "")
    : referenceID
      ? "[reposted note unavailable on current relays]"
      : "";
  if (refMode === "quote" || refMode === "repost") {
    appendReferenceTemplate(container, refText);
  }

  const map =
    referencedByID instanceof Map ? referencedByID : new Map(Object.entries(referencedByID || {}));
  container.querySelectorAll(":scope > .ascii-inline-reference-source").forEach((node) => node.remove());
  const bodySource = noteMainBodySourceText(event);
  appendInlineReferenceTemplates(container, bodySource, map, profilesByPubkey);

  const extraSources = [];
  if (hasReference && reference?.content) extraSources.push(reference.content);
  for (const inline of inlineReferenceEvents(bodySource, map)) {
    extraSources.push(inline.content);
  }
  applyAsciiMentionsToShell(container, bodySource, profilesByPubkey, extraSources);
}
