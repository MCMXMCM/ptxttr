import {
  openClientDB,
  requestResult,
  transactionDone,
  STORE_EVENTS,
  STORE_TAG_INDEX,
  STORE_PROFILES,
  STORE_ROUTES,
  STORE_FEED_PAGES,
  STORE_THREAD_BUNDLES,
  STORE_METADATA,
  STORE_FRESHNESS,
  STORE_AVATARS,
} from "./client-store.js";
import { appFeatures } from "./app/bootstrap.js";

const NOTE_KINDS = new Set([1, 6, 7, 1018, 1068, 1111, 9735, 30023]);
const METADATA_KINDS = new Set([0]);
const USER_DATA_KINDS = new Set([3, 10000, 10002, 10003]);
const DERIVED_STORES = [STORE_ROUTES, STORE_FEED_PAGES, STORE_THREAD_BUNDLES, STORE_FRESHNESS];
const BYTES_PER_GB = 1024 ** 3;

function isDesktop() {
	return Boolean(appFeatures().storageControls);
}

function formatBytes(value) {
  const bytes = Math.max(0, Number(value) || 0);
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let amount = bytes;
  let unit = "B";
  for (const next of units) {
    amount /= 1024;
    unit = next;
    if (amount < 1024) break;
  }
  return `${amount >= 10 ? amount.toFixed(1) : amount.toFixed(2)} ${unit}`;
}

function bytesFromGB(value) {
  const gb = Number(value);
  if (!Number.isFinite(gb) || gb < 0.0625 || gb > 1024) return 0;
  return Math.round(gb * BYTES_PER_GB);
}

function formatGBInput(bytes) {
  const value = Math.max(0, Number(bytes) || 0) / BYTES_PER_GB;
  return Number(value.toFixed(4)).toString();
}

async function browserUsage() {
  if (!navigator.storage?.estimate) return { usage: 0, quota: 0 };
  const estimate = await navigator.storage.estimate();
  return {
    usage: Number(estimate?.usage || 0),
    quota: Number(estimate?.quota || 0),
  };
}

async function deleteBrowserEvents(kindSet) {
  const db = await openClientDB();
  const readTx = db.transaction(STORE_EVENTS, "readonly");
  const events = await requestResult(readTx.objectStore(STORE_EVENTS).getAll());
  await transactionDone(readTx);
  const ids = (events || [])
    .filter((event) => kindSet.has(Number(event?.kind)))
    .map((event) => String(event?.id || ""))
    .filter(Boolean);
  if (!ids.length) return 0;

  const tx = db.transaction([STORE_EVENTS, STORE_TAG_INDEX], "readwrite");
  const eventStore = tx.objectStore(STORE_EVENTS);
  const tagIndex = tx.objectStore(STORE_TAG_INDEX).index("event_id");
  for (const id of ids) {
    eventStore.delete(id);
    const rows = await requestResult(tagIndex.getAllKeys(IDBKeyRange.only(id)));
    for (const key of rows || []) tx.objectStore(STORE_TAG_INDEX).delete(key);
  }
  await transactionDone(tx);
  return ids.length;
}

async function clearStores(storeNames) {
  if (!storeNames.length) return;
  const db = await openClientDB();
  const names = storeNames.filter((name) => db.objectStoreNames.contains(name));
  if (!names.length) return;
  const tx = db.transaction(names, "readwrite");
  names.forEach((name) => tx.objectStore(name).clear());
  await transactionDone(tx);
}

async function clearBrowserCache(scope) {
  if (scope === "all") {
    await clearStores([
      STORE_EVENTS,
      STORE_TAG_INDEX,
      STORE_PROFILES,
      STORE_ROUTES,
      STORE_FEED_PAGES,
      STORE_THREAD_BUNDLES,
      STORE_METADATA,
      STORE_FRESHNESS,
      STORE_AVATARS,
    ]);
    return;
  }
  const kinds = scope === "notes"
    ? NOTE_KINDS
    : scope === "metadata"
      ? METADATA_KINDS
      : USER_DATA_KINDS;
  await deleteBrowserEvents(kinds);
  const extra = scope === "metadata"
    ? [STORE_PROFILES, STORE_AVATARS]
    : scope === "user_data"
      ? [STORE_METADATA]
      : [];
  await clearStores([...DERIVED_STORES, ...extra]);
}

async function fetchServerUsage() {
  const response = await fetch("/__ptxt/desktop/storage", {
    headers: { Accept: "application/json" },
    cache: "no-store",
  });
  if (!response.ok) throw new Error(`Storage usage failed (${response.status})`);
  return response.json();
}

