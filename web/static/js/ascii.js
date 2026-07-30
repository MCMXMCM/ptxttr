import { fetchWithSession, normalizePubkey } from "./session.js";
import { compactReplyBadge, padAsciiDecimal, replyLabelForCount } from "./reply-label.js";
import { getImageModePref } from "./sort-prefs.js";
import { NOSTR_REF_PATTERN, nostrRefLink } from "./nip27.js";
import { FEED_LOADER_STATUSES } from "./shell.js";
import { prepareInlineVideo } from "./inline-video.js";
import { createMediaGrid, hydrateMediaGrid, mediaGridSignature } from "./media-grid.js";
import { pollDescriptorForContainer, pollDraftSelection, bindPollDelegates, PollType } from "./poll.js";
import { bindBroadcastDelegates } from "./broadcast.js";
import { hydrateVisibleZapTotals } from "./zap-display.js";
import { formatCompactSats } from "./zap-utils.js";
import {
  buildThreadParentSkeletonText,
  buildThreadReplySkeletonText,
  buildThreadSelectedSkeletonText,
} from "./thread-skeleton-text.js";
import {
  buildAsciiRenderCacheKey,
  columnsFromWidth,
  shouldRenderAscii,
  ASCII_MAX_COLUMNS,
  ASCII_MIN_COLUMNS,
} from "./ascii-layout.js";
import {
  displayWidth,
  graphemes,
  isWideGrapheme,
  measureGlyphMetrics as measureLayoutGlyphMetrics,
  padRight,
  takeColumns,
} from "./ascii-layout-engine.js";
import { asciiWidthCookie, asciiWidthCookieNameForViewport } from "./ascii-width-hint.js";
import {
  isReferenceExpanded,
  markReferenceExpanded,
} from "./ascii-reference-expansion.js";

const runeLength = displayWidth;

function formatThousandsSpaced(n, minLen) {
	let value = String(Math.max(0, Math.floor(Number(n) || 0)));
	const groups = [];
	while (value.length > 3) {
		groups.unshift(value.slice(-3));
		value = value.slice(0, -3);
	}
	if (value) groups.unshift(value);
	let output = groups.join(" ");
	while (output.length < minLen) output = ` ${output}`;
	return output;
}

function localViewerPubkey() {
	try {
		return String(JSON.parse(localStorage.getItem("ptxt_nostr_session") || "{}").pubkey || "").toLowerCase();
	} catch {
		return "";
	}
}

function canDeleteNoteLight(container) {
	const viewer = localViewerPubkey();
	const author = String(container?.dataset?.replyPubkey || "").toLowerCase();
	const kind = Number.parseInt(String(container?.dataset?.asciiEventKind || "1"), 10);
	return Boolean(viewer && author && viewer === author && (kind === 1 || kind === 6 || kind === 30023));
}

// Link #words to /tag/… (Unicode letters, numbers, underscore). The server
// applies stricter path rules when resolving the feed URL.
const HASHTAG_PATTERN_STEM = "(?:^|[\\s])#([\\p{L}\\p{N}_]+)";
const HASHTAG_PATTERN = new RegExp(HASHTAG_PATTERN_STEM, "gu");
const NOSTR_REF_DETECT_PATTERN = new RegExp(NOSTR_REF_PATTERN.source, "iu");

const minColumns = ASCII_MIN_COLUMNS;
const maxColumns = ASCII_MAX_COLUMNS;
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
const DISPLAY_BLOSSOM_URL_PATTERN = /https?:\/\/@[^\s<>"'`]+(?:\s+[^\s<>"'`/]+)*\.blossom\.band\/[^\s<>"'`]+/gi;
const NOSTR_OR_HASHTAG_PATTERN = new RegExp(`${HTTPS_URL_PATTERN.source}|${NOSTR_REF_PATTERN.source}|${HASHTAG_PATTERN_STEM}`, "giu");
const observed = new WeakSet();
const browserWindow = typeof window !== "undefined" ? window : null;
const mobileActionsQuery = typeof browserWindow?.matchMedia === "function"
  ? browserWindow.matchMedia("(max-width: 700px)")
  : { matches: false, addEventListener() {}, removeEventListener() {} };
const resizeObserver = typeof ResizeObserver === "function" ? new ResizeObserver((entries) => {
  entries.forEach((entry) => {
    queueAsciiResize(entry.target, entry.contentRect?.width || 0);
  });
}) : null;
const imageViewerState = {
  items: [],
  index: 0,
  ownerNoteID: "",
};
const asciiMeasuredWidthHints = new WeakMap();
const queuedAsciiResizeTargets = new Set();
let asciiResizeScheduled = false;
let feedLoaderTick = 0;
let feedLoaderTimer = 0;
const loaderLayoutObservedColumns = new WeakSet();
let feedLoaderColumnObserver = null;
let persistedAsciiWidth = 0;

function persistAsciiWidth(container, columns) {
  if (columns === persistedAsciiWidth) return;
  if (!(container instanceof Element)) return;
  // Nested replies can be narrower than the route column. Persist only a
  // top-level card measurement because that is the width the server renders.
  if (container.parentElement?.closest?.("[data-ascii-kind]")) return;
  const value = asciiWidthCookie(
    columns,
    window.location.protocol === "https:",
    asciiWidthCookieNameForViewport(window.innerWidth),
  );
  if (!value) return;
  document.cookie = value;
  persistedAsciiWidth = columns;
}

const asciiPerf = (() => {
  const state = {
    measureCalls: 0,
    measureMs: 0,
    renderCalls: 0,
    renderMs: 0,
    renderBodyMs: 0,
    skippedRenderCalls: 0,
    renderedCards: 0,
    resizeBatches: 0,
    resizeBatchItems: 0,
    lastResizeBatchSize: 0,
  };
  return {
    state,
    reset() {
      Object.keys(state).forEach((key) => {
        state[key] = 0;
      });
    },
    snapshot() {
      return { ...state };
    },
  };
})();

if (browserWindow) browserWindow.__ptxtAsciiPerf = asciiPerf;

function observeFeedLoaderColumn(column) {
  if (!column || !(column instanceof Element)) return;
  if (loaderLayoutObservedColumns.has(column)) return;
  loaderLayoutObservedColumns.add(column);
  if (!("ResizeObserver" in window)) return;
  if (!feedLoaderColumnObserver) {
    feedLoaderColumnObserver = new ResizeObserver(() => {
      renderFeedLoaders(document);
      renderSkeletonWaveCards(document);
      renderThreadSkeletonCards(document);
    });
  }
  feedLoaderColumnObserver.observe(column);
}

function queueAsciiResize(container, widthHint = 0) {
  if (!(container instanceof Element)) return;
  if (Number.isFinite(widthHint) && widthHint > 0) {
    asciiMeasuredWidthHints.set(container, widthHint);
  }
  queuedAsciiResizeTargets.add(container);
  if (asciiResizeScheduled) return;
  asciiResizeScheduled = true;
  requestAnimationFrame(() => {
    asciiResizeScheduled = false;
    const targets = [...queuedAsciiResizeTargets];
    queuedAsciiResizeTargets.clear();
    asciiPerf.state.resizeBatches += 1;
    asciiPerf.state.resizeBatchItems += targets.length;
    asciiPerf.state.lastResizeBatchSize = targets.length;
    targets.forEach((target) => {
      renderAscii(target, {
        widthHint: asciiMeasuredWidthHints.get(target) || 0,
      });
      asciiMeasuredWidthHints.delete(target);
    });
  });
}

function registerLoaderLayoutObservers(root = document) {
  queryFeedLoaders(root).forEach((loader) => {
    observeFeedLoaderColumn(loader.closest(".feed-column") || loader);
  });
  querySkeletonWaveCards(root).forEach((card) => {
    observeFeedLoaderColumn(card.closest(".feed-column") || card.parentElement);
  });
  queryThreadSkeletonCards(root).forEach((card) => {
    observeFeedLoaderColumn(card.closest(".feed-column") || card.parentElement);
  });
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

function appendLine(pre, parts = []) {
  const line = document.createElement("span");
  line.className = "ascii-line";
  parts.forEach((part) => line.append(part));
  pre.append(line, "\n");
}

function noteChrome(value) {
  const item = document.createElement("span");
  item.className = "note-chrome";
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

function blossomPathSuffix(url) {
  if (!url) return "";
  const cleaned = String(url).trim().replace(TRAILING_URL_PUNCTUATION, "");
  const match = /\.blossom\.band\/([^\s<>"'`]+)/i.exec(cleaned);
  return match ? match[1] : "";
}

