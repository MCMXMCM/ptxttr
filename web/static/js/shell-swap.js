/**
 * SPA route swaps: replace only `[data-route-outlet]` so mobile bar, menu, and
 * composer stay mounted. Preserves `.rail-user` inside the outlet.
 * Falls back to legacy full-main merge when the outlet is absent (tests / old HTML).
 */

/** @param {HTMLElement | null} navRoot `#app-main[data-nav-root]` */
export function routeOutletElement(navRoot) {
  return navRoot?.querySelector("[data-route-outlet]") ?? null;
}

/**
 * Feed-shell routes use native document scroll (`window`), including mobile.
 * Kept for callers that branch on a scroll root element; always `null` here.
 *
 * @param {HTMLElement | null} navRoot
 * @returns {null}
 */
export function routeScrollRoot(_navRoot) {
  return null;
}

/** @param {HTMLElement | null} navRoot */
export function routeScrollTop(navRoot) {
  void navRoot;
  return window.scrollY;
}

/** @param {HTMLElement | null} navRoot */
export function setRouteScrollTop(navRoot, y) {
  void navRoot;
  window.scrollTo(0, y);
}

/** @param {HTMLElement | null} navRoot */
export function scrollRouteToTop(navRoot) {
  setRouteScrollTop(navRoot, 0);
}

/** HTML string inside `[data-route-outlet]` for snapshotting restores. */
export function routeOutletInnerHTML(navRoot) {
  const outlet = routeOutletElement(navRoot);
  return outlet ? outlet.innerHTML : (navRoot?.innerHTML ?? "");
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
  const existingRailUser = outlet.querySelector(".left-rail .rail-user");
  const stage = document.createElement("div");
  stage.innerHTML = outletHtml;
  if (existingRailUser) {
    const nextRailUser = stage.querySelector(".left-rail .rail-user");
    if (nextRailUser) nextRailUser.replaceWith(existingRailUser);
  }
  outlet.replaceChildren(...Array.from(stage.childNodes));
}

/**
 * Replace outlet markup then apply scroll on the route scroll root (sync + next frame).
 * @param {HTMLElement | null} navRoot
 * @param {string} html
 * @param {number} scrollTop
 */
export function replaceRouteOutletAndScroll(navRoot, html, scrollTop) {
  replaceRouteOutletHTML(navRoot, html);
  const y = Number(scrollTop) || 0;
  setRouteScrollTop(navRoot, y);
  requestAnimationFrame(() => {
    setRouteScrollTop(navRoot, y);
  });
}

/**
 * @deprecated Prefer {@link replaceRouteOutletHTML} when `[data-route-outlet]` exists.
 * Merges mobile bar, menu, and rail user into a full-main HTML string.
 */
export function replaceNavRootHTMLPreservingChrome(navRoot, html) {
  if (!navRoot) return;
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
  if (existingRailUser) {
    const nextRailUser = stage.querySelector(".left-rail .rail-user");
    if (nextRailUser) nextRailUser.replaceWith(existingRailUser);
  }

  navRoot.replaceChildren(...Array.from(stage.childNodes));
}