async function refresh(root) {
  const status = root.querySelector("[data-desktop-storage-status]");
  const [server, browser] = await Promise.all([fetchServerUsage(), browserUsage()]);
  root.querySelector("[data-storage-total]").textContent = formatBytes(
    Number(server.disk_bytes || 0) + browser.usage,
  );
  root.querySelector("[data-storage-sqlite]").textContent = formatBytes(server.disk_bytes);
  root.querySelector("[data-storage-browser]").textContent = formatBytes(browser.usage);
  root.querySelector("[data-storage-notes]").textContent =
    `${formatBytes(server.notes?.bytes)} · ${Number(server.notes?.events || 0).toLocaleString()} events`;
  root.querySelector("[data-storage-metadata]").textContent =
    `${formatBytes(server.metadata?.bytes)} · ${Number(server.metadata?.events || 0).toLocaleString()} events`;
  root.querySelector("[data-storage-user-data]").textContent =
    `${formatBytes(server.user_data?.bytes)} · ${Number(server.user_data?.events || 0).toLocaleString()} events`;
  const limitInput = root.querySelector("[data-desktop-cache-limit-gb]");
  if (limitInput && document.activeElement !== limitInput) {
    limitInput.value = formatGBInput(server.max_bytes);
  }
  const limitStatus = server.max_bytes
    ? `SQLite cache limit: ${formatBytes(server.max_bytes)}. Least-recently-used data is removed at the limit.`
    : "SQLite cache limit is not configured.";
  status.textContent = browser.quota
    ? `${limitStatus} Device browser quota: ${formatBytes(browser.quota)}.`
    : limitStatus;
  return { server, browser };
}

async function saveLimit(root) {
  const input = root.querySelector("[data-desktop-cache-limit-gb]");
  const button = root.querySelector("[data-desktop-cache-limit-save]");
  const status = root.querySelector("[data-desktop-storage-status]");
  const maxBytes = bytesFromGB(input?.value);
  if (!maxBytes) {
    status.textContent = "Enter a cache limit between 0.0625 GB (64 MB) and 1024 GB.";
    input?.focus();
    return;
  }
  if (button) button.disabled = true;
  status.textContent = "Saving cache limit...";
  try {
    const response = await fetch("/__ptxt/desktop/storage", {
      method: "PUT",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: JSON.stringify({ max_bytes: maxBytes }),
    });
    if (!response.ok) throw new Error(`Cache limit save failed (${response.status})`);
    await response.json();
    await refresh(root);
    status.textContent = `Cache limit saved at ${formatBytes(maxBytes)}. LRU cleanup runs automatically when needed.`;
  } catch (error) {
    status.textContent = error?.message || "Cache limit save failed.";
  } finally {
    if (button) button.disabled = false;
  }
}

async function clearScope(root, scope) {
  const labels = {
    notes: "note data",
    metadata: "profile metadata",
    user_data: "user data",
    all: "all cached public Nostr data",
  };
  if (!window.confirm(`Clear ${labels[scope]} from this device? Accounts, private keys, and settings will be preserved.`)) {
    return;
  }
  const status = root.querySelector("[data-desktop-storage-status]");
  const buttons = [...root.querySelectorAll("[data-desktop-storage-clear]")];
  buttons.forEach((button) => { button.disabled = true; });
  status.textContent = `Clearing ${labels[scope]}...`;
  try {
    await clearBrowserCache(scope);
    const response = await fetch("/__ptxt/desktop/storage/clear", {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: JSON.stringify({ scope }),
    });
    if (!response.ok) throw new Error(`Cache clear failed (${response.status})`);
    const result = await response.json();
    await refresh(root);
    if (result?.warning) status.textContent = result.warning;
  } catch (error) {
    status.textContent = error?.message || "Cache clear failed.";
  } finally {
    buttons.forEach((button) => { button.disabled = false; });
  }
}

export function initDesktopStorage(scope = document) {
  if (!isDesktop()) return;
  const root = scope.querySelector?.("[data-desktop-storage]");
  if (!root || root._ptxtDesktopStorageBound) return;
  root._ptxtDesktopStorageBound = true;
  root.hidden = false;
  root.querySelector("[data-desktop-storage-refresh]")?.addEventListener("click", () => {
    void refresh(root).catch((error) => {
      root.querySelector("[data-desktop-storage-status]").textContent =
        error?.message || "Storage usage failed.";
    });
  });
  root.querySelector("[data-desktop-cache-limit-save]")?.addEventListener("click", () => {
    void saveLimit(root);
  });
  root.querySelector("[data-desktop-cache-limit-gb]")?.addEventListener("keydown", (event) => {
    if (event.key !== "Enter") return;
    event.preventDefault();
    void saveLimit(root);
  });
  root.querySelectorAll("[data-desktop-storage-clear]").forEach((button) => {
    button.addEventListener("click", () => void clearScope(root, button.dataset.desktopStorageClear));
  });
  void refresh(root).catch((error) => {
    root.querySelector("[data-desktop-storage-status]").textContent =
      error?.message || "Storage usage failed.";
  });
}

export { bytesFromGB, formatBytes, formatGBInput };
