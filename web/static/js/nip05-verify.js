import { positionPopoverNearAnchor } from "./popover_anchor.js";
import { normalizePubkey } from "./relay-utils.js";

const NIP05_CACHE_TTL_MS = 60 * 60 * 1000;
const nip05StatusCache = new Map();
const nip05Inflight = new Map();

const STATUS_LABELS = {
  verified: "NIP-5 verified for this profile.",
  pubkeyMismatch: "NIP-5 points to a different pubkey.",
  nameNotFound: "This name was not found in the site's NIP-5 record.",
  redirectRejected: "The NIP-5 lookup redirect was rejected.",
  unreachable: "The NIP-5 record could not be reached.",
  invalidIdentifier: "This NIP-5 identifier is invalid.",
	checking: "Checking NIP-5 verification…",
	unknown: "NIP-5 verification has not been refreshed yet.",
};

function applyNIP05Status(statusNode, status) {
  if (!statusNode) return;
  if (!statusNode.dataset) statusNode.dataset = {};
  statusNode.dataset.nip05StatusKind = status;
  statusNode.dataset.nip05StatusDetail = STATUS_LABELS[status] || "Unknown NIP-5 verification state.";
  statusNode.setAttribute?.("aria-label", `NIP-5 verification status: ${statusNode.dataset.nip05StatusDetail}`);
  statusNode.classList?.remove?.("muted", "is-ok", "is-error");
	if (status === "checking" || status === "unknown") {
    statusNode.textContent = "";
    statusNode.hidden = true;
    statusNode.setAttribute?.("aria-expanded", "false");
    return;
  }
  if (status === "verified") {
    statusNode.textContent = "✓";
    statusNode.classList?.add?.("is-ok");
    statusNode.hidden = false;
    return;
  }
  statusNode.textContent = "×";
  statusNode.classList?.add?.("is-error");
  statusNode.hidden = false;
}

let nip05Popover = null;
let nip05PopoverOutsideCloseWired = false;
let nip05PopoverBindingsWired = false;

function isNativePopoverSupported() {
  return typeof HTMLDivElement !== "undefined" && "showPopover" in HTMLDivElement.prototype;
}

function ensureNIP05Popover() {
  if (nip05Popover) return nip05Popover;
  if (!document?.createElement || !document.body?.appendChild) return null;
  nip05Popover = document.createElement("div");
  nip05Popover.id = "ptxt-nip05-popover";
  nip05Popover.setAttribute("popover", "auto");
  nip05Popover.className = "nip05-detail-popover";
  if (!isNativePopoverSupported()) {
    nip05Popover.hidden = true;
  }
  document.body.appendChild(nip05Popover);
  return nip05Popover;
}

function wireNIP05PopoverOutsideClose() {
  if (nip05PopoverOutsideCloseWired || !document?.addEventListener) return;
  nip05PopoverOutsideCloseWired = true;
  document.addEventListener("pointerdown", (event) => {
    const pop = ensureNIP05Popover();
    if (!pop || pop.hidden) return;
    const t = event.target;
    if (t && (pop === t || pop.contains?.(t))) return;
    if (t && t.closest?.("[data-nip05-status]")) return;
    hideNIP05Popover();
  }, true);
}

function showNIP05Popover() {
  const pop = ensureNIP05Popover();
  if (!pop) return;
  if (isNativePopoverSupported() && !pop.dataset.nip05PopoverLegacy) {
    try {
      pop.showPopover();
      return;
    } catch {
      pop.removeAttribute("popover");
      pop.dataset.nip05PopoverLegacy = "1";
    }
  }
  pop.hidden = false;
  wireNIP05PopoverOutsideClose();
}

