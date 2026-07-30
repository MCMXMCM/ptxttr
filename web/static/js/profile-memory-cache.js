import { normalizePubkey } from "./relay-utils.js";

const profiles = new Map();
export const PROFILE_MEMORY_CACHE_UPDATED_EVENT = "ptxt:profile-memory-cache-updated";

function renderableFieldCount(profile = {}) {
  return [
    profile.display_name,
    profile.name,
    profile.about,
    profile.picture,
    profile.avatar_url,
    profile.nip05,
    profile.website,
    profile.lud16,
    profile.lud06,
  ].reduce((count, value) => count + (String(value || "").trim() ? 1 : 0), 0);
}

function shouldReplace(current, next) {
  if (!current) return true;
  const nextCreated = Number(next?.created_at || 0);
  const currentCreated = Number(current?.created_at || 0);
  if (nextCreated > currentCreated) return true;
  if (nextCreated < currentCreated) return false;
  return renderableFieldCount(next) >= renderableFieldCount(current);
}

function emitProfileUpdated(profile) {
  const target = globalThis.window || globalThis;
  if (!target || typeof target.dispatchEvent !== "function") return;
  let event;
  if (typeof globalThis.CustomEvent === "function") {
    event = new CustomEvent(PROFILE_MEMORY_CACHE_UPDATED_EVENT, { detail: { profile } });
  } else if (typeof globalThis.Event === "function") {
    event = new Event(PROFILE_MEMORY_CACHE_UPDATED_EVENT);
    event.detail = { profile };
  } else {
    return;
  }
  target.dispatchEvent(event);
}

export function rememberProfile(profile) {
  const pk = normalizePubkey(profile?.pubkey);
  if (!pk) return null;
  const next = { ...profile, pubkey: pk };
  const current = profiles.get(pk);
  if (shouldReplace(current, next)) {
    profiles.set(pk, next);
    emitProfileUpdated(next);
    return next;
  }
  return current;
}

export function rememberProfiles(profilesByPubkey = {}) {
  for (const [pubkey, profile] of Object.entries(profilesByPubkey || {})) {
    rememberProfile({ ...profile, pubkey: profile?.pubkey || pubkey });
  }
  return profilesByPubkey;
}

export function cachedProfile(pubkey) {
  const pk = normalizePubkey(pubkey);
  return pk ? profiles.get(pk) || null : null;
}

export function cachedProfiles(pubkeys = []) {
  const out = {};
  for (const raw of pubkeys || []) {
    const pk = normalizePubkey(raw);
    const profile = cachedProfile(pk);
    if (pk && profile) out[pk] = profile;
  }
  return out;
}

function mergeProfileFields(baseProfile, fallbackProfile) {
  if (!fallbackProfile) return baseProfile;
  return {
    ...fallbackProfile,
    ...baseProfile,
    about: String(baseProfile?.about || fallbackProfile?.about || "").trim(),
    display_name: String(baseProfile?.display_name || fallbackProfile?.display_name || "").trim(),
    name: String(baseProfile?.name || fallbackProfile?.name || "").trim(),
    avatar_url: String(baseProfile?.avatar_url || fallbackProfile?.avatar_url || "").trim(),
    picture: String(baseProfile?.picture || fallbackProfile?.picture || "").trim(),
    website: String(baseProfile?.website || fallbackProfile?.website || "").trim(),
    nip05: String(baseProfile?.nip05 || fallbackProfile?.nip05 || "").trim(),
    lud16: String(baseProfile?.lud16 || fallbackProfile?.lud16 || "").trim(),
    lud06: String(baseProfile?.lud06 || fallbackProfile?.lud06 || "").trim(),
    event_id: String(baseProfile?.event_id || fallbackProfile?.event_id || "").trim(),
    created_at: Number(baseProfile?.created_at || fallbackProfile?.created_at || 0) || 0,
  };
}

export function mergeCachedProfilesByPubkey(pubkeys = [], ...profileMaps) {
  const keys = new Set((pubkeys || []).map(normalizePubkey).filter(Boolean));
  profileMaps.forEach((profilesByPubkey) => {
    Object.keys(profilesByPubkey || {}).forEach((pubkey) => {
      const pk = normalizePubkey(pubkey);
      if (pk) keys.add(pk);
    });
  });
  const out = {};
  keys.forEach((pk) => {
    let merged = null;
    profileMaps.forEach((profilesByPubkey) => {
      const profile = profilesByPubkey?.[pk];
      if (!profile) return;
      merged = mergeProfileFields({ ...profile, pubkey: normalizePubkey(profile.pubkey) || pk }, merged);
    });
    const memory = cachedProfile(pk);
    if (memory) merged = mergeProfileFields(memory, merged);
    if (merged) out[pk] = merged;
  });
  return out;
}

export function clearProfileMemoryCache() {
  profiles.clear();
}
