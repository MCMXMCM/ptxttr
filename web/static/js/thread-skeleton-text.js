const MIN_COLUMNS = 32;
const MAX_COLUMNS = 160;

function runeLength(value) {
  return [...String(value || "")].length;
}

function repeat(char, count) {
  return String(char || "").repeat(Math.max(1, count));
}

function padRight(value, width) {
  return value + " ".repeat(Math.max(0, width - runeLength(value)));
}

function fillToWidth(pattern, cols) {
  if (cols < 1 || !pattern) return "";
  let out = "";
  while (runeLength(out) < cols) out += pattern;
  return [...out].slice(0, cols).join("");
}

function replyTextWidth(width) {
  const w = width - 8;
  return w < 20 ? 20 : w;
}

function clampWidth(width) {
  return Math.max(MIN_COLUMNS, Math.min(MAX_COLUMNS, width || MIN_COLUMNS));
}

export function buildThreadReplySkeletonText(width, { isLast = false } = {}) {
  const w = clampWidth(width);
  const contentPrefix = isLast ? "     " : "|    ";
  const headerPrefix = "     ░░░░░░░░ -- ░░░░░ ";
  const headerSuffix = "[...]";
  const headerRule = repeat("-", Math.max(1, w - runeLength(headerPrefix) - runeLength(headerSuffix)));
  const contentWidth = Math.max(1, replyTextWidth(w));
  const contentRow = `${contentPrefix}${padRight(fillToWidth("░", contentWidth), contentWidth)}`;
  const footerPrefix = `${contentPrefix}[Δ] ░ [∇] `;
  const footerSuffix = " [reply] ---+";
  const footerRule = repeat("-", Math.max(1, w - runeLength(footerPrefix) - runeLength(footerSuffix)));
  const lines = [
    `${headerPrefix}${headerRule}${headerSuffix}`,
    contentRow,
    contentRow,
    `${footerPrefix}${footerRule}${footerSuffix}`,
  ];
  if (!isLast) lines.push("|");
  return lines.join("\n");
}

export function buildThreadParentSkeletonText(width) {
  const w = clampWidth(width);
  // The web parent skeleton has a real absolutely-positioned avatar. Three
  // leading columns place the author placeholder one column beyond its right
  // edge; the five-column iOS offset assumes no separate avatar element.
  const headerPrefix = "   ░░░░░░░░ -- ░░░░░ ";
  const headerSuffix = "[...]";
  const headerRule = repeat("-", Math.max(1, w - runeLength(headerPrefix) - runeLength(headerSuffix)));
  const contentPrefix = "|    ";
  const contentWidth = Math.max(1, replyTextWidth(w));
  const contentRow = `${contentPrefix}${padRight(fillToWidth("░", contentWidth), contentWidth)}`;
  const footerPrefix = `${contentPrefix}--- ░░░ `;
  const footerSuffix = " ---+";
  const footerRule = repeat("-", Math.max(1, w - runeLength(footerPrefix) - runeLength(footerSuffix)));
  return [
    `${headerPrefix}${headerRule}${headerSuffix}`,
    contentRow,
    contentRow,
    contentRow,
    `${footerPrefix}${footerRule}${footerSuffix}`,
  ].join("\n");
}

export function buildThreadSelectedSkeletonText(width) {
  const w = clampWidth(width);
  const headerPrefix = "     ░░░░░░░░ -- ░░░░░ ";
  const headerSuffix = "[...]+";
  const headerRule = repeat("-", Math.max(1, w - runeLength(headerPrefix) - runeLength(headerSuffix)));
  const contentWidth = Math.max(1, w - 4);
  const contentRow = `| ${padRight(fillToWidth("░", contentWidth), contentWidth)} |`;
  const footerPrefix = "--- ░░░ ";
  const footerSuffix = " ---+";
  const footerRule = repeat("-", Math.max(1, w - runeLength(footerPrefix) - runeLength(footerSuffix)));
  return [
    `${headerPrefix}${headerRule}${headerSuffix}`,
    contentRow,
    contentRow,
    `${footerPrefix}${footerRule}${footerSuffix}`,
  ].join("\n");
}
