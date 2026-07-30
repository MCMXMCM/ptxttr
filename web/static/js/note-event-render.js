import { nip19 } from "../lib/nostr-tools.js";
import { avatarRetryURL, displayName, parseProfile, preferredAvatarURL } from "./profile-parse.js";
import { normalizePubkey, profilePath } from "./relay-utils.js";
import { enrichNoteShell, imetaMediaItemsJSON, isQuotePost, isSimpleRepost, noteMainBodySourceText } from "./note-references.js";
import { applyAsciiMentionsToShell } from "./nip27.js";
import { appendFeedContextElements } from "./reply-feed-context.js";
import { replyLabelForCount } from "./reply-label.js";
import { rootIDForEvent } from "./thread-tags.js";
import { setAvatarImageSource } from "./avatar-cache.js";
import { parsePollEvent, hydrateVisiblePolls } from "./poll.js";

export { relativeAge } from "./relative-time.js";
import { relativeAge } from "./relative-time.js";

export function encodeNevent(eventID, pubkey) {
  try {
    return nip19.neventEncode({ id: eventID, author: pubkey });
  } catch {
    return eventID;
  }
}

export function encodeNpub(pubkey) {
  try {
    return nip19.npubEncode(pubkey);
  } catch {
    return pubkey;
  }
}

function selectHrefForThread(rootID, eventID) {
  const root = String(rootID || "").toLowerCase();
  const event = String(eventID || "").toLowerCase();
  if (!event) return "/thread/";
  if (!root || root === event) return `/thread/${event}`;
  return `/thread/${root}?selected=${event}#note-${event}`;
}

function applyPollDataset(node, event) {
  const poll = parsePollEvent(event);
  if (!poll) return;
  node.dataset.asciiPoll = JSON.stringify({
    id: poll.id,
    question: poll.question,
    options: poll.options,
    pollType: poll.pollType,
    endsAt: poll.endsAt,
    relays: poll.relays,
    event: {
      id: String(event?.id || "").toLowerCase(),
      pubkey: String(event?.pubkey || "").toLowerCase(),
    },
  });
}

/** Build a note shell compatible with ascii.js rendering. */
export function createNoteArticle(
  event,
  profileInput = null,
  {
    referencedByID = null,
    profilesByPubkey = null,
    replyCount = null,
    reactionTotal = null,
    reactionViewer = null,
    zapTotal = null,
  } = {},
) {
  const pk = normalizePubkey(event?.pubkey);
  const profile = profileInput || parseProfile(pk, null);
  const author = displayName(profile);
  const age = relativeAge(event?.created_at);
  const avatar = preferredAvatarURL(profile);
  const retryAvatar = avatarRetryURL(profile);
  const eventID = String(event?.id || "").toLowerCase();
  const threadRootID = rootIDForEvent(event);
  const bodySource = noteMainBodySourceText(event);
  const userHref = profilePath(pk, event?.relay_url ? [event.relay_url] : []);
  const replyCountValue = Number.parseInt(`${replyCount ?? 0}`, 10) || 0;
  const reactionTotalValue = Number.parseInt(`${reactionTotal ?? 0}`, 10) || 0;
  const profiles = profilesByPubkey || { [pk]: profile };

  const article = document.createElement("article");
  article.className = "note";
  article.id = `note-${eventID}`;
  article.dataset.createdAt = String(event?.created_at || "");
  article.dataset.asciiKind = "note";
  article.dataset.asciiAuthor = author;
  article.dataset.asciiAge = age;
  article.dataset.asciiAvatar = avatar;
  article.dataset.asciiReplyCount = String(replyCountValue);
  article.dataset.asciiReplyLabel = replyLabelForCount(replyCountValue);
  article.dataset.asciiReactionTotal = String(reactionTotalValue);
  article.dataset.asciiReactionViewer = reactionViewer != null ? String(reactionViewer) : "";
  article.dataset.asciiZapTotal = String(Number.parseInt(`${zapTotal ?? 0}`, 10) || 0);
  article.dataset.asciiEventKind = String(event?.kind || 1);
  article.dataset.asciiUserHref = userHref;
  article.dataset.asciiThreadHref = `/thread/${eventID}`;
  article.dataset.asciiThreadRootId = threadRootID;
  article.dataset.asciiSelectHref = selectHrefForThread(threadRootID, eventID);
  article.dataset.asciiNevent = encodeNevent(eventID, pk);
  article.dataset.asciiNpub = encodeNpub(pk);
  article.dataset.asciiRelay = String(event?.relay_url || "");
  article.dataset.asciiSig = String(event?.sig || "");
  article.dataset.asciiEvent = JSON.stringify(event || {});
  article.dataset.replyRootId = threadRootID;
  article.dataset.replyTargetId = eventID;
  article.dataset.replyPubkey = pk;
  const imetaMedia = imetaMediaItemsJSON(event?.tags || []);
  if (imetaMedia) article.dataset.asciiImetaMedia = imetaMedia;
  applyPollDataset(article, event);

  appendFeedContextElements(article, event, profiles);

  const pre = document.createElement("pre");
  pre.className = "ascii-card";
  const av = document.createElement("a");
  av.className = "note-feed-avatar";
  av.href = userHref;
  av.dataset.relayAware = "";
  av.setAttribute("aria-hidden", "true");
  av.tabIndex = -1;
  if (avatar) {
    const img = document.createElement("img");
    img.alt = "";
    img.loading = "lazy";
    img.decoding = "async";
    setAvatarImageSource(img, avatar, { retryURL: retryAvatar });
    av.append(img);
  }
  pre.append(av);
  article.append(pre);

  applyAsciiMentionsToShell(article, bodySource, profiles);

  const drawer = document.createElement("div");
  drawer.className = "note-media-drawer";
  drawer.dataset.noteImageMount = "";
  drawer.hidden = true;
  article.append(drawer);

  if (isSimpleRepost(event) || isQuotePost(event)) {
    enrichNoteShell(article, event, referencedByID || new Map(), profiles);
  }

  return article;
}

