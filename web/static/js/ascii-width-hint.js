export const ASCII_WIDTH_COOKIE_NAME = "ptxt_ascii_w";
export const ASCII_WIDTH_DESKTOP_COOKIE_NAME = "ptxt_ascii_w_desktop";

export function asciiWidthCookieNameForViewport(viewportWidth) {
  return Number(viewportWidth) <= 1023
    ? ASCII_WIDTH_COOKIE_NAME
    : ASCII_WIDTH_DESKTOP_COOKIE_NAME;
}

/**
 * Fallback column counts must match internal/httpx/service.go (45 / 64 / 52).
 * Once ascii.js has measured the live center column, its cookie takes priority
 * at every breakpoint.
 */
export function asciiWidthHintForFetch(pathname) {
  const measured = asciiWidthFromCookie(
    document.cookie,
    asciiWidthCookieNameForViewport(window.innerWidth),
  );
  if (measured) return measured;
  const vw = window.innerWidth;
  let w;
  if (vw <= 700) {
    w = 45;
  } else if (vw <= 1023) {
    w = 64;
  } else {
    w = 52;
  }
  return w;
}

export function asciiWidthFromCookie(cookieString, cookieName = ASCII_WIDTH_COOKIE_NAME) {
  const prefix = `${cookieName}=`;
  const token = String(cookieString || "")
    .split(";")
    .map((part) => part.trim())
    .find((part) => part.startsWith(prefix));
  if (!token) return 0;
  const width = Number.parseInt(token.slice(prefix.length), 10);
  return Number.isFinite(width) && width >= 32 && width <= 160 ? width : 0;
}

export function asciiWidthCookie(columns, secure = false, cookieName = ASCII_WIDTH_COOKIE_NAME) {
  const width = Math.floor(Number(columns));
  if (!Number.isFinite(width) || width < 32 || width > 160) return "";
  return `${cookieName}=${width}; Path=/; Max-Age=31536000; SameSite=Lax${secure ? "; Secure" : ""}`;
}

export function addAsciiWidthHint(params, pathname) {
  if (!(params instanceof URLSearchParams)) return;
  params.set("ascii_w", String(asciiWidthHintForFetch(pathname)));
}
