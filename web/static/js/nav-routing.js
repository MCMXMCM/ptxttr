import { applyRelayParamsToURL } from "./session.js";
import { pubkeyFromProfilePath } from "./relay-utils.js";

export { pubkeyFromProfilePath };

export function routeKind(pathname) {
  if (pathname === "/" || pathname === "/feed") return "feed";
  if (pathname === "/reads") return "reads";
  if (pathname.startsWith("/reads/")) return "read";
  if (pathname === "/bookmarks") return "bookmarks";
  if (pathname === "/search") return "search";
  if (pathname.startsWith("/tag/")) return "tag";
  if (pathname.startsWith("/u/")) return "profile";
  if (pathname.startsWith("/thread/")) return "thread";
  if (pathname === "/relays") return "relays";
  if (pathname === "/notifications") return "notifications";
  if (
    pathname === "/login" ||
    pathname === "/settings" ||
    pathname === "/about" ||
    pathname === "/profile/edit" ||
    pathname === "/support" ||
    pathname === "/ios-plain-text-nostr" ||
    pathname === "/terms" ||
    pathname === "/privacy"
  ) {
    return "stub";
  }
  return "";
}

export const isClientRoutePath = (pathname) => Boolean(routeKind(pathname));

export function withRelays(href) {
  const url = new URL(href, window.location.origin);
  applyRelayParamsToURL(url);
  return `${url.pathname}${url.search}${url.hash}`;
}
