import { resolveFeedFetchModeForViewer } from "./feed-service.js";
import { peekWebOfTrustDiskMembership } from "./wot-service.js";
import { canonicalHex64 } from "./relay-utils.js";
import { bundleHasVisibleThreadReplies } from "./thread-view.js";
import { isThreadWoTEnabledForFocus } from "./thread-wot-prefs.js";
import { peekThreadWarmBundle } from "./thread-graph.js";

export { bundleHasVisibleThreadReplies } from "./thread-view.js";

let relayNativeBundleForPath = () => null;

/** Wired from client-render so same-thread relay-native state can inform skeletons. */
export function bindRelayNativeThreadBundleSource(fn) {
  relayNativeBundleForPath = typeof fn === "function" ? fn : () => null;
}

/** Best-effort sync check before thread hydrate finishes. Defaults to false when unknown. */
export function peekExpectedThreadReplies(pathNoteID, viewerPubkey = "") {
  if (!pathNoteID) return false;
  const bundle = relayNativeBundleForPath(pathNoteID) || peekThreadWarmBundle(pathNoteID);
  if (!bundle) return false;

  const rootID = canonicalHex64(bundle.rootID || bundle.root?.id);
  const selectedID = canonicalHex64(pathNoteID);
  const wotEnabled = isThreadWoTEnabledForFocus();
  let membership = null;
  if (wotEnabled) {
    const mode = resolveFeedFetchModeForViewer(viewerPubkey);
    if (mode.kind !== "wot") return false;
    membership = peekWebOfTrustDiskMembership(mode.seed, mode.depth);
    if (!membership) return false;
  }
  return bundleHasVisibleThreadReplies(bundle, pathNoteID, { wotEnabled, membership });
}
