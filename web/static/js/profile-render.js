import { escapeHTML } from "./render-utils.js";

export function profileWebsiteURL(website) {
  const trimmed = String(website || "").trim();
  if (!trimmed) return "";
  try {
    const parsed = new URL(trimmed);
    if (parsed.protocol) return trimmed;
  } catch {
    // Fall through to https:// normalization.
  }
  return `https://${trimmed}`;
}

export function profileWebsiteDisplay(website) {
  const trimmed = String(website || "").trim();
  if (!trimmed) return "";
  try {
    const parsed = new URL(profileWebsiteURL(trimmed));
    if (!parsed.host) return trimmed;
    return `${parsed.host}${parsed.pathname && parsed.pathname !== "/" ? parsed.pathname : ""}`;
  } catch {
    return trimmed;
  }
}

export function profilePaymentRaw(profile) {
  const lud16 = String(profile?.lud16 || "").trim();
  if (lud16) return lud16;
  return String(profile?.lud06 || "").trim();
}

export function profileMetadataLinksHTML(profile) {
  return renderProfilePaymentHTML(profile);
}

export function renderProfilePaymentHTML(profile) {
  const payment = profilePaymentRaw(profile);
  if (!payment) return "";
  const hasLud16 = Boolean(String(profile?.lud16 || "").trim());
  const paymentLabel = escapeHTML(payment);
  const paymentBody = hasLud16
    ? `<a class="profile-payment-target" href="lightning:${paymentLabel}">
      <span class="profile-payment-symbol muted">₿</span>
      <span class="profile-payment-value">${paymentLabel}</span>
    </a>`
    : `<span class="profile-payment-symbol muted">₿</span>
    <span class="profile-payment-value">${paymentLabel}</span>`;
  return `<p class="profile-payment-line" data-profile-payment-copy data-payment="${escapeHTML(payment)}">
    ${paymentBody}
    <span class="profile-payment-actions">
      <button type="button" class="link-button profile-payment-copy-icon" data-profile-payment-copy-btn aria-label="Copy lightning address" title="Copy lightning address"><span aria-hidden="true" data-profile-payment-copy-glyph>⧉</span></button>
    </span>
  </p>`;
}

export function renderProfileHeroWebsiteHTML(website) {
  const trimmed = String(website || "").trim();
  if (!trimmed) return "";
  const href = escapeHTML(profileWebsiteURL(trimmed));
  const label = escapeHTML(profileWebsiteDisplay(trimmed));
  return `<p class="profile-website-line">
    <span class="profile-website-symbol muted">↗</span>
    <span class="profile-website-value"><a href="${href}" rel="noopener noreferrer" target="_blank">${label}</a></span>
  </p>`;
}

export function formatProfileEventDate(createdAt) {
  const ts = Number(createdAt || 0);
  if (!Number.isFinite(ts) || ts <= 0) return "";
  return new Intl.DateTimeFormat("en-US", { month: "long", year: "numeric", timeZone: "UTC" }).format(new Date(ts * 1000));
}

function formatProfileEventDateISO(createdAt) {
  const ts = Number(createdAt || 0);
  if (!Number.isFinite(ts) || ts <= 0) return "";
  const date = new Date(ts * 1000);
  const year = date.getUTCFullYear();
  const month = String(date.getUTCMonth() + 1).padStart(2, "0");
  return `${year}-${month}`;
}

export function renderProfileHeroMetadataHTML(profile) {
  const eventID = String(profile?.event_id || "").trim();
  if (!eventID) return "";
  const published = formatProfileEventDate(profile?.created_at);
  const publishedISO = formatProfileEventDateISO(profile?.created_at);
  return `metadata from <a href="/thread/${escapeHTML(eventID)}" data-relay-aware>${escapeHTML(eventID.slice(0, 12))}</a>${published ? ` <time datetime="${escapeHTML(publishedISO)}">${escapeHTML(published)}</time>` : ""}`;
}

export function renderProfileIdentHTML(profile) {
  const about = String(profile?.about || "").trim();
  const parts = [];
  if (about) {
    parts.push(`<p>${escapeHTML(about)}</p>`);
  }
  const metadataHTML = renderProfileHeroMetadataHTML(profile);
  if (metadataHTML) {
    parts.push(`<p class="muted profile-hero-metadata">${metadataHTML}</p>`);
  }
  return parts.join("");
}
