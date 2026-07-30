import {
  relativeAge,
  createSelectedNoteArticle,
  createSelectedNoteArticleFromShell,
  encodeNevent,
  encodeNpub,
} from "./note-event-render.js";
import { replyLabelForCount } from "./reply-label.js";
import { enrichNoteShell, imetaMediaItemsJSON, isQuotePost, isSimpleRepost, noteMainBodySourceText } from "./note-references.js";
import { applyAsciiMentionsToShell, rewriteASCIIMentions } from "./nip27.js";
import { avatarRetryURL, displayName, preferredAvatarURL } from "./profile-parse.js";
import {
  buildThreadChildren,
  threadParticipantPubkeys,
} from "./thread-graph.js";
import { effectiveThreadParentID } from "./thread-tags.js";
import { threadParentSkeletonMarkup, threadViewToggleDesktopMarkup } from "./shell.js";
import { trustedHTMLFragment } from "./render-utils.js";
import { linearThreadReplyEvents, resolveThreadView } from "./thread-view.js";
import { applyThreadViewVisibilityFromPreference } from "./thread.js";
import { canonicalHex64, normalizePubkey } from "./relay-utils.js";
import { setAvatarImageSource } from "./avatar-cache.js";
import { parsePollEvent } from "./poll.js";
import { routeKind } from "./nav-routing.js";
import { applyDestinationThreadTransition } from "./note-transition.js";
import { createElement, createLink } from "./render-utils.js";
import { createThreadComhead, createThreadParticipantMeta, createThreadReplyLink } from "./thread-render-helpers.js";
import { ensureThreadRepliesHost } from "./thread-replies-host.js";

