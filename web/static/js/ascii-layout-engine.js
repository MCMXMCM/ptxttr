import { layoutWithLines, prepareWithSegments } from "../lib/pretext.js";

let graphemeSegmenter = null;
let useDoubleWideCells = true;
const preparedTextCache = new Map();
const fontMetricsCache = new Map();
let asciiMeasureNode = null;

function ensureGraphemeSegmenter() {
  if (graphemeSegmenter) return graphemeSegmenter;
  if (typeof Intl !== "undefined" && Intl.Segmenter) {
    graphemeSegmenter = new Intl.Segmenter(undefined, { granularity: "grapheme" });
  }
  return graphemeSegmenter;
}

export function graphemes(value) {
  const segmenter = ensureGraphemeSegmenter();
  if (!segmenter) return [...String(value || "")];
  return [...segmenter.segment(String(value || ""))].map((item) => item.segment);
}

export function isWideGrapheme(value) {
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

export function displayWidth(value) {
  return graphemes(value).reduce((total, item) => total + (isWideGrapheme(item) ? 2 : 1), 0);
}

export function runeLength(value) {
  return displayWidth(value);
}

export function takeColumns(value, width) {
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

export function padRight(value, width) {
  return String(value || "") + " ".repeat(Math.max(0, width - displayWidth(value)));
}

export function truncateEnd(value, width) {
  if (displayWidth(value) <= width) return String(value || "");
  if (width <= 1) return ".";
  return `${takeColumns(value, width - 1)}.`;
}

export function fit(value, width) {
  return padRight(truncateEnd(value, width), width);
}

function parseLetterSpacing(style) {
  const raw = style?.letterSpacing;
  if (!raw || raw === "normal") return 0;
  const parsed = Number.parseFloat(raw);
  return Number.isFinite(parsed) ? parsed : 0;
}

function ensureAsciiMeasureNode() {
  if (asciiMeasureNode?.isConnected) return asciiMeasureNode;
  asciiMeasureNode = document.createElement("span");
  asciiMeasureNode.className = "ascii-measure";
  asciiMeasureNode.style.position = "absolute";
  asciiMeasureNode.style.visibility = "hidden";
  asciiMeasureNode.style.whiteSpace = "pre";
  asciiMeasureNode.style.pointerEvents = "none";
  asciiMeasureNode.style.inset = "-9999px auto auto -9999px";
  (document.body || document.documentElement).append(asciiMeasureNode);
  return asciiMeasureNode;
}

function fontMetricsCacheKey(style) {
  return [
    style.font,
    style.letterSpacing,
    style.fontKerning,
    style.fontFeatureSettings,
    style.fontVariantLigatures,
  ].join("|");
}

export function measureGlyphMetrics(pre) {
  if (typeof HTMLElement === "undefined" || typeof getComputedStyle !== "function" || !(pre instanceof HTMLElement)) {
    return {
      asciiWidth: 8,
      cjkWidth: 16,
      useDoubleWideCells: true,
    };
  }
  const style = getComputedStyle(pre);
  const key = fontMetricsCacheKey(style);
  let metrics = fontMetricsCache.get(key);
  if (!metrics) {
    const measure = ensureAsciiMeasureNode();
    measure.style.font = style.font;
    measure.style.letterSpacing = style.letterSpacing;
    measure.style.fontKerning = style.fontKerning;
    measure.style.fontFeatureSettings = style.fontFeatureSettings;
    measure.style.fontVariantLigatures = style.fontVariantLigatures;
    measure.textContent = "0000000000";
    const asciiWidth = measure.getBoundingClientRect().width / 10;
    measure.textContent = "漢漢漢漢漢";
    const cjkWidth = measure.getBoundingClientRect().width / 5;
    metrics = {
      asciiWidth,
      cjkWidth,
      useDoubleWideCells: cjkWidth >= asciiWidth * 1.5,
    };
    fontMetricsCache.set(key, metrics);
  }
  useDoubleWideCells = metrics.useDoubleWideCells;
  return metrics;
}

export function prepareAsciiFont(pre) {
  if (typeof HTMLElement === "undefined" || !(pre instanceof HTMLElement)) {
    const fallbackFont = typeof getComputedStyle === "function" && document?.body
      ? getComputedStyle(document.body).font || "14px monospace"
      : "14px monospace";
    return {
      font: fallbackFont,
      letterSpacing: 0,
      lineHeight: 18,
      cellWidth: 8,
      color: typeof getComputedStyle === "function" && document?.body
        ? getComputedStyle(document.body).color || "#111"
        : "#111",
      useDoubleWideCells: true,
    };
  }
  const style = getComputedStyle(pre);
  const metrics = measureGlyphMetrics(pre);
  return {
    font: style.font || `${style.fontSize} monospace`,
    letterSpacing: parseLetterSpacing(style),
    lineHeight: parseFloat(style.lineHeight) || parseFloat(style.fontSize) * 1.25 || 18,
    cellWidth: metrics.asciiWidth || parseFloat(style.fontSize) * 0.6 || 8,
    color: style.color || getComputedStyle(document.body).color || "#111",
    useDoubleWideCells: metrics.useDoubleWideCells,
  };
}

export function measureCellWidthFromPre(pre, fontMeta = null) {
  const meta = fontMeta || prepareAsciiFont(pre);
  if (Number.isFinite(meta.cellWidth) && meta.cellWidth > 0) return meta.cellWidth;
  if (!(pre instanceof HTMLElement)) return 8;
  const canvas = document.createElement("canvas");
  const context = canvas.getContext("2d");
  if (!context) return 8;
  context.font = meta.font;
  const measured = context.measureText("0").width;
  return Number.isFinite(measured) && measured > 0 ? measured : meta.lineHeight * 0.6;
}

function preparedCacheKey(text, fontMeta) {
  return `${fontMeta.font}|${fontMeta.letterSpacing}|${text}`;
}

function getPreparedText(text, fontMeta) {
  const key = preparedCacheKey(text, fontMeta);
  let prepared = preparedTextCache.get(key);
  if (!prepared) {
    prepared = prepareWithSegments(String(text || ""), fontMeta.font, {
      whiteSpace: "pre-wrap",
      letterSpacing: fontMeta.letterSpacing || 0,
    });
    preparedTextCache.set(key, prepared);
  }
  return prepared;
}

function wrapTextFallback(text, width) {
  const out = [];
  const paragraphs = String(text || "").trim().split("\n");
  paragraphs.forEach((paragraph) => {
    const words = paragraph.split(/\s+/).filter(Boolean);
    let line = "";
    words.forEach((word) => {
      if (!line) {
        if (displayWidth(word) > width) {
          let rest = word;
          while (rest) {
            const part = takeColumns(rest, width);
            if (!part) break;
            out.push(part);
            rest = rest.slice(part.length);
          }
        } else {
          line = word;
        }
      } else if (displayWidth(line) + 1 + displayWidth(word) <= width) {
        line += ` ${word}`;
      } else {
        out.push(line);
        line = word;
      }
    });
    if (line) out.push(line);
    if (!words.length) out.push("");
  });
  return out.length ? out : [""];
}

/**
 * Wrap plain body text into fixed-width monospace column lines using Pretext.
 * Returns string[] suitable for ASCII box rows.
 */
export function wrapAsciiBody(text, columns, fontMeta = null) {
  const width = Math.max(1, Number(columns) || 1);
  if (!fontMeta?.font) return wrapTextFallback(text, width);
  try {
    const prepared = getPreparedText(text, fontMeta);
    const maxWidthPx = Math.max(1, width * fontMeta.cellWidth);
    const { lines } = layoutWithLines(prepared, maxWidthPx, fontMeta.lineHeight || 18);
    if (!lines?.length) return wrapTextFallback(text, width);
    return lines.map((line) => takeColumns(line.text, width));
  } catch {
    return wrapTextFallback(text, width);
  }
}

export function layoutGlyphsFromLines(lines, meta, noteID, role, rowOffset, layout) {
  const roleOccurrences = layout.roleOccurrences || new Map();
  const noteOccurrences = layout.noteOccurrences || new Map();
  layout.roleOccurrences = roleOccurrences;
  layout.noteOccurrences = noteOccurrences;
  lines.forEach((line, row) => {
    let col = 0;
    for (const char of graphemes(line)) {
      if (char !== " ") {
        const roleBase = `${noteID}:${role}:${char}`;
        const roleCount = roleOccurrences.get(roleBase) || 0;
        roleOccurrences.set(roleBase, roleCount + 1);
        const noteBase = `${noteID}:${char}`;
        const noteCount = noteOccurrences.get(noteBase) || 0;
        noteOccurrences.set(noteBase, noteCount + 1);
        layout.glyphs.push({
          char,
          noteID,
          role,
          x: meta.x + col * meta.cellWidth,
          y: meta.y + (rowOffset + row) * meta.lineHeight,
          key: `${roleBase}:${roleCount}`,
          semanticKey: `${noteBase}:${noteCount}`,
        });
      }
      col += displayWidth(char);
    }
  });
  return rowOffset + lines.length + 1;
}

export function columnsFromPixelWidth(widthPx, cellWidth) {
  const cell = Number.isFinite(cellWidth) && cellWidth > 0 ? cellWidth : 8;
  return Math.max(32, Math.min(96, Math.floor(Number(widthPx) / cell)));
}

export function clearAsciiLayoutCache() {
  preparedTextCache.clear();
  fontMetricsCache.clear();
}
