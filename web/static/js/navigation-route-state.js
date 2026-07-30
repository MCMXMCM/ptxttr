import { routeKind } from "./nav-routing.js";
import { createStore } from "./store.js";

function initialRoute() {
  try {
    return routeKind(globalThis.window?.location?.pathname || "");
  } catch {
    return "";
  }
}

function normalizeURL(input) {
  try {
    return new URL(input, globalThis.window?.location?.origin || "https://example.com");
  } catch {
    return new URL("https://example.com/");
  }
}

export function createNavigationRouteState(seedRoute = initialRoute()) {
  const store = createStore({
    currentRoute: seedRoute,
    refreshGeneration: 0,
    activeNavigationID: 0,
  });
  let pendingWarmupCancel = null;

  function getCurrentRoute() {
    return store.getState().currentRoute;
  }

  function setCurrentRoute(route) {
    store.setState({ currentRoute: String(route || "") });
    return getCurrentRoute();
  }

  function nextRefreshToken(route, urlLike) {
    const refreshGeneration = Number(store.getState().refreshGeneration || 0) + 1;
    store.setState({ refreshGeneration });
    const url = normalizeURL(urlLike);
    return `${route}:${refreshGeneration}:${url.pathname}${url.search}`;
  }

  function beginNavigation() {
    const activeNavigationID = Number(store.getState().activeNavigationID || 0) + 1;
    store.setState({ activeNavigationID });
    return activeNavigationID;
  }

  function navigationRequestIsCurrent(navigationID) {
    return Number(navigationID) > 0 && Number(store.getState().activeNavigationID || 0) === Number(navigationID);
  }

  function enqueueNavigation(work) {
    if (typeof work !== "function") return Promise.resolve();
    return Promise.resolve().then(work);
  }

  function setPendingWarmupCanceler(cancel) {
    pendingWarmupCancel = typeof cancel === "function" ? cancel : null;
  }

  function clearPendingWarmups() {
    if (!pendingWarmupCancel) return false;
    pendingWarmupCancel();
    pendingWarmupCancel = null;
    return true;
  }

  function clearPendingWarmupCanceler(expected = null) {
    if (!expected || pendingWarmupCancel === expected) pendingWarmupCancel = null;
  }

  return {
    store,
    getCurrentRoute,
    setCurrentRoute,
    nextRefreshToken,
    beginNavigation,
    navigationRequestIsCurrent,
    enqueueNavigation,
    setPendingWarmupCanceler,
    clearPendingWarmups,
    clearPendingWarmupCanceler,
  };
}

const defaultRouteState = createNavigationRouteState();

export const navigationRouteStore = defaultRouteState.store;
export const getCurrentRoute = defaultRouteState.getCurrentRoute;
export const setCurrentRoute = defaultRouteState.setCurrentRoute;
export const nextRouteRefreshToken = defaultRouteState.nextRefreshToken;
export const beginNavigation = defaultRouteState.beginNavigation;
export const navigationRequestIsCurrent = defaultRouteState.navigationRequestIsCurrent;
export const enqueueNavigation = defaultRouteState.enqueueNavigation;
export const setPendingRouteWarmupCanceler = defaultRouteState.setPendingWarmupCanceler;
export const clearPendingRouteWarmups = defaultRouteState.clearPendingWarmups;
export const clearPendingRouteWarmupCanceler = defaultRouteState.clearPendingWarmupCanceler;