function canonicalMediaHrefForDisplay(href, container) {
  const suffix = blossomPathSuffix(href);
  if (!suffix || !container) return href;
  const mediaItems = extractImetaMediaFromNoteContainer(container);
  const match = mediaItems.find((item) => blossomPathSuffix(item.url) === suffix);
  return match?.url || href;
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
  const resolvedHref = canonicalMediaHrefForDisplay(href, container);
  if (
    container &&
    (isVideoAssetHttpsUrl(resolvedHref) || (!getImageModePref() && isMediaAssetHttpsUrl(resolvedHref)))
  ) {
    target.append(mediaNoteInlineLink(resolvedHref, label, container));
    return;
  }
  const a = externalHttpsLink(resolvedHref, label);
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
      `${HTTPS_URL_PATTERN.source}|(${escaped.join("|")})|${NOSTR_REF_PATTERN.source}|${HASHTAG_PATTERN_STEM}`,
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
  const hasNostr = !ctx && NOSTR_REF_DETECT_PATTERN.test(text);
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
    if (/^https:\/\//i.test(token)) {
      used += appendHttpsUrls(target, token, urlState, container);
      cursor = start + token.length;
      continue;
    } else if (ctx && ctx.map.has(token)) {
      const info = ctx.map.get(token);
      href = info.href;
      title = info.title || "";
    } else if (/^(?:nostr:)?(?:nevent|nprofile|npub|note)/i.test(token)) {
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

function referenceViewMoreButton(container, width, key) {
  const item = button("view more", (event) => {
    event.preventDefault();
    event.stopPropagation();
    if (!key) return;
    markReferenceExpanded(container, key);
    renderNote(container, width);
  });
  item.setAttribute("aria-expanded", "false");
  return item;
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
  const zapTotal = Number.parseInt(container.dataset.asciiZapTotal || "0", 10) || 0;
  const zapLabel = zapTotal > 0 ? ` ₿${formatCompactSats(zapTotal)}` : "";
  const runeBlockLen = runeLength(`[${up}]`) + runeLength(mid) + runeLength(`[${down}]`) + runeLength(zapLabel);
  const footerParts = [
    noteChrome("["),
    reactionVoteButton(container, "up", up),
    noteChrome("]"),
    noteChrome(mid),
    noteChrome("["),
    reactionVoteButton(container, "down", down),
    noteChrome("]"),
  ];
  if (zapLabel) footerParts.push(noteChrome(zapLabel));
  return { runeBlockLen, footerParts };
}

function pollStateForContainer(container) {
  try {
    const parsed = JSON.parse(String(container?.dataset?.asciiPollState || "{}"));
    return parsed && typeof parsed === "object" ? parsed : {};
  } catch {
    return {};
  }
}

function pollButtonLabel(optionID, pollType, selection, voted) {
  if (voted) return selection.has(optionID) ? "[x]" : "[ ]";
  if (pollType !== PollType.MULTIPLE) return "[vote]";
  return selection.has(optionID) ? "[x]" : "[ ]";
}

function appendPollFeedLine(target, width, beforeNode, innerParts = []) {
  const item = document.createElement("span");
  item.className = "ascii-line";
  item.append(noteChrome("| "));
  const body = document.createElement("span");
  innerParts.forEach((part) => body.append(part));
  const used = innerParts.reduce((sum, part) => sum + runeLength(part.textContent || part.nodeValue || ""), 0);
  body.append(noteChrome(" ".repeat(Math.max(0, Math.max(1, width - 4) - used))));
  item.append(body, noteChrome(" |"));
  target.append(item, "\n");
}

function appendPollReplyLine(target, prefix, innerParts = []) {
  const item = document.createElement("span");
  item.className = "ascii-line";
  item.append(noteChrome(prefix));
  innerParts.forEach((part) => item.append(part));
  target.append(item, "\n");
}

function appendPollContent(target, width, container, prefix = "", mode = "feed") {
  const poll = pollDescriptorForContainer(container);
  if (!poll) return;
  const state = pollStateForContainer(container);
  const tally = state.tally || {};
  const selection = state.voted
    ? new Set(Array.isArray(state.selected) ? state.selected : [])
    : pollDraftSelection(container);
  const totalVotes = Number.parseInt(`${state.totalVotes ?? 0}`, 10) || 0;
  const showResults = Boolean(state.voted || poll.isExpired || (state.loaded && totalVotes > 0));
  const lines = [];
  lines.push([noteChrome(`[poll] ${poll.pollType === PollType.MULTIPLE ? "multiple choice" : "single choice"}`)]);
  poll.options.forEach((option) => {
    const count = Number.parseInt(`${tally[option.id] ?? 0}`, 10) || 0;
    if (showResults) {
      const chosen = selection.has(option.id) ? "* " : "  ";
      lines.push([noteChrome(`${chosen}${option.label} (${count})`)]);
      return;
    }
    const toggle = document.createElement("button");
    toggle.type = "button";
    toggle.className = "link-button";
    toggle.dataset.pollToggleOption = option.id;
    toggle.textContent = pollButtonLabel(option.id, poll.pollType, selection, false);
    lines.push([toggle, noteChrome(` ${option.label}`)]);
  });
  if (showResults) {
    lines.push([noteChrome(totalVotes === 1 ? "1 total vote" : `${totalVotes} total votes`)]);
  } else if (poll.pollType === PollType.MULTIPLE) {
    const submit = document.createElement("button");
    submit.type = "button";
    submit.className = "link-button";
    submit.dataset.pollSubmit = "1";
    submit.textContent = "[submit vote]";
    submit.disabled = selection.size === 0;
    lines.push([submit]);
  }
  lines.forEach((parts) => {
    if (mode === "feed") appendPollFeedLine(target, width, null, parts);
    else appendPollReplyLine(target, prefix, parts);
  });
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
      menu.querySelector("[aria-haspopup='menu']")?.setAttribute("aria-expanded", "false");
    }
  });
}

function actionMenu(container, label, items) {
  const wrap = document.createElement("span");
  wrap.className = "ascii-action-menu";
  const trigger = document.createElement("button");
  trigger.className = "link-button";
  trigger.type = "button";
  trigger.textContent = label;
  trigger.dataset.asciiActionMenuTrigger = "1";
  trigger.setAttribute("aria-haspopup", "menu");
  trigger.setAttribute("aria-expanded", "false");
  wrap.append(trigger);

  const menu = document.createElement("span");
  menu.className = "ascii-action-menu-list";
  menu.setAttribute("role", "menu");
  menu.append(...items);
  wrap.append(menu);
  return wrap;
}

