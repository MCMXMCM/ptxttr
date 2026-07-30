const HASHTAG_MAX_RUNES = 64;
const HASHTAG_CHAR = /[\p{L}\p{N}_]/u;
const HASHTAG_PATTERN = /(^|\s)#([\p{L}\p{N}_]+)/gu;

function hashtagCharCount(value) {
  let count = 0;
  for (const _ of value) count += 1;
  return count;
}

/** NIP-12 / iOS NostrValidation.normalizeHashtag (letters, numbers, underscore). */
export function normalizeHashtag(value) {
  let trimmed = String(value || "").trim();
  if (trimmed.startsWith("#")) trimmed = trimmed.slice(1);
  if (!trimmed || hashtagCharCount(trimmed) > HASHTAG_MAX_RUNES) return "";
  for (const ch of trimmed) {
    if (!HASHTAG_CHAR.test(ch)) return "";
  }
  return trimmed;
}

/** Extract #hashtags from note body (mirrors iOS NostrValidation.hashtags). */
export function hashtagsInContent(content) {
  const text = String(content || "");
  const out = [];
  const seen = new Set();
  for (const match of text.matchAll(HASHTAG_PATTERN)) {
    const tag = normalizeHashtag(match[2]);
    if (!tag) continue;
    const key = tag.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(tag);
  }
  return out;
}

/** True when event has NIP-12 t tag or body #hashtag (mirrors iOS eventHasHashtag). */
export function eventHasHashtag(event, tag) {
  const normalized = normalizeHashtag(tag);
  if (!normalized) return false;
  const want = normalized.toLowerCase();
  for (const row of event?.tags || []) {
    if (!Array.isArray(row) || row.length < 2 || row[0] !== "t") continue;
    const value = String(row[1] || "").trim();
    if (value && value.toLowerCase() === want) return true;
  }
  return hashtagsInContent(event?.content).some((row) => row.toLowerCase() === want);
}

export function parseTagFromPath(pathname) {
  const path = String(pathname || "");
  if (!path.startsWith("/tag/")) return "";
  const rest = path.slice("/tag/".length);
  if (!rest || rest.includes("/")) return "";
  try {
    return normalizeHashtag(decodeURIComponent(rest));
  } catch {
    return normalizeHashtag(rest);
  }
}

export function tagScopeFromURL(urlLike) {
  const url = new URL(urlLike, window.location.origin);
  const scope = String(url.searchParams.get("scope") || "").trim().toLowerCase();
  return scope === "all" ? "all" : "network";
}

export function tagScopeLabel(scope) {
  return scope === "all" ? "all notes" : "your network";
}
