import { normalizePubkey } from "./relay-utils.js";

export const FIRST_LOGIN_BOOTSTRAP_KEY = "ptxt_first_login_bootstrap_pubkey";
export const BOOTSTRAPPED_VIEWERS_KEY = "ptxt_bootstrapped_viewers";

function normalizedViewer(pubkey) {
  return normalizePubkey(String(pubkey || "").trim());
}

function readCompletedViewers() {
  try {
    const raw = localStorage.getItem(BOOTSTRAPPED_VIEWERS_KEY);
    const parsed = JSON.parse(raw || "[]");
    if (!Array.isArray(parsed)) return new Set();
    return new Set(parsed.map((value) => normalizedViewer(value)).filter(Boolean));
  } catch {
    return new Set();
  }
}

function writeCompletedViewers(viewers) {
  try {
    localStorage.setItem(BOOTSTRAPPED_VIEWERS_KEY, JSON.stringify([...viewers]));
  } catch {
    // ignore
  }
}

export function bootstrapPendingViewer() {
  try {
    return normalizedViewer(sessionStorage.getItem(FIRST_LOGIN_BOOTSTRAP_KEY));
  } catch {
    return "";
  }
}

export function hasCompletedBootstrap(pubkey) {
  const viewer = normalizedViewer(pubkey);
  if (!viewer) return false;
  return readCompletedViewers().has(viewer);
}

export function markBootstrapPending(pubkey) {
  const viewer = normalizedViewer(pubkey);
  if (!viewer || hasCompletedBootstrap(viewer)) {
    clearBootstrapPending();
    return false;
  }
  try {
    sessionStorage.setItem(FIRST_LOGIN_BOOTSTRAP_KEY, viewer);
    return true;
  } catch {
    return false;
  }
}

export function markBootstrapComplete(pubkey) {
  const viewer = normalizedViewer(pubkey);
  if (!viewer) return false;
  const completed = readCompletedViewers();
  completed.add(viewer);
  writeCompletedViewers(completed);
  if (bootstrapPendingViewer() === viewer) clearBootstrapPending();
  return true;
}

export function clearBootstrapPending() {
  try {
    sessionStorage.removeItem(FIRST_LOGIN_BOOTSTRAP_KEY);
  } catch {
    // ignore
  }
}

export function clearBootstrapPendingIfViewerChanged(pubkey) {
  const viewer = normalizedViewer(pubkey);
  const pending = bootstrapPendingViewer();
  if (!pending) return;
  if (viewer && pending === viewer) return;
  clearBootstrapPending();
}

export function shouldShowFirstLoginBootstrap(pubkey) {
  const viewer = normalizedViewer(pubkey);
  if (!viewer) return false;
  return bootstrapPendingViewer() === viewer && !hasCompletedBootstrap(viewer);
}
