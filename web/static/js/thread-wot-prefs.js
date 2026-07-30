/** Thread WoT follows the global on/off preference; guest depth is thread-specific. */

import { getWebOfTrustEnabledPref } from "./sort-prefs.js";
import { DEFAULT_LOGGED_OUT_THREAD_WOT_DEPTH } from "./viewer-defaults.js";

export { threadPathNoteID } from "./thread-hydrate.js";

export function isThreadWoTEnabledForFocus() {
  return getWebOfTrustEnabledPref();
}

export function threadWoTDepthForViewer(viewerPubkey, configuredDepth) {
  if (!String(viewerPubkey || "").trim()) return DEFAULT_LOGGED_OUT_THREAD_WOT_DEPTH;
  return Math.min(3, Math.max(1, Number(configuredDepth) || 1));
}
