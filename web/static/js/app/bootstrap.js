const DEFAULT_ROUTE = Object.freeze({
  path: "/",
  search: "",
  hash: "",
  route: "",
  isCrawler: false,
});

const DEFAULT_BOOTSTRAP = Object.freeze({
  version: 1,
  assetBasePath: "/static",
  route: DEFAULT_ROUTE,
  viewer: {
    pubkey: "",
    seedPubkey: "",
    relays: [],
  },
  features: {
    documentNavigation: true,
    indexedDb: true,
    browserWrites: true,
    directRelayReads: false,
    relayNativeRoutesPrimary: false,
    sharePreviewWarm: true,
    crawlerPreviewSSR: false,
    aboutSSR: true,
  },
  guest: {
    defaultWOTSeedNpub: "",
    defaultWOTDepth: 0,
  },
  initialProfile: null,
});

let cachedBootstrap = null;

function parseJSONScript(id) {
  const element = globalThis.document?.getElementById?.(id);
  const raw = String(element?.textContent || "").trim();
  if (!raw) return null;
  try {
    return JSON.parse(raw);
  } catch {
    return null;
  }
}

function desktopDocumentMode() {
  return globalThis.document?.documentElement?.dataset?.ptxtDesktopMode === "1";
}

function normalizeRoute(value = {}) {
  return {
    path: String(value.path || DEFAULT_ROUTE.path),
    search: String(value.search || DEFAULT_ROUTE.search),
    hash: String(value.hash || DEFAULT_ROUTE.hash),
    route: String(value.route || DEFAULT_ROUTE.route),
    isCrawler: Boolean(value.isCrawler),
  };
}

function normalizeBootstrap(value = {}) {
  const route = normalizeRoute(value.route);
  return {
    version: Number(value.version) || DEFAULT_BOOTSTRAP.version,
    assetBasePath: String(value.assetBasePath || DEFAULT_BOOTSTRAP.assetBasePath),
    route,
    viewer: {
      pubkey: String(value.viewer?.pubkey || ""),
      seedPubkey: String(value.viewer?.seedPubkey || ""),
      relays: Array.isArray(value.viewer?.relays)
        ? value.viewer.relays.map((relay) => String(relay || "").trim()).filter(Boolean)
        : [],
    },
    features: {
      ...DEFAULT_BOOTSTRAP.features,
      ...(value.features && typeof value.features === "object" ? value.features : {}),
    },
    guest: {
      defaultWOTSeedNpub: String(value.guest?.defaultWOTSeedNpub || ""),
      defaultWOTDepth: Number(value.guest?.defaultWOTDepth) || 0,
    },
    initialProfile: value.initialProfile && typeof value.initialProfile === "object"
      ? {
        pubkey: String(value.initialProfile.pubkey || "").trim().toLowerCase(),
        name: String(value.initialProfile.name || "").trim(),
        display_name: String(value.initialProfile.display_name || "").trim(),
        about: String(value.initialProfile.about || "").trim(),
        picture: String(value.initialProfile.picture || "").trim(),
        website: String(value.initialProfile.website || "").trim(),
        nip05: String(value.initialProfile.nip05 || "").trim(),
        lud16: String(value.initialProfile.lud16 || "").trim(),
        lud06: String(value.initialProfile.lud06 || "").trim(),
        event_id: String(value.initialProfile.event_id || "").trim(),
        created_at: Number(value.initialProfile.created_at || 0) || 0,
        relay_hints: Array.isArray(value.initialProfile.relay_hints)
          ? value.initialProfile.relay_hints.map((relay) => String(relay || "").trim()).filter(Boolean)
          : [],
      }
      : null,
  };
}

export function readRouteContext() {
  return normalizeRoute(parseJSONScript("ptxt-route-context") || DEFAULT_ROUTE);
}

export function readAppBootstrap() {
  const embedded = parseJSONScript("ptxt-app-bootstrap");
  if (embedded) return normalizeBootstrap(embedded);
  return normalizeBootstrap({
    ...DEFAULT_BOOTSTRAP,
    route: readRouteContext(),
    features: {
      ...DEFAULT_BOOTSTRAP.features,
      directRelayReads: desktopDocumentMode(),
      relayNativeRoutesPrimary: desktopDocumentMode(),
    },
  });
}

export function initializeAppBootstrap() {
  cachedBootstrap = readAppBootstrap();
  globalThis.__ptxtAppBootstrap = cachedBootstrap;
  return cachedBootstrap;
}

export function appBootstrap() {
  if (!cachedBootstrap) return initializeAppBootstrap();
  return cachedBootstrap;
}

export function appRouteContext() {
  return appBootstrap().route;
}

export function currentRouteURL() {
  const route = appRouteContext();
  return `${route.path || "/"}${route.search ? `?${route.search}` : ""}${route.hash ? `#${route.hash}` : ""}`;
}

export function setAppBootstrapForTests(value) {
  cachedBootstrap = normalizeBootstrap(value || DEFAULT_BOOTSTRAP);
  globalThis.__ptxtAppBootstrap = cachedBootstrap;
}
