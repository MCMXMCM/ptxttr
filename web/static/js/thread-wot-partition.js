import { sortEventsOldestFirst } from "./feed-query.js";
import { canonicalHex64, dedupeEventsByID, normalizePubkey } from "./relay-utils.js";
import { parentID, effectiveThreadParentID } from "./thread-tags.js";

const MAX_DEPTH = 32;

function threadContextEventIDs(rootID, root, selected, parent, lookup) {
  const preserved = new Set();
  if (root?.id) preserved.add(canonicalHex64(root.id));
  if (selected?.id) preserved.add(canonicalHex64(selected.id));
  if (parent?.id) preserved.add(canonicalHex64(parent.id));
  if (!selected || !lookup) return preserved;

  let current = selected;
  const rootHex = canonicalHex64(rootID);
  for (let hop = 0; hop < MAX_DEPTH; hop++) {
    const parentIDValue = parentID(rootHex, current);
    if (!parentIDValue || parentIDValue === rootHex || parentIDValue === canonicalHex64(current.id)) break;
    if (preserved.has(parentIDValue)) break;
    preserved.add(parentIDValue);
    const ancestor = lookup(parentIDValue);
    if (!ancestor) break;
    current = ancestor;
  }
  return preserved;
}

export function partitionRepliesByWoT(
  replies,
  rootID,
  root,
  selected,
  parent,
  membership,
  lookup,
) {
  const preserved = threadContextEventIDs(rootID, root, selected, parent, lookup);
  const trusted = [];
  const excluded = [];
  for (const event of replies || []) {
    if (!event?.id) continue;
    const id = canonicalHex64(event.id);
    if (preserved.has(id)) {
      trusted.push(event);
      continue;
    }
    const pubkey = normalizePubkey(event.pubkey);
    if (!pubkey || !membership.has(pubkey)) {
      excluded.push(event);
      continue;
    }
    trusted.push(event);
  }
  return { trusted, excluded };
}

function bridgeRepliesForTree(trusted, allReplies, rootID, parentByID) {
  const root = canonicalHex64(rootID);
  const trustedIDs = new Set(trusted.map((event) => canonicalHex64(event.id)).filter(Boolean));
  const replyByID = new Map(
    (allReplies || []).map((event) => [canonicalHex64(event.id), event]).filter(([id]) => id),
  );
  const includedIDs = new Set(trustedIDs);
  const bridges = [];
  const bridgeIDs = new Set();
  for (const reply of trusted) {
    let parent = effectiveThreadParentID(root, reply, parentByID);
    while (parent && parent !== root && !includedIDs.has(parent)) {
      const parentEvent = replyByID.get(parent);
      if (!parentEvent) break;
      if (!trustedIDs.has(parent) && !bridgeIDs.has(parent)) {
        bridgeIDs.add(parent);
        bridges.push(parentEvent);
      }
      includedIDs.add(parent);
      parent = effectiveThreadParentID(root, parentEvent, parentByID);
    }
  }
  return sortEventsOldestFirst(bridges);
}

export function isDirectThreadReply(reply, parentIDValue, rootID, parentByID) {
  const parent = canonicalHex64(parentIDValue);
  const root = canonicalHex64(rootID);
  const replyID = canonicalHex64(reply?.id);
  if (!replyID || replyID === parent) return false;
  return effectiveThreadParentID(root, reply, parentByID) === parent;
}

export function partitionThreadRepliesByWoT(
  replies,
  rootID,
  root,
  selected,
  parent,
  parentByID,
  membership,
  lookup,
) {
  const allReplies = dedupeEventsByID(replies);
  const { trusted, excluded } = partitionRepliesByWoT(
    allReplies,
    rootID,
    root,
    selected,
    parent,
    membership,
    lookup,
  );
  const bridges = bridgeRepliesForTree(trusted, allReplies, rootID, parentByID);
  const treeReplies = sortEventsOldestFirst(dedupeEventsByID([...trusted, ...bridges]));

  let focusID = canonicalHex64(selected?.id);
  if (!focusID && root?.id) focusID = canonicalHex64(root.id);
  const filteredDirect = sortEventsOldestFirst(
    excluded.filter((reply) => isDirectThreadReply(reply, focusID, rootID, parentByID)),
  );

  return {
    trustedReplies: trusted,
    bridgeReplies: bridges,
    filteredReplies: filteredDirect,
    treeReplies,
  };
}

export function buildFilteredReplyNodes(events, depth, parentIDValue) {
  const parent = canonicalHex64(parentIDValue);
  const clampedDepth = Math.max(1, depth);
  return (events || []).map((event) => ({
    event,
    depth: clampedDepth,
    parentID: parent,
  }));
}

export function excludeVisibleReplies(filteredReplies, visibleEvents) {
  const visibleIDs = new Set((visibleEvents || []).map((event) => canonicalHex64(event?.id)).filter(Boolean));
  return (filteredReplies || []).filter((event) => !visibleIDs.has(canonicalHex64(event?.id)));
}

export function filteredReplyRailDepth(focusedView, selectedDepth, focusIsRoot) {
  if (focusIsRoot) return 1;
  if (focusedView) return 1;
  return Math.min(5, Math.max(1, selectedDepth));
}

export function selectedDepthFromRoot(root, selected, events, parentByID) {
  const rootID = canonicalHex64(root?.id);
  const selectedID = canonicalHex64(selected?.id);
  if (!rootID || !selectedID || rootID === selectedID) return 0;
  let depth = 0;
  let current = selected;
  for (let hop = 0; hop < MAX_DEPTH; hop++) {
    const parent = effectiveThreadParentID(rootID, current, parentByID);
    if (parent === rootID) return depth + 1;
    depth += 1;
    const parentEvent = (events || []).find((event) => canonicalHex64(event.id) === parent);
    if (!parentEvent) break;
    current = parentEvent;
  }
  return depth;
}
