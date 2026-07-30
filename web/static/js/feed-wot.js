import { normalizePubkey } from "./relay-utils.js";
import { DEFAULT_LOGGED_OUT_WOT_SEED_NPUB } from "./viewer-defaults.js";

/**
 * Pure WoT resolution for tests and feed-service.
 * Mirrors internal/httpx/handlers_feed.go logged-out defaults:
 * WoT on, fixed Gigi seed, configured depth when prefs are unset.
 */
export function resolveFeedWoTFromInputs({
  viewerPubkey = "",
  wotEnabled = true,
  seedPref = "",
  loggedOutDefaultSeed = DEFAULT_LOGGED_OUT_WOT_SEED_NPUB,
  depth = 3,
} = {}) {
  const viewer = normalizePubkey(viewerPubkey);
  const loggedOut = !viewer;
  if (!wotEnabled) {
    return { kind: "firehose" };
  }

  const seedRaw = loggedOut
    ? String(loggedOutDefaultSeed).trim()
    : String(seedPref || viewerPubkey).trim();
  const seed = normalizePubkey(seedRaw);
  if (!seed) {
    return { kind: "empty" };
  }

  return { kind: "wot", seed, depth };
}

export function feedFetchModeFromPrefs(viewerPubkey, prefs = {}) {
  return resolveFeedWoTFromInputs({
    viewerPubkey,
    wotEnabled: prefs.wotEnabled,
    seedPref: prefs.seedPref,
    loggedOutDefaultSeed: prefs.loggedOutDefaultSeed,
    depth: prefs.depth,
  });
}