function copyButton(label, value) {
  const item = document.createElement("button");
  item.className = "link-button";
  item.type = "button";
  item.textContent = label;
  item.dataset.noteMenuAction = "copy";
  item.dataset.copyValue = value || "";
  return item;
}

function shareNoteButton(container) {
  const item = document.createElement("button");
  item.className = "link-button";
  item.type = "button";
  item.textContent = "[share]";
  item.dataset.noteMenuAction = "share";
  const href = replyThreadHref(container);
  if (!href || href === "#") item.disabled = true;
  return item;
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

function canBroadcastContainer(container) {
  const id = noteIDForContainer(container);
  const kind = Number.parseInt(String(container?.dataset?.asciiEventKind || "0"), 10);
  const sig = String(container?.dataset?.asciiSig || "").trim();
  return Boolean(id && sig && [1, 6, 1068].includes(kind));
}

function broadcastMenuButton(container) {
  const item = document.createElement("button");
  item.className = "link-button";
  item.type = "button";
  item.textContent = "[broadcast]";
  item.dataset.broadcastEvent = "1";
  item.dataset.noteId = noteIDForContainer(container);
  item.disabled = !canBroadcastContainer(container);
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

function deleteNoteMenuButton(container) {
  const item = document.createElement("button");
  item.className = "link-button";
  item.type = "button";
  item.textContent = "[delete note]";
  item.dataset.noteMenuAction = "delete-note";
  if (!noteIDForContainer(container)) item.disabled = true;
  return item;
}

/** Shared `[...]` items for feed notes, thread replies, and selected focus card. */
function asciiNoteOverflowMenuItems(container) {
  const items = [
    bookmarkToggleButton(container),
    shareNoteButton(container),
    muteAuthorMenuButton(container),
    viewReactionsMenuButton(container),
    repostComposeButton(container),
    broadcastMenuButton(container),
    link(replyThreadHref(container), "[view thread]"),
  ];
	if (canDeleteNoteLight(container)) {
    items.push(deleteNoteMenuButton(container));
  }
  items.push(
    copyButton("[copy note id]", container.dataset.asciiNevent),
    copyButton("[copy user public key]", container.dataset.asciiNpub),
  );
  return items;
}

function viewReactionsMenuButton(container) {
  const id = noteIDForContainer(container);
  const item = document.createElement("button");
  item.className = "link-button";
  item.type = "button";
  item.textContent = "[view reactions]";
  item.dataset.noteMenuAction = "view-reactions";
  if (!id) item.disabled = true;
  return item;
}

function toggleActionMenuFromTrigger(trigger) {
  const wrap = trigger?.closest?.(".ascii-action-menu");
  if (!wrap) return;
  const isOpen = !wrap.classList.contains("is-open");
  closeActionMenus(isOpen ? wrap : null);
  wrap.classList.toggle("is-open", isOpen);
  trigger.setAttribute("aria-expanded", isOpen ? "true" : "false");
}

function noteMenuContainerForAction(target) {
  return target?.closest?.("[data-ascii-kind], [data-thread-tree-note]") || null;
}

function shareSurfaceForContainer(container) {
  const noteID = noteIDForContainer(container);
  const kind = String(container?.dataset?.asciiKind || "").trim();
  const rootID = String(container?.dataset?.replyRootId || container?.dataset?.asciiThreadRootId || "").trim();
  if (kind === "reply" || kind === "selected") return "thread_focus";
  if (kind === "note" && rootID && noteID && rootID !== noteID) return "thread_focus";
  return "note_op";
}

function shareRootIDForContainer(container) {
  return String(container?.dataset?.replyRootId || container?.dataset?.asciiThreadRootId || "").trim();
}

function shareParentIDForContainer(container) {
  const explicit = String(container?.dataset?.shareParentId || "").trim();
  if (explicit) return explicit;
  const focus = container?.closest?.(".thread-focus");
  if (!focus) return "";
  const parent = focus.querySelector?.(".thread-focus-parent[id^='note-']");
  return parent?.id?.replace(/^note-/, "") || "";
}

function handleNoteMenuAction(action, trigger, event) {
  const container = noteMenuContainerForAction(trigger);
  if (!container) return;
  if (action === "copy") {
    event.stopPropagation();
    void copyText(trigger.dataset.copyValue || "", trigger);
    return;
  }
  if (action === "share") {
    event.stopPropagation();
    const href = replyThreadHref(container);
    if (!href || href === "#") return;
    const noteID = noteIDForContainer(container);
    const title = container?.dataset?.asciiAuthor
      ? `${container.dataset.asciiAuthor} on ptxt`
      : document.title || "ptxt";
    const fallbackURL = new URL(href, window.location.origin).toString();
    closeActionMenus();
    async function runShare() {
      let shareURL = fallbackURL;
      if (noteID) {
        const response = await fetchWithSession("/api/shares", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            note_id: noteID,
            surface: shareSurfaceForContainer(container),
            root_id: shareRootIDForContainer(container),
            parent_id: shareParentIDForContainer(container),
          }),
        }).catch(() => null);
        if (response?.ok) {
          const data = await response.json().catch(() => null);
          if (data?.url) shareURL = String(data.url);
        }
      }
      if (typeof navigator.share !== "function") {
        await copyText(shareURL, trigger);
        return;
      }
      try {
        await navigator.share({ title, url: shareURL });
      } catch (error) {
        if (error?.name !== "AbortError") {
          await copyText(shareURL, trigger);
        }
      }
    }
    void runShare();
    return;
  }
  if (action === "view-reactions") {
    event.stopPropagation();
    closeActionMenus();
    const id = noteIDForContainer(container);
		if (id) void import("./reactions.js").then(({ openReactionsModal }) => openReactionsModal(id));
    return;
  }
  if (action === "delete-note") {
    event.stopPropagation();
    closeActionMenus();
    const id = noteIDForContainer(container);
    if (!id) return;
    if (!window.confirm("Delete this note from relays and remove it from your views?")) return;
		void import("./note-deletion.js")
			.then(({ publishNoteDeletion }) => publishNoteDeletion(id))
			.catch((err) => window.alert(err instanceof Error ? err.message : "Delete failed."));
  }
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

function referenceMediaItems(container) {
  return mergeMediaItemsDedup(
    extractMediaItems(referenceSourceText(container)),
    extractReferenceImetaMediaFromNoteContainer(container),
  );
}

function inlineReferenceSources(container) {
  if (!container) return [];
  return [...container.querySelectorAll(":scope > .ascii-inline-reference-source")]
    .map((tmpl, index) => ({
      key: `inline:${tmpl.dataset.asciiRefId || index}`,
      author: tmpl.dataset.asciiRefAuthor || "",
      age: tmpl.dataset.asciiRefAge || "",
      replyLabel: tmpl.dataset.asciiRefReplyLabel || "",
      threadHref: tmpl.dataset.asciiRefThreadHref || "",
      source: tmpl.content?.textContent?.trim() || "",
    }))
    .filter((ref) => ref.source || ref.threadHref);
}

