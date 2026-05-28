import { refreshAscii } from "./ascii.js";
import { wireAvatarImageFallbacks } from "./layout.js";
import { fetchWithSession, shortPubkey } from "./session.js";

const maxProfileBatch = 32;
const inFlight = new Set();
const emptyProfiles = new Set();

function normalizePubkeyToken(value) {
  const token = String(value || "").trim().toLowerCase();
  return /^[0-9a-f]{64}$/.test(token) ? token : "";
}

function pubkeyFromHref(href) {
  const value = String(href || "");
  const match = value.match(/\/u\/([^/?#]+)/);
  return normalizePubkeyToken(decodeURIComponent(match?.[1] || ""));
}

function cardPubkey(card) {
  return normalizePubkeyToken(card?.dataset?.replyPubkey) ||
    pubkeyFromHref(card?.dataset?.asciiUserHref) ||
    pubkeyFromHref(card?.querySelector?.("a[href^='/u/']")?.getAttribute("href"));
}

function profileLabel(profile) {
  return String(profile?.display_name || profile?.name || "").trim();
}

function profileRoots(root) {
  if (Array.isArray(root)) {
    const roots = root.filter((item) => item instanceof Element || item === document);
    return roots.length ? roots : [document];
  }
  return root instanceof Element || root === document ? [root] : [document];
}

function profileItems(root, selector) {
  const items = [];
  if (root instanceof Element && root.matches(selector)) items.push(root);
  root.querySelectorAll(selector).forEach((item) => items.push(item));
  return items;
}

function isFallbackProfileLabel(label, pubkey) {
  const value = String(label || "").trim();
  return !value || value === shortPubkey(pubkey) || value === pubkey.slice(0, 12);
}

function isFallbackAuthor(card, pubkey) {
  return isFallbackProfileLabel(card?.dataset?.asciiAuthor, pubkey);
}

function needsProfileRefresh(card, pubkey) {
  if (!pubkey || inFlight.has(pubkey) || emptyProfiles.has(pubkey)) return false;
  return isFallbackAuthor(card, pubkey);
}

function collectMissingPubkeys(root) {
  const pubkeys = [];
  const seen = new Set();
  profileRoots(root).forEach((scope) => {
    profileItems(scope, "[data-ascii-kind], [data-thread-tree-note], a.thread-person").forEach((item) => {
      const isThreadPerson = item.matches("a.thread-person");
      const pubkey = isThreadPerson ? pubkeyFromHref(item.getAttribute("href")) : cardPubkey(item);
      if (!pubkey || seen.has(pubkey)) return;
      if (isThreadPerson ? !isThreadPersonFallbackName(item, pubkey) : !needsProfileRefresh(item, pubkey)) return;
      seen.add(pubkey);
      pubkeys.push(pubkey);
    });
  });
  return pubkeys.slice(0, maxProfileBatch);
}

function isThreadPersonFallbackName(linkEl, pubkey) {
  const label = String(linkEl.querySelector("strong")?.textContent || "").trim();
  return isFallbackProfileLabel(label, pubkey);
}

function ensureAvatarImage(anchor, avatarURL, className = "") {
  if (!anchor || !avatarURL) return;
  anchor.querySelectorAll(".thread-tree-avatar-fallback, .thread-person-avatar-fallback").forEach((el) => el.remove());
  let img = anchor.querySelector("img");
  if (!img) {
    img = document.createElement("img");
    img.alt = "";
    img.loading = "lazy";
    img.decoding = "async";
    anchor.prepend(img);
  }
  if (className) img.className = className;
  if (img.getAttribute("src") !== avatarURL) img.src = avatarURL;
}

function applyAsciiProfile(card, pubkey, profile) {
  const label = profileLabel(profile);
  const avatarURL = String(profile?.avatar_url || "").trim();
  let changed = false;
  if (label && isFallbackAuthor(card, pubkey)) {
    card.dataset.asciiAuthor = label;
    changed = true;
  }
  if (avatarURL && card.dataset.asciiAvatar !== avatarURL) {
    card.dataset.asciiAvatar = avatarURL;
    changed = true;
  }
  if (avatarURL) {
    const avatar = card.querySelector(":scope > .ascii-card .note-feed-avatar, :scope > .comment-avatar, :scope > .note-avatar");
    ensureAvatarImage(avatar, avatarURL);
  }
  return changed;
}

function applyTreeProfile(card, pubkey, profile) {
  const label = profileLabel(profile);
  const avatarURL = String(profile?.avatar_url || "").trim();
  if (label) {
    const nameLink = card.querySelector(".hn-comhead a[href^='/u/']");
    if (nameLink && isFallbackProfileLabel(nameLink.textContent, pubkey)) {
      nameLink.textContent = label;
    }
  }
  if (avatarURL) {
    ensureAvatarImage(card.querySelector(".hn-tree-avatar"), avatarURL, "thread-tree-avatar");
  }
}

function applyThreadPersonProfile(linkEl, pubkey, profile) {
  const label = profileLabel(profile);
  const avatarURL = String(profile?.avatar_url || "").trim();
  const strong = linkEl.querySelector("strong");
  if (label && strong && isThreadPersonFallbackName(linkEl, pubkey)) {
    strong.textContent = label;
  }
  if (avatarURL) ensureAvatarImage(linkEl, avatarURL);
}

function applyProfiles(root, profiles) {
  let asciiChanged = false;
  const roots = profileRoots(root);
  roots.forEach((scope) => {
    profileItems(scope, "[data-ascii-kind]").forEach((card) => {
      const pubkey = cardPubkey(card);
      const profile = profiles[pubkey];
      if (profile) asciiChanged = applyAsciiProfile(card, pubkey, profile) || asciiChanged;
    });
    profileItems(scope, "[data-thread-tree-note]").forEach((card) => {
      const pubkey = cardPubkey(card);
      const profile = profiles[pubkey];
      if (profile) applyTreeProfile(card, pubkey, profile);
    });
    profileItems(scope, "a.thread-person").forEach((linkEl) => {
      const pubkey = pubkeyFromHref(linkEl.getAttribute("href"));
      const profile = profiles[pubkey];
      if (profile) applyThreadPersonProfile(linkEl, pubkey, profile);
    });
  });
  if (asciiChanged) roots.forEach((scope) => refreshAscii(scope));
  roots.forEach((scope) => wireAvatarImageFallbacks(scope));
}

async function fetchProfiles(pubkeys) {
  const url = new URL("/api/profiles", window.location.origin);
  pubkeys.forEach((pubkey) => url.searchParams.append("pubkey", pubkey));
  const response = await fetchWithSession(url.toString());
  if (!response.ok) return {};
  return response.json();
}

export async function refreshVisibleNoteProfiles(root = document) {
  const pubkeys = collectMissingPubkeys(root);
  if (!pubkeys.length) return;
  pubkeys.forEach((pubkey) => inFlight.add(pubkey));
  try {
    const profiles = await fetchProfiles(pubkeys);
    pubkeys.forEach((pubkey) => {
      const profile = profiles?.[pubkey];
      if (!profileLabel(profile) && !String(profile?.avatar_url || "").trim()) {
        emptyProfiles.add(pubkey);
      }
    });
    applyProfiles(root, profiles || {});
  } catch {
    // Keep server-rendered fallbacks; a later route refresh can try again.
  } finally {
    pubkeys.forEach((pubkey) => inFlight.delete(pubkey));
  }
}
