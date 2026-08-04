export function shouldHydrateClientRoute({
  route = "",
  features = {},
  relayNativeOverride = false,
  serverRenderedInitialRoute = false,
  serverRendered = false,
} = {}) {
  if (!route || route === "stub") return false;

  // Desktop profile documents can arrive without the viewer header and are
  // therefore cache-only shells. Their client hydration still uses the local
  // sidecar (and its SQLite store), not a second browser relay/event store.
  const desktopProfile = route === "profile" && features?.localFirst === true;
  const fallbackEnabled = relayNativeOverride || features?.directRelayReads === true;
  if (!desktopProfile && !fallbackEnabled) return false;

  return desktopProfile || relayNativeOverride || (!serverRenderedInitialRoute && !serverRendered);
}
