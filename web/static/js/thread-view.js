import { sortEventsOldestFirst } from "./feed-query.js";
import { canonicalHex64, dedupeEventsByID } from "./relay-utils.js";
import { directReplyEvents, effectiveThreadParentID, focusParentEvent } from "./thread-tags.js";
import {
  partitionThreadRepliesByWoT,
} from "./thread-wot-partition.js";

const MAX_DEPTH = 20;

function countDescendants(nodes) {
  let total = 0;
  for (const node of nodes || []) {
    total += 1;
    total += countDescendants(node.children);
  }
  return total;
}

function walk(parentIDValue, depth, childrenByParent, seen) {
  if (depth >= MAX_DEPTH) return [];
  const nodes = [];
  const events = childrenByParent.get(parentIDValue) || [];
  for (const event of events) {
    const eventID = canonicalHex64(event.id);
    if (!eventID || seen.has(eventID)) continue;
    seen.add(eventID);
    const childNodes = walk(eventID, depth + 1, childrenByParent, seen);
    nodes.push({
      event,
      depth: depth + 1,
      parentID: parentIDValue,
      children: childNodes,
      replyCount: countDescendants(childNodes),
    });
  }
  return nodes;
}

function findFocusPath(nodes, selectedID) {
  const want = canonicalHex64(selectedID);
  for (const node of nodes || []) {
    if (canonicalHex64(node.event.id) === want) {
      return [node];
    }
    const sub = findFocusPath(node.children, selectedID);
    if (sub.length) return [node, ...sub];
  }
  return [];
}

function rebasedSubtree(node, baseDepth) {
  return {
    event: node.event,
    depth: baseDepth,
    parentID: node.parentID,
    children: (node.children || []).map((child) => rebasedSubtree(child, baseDepth + 1)),
    replyCount: node.replyCount,
  };
}

function leafNode(node) {
  return {
    event: node.event,
    depth: 1,
    parentID: node.parentID,
    children: [],
    replyCount: node.replyCount,
  };
}

/**
 * Mirrors internal/thread/thread.go BuildSelectedWithParents.
 * Builds the reply tree and focus metadata used by linear thread view.
 */
export function buildThreadSelected(root, selected, replies, parentByID = {}) {
  const rootID = canonicalHex64(root?.id);
  const selectedID = canonicalHex64(selected?.id);
  const childrenByParent = new Map();

  for (const reply of replies || []) {
    const replyID = canonicalHex64(reply?.id);
    if (!replyID || replyID === rootID) continue;
    const parent = effectiveThreadParentID(rootID, reply, parentByID);
    if (!childrenByParent.has(parent)) childrenByParent.set(parent, []);
    childrenByParent.get(parent).push(reply);
  }

  for (const [parent, list] of childrenByParent.entries()) {
    childrenByParent.set(parent, sortEventsOldestFirst(list));
  }

  const nodes = walk(rootID, 0, childrenByParent, new Set());
  const view = {
    root,
    selected,
    rootID,
    selectedID,
    nodes,
    focusMode: false,
    parentNode: null,
    selectedNode: null,
    hiddenAncestors: [],
    hiddenNodeCount: 0,
    replyCount: nodes.length,
  };

  if (!selectedID || selectedID === rootID) {
    return view;
  }

  const path = findFocusPath(nodes, selectedID);
  if (!path.length) {
    return view;
  }

  const selectedNode = path[path.length - 1];
  const parentNode = path.length >= 2 ? path[path.length - 2] : null;
  view.focusMode = true;
  view.selectedNode = rebasedSubtree(selectedNode, 0);
  view.parentNode = parentNode;

  if (parentNode) {
    if (rootID && rootID !== canonicalHex64(parentNode.event.id)) {
      view.hiddenAncestors.push(
        leafNode({
          event: root,
          depth: 1,
          parentID: "",
          children: [],
          replyCount: 0,
        }),
      );
    }
    for (const ancestor of path.slice(0, -2)) {
      view.hiddenAncestors.push(leafNode(ancestor));
    }
  } else if (path.length > 1) {
    for (const ancestor of path.slice(0, -1)) {
      view.hiddenAncestors.push(leafNode(ancestor));
    }
  }

  const visibleSubtree = 1 + countDescendants(selectedNode.children);
  const total = countDescendants(nodes);
  let hidden = total - visibleSubtree;
  if (parentNode) hidden -= 1;
  view.hiddenNodeCount = Math.max(0, hidden);

  return view;
}

/** Mirrors internal/httpx/handlers.go linearThreadDirectNodes. */
function linearThreadDirectNodes(nodes) {
  return (nodes || []).map((node) => ({
    event: node.event,
    depth: 1,
    parentID: node.parentID,
    children: [],
    replyCount: node.replyCount,
  }));
}