/** Selected-style thread OP shell (iOS `.selected` in linear thread view). */
export function createSelectedNoteArticle(
  event,
  profileInput = null,
  {
    referencedByID = null,
    profilesByPubkey = null,
    hasVisibleChildren = false,
    isFocused = false,
    extraClass = "",
    rootID = null,
    replyCount = 0,
    zapTotal = 0,
    userHref = "",
  } = {},
) {
  const pk = normalizePubkey(event?.pubkey);
  const profile = profileInput || parseProfile(pk, null);
  const author = displayName(profile);
  const age = relativeAge(event?.created_at);
  const avatar = preferredAvatarURL(profile);
  const retryAvatar = avatarRetryURL(profile);
  const eventID = String(event?.id || "").toLowerCase();
  const bodySource = noteMainBodySourceText(event);
  const resolvedUserHref = userHref || profilePath(pk, event?.relay_url ? [event.relay_url] : []);

  const article = document.createElement("article");
  article.className = `note${isFocused ? " is-focused" : ""}${extraClass ? ` ${extraClass}` : ""}`;
  article.id = `note-${eventID}`;
  article.dataset.createdAt = String(event?.created_at || "");
  article.dataset.asciiKind = "selected";
  article.dataset.asciiAuthor = author;
  article.dataset.asciiAge = age;
  article.dataset.asciiAvatar = avatar;
  const replyCountValue = Number.parseInt(`${replyCount ?? 0}`, 10) || 0;
  article.dataset.asciiReplyCount = String(replyCountValue);
  article.dataset.asciiReplyLabel = replyLabelForCount(replyCountValue);
  article.dataset.asciiReactionTotal = "0";
  article.dataset.asciiReactionViewer = "";
  article.dataset.asciiZapTotal = String(Number.parseInt(`${zapTotal ?? 0}`, 10) || 0);
  article.dataset.asciiEventKind = String(event?.kind || 1);
  article.dataset.asciiUserHref = resolvedUserHref;
  article.dataset.asciiThreadHref = `/thread/${eventID}`;
  article.dataset.asciiSelectHref = selectHrefForThread(rootID || eventID, eventID);
  article.dataset.asciiNevent = encodeNevent(eventID, pk);
  article.dataset.asciiNpub = encodeNpub(pk);
  article.dataset.asciiRelay = String(event?.relay_url || "");
  article.dataset.asciiSig = String(event?.sig || "");
  article.dataset.asciiEvent = JSON.stringify(event || {});
  article.dataset.replyRootId = String(rootID || eventID).toLowerCase();
  article.dataset.replyTargetId = eventID;
  article.dataset.replyPubkey = pk;
  const imetaMedia = imetaMediaItemsJSON(event?.tags || []);
  if (imetaMedia) article.dataset.asciiImetaMedia = imetaMedia;
  applyPollDataset(article, event);
  if (hasVisibleChildren) article.dataset.asciiHasVisibleChildren = "true";

  const avatarLink = document.createElement("a");
  avatarLink.className = "note-avatar";
  avatarLink.href = resolvedUserHref;
  avatarLink.dataset.relayAware = "";
  avatarLink.setAttribute("aria-hidden", "true");
  avatarLink.tabIndex = -1;
  if (avatar) {
    const img = document.createElement("img");
    img.alt = "";
    img.loading = "lazy";
    img.decoding = "async";
    setAvatarImageSource(img, avatar, { retryURL: retryAvatar });
    avatarLink.append(img);
  }
  article.append(avatarLink);

  const pre = document.createElement("pre");
  pre.className = "ascii-reply";
  article.append(pre);

  const profiles = profilesByPubkey || { [pk]: profile };
  applyAsciiMentionsToShell(article, bodySource, profiles);

  const drawer = document.createElement("div");
  drawer.className = "note-media-drawer";
  drawer.dataset.noteImageMount = "";
  drawer.hidden = true;
  article.append(drawer);

  if (isSimpleRepost(event) || isQuotePost(event)) {
    enrichNoteShell(article, event, referencedByID || new Map(), profiles);
  }

  return article;
}

