import { normalizeRelayList } from "./relay-config.js";

export function profileRelayListsMatch(current = [], next = []) {
  if (current.length !== next.length) return false;
  return current.every((relay, index) => relay === next[index]);
}

export function mergeProfileRelays(current = [], incoming = []) {
  return normalizeRelayList([...(current || []), ...(incoming || [])]);
}

export function profileDisplayRelays(relays = [], fallbackRelays = []) {
  const normalized = normalizeRelayList(relays);
  if (!normalized.length) return [];
  const fallback = new Set(normalizeRelayList(fallbackRelays));
  if (fallback.size > 0 && normalized.every((relay) => fallback.has(relay))) return [];
  return normalized;
}

export function profileRelayHintsToList(relayHints = {}) {
  return normalizeRelayList([
    ...(Array.isArray(relayHints.read) ? relayHints.read : []),
    ...(Array.isArray(relayHints.write) ? relayHints.write : []),
    ...(Array.isArray(relayHints.any) ? relayHints.any : []),
  ]);
}

export function profileAuthorWriteRelays(relayHints = {}) {
  return normalizeRelayList([
    ...(Array.isArray(relayHints.write) ? relayHints.write : []),
    ...(Array.isArray(relayHints.any) ? relayHints.any : []),
  ]);
}

export function profileFallbackRelays(relayHints = {}, followRelayHints = null, targetPubkey = "") {
  const hintedRelay = followRelayHints instanceof Map
    ? String(followRelayHints.get(String(targetPubkey || "").trim().toLowerCase()) || "").trim()
    : "";
  return normalizeRelayList([
    ...profileRelayHintsToList(relayHints),
    hintedRelay,
  ]);
}

export function profileRelaysFromHTML(html = "") {
  const markup = String(html || "").trim();
  if (!markup) return [];
  if (typeof document === "undefined" || typeof document.createElement !== "function") {
    const matches = [...markup.matchAll(/data-check-relay="([^"]+)"/g)];
    return normalizeRelayList(matches.map((match) => String(match[1] || "").trim()).filter(Boolean));
  }
  const container = document.createElement("template");
  container.innerHTML = markup;
  return normalizeRelayList(
    [...container.content.querySelectorAll("[data-check-relay]")]
      .map((node) => String(node.getAttribute("data-check-relay") || node.textContent || "").trim())
      .filter(Boolean),
  );
}
