import { fetchWithSession, getSession, normalizedPubkey, normalizeRelayURL, saveSelectedRelays, selectedRelays } from "./session.js";

let list = null;
let suggestions = null;
let globalClickBound = false;

function addRelay(url) {
  const normalized = normalizeRelayURL(url);
  if (!normalized) {
    alert("Relay URL must start with ws:// or wss://");
    return;
  }
  if (!list) return;
  if (list.querySelector(`[data-relay="${CSS.escape(normalized)}"]`)) return;
  const li = document.createElement("li");
  li.dataset.relay = normalized;

  const code = document.createElement("code");
  code.className = "relay-url-popover";
  code.textContent = normalized;
  code.dataset.checkRelay = normalized;
  code.tabIndex = 0;
  code.setAttribute("role", "button");

  const removeButton = document.createElement("button");
  removeButton.type = "button";
  removeButton.textContent = "remove";
  removeButton.dataset.removeRelay = normalized;

  li.append(code, document.createTextNode(" "), removeButton);
  list.append(li);
}

function syncSelectedRelays() {
  if (!list) return;
  const seen = new Set(Array.from(list.querySelectorAll("[data-relay]")).map((item) => item.dataset.relay));
  for (const relay of selectedRelays()) {
    if (seen.has(relay)) continue;
    addRelay(relay);
  }
}

function bindRelayForm() {
  const form = document.querySelector("[data-relay-form]");
  if (!form || form.dataset.bound === "1") return;
  form.dataset.bound = "1";
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    const relay = normalizeRelayURL(String(new FormData(event.currentTarget).get("relay") || ""));
    if (!relay) return;
    addRelay(relay);
    saveSelectedRelays([...selectedRelays(), relay]);
    event.currentTarget.reset();
  });
}

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
    const add = from.closest("[data-add-relay]");
    if (add) {
      const relay = add.dataset.addRelay;
      addRelay(relay);
      saveSelectedRelays([...selectedRelays(), relay]);
      add.textContent = "added";
      return;
    }

    const remove = from.closest("[data-remove-relay]");
    if (remove) {
      const relay = remove.dataset.removeRelay;
      saveSelectedRelays(selectedRelays().filter((item) => item !== relay));
      remove.closest("[data-relay]")?.remove();
      return;
    }

    const anchor = from.closest("[data-check-relay]");
    if (!anchor) return;
    const pop = ensureRelayInfoPopover();
    pop.textContent = "Loading...";
    positionRelayPopoverNear(anchor, pop);
    showRelayInfoPopover();
    const response = await fetch(`/api/relay-info?url=${encodeURIComponent(anchor.dataset.checkRelay)}`);
    pop.textContent = formatRelayInfo(await response.json());
    positionRelayPopoverNear(anchor, pop);
  });
  document.addEventListener("keydown", (event) => {
    if (event.key !== "Enter" && event.key !== " ") return;
    const anchor = event.target?.closest?.("[data-check-relay]");
    if (!anchor || event.target !== anchor) return;
    event.preventDefault();
    anchor.click();
  });
}

async function loadSessionRelaySuggestions() {
  if (!suggestions) return;
  const pubkey = normalizedPubkey(getSession());
  if (!pubkey) return;
  // Selected relays travel as X-Ptxt-Relays via fetchWithSession; no need in URL.
  try {
    const response = await fetchWithSession(`/relays?fragment=suggestions`);
    if (!response.ok) return;
    suggestions.innerHTML = await response.text();
  } catch {
    // Relay suggestions are progressive enhancement; the page is useful without them.
  }
}

export function initRelaysPage(root = document) {
  list = root.querySelector("[data-relay-list]");
  suggestions = root.querySelector("[data-relay-suggestions]");
  if (!list) return;
  bindRelayForm();
  syncSelectedRelays();
  void loadSessionRelaySuggestions();
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

function positionRelayPopoverNear(anchor, pop) {
  const margin = 8;
  const maxW = Math.min(360, window.innerWidth - 2 * margin);
  pop.style.maxWidth = `${maxW}px`;
  void pop.offsetWidth;
  const rect = anchor.getBoundingClientRect();
  const ph = pop.offsetHeight || 120;
  let top = rect.bottom + margin;
  if (top + ph > window.innerHeight - margin && rect.top - ph - margin > margin) {
    top = rect.top - ph - margin;
  }
  top = Math.max(margin, Math.min(top, window.innerHeight - ph - margin));
  let left = rect.left;
  if (left + maxW > window.innerWidth - margin) {
    left = window.innerWidth - maxW - margin;
  }
  left = Math.max(margin, left);
  pop.style.setProperty("position", "fixed");
  pop.style.setProperty("top", `${top}px`);
  pop.style.setProperty("left", `${left}px`);
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