function detachFirst(root, selectors) {
  for (const selector of selectors) {
    const node = root.querySelector(selector);
    if (node instanceof Element) {
      node.remove();
      return node;
    }
  }
  return null;
}

/**
 * Rebuild selected-thread chrome around an existing note/comment shell while
 * preserving live avatar/media/content subnodes to avoid reload flashes.
 */
export function createSelectedNoteArticleFromShell(
  shell,
  event,
  profileInput = null,
  options = {},
) {
  if (!(shell instanceof Element)) {
    return createSelectedNoteArticle(event, profileInput, options);
  }
  const article = createSelectedNoteArticle(event, profileInput, options);
  const avatar = detachFirst(shell, [
    ":scope > .note-avatar",
    ":scope > .comment-avatar",
    ":scope > pre .note-feed-avatar",
    ".note-feed-avatar",
  ]);
  const pre = detachFirst(shell, [
    ":scope > pre.ascii-card",
    ":scope > pre.ascii-reply",
  ]);
  const drawer = detachFirst(shell, [
    ":scope > .note-media-drawer",
  ]);

  article.querySelector(":scope > .note-avatar")?.remove();
  article.querySelector(":scope > pre.ascii-reply")?.remove();
  article.querySelector(":scope > .note-media-drawer")?.remove();

  if (avatar instanceof HTMLAnchorElement) {
    avatar.className = "note-avatar";
    if (options.userHref) avatar.href = options.userHref;
    avatar.setAttribute("aria-hidden", "true");
    avatar.tabIndex = -1;
    article.append(avatar);
  }
  if (pre instanceof HTMLElement) {
    pre.className = "ascii-reply";
    article.append(pre);
  }
  if (drawer instanceof HTMLElement) {
    article.append(drawer);
  }

  return article;
}

/**
 * Rebuild feed-list chrome from a thread-focus shell while preserving live
 * avatar/media/content subnodes to avoid reload flashes.
 */
export function createNoteArticleFromThreadShell(
  shell,
  event,
  profileInput = null,
  options = {},
) {
  if (!(shell instanceof Element)) {
    return createNoteArticle(event, profileInput, options);
  }
  const article = createNoteArticle(event, profileInput, options);
  const avatar = detachFirst(shell, [
    ":scope > .note-avatar",
    ":scope > .comment-avatar",
    ":scope > pre .note-feed-avatar",
    ".note-feed-avatar",
  ]);
  const pre = detachFirst(shell, [
    ":scope > pre.ascii-card",
    ":scope > pre.ascii-reply",
  ]);
  const drawer = detachFirst(shell, [
    ":scope > .note-media-drawer",
  ]);

  article.querySelector(":scope > pre.ascii-card")?.remove();
  article.querySelector(":scope > .note-media-drawer")?.remove();

  if (pre instanceof HTMLElement) {
    pre.className = "ascii-card";
    if (avatar instanceof HTMLAnchorElement) {
      avatar.className = "note-feed-avatar";
      avatar.setAttribute("aria-hidden", "true");
      avatar.tabIndex = -1;
      if (!pre.querySelector(".note-feed-avatar")) {
        pre.prepend(avatar);
      }
    }
    article.append(pre);
  }
  if (drawer instanceof HTMLElement) {
    article.append(drawer);
  }

  article.classList.remove("is-focused", "thread-focus-selected", "ptxt-carried-profile-note", "ptxt-carried-thread-note");
  article.dataset.asciiKind = "note";
  delete article.dataset.asciiSelected;

  return article;
}

