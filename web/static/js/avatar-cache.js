import {
  STORE_AVATARS,
  openClientDB,
  requestResult,
  transactionDone,
} from "./client-store.js";

const maxAvatarBytes = 2 * 1024 * 1024;
const maxTotalAvatarBytes = 32 * 1024 * 1024;
const maxAvatarRecords = 500;
const targetTotalAvatarBytes = 24 * 1024 * 1024;
const targetAvatarRecords = 400;
const maintenanceIntervalMS = 60_000;
const freshTTL = 7 * 24 * 60 * 60 * 1000;
const memory = new Map();
const inFlight = new Map();
let lastMaintenanceAt = 0;
let maintenancePromise = null;

function normalizeAvatarURL(value) {
  const raw = String(value || "").trim();
  if (!raw || raw.startsWith("data:") || raw.startsWith("blob:")) return raw;
  try {
    return new URL(raw, window.location.origin).href;
  } catch {
    return "";
  }
}

function preserveAvatarBox(img) {
  if (!(img instanceof HTMLImageElement)) return;
  const width = Number(img.getAttribute("width")) || img.clientWidth || img.naturalWidth || 0;
  const height = Number(img.getAttribute("height")) || img.clientHeight || img.naturalHeight || width || 0;
  if (width > 0 && !img.getAttribute("width")) img.setAttribute("width", `${width}`);
  if (height > 0 && !img.getAttribute("height")) img.setAttribute("height", `${height}`);
  if (!img.style.aspectRatio && width > 0 && height > 0) {
    img.style.aspectRatio = `${width} / ${height}`;
  }
}

function isCacheableURL(url) {
  if (!url) return false;
  try {
    const parsed = new URL(url, window.location.origin);
    return parsed.protocol === "http:" || parsed.protocol === "https:";
  } catch {
    return false;
  }
}

function shouldClientCacheAvatarURL(url) {
  if (!isCacheableURL(url)) return false;
  try {
    const parsed = new URL(url, window.location.origin);
    return parsed.origin === window.location.origin;
  } catch {
    return false;
  }
}

function rememberAvatarURL(url, blob) {
  const prior = memory.get(url);
  if (prior?.objectURL) URL.revokeObjectURL(prior.objectURL);
  memory.set(url, { src: url, size: blob?.size || 0, saved_at: Date.now() });
  return url;
}

async function readAvatar(url) {
  try {
    const db = await openClientDB();
    const tx = db.transaction(STORE_AVATARS, "readonly");
    return requestResult(tx.objectStore(STORE_AVATARS).get(url));
  } catch {
    return null;
  }
}

async function writeAvatar(url, blob) {
  if (!url || !(blob instanceof Blob) || !blob.size || blob.size > maxAvatarBytes) return null;
  try {
    const now = Date.now();
    const db = await openClientDB();
    const tx = db.transaction(STORE_AVATARS, "readwrite");
    const record = { url, blob, size: blob.size, type: blob.type || "", saved_at: now, last_used_at: now };
    tx.objectStore(STORE_AVATARS).put(record);
    await transactionDone(tx);
    scheduleAvatarCacheMaintenance();
    return record;
  } catch {
    return null;
  }
}

function scheduleAvatarCacheMaintenance({ force = false } = {}) {
  const now = Date.now();
  if (!force && now - lastMaintenanceAt < maintenanceIntervalMS) return maintenancePromise;
  if (maintenancePromise) return maintenancePromise;
  lastMaintenanceAt = now;
  maintenancePromise = pruneAvatarCache().catch(() => 0).finally(() => {
    maintenancePromise = null;
  });
  return maintenancePromise;
}

export async function pruneAvatarCache({
  maxBytes = maxTotalAvatarBytes,
  maxRecords = maxAvatarRecords,
  targetBytes = targetTotalAvatarBytes,
  targetRecords = targetAvatarRecords,
} = {}) {
  const db = await openClientDB();
  const readTx = db.transaction(STORE_AVATARS, "readonly");
  const rows = await requestResult(readTx.objectStore(STORE_AVATARS).getAll());
  await transactionDone(readTx);
  const avatars = (Array.isArray(rows) ? rows : []).map((row) => ({
    url: String(row?.url || ""),
    size: Math.max(0, Number(row?.size || row?.blob?.size || 0)),
    lastUsedAt: Number(row?.last_used_at || row?.saved_at || 0),
  })).filter((row) => row.url);
  const totalBytes = avatars.reduce((sum, row) => sum + row.size, 0);
  if (avatars.length <= maxRecords && totalBytes <= maxBytes) return 0;

  const keepRecords = Math.max(1, Math.min(Number(targetRecords) || maxRecords, maxRecords));
  const keepBytes = Math.max(1, Math.min(Number(targetBytes) || maxBytes, maxBytes));
  avatars.sort((a, b) => a.lastUsedAt - b.lastUsedAt);
  const stale = [];
  let remainingRecords = avatars.length;
  let remainingBytes = totalBytes;
  for (const row of avatars) {
    if (remainingRecords <= keepRecords && remainingBytes <= keepBytes) break;
    stale.push(row.url);
    remainingRecords -= 1;
    remainingBytes -= row.size;
  }
  if (!stale.length) return 0;

  const writeTx = db.transaction(STORE_AVATARS, "readwrite");
  const store = writeTx.objectStore(STORE_AVATARS);
  stale.forEach((url) => {
    store.delete(url);
    const cached = memory.get(url);
    if (cached?.objectURL) URL.revokeObjectURL(cached.objectURL);
    memory.delete(url);
  });
  await transactionDone(writeTx);
  if (remainingRecords > maxRecords || remainingBytes > maxBytes) {
    scheduleAvatarCacheMaintenance({ force: true });
  }
  return stale.length;
}