/** Mirrors internal/httpx/handlers.go buildThreadDirectReplyCounts. */
export function buildThreadDirectReplyCounts(view, events) {
  const counts = {};
  for (const event of events || []) {
    const id = canonicalHex64(event?.id);
    if (id) counts[id] = 0;
  }
  const rootID = canonicalHex64(view?.root?.id);
  if (rootID) counts[rootID] = (view?.nodes || []).length;

  function accumulate(nodes) {
    for (const node of nodes || []) {
      const id = canonicalHex64(node.event?.id);
      if (id) counts[id] = (node.children || []).length;
      accumulate(node.children);
    }
  }
  accumulate(view?.nodes);
  return counts;
}

function linearThreadReplyNodesWithFallback(view, events, parentByID) {
  const rootID = canonicalHex64(view?.root?.id);
  const selectedID = canonicalHex64(view?.selected?.id);

  if (view?.focusMode && view.selectedNode && selectedID && selectedID !== rootID) {
    const focused = linearThreadDirectNodes(view.selectedNode.children);
    if (focused.length) return focused;
    const direct = directReplyEvents(events, parentByID, rootID, selectedID);
    if (direct.length) {
      return linearThreadDirectNodes(
        direct.map((event) => ({
          event,
          depth: 1,
          parentID: selectedID,
          children: [],
          replyCount: 0,
        })),
      );
    }
  }

  const nodes = linearThreadReplyNodes(view);
  if (nodes.length) return nodes;
  if (!selectedID || selectedID === rootID) return nodes;
  const direct = directReplyEvents(events, parentByID, rootID, selectedID);
  return linearThreadDirectNodes(
    direct.map((event) => ({
      event,
      depth: 1,
      parentID: selectedID,
      children: [],
      replyCount: 0,
    })),
  );
}

/** Mirrors internal/httpx/handlers.go linearThreadReplyNodes. */
export function linearThreadReplyNodes(view) {
  if (view?.focusMode && view.selectedNode) {
    return linearThreadDirectNodes(view.selectedNode.children);
  }
  return linearThreadDirectNodes(view?.nodes);
}

/** Mirrors internal/httpx/handlers.go linearThreadOtherReplyNodes. */
export function linearThreadOtherReplyNodes(view) {
  if (!view?.focusMode || !view.selectedNode?.event?.id) return [];
  const selectedID = canonicalHex64(view.selectedNode.event.id);
  const parentIDValue = canonicalHex64(view.parentNode?.event?.id);
  const out = [];
  for (const node of view.nodes || []) {
    const nodeID = canonicalHex64(node.event.id);
    if (nodeID === selectedID || (parentIDValue && nodeID === parentIDValue)) continue;
    out.push({
      event: node.event,
      depth: 1,
      parentID: node.parentID,
      children: [],
      replyCount: node.replyCount,
    });
  }
  return out;
}

/** Build thread view metadata used by linear thread rendering. */
export function resolveThreadView(root, selected, events, parentByID = {}) {
  const rootID = canonicalHex64(root?.id);
  const replies = (events || []).filter((event) => canonicalHex64(event?.id) !== rootID);
  const view = buildThreadSelected(root, selected, replies, parentByID);
  return {
    view,
    replyCounts: buildThreadDirectReplyCounts(view, events),
    linearNodes: linearThreadReplyNodesWithFallback(view, events, parentByID),
  };
}

/** Event list for the linear thread reply column (iOS thread.nodes / selectedNode.children). */
export function linearThreadReplyEvents(root, selected, events, parentByID = {}) {
  return resolveThreadView(root, selected, events, parentByID).linearNodes.map((node) => node.event);
}

/**
 * True when a thread bundle shows direct replies (or filtered WoT reveals) under focus.
 * Used to avoid reply skeletons that imply content is coming.
 */
export function bundleHasVisibleThreadReplies(bundle, pathNoteID, { wotEnabled, membership = null } = {}) {
  if (!bundle?.root) return false;
  const rootID = canonicalHex64(bundle.rootID || bundle.root?.id);
  const selectedID = canonicalHex64(pathNoteID || bundle.selectedID || rootID);
  if (!rootID || !selectedID) return false;

  const selected =
    bundle.events?.find((event) => canonicalHex64(event.id) === selectedID) ||
    (selectedID === rootID ? bundle.root : null);
  if (!selected) return false;

  let events = bundle.events || [];
  const parentByID = bundle.parentByID || {};

  if (wotEnabled) {
    if (!membership) return false;
    const root = bundle.root;
    const replies = events.filter((event) => canonicalHex64(event.id) !== rootID);
    const parentEvent = focusParentEvent(root, selected, events, parentByID);
    const lookup = (id) => events.find((event) => canonicalHex64(event.id) === canonicalHex64(id)) || null;
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
    events = dedupeEventsByID([root, selected, ...partition.treeReplies]);
  }

  const resolved = resolveThreadView(bundle.root, selected, events, parentByID);
  return resolved.linearNodes.length > 0;
}
