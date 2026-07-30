export const POWER_MODE_KEY = "ptxt_power_mode";

const MOBILE_POINTER_QUERY = "(pointer: coarse)";
const NARROW_VIEWPORT_QUERY = "(max-width: 760px)";

export function configuredPowerMode() {
  try {
    if (typeof localStorage === "undefined") return "auto";
    const raw = String(localStorage.getItem(POWER_MODE_KEY) || "auto").toLowerCase();
    if (raw === "full" || raw === "saver" || raw === "auto") return raw;
  } catch {
    // ignore storage denial
  }
  return "auto";
}

export function isMobileLikeDevice() {
  const nav = typeof navigator !== "undefined" ? navigator : {};
  const win = typeof window !== "undefined" ? window : {};
  const coarse = typeof matchMedia !== "undefined" && matchMedia(MOBILE_POINTER_QUERY).matches;
  const narrow = typeof matchMedia !== "undefined" && matchMedia(NARROW_VIEWPORT_QUERY).matches;
  const narrowViewport = Number(win.innerWidth || 0) > 0 && Number(win.innerWidth || 0) <= 760;
  const touch = Number(nav.maxTouchPoints || 0) > 0;
  const lowCore = Number(nav.hardwareConcurrency || 0) > 0 && Number(nav.hardwareConcurrency || 0) <= 4;
  const lowMemory = Number(nav.deviceMemory || 0) > 0 && Number(nav.deviceMemory || 0) <= 4;
  return Boolean(coarse || touch || narrowViewport || (narrow && (lowCore || lowMemory)));
}

export function connectionSaveDataEnabled() {
  return Boolean(typeof navigator !== "undefined" && navigator.connection?.saveData);
}

export function pageIsHidden() {
  return typeof document !== "undefined" && document.visibilityState === "hidden";
}

export function powerSaverActive() {
  const mode = configuredPowerMode();
  if (mode === "full") return false;
  if (mode === "saver") return true;
  return pageIsHidden() || connectionSaveDataEnabled();
}

export function allowSpeculativeWork() {
  return configuredPowerMode() === "full" || (!powerSaverActive() && !pageIsHidden());
}

export function powerLimitedCount(full, saver) {
  return powerSaverActive() ? saver : full;
}

export function powerLimitedTimeoutMs(full, saver) {
  return powerSaverActive() ? saver : full;
}

export function onPowerStateChange(callback) {
  if (typeof callback !== "function") return () => {};
  const onChange = () => callback(powerSaverActive());
  const doc = typeof document !== "undefined" ? document : null;
  const win = typeof window !== "undefined" ? window : null;
  const nav = typeof navigator !== "undefined" ? navigator : null;
  const onStorage = (event) => {
    if (event.key === POWER_MODE_KEY) onChange();
  };
  doc?.addEventListener?.("visibilitychange", onChange);
  win?.addEventListener?.("focus", onChange);
  win?.addEventListener?.("pageshow", onChange);
  win?.addEventListener?.("storage", onStorage);
  const conn = nav?.connection;
  conn?.addEventListener?.("change", onChange);
  return () => {
    doc?.removeEventListener?.("visibilitychange", onChange);
    win?.removeEventListener?.("focus", onChange);
    win?.removeEventListener?.("pageshow", onChange);
    win?.removeEventListener?.("storage", onStorage);
    conn?.removeEventListener?.("change", onChange);
  };
}
