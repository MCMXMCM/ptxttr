import { authorMembershipSet } from "./feed-query.js";
import { resolveFeedFetchModeForViewer } from "./feed-service.js";
import { expandWebOfTrust } from "./wot-service.js";
import { canonicalHex64, dedupeEventsByID } from "./relay-utils.js";
import { focusParentEvent, threadExpectsFocusView } from "./thread-tags.js";
import { threadWoTDepthForViewer } from "./thread-wot-prefs.js";
import {
  buildFilteredReplyNodes,
  excludeVisibleReplies,
  filteredReplyRailDepth,
  partitionThreadRepliesByWoT,
  selectedDepthFromRoot,
} from "./thread-wot-partition.js";

export {
  buildFilteredReplyNodes,
  filteredReplyRailDepth,
  excludeVisibleReplies,
  isDirectThreadReply,
  partitionRepliesByWoT,
  partitionThreadRepliesByWoT,
  selectedDepthFromRoot,
} from "./thread-wot-partition.js";

export async function resolveThreadWoTMembership(viewerPubkey) {
  const mode = resolveFeedFetchModeForViewer(viewerPubkey);
  if (mode.kind !== "wot") return new Set();
  const depth = threadWoTDepthForViewer(viewerPubkey, mode.depth);
  const authors = await expandWebOfTrust(mode.seed, depth);
  return authorMembershipSet(authors);
}

export function applyThreadWoTToBundle(bundle, { wotEnabled, membership }) {
  const root = bundle.root;
  const selected = bundle.selected || root;
  const rootID = canonicalHex64(bundle.rootID || root?.id);
  const selectedID = canonicalHex64(bundle.selectedID || selected?.id);
  const parentByID = bundle.parentByID || {};
  const allEvents = bundle.events || [];
  const replies = allEvents.filter((event) => canonicalHex64(event.id) !== rootID);

  if (!wotEnabled) {
    return {
      ...bundle,
      rootID,
      selectedID,
      wot: {
        enabled: false,
        filteredCount: 0,
        filteredReplies: [],
        filteredReplyNodes: [],
      },
    };
  }

  const parentEvent = focusParentEvent(root, selected, allEvents, parentByID);
  const lookup = (id) => allEvents.find((event) => canonicalHex64(event.id) === canonicalHex64(id)) || null;
  const partition = partitionThreadRepliesByWoT(
    replies,
    rootID,
    root,
    selected,
    parentEvent?.id && canonicalHex64(parentEvent.id) !== rootID ? parentEvent : null,
    parentByID,
    membership,
    lookup,
  );

  const displayEvents = dedupeEventsByID([root, selected, ...partition.treeReplies]);
  const filteredReplies = excludeVisibleReplies(partition.filteredReplies, displayEvents);
  const focused = threadExpectsFocusView(root, selected);
  const selectedDepth = selectedDepthFromRoot(root, selected, allEvents, parentByID);
  const focusIsRoot = rootID === selectedID;
  const filteredReplyNodes = buildFilteredReplyNodes(
    filteredReplies,
    filteredReplyRailDepth(focused, selectedDepth, focusIsRoot),
    focused ? selectedID : rootID,
  );

  return {
    ...bundle,
    root,
    selected,
    rootID,
    selectedID,
    events: displayEvents,
    parentByID,
    wot: {
      enabled: true,
      filteredCount: filteredReplies.length,
      filteredReplies,
      filteredReplyNodes,
    },
  };
}
