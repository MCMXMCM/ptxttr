import { KIND_NOTE } from "./nostr-kinds.js";
import { isQuotePost } from "./note-references.js";
import { displayName } from "./profile-parse.js";
import { parentID } from "./thread-tags.js";
import { canonicalHex64, normalizePubkey, profilePath } from "./relay-utils.js";

function shortPubkey(pubkey) {
  if (!pubkey) return "";
  if (pubkey.length <= 12) return pubkey;
  return `${pubkey.slice(0, 8)}…${pubkey.slice(-4)}`;
}

function escapeHtml(value) {
  return String(value || "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

/** Mirrors thread.RootID (kind-1 only; empty when no e-tags). */
export function feedThreadRootID(event) {
  if (Number(event?.kind) !== KIND_NOTE) return "";
  for (const tag of event?.tags || []) {
    if (!Array.isArray(tag) || tag.length < 2 || tag[0] !== "e") continue;
    const marker = tag.length >= 4 ? String(tag[3] || "") : "";
    if (marker.toLowerCase() === "root") return canonicalHex64(tag[1]);
  }
  for (const tag of event?.tags || []) {
    if (!Array.isArray(tag) || tag.length < 2 || tag[0] !== "e") continue;
    return canonicalHex64(tag[1]);
  }
  return "";
}

/** Mirrors httpx.isFeedThreadReply. */
export function isFeedThreadReply(event) {
  if (Number(event?.kind) !== KIND_NOTE) return false;
  const root = feedThreadRootID(event);
  const parent = canonicalHex64(parentID(root, event));
  if (!parent) return false;
  return parent !== canonicalHex64(event?.id);
}

/** Mirrors httpx.replyContextVisible. */
export function replyContextVisible(event) {
  return isFeedThreadReply(event) && !isQuotePost(event);
}

/** Mirrors httpx.replyContextTargets. */
export function replyContextTargets(event) {
  const author = normalizePubkey(event?.pubkey);
  const seen = new Set();
  const out = [];
  for (const tag of event?.tags || []) {
    if (!Array.isArray(tag) || tag.length < 2 || tag[0] !== "p") continue;
    const pk = normalizePubkey(tag[1]);
    if (!pk || seen.has(pk)) continue;
    if (author && pk === author) continue;
    seen.add(pk);
    out.push(pk);
  }
  return out;
}

function profileLabel(profilesByPubkey, pubkey) {
  const pk = normalizePubkey(pubkey);
  const profile = profilesByPubkey?.[pk];
  if (profile) return displayName(profile);
  return shortPubkey(pk);
}

function replyMentionLinkHTML(profilesByPubkey, pubkey) {
  const pk = normalizePubkey(pubkey);
  if (pk.length !== 64) return "";
  const label = profileLabel(profilesByPubkey, pk);
  return `<a href="${escapeHtml(profilePath(pk))}" data-relay-aware>@${escapeHtml(label)}</a>`;
}

/** Safe HTML for the reply context row (mirrors httpx.replyContextHTML). */
export function replyContextHTML(event, profilesByPubkey = {}) {
  if (!replyContextVisible(event)) return "";

  const root = feedThreadRootID(event);
  const parent = canonicalHex64(parentID(root, event));
  const targets = replyContextTargets(event);
  let html = `<span class="note-feed-context-lead">Replying to </span>`;

  if (!targets.length) {
    if (parent.length !== 64) return html;
    return `${html}<a href="/thread/${escapeHtml(parent)}" data-relay-aware>thread</a>`;
  }

  const show = targets.length > 2 ? targets.slice(0, 2) : targets;
  const rest = targets.length > 2 ? targets.length - 2 : 0;
  html += show
    .map((pk) => replyMentionLinkHTML(profilesByPubkey, pk))
    .filter(Boolean)
    .join(" ");
  if (rest > 0) {
    html += ` <span class="note-feed-context-tail">and ${rest} other${rest === 1 ? "" : "s"}</span>`;
  }
  return html;
}

/** Safe HTML for the repost banner (mirrors httpx.repostContextHTML). */
export function repostContextHTML(event, profilesByPubkey = {}) {
  if (Number(event?.kind) !== 6) return "";
  const pk = normalizePubkey(event?.pubkey);
  const name = escapeHtml(profileLabel(profilesByPubkey, pk));
  return `<span class="note-feed-context-repost-inner">${name} reposted</span>`;
}

/** Adds feed reply/repost context elements expected by ascii.js (server partial parity). */
export function appendFeedContextElements(container, event, profilesByPubkey = {}) {
  if (!container || !event) return;

  const repostHTML = repostContextHTML(event, profilesByPubkey);
  if (repostHTML) {
    const banner = document.createElement("p");
    banner.className = "note-feed-context note-feed-context--repost";
    banner.innerHTML = repostHTML;
    container.append(banner);
  }

  const replyHTML = replyContextHTML(event, profilesByPubkey);
  if (replyHTML) {
    const tmpl = document.createElement("template");
    tmpl.className = "note-reply-context-tmpl";
    tmpl.innerHTML = replyHTML;
    container.append(tmpl);
  }
}
