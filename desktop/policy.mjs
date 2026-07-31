export const PRODUCT_NAME = "Plain Text Nostr";
export const BUNDLE_ID = "com.ptxttr.desktop";
export const DEFAULT_PORT = 24787;
export const TAB_GROUP_ID = "com.ptxttr.desktop.tabs";

export function loopbackOrigin(port = DEFAULT_PORT) {
  return `http://127.0.0.1:${port}`;
}

export function classifyNavigation(rawURL, origin) {
  let parsed;
  try {
    parsed = new URL(rawURL, origin);
  } catch {
    return { kind: "blocked", url: "" };
  }
  if (parsed.origin === origin && (parsed.protocol === "http:" || parsed.protocol === "https:")) {
    return { kind: "internal", url: parsed.toString() };
  }
  if ((parsed.protocol === "http:" || parsed.protocol === "https:") && parsed.hostname) {
    return { kind: "external", url: parsed.toString() };
  }
  if (parsed.protocol === "ptxt-action:") {
    return { kind: "action", url: parsed.toString(), action: parsed.hostname || parsed.pathname.slice(1) };
  }
  return { kind: "blocked", url: parsed.toString() };
}

export function swipeNavigation(direction, history) {
  if (direction === "right" && history?.canGoBack?.()) {
    history.goBack();
    return "back";
  }
  if (direction === "left" && history?.canGoForward?.()) {
    history.goForward();
    return "forward";
  }
  return "none";
}

export function shouldOpenInBackground(disposition) {
  return disposition === "background-tab";
}
