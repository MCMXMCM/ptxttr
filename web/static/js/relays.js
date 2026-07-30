import { positionPopoverNearAnchor } from "./popover_anchor.js";
import { nip11 } from "../lib/nostr-tools.js";
import { directRelaysEnabled } from "./relay-config.js";
let globalClickBound = false;
const relayInfoCache = new Map();

function eventTargetElement(event) {
  const t = event.target;
  if (!t) return null;
  if (t.nodeType === 3) {
    return t.parentElement;
  }
  return t instanceof Element ? t : null;
}

function bindGlobalRelayClicks() {
  if (globalClickBound) return;
  globalClickBound = true;
  document.addEventListener("click", async (event) => {
    const from = eventTargetElement(event);
    if (!from) return;
    const anchor = from.closest("[data-check-relay]");
    if (!anchor) return;
    const pop = ensureRelayInfoPopover();
    pop.textContent = "Loading...";
    positionPopoverNearAnchor(anchor, pop, { maxWidth: 360, fallbackHeight: 120 });
    showRelayInfoPopover();
    const info = await fetchCachedRelayInfo(anchor.dataset.checkRelay);
    pop.textContent = formatRelayInfo(info || { error: "Could not load relay info." });
    positionPopoverNearAnchor(anchor, pop, { maxWidth: 360, fallbackHeight: 120 });
  });
  document.addEventListener("keydown", (event) => {
    if (event.key !== "Enter" && event.key !== " ") return;
    const anchor = event.target?.closest?.("[data-check-relay]");
    if (!anchor || event.target !== anchor) return;
    event.preventDefault();
    anchor.click();
  });
}

export function initRelaysPage(root = document) {
  hydrateRelayInfoCards(root);
}

export function hydrateRelayInfoCards(root = document) {
  root.querySelectorAll?.("[data-relay-info-card]").forEach((card) => {
    if (!(card instanceof HTMLElement)) return;
    if (card.dataset.relayInfoHydrated === "1" || card.dataset.relayInfoHydrating === "1") return;
    const relay = card.dataset.relayUrl || card.querySelector("[data-check-relay]")?.getAttribute("data-check-relay") || "";
    if (!relay) return;
    card.dataset.relayInfoHydrating = "1";
    void fetchCachedRelayInfo(relay).then((info) => {
      card.dataset.relayInfoHydrated = "1";
      delete card.dataset.relayInfoHydrating;
      applyRelayInfoToCard(card, relay, info);
    });
  });
}

async function fetchCachedRelayInfo(relay) {
  const relayURL = String(relay || "").trim();
  if (!relayURL || !directRelaysEnabled()) return null;
  if (!relayInfoCache.has(relayURL)) {
    relayInfoCache.set(relayURL, nip11.fetchRelayInformation(relayURL).catch(() => null));
  }
  return relayInfoCache.get(relayURL);
}

function applyRelayInfoToCard(card, relay, info) {
  if (!info || typeof info !== "object") return;
  const title = relayDisplayName(relay, info);
  const titleNode = card.querySelector("[data-relay-title]");
  if (titleNode && title) titleNode.textContent = title;
  const description = String(info.description || "").trim();
  const descriptionNode = card.querySelector("[data-relay-description]");
  if (descriptionNode instanceof HTMLElement) {
    descriptionNode.textContent = description;
    descriptionNode.hidden = !description;
  }
  const nipsNode = card.querySelector("[data-relay-nips]");
  if (nipsNode instanceof HTMLElement) {
    const nips = relayNIPLabels(info.supported_nips);
    nipsNode.replaceChildren(...nips.map((label) => {
      const chip = document.createElement("span");
      chip.className = "relay-card-nip";
      chip.textContent = label;
      return chip;
    }));
    nipsNode.hidden = nips.length === 0;
  }
  const icon = String(info.icon || "").trim();
  const iconNode = card.querySelector("[data-relay-icon]");
  const iconURL = safeImageURL(icon, relay);
  if (iconNode instanceof HTMLElement && iconURL) {
    iconNode.textContent = "";
    const image = document.createElement("img");
    image.src = iconURL;
    image.alt = "";
    image.loading = "lazy";
    image.decoding = "async";
    iconNode.append(image);
  }
}

