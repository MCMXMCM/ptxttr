import { nip19 } from "../lib/nostr-tools.js";
import { canonicalHex64 } from "./relay-utils.js";

/** Parse kind-0 profile metadata (mirrors nostrx.ParseProfile). */
export function parseProfile(pubkey, event) {
  const pk = canonicalHex64(pubkey);
  const base = {
    pubkey: pk,
    name: "",
    display_name: "",
    about: "",
    picture: "",
    website: "",
    nip05: "",
    lud16: "",
    lud06: "",
    event_id: "",
    created_at: 0,
    content: "",
    avatar_url: "",
  };
  if (!event) return base;
  base.event_id = String(event.id || "");
  base.created_at = Number(event.created_at || 0) || 0;
  base.content = String(event.content || "");
  try {
    const raw = JSON.parse(event.content || "{}");
    base.name = String(raw.name || "").trim();
    base.display_name = String(raw.display_name || "").trim();
    base.about = String(raw.about || "").trim();
    base.picture = String(raw.picture || "").trim();
    base.website = String(raw.website || "").trim();
    base.nip05 = String(raw.nip05 || "").trim();
    base.lud16 = String(raw.lud16 || "").trim();
    base.lud06 = String(raw.lud06 || "").trim();
  } catch {
    // keep empty fields
  }
  base.avatar_url = avatarURLFor(pk, base.picture);
  return base;
}

export function displayName(profile) {
  const display = String(profile?.display_name || "").trim();
  if (display) return display;
  const name = String(profile?.name || "").trim();
  if (name) return name;
  return shortNpubLabel(profile?.pubkey);
}

export function preferredAvatarURL(profile) {
  return String(profile?.avatar_url || profile?.picture || "").trim();
}

export function avatarRetryURL(profile) {
  const picture = String(profile?.picture || "").trim();
  if (!picture) return "";
  return picture === preferredAvatarURL(profile) ? "" : picture;
}

function profileAvatarBaseOrigin() {
  if (typeof window !== "undefined" && window.location?.origin) return window.location.origin;
  if (globalThis.location?.origin) return globalThis.location.origin;
  return "http://localhost";
}

export function normalizeProfileAvatarURL(value, baseOrigin = profileAvatarBaseOrigin()) {
  const raw = String(value || "").trim();
  if (!raw || raw.startsWith("data:") || raw.startsWith("blob:")) return raw;
  try {
    return new URL(raw, baseOrigin).href;
  } catch {
    return raw;
  }
}

function avatarProxyIdentity(url, baseOrigin) {
  if (!url) return null;
  try {
    const parsed = new URL(url, baseOrigin);
    const origin = new URL(baseOrigin).origin;
    if (parsed.origin !== origin || !parsed.pathname.startsWith("/avatar/")) return null;
    return {
      path: `${parsed.origin}${parsed.pathname}`,
      version: parsed.searchParams.get("v") || "",
    };
  } catch {
    return null;
  }
}

export function profileAvatarURLsMatch(currentURL, expectedURL, baseOrigin = profileAvatarBaseOrigin()) {
  const current = normalizeProfileAvatarURL(currentURL, baseOrigin);
  const expected = normalizeProfileAvatarURL(expectedURL, baseOrigin);
  if (current === expected) return true;
  const currentProxy = avatarProxyIdentity(current, baseOrigin);
  const expectedProxy = avatarProxyIdentity(expected, baseOrigin);
  if (!currentProxy || !expectedProxy || currentProxy.path !== expectedProxy.path) return false;
  return !currentProxy.version || !expectedProxy.version || currentProxy.version === expectedProxy.version;
}

export function nip05DisplayText(nip05) {
  const raw = String(nip05 || "").trim();
  const at = raw.lastIndexOf("@");
  if (at <= 0 || at >= raw.length - 1) return raw;
  const localPart = raw.slice(0, at).trim().toLowerCase();
  const domain = raw.slice(at + 1).trim().toLowerCase();
  if (!localPart || !domain) return raw;
  if (!/^[a-z0-9_.-]+$/.test(localPart) || /[/ @]/.test(domain)) return raw;
  return localPart === "_" ? domain : raw;
}

export function shortNpubLabel(pubkey) {
  const pk = canonicalHex64(pubkey);
  if (!pk) return "";
  let npub = pk;
  try {
    npub = nip19.npubEncode(pk);
  } catch {
    // fall back to the raw key when encoding fails
  }
  if (npub.length <= 12) return npub;
  return `${npub.slice(0, 8)}..${npub.slice(-4)}`;
}

export function isFallbackProfileLabel(label, pubkey) {
  const value = String(label || "").trim();
  const pk = canonicalHex64(pubkey);
  if (!value || !pk) return true;
  if (
    value === pk ||
    value === pk.slice(0, 12) ||
    value === `${pk.slice(0, 8)}…${pk.slice(-4)}` ||
    value === shortNpubLabel(pk)
  ) return true;
  try {
    return value === nip19.npubEncode(pk);
  } catch {
    return false;
  }
}

/** Route cacheable profile pictures through the server avatar proxy. */
export function avatarURLFor(pubkey, picture) {
  const url = String(picture || "").trim();
  if (!url) return "";
  if (/^(?:data|blob):/i.test(url) || url.startsWith("/")) return url;
  const pk = canonicalHex64(pubkey);
  if (!pk) return url;
  return `/avatar/${encodeURIComponent(pk)}`;
}

export function profileAPIEntry(profile) {
  return {
    name: String(profile?.name || ""),
    display_name: String(profile?.display_name || ""),
    about: String(profile?.about || "").trim(),
    picture: String(profile?.picture || ""),
    avatar_url: String(profile?.avatar_url || ""),
  };
}
