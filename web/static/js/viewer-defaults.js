import { prefUnset } from "./prefs-utils.js";

/** Logged-out WoT defaults (shared by session transport and sort-prefs UI). */
// Keep this aligned with internal/httpx.defaultLoggedOutWOTDepth. The first
// document request cannot carry browser-local preferences, so a different
// client default makes the SSR cursor invalid as soon as fetchWithSession()
// starts sending X-Ptxt-Wot-Depth on pagination and fragment requests.
export const DEFAULT_LOGGED_OUT_WOT_DEPTH = 1;
export const DEFAULT_LOGGED_OUT_THREAD_WOT_DEPTH = 3;
export const DEFAULT_LOGGED_OUT_WOT_SEED_NPUB =
  "npub1dergggklka99wwrs92yz8wdjs952h2ux2ha2ed598ngwu9w7a6fsh9xzpc";

const IMAGE_MODE_KEY = "ptxt_image_mode";
const WEB_OF_TRUST_ENABLED_KEY = "ptxt_wot_enabled";
const WEB_OF_TRUST_DEPTH_KEY = "ptxt_wot_depth";
const WEB_OF_TRUST_SEED_KEY = "ptxt_wot_seed_pubkey";
const SESSION_KEY = "ptxt_nostr_session";

function hasViewerPubkey() {
  try {
    const raw = localStorage.getItem(SESSION_KEY);
    if (!raw) return false;
    const data = JSON.parse(raw);
    return Boolean(String(data?.pubkey || "").trim());
  } catch {
    return false;
  }
}

export function desktopModeEnabled() {
  return globalThis.document?.documentElement?.dataset?.ptxtDesktopMode === "1";
}

export function loggedOutWebOfTrustDepthPref() {
  if (!desktopModeEnabled()) return DEFAULT_LOGGED_OUT_WOT_DEPTH;
  try {
    const raw = Number.parseInt(localStorage.getItem(WEB_OF_TRUST_DEPTH_KEY) || "", 10);
    if (!Number.isFinite(raw)) return DEFAULT_LOGGED_OUT_WOT_DEPTH;
    return Math.min(3, Math.max(1, raw));
  } catch {
    return DEFAULT_LOGGED_OUT_WOT_DEPTH;
  }
}

/** Writes default viewer prefs when keys are unset (logged-out WoT on, media on). */
export function applyDefaultViewerPrefsIfUnset() {
  if (prefUnset(IMAGE_MODE_KEY)) {
    localStorage.setItem(IMAGE_MODE_KEY, "1");
  }
  if (hasViewerPubkey()) return;
  if (prefUnset(WEB_OF_TRUST_ENABLED_KEY)) {
    localStorage.setItem(WEB_OF_TRUST_ENABLED_KEY, "1");
  }
  if (!desktopModeEnabled() || prefUnset(WEB_OF_TRUST_DEPTH_KEY)) {
    localStorage.setItem(WEB_OF_TRUST_DEPTH_KEY, String(DEFAULT_LOGGED_OUT_WOT_DEPTH));
  }
  localStorage.removeItem(WEB_OF_TRUST_SEED_KEY);
}
