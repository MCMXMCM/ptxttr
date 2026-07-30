/** Helpers for the client-hydrated document shell. */

/** @param {HTMLElement | null} navRoot `#app-main[data-nav-root]` */
export function routeOutletElement(navRoot) {
  return navRoot?.querySelector('[data-route-outlet="root"]')
    ?? navRoot?.querySelector("[data-route-outlet]")
    ?? null;
}

function computedOverflowY(element) {
  if (!element || typeof getComputedStyle !== "function") return "";
  try {
    return String(getComputedStyle(element).overflowY || "").toLowerCase();
  } catch {
    return "";
  }
}

function isScrollableElement(element) {
  if (!element) return false;
  const overflowY = computedOverflowY(element);
  if (!/(auto|scroll|overlay)/.test(overflowY)) return false;
  return Number(element.scrollHeight || 0) > Number(element.clientHeight || 0);
}

function isRouteVisibleElement(element) {
  if (!element || typeof element !== "object") return false;
  if (typeof HTMLElement !== "undefined" && !(element instanceof HTMLElement)) return false;
  if (element.hidden || element.getAttribute?.("aria-hidden") === "true") return false;
  if (element.closest?.("[hidden], [aria-hidden='true']")) return false;
  try {
    const style = getComputedStyle(element);
    if (style.display === "none") return false;
    if (style.visibility === "hidden") return false;
  } catch {
    // ignore computed-style lookup failures
  }
  return true;
}

/**
 * Detect the route-owned scroller. Most routes use document scroll, but some
 * embedded layouts can end up with a nested scrolling root inside the outlet.
 *
 * @param {HTMLElement | null} navRoot
 * @returns {HTMLElement | null}
 */
export function routeScrollRoot(navRoot) {
  const outlet = routeOutletElement(navRoot);
  const primary = outlet?.querySelector?.("[data-shell-main], [data-profile-shell], .feed-column") || null;
  const candidates = [
    ...(primary ? [primary] : []),
    ...(outlet?.querySelectorAll?.("[data-shell-main], [data-profile-shell], .feed-column") || []),
    outlet,
    navRoot,
  ];
  for (const candidate of candidates) {
    if (isRouteVisibleElement(candidate) === false && typeof HTMLElement !== "undefined" && candidate instanceof HTMLElement) continue;
    if (isScrollableElement(candidate)) return candidate;
  }
  return null;
}

/** @param {HTMLElement | null} navRoot */
export function routeScrollTop(navRoot) {
  const root = routeScrollRoot(navRoot);
  if (root) {
    const y = Number(root.scrollTop);
    return Number.isFinite(y) && y > 0 ? y : 0;
  }
  const doc = globalThis.document;
  const scrollingElement = doc?.scrollingElement || doc?.documentElement || doc?.body || null;
  const y = Number(globalThis.window?.scrollY);
  if (Number.isFinite(y) && y > 0) return y;
  const elementY = Number(scrollingElement?.scrollTop);
  if (Number.isFinite(elementY) && elementY > 0) return elementY;
  return 0;
}

/** @param {HTMLElement | null} navRoot */
export function setRouteScrollTop(navRoot, y) {
  const nextY = Math.max(0, Number(y) || 0);
  const root = routeScrollRoot(navRoot);
  if (root) {
    root.scrollTop = nextY;
    return;
  }
  const doc = globalThis.document;
  const scrollingElement = doc?.scrollingElement || doc?.documentElement || doc?.body || null;
  if (scrollingElement) scrollingElement.scrollTop = nextY;
  if (doc?.documentElement && doc.documentElement !== scrollingElement) {
    doc.documentElement.scrollTop = nextY;
  }
  if (doc?.body && doc.body !== scrollingElement) {
    doc.body.scrollTop = nextY;
  }
  globalThis.window?.scrollTo?.(0, nextY);
}

/** @param {HTMLElement | null} navRoot */
export function scrollRouteToTop(navRoot) {
  setRouteScrollTop(navRoot, 0);
}

/**
 * @param {HTMLElement | null} navRoot
 * @param {string} outletHtml
 */
export function replaceRouteOutletHTML(navRoot, outletHtml) {
  if (!navRoot) return;
  const outlet = routeOutletElement(navRoot);
  if (!outlet) {
    replaceNavRootHTMLPreservingChrome(navRoot, outletHtml);
    return;
  }
  const existingLeftRail = outlet.querySelector(".left-rail");
  const existingRailUser = outlet.querySelector(".left-rail .rail-user");
  const stage = document.createElement("div");
  stage.innerHTML = outletHtml;
  const nextLeftRail = stage.querySelector(".left-rail");
  if (existingLeftRail && nextLeftRail) {
    syncLeftRailActiveState(existingLeftRail, nextLeftRail);
    nextLeftRail.replaceWith(existingLeftRail);
  } else if (existingRailUser) {
    const nextRailUser = stage.querySelector(".left-rail .rail-user");
    if (nextRailUser) nextRailUser.replaceWith(existingRailUser);
  }
  outlet.replaceChildren(...Array.from(stage.childNodes));
}

function syncLeftRailActiveState(currentShell, nextShell) {
  const currentLinks = leftRailScopedQueryAll(currentShell, ".left-rail a[href]");
  const nextActiveHrefs = new Set(
    leftRailScopedQueryAll(nextShell, ".left-rail a[aria-current='page']")
      .map((link) => link.getAttribute?.("href") || "")
      .filter(Boolean),
  );
  currentLinks.forEach((link) => {
    const href = link.getAttribute?.("href") || "";
    if (nextActiveHrefs.has(href)) link.setAttribute?.("aria-current", "page");
    else link.removeAttribute?.("aria-current");
  });
}

function leftRailScopedQueryAll(root, selector) {
  if (!root?.querySelectorAll) return [];
  const direct = Array.from(root.querySelectorAll(selector) || []);
  if (direct.length || !selector.startsWith(".left-rail ")) return direct;
  return Array.from(root.querySelectorAll(selector.slice(".left-rail ".length)) || []);
}

function replaceNavRootHTMLPreservingChrome(navRoot, html) {
  if (!navRoot) return;
  const existingLeftRail = navRoot.querySelector(".left-rail");
  const existingRailUser = navRoot.querySelector(".left-rail .rail-user");
  const existingMobileBar = navRoot.querySelector(".mobile-bar");
  const existingMobileMenu = navRoot.querySelector("[data-mobile-menu]");

  const stage = document.createElement("div");
  stage.innerHTML = html;

  if (existingMobileBar) {
    const nextMobileBar = stage.querySelector(".mobile-bar");
    if (nextMobileBar) nextMobileBar.replaceWith(existingMobileBar);
  }
  if (existingMobileMenu) {
    const nextMobileMenu = stage.querySelector("[data-mobile-menu]");
    if (nextMobileMenu) nextMobileMenu.replaceWith(existingMobileMenu);
  }
  const nextLeftRail = stage.querySelector(".left-rail");
  if (existingLeftRail && nextLeftRail) {
    syncLeftRailActiveState(existingLeftRail, nextLeftRail);
    nextLeftRail.replaceWith(existingLeftRail);
  } else if (existingRailUser) {
    const nextRailUser = stage.querySelector(".left-rail .rail-user");
    if (nextRailUser) nextRailUser.replaceWith(existingRailUser);
  }

  navRoot.replaceChildren(...Array.from(stage.childNodes));
}