function hideNIP05Popover() {
  if (!nip05Popover) return;
  if (isNativePopoverSupported() && typeof nip05Popover.hidePopover === "function" && !nip05Popover.hidden) {
    try {
      nip05Popover.hidePopover();
    } catch {
      nip05Popover.hidden = true;
    }
  } else {
    nip05Popover.hidden = true;
  }
  if (typeof document?.querySelectorAll === "function") {
    document.querySelectorAll("[data-nip05-status][aria-expanded='true']").forEach((node) => {
      node.setAttribute?.("aria-expanded", "false");
    });
  }
}

function openNIP05Popover(anchor) {
  const pop = ensureNIP05Popover();
  if (!pop || !anchor) return;
  const detail = String(anchor.dataset.nip05StatusDetail || "").trim() || "Unknown NIP-5 verification state.";
  pop.textContent = detail;
  positionPopoverNearAnchor(anchor, pop, { maxWidth: 320, fallbackHeight: 72 });
  showNIP05Popover();
  anchor.setAttribute?.("aria-expanded", "true");
  positionPopoverNearAnchor(anchor, pop, { maxWidth: 320, fallbackHeight: 72 });
}

function bindNIP05StatusPopover() {
  if (nip05PopoverBindingsWired || !document?.addEventListener) return;
  nip05PopoverBindingsWired = true;
  document.addEventListener("click", (event) => {
    const anchor = event.target?.closest?.("[data-nip05-status]");
    if (!anchor || anchor.hidden) return;
    event.preventDefault();
    openNIP05Popover(anchor);
  });
  document.addEventListener("keydown", (event) => {
    if (event.key !== "Enter" && event.key !== " ") return;
    const anchor = event.target?.closest?.("[data-nip05-status]");
    if (!anchor || event.target !== anchor || anchor.hidden) return;
    event.preventDefault();
    openNIP05Popover(anchor);
  });
}

function parseIdentifier(value) {
  const trimmed = String(value || "").trim();
  const at = trimmed.lastIndexOf("@");
  if (at <= 0 || at >= trimmed.length - 1) return null;
  const localPart = trimmed.slice(0, at).trim().toLowerCase();
  const domain = trimmed.slice(at + 1).trim().toLowerCase();
  if (!localPart || !domain || /[\/ @]/.test(domain)) return null;
  if (!/^[a-z0-9._-]+$/.test(localPart)) return null;
  return { localPart, domain };
}

function nip05Match(doc, localPart) {
  const names = doc && typeof doc === "object" ? doc.names : null;
  if (!names || typeof names !== "object") return "";
  if (typeof names[localPart] === "string" && names[localPart].trim()) return names[localPart].trim();
  const wanted = localPart.toLowerCase();
  for (const [name, pubkey] of Object.entries(names)) {
    if (String(name || "").trim().toLowerCase() !== wanted) continue;
    const value = String(pubkey || "").trim();
    if (value) return value;
  }
  return "";
}

export async function queryNIP05Profile(identifier) {
  const parsed = parseIdentifier(identifier);
  if (!parsed) return null;
  const url = new URL(`https://${parsed.domain}/.well-known/nostr.json`);
  url.searchParams.set("name", parsed.localPart);
  const response = await fetch(url.toString(), { redirect: "error" });
  if (!response.ok) throw new Error(`nip05 http ${response.status}`);
  const doc = await response.json();
  const pubkey = nip05Match(doc, parsed.localPart);
  return pubkey ? { pubkey } : null;
}

function nip05CacheKey(identifier, pubkey) {
  const normalizedIdentifier = String(identifier || "").trim().toLowerCase();
  const normalizedKey = normalizePubkey(pubkey);
  return normalizedIdentifier && normalizedKey ? `${normalizedIdentifier}|${normalizedKey}` : "";
}

function readCachedNIP05Status(identifier, pubkey) {
  const key = nip05CacheKey(identifier, pubkey);
  if (!key) return "";
  const entry = nip05StatusCache.get(key);
  if (!entry) return "";
  if (Date.now() - entry.fetchedAt > NIP05_CACHE_TTL_MS) {
    nip05StatusCache.delete(key);
    return "";
  }
  return entry.status || "";
}