/** Quote/repost body text with image placeholders applied (shared by tree quotes and nested ASCII refs). */
function referenceBodyDisplaySource(container, imageMode) {
  const raw = referenceSourceText(container);
  return displaySourceForMedia(raw, imageMode ? extractMediaItems(raw) : [], imageMode);
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

function takeMainMediaGridForPreRebuild(pre) {
  const wrap = pre.querySelector(":scope .note-media-grid-row:not(.reference-media-row) .note-media-grid-wrap");
  if (!wrap) return null;
  wrap.remove();
  return wrap;
}

function takeReferenceMediaGridsForPreRebuild(pre) {
  return [...pre.querySelectorAll(":scope .reference-media-row .note-media-grid-wrap")].map((wrap) => {
    wrap.remove();
    return wrap;
  });
}

function takeMatchingMediaGrid(wraps, items) {
  const signature = mediaGridSignature(items);
  const index = wraps.findIndex((wrap) => wrap.dataset.mediaGridSignature === signature);
  if (index < 0) return null;
  return wraps.splice(index, 1)[0];
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

function blossomMediaPathKey(url) {
  if (!url) return "";
  const cleaned = String(url).trim().replace(TRAILING_URL_PUNCTUATION, "");
  const match = /^https?:\/\/[^/]*\.blossom\.band\/([^\s<>"'`?#]+)/i.exec(cleaned);
  return match ? match[1].toLowerCase() : "";
}

function mediaDedupKey(item) {
  const blossomKey = blossomMediaPathKey(item?.url || "");
  return blossomKey ? `blossom:${blossomKey}` : String(item?.url || "");
}

function mergeMediaItemsDedup(a, b) {
  const seen = new Map();
  const out = [];
  for (const list of [a, b]) {
    for (const item of list) {
      if (!item?.url) continue;
      const key = mediaDedupKey(item);
      if (seen.has(key)) {
        // Later `imeta` items should replace display-only Blossom body URLs.
        out[seen.get(key)] = item;
        continue;
      }
      seen.set(key, out.length);
      out.push(item);
    }
  }
  return out;
}

/** Parse `data-ascii-imeta-media` JSON from the note/reply shell (NIP-94 imeta tags). */
function extractImetaMediaFromDataset(raw) {
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    const out = [];
    for (const entry of parsed) {
      const url = typeof entry?.url === "string" ? entry.url.trim() : "";
      const type = typeof entry?.type === "string" ? entry.type.trim() : "";
      if (!url || (type !== "image" && type !== "video")) continue;
      if (!/^https?:\/\//i.test(url)) continue;
      const width = Number.parseInt(`${entry?.width ?? ""}`, 10);
      const height = Number.parseInt(`${entry?.height ?? ""}`, 10);
      out.push({
        url,
        type,
        ...(width > 0 && height > 0 ? { width, height } : {}),
      });
    }
    return out;
  } catch {
    return [];
  }
}

function extractImetaMediaFromNoteContainer(container) {
  return extractImetaMediaFromDataset(container?.dataset?.asciiImetaMedia || "");
}

function extractReferenceImetaMediaFromNoteContainer(container) {
  return extractImetaMediaFromDataset(container?.dataset?.asciiRefImetaMedia || "");
}

/** Media URLs from note body plus optional `imeta` tags (deduped). */
function mainBodyMediaItems(container, text) {
  const bodyItems = extractMediaItems(text);
  if (container?.dataset?.asciiRefMode === "repost") return bodyItems;
  return mergeMediaItemsDedup(bodyItems, extractImetaMediaFromNoteContainer(container));
}

/** @param {ReturnType<typeof mainBodyMediaItems> | undefined} precomputedMain when caller already computed main-body items for `sourceText(container)`. */
function mediaItemsForAsciiNote(container, precomputedMain) {
  return precomputedMain ?? mainBodyMediaItems(container, sourceText(container));
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

// In image mode, returns rawSource with media URLs stripped. Outside image mode,
// returns rawSource unchanged.
function displaySourceForMedia(rawSource, mediaItems, imageMode) {
  if (!imageMode || mediaItems.length === 0) return rawSource;
  return stripMediaUrlsFromText(rawSource);
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

function normalizeViewerItem(item) {
  if (typeof item === "string") return { url: item, type: isVideoAssetHttpsUrl(item) ? "video" : "image" };
  const url = typeof item?.url === "string" ? item.url : "";
  const type = item?.type === "video" ? "video" : "image";
  return url ? { url, type } : null;
}

function setImageViewerState(items, index = 0, owner = null) {
  imageViewerState.items = Array.isArray(items) ? items.map(normalizeViewerItem).filter(Boolean) : [];
  imageViewerState.ownerNoteID = noteIDForContainer(owner);
  if (!imageViewerState.items.length) {
    imageViewerState.index = 0;
    return;
  }
  const bounded = Number.isFinite(index) ? Math.trunc(index) : 0;
  imageViewerState.index = Math.min(imageViewerState.items.length - 1, Math.max(0, bounded));
}

function renderImageViewer(dialog) {
  if (!dialog) return;
  const media = dialog.querySelector("[data-image-viewer-media]");
  const prev = dialog.querySelector("[data-image-viewer-prev]");
  const next = dialog.querySelector("[data-image-viewer-next]");
  if (!media) return;
  media.textContent = "";
  if (!imageViewerState.items.length) {
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
  const total = imageViewerState.items.length;
  const index = Math.min(total - 1, Math.max(0, imageViewerState.index));
  imageViewerState.index = index;
  const item = imageViewerState.items[index];
  if (item.type === "video") {
    const video = document.createElement("video");
    video.src = item.url;
    video.controls = true;
    video.autoplay = true;
    prepareInlineVideo(video);
    media.append(video);
  } else {
    const img = document.createElement("img");
    img.src = item.url;
    img.alt = "";
    media.append(img);
  }
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
  if (!imageViewerState.items.length) return;
  const total = imageViewerState.items.length;
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
      <div class="image-viewer-media" data-image-viewer-media></div>
      <button type="button" class="image-viewer-nav image-viewer-nav-next" data-image-viewer-next aria-label="Next image">&gt;</button>
    </div>
  `;
  dialog.addEventListener("click", (event) => {
    if (event.target === dialog) {
      dialog.close();
      return;
    }
    if (!(event.target instanceof Element)) return;
    const control = event.target.closest(".image-viewer-nav, .image-viewer-close-row");
    if (control && dialog.contains(control)) return;
    const media = event.target.closest("[data-image-viewer-media]");
    if (media && dialog.contains(media)) return;
    const body = event.target.closest(".image-viewer-body");
    if (body && dialog.contains(body)) dialog.close();
  });
  dialog.addEventListener("close", () => {
    if (!imageViewerState.ownerNoteID) return;
    const owner = document.getElementById(`note-${imageViewerState.ownerNoteID}`);
    if (!owner || !imageMount(owner)) return;
    if (owner.dataset.asciiMediaExpanded === "true") return;
    owner.dataset.asciiMediaExpanded = "true";
    markAsciiDirty(owner);
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
  document.body.append(dialog);
  return dialog;
}

export function openImageViewer(urls, index = 0, owner = null) {
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
  prepareInlineVideo(video);
  figure.append(video);
  return figure;
}

function mediaPreview(item, options = {}) {
  if (item.type === "video") return videoPreview(item.url);
  return imagePreview(item.url, options);
}

function mediaGrid(container, items, reusableWrap = null) {
  if (!items.length) return null;
  if (
    reusableWrap instanceof HTMLElement &&
    reusableWrap.dataset.mediaGridSignature === mediaGridSignature(items)
  ) {
    const hydrated = hydrateMediaGrid(reusableWrap, items, {
      onOpen: (index) => openImageViewer(items, index, container),
    });
    if (hydrated) return hydrated;
  }
  const wrap = createMediaGrid(items, {
    wrapperClass: "ascii-inline-media",
    onOpen: (index) => openImageViewer(items, index, container),
  });
  return wrap;
}

function mediaFooterButton(container, label) {
  const button = document.createElement("button");
  button.className = "link-button note-footer-media-link";
  button.type = "button";
  button.textContent = label || "[media]";
  button.addEventListener("click", (event) => {
    event.preventDefault();
    const mediaItems = mediaItemsForAsciiNote(container);
    if (mediaItems.length) {
      openImageViewer(mediaItems, 0, container);
      return;
    }
    container.querySelector(".note-media-grid-row")?.scrollIntoView({ block: "nearest" });
  });
  return button;
}

function renderMountedMedia(container, items) {
  const mount = imageMount(container);
  if (!mount) return;
  const enabled = getImageModePref();
  if (enabled) {
    mount.textContent = "";
    mount.hidden = true;
    return;
  }
  const expanded = container.dataset.asciiMediaExpanded === "true";
  mount.textContent = "";
  mount.hidden = !expanded || !items.length;
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

function appendNoteMedia(container, content, items, lineFactory, { reusableGridWrap = null } = {}) {
  if (!items.length) return;
  const imageMode = getImageModePref();
  if (imageMode) {
    const body = mediaGrid(container, items, reusableGridWrap);
    if (!body) return;
    content.append(lineFactory({
      className: "note-media-grid-row",
      body,
      isGrid: true,
    }), "\n");
    return;
  }
  items.forEach((item) => {
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

function measureGlyphMetrics(pre) {
  return measureLayoutGlyphMetrics(pre);
}

function measureColumns(container, pre, widthHint = 0) {
  const startedAt = performance.now();
  const widthPx = Number.isFinite(widthHint) && widthHint > 0
    ? widthHint
    : container.getBoundingClientRect().width;
  if (!widthPx) return 0;
  const metrics = measureGlyphMetrics(pre);
  const columns = columnsFromWidth(widthPx, metrics.asciiWidth);
  asciiPerf.state.measureCalls += 1;
  asciiPerf.state.measureMs += performance.now() - startedAt;
  return columns;
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
  DISPLAY_BLOSSOM_URL_PATTERN.lastIndex = 0;
  const displayMatch = DISPLAY_BLOSSOM_URL_PATTERN.exec(word);
  const m = displayMatch && displayMatch.index === 0 ? displayMatch : HTTPS_URL_PATTERN.exec(word);
  if (!m || m.index !== 0) return null;
  const raw = m[0];
  const href = raw.replace(TRAILING_URL_PUNCTUATION, "");
  const punctFromMatch = raw.slice(href.length);
  const afterMatch = word.slice(raw.length);
  if (afterMatch && !/^[,).!?;:]*$/.test(afterMatch)) return null;

  const isMedia = isMediaAssetHttpsUrl(href);
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

function tokenizeWrapWords(raw) {
  const source = raw.trim();
  if (!source) return [];
  const words = [];
  let cursor = 0;
  DISPLAY_BLOSSOM_URL_PATTERN.lastIndex = 0;
  let match;
  while ((match = DISPLAY_BLOSSOM_URL_PATTERN.exec(source)) !== null) {
    if (match.index > cursor) {
      words.push(...source.slice(cursor, match.index).trim().split(/\s+/).filter(Boolean));
    }
    words.push(match[0]);
    cursor = match.index + match[0].length;
  }
  if (cursor < source.length) {
    words.push(...source.slice(cursor).trim().split(/\s+/).filter(Boolean));
  }
  return words;
}

function wrapText(text, width) {
  const clean = text.trim();
  if (!clean) return [{ text: "", ext: null }];
  const rows = [];
  const pushRow = (t, ext = null) => {
    rows.push({ text: t, ext });
  };

  clean.split("\n").forEach((raw) => {
    const wordQueue = tokenizeWrapWords(raw);
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
function appendThreadTreeQuoteMinimal(target, width, container, imageMode, reusableMediaGrid = null) {
  const mode = container.dataset.asciiRefMode;
  if (!mode) return;
  const referenceSource = referenceBodyDisplaySource(container, imageMode).trim();
  const refMediaItems = imageMode ? referenceMediaItems(container) : [];
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
  if (referenceSource) {
    wrapText(referenceSource, tw).forEach((row) => {
      const line = document.createElement("span");
      line.className = "thread-tree-text-line";
      appendAsciiTextWithLineLink(line, row.text, container, row.ext, null);
      target.append(line);
    });
  }
  if (refMediaItems.length > 0) {
    const media = document.createElement("div");
    media.className = "thread-tree-quote-media";
    const hydrated = hydrateMediaGrid(reusableMediaGrid, refMediaItems, {
      stopPropagation: true,
      onOpen: (index) => openImageViewer(refMediaItems, index, container),
    });
    media.append(hydrated || createMediaGrid(refMediaItems, {
      wrapperTag: "div",
      gridTag: "div",
      wrapperClass: "thread-tree-media-grid-wrap",
      stopPropagation: true,
      onOpen: (index) => openImageViewer(refMediaItems, index, container),
    }));
    target.append(media);
  }
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
    const reusableMediaGrid = mount.querySelector(":scope .note-media-grid-wrap");
    reusableMediaGrid?.remove();
    mount.textContent = "";
    appendThreadTreeQuoteMinimal(mount, width, card, imageModeOn, reusableMediaGrid);
    mount.hidden = mount.childNodes.length === 0;
  });
}

function appendNestedReferenceLines(target, width, container, reusableMediaGrids = []) {
  const mode = container.dataset.asciiRefMode;
  if (!mode) return;
  const imageMode = getImageModePref();
  const card = document.createElement("span");
  card.className = "ascii-reference-card";
  const threadHref = container.dataset.asciiRefThreadHref || "";
  if (threadHref) {
    card.dataset.asciiRefSelectHref = threadHref;
    bindReferenceCardNavigation(card, threadHref);
  }
  appendReferenceCardLines(card, width, container, {
    key: "nested",
    author: container.dataset.asciiRefAuthor || "",
    age: container.dataset.asciiRefAge || "",
    replyLabel: container.dataset.asciiRefReplyLabel || "",
    threadHref,
    source: referenceBodyDisplaySource(container, imageMode),
    mediaItems: imageMode ? referenceMediaItems(container) : [],
  }, reusableMediaGrids);
  target.append(card);
}

function appendInlineReferenceLines(target, width, container) {
  const imageMode = getImageModePref();
  inlineReferenceSources(container).forEach((ref) => {
    const card = document.createElement("span");
    card.className = "ascii-reference-card";
    if (ref.threadHref) {
      card.dataset.asciiRefSelectHref = ref.threadHref;
      bindReferenceCardNavigation(card, ref.threadHref);
    }
    appendReferenceCardLines(card, width, container, {
      ...ref,
      source: displaySourceForMedia(ref.source, imageMode ? extractMediaItems(ref.source) : [], imageMode),
    });
    target.append(card);
  });
}

function bindReferenceCardNavigation(card, threadHref) {
  card.addEventListener("click", (event) => {
    if (event.defaultPrevented || (typeof event.button === "number" && event.button !== 0)) return;
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    const target = event.target;
    if (!(target instanceof Element)) return;
    if (target.closest("a, button, input, textarea, select, summary, [contenteditable='true']")) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    window.location.assign(threadHref);
  });
}

function appendReferenceCardLines(target, width, container, ref, reusableMediaGrids = []) {
  const innerWidth = Math.max(20, width - 8);
  const innerContentWidth = Math.max(8, innerWidth - 4);
  const refAuthor = truncateMiddle(ref.author || "", Math.max(8, innerWidth - 16));
  const refAge = ref.age || "";
  const refReplyLabel = ref.replyLabel || "";
  const refThreadHref = ref.threadHref || "";
  const refAttrs = refThreadHref
    ? { asciiRefSelectHref: refThreadHref, asciiRefHit: "1" }
    : null;
  const referenceSource = ref.source || "";
  const headerRule = repeat("-", Math.max(1, innerWidth - runeLength(`+- ${refAuthor} -- ${refAge} +`)));
  appendBoxedPartsLine(target, width, [
    noteChrome("  +- "),
    document.createTextNode(refAuthor),
    noteChrome(` -- ${refAge} ${headerRule}+`),
  ], refAttrs);
  const rows = wrapText(referenceSource, innerContentWidth);
  const collapsing = rows.length > collapsedNoteLines && !isReferenceExpanded(container, ref.key);
  const visibleRows = collapsing ? rows.slice(0, collapsedNoteLines) : rows;
  if (collapsing && visibleRows.length > 0) {
    const li = visibleRows.length - 1;
    visibleRows[li] = { ...visibleRows[li], text: addTrailingDots(visibleRows[li].text, innerContentWidth) };
  }
  visibleRows.forEach((row) => {
    appendReferenceBodyLine(target, width, row.text, innerContentWidth, refAttrs, container);
  });
  appendReferenceMediaLines(
    target,
    width,
    container,
    refAttrs,
    ref.mediaItems || [],
    reusableMediaGrids,
  );
  if (collapsing) {
    appendReferenceViewMoreLine(target, width, refAttrs, referenceViewMoreButton(container, width, ref.key));
  }
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
  appendBoxedPartsLine(target, width, [
    noteChrome("  +-- "),
    noteChrome(refRb),
    noteChrome(` ${footerRule}`),
    ...(refReplyLabel ? [noteChrome(" "), document.createTextNode(refReplyLabel)] : []),
    noteChrome(" "),
    noteChrome("["),
    document.createTextNode("reply"),
    noteChrome("]"),
    noteChrome(" ---+"),
  ], refAttrs);
}

function appendReferenceBodyLine(target, width, text, innerContentWidth, attrs, container) {
  const item = document.createElement("span");
  item.className = "ascii-line";
  if (attrs) {
    Object.entries(attrs).forEach(([key, value]) => {
      if (value === undefined || value === null || value === "") return;
      item.dataset[key] = value;
    });
  }
  item.append(noteChrome("| "));
  const middle = document.createElement("span");
  middle.append(noteChrome("  | "));
  const body = document.createElement("span");
  const usedText = appendAsciiTextWithLineLink(body, text, container, null, null);
  middle.append(body, noteChrome(`${" ".repeat(Math.max(0, innerContentWidth - usedText))} |`));
  const used = runeLength("  | ") + usedText + Math.max(0, innerContentWidth - usedText) + runeLength(" |");
  middle.append(" ".repeat(Math.max(0, Math.max(1, width - 4) - used)));
  const refHref = attrs?.asciiRefSelectHref || "";
  if (refHref && !body.querySelector("a, button")) {
    const link = document.createElement("a");
    link.className = "ascii-reference-line-link";
    link.href = refHref;
    link.append(...middle.childNodes);
    item.append(link, noteChrome(" |"));
  } else {
    item.append(middle, noteChrome(" |"));
  }
  target.append(item, "\n");
}

function appendBoxedPartsLine(target, width, parts, attrs) {
  const refHref = attrs?.asciiRefSelectHref || "";
  const item = document.createElement(refHref ? "a" : "span");
  item.className = "ascii-line";
  if (refHref) item.href = refHref;
  if (attrs) {
    Object.entries(attrs).forEach(([key, value]) => {
      if (value === undefined || value === null || value === "") return;
      item.dataset[key] = value;
    });
  }
  item.append(noteChrome("| "));
  const middle = document.createElement("span");
  parts.forEach((part) => middle.append(part));
  const used = parts.reduce((sum, part) => sum + runeLength(part.textContent || part.nodeValue || ""), 0);
  middle.append(" ".repeat(Math.max(0, Math.max(1, width - 4) - used)));
  item.append(middle, noteChrome(" |"));
  target.append(item, "\n");
}

function appendReferenceMediaLines(target, width, container, attrs, items, reusableMediaGrids = []) {
  if (!items.length) return;
  const body = mediaGrid(container, items, takeMatchingMediaGrid(reusableMediaGrids, items));
  if (!body) return;
  const item = document.createElement("span");
  item.className = "ascii-line note-image-boxed-row note-media-grid-row reference-media-row";
  item.style.setProperty("--ascii-box-row-width", `${width}ch`);
  if (attrs) {
    Object.entries(attrs).forEach(([key, value]) => {
      if (value === undefined || value === null || value === "") return;
      item.dataset[key] = value;
    });
  }
  const prefix = document.createElement("span");
  prefix.className = "note-media-reference-prefix";
  prefix.setAttribute("aria-hidden", "true");
  const suffix = document.createElement("span");
  suffix.className = "note-media-reference-suffix";
  suffix.setAttribute("aria-hidden", "true");
  item.append(prefix, body, suffix);
  target.append(item, "\n");
}

function appendReferenceViewMoreLine(target, width, attrs, vmButton) {
  const innerWidth = Math.max(20, width - 8);
  const innerContentWidth = Math.max(8, innerWidth - 4);
  const item = document.createElement("span");
  item.className = "ascii-line";
  if (attrs) {
    Object.entries(attrs).forEach(([key, value]) => {
      if (value === undefined || value === null || value === "") return;
      item.dataset[key] = value;
    });
  }
  item.append(noteChrome("| "));
  const middle = document.createElement("span");
  const label = vmButton.textContent || "view more";
  middle.append(
    noteChrome("  | "),
    vmButton,
    noteChrome(" ".repeat(Math.max(0, innerContentWidth - runeLength(label)))),
    noteChrome(" |"),
  );
  const used = runeLength(`  | ${label}`) + Math.max(0, innerContentWidth - runeLength(label)) + runeLength(" |");
  middle.append(" ".repeat(Math.max(0, Math.max(1, width - 4) - used)));
  item.append(middle, noteChrome(" |"));
  target.append(item, "\n");
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
  const hasMediaItems = mediaItems.length > 0;
  const hasMedia = imageMode && hasMediaItems;
  const author = authorForWidth(container, width);
  const age = container.dataset.asciiAge || "";
  const replyCount = Number.parseInt(container.dataset.asciiReplyCount || "0", 10);
  const replyLabelDataset = container.dataset.asciiReplyLabel || replyLabelForCount(replyCount);
  const contentWidth = Math.max(1, width - 4);
  const noteSource = displaySourceForMedia(rawSource, outerMediaItems, imageMode);
  const allRows = refMode === "repost" || !noteSource.trim() ? [] : wrapText(noteSource, contentWidth);
  const isLong = allRows.length > collapsedNoteLines;
  const isExpanded = container.dataset.asciiExpanded === "true";
  const collapsing = isLong && !isExpanded;
  const viewMoreInBody = collapsing && mobileActionsQuery.matches;
  const viewMoreInHeader =
    collapsing && !mobileActionsQuery.matches && (hasReference || hasMediaItems);
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
  const headerActionLabel = viewMoreInHeader ? "view more" : "[...]";
  const topSuffix = `${headerActionLabel}+`;
  const feedAvatarReserve = hasFeedAvatar ? feedNoteAvatarRuneReserve : 0;
  const headerDashCount = Math.max(1, width - runeLength(topPrefix + topSuffix) - feedAvatarReserve);
  const headerAction = () =>
    viewMoreInHeader ? viewMoreButton(container, width) : noteMenu(container);
  const savedFeedAvatar = takeFeedAvatarForPreRebuild(pre);
  const savedMediaMount = takeImageMountForPreRebuild(container);
  let savedMediaGrid = takeMainMediaGridForPreRebuild(pre);
  const savedReferenceMediaGrids = takeReferenceMediaGridsForPreRebuild(pre);
  const consumeSavedMediaGrid = () => {
    const wrap = savedMediaGrid;
    savedMediaGrid = null;
    return wrap;
  };
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
      headerAction(),
      noteChrome("+"),
    );
    headerLine.append(tail);
    pre.append(headerLine, "\n");
  } else {
    appendLine(pre, [
      noteChrome("+- "),
      link(container.dataset.asciiUserHref || "#", author),
      noteChrome(` -- ${age} ${repeat("-", headerDashCount)}`),
      headerAction(),
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
  appendPollContent(content, width, container, "", "feed");
  appendInlineReferenceLines(content, width, container);
  const appendOuterMedia = () => appendNoteMedia(container, content, mediaItems, ({ className = "", body, hidden = false, isGrid = false }) => {
    const item = document.createElement("span");
    item.className = `ascii-line note-image-boxed-row ${className}`.trim();
    item.hidden = hidden;
    item.style.setProperty("--ascii-box-row-width", `${width}ch`);
    if (isGrid) {
      const leftEdge = document.createElement("span");
      leftEdge.className = "note-media-grid-edge note-media-grid-edge-left";
      leftEdge.setAttribute("aria-hidden", "true");
      const rightEdge = document.createElement("span");
      rightEdge.className = "note-media-grid-edge note-media-grid-edge-right";
      rightEdge.setAttribute("aria-hidden", "true");
      item.append(leftEdge, body, rightEdge);
    } else {
      item.append(noteChrome("| "), body, noteChrome(" |"));
    }
    return item;
  }, { reusableGridWrap: consumeSavedMediaGrid() });
  if (refMode === "quote") appendOuterMedia();
  if (hasReference) {
    appendNestedReferenceLines(content, width, container, savedReferenceMediaGrids);
  }
  if (refMode !== "quote") appendOuterMedia();
  pre.append(content);
  if (savedMediaMount) pre.append(savedMediaMount);
  appendLine(pre, [noteChrome(boxLine(width))]);
  const threadHref = container.dataset.asciiThreadHref || "#";
  const reactionSeg = reactionLayoutSegments(container);
  const collapseRunes =
    collapsing && !viewMoreInBody && !viewMoreInHeader
      ? runeLength(" --- ") + runeLength("view more")
      : 0;
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
  if (collapsing && !viewMoreInBody && !viewMoreInHeader) {
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
  let savedMediaGrid = takeMainMediaGridForPreRebuild(pre);
  const savedReferenceMediaGrids = takeReferenceMediaGridsForPreRebuild(pre);
  const consumeSavedMediaGrid = () => {
    const wrap = savedMediaGrid;
    savedMediaGrid = null;
    return wrap;
  };
  pre.textContent = "";
  const headerParts = [
    link(container.dataset.asciiUserHref || "#", visibleAuthor),
    noteChrome(` -- ${age}`),
  ];
  const pad = " ".repeat(Math.max(1, headerWidth - runeLength(leftText) - runeLength("[...]")));
  headerParts.push(noteChrome(pad), noteMenu(container));
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
  const refMode = container.dataset.asciiRefMode || "";
  const tw = replyTextWidth(width);
  const replyRows =
    refMode === "repost" || !replySource.trim() ? [] : wrapText(replySource, tw);
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
  appendPollContent(content, width, container, contentPrefix, "reply");
  const appendReplyMedia = () => appendNoteMedia(container, content, replyMediaItems, ({ className = "", body, hidden = false, isGrid = false }) => {
    const item = document.createElement("span");
    item.className = `ascii-line reply-media-row ${className}`.trim();
    item.hidden = hidden;
    if (isGrid) {
      const prefix = document.createElement("span");
      prefix.className = "note-media-reply-prefix";
      if (contentPrefix.startsWith("|")) prefix.classList.add("has-rail");
      prefix.setAttribute("aria-hidden", "true");
      item.append(prefix, body);
    } else {
      item.append(noteChrome(contentPrefix), body);
    }
    return item;
  }, { reusableGridWrap: consumeSavedMediaGrid() });
  if (refMode === "quote") appendReplyMedia();
  appendNestedReferenceLines(content, width, container, savedReferenceMediaGrids);
  if (refMode !== "quote") appendReplyMedia();
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
  const footerParts = [noteChrome(contentPrefix), noteChrome(" "), ...reactionSeg.footerParts, noteChrome(" ")];
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
    appendLine(subtree, [noteChrome(linePrefix)]);
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
  const refMode = container.dataset.asciiRefMode || "";
  const author = authorForWidth(container, width);
  const age = container.dataset.asciiAge || "";
  const hasVisibleChildren = container.dataset.asciiHasVisibleChildren === "true";
  const bodyPrefix = hasVisibleChildren ? "| " : "";
  const bodyPrefixRunes = runeLength(bodyPrefix);
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
  // The CSS avatar inset consumes `headerAvatarReserve` visual columns. Keep
  // the complete `[...] +` header inside the same measured width as the body.
  const topRule = repeat("-", Math.max(1, headerWidth - runeLength(topPrefix + topSuffix)));
  let savedMediaGrid = takeMainMediaGridForPreRebuild(pre);
  const savedReferenceMediaGrids = takeReferenceMediaGridsForPreRebuild(pre);
  const consumeSavedMediaGrid = () => {
    const wrap = savedMediaGrid;
    savedMediaGrid = null;
    return wrap;
  };
  pre.textContent = "";
  appendLine(pre, [
    link(container.dataset.asciiUserHref || "#", visibleAuthor),
    noteChrome(` -- ${age} `),
    noteChrome(topRule),
    noteMenu(container),
    noteChrome("+"),
  ]);
  const content = document.createElement("span");
  content.className = "note-content";
  const contentWidth = Math.max(1, width - 2 - bodyPrefixRunes);
  const selectedPadSpaces = " ".repeat(contentWidth);
  const appendSelectedPadLine = () => {
    const item = document.createElement("span");
    item.className = "ascii-line";
    if (bodyPrefix) item.append(noteChrome(bodyPrefix));
    const middle = document.createElement("span");
    middle.append(selectedPadSpaces);
    item.append(middle, noteChrome(" |"));
    content.append(item, "\n");
  };
  const selectedRows =
    refMode === "repost" || !selectedSource.trim() ? [] : wrapText(selectedSource, contentWidth);
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
    if (bodyPrefix) item.append(noteChrome(bodyPrefix));
    item.append(middle, noteChrome(" |"));
    content.append(item, "\n");
  });
  if (selectedRows.length > 0) {
    appendSelectedPadLine();
  }
  const appendSelectedMedia = () => appendNoteMedia(container, content, mediaItems, ({ className = "", body, hidden = false, isGrid = false }) => {
    const item = document.createElement("span");
    item.className = `ascii-line selected-media-row ${className}`.trim();
    item.hidden = hidden;
    const isFocusedReply = container.classList.contains("thread-focus-selected");
    if (isGrid) {
      const rightEdge = document.createElement("span");
      rightEdge.className = "note-media-grid-edge note-media-grid-edge-right";
      rightEdge.setAttribute("aria-hidden", "true");
      if (isFocusedReply) {
        item.append(body, rightEdge);
      } else {
        const leftEdge = document.createElement("span");
        leftEdge.className = "note-media-grid-edge note-media-grid-edge-left";
        leftEdge.setAttribute("aria-hidden", "true");
        item.append(leftEdge, body, rightEdge);
      }
    } else {
      if (!isFocusedReply) item.append(noteChrome("| "));
      item.append(body, noteChrome(" |"));
    }
    return item;
  }, { reusableGridWrap: consumeSavedMediaGrid() });
  if (refMode === "quote") appendSelectedMedia();
  appendNestedReferenceLines(content, width, container, savedReferenceMediaGrids);
  appendPollContent(content, width, container, bodyPrefix, "reply");
  if (refMode !== "quote") appendSelectedMedia();
  pre.append(content);
  const threadHrefSel = container.dataset.asciiThreadHref || "#";
  const reactionSegSel = reactionLayoutSegments(container);
  const leftFixedSel = bodyPrefixRunes + 1 + reactionSegSel.runeBlockLen + 1;
  const tailSel = (withReplyLabel) => {
    const close = runeLength(" ---+");
    const bracket = runeLength("[reply]");
    if (withReplyLabel) {
      return 1 + runeLength(replyLabel) + 1 + bracket + close;
    }
    return 1 + bracket + close;
  };
  const replyParts = [];
  if (bodyPrefix) replyParts.push(noteChrome(bodyPrefix));
  replyParts.push(noteChrome(" "), ...reactionSegSel.footerParts, noteChrome(" "));
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
    item.append(noteChrome(linePrefix), noteChrome(spaces), link(href, label));
  });
}

function markAsciiDirty(container) {
  if (!(container instanceof Element)) return;
  container.dataset.ptxtAsciiDirty = "1";
}

function clearAsciiDirty(container) {
  if (!(container instanceof Element)) return;
  delete container.dataset.ptxtAsciiDirty;
}

function renderAscii(container, options = {}) {
  const pre = container.querySelector(":scope > .ascii-card, :scope > .ascii-reply");
  if (!pre) return;
  const startedAt = performance.now();
  const width = measureColumns(container, pre, options.widthHint || 0);
  persistAsciiWidth(container, width);
  const cacheKey = buildAsciiRenderCacheKey(width, mobileActionsQuery.matches, getImageModePref());
  const dirty = options.force === true || container.dataset.ptxtAsciiDirty === "1";
  if (!shouldRenderAscii(container._ptxtAsciiLayoutKey || "", cacheKey, dirty)) {
    asciiPerf.state.renderCalls += 1;
    asciiPerf.state.skippedRenderCalls += 1;
    return;
  }
  container._ptxtAsciiLayoutKey = cacheKey;
  const renderBodyStartedAt = performance.now();
  if (container.dataset.asciiKind === "note") {
    renderNote(container, width);
  } else if (container.dataset.asciiKind === "reply") {
    renderReply(container, width);
  } else if (container.dataset.asciiKind === "selected") {
    renderSelected(container, width);
  }
	if (localViewerPubkey()) {
		void import("./mutations.js").then(({ syncMuteToggleButtons }) => syncMuteToggleButtons(container));
	}
  clearAsciiDirty(container);
  asciiPerf.state.renderCalls += 1;
  asciiPerf.state.renderedCards += 1;
  asciiPerf.state.renderBodyMs += performance.now() - renderBodyStartedAt;
  asciiPerf.state.renderMs += performance.now() - startedAt;
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

function queryThreadSkeletonCards(root = document) {
  const selector = ".thread-reply-skeleton-item > .text-skeleton-note, .thread-focus-parent--skeleton > .text-skeleton-note, .thread-selected-skeleton > .text-skeleton-note";
  if (root === document) {
    return [...document.querySelectorAll(selector)];
  }
  if (!(root instanceof Element)) return [];
  const cards = root.matches(selector) ? [root] : [];
  cards.push(...root.querySelectorAll(selector));
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

function renderThreadSkeletonCards(root = document) {
  const cards = queryThreadSkeletonCards(root);
  cards.forEach((card) => {
    const container = card.closest(".comment, .note") || card.parentElement || card;
    const width = measureColumns(container, card) || minColumns;
    if (card.closest(".thread-selected-skeleton")) {
      card.textContent = buildThreadSelectedSkeletonText(width);
      return;
    }
    if (card.closest(".thread-focus-parent--skeleton")) {
      card.textContent = buildThreadParentSkeletonText(width);
      return;
    }
    const item = card.closest(".thread-reply-skeleton-item");
    card.textContent = buildThreadReplySkeletonText(width, { isLast: !item?.nextElementSibling });
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
    renderThreadSkeletonCards(document);
  } else {
    updated += renderFeedLoaders(root);
    updated += renderSkeletonWaveCards(root);
    renderThreadSkeletonCards(root);
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
	if (localViewerPubkey()) {
		void import("./reactions.js").then(({ ensureNoteReactionsDelegated }) => ensureNoteReactionsDelegated());
	}
  bindPollDelegates();
  bindBroadcastDelegates();
  asciiKindRoots(root).forEach(observeAscii);
  if (!document.body?.dataset?.guestV2) {
    void hydrateVisibleZapTotals(root).catch(() => {});
  }
}

function flushAsciiRefreshRoots(roots) {
  roots.forEach((item) => {
    asciiKindRoots(item).forEach((container) => {
      markAsciiDirty(container);
      renderAscii(container);
    });
    refreshThreadTreeQuotes(item);
    renderThreadSkeletonCards(item);
  });
}

/** Re-layout ascii immediately — use during route/focus swaps to avoid one-frame overflow. */
export function refreshAsciiSync(root = document) {
  flushAsciiRefreshRoots([root]);
}

export function refreshAscii(root = document) {
  pendingAsciiRefreshRoots.add(root);
  if (asciiRefreshScheduled) return;
  asciiRefreshScheduled = true;
  requestAnimationFrame(() => {
    asciiRefreshScheduled = false;
    const roots = [...pendingAsciiRefreshRoots];
    pendingAsciiRefreshRoots.clear();
    flushAsciiRefreshRoots(roots);
  });
}

const pendingAsciiRefreshRoots = new Set();
let asciiRefreshScheduled = false;

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

document.addEventListener("click", (event) => {
  if (!(event.target instanceof Element)) return;
  const trigger = event.target.closest("[data-ascii-action-menu-trigger]");
  if (trigger) {
    event.preventDefault();
    event.stopPropagation();
    toggleActionMenuFromTrigger(trigger);
    return;
  }
  const action = event.target.closest("[data-note-menu-action]");
  if (action) {
    event.preventDefault();
    event.stopPropagation();
    handleNoteMenuAction(action.dataset.noteMenuAction || "", action, event);
  }
}, true);

document.addEventListener("click", (event) => {
  if (!(event.target instanceof Element)) {
    closeActionMenus();
    return;
  }
  if (event.target.closest("[data-ascii-action-menu-trigger], [data-note-menu-action]")) return;
  closeActionMenus();
});

window.addEventListener("ptxt:poll-updated", (event) => {
  const noteId = String(event?.detail?.noteId || "").trim().toLowerCase();
  if (!noteId) {
    rerenderAllAscii();
    return;
  }
  const node = document.getElementById(`note-${noteId}`);
  if (node) refreshAscii(node);
});

function rerenderAllAscii() {
  asciiKindRoots(document).forEach((container) => {
    markAsciiDirty(container);
    renderAscii(container);
  });
  refreshThreadTreeQuotes(document);
  renderThreadSkeletonCards(document);
}

mobileActionsQuery.addEventListener("change", () => {
  rerenderAllAscii();
  renderFeedLoaders(document);
  renderSkeletonWaveCards(document);
  renderThreadSkeletonCards(document);
});

window.addEventListener("ptxt:image-mode-changed", () => {
  rerenderAllAscii();
});
