export function isIOSWebKit() {
  const nav = globalThis.navigator || {};
  const ua = String(nav.userAgent || "");
  const platform = String(nav.platform || "");
  const maxTouchPoints = Number(nav.maxTouchPoints) || 0;
  const isiOS =
    /iPad|iPhone|iPod/.test(platform) ||
    (platform === "MacIntel" && maxTouchPoints > 1);
  if (!isiOS) return false;
  return /WebKit/i.test(ua) && !/(CriOS|FxiOS|EdgiOS|OPiOS)/i.test(ua);
}

export function isSafariWebKit() {
  const ua = String(globalThis.navigator?.userAgent || "");
  if (!ua) return false;
  if (!/AppleWebKit/i.test(ua)) return false;
  if (/(Chrome|Chromium|CriOS|Edg|EdgiOS|FxiOS|Firefox|OPR|Opera|DuckDuckGo)/i.test(ua)) {
    return false;
  }
  return /Safari/i.test(ua);
}
