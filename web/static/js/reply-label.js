/** Decimal string for n, left-padded with spaces to at least minLen columns (matches Go asciiDecimalPad). */
export function padAsciiDecimal(n, minLen) {
  const s = String(n);
  if (s.length >= minLen) return s;
  return " ".repeat(minLen - s.length) + s;
}

export function replyLabelForCount(count) {
  if (count <= 0) return "";
  const kind = count === 1 ? "rply" : "rpls";
  return `${padAsciiDecimal(count, 3)} ${kind}`;
}

/** Unpadded reply badge for tight mobile footers (matches semantics of replyLabelForCount). */
export function compactReplyBadge(count) {
  if (count <= 0) return "";
  const kind = count === 1 ? "rply" : "rpls";
  return `${count} ${kind}`;
}
