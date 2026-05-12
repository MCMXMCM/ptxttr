import { applyRelayParamsToURL } from "./session.js";

export function closestLink(target) {
  if (!(target instanceof Element)) return null;
  return target.closest("a[href]");
}

export function shouldInterceptLink(event, link, main) {
  if (!link || !main) return false;
  if (event.defaultPrevented) return false;
  if (event.button !== 0) return false;
  if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return false;
  const url = new URL(link.href, window.location.origin);
  if (url.origin !== window.location.origin) return false;
  return Boolean(routeKind(url.pathname));
}

export function routeKind(pathname) {
  if (pathname === "/" || pathname === "/feed") return "feed";
  if (pathname === "/reads") return "reads";
  if (pathname === "/bookmarks") return "bookmarks";
  if (pathname === "/search") return "search";
  if (pathname.startsWith("/tag/")) return "tag";
  if (pathname.startsWith("/u/")) return "profile";
  if (pathname.startsWith("/thread/")) return "thread";
  if (pathname === "/relays") return "relays";
  if (pathname === "/notifications") return "notifications";
  if (pathname === "/settings" || pathname === "/about" || pathname === "/profile/edit") {
    return "stub";
  }
  return "";
}

export function withRelays(href) {
  const url = new URL(href, window.location.origin);
  applyRelayParamsToURL(url);
  return `${url.pathname}${url.search}${url.hash}`;
}
