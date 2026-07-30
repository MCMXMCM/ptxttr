import { relayHintsFromNoteElement } from "./dom-relay-hints.js";
import { normalizeRelayList } from "./relay-config.js";
import { normalizePubkey } from "./relay-utils.js";

const PROFILE_PREVIEW_TTL_MS = 2 * 60 * 1000;
const previewsByPubkey = new Map();

function trimText(value, max = 2048) {
  const text = String(value || "").trim();
  return text.length > max ? text.slice(0, max) : text;
}

function relayHintsFromNode(node, card) {
  return normalizeRelayList(relayHintsFromNoteElement(card || node));
}

function eventFromNode(card) {
  const raw = String(card?.dataset?.asciiEvent || "").trim();
  if (!raw) return null;
  try {
    const event = JSON.parse(raw);
    return event?.id && event?.pubkey ? event : null;
  } catch {
    return null;
  }
}

function profilePreviewFromNode(node, pubkey) {
  const card = node?.closest?.(".note, .comment, [data-thread-tree-note]") || null;
  if (!card) return null;
  const authorLink = node?.closest?.("a[href^='/u/']") || card?.querySelector?.("a[href^='/u/']");
  const linkText = trimText(authorLink?.textContent || "", 128);
  const display = trimText(card?.dataset?.asciiAuthor || linkText, 128);
  const avatar = trimText(card?.dataset?.asciiAvatar || "", 2048);
  const relay_hints = relayHintsFromNode(node, card);
  const event = eventFromNode(card);
  if (!display && !avatar && normalizePubkey(event?.pubkey) !== pubkey) return null;
  const preview = {
    pubkey,
    display_name: display,
    name: display,
    avatar_url: avatar,
    picture: avatar,
    relay_hints,
    savedAt: Date.now(),
  };
  if (normalizePubkey(event?.pubkey) === pubkey) preview.timeline_event = event;
  return preview;
}

export function rememberProfileRoutePreviewFromLink(link) {
  const href = String(link?.getAttribute?.("href") || link?.href || "").trim();
  const match = href.match(/\/u\/([^/?#]+)/);
  const pubkey = normalizePubkey(decodeURIComponent(match?.[1] || ""));
  if (!pubkey) return null;
  const preview = profilePreviewFromNode(link, pubkey);
  if (!preview) return null;
  previewsByPubkey.set(pubkey, preview);
  return preview;
}

export function profileRoutePreview(pubkey) {
  const pk = normalizePubkey(pubkey);
  if (!pk) return null;
  const preview = previewsByPubkey.get(pk);
  if (!preview) return null;
  if (Date.now() - Number(preview.savedAt || 0) > PROFILE_PREVIEW_TTL_MS) {
    previewsByPubkey.delete(pk);
    return null;
  }
  return preview;
}
