import { canonicalHex64 } from "./relay-utils.js";
import { parentID, rootIDForEvent } from "./thread-tags.js";

export function cachedThreadRoutePreviewState(event, parentEvent = null) {
  const selectedID = canonicalHex64(event?.id);
  if (!selectedID) {
    return {
      rendered: false,
      selectedID: "",
      rootID: "",
      isReply: false,
      parentLoaded: false,
      showsParentSkeleton: false,
    };
  }
  const rootID = canonicalHex64(rootIDForEvent(event)) || selectedID;
  const isReply = rootID !== selectedID || Boolean(parentID(rootID, event));
  const parentLoaded = Boolean(parentEvent?.id);
  return {
    rendered: true,
    selectedID,
    rootID,
    isReply,
    parentLoaded,
    showsParentSkeleton: isReply && !parentLoaded,
  };
}
