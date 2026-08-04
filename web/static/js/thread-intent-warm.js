import { appBootstrap } from "./app/bootstrap.js";
import { allowSpeculativeWork } from "./power-mode.js";
import { fetchWithSession, normalizedPubkey } from "./session.js";

const HOVER_DELAY_MS = 120;
const DEDUPE_TTL_MS = 2 * 60_000;
const timers = new Map();
const warmedAt = new Map();

function threadIDFromURL(urlLike) {
  let url;
  try {
    url = new URL(urlLike, window.location.origin);
  } catch {
    return "";
  }
  if (url.origin !== window.location.origin || !url.pathname.startsWith("/thread/")) return "";
  const selected = String(url.searchParams.get("selected") || "").toLowerCase();
  if (/^[0-9a-f]{64}$/.test(selected)) return selected;
  const hash = url.hash.match(/^#note-([0-9a-f]{64})$/i)?.[1]?.toLowerCase() || "";
  if (hash) return hash;
  const path = url.pathname.match(/^\/thread\/([0-9a-f]{64})/i)?.[1]?.toLowerCase() || "";
  return path;
}

function intentTarget(target) {
  if (!(target instanceof Element)) return null;
  const card = target.closest("[data-ascii-select-href]");
  const link = target.closest("a[href^='/thread/']");
  const href = card?.getAttribute("data-ascii-select-href") || link?.getAttribute("href") || "";
  const id = threadIDFromURL(href);
  return id ? { id, owner: card || link } : null;
}

function recentlyWarmed(id, now = Date.now()) {
  const at = Number(warmedAt.get(id) || 0);
  if (at > 0 && now - at < DEDUPE_TTL_MS) return true;
  if (at > 0) warmedAt.delete(id);
  return false;
}

function dispatchWarm(id, reason) {
  if (!id || !normalizedPubkey() || recentlyWarmed(id)) return;
  warmedAt.set(id, Date.now());
  void fetchWithSession("/api/thread-warm", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ id, reason }),
  }).then((response) => {
    if (response.status === 503) warmedAt.delete(id);
  }).catch(() => warmedAt.delete(id));
}

function scheduleHover(target) {
  if (!allowSpeculativeWork()) return;
  const intent = intentTarget(target);
  if (!intent || recentlyWarmed(intent.id) || timers.has(intent.id)) return;
  const timer = window.setTimeout(() => {
    timers.delete(intent.id);
    dispatchWarm(intent.id, "hover");
  }, HOVER_DELAY_MS);
  timers.set(intent.id, timer);
}

function cancelHover(target) {
  const intent = intentTarget(target);
  if (!intent) return;
  const timer = timers.get(intent.id);
  if (timer !== undefined) window.clearTimeout(timer);
  timers.delete(intent.id);
}

function dispatchImmediate(target, reason) {
  const intent = intentTarget(target);
  if (!intent) return;
  cancelHover(target);
  dispatchWarm(intent.id, reason);
}

export function initThreadIntentWarm(root = document) {
  if (!appBootstrap().features?.desktopShell || !normalizedPubkey()) return;
  if (root.documentElement?.dataset?.ptxtThreadIntentWarm === "1") return;
  if (root.documentElement) root.documentElement.dataset.ptxtThreadIntentWarm = "1";
  root.addEventListener("pointerover", (event) => scheduleHover(event.target), { passive: true });
  root.addEventListener("pointerout", (event) => cancelHover(event.target), { passive: true });
  root.addEventListener("focusin", (event) => dispatchImmediate(event.target, "focus"), { passive: true });
  root.addEventListener("pointerdown", (event) => dispatchImmediate(event.target, "pointer"), { passive: true });
  root.addEventListener("touchstart", (event) => dispatchImmediate(event.target, "pointer"), { passive: true });
}

function resetThreadIntentWarmForTests() {
  for (const timer of timers.values()) window.clearTimeout(timer);
  timers.clear();
  warmedAt.clear();
}

export const threadIntentWarmInternals = {
  threadIDFromURL,
  recentlyWarmed,
  resetThreadIntentWarmForTests,
};
