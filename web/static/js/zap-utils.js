import { canonicalHex64, normalizePubkey } from "./relay-utils.js";

export function formatCompactSats(value) {
  const sats = Math.max(0, Math.floor(Number(value) || 0));
  if (sats < 1000) return String(sats);
  return `${(sats / 1000).toFixed(1).replace(/\.0$/, "")}k`;
}

function firstTagValue(tags, name, normalize = null) {
  for (const tag of tags || []) {
    if (!Array.isArray(tag) || tag.length < 2 || tag[0] !== name) continue;
    const value = String(tag[1] || "").trim();
    if (!value) continue;
    if (typeof normalize === "function") {
      const normalized = normalize(value);
      if (normalized) return normalized;
      continue;
    }
    return value;
  }
  return "";
}

function zapRequestDescription(event) {
  const raw = firstTagValue(event?.tags || [], "description");
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw);
    return parsed && typeof parsed === "object" ? parsed : null;
  } catch {
    return null;
  }
}

function descriptionTagValue(description, name, normalize = null) {
  const tags = Array.isArray(description?.tags) ? description.tags : [];
  return firstTagValue(tags, name, normalize);
}

export function zapTargetNoteID(event) {
  return (
    firstTagValue(event?.tags || [], "e", canonicalHex64) ||
    descriptionTagValue(zapRequestDescription(event), "e", canonicalHex64) ||
    ""
  );
}

export function zapTargetPubkey(event) {
  return (
    firstTagValue(event?.tags || [], "p", normalizePubkey) ||
    descriptionTagValue(zapRequestDescription(event), "p", normalizePubkey) ||
    ""
  );
}

export function zapAmountMillisats(event) {
  const topLevel = firstTagValue(event?.tags || [], "amount");
  if (topLevel) {
    const amount = Number.parseInt(topLevel, 10);
    if (Number.isFinite(amount) && amount > 0) return amount;
  }
  const descriptionAmount = descriptionTagValue(zapRequestDescription(event), "amount");
  if (!descriptionAmount) return 0;
  const amount = Number.parseInt(descriptionAmount, 10);
  return Number.isFinite(amount) && amount > 0 ? amount : 0;
}

export function zapAmountSats(event) {
  const millisats = zapAmountMillisats(event);
  if (!millisats) return 0;
  return Math.floor(millisats / 1000);
}

export function zapSenderPubkey(event) {
  const request = zapRequestDescription(event);
  return normalizePubkey(request?.pubkey || "");
}

export function zapMessage(event) {
  const request = zapRequestDescription(event);
  const value = String(request?.content || "").trim();
  return value || "";
}
