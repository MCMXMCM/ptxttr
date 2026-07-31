import { normalizedPubkey } from "./session.js";
import {
  feedSortForSession,
  getEffectiveLoggedOutWebOfTrustSeed,
  getFeedSortPref,
  getWebOfTrustDepthPref,
  getWebOfTrustEnabledPref,
  WEB_OF_TRUST_SEED_PRESETS,
} from "./sort-prefs.js";
import { DEFAULT_LOGGED_OUT_WOT_SEED_NPUB, desktopModeEnabled } from "./viewer-defaults.js";

const LOGIN_LINK = '<a href="/login">Login</a>';

/** Map seed npub to preset label (mirrors server loggedOutWOTSeedDisplayName). */
export function loggedOutSeedDisplayName(seedNpub) {
  const normalized = String(seedNpub || "").trim().toLowerCase();
  if (!normalized) {
    return WEB_OF_TRUST_SEED_PRESETS[0]?.label || "Gigi";
  }
  const preset = WEB_OF_TRUST_SEED_PRESETS.find((entry) => entry.value.toLowerCase() === normalized);
  if (preset) return preset.label;
  return "Gigi";
}

export function effectiveFeedSort(urlLike) {
  const url = urlLike instanceof URL ? urlLike : new URL(String(urlLike || "/"), "http://localhost");
  const pubkey = normalizedPubkey();
  const raw = url.searchParams.get("sort") || getFeedSortPref() || "";
  return feedSortForSession(pubkey, raw) || "recent";
}

/** Compact signature of viewer + feed heading prefs for cache busting and stale DOM checks. */
export function feedHeadingPrefSignature(urlLike = window.location.href) {
  const pubkey = normalizedPubkey();
  const sort = effectiveFeedSort(urlLike);
  const wotEnabled = getWebOfTrustEnabledPref();
  const wotDepth = getWebOfTrustDepthPref();
  const seedNpub = pubkey ? "" : getEffectiveLoggedOutWebOfTrustSeed();
  return [
    pubkey || "logged-out",
    sort,
    wotEnabled ? "wot1" : "wot0",
    `${wotDepth}`,
    seedNpub.toLowerCase(),
  ].join("|");
}

export function feedHeadingSummaryText({
  loggedOut = false,
  seedDisplayName = "Gigi",
  seedNpub = "",
} = {}) {
  if (!loggedOut) {
    return "";
  }
  const name = seedDisplayName || "Gigi";
  const npub = String(seedNpub || DEFAULT_LOGGED_OUT_WOT_SEED_NPUB).trim();
  return `The default view is seeded from <a href="/u/${npub}">${name}'s</a> web of trust. ${LOGIN_LINK} to use your own.`;
}

function sortOptionsMarkup(sort, loggedOut) {
  const recentSelected = sort === "recent" ? " selected" : "";
  if (loggedOut) {
    const trend7Selected = sort === "trend7d" ? " selected" : "";
    const trend24Selected = sort === "trend24h" ? " selected" : "";
    return `
                  <option value="recent"${recentSelected}>Chronological</option>
                  <option value="trend7d"${trend7Selected}>7 Day Trend</option>
                  <option value="trend24h"${trend24Selected}>1 Day Trend</option>`;
  }
  const trend24Selected = sort === "trend24h" ? " selected" : "";
  const trend7Selected = sort === "trend7d" ? " selected" : "";
  return `
                  <option value="recent"${recentSelected}>Chronological</option>
                  <option value="trend24h"${trend24Selected}>1 Day Trend</option>
                  <option value="trend7d"${trend7Selected}>7 Day Trend</option>`;
}

function wotControlsMarkup(wotDepth) {
  const d1 = wotDepth === 1 ? " selected" : "";
  const d2 = wotDepth === 2 ? " selected" : "";
  const d3 = wotDepth === 3 ? " selected" : "";
  return `
              <div class="feed-wot-quick feed-heading-wot" data-feed-wot-controls data-wot-depth="${wotDepth}">
                <label class="feed-wot-quick-label" for="feed-wot-depth-feed">WOT</label>
                <select id="feed-wot-depth-feed" class="feed-wot-depth-select" data-feed-wot-depth-select aria-label="Web of Trust depth">
                  <option value="1"${d1}>wot: 1°</option>
                  <option value="2"${d2}>wot: 2°</option>
                  <option value="3"${d3}>wot: 3°</option>
                </select>
              </div>`;
}

export function renderFeedHeadingMarkup(urlLike = window.location.href) {
  const loggedOut = !normalizedPubkey();
  const sort = effectiveFeedSort(urlLike);
  const wotEnabled = getWebOfTrustEnabledPref();
  const wotDepth = getWebOfTrustDepthPref();
  const seedNpub = loggedOut ? getEffectiveLoggedOutWebOfTrustSeed() : "";
  const seedDisplayName = loggedOut ? loggedOutSeedDisplayName(seedNpub) : "";
  const summary = feedHeadingSummaryText({
    loggedOut,
    seedDisplayName,
    seedNpub,
  });
  const newNoteButton = loggedOut
    ? ""
    : '<button type="button" class="rail-post feed-heading-post" data-post-trigger>New Note</button>';
  const wotControls = wotEnabled && (!loggedOut || desktopModeEnabled()) ? wotControlsMarkup(wotDepth) : "";
  const summaryMarkup = loggedOut
    ? `<p class="muted feed-heading-summary">${summary}</p>`
    : "";
  return `
    <section class="page-heading">
      <div class="ascii-border">+----------------------------------------------------------------+</div>
      <div class="ascii-content">
        <div class="feed-heading-top">
          <div class="feed-heading-title-row">
            <div class="feed-heading-controls">
              <label class="feed-sort-label">Sort
                <select data-feed-sort-select>${sortOptionsMarkup(sort, loggedOut)}
                </select>
              </label>
              ${wotControls}${newNoteButton}
            </div>
          </div>
          ${summaryMarkup}
        </div>
      </div>
      <div class="ascii-border">+----------------------------------------------------------------+</div>
    </section>
  `;
}

export function stampFeedHeadingSignature(headingNode, urlLike = window.location.href) {
  if (!headingNode) return;
  headingNode.dataset.feedHeadingSignature = feedHeadingPrefSignature(urlLike);
}

export function feedHeadingNeedsRefresh(headingNode, urlLike = window.location.href) {
  if (!headingNode) return false;
  const stored = headingNode.dataset.feedHeadingSignature || "";
  return stored !== feedHeadingPrefSignature(urlLike);
}

export function applyFeedHeadingMarkup(headingNode, urlLike = window.location.href) {
  if (!headingNode) return;
  headingNode.innerHTML = renderFeedHeadingMarkup(urlLike);
  stampFeedHeadingSignature(headingNode, urlLike);
}
