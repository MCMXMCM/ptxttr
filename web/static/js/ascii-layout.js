export const ASCII_MIN_COLUMNS = 32;
export const ASCII_MAX_COLUMNS = 160;

export function clampAsciiColumns(columns) {
  if (!Number.isFinite(columns)) return 0;
  return Math.max(ASCII_MIN_COLUMNS, Math.min(ASCII_MAX_COLUMNS, Math.floor(columns)));
}

export function columnsFromWidth(widthPx, cellWidthPx) {
  if (!Number.isFinite(widthPx) || widthPx <= 0) return 0;
  if (!Number.isFinite(cellWidthPx) || cellWidthPx <= 0) return 0;
  return clampAsciiColumns(widthPx / cellWidthPx);
}

export function buildAsciiRenderCacheKey(columns, mobileActions, imageMode) {
  if (!columns) return "";
  return `${columns}:${mobileActions ? "1" : "0"}:${imageMode ? "1" : "0"}`;
}

export function shouldRenderAscii(previousLayoutKey, nextLayoutKey, dirty = false) {
  if (!nextLayoutKey) return false;
  return dirty || previousLayoutKey !== nextLayoutKey;
}