function existingNoteIDs(container) {
  const seen = new Set();
  container.querySelectorAll(".note[id^='note-']").forEach((node) => {
    const id = node.id.replace(/^note-/, "").toLowerCase();
    if (id) seen.add(id);
  });
  return seen;
}

export function renderNoteFeed(container, events, profilesByPubkey = {}, options = {}) {
  const {
    emptyText = "No notes yet.",
    referencedByID = null,
    replyCounts = null,
    reactionStats = null,
    zapTotals = null,
  } = options;
  if (!container) return;
  container.classList.remove("profile-feed-skeleton");
  container.replaceChildren();
  if (!events?.length) {
    const empty = document.createElement("p");
    empty.className = "muted";
    empty.textContent = emptyText;
    container.append(empty);
    return;
  }
  appendNoteFeed(container, events, profilesByPubkey, { referencedByID, replyCounts, reactionStats, zapTotals });
  void hydrateVisiblePolls(container).catch(() => {});
}

export function appendNoteFeed(
  container,
  events,
  profilesByPubkey = {},
  { referencedByID = null, replyCounts = null, reactionStats = null, zapTotals = null } = {},
) {
  if (!container || !events?.length) return 0;
  const seen = existingNoteIDs(container);
  let appended = 0;
  const fragment = document.createDocumentFragment();
  for (const event of events) {
    const eventID = String(event?.id || "").toLowerCase();
    if (!eventID || seen.has(eventID)) continue;
    seen.add(eventID);
    const pk = normalizePubkey(event?.pubkey);
    const profile = profilesByPubkey[pk] || parseProfile(pk, null);
    const reactionRow = reactionStats?.[eventID];
    fragment.append(
      createNoteArticle(event, profile, {
        referencedByID,
        profilesByPubkey,
        replyCount: replyCounts?.[eventID],
        reactionTotal: reactionRow?.total,
        reactionViewer: reactionRow?.viewer,
        zapTotal: zapTotals?.[eventID],
      }),
    );
    appended += 1;
  }
  if (appended > 0) container.append(fragment);
  container.querySelector(".muted")?.remove();
  if (appended > 0) void hydrateVisiblePolls(container).catch(() => {});
  return appended;
}

export function prependNoteFeed(
  container,
  events,
  profilesByPubkey = {},
  { referencedByID = null, replyCounts = null, reactionStats = null, zapTotals = null } = {},
) {
  if (!container || !events?.length) return 0;
  const seen = existingNoteIDs(container);
  const sorted = [...events].sort((a, b) => {
    const delta = Number(b?.created_at || 0) - Number(a?.created_at || 0);
    if (delta !== 0) return delta;
    return String(b?.id || "").localeCompare(String(a?.id || ""));
  });
  let prepended = 0;
  const anchor = container.querySelector(".note[id^='note-']");
  const fragment = document.createDocumentFragment();
  for (let index = sorted.length - 1; index >= 0; index -= 1) {
    const event = sorted[index];
    const eventID = String(event?.id || "").toLowerCase();
    if (!eventID || seen.has(eventID)) continue;
    seen.add(eventID);
    const pk = normalizePubkey(event?.pubkey);
    const profile = profilesByPubkey[pk] || parseProfile(pk, null);
    const reactionRow = reactionStats?.[eventID];
    const article = createNoteArticle(event, profile, {
      referencedByID,
      profilesByPubkey,
      replyCount: replyCounts?.[eventID],
      reactionTotal: reactionRow?.total,
      reactionViewer: reactionRow?.viewer,
      zapTotal: zapTotals?.[eventID],
    });
    fragment.prepend(article);
    prepended += 1;
  }
  if (prepended > 0) {
    if (anchor) container.insertBefore(fragment, anchor);
    else container.prepend(fragment);
  }
  container.querySelector(".muted")?.remove();
  if (prepended > 0) void hydrateVisiblePolls(container).catch(() => {});
  return prepended;
}
