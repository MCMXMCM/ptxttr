import { syncMuteToggleButtons } from "./mutations.js";
import { ensureNoteReactionsDelegated, formatThousandsSpaced, openReactionsModal } from "./reactions.js";
import { normalizePubkey } from "./session.js";
import { compactReplyBadge, padAsciiDecimal, replyLabelForCount } from "./reply-label.js";
import { getImageModePref } from "./sort-prefs.js";
import { NOSTR_REF_PATTERN, nostrRefLink } from "./nip27.js";
import { FEED_LOADER_STATUSES } from "./shell.js";
import { prepareInlineVideo } from "./inline-video.js";

// Link #words to /tag/… (Unicode letters, numbers, underscore). The server
// applies stricter path rules when resolving the feed URL.
const HASHTAG_PATTERN_STEM = "(?:^|[\\s])#([\\p{L}\\p{N}_]+)";
const HASHTAG_PATTERN = new RegExp(HASHTAG_PATTERN_STEM, "gu");
const NOSTR_OR_HASHTAG_PATTERN = new RegExp(`${NOSTR_REF_PATTERN.source}|${HASHTAG_PATTERN_STEM}`, "giu");

const minColumns = 32;
const maxColumns = 160;
const collapsedNoteLines = 3;
/** Extra monospace columns reserved on feed note header rows for `padding-left` + tile (see `.note-feed-avatar`). */
const feedNoteAvatarRuneReserve = 5;
/** NBSP columns after `| ` on "Replying to" row; keep in sync with `--feed-repost-text-start` in app.css. */
const feedReplyContextInsetRunes = 7;
const MEDIA_URL_PATTERN = /https?:\/\/[^\s<>"'`]+/gi;
const IMAGE_EXT_PATTERN = /\.(?:png|jpe?g|gif|webp|avif|svg)(?:[?#][^\s<>"']*)?$/i;
const VIDEO_EXT_PATTERN = /\.(?:mp4|webm|m4v|mov|ogv|ogg)(?:[?#][^\s<>"']*)?$/i;
const TRAILING_URL_PUNCTUATION = /[),.!?;:]+$/;
const HTTPS_URL_PATTERN = /https:\/\/[^\s<>"'`]+/gi;
const observed = new WeakSet();
const mobileActionsQuery = window.matchMedia("(max-width: 700px)");
let useDoubleWideCells = true;
const graphemeSegmenter = "Intl" in window && "Segmenter" in Intl
  ? new Intl.Segmenter(undefined, { granularity: "grapheme" })
  : null;
const resizeObserver = "ResizeObserver" in window ? new ResizeObserver((entries) => {
  entries.forEach((entry) => renderAscii(entry.target));
}) : null;
const imageViewerState = {
  urls: [],
  index: 0,
  ownerNoteID: "",
};
let feedLoaderTick = 0;
let feedLoaderTimer = 0;
const loaderLayoutObservedColumns = new WeakSet();
let feedLoaderColumnObserver = null;

function observeFeedLoaderColumn(column) {
  if (!column || !(column instanceof Element)) return;
  if (loaderLayoutObservedColumns.has(column)) return;
  loaderLayoutObservedColumns.add(column);
  if (!("ResizeObserver" in window)) return;
  if (!feedLoaderColumnObserver) {
    feedLoaderColumnObserver = new ResizeObserver(() => {
      renderFeedLoaders(document);
      renderSkeletonWaveCards(document);
    });
  }
  feedLoaderColumnObserver.observe(column);
}

function registerLoaderLayoutObservers(root = document) {
  queryFeedLoaders(root).forEach((loader) => {
    observeFeedLoaderColumn(loader.closest(".feed-column") || loader);
  });
  querySkeletonWaveCards(root).forEach((card) => {
    observeFeedLoaderColumn(card.closest(".feed-column") || card.parentElement);
  });
}

function graphemes(value) {
  if (!graphemeSegmenter) return [...value];
  return [...graphemeSegmenter.segment(value)].map((item) => item.segment);
}

function isWideGrapheme(value) {
  if (!useDoubleWideCells) return false;
  if (/\p{Extended_Pictographic}/u.test(value)) return true;
  return [...value].some((char) => {
    const code = char.codePointAt(0);
    return (code >= 0x1100 && code <= 0x115f) ||
      code === 0x2329 ||
      code === 0x232a ||
      (code >= 0x2e80 && code <= 0xa4cf) ||
      (code >= 0xac00 && code <= 0xd7a3) ||
      (code >= 0xf900 && code <= 0xfaff) ||
      (code >= 0xfe10 && code <= 0xfe19) ||
      (code >= 0xfe30 && code <= 0xfe6f) ||
      (code >= 0xff00 && code <= 0xff60) ||
      (code >= 0xffe0 && code <= 0xffe6);
  });
}

function runeLength(value) {
  return graphemes(value).reduce((total, item) => total + (isWideGrapheme(item) ? 2 : 1), 0);
}

function takeColumns(value, width) {
  let used = 0;
  let out = "";
  for (const item of graphemes(value)) {
    const itemWidth = isWideGrapheme(item) ? 2 : 1;
    if (used + itemWidth > width) break;
    out += item;
    used += itemWidth;
  }
  return out;
}

function dropColumns(value, width) {
  let used = 0;
  let index = 0;
  const items = graphemes(value);
  for (; index < items.length; index += 1) {
    const itemWidth = isWideGrapheme(items[index]) ? 2 : 1;
    if (used + itemWidth > width) break;
    used += itemWidth;
  }
  return items.slice(index).join("");
}

function truncateMiddle(value, max) {
  if (runeLength(value) <= max) return value;
  if (max <= 1) return "…";
  const head = Math.floor((max - 1) / 2);
  const tail = max - 1 - head;
  const items = graphemes(value);
  let tailText = "";
  let tailWidth = 0;
  for (let index = items.length - 1; index >= 0; index -= 1) {
    const itemWidth = isWideGrapheme(items[index]) ? 2 : 1;
    if (tailWidth + itemWidth > tail) break;
    tailText = items[index] + tailText;
    tailWidth += itemWidth;
  }
  return `${takeColumns(value, head)}…${tailText}`;
}

// Must match internal/httpx/render.go replyTextWidth.
function replyTextWidth(width) {
  const w = width - 8;
  return w < 20 ? 20 : w;
}

function repeat(char, count) {
  return char.repeat(Math.max(1, count));
}

const FEED_LOADER_FRAME_VARIANTS = 3;

function fillToWidth(pattern, cols) {
  if (cols < 1 || !pattern) return "";
  let out = "";
  while (runeLength(out) < cols) {
    out += pattern;
  }
  return takeColumns(out, cols);
}

/** Monospace column width for feed / skeleton wave cards (same font as `measureColumns`). */
function feedLoaderMeasureRoot(card) {
  const loader = card.closest("[data-feed-loader]");
  if (loader) return loader;
  return card.closest(".feed-column") || card.closest("main") || document.documentElement;
}

/**
 * Two stacked ASCII boxes, each line exactly `width` monospace columns (closed + on the right).
 * @param {number} width outer width including border `+` … `+`
 * @param {number} cardIndex which stacked card (offsets wave phase)
 * @param {number} frameIndex animation frame
 */
function buildFeedLoaderCardText(width, cardIndex, frameIndex) {
  const w = Math.max(minColumns, Math.min(maxColumns, width || minColumns));
  const inner = Math.max(1, w - 4);
  const rule = `+${repeat("-", w - 2)}+`;
  const phase = (Number(cardIndex) + Number(frameIndex)) % FEED_LOADER_FRAME_VARIANTS;
  const tildePattern = phase === 0 ? "~ " : phase === 1 ? " ~" : "~ ";
  const dashPattern = (Number(cardIndex) + Number(frameIndex)) % 2 === 0 ? "---------- " : "----------- ";
  const rowTilde = `| ${padRight(fillToWidth(tildePattern, inner), inner)} |`;
  const rowDash = `| ${padRight(fillToWidth(dashPattern, inner), inner)} |`;
  return [rule, rowTilde, rowDash, rule, rowTilde, rowDash, rule].join("\n");
}

function padRight(value, width) {
  return value + " ".repeat(Math.max(0, width - runeLength(value)));
}

function appendLine(pre, parts = []) {
  const line = document.createElement("span");
  line.className = "ascii-line";
  parts.forEach((part) => line.append(part));
  pre.append(line, "\n");
}

function noteChrome(value) {
  const item = document.createElement("span");
  item.textContent = value;
  return item;
}

function link(href, label) {
  const item = document.createElement("a");
  item.href = href;
  item.dataset.relayAware = "";
  item.textContent = label;
  return item;
}

function externalHttpsLink(href, label) {
  const item = document.createElement("a");
  item.href = href;
  item.target = "_blank";
  item.rel = "noopener noreferrer";
  item.textContent = label;
  return item;
}

function findAsciiMediaPreviewRow(noteRoot, href) {
  if (!noteRoot || !href) return null;
  for (const row of noteRoot.querySelectorAll("[data-ascii-media-preview-url]")) {
    if (row.dataset.asciiMediaPreviewUrl === href) return row;
  }
  return null;
}

/** True for direct https links to common video file extensions. */
function isVideoAssetHttpsUrl(href) {
  return Boolean(href && VIDEO_EXT_PATTERN.test(href));
}

/**
 * Dashed control that toggles a hidden inline preview row. Video URLs use a
 * <button> so taps never navigate: many CDNs (e.g. some Blossom endpoints)
 * mislabel bytes; Safari then offers a .bin download instead of playing.
 */
function mediaNoteInlineLink(href, label, noteRoot) {
  const applyToggle = (control) => {
    const row = findAsciiMediaPreviewRow(noteRoot, href);
    if (!row) return;
    const show = row.hidden;
    row.hidden = !show;
    control.setAttribute("aria-expanded", show ? "true" : "false");
  };
  if (isVideoAssetHttpsUrl(href)) {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "note-image-toggle";
    b.textContent = label;
    b.setAttribute("aria-expanded", "false");
    b.addEventListener("click", (event) => {
      event.preventDefault();
      applyToggle(b);
    });
    return b;
  }
  const a = document.createElement("a");
  a.href = href;
  a.className = "note-image-toggle";
  a.textContent = label;
  a.setAttribute("aria-expanded", "false");
  a.rel = "noopener noreferrer";
  a.addEventListener("click", (event) => {
    event.preventDefault();
    applyToggle(a);
  });
  return a;
}

function appendHttpsOrMediaLineAnchor(target, href, label, container) {
  if (
    container &&
    (isVideoAssetHttpsUrl(href) || (!getImageModePref() && isMediaAssetHttpsUrl(href)))
  ) {
    target.append(mediaNoteInlineLink(href, label, container));
    return;
  }
  const a = externalHttpsLink(href, label);
  a.classList.add("ascii-content-link");
  target.append(a);
}

/** Same image/video detection as extractMediaItems (after trailing punct strip). */
function isMediaAssetHttpsUrl(href) {
  if (!href) return false;
  return IMAGE_EXT_PATTERN.test(href) || VIDEO_EXT_PATTERN.test(href);
}

/**
 * Resolves the original https hrefs for a line whose visible text was clipped
 * by `truncateMiddle` / `addTrailingDots`. The clipped text loses URL endings,
 * so `appendHttpsUrls` cannot re-derive the real href from what it renders;
 * this scan must mirror `appendHttpsUrls`' match + trailing-punct rules so the
 * Nth match here stays the Nth match there.
 */
function listHttpsAutolinkHrefsInOrder(fullText) {
  if (!fullText) return [];
  const hrefs = [];
  HTTPS_URL_PATTERN.lastIndex = 0;
  let m;
  while ((m = HTTPS_URL_PATTERN.exec(fullText)) !== null) {
    const raw = m[0];
    const href = raw.replace(TRAILING_URL_PUNCTUATION, "");
    if (!isMediaAssetHttpsUrl(href)) hrefs.push(href);
  }
  return hrefs;
}

/**
 * Appends note line text with optional per-line external URL styling.
 * Used when wrapText split a long https URL across rows: continuation
 * fragments do not match HTTPS_URL_PATTERN, so we carry { href } (whole
 * line linked) or { href, linkedPrefix } (prefix linked, suffix plain).
 */
function appendAsciiTextWithLineLink(target, text, container, lineLink, urlState) {
  if (!lineLink?.href) return appendMentionAwareText(target, text, container, urlState);
  const { linkPart, collapseSuffix } = splitCollapsePreviewSuffix(text);
  if (!lineLink.linkedPrefix) {
    appendHttpsOrMediaLineAnchor(target, lineLink.href, linkPart, container);
    if (collapseSuffix) target.append(collapseSuffix);
    return runeLength(text);
  }
  const prefix = lineLink.linkedPrefix;
  if (linkPart.startsWith(prefix)) {
    appendHttpsOrMediaLineAnchor(target, lineLink.href, prefix, container);
    const after = linkPart.slice(prefix.length);
    if (after) appendHttpsUrls(target, after, urlState, container);
    if (collapseSuffix) target.append(collapseSuffix);
    return runeLength(text);
  }
  appendHttpsOrMediaLineAnchor(target, lineLink.href, linkPart, container);
  if (collapseSuffix) target.append(collapseSuffix);
  return runeLength(text);
}

/** Autolink https URLs in plain text; image URLs toggle when image mode is off; video URLs always toggle (avoid Safari .bin download on mislabeled hosts). */
function appendHttpsUrls(target, text, urlState, container) {
  if (!text) return 0;
  let used = 0;
  HTTPS_URL_PATTERN.lastIndex = 0;
  let cursor = 0;
  let match;
  while ((match = HTTPS_URL_PATTERN.exec(text)) !== null) {
    const start = match.index;
    if (start > cursor) {
      const before = text.slice(cursor, start);
      target.append(before);
      used += runeLength(before);
    }
    const raw = match[0];
    const displayHref = raw.replace(TRAILING_URL_PUNCTUATION, "");
    const punctSuffix = raw.slice(displayHref.length);
    if (isMediaAssetHttpsUrl(displayHref)) {
      if (container && (!getImageModePref() || isVideoAssetHttpsUrl(displayHref))) {
        target.append(mediaNoteInlineLink(displayHref, displayHref, container));
        used += runeLength(displayHref);
        if (punctSuffix) {
          target.append(punctSuffix);
          used += runeLength(punctSuffix);
        }
      } else {
        target.append(raw);
        used += runeLength(raw);
      }
    } else {
      let resolvedHref = displayHref;
      if (urlState?.hrefs && urlState.nextIndex.i < urlState.hrefs.length) {
        resolvedHref = urlState.hrefs[urlState.nextIndex.i];
        urlState.nextIndex.i += 1;
      }
      const a = externalHttpsLink(resolvedHref, displayHref);
      a.classList.add("ascii-content-link");
      target.append(a);
      used += runeLength(displayHref);
      if (punctSuffix) {
        target.append(punctSuffix);
        used += runeLength(punctSuffix);
      }
    }
    cursor = start + raw.length;
  }
  if (cursor < text.length) {
    const tail = text.slice(cursor);
    target.append(tail);
    used += runeLength(tail);
  }
  return used;
}

// readMentionMap parses the JSON `data-ascii-mentions` attribute on a note
// container into a `label -> { href, title }` map plus a precompiled regex
// that matches any of the labels in priority (longest-first) order. The
// server pre-resolves NIP-27 references so the rewritten note source already
// contains friendly labels (e.g. "@PaulKeating", "note:abc123de"); we just
// need to turn each label back into a link.
function readMentionMap(container) {
  if (!container) return null;
  if (container.__asciiMentionMap !== undefined) return container.__asciiMentionMap;
  container.__asciiMentionMap = null;
  const raw = container.getAttribute?.("data-ascii-mentions");
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed) || parsed.length === 0) return null;
    const map = new Map();
    parsed.forEach((entry) => {
      if (!entry || typeof entry.label !== "string" || typeof entry.href !== "string") return;
      if (!entry.label || !entry.href) return;
      map.set(entry.label, { href: entry.href, title: typeof entry.title === "string" ? entry.title : "" });
    });
    if (map.size === 0) return null;
    const labels = [...map.keys()].sort((a, b) => b.length - a.length);
    const escaped = labels.map((label) => label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"));
    const pattern = new RegExp(
      `(${escaped.join("|")})|${NOSTR_REF_PATTERN.source}|${HASHTAG_PATTERN_STEM}`,
      "giu",
    );
    container.__asciiMentionMap = { map, pattern };
    return container.__asciiMentionMap;
  } catch {
    return null;
  }
}

function appendMentionAwareText(target, text, container, urlState) {
  if (!text) return 0;
  const ctx = readMentionMap(container);
  const hasNostr = !ctx && text.indexOf("nostr:") >= 0;
  const hasHashtag = !ctx && HASHTAG_PATTERN.test(text);
  HASHTAG_PATTERN.lastIndex = 0;
  if (!ctx && !hasNostr && !hasHashtag) {
    return appendHttpsUrls(target, text, urlState, container);
  }
  const pattern = ctx ? ctx.pattern : NOSTR_OR_HASHTAG_PATTERN;
  pattern.lastIndex = 0;
  let cursor = 0;
  let used = 0;
  let match;
  while ((match = pattern.exec(text)) !== null) {
    const start = match.index;
    if (start > cursor) {
      const before = text.slice(cursor, start);
      used += appendHttpsUrls(target, before, urlState, container);
    }
    const token = match[0];
    let href = "";
    let label = token;
    let title = token;
    if (ctx && ctx.map.has(token)) {
      const info = ctx.map.get(token);
      href = info.href;
      title = info.title || "";
    } else if (token.toLowerCase().startsWith("nostr:")) {
      const ref = nostrRefLink(token);
      if (ref?.href && ref?.label) {
        href = ref.href;
        label = ref.label;
        title = token;
      }
    } else {
      const hm = /^(\s*)#([\p{L}\p{N}_]+)$/u.exec(token);
      if (hm) {
        const prefix = hm[1];
        const tag = hm[2];
        href = `/tag/${encodeURIComponent(tag)}`;
        label = `#${tag}`;
        title = label;
        if (prefix) {
          target.append(prefix);
          used += runeLength(prefix);
        }
      }
    }
    if (href) {
      const mention = link(href, label);
      mention.classList.add("ascii-content-link");
      if (title) mention.title = title;
      target.append(mention);
      used += runeLength(label);
    } else {
      used += appendHttpsUrls(target, token, urlState, container);
    }
    cursor = start + token.length;
  }
  if (cursor < text.length) {
    const tail = text.slice(cursor);
    used += appendHttpsUrls(target, tail, urlState, container);
  }
  return used;
}

function button(label, onClick) {
  const item = document.createElement("button");
  item.className = "link-button";
  item.type = "button";
  item.textContent = label;
  item.addEventListener("click", onClick);
  return item;
}

function viewMoreButton(container, width) {
  return button("view more", () => {
    container.dataset.asciiExpanded = "true";
    renderNote(container, width);
  });
}

function reactionGlyphChars(container) {
  const voter = String(container.dataset.asciiReactionViewer || "").trim();
  const up = voter === "+" ? "▲" : "△";
  const down = voter === "-" ? "▼" : "▽";
  return { up, down };
}

function reactionVoteButton(container, side, glyph) {
  const b = document.createElement("button");
  b.type = "button";
  b.className = "link-button ascii-reaction-vote";
  b.dataset.asciiReactionVote = side;
  b.setAttribute("aria-label", side === "up" ? "Upvote" : "Downvote");
  b.textContent = glyph;
  return b;
}

/** Ruler width (`runeBlockLen`) plus footer nodes for `[△] n [▽]`; single parse per render. */
function reactionLayoutSegments(container) {
  const total = Number.parseInt(container.dataset.asciiReactionTotal || "0", 10) || 0;
  const { up, down } = reactionGlyphChars(container);
  const numRaw = formatThousandsSpaced(total, 1);
  const mid = ` ${numRaw} `;
  const runeBlockLen = runeLength(`[${up}]`) + runeLength(mid) + runeLength(`[${down}]`);
  const footerParts = [
    noteChrome("["),
    reactionVoteButton(container, "up", up),
    noteChrome("]"),
    noteChrome(mid),
    noteChrome("["),
    reactionVoteButton(container, "down", down),
    noteChrome("]"),
  ];
  return { runeBlockLen, footerParts };
}

async function copyText(value, trigger) {
  if (!value) return;
  try {
    await navigator.clipboard.writeText(value);
    const previous = trigger.textContent;
    trigger.textContent = "[copied]";
    setTimeout(() => {
      trigger.textContent = previous;
    }, 1200);
  } catch {
    window.prompt("Copy this value", value);
  }
}

export function closeActionMenus(except = null) {
  document.querySelectorAll(".ascii-action-menu.is-open").forEach((menu) => {
    if (menu !== except) {
      menu.classList.remove("is-open");
      menu.querySelector(".profile-stats-menu-trigger")?.setAttribute("aria-expanded", "false");
    }
  });
}

function actionMenu(container, label, items) {
  const wrap = document.createElement("span");
  wrap.className = "ascii-action-menu";
  const trigger = button(label, (event) => {
    event.stopPropagation();
    const isOpen = wrap.classList.toggle("is-open");
    closeActionMenus(isOpen ? wrap : null);
  });
  trigger.setAttribute("aria-haspopup", "menu");
  wrap.append(trigger);

  const menu = document.createElement("span");
  menu.className = "ascii-action-menu-list";
  menu.setAttribute("role", "menu");
  menu.append(...items);
  wrap.append(menu);
  return wrap;
}

function copyButton(label, value) {
  return button(label, (event) => {
    event.stopPropagation();
    copyText(value || "", event.currentTarget);
  });
}

function noteIDForContainer(container) {
  return container?.id?.replace(/^note-/, "") || "";
}

function bookmarkToggleButton(container) {
  const id = noteIDForContainer(container);
  const item = document.createElement("button");
  item.className = "link-button";
  item.type = "button";
  item.dataset.bookmarkToggle = "1";
  item.dataset.noteId = id;
  const isBookmarked = container?.dataset?.bookmarked === "1";
  item.textContent = isBookmarked ? "[unbookmark]" : "[bookmark]";
  if (!id) item.disabled = true;
  return item;
}

function repostComposeButton(container) {
  const id = noteIDForContainer(container);
  const item = document.createElement("button");
  item.className = "link-button";
  item.type = "button";
  item.dataset.repostAction = "1";
  item.dataset.repostTargetId = id;
  item.dataset.repostPubkey = container?.dataset?.replyPubkey || "";
  item.dataset.repostRelay = container?.dataset?.asciiRelay || "";
  item.textContent = "[repost]";
  if (!id) item.disabled = true;
  return item;
}

function muteAuthorMenuButton(container) {
  const pk = normalizePubkey(container?.dataset?.replyPubkey || "");
  const item = document.createElement("button");
  item.className = "link-button";
  item.type = "button";
  item.setAttribute("data-mute-toggle", "");
  item.setAttribute("data-mute-bracket-labels", "");
  item.setAttribute("data-pubkey", pk || "");
  item.textContent = "[mute]";
  if (!pk) item.disabled = true;
  return item;
}

function replyActionLink(container, href, label = "reply") {
  const item = link(href, label);
  item.dataset.replyAction = "1";
  item.dataset.replyRootId = container?.dataset?.replyRootId || "";
  item.dataset.replyTargetId = container?.dataset?.replyTargetId || noteIDForContainer(container);
  item.dataset.replyPubkey = container?.dataset?.replyPubkey || "";
  return item;
}

/** DOM nodes for `[reply]` (brackets as chrome, label is the link). */
function bracketedReplyLink(container, href) {
  return [noteChrome("["), replyActionLink(container, href), noteChrome("]")];
}

function replyThreadHref(container) {
  return container.dataset.asciiThreadHref || container.dataset.asciiSelectHref || "#";
}

/** Shared `[...]` items for feed notes, thread replies, and selected focus card. */
function asciiNoteOverflowMenuItems(container) {
  return [
    bookmarkToggleButton(container),
    muteAuthorMenuButton(container),
    viewReactionsMenuButton(container),
    repostComposeButton(container),
    link(replyThreadHref(container), "[view thread]"),
    copyButton("[copy note id]", container.dataset.asciiNevent),
    copyButton("[copy user public key]", container.dataset.asciiNpub),
  ];
}

function viewReactionsMenuButton(container) {
  const id = noteIDForContainer(container);
  const item = document.createElement("button");
  item.className = "link-button";
  item.type = "button";
  item.textContent = "[view reactions]";
  if (!id) item.disabled = true;
  item.addEventListener("click", (event) => {
    event.stopPropagation();
    closeActionMenus();
    void openReactionsModal(id);
  });
  return item;
}

function noteMenu(container) {
  return actionMenu(container, "[...]", asciiNoteOverflowMenuItems(container));
}

function sourceText(container) {
  return container.querySelector(":scope > .ascii-source")?.content?.textContent?.trim() || "";
}

function referenceSourceText(container) {
  return container.querySelector(":scope > .ascii-reference-source")?.content?.textContent?.trim() || "";
}

/** Quote/repost body text with image placeholders applied (shared by tree quotes and nested ASCII refs). */
function referenceBodyDisplaySource(container, imageMode) {
  const raw = referenceSourceText(container);
  const referenceMediaItems = imageMode ? extractMediaItems(raw) : [];
  return displaySourceForMedia(raw, referenceMediaItems, imageMode);
}

function imageMount(container) {
  return container.querySelector(":scope [data-note-image-mount]");
}

/** Detach media mount before `pre.textContent = ""` so it is not destroyed; caller re-appends inside `pre`. */
function takeImageMountForPreRebuild(container) {
  const mount = imageMount(container);
  if (mount) mount.remove();
  return mount;
}

/** Detach feed avatar link before `pre.textContent = ""` so it is not destroyed; caller prepends back into `pre`. */
function takeFeedAvatarForPreRebuild(pre) {
  const link = pre.querySelector(":scope > .note-feed-avatar");
  if (!link) return null;
  link.remove();
  return link;
}

function extractMediaItems(text) {
  const matches = text.match(MEDIA_URL_PATTERN) || [];
  const unique = new Set();
  const items = [];
  matches.forEach((raw) => {
    const url = raw.replace(TRAILING_URL_PUNCTUATION, "");
    if (!url || unique.has(url)) return;
    let type = "";
    if (IMAGE_EXT_PATTERN.test(url)) {
      type = "image";
    } else if (VIDEO_EXT_PATTERN.test(url)) {
      type = "video";
    }
    if (!type) return;
    unique.add(url);
    items.push({ url, type });
  });
  return items;
}

function mergeMediaItemsDedup(a, b) {
  const seen = new Set();
  const out = [];
  for (const list of [a, b]) {
    for (const item of list) {
      if (!item?.url || seen.has(item.url)) continue;
      seen.add(item.url);
      out.push(item);
    }
  }
  return out;
}

/** Parse `data-ascii-imeta-media` JSON from the note/reply shell (NIP-94 imeta tags). */
function extractImetaMediaFromNoteContainer(container) {
  if (!container?.dataset?.asciiImetaMedia) return [];
  try {
    const parsed = JSON.parse(container.dataset.asciiImetaMedia);
    if (!Array.isArray(parsed)) return [];
    const out = [];
    for (const entry of parsed) {
      const url = typeof entry?.url === "string" ? entry.url.trim() : "";
      const type = typeof entry?.type === "string" ? entry.type.trim() : "";
      if (!url || (type !== "image" && type !== "video")) continue;
      if (!/^https?:\/\//i.test(url)) continue;
      out.push({ url, type });
    }
    return out;
  } catch {
    return [];
  }
}

/** Media URLs from note body plus optional `imeta` tags (deduped). */
function mainBodyMediaItems(container, text) {
  return mergeMediaItemsDedup(extractMediaItems(text), extractImetaMediaFromNoteContainer(container));
}

/** @param {ReturnType<typeof mainBodyMediaItems> | undefined} precomputedMain when caller already computed main-body items for `sourceText(container)`. */
function mediaItemsForAsciiNote(container, precomputedMain) {
  const main =
    precomputedMain ?? mainBodyMediaItems(container, sourceText(container));
  const refMode = container.dataset.asciiRefMode || "";
  if (refMode !== "quote" && refMode !== "repost") return main;
  return mergeMediaItemsDedup(main, extractMediaItems(referenceSourceText(container)));
}

function imageItems(items) {
  return items.filter((item) => item.type === "image");
}

function mediaSummaryLabel(items, compactMobile = false) {
  if (!items.length) return "";
  let images = 0;
  let videos = 0;
  for (const item of items) {
    if (item.type === "image") images++;
    else if (item.type === "video") videos++;
  }
  if (compactMobile) {
    if (images > 0 && videos === 0) return `${images} img`;
    if (videos > 0 && images === 0) return `${videos} vid`;
    return `${items.length} v/i`;
  }
  if (images > 0 && videos === 0) {
    if (images === 1) return `${padAsciiDecimal(1, 2)} image `;
    return `${padAsciiDecimal(images, 2)} images`;
  }
  if (videos > 0 && images === 0) {
    if (videos === 1) return `${padAsciiDecimal(1, 2)} video `;
    return `${padAsciiDecimal(videos, 2)} videos`;
  }
  const n = items.length;
  if (n === 1) return `${padAsciiDecimal(1, 2)} media `;
  return `${padAsciiDecimal(n, 2)} media `;
}

// In image mode, returns rawSource with media URLs stripped, or a typed count
// placeholder when stripping leaves the text empty. Outside image mode, returns
// rawSource unchanged.
function displaySourceForMedia(rawSource, mediaItems, imageMode) {
  if (!imageMode || mediaItems.length === 0) return rawSource;
  const stripped = stripMediaUrlsFromText(rawSource);
  return stripped.trim() ? stripped : mediaSummaryLabel(mediaItems);
}

/**
 * Removes image/video URLs from note text when image mode is on, since they
 * are surfaced as previews via the bottom media button instead of inline.
 * Non-media https URLs are left intact. Whitespace-only lines created by the
 * removal are dropped to avoid empty rows; in-line removals collapse adjacent
 * whitespace to a single space.
 */
function stripMediaUrlsFromText(text) {
  if (!text) return text;
  const stripped = text.replace(MEDIA_URL_PATTERN, (raw) => {
    const url = raw.replace(TRAILING_URL_PUNCTUATION, "");
    return IMAGE_EXT_PATTERN.test(url) || VIDEO_EXT_PATTERN.test(url) ? "" : raw;
  });
  return stripped
    .split("\n")
    .map((line) => line.replace(/[ \t]+/g, " ").trim())
    .filter((line, index, arr) => line !== "" || (index > 0 && arr[index - 1] !== ""))
    .join("\n")
    .trim();
}

function setImageViewerState(urls, index = 0, owner = null) {
  imageViewerState.urls = Array.isArray(urls) ? urls.filter(Boolean) : [];
  imageViewerState.ownerNoteID = noteIDForContainer(owner);
  if (!imageViewerState.urls.length) {
    imageViewerState.index = 0;
    return;
  }
  const bounded = Number.isFinite(index) ? Math.trunc(index) : 0;
  imageViewerState.index = Math.min(imageViewerState.urls.length - 1, Math.max(0, bounded));
}

function renderImageViewer(dialog) {
  if (!dialog) return;
  const img = dialog.querySelector("[data-image-viewer-img]");
  const prev = dialog.querySelector("[data-image-viewer-prev]");
  const next = dialog.querySelector("[data-image-viewer-next]");
  if (!img) return;
  if (!imageViewerState.urls.length) {
    img.removeAttribute("src");
    if (prev) {
      prev.disabled = true;
      prev.hidden = true;
    }
    if (next) {
      next.disabled = true;
      next.hidden = true;
    }
    return;
  }
  const total = imageViewerState.urls.length;
  const index = Math.min(total - 1, Math.max(0, imageViewerState.index));
  imageViewerState.index = index;
  img.src = imageViewerState.urls[index];
  const showNav = total > 1;
  if (prev) {
    prev.disabled = !showNav;
    prev.hidden = !showNav;
  }
  if (next) {
    next.disabled = !showNav;
    next.hidden = !showNav;
  }
}

function stepImageViewer(delta) {
  if (!imageViewerState.urls.length) return;
  const total = imageViewerState.urls.length;
  if (total <= 1) return;
  const next = (imageViewerState.index + delta + total) % total;
  imageViewerState.index = next;
  const dialog = document.querySelector("[data-image-viewer-dialog]");
  renderImageViewer(dialog);
}

function ensureImageViewer() {
  let dialog = document.querySelector("[data-image-viewer-dialog]");
  if (dialog) return dialog;
  dialog = document.createElement("dialog");
  dialog.className = "image-viewer-dialog";
  dialog.dataset.imageViewerDialog = "";
  dialog.innerHTML = `
    <form method="dialog" class="image-viewer-close-row">
      <button type="submit" class="image-viewer-close-button" data-close-image-viewer aria-label="Close image viewer">X</button>
    </form>
    <div class="image-viewer-body">
      <button type="button" class="image-viewer-nav image-viewer-nav-prev" data-image-viewer-prev aria-label="Previous image">&lt;</button>
      <img src="" alt="" data-image-viewer-img>
      <button type="button" class="image-viewer-nav image-viewer-nav-next" data-image-viewer-next aria-label="Next image">&gt;</button>
    </div>
  `;
  dialog.addEventListener("click", (event) => {
    if (event.target === dialog) dialog.close();
  });
  dialog.addEventListener("close", () => {
    if (!imageViewerState.ownerNoteID) return;
    const owner = document.getElementById(`note-${imageViewerState.ownerNoteID}`);
    if (!owner || !imageMount(owner)) return;
    owner.dataset.asciiMediaExpanded = "true";
    delete owner._ptxtAsciiColumns;
    renderAscii(owner);
  });
  dialog.addEventListener("keydown", (event) => {
    if (event.key === "ArrowLeft") {
      event.preventDefault();
      stepImageViewer(-1);
    } else if (event.key === "ArrowRight") {
      event.preventDefault();
      stepImageViewer(1);
    }
  });
  dialog.querySelector("[data-image-viewer-prev]")?.addEventListener("click", (event) => {
    event.preventDefault();
    stepImageViewer(-1);
  });
  dialog.querySelector("[data-image-viewer-next]")?.addEventListener("click", (event) => {
    event.preventDefault();
    stepImageViewer(1);
  });
  renderImageViewer(dialog);
  document.body.append(dialog);
  return dialog;
}

function openImageViewer(urls, index = 0, owner = null) {
  if (Array.isArray(urls)) {
    setImageViewerState(urls, index, owner);
  } else {
    setImageViewerState([urls], 0, owner);
  }
  const dialog = ensureImageViewer();
  renderImageViewer(dialog);
  if (dialog.open) return;
  if (typeof dialog.showModal === "function") {
    dialog.showModal();
  } else {
    dialog.setAttribute("open", "");
  }
}

function imagePreview(url, { onHide = null, onOpen = null } = {}) {
  const figure = document.createElement("figure");
  figure.className = "note-media-preview note-image-preview ascii-inline-media";
  const img = document.createElement("img");
  img.src = url;
  img.alt = "";
  img.loading = "lazy";
  img.decoding = "async";
  if (onOpen) {
    img.classList.add("is-clickable");
    img.addEventListener("click", () => onOpen());
  }
  if (onHide) {
    img.classList.add("is-collapsible");
    img.addEventListener("click", () => onHide());
  }
  figure.append(img);
  return figure;
}

function videoPreview(url) {
  const figure = document.createElement("figure");
  figure.className = "note-media-preview note-video-preview ascii-inline-media";
  const video = document.createElement("video");
  video.src = url;
  video.controls = true;
  video.preload = "metadata";
  prepareInlineVideo(video);
  figure.append(video);
  return figure;
}

function mediaPreview(item, options = {}) {
  if (item.type === "video") return videoPreview(item.url);
  return imagePreview(item.url, options);
}

function mediaFooterButton(container, label) {
  const button = document.createElement("button");
  const isOpen = container.dataset.asciiMediaExpanded === "true";
  button.className = "link-button note-footer-media-link";
  button.type = "button";
  button.textContent = label || "[media]";
  button.setAttribute("aria-expanded", isOpen ? "true" : "false");
  button.addEventListener("click", () => {
    container.dataset.asciiMediaExpanded = isOpen ? "false" : "true";
    renderMountedMedia(container, mediaItemsForAsciiNote(container));
    delete container._ptxtAsciiColumns;
    renderAscii(container);
  });
  return button;
}

function renderMountedMedia(container, items) {
  const mount = imageMount(container);
  if (!mount) return;
  const enabled = getImageModePref();
  const expanded = container.dataset.asciiMediaExpanded === "true";
  mount.textContent = "";
  mount.hidden = !enabled || !expanded || !items.length;
  if (mount.hidden) return;
  const imageURLs = imageItems(items).map((item) => item.url);
  const imageURLIndex = new Map(imageURLs.map((url, index) => [url, index]));
  items.forEach((item) => {
    const imageIndex = imageURLIndex.get(item.url);
    const preview = mediaPreview(item, {
      onOpen: Number.isInteger(imageIndex) ? () => openImageViewer(imageURLs, imageIndex, container) : null,
    });
    mount.append(preview);
  });
}

function appendNoteMedia(container, content, items, lineFactory) {
  if (!items.length) return;
  const imageMode = getImageModePref();
  items.forEach((item) => {
    if (imageMode && item.type !== "video") return;
    const previewRow = lineFactory({
      className: "note-image-inline-row",
      body: mediaPreview(item, {
        onHide: () => {
          previewRow.hidden = true;
        },
      }),
      hidden: true,
    });
    previewRow.dataset.asciiMediaPreviewUrl = item.url;
    content.append(previewRow, "\n");
  });
}

function measureColumns(container, pre) {
  const rect = container.getBoundingClientRect();
  if (!rect.width) return 0;
  const style = getComputedStyle(pre);
  const measure = document.createElement("span");
  measure.className = "ascii-measure";
  measure.style.font = style.font;
  measure.style.position = "absolute";
  measure.style.visibility = "hidden";
  measure.style.whiteSpace = "pre";
  document.body.append(measure);

  measure.textContent = "0000000000";
  const asciiWidth = measure.getBoundingClientRect().width / 10;
  measure.textContent = "漢漢漢漢漢";
  const cjkWidth = measure.getBoundingClientRect().width / 5;
  measure.remove();

  // Some font stacks render CJK glyphs in a single monospace cell (same as
  // ASCII), while others render them as double-width. Detect per runtime.
  useDoubleWideCells = cjkWidth >= asciiWidth * 1.5;
  if (!asciiWidth) return 0;
  return Math.max(minColumns, Math.min(maxColumns, Math.floor(rect.width / asciiWidth)));
}

/**
 * If `word` is a long https URL (including image/video URLs), split the href
 * into width-sized chunks so each row shares the same link styling and full
 * `href`. Without this, the first chunk can be autolinked as non-media (no
 * file extension yet) while continuations are plain text.
 */
function httpsUrlRowsForLongWord(word, width) {
  if (runeLength(word) <= width) return null;
  if (word.charCodeAt(0) !== 0x68 /* 'h' */) return null;
  HTTPS_URL_PATTERN.lastIndex = 0;
  const m = HTTPS_URL_PATTERN.exec(word);
  if (!m || m.index !== 0) return null;
  const raw = m[0];
  const href = raw.replace(TRAILING_URL_PUNCTUATION, "");
  const punctFromMatch = raw.slice(href.length);
  const afterMatch = word.slice(raw.length);
  if (afterMatch && !/^[,).!?;:]*$/.test(afterMatch)) return null;

  const isMedia = isMediaAssetHttpsUrl(href);
  if (isMedia && getImageModePref() && !VIDEO_EXT_PATTERN.test(href)) return null;

  const tailPlain = punctFromMatch + afterMatch;
  const chunks = [];
  let rest = href;
  while (runeLength(rest) > width) {
    chunks.push(takeColumns(rest, width));
    rest = dropColumns(rest, width);
  }
  if (rest) chunks.push(rest);
  if (!chunks.length) return null;

  const ext = (extra = {}) => ({ href, media: isMedia, ...extra });
  const specs = [];
  if (!tailPlain) {
    chunks.forEach((chunk) => specs.push({ text: chunk, ext: ext() }));
    return { specs, tailQueue: [] };
  }
  const lastChunk = chunks[chunks.length - 1];
  const headChunks = chunks.slice(0, -1);
  headChunks.forEach((chunk) => specs.push({ text: chunk, ext: ext() }));
  if (runeLength(lastChunk) + runeLength(tailPlain) <= width) {
    specs.push({
      text: lastChunk + tailPlain,
      ext: ext({ linkedPrefix: lastChunk }),
    });
    return { specs, tailQueue: [] };
  }
  if (lastChunk) specs.push({ text: lastChunk, ext: ext() });
  return { specs, tailQueue: tailPlain ? [tailPlain] : [] };
}

function wrapText(text, width) {
  const clean = text.trim();
  if (!clean) return [{ text: "", ext: null }];
  const rows = [];
  const pushRow = (t, ext = null) => {
    rows.push({ text: t, ext });
  };

  clean.split("\n").forEach((raw) => {
    const wordQueue = raw.trim().split(/\s+/).filter(Boolean);
    if (!wordQueue.length) {
      pushRow("");
      return;
    }
    let line = "";
    let lineExt = null;

    const flushLine = () => {
      if (line !== "" || lineExt) {
        pushRow(line, lineExt);
        line = "";
        lineExt = null;
      }
    };

    while (wordQueue.length) {
      let word = wordQueue.shift();
      const urlRows = httpsUrlRowsForLongWord(word, width);
      if (urlRows) {
        flushLine();
        urlRows.specs.forEach((spec) => pushRow(spec.text, spec.ext));
        urlRows.tailQueue.forEach((w) => wordQueue.unshift(w));
        continue;
      }

      if (!line) {
        while (runeLength(word) > width) {
          pushRow(takeColumns(word, width), null);
          word = dropColumns(word, width);
        }
        line = word;
        lineExt = null;
        continue;
      }
      if (runeLength(line) + 1 + runeLength(word) <= width) {
        line += ` ${word}`;
        continue;
      }
      flushLine();
      while (runeLength(word) > width) {
        pushRow(takeColumns(word, width), null);
        word = dropColumns(word, width);
      }
      line = word;
      lineExt = null;
    }
    flushLine();
  });

  return rows;
}

function hasFeedNoteAvatarSlot(container) {
  return container.dataset.asciiKind === "note" && Boolean(container.querySelector(".note-feed-avatar"));
}

function authorForWidth(container, width) {
  const reserve = hasFeedNoteAvatarSlot(container) ? feedNoteAvatarRuneReserve : 0;
  const maxAuthor = Math.max(8, width - runeLength("+-  --  [...] +") - reserve);
  return truncateMiddle(container.dataset.asciiAuthor || "", maxAuthor);
}

function boxLine(width, content = "") {
  const contentWidth = Math.max(1, width - 4);
  const clipped = truncateMiddle(content, contentWidth);
  return `| ${padRight(clipped, contentWidth)} |`;
}

function openBoxLine(width, content = "") {
  const contentWidth = Math.max(1, width - 2);
  const clipped = truncateMiddle(content, contentWidth);
  return `${padRight(clipped, contentWidth)} |`;
}

function addTrailingDots(value, width) {
  const suffix = "...";
  if (width <= runeLength(suffix)) return takeColumns(suffix, width);
  if (runeLength(value) + 1 + runeLength(suffix) <= width) {
    return `${value} ${suffix}`;
  }
  return `${takeColumns(value, width - runeLength(suffix))}${suffix}`;
}

function splitCollapsePreviewSuffix(display) {
  const m = display.match(/^([\s\S]*?)(\s*\.\.\.)$/);
  if (!m) return { linkPart: display, collapseSuffix: "" };
  return { linkPart: m[1], collapseSuffix: m[2] };
}

/** Reply feed context from server `<template class="note-reply-context-tmpl">` (under author row, inside the box). */
function readReplyContextTemplateHTML(container) {
  const tmpl = container.querySelector(":scope > template.note-reply-context-tmpl");
  if (!tmpl) return "";
  return String(tmpl.innerHTML || "").trim();
}

function appendReplyContextFeedLine(target, width, container) {
  const html = readReplyContextTemplateHTML(container);
  if (!html) return;
  // Same geometry as appendBoxedTextLine: "| " + (width-4) + " |". NBSP filler so spaces are not
  // collapsed after </a> in the inline DOM (regular spaces would glue the closing bar to the link).
  const openChrome = "| ";
  const closeChrome = " |";
  const insetRunes = hasFeedNoteAvatarSlot(container) ? feedReplyContextInsetRunes : 0;
  const innerWidth = Math.max(
    1,
    width - runeLength(openChrome) - runeLength(closeChrome) - insetRunes,
  );
  const item = document.createElement("span");
  item.className = "ascii-line ascii-line-reply-context";
  item.append(noteChrome(openChrome));
  if (insetRunes > 0) {
    item.append(document.createTextNode("\u00A0".repeat(insetRunes)));
  }
  const body = document.createElement("span");
  body.className = "ascii-note-reply-context-body";
  body.innerHTML = html;
  const used = runeLength((body.textContent || "").trim());
  const pad = "\u00A0".repeat(Math.max(0, innerWidth - used));
  item.append(body, document.createTextNode(pad), noteChrome(closeChrome));
  target.append(item, "\n");
}

/** Same horizontal inset as "Replying to" (see `feedReplyContextInsetRunes`). */
function appendViewMoreContentLine(target, width, container, vmButton) {
  const openChrome = "| ";
  const closeChrome = " |";
  const insetRunes = hasFeedNoteAvatarSlot(container) ? feedReplyContextInsetRunes : 0;
  const innerWidth = Math.max(
    1,
    width - runeLength(openChrome) - runeLength(closeChrome) - insetRunes,
  );
  const item = document.createElement("span");
  item.className = "ascii-line ascii-line-note-view-more";
  item.append(noteChrome(openChrome));
  if (insetRunes > 0) {
    item.append(document.createTextNode("\u00A0".repeat(insetRunes)));
  }
  const label = vmButton.textContent || "view more";
  const used = runeLength(label);
  const pad = "\u00A0".repeat(Math.max(0, innerWidth - used));
  item.append(vmButton, document.createTextNode(pad), noteChrome(closeChrome));
  target.append(item, "\n");
}

function appendBoxedTextLine(target, width, text, attrs = null, container = null, lineLink = null, hrefSourceLine = null) {
  const contentWidth = Math.max(1, width - 4);
  const item = document.createElement("span");
  item.className = "ascii-line";
  if (attrs) {
    Object.entries(attrs).forEach(([key, value]) => {
      if (value === undefined || value === null || value === "") return;
      item.dataset[key] = value;
    });
  }
  item.append(noteChrome("| "));
  const clipped = lineLink?.href ? text : truncateMiddle(text, contentWidth);
  const middle = document.createElement("span");
  const hrefOrigin = hrefSourceLine ?? text;
  const urlState =
    !lineLink?.href && hrefOrigin.includes("https://") &&
    (clipped !== text || (hrefSourceLine && hrefSourceLine !== text))
      ? { hrefs: listHttpsAutolinkHrefsInOrder(hrefOrigin), nextIndex: { i: 0 } }
      : null;
  const used = appendAsciiTextWithLineLink(middle, clipped, container, lineLink, urlState);
  middle.append(" ".repeat(Math.max(0, contentWidth - used)));
  item.append(middle, noteChrome(" |"));
  target.append(item, "\n");
}

/** Ensures `.thread-tree-quote` after `.thread-tree-text`; returns mount and text for layout measure. */
function threadTreeQuoteMountContext(card) {
  const textEl = card.querySelector(".thread-tree-text");
  if (!(textEl instanceof Element)) return null;
  const collapse = card.querySelector("[data-thread-tree-collapsible]");
  const host = collapse instanceof Element ? collapse : textEl.parentElement;
  if (!(host instanceof Element)) return null;
  let m = host.querySelector(":scope > .thread-tree-quote");
  if (!m) {
    m = document.createElement("div");
    m.className = "thread-tree-quote";
    textEl.insertAdjacentElement("afterend", m);
  }
  return { mount: m, textEl };
}

/** Renders quoted/reposted note body in tree rows (plain lines, no ASCII box). */
function appendThreadTreeQuoteMinimal(target, width, container, imageMode) {
  const mode = container.dataset.asciiRefMode;
  if (!mode) return;
  const referenceSource = referenceBodyDisplaySource(container, imageMode).trim();
  const tw = replyTextWidth(width);
  const refAuthor = (container.dataset.asciiRefAuthor || "").trim();
  const refAge = (container.dataset.asciiRefAge || "").trim();
  const refThreadHref = container.dataset.asciiRefThreadHref || "";
  const attribLabel = [refAuthor, refAge].filter(Boolean).join(" ").trim();
  if (attribLabel) {
    const attrib = document.createElement("div");
    attrib.className = "thread-tree-quote-attrib muted";
    const label = truncateMiddle(attribLabel, tw);
    if (refThreadHref) attrib.append(link(refThreadHref, label));
    else attrib.append(label);
    target.append(attrib);
  }
  if (!referenceSource) return;
  wrapText(referenceSource, tw).forEach((row) => {
    const line = document.createElement("span");
    line.className = "thread-tree-text-line";
    appendAsciiTextWithLineLink(line, row.text, container, row.ext, null);
    target.append(line);
  });
}

/** Hydrates tree-view quote/repost blocks (minimal typography, not feed ASCII boxes). */
export function refreshThreadTreeQuotes(root = document) {
  const scope = root instanceof Element ? root : document;
  const cards = new Set();
  if (scope instanceof Element && scope.matches("[data-thread-tree-note][data-ascii-ref-mode]")) {
    cards.add(scope);
  }
  scope.querySelectorAll("[data-thread-tree-note][data-ascii-ref-mode]").forEach((el) => cards.add(el));
  const imageModeOn = getImageModePref();
  const imageModeKey = imageModeOn ? "1" : "0";
  const mobile = mobileActionsQuery.matches ? "1" : "0";
  cards.forEach((card) => {
    const ctx = threadTreeQuoteMountContext(card);
    if (!ctx) return;
    const { mount, textEl } = ctx;
    const width = measureColumns(card, textEl);
    if (!width) {
      delete card._ptxtTreeQuoteKey;
      mount.textContent = "";
      mount.hidden = true;
      return;
    }
    const key = `${width}:${imageModeKey}:${mobile}:${card.dataset.asciiRefMode || ""}`;
    if (card._ptxtTreeQuoteKey === key) return;
    card._ptxtTreeQuoteKey = key;
    mount.textContent = "";
    appendThreadTreeQuoteMinimal(mount, width, card, imageModeOn);
    mount.hidden = mount.childNodes.length === 0;
  });
}

function appendNestedReferenceLines(target, width, container) {
  const mode = container.dataset.asciiRefMode;
  if (!mode) return;
  const innerWidth = Math.max(20, width - 8);
  const innerContentWidth = Math.max(8, innerWidth - 4);
  const refAuthor = truncateMiddle(container.dataset.asciiRefAuthor || "", Math.max(8, innerWidth - 16));
  const refAge = container.dataset.asciiRefAge || "";
  const refReplyLabel = container.dataset.asciiRefReplyLabel || "";
  const refThreadHref = container.dataset.asciiRefThreadHref || "";
  const refAttrs = refThreadHref
    ? { asciiRefSelectHref: refThreadHref, asciiRefHit: "1" }
    : null;
  const imageMode = getImageModePref();
  const referenceSource = referenceBodyDisplaySource(container, imageMode);
  const headerPrefix = `  +- ${refAuthor} -- ${refAge} `;
  const headerRule = repeat("-", Math.max(1, innerWidth - runeLength(`+- ${refAuthor} -- ${refAge} +`)));
  appendBoxedTextLine(target, width, `${headerPrefix}${headerRule}+`, refAttrs, container);
  wrapText(referenceSource, innerContentWidth).forEach((row) => {
    appendBoxedTextLine(target, width, `  | ${padRight(row.text, innerContentWidth)} |`, refAttrs, container);
  });
  const refRb = (() => {
    const up = "△";
    const down = "▽";
    const num = formatThousandsSpaced(0, 1);
    return `[${up}] ${num} [${down}]`;
  })();
  const footerSuffix = refReplyLabel ? ` ${refReplyLabel} [reply] ---+` : " [reply] ---+";
  const footerRule = repeat(
    "-",
    Math.max(1, innerWidth - runeLength(`+-- ${refRb} `) - runeLength(footerSuffix)),
  );
  appendBoxedTextLine(target, width, `  +-- ${refRb} ${footerRule}${footerSuffix}`, refAttrs, container);
}

function renderNote(container, width) {
  const pre = container.querySelector(":scope > .ascii-card");
  if (!pre) return;
  const refMode = container.dataset.asciiRefMode || "";
  const hasReference = Boolean(refMode);
  const rawSource = sourceText(container);
  const outerMediaItems = mainBodyMediaItems(container, rawSource);
  const mediaItems = mediaItemsForAsciiNote(container, outerMediaItems);
  const imageMode = getImageModePref();
  const hasMedia = imageMode && mediaItems.length > 0;
  const author = authorForWidth(container, width);
  const age = container.dataset.asciiAge || "";
  const replyCount = Number.parseInt(container.dataset.asciiReplyCount || "0", 10);
  const replyLabelDataset = container.dataset.asciiReplyLabel || replyLabelForCount(replyCount);
  const contentWidth = Math.max(1, width - 4);
  const noteSource = displaySourceForMedia(rawSource, outerMediaItems, imageMode);
  const allRows = refMode === "repost" ? [] : wrapText(noteSource, contentWidth);
  const isLong = !hasReference && allRows.length > collapsedNoteLines;
  const isExpanded = container.dataset.asciiExpanded === "true";
  const collapsing = isLong && !isExpanded;
  const viewMoreInBody = collapsing && mobileActionsQuery.matches;
  const congestedMobileFooter = mobileActionsQuery.matches && collapsing && hasMedia;
  const mediaLabel = mediaSummaryLabel(mediaItems, congestedMobileFooter);
  const replyLabel =
    congestedMobileFooter && replyCount > 0 ? compactReplyBadge(replyCount) : replyLabelDataset;
  const visibleRows = collapsing ? allRows.slice(0, collapsedNoteLines) : allRows;
  let collapseHrefSource = null;
  if (collapsing) {
    const li = visibleRows.length - 1;
    const last = visibleRows[li];
    collapseHrefSource = last.text;
    visibleRows[li] = { text: addTrailingDots(last.text, contentWidth), ext: last.ext };
  }
  const hasFeedAvatar = hasFeedNoteAvatarSlot(container);
  const topPrefix = hasFeedAvatar ? `+--${author} -- ${age} ` : `+- ${author} -- ${age} `;
  const topSuffix = "[...]+";
  const feedAvatarReserve = hasFeedAvatar ? feedNoteAvatarRuneReserve : 0;
  const headerDashCount = Math.max(1, width - runeLength(topPrefix + topSuffix) - feedAvatarReserve);
  const savedFeedAvatar = takeFeedAvatarForPreRebuild(pre);
  const savedMediaMount = takeImageMountForPreRebuild(container);
  pre.textContent = "";
  if (savedFeedAvatar) pre.prepend(savedFeedAvatar);
  if (hasFeedAvatar) {
    const headerLine = document.createElement("span");
    headerLine.className = "ascii-line ascii-line-feed-header";
    headerLine.append(noteChrome("+--"));
    const tail = document.createElement("span");
    tail.className = "ascii-line-feed-header-tail";
    tail.append(
      link(container.dataset.asciiUserHref || "#", author),
      noteChrome(` -- ${age} ${repeat("-", headerDashCount)}`),
      noteMenu(container),
      noteChrome("+"),
    );
    headerLine.append(tail);
    pre.append(headerLine, "\n");
  } else {
    appendLine(pre, [
      noteChrome("+- "),
      link(container.dataset.asciiUserHref || "#", author),
      noteChrome(` -- ${age} ${repeat("-", headerDashCount)}`),
      noteMenu(container),
      noteChrome("+"),
    ]);
  }
  // Empty top `| … |` row would sit above "Replying to"; skip it when that row is present.
  const hasReplyContext = Boolean(readReplyContextTemplateHTML(container));
  if (!hasReplyContext) {
    appendLine(pre, [noteChrome(boxLine(width))]);
  }
  const content = document.createElement("span");
  content.className = "note-content ascii-note-content";
  appendReplyContextFeedLine(content, width, container);
  if (visibleRows.length > 0) {
    appendBoxedTextLine(content, width, "", null, container);
  }
  visibleRows.forEach((row, index) => {
    const hrefSource =
      collapseHrefSource != null && index === visibleRows.length - 1
        ? collapseHrefSource
        : null;
    appendBoxedTextLine(content, width, row.text, null, container, row.ext, hrefSource);
  });
  if (visibleRows.length > 0) {
    appendBoxedTextLine(content, width, "", null, container);
  }
  if (viewMoreInBody) {
    appendViewMoreContentLine(content, width, container, viewMoreButton(container, width));
  }
  if (hasReference) {
    appendNestedReferenceLines(content, width, container);
  }
  appendNoteMedia(container, content, mediaItems, ({ className = "", body, hidden = false }) => {
    const item = document.createElement("span");
    item.className = `ascii-line note-image-boxed-row ${className}`.trim();
    item.hidden = hidden;
    item.style.setProperty("--ascii-box-row-width", `${width}ch`);
    item.append(noteChrome("| "), body, noteChrome(" |"));
    return item;
  });
  pre.append(content);
  if (savedMediaMount) pre.append(savedMediaMount);
  appendLine(pre, [noteChrome(boxLine(width))]);
  const threadHref = container.dataset.asciiThreadHref || "#";
  const reactionSeg = reactionLayoutSegments(container);
  const collapseRunes =
    collapsing && !viewMoreInBody ? runeLength(" --- ") + runeLength("view more") : 0;
  const leftFixedRunes =
    runeLength("+-- ") + reactionSeg.runeBlockLen + collapseRunes + 1;
  const tailAfterDashes = (withReplyLabel) => {
    const close = runeLength(" ---+");
    const bracket = runeLength("[reply]");
    if (withReplyLabel) {
      return 1 + runeLength(replyLabel) + 1 + bracket + close;
    }
    return 1 + bracket + close;
  };
  const footerParts = [noteChrome("+-- ")];
  footerParts.push(...reactionSeg.footerParts);
  if (collapsing && !viewMoreInBody) {
    footerParts.push(noteChrome(" --- "), viewMoreButton(container, width));
  }
  footerParts.push(noteChrome(" "));
  if (hasMedia && replyLabel) {
    const mid = ` ${mediaLabel} `;
    const remaining = Math.max(
      2,
      width - leftFixedRunes - runeLength(mid) - tailAfterDashes(true),
    );
    const firstRule = Math.max(1, Math.floor(remaining / 2));
    const secondRule = Math.max(1, remaining - firstRule);
    footerParts.push(
      noteChrome(` ${repeat("-", firstRule)} `),
      mediaFooterButton(container, mediaLabel),
      noteChrome(` ${repeat("-", secondRule)} `),
      link(threadHref, replyLabel),
      noteChrome(" "),
      ...bracketedReplyLink(container, threadHref),
      noteChrome(" ---+"),
    );
  } else if (hasMedia) {
    const mid = ` ${mediaLabel} `;
    const remaining = Math.max(1, width - leftFixedRunes - runeLength(mid) - tailAfterDashes(false));
    footerParts.push(
      noteChrome(` ${repeat("-", remaining)} `),
      mediaFooterButton(container, mediaLabel),
      noteChrome(" "),
      ...bracketedReplyLink(container, threadHref),
      noteChrome(" ---+"),
    );
  } else {
    const dashCount = Math.max(1, width - leftFixedRunes - tailAfterDashes(Boolean(replyLabel)));
    footerParts.push(noteChrome(`${repeat("-", dashCount)}`));
    if (replyLabel) {
      footerParts.push(noteChrome(" "), link(threadHref, replyLabel));
    }
    footerParts.push(noteChrome(" "), ...bracketedReplyLink(container, threadHref), noteChrome(" ---+"));
  }
  appendLine(pre, footerParts);
  renderMountedMedia(container, mediaItems);
}

function appendReplyContentPadLine(content, contentPrefix, padSpaces) {
  const row = document.createElement("span");
  row.className = "ascii-line";
  row.append(noteChrome(contentPrefix), padSpaces);
  content.append(row, "\n");
}

function renderReply(container, width) {
  const pre = container.querySelector(":scope > .ascii-reply");
  if (!pre) return;
  const author = authorForWidth(container, width);
  const age = container.dataset.asciiAge || "";
  const isLast = container.dataset.asciiIsLast === "true";
  const hasChildren = container.dataset.asciiHasChildren === "true";
  const replyCount = Number.parseInt(container.dataset.asciiReplyCount || "0", 10);
  const replyLabelDataset = container.dataset.asciiReplyLabel || replyLabelForCount(replyCount);
  let replyLabel = replyLabelDataset;
  // Depth-based indentation is applied via CSS margin-left on .comment so
  // the rail glyph (`|`) at pre col 0 lines up with the rail drawn by the
  // parent. The avatar is absolutely positioned over that same rail
  // column via CSS, so the rail glyphs above and below the header row
  // visually flow into the avatar like a single vertical thread.
  // Show the rail in this node's content lines whenever the node has
  // children (the rail descends into them) OR is not the last sibling
  // (the rail continues to the next sibling). Only a leaf that is also
  // the last sibling drops the rail — that's the bottom of the branch.
  const showRail = hasChildren || !isLast;
  const linePrefix = "|";
  const contentPrefix = showRail ? "|    " : "     ";
  // The header line is shifted right via CSS padding-left to leave room
  // for the absolutely-positioned avatar. Reserve enough header width so
  // the right-aligned overflow menu stays inside the visible box.
  const headerAvatarReserve = 7;
  // Header: `{author} -- {age}` then spaces, then right-aligned `[...]`.
  const headerWidth = Math.max(20, width - headerAvatarReserve);
  const maxReplyAuthor = Math.max(
    8,
    headerWidth - runeLength(` -- ${age}`) - runeLength("[...]") - 1,
  );
  const visibleAuthor = truncateMiddle(author, maxReplyAuthor);
  const leftText = `${visibleAuthor} -- ${age}`;
  pre.textContent = "";
  const headerParts = [
    link(container.dataset.asciiUserHref || "#", visibleAuthor),
    ` -- ${age}`,
  ];
  const pad = " ".repeat(Math.max(1, headerWidth - runeLength(leftText) - runeLength("[...]")));
  headerParts.push(pad, noteMenu(container));
  appendLine(pre, headerParts);
  const subtree = document.createElement("span");
  subtree.className = "thread-reply-collapse";
  const content = document.createElement("span");
  content.className = "reply-content";
  const replyRawSource = sourceText(container);
  const replyMediaItems = mainBodyMediaItems(container, replyRawSource);
  const imageMode = getImageModePref();
  const hasMedia = imageMode && replyMediaItems.length > 0;
  const footerCompact = mobileActionsQuery.matches && hasMedia && replyCount > 0;
  const mediaLabel = mediaSummaryLabel(replyMediaItems, footerCompact);
  if (footerCompact) {
    replyLabel = compactReplyBadge(replyCount);
  }
  const replySource = displaySourceForMedia(replyRawSource, replyMediaItems, imageMode);
  const tw = replyTextWidth(width);
  const replyRows = wrapText(replySource, tw);
  const replyPadSpaces = replyRows.length > 0 ? " ".repeat(tw) : "";
  if (replyPadSpaces) {
    appendReplyContentPadLine(content, contentPrefix, replyPadSpaces);
  }
  replyRows.forEach((row) => {
    const item = document.createElement("span");
    item.className = "ascii-line";
    item.append(noteChrome(contentPrefix));
    appendAsciiTextWithLineLink(item, row.text, container, row.ext, null);
    content.append(item, "\n");
  });
  if (replyPadSpaces) {
    appendReplyContentPadLine(content, contentPrefix, replyPadSpaces);
  }
  appendNoteMedia(container, content, replyMediaItems, ({ className = "", body, hidden = false }) => {
    const item = document.createElement("span");
    item.className = `ascii-line ${className}`.trim();
    item.hidden = hidden;
    item.append(contentPrefix, body);
    return item;
  });
  subtree.append(content);
  const selectHref = container.dataset.asciiSelectHref || "#";
  const reactionSeg = reactionLayoutSegments(container);
  const leftFixedRunes = runeLength(contentPrefix) + 1 + reactionSeg.runeBlockLen + 1;
  const tailAfterDashesSel = (withReplyLabel) => {
    const close = runeLength(" ---+");
    const bracket = runeLength("[reply]");
    if (withReplyLabel) {
      return 1 + runeLength(replyLabel) + 1 + bracket + close;
    }
    return 1 + bracket + close;
  };
  const footerParts = [contentPrefix, noteChrome(" "), ...reactionSeg.footerParts, noteChrome(" ")];
  if (hasMedia && replyLabel) {
    const mid = ` ${mediaLabel} `;
    const remaining = Math.max(
      2,
      width - leftFixedRunes - runeLength(mid) - tailAfterDashesSel(true),
    );
    const firstRule = Math.max(1, Math.floor(remaining / 2));
    const secondRule = Math.max(1, remaining - firstRule);
    footerParts.push(
      noteChrome(` ${repeat("-", firstRule)} `),
      mediaFooterButton(container, mediaLabel),
      noteChrome(` ${repeat("-", secondRule)} `),
      link(selectHref, replyLabel),
      noteChrome(" "),
      ...bracketedReplyLink(container, selectHref),
      noteChrome(" ---+"),
    );
  } else if (hasMedia) {
    const mid = ` ${mediaLabel} `;
    const remaining = Math.max(1, width - leftFixedRunes - runeLength(mid) - tailAfterDashesSel(false));
    footerParts.push(
      noteChrome(` ${repeat("-", remaining)} `),
      mediaFooterButton(container, mediaLabel),
      noteChrome(" "),
      ...bracketedReplyLink(container, selectHref),
      noteChrome(" ---+"),
    );
  } else if (replyLabel) {
    const dashCount = Math.max(1, width - leftFixedRunes - tailAfterDashesSel(true));
    footerParts.push(
      noteChrome(`${repeat("-", dashCount)}`),
      noteChrome(" "),
      link(selectHref, replyLabel),
      noteChrome(" "),
      ...bracketedReplyLink(container, selectHref),
      noteChrome(" ---+"),
    );
  } else {
    const dashCount = Math.max(1, width - leftFixedRunes - tailAfterDashesSel(false));
    footerParts.push(
      noteChrome(`${repeat("-", dashCount)}`),
      noteChrome(" "),
      ...bracketedReplyLink(container, selectHref),
      noteChrome(" ---+"),
    );
  }
  appendLine(subtree, footerParts);
  /* Trailing `|` connects into nested replies, or to the next sibling when
     this row is not the last child. */
  if (showRail) {
    appendLine(subtree, [linePrefix]);
  }
  pre.append(subtree);
  renderMountedMedia(container, replyMediaItems);
  renderContinueLinks(container, width, linePrefix);
}

function renderSelected(container, width) {
  // Render the focused selected note as a three-sided box: top border,
  // right edge, and bottom border, but no left edge so it still feels
  // visually attached to the thread column/avatar.
  const pre = container.querySelector(":scope > .ascii-reply");
  if (!pre) return;
  const rawSource = sourceText(container);
  const mediaItems = mainBodyMediaItems(container, rawSource);
  const imageMode = getImageModePref();
  const hasMedia = imageMode && mediaItems.length > 0;
  const replyCount = parseInt(container.dataset.asciiReplyCount || "0", 10) || 0;
  const replyLabelDataset = container.dataset.asciiReplyLabel || replyLabelForCount(replyCount);
  let replyLabel = replyLabelDataset;
  const footerCompactSel = mobileActionsQuery.matches && hasMedia && replyCount > 0;
  const mediaLabel = mediaSummaryLabel(mediaItems, footerCompactSel);
  if (footerCompactSel) {
    replyLabel = compactReplyBadge(replyCount);
  }
  const selectedSource = displaySourceForMedia(rawSource, mediaItems, imageMode);
  const author = authorForWidth(container, width);
  const age = container.dataset.asciiAge || "";
  // Reserve enough header columns for the avatar's CSS padding-left so
  // the right-aligned `[...]` overflow menu stays inside the visible box.
  const headerAvatarReserve = 6;
  const headerWidth = Math.max(20, width - headerAvatarReserve);
  const maxAuthor = Math.max(
    8,
    headerWidth - runeLength(` -- ${age}`) - runeLength("[...]") - 1,
  );
  const visibleAuthor = truncateMiddle(author, maxAuthor);
  const topPrefix = `${visibleAuthor} -- ${age} `;
  const topSuffix = "[...]+";
  // One extra dash so the closing `+` lines up with the body/footer `|` column (same total width as content rows).
  const topRule = repeat("-", Math.max(1, headerWidth - runeLength(topPrefix + topSuffix) + 1));
  pre.textContent = "";
  appendLine(pre, [
    link(container.dataset.asciiUserHref || "#", visibleAuthor),
    ` -- ${age} `,
    topRule,
    noteMenu(container),
    "+",
  ]);
  const content = document.createElement("span");
  content.className = "note-content";
  const contentWidth = Math.max(1, width - 2);
  const selectedPadSpaces = " ".repeat(contentWidth);
  const appendSelectedPadLine = () => {
    const item = document.createElement("span");
    item.className = "ascii-line";
    const middle = document.createElement("span");
    middle.append(selectedPadSpaces);
    item.append(middle, noteChrome(" |"));
    content.append(item, "\n");
  };
  const selectedRows = wrapText(selectedSource, replyTextWidth(width));
  if (selectedRows.length > 0) {
    appendSelectedPadLine();
  }
  selectedRows.forEach((row) => {
    const item = document.createElement("span");
    item.className = "ascii-line";
    const { text: line, ext } = row;
    const clipped = ext?.href ? line : truncateMiddle(line, contentWidth);
    const middle = document.createElement("span");
    const urlState =
      !ext?.href && clipped !== line && line.includes("https://")
        ? { hrefs: listHttpsAutolinkHrefsInOrder(line), nextIndex: { i: 0 } }
        : null;
    const used = appendAsciiTextWithLineLink(middle, clipped, container, ext, urlState);
    middle.append(" ".repeat(Math.max(0, contentWidth - used)));
    item.append(middle, noteChrome(" |"));
    content.append(item, "\n");
  });
  if (selectedRows.length > 0) {
    appendSelectedPadLine();
  }
  appendNoteMedia(container, content, mediaItems, ({ className = "", body, hidden = false }) => {
    const item = document.createElement("span");
    item.className = `ascii-line ${className}`.trim();
    item.hidden = hidden;
    item.append(body, noteChrome(" |"));
    return item;
  });
  pre.append(content);
  const threadHrefSel = container.dataset.asciiThreadHref || "#";
  const reactionSegSel = reactionLayoutSegments(container);
  const leftFixedSel = 1 + reactionSegSel.runeBlockLen + 1;
  const tailSel = (withReplyLabel) => {
    const close = runeLength(" ---+");
    const bracket = runeLength("[reply]");
    if (withReplyLabel) {
      return 1 + runeLength(replyLabel) + 1 + bracket + close;
    }
    return 1 + bracket + close;
  };
  const replyParts = [noteChrome(" "), ...reactionSegSel.footerParts, noteChrome(" ")];
  if (hasMedia && replyLabel) {
    const mid = ` ${mediaLabel} `;
    const remaining = Math.max(2, width - leftFixedSel - runeLength(mid) - tailSel(true));
    const firstRule = Math.max(1, Math.floor(remaining / 2));
    const secondRule = Math.max(1, remaining - firstRule);
    replyParts.push(
      noteChrome(` ${repeat("-", firstRule)} `),
      mediaFooterButton(container, mediaLabel),
      noteChrome(` ${repeat("-", secondRule)} `),
      link(threadHrefSel, replyLabel),
      noteChrome(" "),
      ...bracketedReplyLink(container, threadHrefSel),
      noteChrome(" ---+"),
    );
  } else if (hasMedia) {
    const mid = ` ${mediaLabel} `;
    const remaining = Math.max(1, width - leftFixedSel - runeLength(mid) - tailSel(false));
    replyParts.push(
      noteChrome(` ${repeat("-", remaining)} `),
      mediaFooterButton(container, mediaLabel),
      noteChrome(" "),
      ...bracketedReplyLink(container, threadHrefSel),
      noteChrome(" ---+"),
    );
  } else if (replyLabel) {
    const dashCount = Math.max(1, width - leftFixedSel - tailSel(true));
    replyParts.push(
      noteChrome(`${repeat("-", dashCount)}`),
      noteChrome(" "),
      link(threadHrefSel, replyLabel),
      noteChrome(" "),
      ...bracketedReplyLink(container, threadHrefSel),
      noteChrome(" ---+"),
    );
  } else {
    const dashCount = Math.max(1, width - leftFixedSel - tailSel(false));
    replyParts.push(
      noteChrome(`${repeat("-", dashCount)}`),
      noteChrome(" "),
      ...bracketedReplyLink(container, threadHrefSel),
      noteChrome(" ---+"),
    );
  }
  appendLine(pre, replyParts);
  renderMountedMedia(container, mediaItems);
}

function renderContinueLinks(container, width, linePrefix) {
  container.querySelectorAll(":scope > .continue-thread").forEach((item) => {
    const href = item.querySelector("a")?.href || "#";
    const label = "continue thread";
    const spaces = " ".repeat(Math.max(1, width - runeLength(linePrefix + label)));
    item.textContent = "";
    item.append(linePrefix, spaces, link(href, label));
  });
}

function renderAscii(container) {
  const pre = container.querySelector(":scope > .ascii-card, :scope > .ascii-reply");
  if (!pre) return;
  const width = measureColumns(container, pre);
  const cacheKey = `${width}:${mobileActionsQuery.matches}:${getImageModePref() ? "1" : "0"}`;
  if (!width || container._ptxtAsciiColumns === cacheKey) return;
  container._ptxtAsciiColumns = cacheKey;
  if (container.dataset.asciiKind === "note") {
    renderNote(container, width);
  } else if (container.dataset.asciiKind === "reply") {
    renderReply(container, width);
  } else if (container.dataset.asciiKind === "selected") {
    renderSelected(container, width);
  }
  syncMuteToggleButtons(container);
}

function observeAscii(container) {
  if (observed.has(container)) return;
  observed.add(container);
  resizeObserver?.observe(container);
  renderAscii(container);
}

function queryFeedLoaders(root = document) {
  if (root === document) {
    return [...document.querySelectorAll("[data-feed-loader]")];
  }
  if (!(root instanceof Element)) return [];
  const loaders = root.matches("[data-feed-loader]") ? [root] : [];
  loaders.push(...root.querySelectorAll("[data-feed-loader]"));
  return loaders;
}

function renderFeedLoaders(root = document) {
  const loaders = queryFeedLoaders(root);
  loaders.forEach((loader) => {
    const statusNode = loader.querySelector("[data-feed-loader-status]");
    if (statusNode) {
      statusNode.textContent = FEED_LOADER_STATUSES[Math.floor(feedLoaderTick / 2) % FEED_LOADER_STATUSES.length];
    }
    loader.querySelectorAll("[data-feed-loader-card]").forEach((card, index) => {
      const cardIdx = Number.parseInt(card.dataset.feedLoaderCard || `${index}`, 10);
      const width = measureColumns(loader, card) || minColumns;
      card.textContent = buildFeedLoaderCardText(width, cardIdx, feedLoaderTick % FEED_LOADER_FRAME_VARIANTS);
    });
  });
  return loaders.length;
}

function querySkeletonWaveCards(root = document) {
  if (root === document) {
    return [...document.querySelectorAll("[data-skeleton-wave-card]")];
  }
  if (!(root instanceof Element)) return [];
  const cards = root.matches("[data-skeleton-wave-card]") ? [root] : [];
  cards.push(...root.querySelectorAll("[data-skeleton-wave-card]"));
  return cards;
}

function renderSkeletonWaveCards(root = document) {
  const cards = querySkeletonWaveCards(root);
  cards.forEach((card, index) => {
    const cardIdx = Number.parseInt(card.dataset.skeletonWaveCard || `${index}`, 10);
    const scope = feedLoaderMeasureRoot(card);
    const width = measureColumns(scope, card) || minColumns;
    card.textContent = buildFeedLoaderCardText(width, cardIdx, feedLoaderTick % FEED_LOADER_FRAME_VARIANTS);
  });
  return cards.length;
}

function skeletonAnimationTargetsRemain() {
  return queryFeedLoaders(document).length > 0 || document.querySelector("[data-skeleton-wave-card]") !== null;
}

function startFeedLoaderAnimation() {
  if (feedLoaderTimer) return;
  feedLoaderTimer = window.setInterval(() => {
    feedLoaderTick += 1;
    renderFeedLoaders(document);
    renderSkeletonWaveCards(document);
    if (skeletonAnimationTargetsRemain()) return;
    window.clearInterval(feedLoaderTimer);
    feedLoaderTimer = 0;
  }, 900);
}

function initFeedLoaders(root = document) {
  registerLoaderLayoutObservers(root);
  let updated = 0;
  if (root === document) {
    updated += renderFeedLoaders(document);
    updated += renderSkeletonWaveCards(document);
  } else {
    updated += renderFeedLoaders(root);
    updated += renderSkeletonWaveCards(root);
  }
  if (updated === 0) return;
  startFeedLoaderAnimation();
}

/** Note shells carry `[data-ascii-kind]` on the element itself, not only on descendants. */
function asciiKindRoots(root) {
  if (root === document) {
    return [...document.querySelectorAll("[data-ascii-kind]")];
  }
  if (!(root instanceof Element)) {
    return [];
  }
  const out = [];
  if (root.matches("[data-ascii-kind]")) {
    out.push(root);
  }
  out.push(...root.querySelectorAll("[data-ascii-kind]"));
  return out;
}

function initAscii(root = document) {
  ensureNoteReactionsDelegated();
  asciiKindRoots(root).forEach(observeAscii);
}

export function refreshAscii(root = document) {
  asciiKindRoots(root).forEach((container) => {
    delete container._ptxtAsciiColumns;
    renderAscii(container);
  });
  refreshThreadTreeQuotes(root);
}

const queuedAsciiRoots = new Set();
let asciiInitScheduled = false;

function scheduleAsciiInit(root) {
  if (!(root instanceof Element) && root !== document) return;
  queuedAsciiRoots.add(root);
  if (asciiInitScheduled) return;
  asciiInitScheduled = true;
  requestAnimationFrame(() => {
    asciiInitScheduled = false;
    queuedAsciiRoots.forEach((item) => initAscii(item));
    queuedAsciiRoots.clear();
  });
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", () => {
    document.documentElement.classList.add("ascii-enhanced");
    initAscii();
    initFeedLoaders();
  });
} else {
  document.documentElement.classList.add("ascii-enhanced");
  initAscii();
  initFeedLoaders();
}

const asciiMutationRoot = document.querySelector("[data-nav-root]") || document.documentElement;

new MutationObserver((mutations) => {
  mutations.forEach((mutation) => {
    mutation.addedNodes.forEach((node) => {
      if (!(node instanceof Element)) return;
      if (node.matches("[data-ascii-kind]")) observeAscii(node);
      scheduleAsciiInit(node);
      initFeedLoaders(node);
    });
  });
}).observe(asciiMutationRoot, { childList: true, subtree: true });

document.addEventListener("click", () => closeActionMenus());

function rerenderAllAscii() {
  asciiKindRoots(document).forEach((container) => {
    delete container._ptxtAsciiColumns;
    renderAscii(container);
  });
  refreshThreadTreeQuotes(document);
}

mobileActionsQuery.addEventListener("change", () => {
  rerenderAllAscii();
  renderFeedLoaders(document);
  renderSkeletonWaveCards(document);
});

window.addEventListener("ptxt:image-mode-changed", () => {
  rerenderAllAscii();
});