async function touchAvatar(url, record) {
  if (!record) return;
  try {
    const db = await openClientDB();
    const tx = db.transaction(STORE_AVATARS, "readwrite");
    tx.objectStore(STORE_AVATARS).put({ ...record, last_used_at: Date.now() });
    await transactionDone(tx);
  } catch {
    // Last-used timestamps are opportunistic.
  }
}

async function fetchAndCacheAvatar(url) {
  if (!shouldClientCacheAvatarURL(url)) return null;
  if (inFlight.has(url)) return inFlight.get(url);
  const promise = fetch(url, {
    cache: "force-cache",
    credentials: new URL(url).origin === window.location.origin ? "same-origin" : "omit",
    mode: "cors",
  })
    .then(async (response) => {
      if (!response.ok) return null;
      const type = String(response.headers.get("content-type") || "").toLowerCase();
      if (type && !type.startsWith("image/")) return null;
      const blob = await response.blob();
      if (!blob.size || blob.size > maxAvatarBytes) return null;
      const finalURL = normalizeAvatarURL(response.url || url) || url;
      if (!shouldClientCacheAvatarURL(finalURL)) return url;
      await writeAvatar(finalURL, blob);
      return rememberAvatarURL(finalURL, blob);
    })
    .catch(() => null)
    .finally(() => {
      inFlight.delete(url);
    });
  inFlight.set(url, promise);
  return promise;
}

async function cachedAvatarSrc(url) {
  if (!shouldClientCacheAvatarURL(url)) return null;
  const cached = memory.get(url);
  if (cached?.src) return cached.src;
  const record = await readAvatar(url);
  if (record?.blob instanceof Blob) {
    const src = rememberAvatarURL(url, record.blob);
    void touchAvatar(url, record);
    if (Date.now() - Number(record.saved_at || 0) > freshTTL) void fetchAndCacheAvatar(url);
    return src;
  }
  return null;
}

export function setAvatarImageSource(img, avatarURL, { retryURL = undefined } = {}) {
  if (!(img instanceof HTMLImageElement)) return;
  preserveAvatarBox(img);
  if (!img.decoding) img.decoding = "async";
  if (!img.loading) img.loading = "lazy";
  const url = normalizeAvatarURL(avatarURL);
  const previousURL = img.dataset.ptxtAvatarOriginalSrc || "";
  if (previousURL !== url) delete img.dataset.ptxtAvatarRetryAttempted;
  if (retryURL !== undefined) {
    const retry = normalizeAvatarURL(retryURL);
    if (retry && retry !== url) img.dataset.ptxtAvatarRetrySrc = retry;
    else delete img.dataset.ptxtAvatarRetrySrc;
  }
  img.dataset.ptxtAvatarOriginalSrc = url;
  if (!url) {
    img.removeAttribute("src");
    return;
  }
  if (!isCacheableURL(url) || url.startsWith("data:") || url.startsWith("blob:")) {
    if (img.getAttribute("src") !== url) img.src = url;
    return;
  }
  if (!shouldClientCacheAvatarURL(url)) {
    if (img.getAttribute("src") !== url) img.src = url;
    return;
  }
  // Bare /avatar/<pubkey> requests already redirect to the fingerprinted
  // immutable URL. Let that single image request discover the canonical URL;
  // a separate /api/avatar-meta call per visible card caused origin storms and
  // could consume the anonymous route limiter before a thread navigation.
  const cached = memory.get(url);
  if (cached?.src) {
    if (img.getAttribute("src") !== cached.src) img.src = cached.src;
    void fetchAndCacheAvatar(url);
    return;
  }
  void cachedAvatarSrc(url).then((src) => {
    if (src && img.dataset.ptxtAvatarOriginalSrc === url) {
      img.src = src;
      return;
    }
    if (!img.getAttribute("src") && img.dataset.ptxtAvatarOriginalSrc === url) {
      img.src = url;
    }
  });
  if (!img.getAttribute("src")) img.src = url;
  void fetchAndCacheAvatar(url).then((src) => {
    if (src && img.dataset.ptxtAvatarOriginalSrc === url) img.src = src;
  });
}