function cacheNIP05Status(identifier, pubkey, status) {
  const key = nip05CacheKey(identifier, pubkey);
  if (!key || !status) return status;
  nip05StatusCache.set(key, { status, fetchedAt: Date.now() });
  return status;
}

async function verifyNIP05Status(identifier, pubkey) {
  const cached = readCachedNIP05Status(identifier, pubkey);
  if (cached) return cached;

  const key = nip05CacheKey(identifier, pubkey);
  if (!key) return "invalidIdentifier";
  if (nip05Inflight.has(key)) return nip05Inflight.get(key);

  const request = (async () => {
    const expected = normalizePubkey(pubkey);
    let status = "";
    try {
      const profile = await queryNIP05Profile(identifier);
      const found = normalizePubkey(profile?.pubkey || "");
      if (found) status = found === expected ? "verified" : "pubkeyMismatch";
      else status = "nameNotFound";
    } catch {
      status = "unreachable";
    }
    if (!status) status = "unreachable";
    return cacheNIP05Status(identifier, pubkey, status);
  })();

  nip05Inflight.set(key, request);
  try {
    return await request;
  } finally {
    nip05Inflight.delete(key);
  }
}

async function verifyNIP05(node) {
  const identifier = String(node.getAttribute("data-nip05") || "").trim();
  const pubkey = String(node.getAttribute("data-pubkey") || "").trim();
  const statusNode = node.querySelector("[data-nip05-status]");
  if (!identifier || !pubkey || !statusNode) return;
  if (node.dataset.nip05Loaded === "1") return;
  const serverStatus = String(statusNode.dataset?.nip05StatusKind || "").trim();
	if (serverStatus && serverStatus !== "checking") {
    node.dataset.nip05Loaded = "1";
    node.dataset.nip05Status = serverStatus;
    return;
	}
	if (document.body?.dataset?.guestV2) {
		node.dataset.nip05Loaded = "1";
		node.dataset.nip05Status = "unknown";
		applyNIP05Status(statusNode, "unknown");
		return;
	}
  node.dataset.nip05Loaded = "1";
  applyNIP05Status(statusNode, "checking");
  try {
    const status = await verifyNIP05Status(identifier, pubkey);
    applyNIP05Status(statusNode, status);
    node.dataset.nip05Status = status;
  } catch {
    applyNIP05Status(statusNode, "unreachable");
    node.dataset.nip05Status = "unreachable";
  }
}

export function refreshNIP05Verification(root = document) {
  root.querySelectorAll("[data-nip05-verify]").forEach((node) => {
    void verifyNIP05(node);
  });
}

export function bindProfilePaymentCopy(root = document) {
  root.querySelectorAll("[data-profile-payment-copy]").forEach((wrap) => {
    const button = wrap.querySelector("[data-profile-payment-copy-btn]");
    const glyph = wrap.querySelector("[data-profile-payment-copy-glyph]");
    const payment = String(wrap.getAttribute("data-payment") || "").trim();
    if (!button || button.dataset.boundPaymentCopy === "1" || !payment) return;
    button.dataset.boundPaymentCopy = "1";
    button.addEventListener("click", async (event) => {
      event.preventDefault();
      const iconNode = glyph instanceof HTMLElement ? glyph : button;
      try {
        await navigator.clipboard.writeText(payment);
        const prev = iconNode.textContent;
        iconNode.textContent = "✓";
        button.dataset.copied = "1";
        window.setTimeout(() => {
          iconNode.textContent = prev;
          delete button.dataset.copied;
        }, 1000);
      } catch {
        const prev = iconNode.textContent;
        iconNode.textContent = "!";
        button.dataset.copied = "0";
        window.setTimeout(() => {
          iconNode.textContent = prev;
          delete button.dataset.copied;
        }, 1000);
      }
    });
  });
}

bindNIP05StatusPopover();