function relayDisplayName(relay, info = {}) {
  const name = String(info.name || "").trim();
  if (name) return name;
  return relayHostLabel(relay);
}

export function relayHostLabel(relay) {
  try {
    return new URL(String(relay || "").replace(/^ws:\/\//, "http://").replace(/^wss:\/\//, "https://")).hostname || String(relay || "").trim();
  } catch {
    return String(relay || "").trim();
  }
}

function relayNIPLabels(raw) {
  if (!Array.isArray(raw)) return [];
  return [...new Set(raw
    .map((nip) => Number.parseInt(`${nip}`, 10))
    .filter((nip) => Number.isFinite(nip) && nip >= 0))]
    .sort((a, b) => a - b)
    .slice(0, 12)
    .map((nip) => `NIP-${nip}`);
}

function relayHTTPBaseURL(relay) {
  try {
    const parsed = new URL(String(relay || "").replace(/^ws:\/\//, "http://").replace(/^wss:\/\//, "https://"));
    return parsed.origin + "/";
  } catch {
    return window.location?.href || "https://example.com/";
  }
}

function safeImageURL(raw, relay) {
  try {
    const parsed = new URL(raw, relayHTTPBaseURL(relay));
    if (parsed.protocol !== "https:" && parsed.protocol !== "http:") return "";
    return parsed.href;
  } catch {
    return "";
  }
}

let relayInfoPopover = null;
let popoverOutsideCloseWired = false;
function isNativePopoverSupported() {
  return typeof HTMLDivElement !== "undefined" && "showPopover" in HTMLDivElement.prototype;
}

function ensureRelayInfoPopover() {
  if (relayInfoPopover) return relayInfoPopover;
  relayInfoPopover = document.createElement("div");
  relayInfoPopover.id = "ptxt-relay-info-popover";
  relayInfoPopover.setAttribute("popover", "auto");
  relayInfoPopover.className = "relay-detail-popover";
  if (!isNativePopoverSupported()) {
    relayInfoPopover.hidden = true;
  }
  document.body.appendChild(relayInfoPopover);
  return relayInfoPopover;
}

function wireRelayPopoverOutsideClose() {
  if (popoverOutsideCloseWired) return;
  popoverOutsideCloseWired = true;
  document.addEventListener(
    "pointerdown",
    (event) => {
      const p = ensureRelayInfoPopover();
      if (p.hidden) return;
      const t = event.target;
      if (t && (p === t || (p.contains && p.contains(t)))) return;
      if (t && t.closest && t.closest("[data-check-relay]")) return;
      p.hidden = true;
    },
    true,
  );
}

function showRelayInfoPopover() {
  const pop = ensureRelayInfoPopover();
  if (isNativePopoverSupported() && !pop.dataset.relayPopoverLegacy) {
    try {
      pop.showPopover();
      return;
    } catch {
      pop.removeAttribute("popover");
      pop.dataset.relayPopoverLegacy = "1";
    }
  }
  pop.hidden = false;
  wireRelayPopoverOutsideClose();
}

bindGlobalRelayClicks();
initRelaysPage(document);

function formatRelayInfo(info) {
  if (!info || typeof info !== "object") return "No relay info returned.";
  const lines = [];
  lines.push(info.url || "unknown relay");
  lines.push(info.error ? `status: error - ${info.error}` : "status: ok");
  if (info.name) lines.push(`name: ${info.name}`);
  if (info.software) lines.push(`software: ${info.software}${info.version ? ` ${info.version}` : ""}`);
  if (Array.isArray(info.supported_nips) && info.supported_nips.length) {
    lines.push(`supported NIPs: ${info.supported_nips.join(", ")}`);
  }
  if (info.description) lines.push(`description: ${info.description}`);
  return lines.join("\n");
}
