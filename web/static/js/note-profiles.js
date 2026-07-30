import { refreshAscii } from "./ascii.js";
import { normalizeThreadPersonAvatars, wireAvatarImageFallbacks } from "./layout.js";
import { mentionPubkeysForEvent } from "./note-mention-pubkeys.js";
import { applyAsciiMentionsToShell } from "./nip27.js";
import { noteMainBodySourceText } from "./note-references.js";
import { avatarRetryURL, preferredAvatarURL, profileAvatarURLsMatch } from "./profile-parse.js";
import { briefBioText } from "./thread-render-helpers.js";
import { fetchProfiles } from "./relay-reads.js";
import { normalizePubkey } from "./relay-utils.js";
import { fetchWithSession, shortPubkey } from "./session.js";
import { setAvatarImageSource } from "./avatar-cache.js";
import {
  PROFILE_MEMORY_CACHE_UPDATED_EVENT,
  cachedProfiles,
  rememberProfiles,
} from "./profile-memory-cache.js";

const maxProfileBatch = 32;
const inFlight = new Set();
const emptyProfiles = new Set();

function pubkeyFromHref(href) {
  const value = String(href || "");
  const match = value.match(/\/u\/([^/?#]+)/);
  return normalizePubkey(decodeURIComponent(match?.[1] || ""));
}

export function noteProfilePubkey(card) {
  return normalizePubkey(card?.dataset?.replyPubkey) ||
    pubkeyFromHref(card?.dataset?.asciiUserHref) ||
    pubkeyFromHref(card?.querySelector?.("a[href^='/u/']")?.getAttribute("href"));
}

function eventFromCard(card) {
  const raw = String(card?.dataset?.asciiEvent || "").trim();
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw);
    return parsed && typeof parsed === "object" ? parsed : null;
  } catch {
    return null;
  }
}

function profileLabel(profile) {
  return String(profile?.display_name || profile?.name || "").trim();
}

function profileHasRenderableFields(profile) {
  return Boolean(profileLabel(profile) || preferredAvatarURL(profile));
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

function avatarElementForProfileItem(item) {
  if (!item) return null;
  if (item.matches?.("a.thread-person")) return item;
  return item.querySelector?.(
    ":scope > .ascii-card .note-feed-avatar, :scope > .comment-avatar, :scope > .note-avatar, :scope .hn-tree-avatar",
  );
}

function avatarNeedsProfileRefresh(item) {
  const avatar = avatarElementForProfileItem(item);
  if (!avatar) return false;
  const img = avatar.matches?.("img") ? avatar : avatar.querySelector?.("img");
  if (!(img instanceof HTMLImageElement)) return true;
  if (img.complete && img.naturalWidth === 0) return true;
  const current = img.dataset.ptxtAvatarOriginalSrc || img.currentSrc || img.getAttribute("src") || "";
  const expected = item?.dataset?.asciiAvatar || "";
  return Boolean(expected && current && !profileAvatarURLsMatch(current, expected));
}

function needsProfileRefresh(card, pubkey) {
  if (!pubkey || inFlight.has(pubkey)) return false;
  if (emptyProfiles.has(pubkey) && !profileHasRenderableFields(cachedProfiles([pubkey])?.[pubkey])) return false;
  return isFallbackAuthor(card, pubkey) || avatarNeedsProfileRefresh(card);
}

function collectMissingPubkeys(root) {
  const pubkeys = [];
  const seen = new Set();
  const pushPubkey = (pubkey) => {
    if (!pubkey || seen.has(pubkey) || inFlight.has(pubkey)) return;
    if (emptyProfiles.has(pubkey) && !profileHasRenderableFields(cachedProfiles([pubkey])?.[pubkey])) return;
    seen.add(pubkey);
    pubkeys.push(pubkey);
  };
  profileRoots(root).forEach((scope) => {
    profileItems(scope, "[data-ascii-kind], [data-thread-tree-note], a.thread-person").forEach((item) => {
      const isThreadPerson = item.matches("a.thread-person");
      const pubkey = isThreadPerson ? pubkeyFromHref(item.getAttribute("href")) : noteProfilePubkey(item);
      if (
        isThreadPerson
          ? !isThreadPersonFallbackName(item, pubkey) && !avatarNeedsProfileRefresh(item)
          : !needsProfileRefresh(item, pubkey)
      ) return;
      pushPubkey(pubkey);
    });
    profileItems(scope, "[data-ascii-kind]").forEach((card) => {
      const event = eventFromCard(card);
      mentionPubkeysForEvent(event).forEach((pubkey) => pushPubkey(pubkey));
    });
  });
  return pubkeys.slice(0, maxProfileBatch);
}

function isThreadPersonFallbackName(linkEl, pubkey) {
  const label = String(linkEl.querySelector("strong")?.textContent || "").trim();
  return isFallbackProfileLabel(label, pubkey);
}

function ensureAvatarImage(anchor, avatarURL, className = "", retryURL = "") {
  if (!anchor || !avatarURL) return;
  const isThreadPerson = anchor.matches?.("a.thread-person");
  anchor.querySelectorAll(
    isThreadPerson ? ":scope > .thread-person-avatar-fallback" : ".thread-tree-avatar-fallback",
  ).forEach((el) => el.remove());
  let img = isThreadPerson
    ? Array.from(anchor.children).find((child) => child instanceof HTMLImageElement)
    : anchor.querySelector("img");
  if (!img) {
    img = document.createElement("img");
    img.alt = "";
    img.loading = "lazy";
    img.decoding = "async";
    anchor.prepend(img);
  }
  if (className) img.className = className;
  setAvatarImageSource(img, avatarURL, { retryURL });
  if (isThreadPerson) normalizeThreadPersonAvatars(anchor);
}

function applyAsciiProfile(card, pubkey, profile) {
  const label = profileLabel(profile);
  const avatarURL = preferredAvatarURL(profile);
  const retryURL = avatarRetryURL(profile);
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
    ensureAvatarImage(avatar, avatarURL, "", retryURL);
  }
  return changed;
}

function applyTreeProfile(card, pubkey, profile) {
  const label = profileLabel(profile);
  const avatarURL = preferredAvatarURL(profile);
  const retryURL = avatarRetryURL(profile);
  if (label) {
    const nameLink = card.querySelector(".hn-comhead a[href^='/u/']");
    if (nameLink && isFallbackProfileLabel(nameLink.textContent, pubkey)) {
      nameLink.textContent = label;
    }
  }
  if (avatarURL) {
    ensureAvatarImage(card.querySelector(".hn-tree-avatar"), avatarURL, "thread-tree-avatar", retryURL);
  }
}

function applyThreadPersonProfile(linkEl, pubkey, profile) {
  const label = profileLabel(profile);
  const avatarURL = preferredAvatarURL(profile);
  const retryURL = avatarRetryURL(profile);
  const strong = linkEl.querySelector("strong");
  if (label && strong && isThreadPersonFallbackName(linkEl, pubkey)) {
    strong.textContent = label;
  }
  if (avatarURL) ensureAvatarImage(linkEl, avatarURL, "", retryURL);
  const about = briefBioText(profile?.about);
  let aboutEl = linkEl.querySelector(".thread-person-about");
  if (about) {
    if (!aboutEl) {
      aboutEl = document.createElement("span");
      aboutEl.className = "muted thread-person-about";
      const meta = linkEl.querySelector(".thread-person-meta");
      if (meta && strong) strong.after(aboutEl);
      else if (meta) meta.append(aboutEl);
    }
    aboutEl.textContent = about;
  } else if (aboutEl) {
    aboutEl.remove();
  }
}

function asciiSourceText(card) {
  const source = card?.querySelector?.(":scope > .ascii-source");
  return String(source?.content?.textContent || "");
}

function mentionExtraSources(card) {
  const sources = [];
  card?.querySelectorAll?.(":scope > .ascii-reference-source, :scope > .ascii-inline-reference-source")
    ?.forEach((node) => {
      const text = String(node?.content?.textContent || "");
      if (text) sources.push(text);
    });
  return sources;
}

function applyAsciiMentionProfiles(card, profiles) {
  const event = eventFromCard(card);
  const mentionPubkeys = mentionPubkeysForEvent(event);
  if (!mentionPubkeys.length) return false;
  if (!mentionPubkeys.some((pubkey) => profileLabel(profiles?.[pubkey]))) return false;

  const previousText = asciiSourceText(card);
  const previousMentions = String(card?.dataset?.asciiMentions || "");
  applyAsciiMentionsToShell(card, noteMainBodySourceText(event), profiles, mentionExtraSources(card));
  return previousText !== asciiSourceText(card) || previousMentions !== String(card?.dataset?.asciiMentions || "");
}

function applyProfiles(root, profiles) {
  const changedAsciiCards = new Set();
  const roots = profileRoots(root);
  roots.forEach((scope) => {
    profileItems(scope, "[data-ascii-kind]").forEach((card) => {
      const pubkey = noteProfilePubkey(card);
      const profile = profiles[pubkey];
      if (profile && applyAsciiProfile(card, pubkey, profile)) changedAsciiCards.add(card);
      if (applyAsciiMentionProfiles(card, profiles)) changedAsciiCards.add(card);
    });
    profileItems(scope, "[data-thread-tree-note]").forEach((card) => {
      const pubkey = noteProfilePubkey(card);
      const profile = profiles[pubkey];
      if (profile) applyTreeProfile(card, pubkey, profile);
    });
    profileItems(scope, "a.thread-person").forEach((linkEl) => {
      const pubkey = pubkeyFromHref(linkEl.getAttribute("href"));
      const profile = profiles[pubkey];
      if (profile) applyThreadPersonProfile(linkEl, pubkey, profile);
    });
  });
  changedAsciiCards.forEach((card) => refreshAscii(card));
  roots.forEach((scope) => wireAvatarImageFallbacks(scope));
}

function applyProfileMemoryCacheUpdate(event) {
  const profile = event?.detail?.profile;
  const pubkey = normalizePubkey(profile?.pubkey);
  if (!pubkey || typeof document === "undefined") return;
  emptyProfiles.delete(pubkey);
  applyProfiles(document, { [pubkey]: profile });
}

if (typeof window !== "undefined" && typeof window.addEventListener === "function") {
  window.addEventListener(PROFILE_MEMORY_CACHE_UPDATED_EVENT, applyProfileMemoryCacheUpdate);
}

// Resolve profile metadata through the server projection first, then wait for a
// relay lookup only for authors the server does not know. Feed refreshes use
// this before exposing a pending batch so their first rendered frame already
// has the same identity data as a settled note card.
export async function fetchNoteProfiles(pubkeys) {
  const normalized = [...new Set((pubkeys || []).map(normalizePubkey).filter(Boolean))].slice(0, maxProfileBatch);
  if (!normalized.length) return {};
  let serverProfiles = {};
  try {
    const url = new URL("/api/profiles", window.location.origin);
    url.searchParams.set("pubkey", normalized.join(","));
    const response = await fetchWithSession(url.pathname + url.search, {
      headers: { Accept: "application/json" },
    });
    if (response.ok) {
      const payload = await response.json();
      if (payload && typeof payload === "object") serverProfiles = payload;
    }
  } catch {
    serverProfiles = {};
  }
  const missing = normalized.filter((pubkey) => {
    const profile = serverProfiles?.[pubkey];
    return !profileLabel(profile) && !preferredAvatarURL(profile);
  });
  if (!missing.length) return rememberProfiles(serverProfiles);
  const relayProfiles = await fetchProfiles(missing);
  const merged = { ...serverProfiles };
  missing.forEach((pubkey) => {
    const relayProfile = relayProfiles?.[pubkey];
    if (profileHasRenderableFields(relayProfile)) {
      merged[pubkey] = { ...serverProfiles?.[pubkey], ...relayProfile };
    }
  });
  return rememberProfiles(merged);
}

export async function refreshVisibleNoteProfiles(root = document) {
  const pubkeys = collectMissingPubkeys(root);
  if (!pubkeys.length) return;
  const memoryProfiles = cachedProfiles(pubkeys);
  const memoryPubkeys = pubkeys.filter((pubkey) => profileHasRenderableFields(memoryProfiles?.[pubkey]));
  if (memoryPubkeys.length) {
    memoryPubkeys.forEach((pubkey) => emptyProfiles.delete(pubkey));
    applyProfiles(root, memoryProfiles);
  }
  const fetchPubkeys = pubkeys.filter((pubkey) => !profileHasRenderableFields(memoryProfiles?.[pubkey]));
  if (!fetchPubkeys.length) return;
  fetchPubkeys.forEach((pubkey) => inFlight.add(pubkey));
  try {
    const profiles = await fetchNoteProfiles(fetchPubkeys);
    fetchPubkeys.forEach((pubkey) => {
      const profile = profiles?.[pubkey];
      if (!profileLabel(profile) && !preferredAvatarURL(profile)) {
        emptyProfiles.add(pubkey);
      }
    });
    applyProfiles(root, profiles || {});
  } catch {
    // Keep server-rendered fallbacks; a later route refresh can try again.
  } finally {
    fetchPubkeys.forEach((pubkey) => inFlight.delete(pubkey));
  }
}