const TREE_MEDIA_URL_PATTERN = /https?:\/\/[^\s<>"'`]+/gi;
const TREE_IMAGE_EXT_PATTERN = /\.(?:png|jpe?g|gif|webp|avif|svg)(?:[?#][^\s<>"']*)?$/i;
const TREE_VIDEO_EXT_PATTERN = /\.(?:mp4|webm|m4v|mov|ogv|ogg)(?:[?#][^\s<>"']*)?$/i;
const TREE_TRAILING_URL_PUNCTUATION = /[),.!?;:]+$/;

function activeThreadColumn(root = document) {
  if (!root?.querySelector) return null;
  const threadFragment = root.querySelector("#thread-summary, #thread-focus, #thread-tree-view, #thread-ancestors");
  if (threadFragment?.closest) {
    const column = threadFragment.closest(".feed-column");
    if (column instanceof HTMLElement) return column;
  }
  const taggedColumn = root.querySelector(".feed-column[data-thread-selected-id], .feed-column[data-thread-root-id]");
  if (taggedColumn instanceof HTMLElement) return taggedColumn;
  const fallback = root.querySelector(".feed-column");
  return fallback instanceof HTMLElement ? fallback : null;
}

function profileFor(profilesByPubkey, pubkey) {
  const pk = normalizePubkey(pubkey);
  return profilesByPubkey?.[pk] || { pubkey: pk };
}

function appendAsciiSource(container, content, profilesByPubkey = {}) {
  const { text } = rewriteASCIIMentions(String(content || ""), profilesByPubkey);
  const source = document.createElement("template");
  source.className = "ascii-source";
  source.content.append(document.createTextNode(text));
  container.append(source);
  return text;
}

function threadTreeMainBodyText(event, profilesByPubkey = {}) {
  if (isSimpleRepost(event)) return "";
  return rewriteASCIIMentions(noteMainBodySourceText(event), profilesByPubkey).text;
}

function threadTreeMediaType(url) {
  const lower = String(url || "").toLowerCase().split(/[?#]/, 1)[0];
  if (TREE_IMAGE_EXT_PATTERN.test(lower)) return "image";
  if (TREE_VIDEO_EXT_PATTERN.test(lower)) return "video";
  return "";
}

function threadTreeExtractMediaItems(content) {
  const matches = String(content || "").match(TREE_MEDIA_URL_PATTERN) || [];
  const seen = new Set();
  const items = [];
  const mediaURLs = new Set();
  matches.forEach((raw) => {
    const url = raw.replace(TREE_TRAILING_URL_PUNCTUATION, "");
    if (!url || seen.has(url)) return;
    const type = threadTreeMediaType(url);
    if (!type) return;
    seen.add(url);
    mediaURLs.add(url);
    items.push({ url, type });
  });
  return { items, mediaURLs };
}

function threadTreeParseImetaItems(tags) {
  const raw = imetaMediaItemsJSON(tags || []);
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter((item) => item?.url && item?.type) : [];
  } catch {
    return [];
  }
}

function threadTreeBlossomPathKey(url) {
  if (!url) return "";
  const cleaned = String(url).trim().replace(TREE_TRAILING_URL_PUNCTUATION, "");
  const match = /^https?:\/\/[^/]*\.blossom\.band\/([^\s<>"'`?#]+)/i.exec(cleaned);
  return match ? match[1].toLowerCase() : "";
}

function threadTreeMediaDedupKey(item) {
  const blossomKey = threadTreeBlossomPathKey(item?.url || "");
  return blossomKey ? `blossom:${blossomKey}` : String(item?.url || "");
}

function threadTreeMergeMediaItems(a, b) {
  const seen = new Map();
  const out = [];
  for (const list of [a, b]) {
    for (const item of list) {
      if (!item?.url) continue;
      const key = threadTreeMediaDedupKey(item);
      if (seen.has(key)) {
        out[seen.get(key)] = item;
        continue;
      }
      seen.set(key, out.length);
      out.push(item);
    }
  }
  return out;
}

function threadTreeStripMediaURLs(content, mediaURLs) {
  if (!content) return "";
  const stripped = String(content).replace(TREE_MEDIA_URL_PATTERN, (raw) => {
    const url = raw.replace(TREE_TRAILING_URL_PUNCTUATION, "");
    return mediaURLs.has(url) ? "" : raw;
  });
  const lines = stripped.split("\n");
  const out = [];
  lines.forEach((line) => {
    const compact = line.trim().split(/\s+/).filter(Boolean).join(" ");
    if (!compact && out[out.length - 1] === "") return;
    out.push(compact);
  });
  return out.join("\n").trim();
}

function threadTreeMediaLabel(items) {
  if (!items.length) return "";
  let images = 0;
  let videos = 0;
  items.forEach((item) => {
    if (item.type === "image") images += 1;
    else if (item.type === "video") videos += 1;
  });
  if (images > 0 && videos === 0) return images === 1 ? " 1 image " : `${String(images).padStart(2, " ")} images`;
  if (videos > 0 && images === 0) return videos === 1 ? " 1 video " : `${String(videos).padStart(2, " ")} videos`;
  return items.length === 1 ? " 1 media " : `${String(items.length).padStart(2, " ")} media `;
}

function threadTreeMediaFields(content, tags) {
  const { items: contentItems, mediaURLs } = threadTreeExtractMediaItems(content);
  const imetaItems = threadTreeParseImetaItems(tags);
  const merged = threadTreeMergeMediaItems(contentItems, imetaItems);
  if (!merged.length) return { itemsJSON: "", label: "", displaySource: "" };
  const stripped = threadTreeStripMediaURLs(content, mediaURLs);
  return {
    itemsJSON: JSON.stringify(merged),
    label: threadTreeMediaLabel(merged),
    displaySource: stripped.trim() ? stripped : "",
  };
}

function appendThreadTreeMediaControls(container, label) {
  if (!label) return;
  const wrap = document.createElement("p");
  wrap.className = "thread-tree-media hn-media";
  wrap.dataset.threadTreeMediaWrap = "";
  wrap.hidden = true;
  const button = document.createElement("button");
  button.className = "link-button thread-tree-media-toggle";
  button.type = "button";
  button.dataset.threadTreeMediaToggle = "";
  button.textContent = label;
  wrap.append(button);
  const preview = document.createElement("div");
  preview.className = "thread-tree-media-preview";
  preview.dataset.threadTreeMediaMount = "";
  preview.hidden = true;
  container.append(wrap, preview);
}

function appendMediaDrawer(container) {
  const drawer = document.createElement("div");
  drawer.className = "note-media-drawer";
  drawer.dataset.noteImageMount = "";
  drawer.hidden = true;
  container.append(drawer);
}

function applyThreadReplyCount(node, count) {
  const next = Number.parseInt(`${count ?? 0}`, 10) || 0;
  node.dataset.asciiReplyCount = String(next);
  node.dataset.asciiReplyLabel = replyLabelForCount(next);
}

function threadProfilePath(pubkey) {
  const pk = normalizePubkey(pubkey);
  return pk ? `/u/${pk}` : "/u/";
}

function applyReplyAsciiDatasets(node, event, profile, { rootID, selectedID, depth, isLast, hasChildren, isFocused, replyCount = 0 }) {
  const pk = normalizePubkey(event.pubkey);
  const author = displayName(profile);
  const eventID = canonicalHex64(event.id);
  const userHref = threadProfilePath(pk);
  node.dataset.asciiKind = "reply";
  node.dataset.asciiAuthor = author;
  node.dataset.asciiAge = relativeAge(event.created_at);
  node.dataset.asciiIsLast = isLast ? "true" : "false";
  node.dataset.asciiHasChildren = hasChildren ? "true" : "false";
  node.dataset.asciiSelected = isFocused || eventID === selectedID ? "true" : "false";
  node.dataset.asciiAvatar = preferredAvatarURL(profile);
  applyThreadReplyCount(node, replyCount);
  node.dataset.asciiReactionTotal = "0";
  node.dataset.asciiReactionViewer = "";
  node.dataset.asciiZapTotal = "0";
  node.dataset.asciiEventKind = String(event?.kind || 1);
  node.dataset.asciiUserHref = userHref;
  node.dataset.asciiSelectHref =
    rootID && rootID !== eventID ? `/thread/${rootID}?selected=${eventID}#note-${eventID}` : `/thread/${eventID}`;
  node.dataset.asciiNevent = encodeNevent(eventID, pk);
  node.dataset.asciiNpub = encodeNpub(pk);
  node.dataset.asciiRelay = String(event?.relay_url || "");
  node.dataset.asciiSig = String(event?.sig || "");
  node.dataset.asciiEvent = JSON.stringify(event || {});
  node.dataset.replyRootId = rootID;
  node.dataset.replyTargetId = eventID;
  node.dataset.replyPubkey = pk;
  const imetaMedia = imetaMediaItemsJSON(event?.tags || []);
  if (imetaMedia) node.dataset.asciiImetaMedia = imetaMedia;
  const poll = parsePollEvent(event);
  if (poll) {
    node.dataset.asciiPoll = JSON.stringify({
      id: poll.id,
      question: poll.question,
      options: poll.options,
      pollType: poll.pollType,
      endsAt: poll.endsAt,
      relays: poll.relays,
      event: {
        id: eventID,
        pubkey: pk,
      },
    });
  }
  node.dataset.depth = String(depth);
  node.style.setProperty("--depth", String(depth));
}

export function createReplyShell(event, profile, options) {
  const {
    rootID,
    selectedID,
    depth = 1,
    isLast = false,
    hasChildren = false,
    isFocused = false,
    extraClass = "",
    referencedByID = null,
    replyCount = 0,
  } = options;
  const pk = normalizePubkey(event.pubkey);
  const eventID = canonicalHex64(event.id);
  const userHref = threadProfilePath(pk);
  const div = document.createElement("div");
  div.id = `note-${eventID}`;
  div.className = `comment${extraClass ? ` ${extraClass}` : ""}${isFocused ? " is-focused" : ""}`;
  applyReplyAsciiDatasets(div, event, profile, {
    rootID,
    selectedID,
    depth,
    isLast,
    hasChildren,
    isFocused,
    replyCount,
  });

  const avatarLink = document.createElement("a");
  avatarLink.className = "comment-avatar";
  avatarLink.href = userHref;
  avatarLink.dataset.relayAware = "";
  avatarLink.setAttribute("aria-hidden", "true");
  avatarLink.tabIndex = -1;
  const avatarURL = preferredAvatarURL(profile);
  const retryAvatar = avatarRetryURL(profile);
  if (avatarURL) {
    const img = document.createElement("img");
    img.alt = "";
    img.loading = "lazy";
    img.decoding = "async";
    setAvatarImageSource(img, avatarURL, { retryURL: retryAvatar });
    avatarLink.append(img);
  }

  const pre = document.createElement("pre");
  pre.className = "ascii-reply";
  div.append(avatarLink, pre);
  const bodySource = noteMainBodySourceText(event);
  const profiles = { [pk]: profile };
  applyAsciiMentionsToShell(div, bodySource, profiles);
  appendMediaDrawer(div);
  if (isSimpleRepost(event) || isQuotePost(event)) {
    enrichNoteShell(div, event, referencedByID || new Map(), profiles);
  }
  return div;
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

function createReplyShellFromShell(shell, event, profile, options) {
  if (!(shell instanceof Element)) {
    return createReplyShell(event, profile, options);
  }
  const reply = createReplyShell(event, profile, options);
  const avatar = detachFirst(shell, [
    ":scope > .comment-avatar",
    ":scope > .note-avatar",
    ":scope > pre .note-feed-avatar",
    ".note-feed-avatar",
  ]);
  const pre = detachFirst(shell, [
    ":scope > pre.ascii-reply",
    ":scope > pre.ascii-card",
  ]);
  const drawer = detachFirst(shell, [
    ":scope > .note-media-drawer",
  ]);

  reply.querySelector(":scope > .comment-avatar")?.remove();
  reply.querySelector(":scope > pre.ascii-reply")?.remove();
  reply.querySelector(":scope > .note-media-drawer")?.remove();

  if (avatar instanceof HTMLAnchorElement) {
    avatar.className = "comment-avatar";
    avatar.setAttribute("aria-hidden", "true");
    avatar.tabIndex = -1;
    reply.append(avatar);
  }
  if (pre instanceof HTMLElement) {
    pre.className = "ascii-reply";
    reply.append(pre);
  }
  if (drawer instanceof HTMLElement) {
    reply.append(drawer);
  }
  return reply;
}

function collectReusableThreadShells(root) {
  const shells = new Map();
  root.querySelectorAll("#thread-focus > [id^='note-'], #thread-replies > [id^='note-'], [data-thread-filtered-replies] > [id^='note-']").forEach((node) => {
    if (!(node instanceof HTMLElement) || !node.id) return;
    shells.set(node.id, node);
  });
  return shells;
}

function configureThreadLoadMore(root, options) {
  const loadMore = root.querySelector("[data-thread-load-more]");
  if (!loadMore) return;
  const { rootID, parentID, selectedID, hasMore, cursor, cursorId } = options;
  loadMore.dataset.rootId = rootID;
  loadMore.dataset.parentId = parentID;
  loadMore.dataset.selectedId = selectedID;
  loadMore.dataset.cursor = cursor || "";
  loadMore.dataset.cursorId = cursorId || "";
  loadMore.dataset.hasMore = hasMore ? "1" : "0";
  loadMore.hidden = !hasMore;
  loadMore.disabled = false;
  if (loadMore.dataset.loading !== "1") {
    loadMore.textContent = loadMore.dataset.loadLabel || "Load more thread replies";
  }
}

export function appendDirectReplyShells(root, replyEvents, bundle, profilesByPubkey, { hasMore = false, referencedByID = null } = {}) {
  const repliesHost = root.querySelector("#thread-replies");
  if (!repliesHost || !replyEvents?.length) return 0;
  const { root: rootEvent, selected, events, parentByID } = bundle;
  const rootID = canonicalHex64(rootEvent.id);
  const selectedID = canonicalHex64((selected || rootEvent).id);
  const { replyCounts } = bundle.replyCounts
    ? { replyCounts: bundle.replyCounts }
    : resolveThreadView(rootEvent, selected || rootEvent, events, parentByID);
  const depth = 1;
  const childrenByParent = buildThreadChildren(events, parentByID, rootID);
  let appended = 0;
  replyEvents.forEach((event, index) => {
    const eventID = canonicalHex64(event.id);
    if (eventID && document.getElementById(`note-${eventID}`)) return;
    const childList = childrenByParent.get(eventID) || [];
    const directCount = replyCounts[eventID] ?? childList.length;
    repliesHost.append(
      createReplyShell(event, profileFor(profilesByPubkey, event.pubkey), {
        rootID,
        selectedID,
        depth,
        isLast: index === replyEvents.length - 1 && !hasMore,
        hasChildren: false,
        isFocused: eventID === selectedID,
        referencedByID,
        replyCount: directCount,
      }),
    );
    appended += 1;
  });
  return appended;
}

function createThreadViewToggle({ showTree = false } = {}) {
  const wrapper = document.createElement("div");
  wrapper.innerHTML = threadViewToggleDesktopMarkup({ showTree }).trim();
  return wrapper.firstElementChild;
}

function filteredRepliesToggleLabel(count) {
  return count === 1 ? "show 1 more" : `show ${count} more`;
}

function renderSummaryHeader() {
  const fragment = document.createDocumentFragment();
  fragment.append(createThreadViewToggle());
  return fragment;
}

function renderTreeList(nodes, profilesByPubkey, { rootID, selectedID, childrenByParent, referencedByID = null }) {
  const ul = document.createElement("ul");
  ul.className = "hn-tree-ul";
  nodes.forEach((event, index) => {
    const eventID = canonicalHex64(event.id);
    const profile = profileFor(profilesByPubkey, event.pubkey);
    const childList = childrenByParent.get(eventID) || [];
    const content = threadTreeMainBodyText(event, profilesByPubkey);
    const media = threadTreeMediaFields(content, event.tags || []);
    const li = document.createElement("li");
    li.className = `hn-comtr thread-tree-item${eventID === selectedID ? " is-selected" : ""}`;
    li.id = `note-${eventID}`;
    li.dataset.threadTreeNote = `note-${eventID}`;
    li.dataset.threadFocusId = eventID;
    li.dataset.replyRootId = rootID;
    li.dataset.replyTargetId = eventID;
    li.dataset.replyPubkey = normalizePubkey(event.pubkey);
    li.dataset.threadTreeSource = content;
    li.dataset.threadTreeDisplaySource = media.displaySource;
    li.dataset.asciiRelay = String(event?.relay_url || "");
    if (media.itemsJSON) li.dataset.threadTreeMedia = media.itemsJSON;

    const body = document.createElement("div");
    body.className = "hn-li-body";
    const avatar = document.createElement("a");
    avatar.className = "hn-tree-avatar";
    avatar.href = threadProfilePath(event.pubkey);
    avatar.dataset.relayAware = "";
    const avatarURL = preferredAvatarURL(profile);
    const retryAvatar = avatarRetryURL(profile);
    if (avatarURL) {
      const img = document.createElement("img");
      img.className = "thread-tree-avatar";
      img.alt = "";
      img.loading = "lazy";
      img.decoding = "async";
      setAvatarImageSource(img, avatarURL, { retryURL: retryAvatar });
      avatar.append(img);
    } else {
      const fallback = document.createElement("span");
      fallback.className = "thread-tree-avatar thread-tree-avatar-fallback";
      fallback.setAttribute("aria-hidden", "true");
      fallback.textContent = "@";
      avatar.append(fallback);
    }

    const stack = document.createElement("div");
    stack.className = "hn-default";
    const head = createThreadComhead(
      profile,
      event.pubkey,
      `/thread/${rootID}#note-${eventID}`,
      relativeAge(event.created_at),
    );

    const collapsible = document.createElement("div");
    collapsible.className = "hn-tree-collapsible";
    collapsible.dataset.threadTreeCollapsible = eventID;
    const text = document.createElement("div");
    text.className = "hn-commtext thread-tree-text";
    collapsible.append(text);
    appendThreadTreeMediaControls(collapsible, media.label);
    appendAsciiSource(collapsible, content, profilesByPubkey);

    stack.append(head, collapsible);
    body.append(avatar, stack);
    li.append(body);
    if (isSimpleRepost(event) || isQuotePost(event)) {
      enrichNoteShell(li, event, referencedByID || new Map(), profilesByPubkey);
    }

    if (childList.length) {
      li.append(renderTreeList(childList, profilesByPubkey, { rootID, selectedID, childrenByParent, referencedByID }));
    }
    ul.append(li);
  });
  return ul;
}

function renderTreeSection(rootEvent, events, parentByID, profilesByPubkey, selectedID, wot = {}, referencedByID = null) {
  const rootID = canonicalHex64(rootEvent.id);
  const childrenByParent = buildThreadChildren(events, parentByID, rootID);
  const section = document.createElement("section");
  section.className = "thread-tree-mode hn-thread-tree-mode";
  section.id = "thread-tree-view";
  section.dataset.threadFragment = "tree";
  section.dataset.threadTreeView = "";
  section.dataset.threadTreeRootId = rootID;
  section.dataset.threadSelectedId = selectedID;
  section.hidden = true;
  if (wot.enabled && wot.filteredCount > 0) {
    section.dataset.threadWotActive = "1";
    section.dataset.threadWotFiltered = String(wot.filteredCount);
  }

  const rootProfile = profileFor(profilesByPubkey, rootEvent.pubkey);
  const rootContent = threadTreeMainBodyText(rootEvent, profilesByPubkey);
  const rootMedia = threadTreeMediaFields(rootContent, rootEvent.tags || []);
  const rootWrap = document.createElement("div");
  rootWrap.className = `hn-story thread-tree-root-note${rootID === selectedID ? " is-selected" : ""}`;
  rootWrap.id = `note-${rootID}`;
  rootWrap.dataset.threadTreeNote = `note-${rootID}`;
  rootWrap.dataset.threadFocusId = rootID;
  rootWrap.dataset.threadTreeSource = rootContent;
  rootWrap.dataset.threadTreeDisplaySource = rootMedia.displaySource;
  rootWrap.dataset.replyRootId = rootID;
  rootWrap.dataset.replyTargetId = rootID;
  rootWrap.dataset.replyPubkey = normalizePubkey(rootEvent.pubkey);
  rootWrap.dataset.asciiAuthor = displayName(rootProfile);
  rootWrap.dataset.asciiAge = relativeAge(rootEvent.created_at);
  rootWrap.dataset.asciiRelay = String(rootEvent?.relay_url || "");
  if (rootMedia.itemsJSON) rootWrap.dataset.threadTreeMedia = rootMedia.itemsJSON;

  const rootAvatar = document.createElement("a");
  rootAvatar.className = "hn-tree-avatar";
  rootAvatar.href = threadProfilePath(rootEvent.pubkey);
  rootAvatar.dataset.relayAware = "";
  rootAvatar.setAttribute("aria-hidden", "true");
  rootAvatar.tabIndex = -1;
  const rootAvatarURL = preferredAvatarURL(rootProfile);
  const rootRetryAvatar = avatarRetryURL(rootProfile);
  if (rootAvatarURL) {
    const img = document.createElement("img");
    img.className = "thread-tree-avatar";
    img.alt = "";
    img.loading = "lazy";
    img.decoding = "async";
    setAvatarImageSource(img, rootAvatarURL, { retryURL: rootRetryAvatar });
    rootAvatar.append(img);
  } else {
    const fallback = document.createElement("span");
    fallback.className = "thread-tree-avatar thread-tree-avatar-fallback";
    fallback.setAttribute("aria-hidden", "true");
    fallback.textContent = "@";
    rootAvatar.append(fallback);
  }

  const rootStack = document.createElement("div");
  rootStack.className = "hn-root-stack";
  const rootHead = createThreadComhead(
    rootProfile,
    rootEvent.pubkey,
    `/thread/${rootID}#note-${rootID}`,
    relativeAge(rootEvent.created_at),
    {
      className: "hn-comhead hn-story-comhead",
      collapseId: rootID,
    },
  );
  const rootCollapse = document.createElement("div");
  rootCollapse.className = "hn-tree-collapsible";
  rootCollapse.dataset.threadTreeCollapsible = rootID;
  const rootText = document.createElement("div");
  rootText.className = "hn-commtext thread-tree-text";
  rootCollapse.append(rootText);
  appendThreadTreeMediaControls(rootCollapse, rootMedia.label);
  const reply = createThreadReplyLink(rootID);
  rootCollapse.append(reply);
  appendAsciiSource(rootCollapse, rootContent, profilesByPubkey);
  rootStack.append(rootHead, rootCollapse);
  rootWrap.append(rootAvatar, rootStack);
  if (isSimpleRepost(rootEvent) || isQuotePost(rootEvent)) {
    enrichNoteShell(rootWrap, rootEvent, referencedByID || new Map(), profilesByPubkey);
  }
  section.append(rootWrap);

  const topLevel = childrenByParent.get(rootID) || [];
  if (topLevel.length) {
    const tree = document.createElement("div");
    tree.className = "hn-comment-tree thread-tree";
    tree.append(renderTreeList(topLevel, profilesByPubkey, { rootID, selectedID, childrenByParent, referencedByID }));
    section.append(tree);
  }
  if (wot.enabled && wot.filteredReplyNodes?.length) {
    const filteredEvents = wot.filteredReplyNodes
      .map((node) => node?.event)
      .filter((event) => event?.id);
    if (filteredEvents.length) {
      const filteredTree = document.createElement("div");
      filteredTree.className = "hn-comment-tree thread-tree thread-tree-filtered-replies";
      filteredTree.dataset.threadTreeFilteredReplies = "";
      filteredTree.hidden = true;
      filteredTree.append(renderTreeList(filteredEvents, profilesByPubkey, {
        rootID,
        selectedID,
        childrenByParent: new Map(),
        referencedByID,
      }));
      section.append(filteredTree);

      const toggleWrap = document.createElement("p");
      toggleWrap.className = "thread-filtered-replies-toggle thread-tree-filtered-replies-toggle";
      const toggle = document.createElement("button");
      toggle.className = "link-button";
      toggle.type = "button";
      toggle.dataset.threadTreeFilteredRepliesToggle = "";
      toggle.dataset.collapsedLabel = filteredRepliesToggleLabel(filteredEvents.length);
      toggle.dataset.expandedLabel = "hide";
      toggle.setAttribute("aria-expanded", "false");
      toggle.textContent = filteredRepliesToggleLabel(filteredEvents.length);
      toggleWrap.append(toggle);
      section.append(toggleWrap);
    }
  }
  return section;
}

function createThreadRailGap(depth = 1) {
  const gap = document.createElement("div");
  gap.className = "thread-rail-gap";
  gap.dataset.depth = String(depth);
  gap.style.setProperty("--depth", String(depth));
  gap.setAttribute("aria-hidden", "true");
  return gap;
}

function resolveDirectParentEvent(rootEvent, selectedEvent, events, parentByID) {
  const rootID = canonicalHex64(rootEvent.id);
  const directParentID = effectiveThreadParentID(rootID, selectedEvent, parentByID);
  if (!directParentID) return null;
  const fromEvents = (events || []).find((event) => canonicalHex64(event.id) === directParentID);
  if (fromEvents?.id) return fromEvents;
  if (canonicalHex64(rootEvent.id) === directParentID) return rootEvent;
  return null;
}

function appendThreadParentSkeleton(section) {
  section.append(trustedHTMLFragment(threadParentSkeletonMarkup()));
}

function renderFocusSection(
  rootEvent,
  selectedEvent,
  events,
  parentByID,
  profilesByPubkey,
  referencedByID = null,
  {
    threadView,
    replyCounts = {},
    hasThreadReplies = false,
    reusableSelectedShell = null,
    reusableReplyShells = null,
    parentUnavailable = false,
  } = {},
) {
  const rootID = canonicalHex64(rootEvent.id);
  const selectedID = canonicalHex64(selectedEvent.id);
  const focused = Boolean(threadView?.focusMode);
  const section = document.createElement("section");
  section.className = "thread-focus";
  section.id = "thread-focus";
  section.dataset.threadFragment = "focus";

  if (focused) {
    const directParent = resolveDirectParentEvent(rootEvent, selectedEvent, events, parentByID);
    if (directParent?.id && canonicalHex64(directParent.id) !== selectedID) {
      const parentIDValue = canonicalHex64(directParent.id);
      section.append(
        createReplyShellFromShell(
          reusableReplyShells?.get(`note-${parentIDValue}`) || null,
          directParent,
          profileFor(profilesByPubkey, directParent.pubkey),
          {
            rootID,
            selectedID,
            depth: 1,
            isLast: false,
            hasChildren: (replyCounts[parentIDValue] || 0) > 0,
            extraClass: "thread-focus-parent",
            referencedByID,
            replyCount: replyCounts[parentIDValue] || 0,
          },
        ),
      );
    } else if (selectedID !== rootID) {
      appendThreadParentSkeleton(section);
    }
    section.append(
      reusableSelectedShell
        ? createSelectedNoteArticleFromShell(
          reusableSelectedShell,
          selectedEvent,
          profileFor(profilesByPubkey, selectedEvent.pubkey),
          {
            rootID,
            referencedByID,
            isFocused: true,
            extraClass: "thread-focus-selected",
            replyCount: replyCounts[selectedID] || 0,
            userHref: threadProfilePath(selectedEvent.pubkey),
          },
        )
        : createSelectedNoteArticle(
          selectedEvent,
          profileFor(profilesByPubkey, selectedEvent.pubkey),
          {
            rootID,
            referencedByID,
            isFocused: true,
            extraClass: "thread-focus-selected",
            replyCount: replyCounts[selectedID] || 0,
            userHref: threadProfilePath(selectedEvent.pubkey),
          },
        ),
    );
    return section;
  }

  if (selectedID !== rootID) {
    section.append(
      reusableSelectedShell
        ? createSelectedNoteArticleFromShell(
          reusableSelectedShell,
          selectedEvent,
          profileFor(profilesByPubkey, selectedEvent.pubkey),
          {
            rootID,
            referencedByID,
            isFocused: true,
            replyCount: replyCounts[selectedID] || 0,
            userHref: threadProfilePath(selectedEvent.pubkey),
          },
        )
        : createSelectedNoteArticle(
          selectedEvent,
          profileFor(profilesByPubkey, selectedEvent.pubkey),
          {
            rootID,
            referencedByID,
            isFocused: true,
            replyCount: replyCounts[selectedID] || 0,
            userHref: threadProfilePath(selectedEvent.pubkey),
          },
        ),
    );
    return section;
  }

  const note = reusableSelectedShell
    ? createSelectedNoteArticleFromShell(
      reusableSelectedShell,
      rootEvent,
      profileFor(profilesByPubkey, rootEvent.pubkey),
      {
        rootID,
        referencedByID,
        hasVisibleChildren: hasThreadReplies,
        isFocused: selectedID === rootID,
        replyCount: replyCounts[rootID] || 0,
        userHref: threadProfilePath(rootEvent.pubkey),
      },
    )
    : createSelectedNoteArticle(
      rootEvent,
      profileFor(profilesByPubkey, rootEvent.pubkey),
      {
        rootID,
        referencedByID,
        hasVisibleChildren: hasThreadReplies,
        isFocused: selectedID === rootID,
        replyCount: replyCounts[rootID] || 0,
        userHref: threadProfilePath(rootEvent.pubkey),
      },
    );
  section.append(note);
  return section;
}

function renderParticipantList(events, profilesByPubkey) {
  const list = document.createElement("ul");
  list.className = "thread-people";
  const pubkeys = threadParticipantPubkeys(events);
  if (!pubkeys.length) {
    const empty = document.createElement("li");
    empty.className = "muted";
    empty.textContent = "No participants yet.";
    list.append(empty);
    return list;
  }

  pubkeys.slice(0, 24).forEach((pk) => {
    const profile = profileFor(profilesByPubkey, pk);
    const li = document.createElement("li");
    const link = document.createElement("a");
    link.className = "thread-person";
    link.href = threadProfilePath(pk);
    link.dataset.relayAware = "";
    const avatarURL = preferredAvatarURL(profile);
    const retryAvatar = avatarRetryURL(profile);
    if (avatarURL) {
      const img = document.createElement("img");
      img.alt = "";
      img.loading = "lazy";
      img.decoding = "async";
      setAvatarImageSource(img, avatarURL, { retryURL: retryAvatar });
      link.append(img);
    } else {
      const fallback = document.createElement("span");
      fallback.className = "thread-person-avatar-fallback";
      fallback.setAttribute("aria-hidden", "true");
      fallback.textContent = "@";
      link.append(fallback);
    }
    const meta = createThreadParticipantMeta(profile);
    link.append(meta);
    li.append(link);
    list.append(li);
  });
  return list;
}

function renderParticipantsRail(events, profilesByPubkey, filteredEvents = []) {
  const aside = document.createElement("aside");
  aside.className = "right-rail";
  aside.dataset.threadFragment = "participants";

  const panel = document.createElement("section");
  panel.className = "thread-people-panel";
  const heading = document.createElement("h2");
  heading.textContent = "People in this thread";
  panel.append(heading);

  const collapsedList = renderParticipantList(events, profilesByPubkey);
  collapsedList.dataset.threadCollapsedParticipants = "";
  panel.append(collapsedList);
  if (filteredEvents.length) {
    const expandedList = renderParticipantList([...events, ...filteredEvents], profilesByPubkey);
    expandedList.dataset.threadExpandedParticipants = "";
    expandedList.hidden = true;
    panel.append(expandedList);
  }
  aside.append(panel);
  return aside;
}

export function renderThreadIntoShell(root, bundle, profilesByPubkey, referencedByID = null) {
  if (routeKind(globalThis.location?.pathname || "") !== "thread") return;
  const { root: rootEvent, selected, events, parentByID, wot = {} } = bundle;
  const rootID = canonicalHex64(rootEvent.id);
  const selectedEvent = selected || rootEvent;
  const selectedID = canonicalHex64(selectedEvent.id);
  const threadResolved = bundle.threadViewResolved
    ?? resolveThreadView(rootEvent, selectedEvent, events, parentByID);
  const { view: threadView, replyCounts } = threadResolved;
  const focused = Boolean(threadView.focusMode);
  const wotView = { ...wot };
  const reusableReplyShells = collectReusableThreadShells(root);

  const column = activeThreadColumn(root);
  if (column) {
    column.dataset.threadRootId = rootID;
    column.dataset.threadSelectedId = selectedID;
    column.dataset.relayNativeThread = "1";
    delete column.dataset.threadRoutePending;
    if (focused) column.dataset.threadExpectsFocus = "1";
    else delete column.dataset.threadExpectsFocus;
  }

  const summary = root.querySelector("#thread-summary");
  if (summary) summary.replaceChildren(renderSummaryHeader());

  const existingTreeHost = root.querySelector("#thread-tree-view");
  const treeSection = renderTreeSection(rootEvent, events, parentByID, profilesByPubkey, selectedID, wotView, referencedByID);
  if (existingTreeHost) existingTreeHost.replaceWith(treeSection);
  else column?.append(treeSection);

  const ancestors = root.querySelector("#thread-ancestors");
  if (ancestors) ancestors.replaceChildren();

  const replyParentID = focused || selectedID !== rootID ? selectedID : rootID;
  const replyEvents =
    bundle.linearReplyPage ??
    threadResolved.linearNodes.map((node) => node.event);
  const hasThreadReplies = replyEvents.length > 0;
  const hasFilteredThreadReplies = Boolean(wot?.enabled && wot.filteredReplyNodes?.length);
  const hasThreadReplyRail = hasThreadReplies || hasFilteredThreadReplies;

  const focusHost = root.querySelector("#thread-focus");
  const reusableSelectedShell =
    root.querySelector(`#thread-focus > #note-${selectedID}, #thread-replies > #note-${selectedID}`) || null;
  const focusSection = renderFocusSection(rootEvent, selectedEvent, events, parentByID, profilesByPubkey, referencedByID, {
    threadView,
    replyCounts,
    hasThreadReplies: hasThreadReplyRail,
    reusableSelectedShell,
    reusableReplyShells,
    parentUnavailable: bundle.selectedParentUnavailable === true,
  });
  if (focusHost instanceof HTMLElement) {
    const hasParentSkeleton = Boolean(focusHost.querySelector(".thread-focus-parent--skeleton"));
    const canUpdateFocusInPlace =
      !hasParentSkeleton &&
      reusableSelectedShell &&
      focusHost.id === "thread-focus" &&
      !focused;
    if (canUpdateFocusInPlace) {
      focusHost.replaceChildren(...focusSection.childNodes);
    } else {
      focusHost.replaceWith(focusSection);
    }
  } else if (column) {
    const existingFocus = column.querySelector("#thread-focus");
    if (existingFocus) existingFocus.replaceWith(focusSection);
    else column.append(focusSection);
  }
  applyDestinationThreadTransition(root, selectedID);

  const repliesSection = root.querySelector(".thread-replies") || column;
  root.querySelector(".thread-rail-gap")?.remove();
  const repliesHost = repliesSection ? ensureThreadRepliesHost(repliesSection) : null;

  if (hasThreadReplyRail && repliesSection) {
    repliesSection.prepend(createThreadRailGap(1));
  }

  const childrenByParent = buildThreadChildren(events, parentByID, rootID);
  if (repliesHost) {
    repliesHost.classList.remove("thread-replies-skeleton");
    repliesHost.classList.add("comments");
    repliesHost.removeAttribute("aria-busy");
    const nextReplies = document.createDocumentFragment();
    replyEvents.forEach((event, index) => {
      const eventID = canonicalHex64(event.id);
      const childList = childrenByParent.get(eventID) || [];
      const directCount = replyCounts[eventID] ?? childList.length;
      nextReplies.append(
        createReplyShellFromShell(
          reusableReplyShells.get(`note-${eventID}`) || null,
          event,
          profileFor(profilesByPubkey, event.pubkey),
          {
            rootID,
            selectedID,
            depth: 1,
            isLast: index === replyEvents.length - 1 && !bundle.replyPagination?.hasMore,
            hasChildren: false,
            isFocused: eventID === selectedID,
            referencedByID,
            replyCount: directCount,
          },
        ),
      );
    });
    repliesHost.replaceChildren(nextReplies);
  }

  repliesSection?.querySelector("[data-thread-filtered-replies]")?.remove();
  repliesSection?.querySelector(".thread-filtered-replies-toggle")?.remove();
  if (wot?.enabled && wot.filteredReplyNodes?.length && repliesHost) {
    const filteredBlock = document.createElement("div");
    filteredBlock.className = "comments thread-filtered-replies";
    filteredBlock.dataset.threadFilteredReplies = "";
    filteredBlock.dataset.threadRootId = rootID;
    filteredBlock.dataset.threadSelectedId = selectedID;
    filteredBlock.hidden = true;
    const filteredReplies = document.createDocumentFragment();
    wot.filteredReplyNodes.forEach((node, index) => {
      filteredReplies.append(
        createReplyShellFromShell(
          reusableReplyShells.get(`note-${canonicalHex64(node.event.id)}`) || null,
          node.event,
          profileFor(profilesByPubkey, node.event.pubkey),
          {
            rootID,
            selectedID,
            depth: node.depth,
            isLast: index === wot.filteredReplyNodes.length - 1,
            hasChildren: false,
            referencedByID,
          },
        ),
      );
    });
    filteredBlock.append(filteredReplies);
    repliesHost.after(filteredBlock);

    const toggleWrap = document.createElement("p");
    toggleWrap.className = "thread-filtered-replies-toggle";
    const toggle = document.createElement("button");
    toggle.className = "link-button";
    toggle.type = "button";
    toggle.dataset.threadFilteredRepliesToggle = "";
    toggle.dataset.collapsedLabel = filteredRepliesToggleLabel(wot.filteredReplyNodes.length);
    toggle.dataset.expandedLabel = "hide";
    toggle.textContent = filteredRepliesToggleLabel(wot.filteredReplyNodes.length);
    toggleWrap.append(toggle);
    filteredBlock.after(toggleWrap);
  }

  if (bundle.replyPagination) {
    configureThreadLoadMore(root, {
      rootID,
      parentID: replyParentID,
      selectedID,
      ...bundle.replyPagination,
    });
  } else {
    const loadMore = root.querySelector("[data-thread-load-more]");
    if (loadMore) loadMore.hidden = true;
  }

  const rail = root.querySelector(".right-rail[data-thread-fragment='participants']");
  const nextRail = renderParticipantsRail(events, profilesByPubkey, wot.filteredReplies || []);
  if (rail) {
    rail.replaceWith(nextRail);
  } else if (column) {
    column.after(nextRail);
  }
  applyThreadViewVisibilityFromPreference(root);
}

export function isRelayNativeThread(root = document) {
  return root.querySelector(".feed-column[data-relay-native-thread='1']") != null;
}
