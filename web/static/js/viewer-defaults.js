import { prefUnset } from "./prefs-utils.js";

/** Logged-out WoT defaults (shared by session transport and sort-prefs UI). */
export const DEFAULT_LOGGED_OUT_WOT_DEPTH = 3;
export const DEFAULT_LOGGED_OUT_WOT_SEED_NPUB =
  "npub1sg6plzptd64u62a878hep2kev88swjh3tw00gjsfl8f237lmu63q0uf63m";

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

/** Writes default viewer prefs when keys are unset (logged-out WoT on, media on). */
export function applyDefaultViewerPrefsIfUnset() {
  if (prefUnset(IMAGE_MODE_KEY)) {
    localStorage.setItem(IMAGE_MODE_KEY, "1");
  }
  if (hasViewerPubkey()) return;
  if (prefUnset(WEB_OF_TRUST_ENABLED_KEY)) {
    localStorage.setItem(WEB_OF_TRUST_ENABLED_KEY, "1");
  }
  if (prefUnset(WEB_OF_TRUST_DEPTH_KEY)) {
    localStorage.setItem(WEB_OF_TRUST_DEPTH_KEY, String(DEFAULT_LOGGED_OUT_WOT_DEPTH));
  }
  if (prefUnset(WEB_OF_TRUST_SEED_KEY)) {
    localStorage.setItem(WEB_OF_TRUST_SEED_KEY, DEFAULT_LOGGED_OUT_WOT_SEED_NPUB);
  }
}
