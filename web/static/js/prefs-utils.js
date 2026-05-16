/** Shared localStorage pref helpers (no session/sort-prefs imports). */

export function prefUnset(key) {
  try {
    const raw = localStorage.getItem(key);
    return raw == null || String(raw).trim() === "";
  } catch {
    return true;
  }
}

/** Matches server-side ParseBool truthy tokens. */
export function isTruthyToken(value) {
  const raw = String(value ?? "").trim().toLowerCase();
  return raw === "1" || raw === "true" || raw === "on" || raw === "yes";
}
