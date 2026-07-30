const MAX_STABLE_EXPANSIONS = 512;
const expandedByElement = new WeakMap();
const expandedByNoteKey = new Set();

function stableNoteKey(container, referenceKey) {
  const noteID = String(container?.id || "").replace(/^note-/, "").trim().toLowerCase();
  const key = String(referenceKey || "").trim();
  return noteID && key ? `${noteID}:${key}` : "";
}

export function isReferenceExpanded(container, referenceKey) {
  if (!container || !referenceKey) return false;
  if (expandedByElement.get(container)?.has(referenceKey)) return true;
  const stableKey = stableNoteKey(container, referenceKey);
  return stableKey ? expandedByNoteKey.has(stableKey) : false;
}

export function markReferenceExpanded(container, referenceKey) {
  if (!container || !referenceKey) return;
  let elementKeys = expandedByElement.get(container);
  if (!elementKeys) {
    elementKeys = new Set();
    expandedByElement.set(container, elementKeys);
  }
  elementKeys.add(referenceKey);

  const stableKey = stableNoteKey(container, referenceKey);
  if (!stableKey || expandedByNoteKey.has(stableKey)) return;
  if (expandedByNoteKey.size >= MAX_STABLE_EXPANSIONS) {
    expandedByNoteKey.delete(expandedByNoteKey.values().next().value);
  }
  expandedByNoteKey.add(stableKey);
}
