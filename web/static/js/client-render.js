import { refreshAscii, refreshAsciiSync } from "./ascii.js";
import { syncBookmarkState } from "./bookmarks.js";
import {
  clearBootstrapPendingIfViewerChanged,
  markBootstrapComplete,
  shouldShowFirstLoginBootstrap,
} from "./first-login-bootstrap.js";
import { shouldPreserveDeferredFeedLoader } from "./feed-deferred-loader.js";
import { homeFeedElement as homeFeed } from "./feed-dom.js";
import { showHomeFeedRefreshLoader } from "./feed-refresh-loader.js";
import { FEED_FIRST_PAINT_LIMIT, cachedFeedNotes, fetchFeedNotes, fetchFirstPaintFeedNotes } from "./feed-service.js";
import { renderedFeedSessionChanged, renderedFeedSort, renderedFeedSortChanged } from "./feed-render-state.js";
import {
  feedPageCursor,
  feedPageMayHaveMore,
  homeFeedLoadMoreHidden,
  isNewerThanFeedCursor,
} from "./feed-pagination.js";
import { fetchReadDetail, fetchReadsPage } from "./reads-service.js";
import { renderReadDetailView, renderReadsList } from "./read-event-render.js";
import {
  createSelectedNoteArticle,
  createSelectedNoteArticleFromShell,
  createNoteArticleFromThreadShell,
  encodeNpub,
  renderNoteFeed,
  appendNoteFeed,
} from "./note-event-render.js";
import { enrichNoteShell, hydrateReferencedEvents } from "./note-references.js";
import { refreshVisibleFeedNoteMetadata, fetchFeedNoteMetadataMaps } from "./feed-metadata.js";
import { serverReplyCountsForEvents } from "./server-feed-metadata.js";
import { wireAvatarImageFallbacks } from "./layout.js";
import { initRetroLoaders, setRetroLoaderProgress } from "./retro-loader.js";
import { initViewMore } from "./notes.js";
import { refreshVisibleNoteProfiles, rememberVisibleNoteProfiles } from "./note-profiles.js";
import { avatarRetryURL, displayName, nip05DisplayText, parseProfile, preferredAvatarURL } from "./profile-parse.js";
import {
  fetchBookmarks,
  fetchEventsByIDs,
  fetchNotesByAuthors,
  fetchProfile,
  fetchProfileFollowGraph,
  fetchDesktopProfileFollowGraph,
  fetchProfileRelayHints,
  fetchProfiles,
} from "./relay-reads.js";
import {
  hydrateNotificationsPage,
} from "./notifications.js";
export { appendClientNotificationsPage } from "./notifications.js";
import { normalizeRelayList } from "./relay-config.js";
import { profileFeedLoaderMarkup, threadParentSkeletonMarkup, threadRepliesSkeletonMarkup } from "./shell.js";
import { bindRelayNativeThreadBundleSource, peekExpectedThreadReplies } from "./thread-replies-skeleton.js";
import { isTrendingSort } from "./trending-service.js";
import { KIND_LONG_FORM, KIND_NOTE, KIND_PROFILE, KIND_REPOST } from "./nostr-kinds.js";
import { canonicalHex64, normalizePubkey, profilePath } from "./relay-utils.js";
import { refreshNIP05Verification } from "./nip05-verify.js";
import { bindProfileStatLinks } from "./profile-tabs.js";
import { fetchFollowContacts, readRelaysForViewer } from "./publish-plan.js";
import { parseTagFromPath, tagScopeLabel, tagScopeFromURL } from "./hashtag-utils.js";
import { fetchHashtagPage, tagScopeToggleURLs } from "./tag-service.js";
import { loadFeed } from "./services/feed-route-loader.js";
import {
  feedSortForSession,
  getFeedSortPref,
  getReadsSortPref,
  getWebOfTrustDepthPref,
  getWebOfTrustEnabledPref,
} from "./sort-prefs.js";
import { fetchWithSession, normalizedPubkey, updateRelayAwareLinks, updateSessionLinks } from "./session.js";
import { pubkeyFromProfilePath, routeKind } from "./nav-routing.js";
import { replaceRouteOutletHTML } from "./shell-swap.js";
import { fetchDirectParentEvent, resolveThreadFromPath, threadParticipantPubkeys, warmThreadFromPath } from "./thread-graph.js";
import { renderThreadIntoShell, isRelayNativeThread, appendDirectReplyShells, createReplyShell } from "./thread-event-render.js";
import { threadPathNoteID, isThreadHydrateComplete, threadFocusNeedsFullHydrate } from "./thread-hydrate.js";
import { initThreadPage, applyThreadViewVisibilityFromPreference } from "./thread.js";
import { isThreadWoTEnabledForFocus } from "./thread-wot-prefs.js";
import { applyThreadWoTToBundle, resolveThreadWoTMembership } from "./thread-wot.js";
import { pageDirectReplies, parentID, rootIDForEvent } from "./thread-tags.js";
import { resolveThreadView } from "./thread-view.js";
import { eventsByAuthors, getEvent, latestReplaceable, putEvents } from "./event-store.js";
import { setAvatarImageSource } from "./avatar-cache.js";
import { pageIsHidden, powerLimitedCount, powerSaverActive } from "./power-mode.js";
import { relayNativeThreadMissingBundleAction } from "./thread-route-fallback.js";
import { cachedThreadRoutePreviewState } from "./thread-preview-state.js";
import { escapeHTML, trustedHTMLFragment } from "./render-utils.js";
import { applyDestinationThreadTransition, applyDestinationProfileTransition, applyThreadTransitionNames, clearThreadTransition, currentThreadTransition, runNoteViewTransition } from "./note-transition.js";
import {
  mergeProfileRelays,
  profileDisplayRelays,
  profileFallbackRelays,
  profileRelayHintsToList,
  profileRelayListsMatch,
} from "./profile-relay-hints.js";
import { profileRoutePreview } from "./profile-route-preview.js";
import { renderProfileHeroMetadataHTML, renderProfileHeroWebsiteHTML, renderProfileIdentHTML, renderProfilePaymentHTML } from "./profile-render.js";
import {
  cachedProfile as cachedMemoryProfile,
  mergeCachedProfilesByPubkey,
  rememberProfile,
  rememberProfiles,
} from "./profile-memory-cache.js";
import { appBootstrap } from "./app/bootstrap.js";
import {
  buildProfileHydrationRelayStages,
  emptyProfileRelayHints,
  fetchProfileFollowGraphAcrossRelayStages,
  fetchInitialProfilePostsAcrossRelayStages,
  hasRenderableProfileMetadata,
  hasAuthoritativeProfileEvent,
  shouldPromoteProfileMetadata,
} from "./profile-hydration.js";

/** Cached unfiltered relay-native thread for client-side WoT toggles. */
let relayNativeThreadState = null;

bindRelayNativeThreadBundleSource((pathNoteID) => {
  const state = relayNativeThreadState;
  if (!state?.fullBundle?.root) return null;
  const selectedID = canonicalHex64(pathNoteID);
  if (!selectedID) return null;
  const rootID = canonicalHex64(state.fullBundle.rootID || state.fullBundle.root?.id);
  if (selectedID === rootID) return state.fullBundle;
  if (state.fullBundle.events?.some((event) => canonicalHex64(event.id) === selectedID)) {
    return state.fullBundle;
  }
  return null;
});

export { isRelayNativeThread };

const THREAD_REPLY_PAGE_SIZE = 25;
const PROFILE_PAGE_SIZE = 25;
const PROFILE_TIMELINE_KINDS = [KIND_NOTE, KIND_REPOST];
const PROFILE_FOLLOW_TIMEOUT_MS = 12_000;
const FEED_INITIAL_BACKOFF_MS = [0, 500, 1500, 3500];
const FEED_INITIAL_PAINT_TIMEOUT_MS = 5000;
const FEED_REFRESH_EMPTY_FALLBACK_MS = 8000;
const FEED_LOAD_TIMED_OUT = Symbol("feed-load-timed-out");
const NEVER = new Promise(() => {});
const PROFILE_INITIAL_POST_BACKOFF_MS = [0, 500, 1500, 3500];
const PROFILE_METADATA_RETRY_DELAYS_MS = [1200, 3200, 7000];
const PROFILE_INITIAL_HYDRATE_TIMEOUT_MS = 6_000;
const PROFILE_HINTED_POSTS_TIMEOUT_MS = 12_000;
const THREAD_MISSING_BUNDLE_RETRY_DELAYS_MS = [700, 1800, 3500];

let relayNativeProfileState = null;

function activeThreadColumn(root = document) {
  if (!root?.querySelector) return null;
  const threadFragment = root.querySelector("#thread-summary, #thread-focus, #thread-tree-view, #thread-ancestors");
  if (threadFragment?.closest) {
    const column = threadFragment.closest(".feed-column");
    if (column instanceof HTMLElement) return column;
  }
  const taggedColumn = root.querySelector(".feed-column[data-thread-selected-id], .feed-column[data-thread-root-id]");
  if (taggedColumn instanceof HTMLElement) return taggedColumn;
  const fallback = root.querySelector(".feed-column");
  return fallback instanceof HTMLElement ? fallback : null;
}

function renderCachedThreadSummaryPreview(root) {
  const summary = root?.querySelector?.("#thread-summary");
  if (!(summary instanceof HTMLElement)) return;
  summary.replaceChildren();
}
let feedHydrationGeneration = 0;
let feedEmptyFallbackTimer = 0;
let profileHydrationGeneration = 0;

function keyGridHTML(value) {
  const text = String(value || "").trim();
  if (!text) return "";
  const chunks = [];
  for (let i = 0; i < text.length; i += 4) chunks.push(text.slice(i, i + 4));
  const rows = [];
  for (let rowStart = 0; rowStart < chunks.length; rowStart += 4) {
    const rowIdx = rowStart / 4;
    const cells = chunks.slice(rowStart, rowStart + 4).map((chunk, col) => {
      const emph = (rowIdx + col) % 2 === 0 ? " profile-npub-cell--emph" : "";
      return `<span class="profile-npub-cell${emph}">${escapeHTML(chunk)}</span>`;
    });
    rows.push(`<div class="profile-npub-grid-row">${cells.join("")}</div>`);
  }
  return `<div class="profile-npub-grid" translate="no">${rows.join("")}</div>`;
}

function profileHeroMenuListHTML(pubkey) {
  const pk = escapeHTML(pubkey);
  return `
    <span class="profile-follow-mute" data-profile-follow-mute>
      <button type="button" role="menuitem" class="link-button profile-follow-toggle" data-follow-toggle data-pubkey="${pk}" aria-pressed="false">Follow</button>
      <button type="button" role="menuitem" class="link-button profile-mute-toggle" data-mute-toggle data-pubkey="${pk}" aria-pressed="false" hidden>Mute</button>
    </span>
    <a role="menuitem" href="/" data-relay-aware>view feed</a>
    <button type="button" role="menuitem" class="link-button" data-profile-tab="user-tab-identifiers">Identities</button>
    <button type="button" role="menuitem" class="link-button" data-profile-hex-copy data-pubkey="${pk}">Copy hex pubkey</button>
    <a role="menuitem" href="/profile/edit" class="link-button" data-own-profile-edit hidden>Edit profile</a>
    <button type="button" role="menuitem" class="link-button" data-own-profile-logout data-logout data-logout-redirect="/login" hidden>Log out</button>
  `;
}

function profileHeroOptionsHTML(pubkey) {
  const pk = escapeHTML(pubkey);
  return `<details class="ascii-action-menu profile-hero-options-menu" data-profile-stats-menu data-profile-actions data-profile-pubkey="${pk}">
    <summary class="profile-hero-options-trigger" data-ascii-action-menu-trigger="1" aria-haspopup="menu" aria-expanded="false" aria-label="Profile options">...</summary>
    <span class="ascii-action-menu-list" role="menu">${profileHeroMenuListHTML(pubkey)}</span>
  </details>`;
}
function profileNpubBlockHTML(pubkey) {
  const npub = encodeNpub(pubkey);
  return `
    <span class="profile-npub-copy-toast" data-profile-npub-copy-status role="status" aria-live="polite" hidden></span>
    ${keyGridHTML(npub)}
  `;
}

function profileNip05HTML(nip05, pubkey) {
  const pk = escapeHTML(pubkey);
  const verified = escapeHTML(nip05);
  return `<p class="profile-nip05-line profile-hero-nip05" data-nip05-verify data-nip05="${verified}" data-pubkey="${pk}">
    <span class="profile-nip05-text">${verified}</span>
    <button type="button" class="profile-nip05-status muted" data-nip05-status aria-label="NIP-5 verification status" aria-haspopup="dialog" aria-expanded="false" hidden>…</button>
  </p>`;
}

export function profileHeaderHasSkeleton(root) {
  const header = profileHeroScope(root);
  if (!header) return false;
  return Boolean(
    header.querySelector(".profile-skeleton") ||
    header.querySelector(".profile-npub-block--skeleton") ||
    header.querySelector(".profile-hero-nip05-skeleton"),
  );
}

function profileHeroScope(root = document) {
  const shell = profileShell(root);
  if (shell instanceof HTMLElement) {
    const header = shell.querySelector("#user-header");
    if (header instanceof HTMLElement) return header;
  }
  const header = root.querySelector?.("#user-header");
  return header instanceof HTMLElement ? header : null;
}

function peekImmediateProfileSeed(pubkey, previewProfile = null) {
  const pk = normalizePubkey(pubkey);
  if (!pk) return null;
  const bootstrapProfile = normalizePubkey(appBootstrap().initialProfile?.pubkey) === pk
    ? appBootstrap().initialProfile
    : null;
  const seededPreview = bootstrapProfile
    ? { ...bootstrapProfile, savedAt: Date.now() }
    : null;
  const preview = previewProfile || seededPreview || profileRoutePreview(pk);
  const memoryProfile = cachedMemoryProfile(pk);
  const liveProfile = relayNativeProfileState?.pubkey === pk ? relayNativeProfileState?.profile : null;
  let immediate = preview || null;
  if (memoryProfile) immediate = mergeProfileSeedProfile(memoryProfile, immediate);
  if (liveProfile) immediate = mergeProfileSeedProfile(liveProfile, immediate);
  return immediate;
}

function ensureProfileHeroPainted(root, profile) {
  if (!profile?.pubkey || !profileHeaderHasSkeleton(root) || !profileHasResolvedMetadata(profile)) return;
  applyProfileHero(root, profile);
}

function profileHasResolvedMetadata(profile) {
  return hasAuthoritativeProfileEvent(profile) && hasRenderableProfileMetadata(profile);
}

/** Fill immutable pubkey-derived profile chrome synchronously (npub, follow). */
export function applyProfilePubkeyShell(root, pubkey) {
  const pk = normalizePubkey(pubkey);
  if (!pk || !root) return;
  const shell = profileShell(root);
  if (shell instanceof HTMLElement) shell.dataset.profilePubkey = pk;
  const header = profileHeroScope(root);
  if (!(header instanceof HTMLElement)) return;
  const options = header.querySelector(".profile-hero-options-skeleton");
  if (options instanceof HTMLElement) {
    options.outerHTML = profileHeroOptionsHTML(pk);
  }
  const npubBlock = header.querySelector(".profile-npub-block--skeleton");
  if (npubBlock instanceof HTMLElement) {
    npubBlock.className = "profile-npub-block profile-npub-block--header profile-npub-copy";
    npubBlock.setAttribute("role", "button");
    npubBlock.tabIndex = 0;
    npubBlock.setAttribute("aria-label", "Copy npub to clipboard");
    npubBlock.removeAttribute("aria-hidden");
    npubBlock.dataset.profileNpubCopy = "";
    npubBlock.dataset.npub = encodeNpub(pk);
    npubBlock.innerHTML = profileNpubBlockHTML(pk);
  }
  bindProfileStatLinks(root);
  updateSessionLinks();
}

/** Paint already-known display name/avatar without claiming complete metadata. */
export function applyImmediateProfileShell(root, pubkey, previewProfile = null) {
  const pk = normalizePubkey(pubkey);
  if (!pk || !root) return null;
  applyProfilePubkeyShell(root, pk);
  const immediateSeed = peekImmediateProfileSeed(pk, previewProfile);
  if (!immediateSeed) return null;
  const immediateProfile = mergeProfileSeedProfile(parseProfile(pk, null), immediateSeed);
  applyProfileIdentity(root, immediateProfile, { revealShell: true });
  return immediateProfile;
}

function finalizeProfileSkeletonShell(root, profile) {
  if (!profileHeaderHasSkeleton(root)) return;
  const pk = normalizePubkey(profile?.pubkey);
  const header = profileHeroScope(root);
  if (!(header instanceof HTMLElement)) return;
  header.querySelectorAll(".profile.profile-skeleton").forEach((section) => {
    if (!(section instanceof HTMLElement)) return;
    section.classList.remove("profile-skeleton");
    section.removeAttribute("aria-hidden");
  });
  const options = header.querySelector(".profile-hero-options-skeleton");
  if (options instanceof HTMLElement && pk) {
    options.outerHTML = profileHeroOptionsHTML(pk);
  }
}

async function cachedProfilesByPubkey(pubkeys) {
  const keys = [...new Set((pubkeys || []).map(normalizePubkey).filter(Boolean))];
  if (!keys.length) return {};
  const memory = {};
  const missing = [];
  for (const pk of keys) {
    const profile = cachedMemoryProfile(pk);
    if (profile) memory[pk] = profile;
    else missing.push(pk);
  }
  if (!missing.length) return memory;
  const entries = await Promise.all(missing.map(async (pk) => {
    const event = await latestReplaceable(pk, KIND_PROFILE).catch(() => null);
    return [pk, rememberProfile(parseProfile(pk, event))];
  }));
  return mergeCachedProfilesByPubkey(keys, memory, Object.fromEntries(entries));
}

async function displayBundleForRelayNativeThread(state) {
  const { fullBundle, profiles, viewerPubkey } = state;
  const wotEnabled = isThreadWoTEnabledForFocus();
  const membership = wotEnabled ? await resolveThreadWoTMembership(viewerPubkey) : new Set();
  const displayBundle = applyThreadWoTToBundle(fullBundle, { wotEnabled, membership });
  return { displayBundle, profiles: mergeCachedProfilesByPubkey(threadParticipantPubkeys(displayBundle.events), profiles) };
}

function cacheThreadView(displayBundle) {
  if (displayBundle.threadViewResolved) return displayBundle.threadViewResolved;
  const { root, selected, events, parentByID } = displayBundle;
  const resolved = resolveThreadView(root, selected, events, parentByID);
  displayBundle.threadViewResolved = resolved;
  displayBundle.threadView = resolved.view;
  displayBundle.replyCounts = resolved.replyCounts;
  return resolved;
}

function directRepliesForBundle(displayBundle) {
  return cacheThreadView(displayBundle).linearNodes.map((node) => node.event);
}

function applyRelayNativeReplyPage(displayBundle, cursor = "", cursorId = "") {
  const page = pageDirectReplies(directRepliesForBundle(displayBundle), cursor, cursorId, THREAD_REPLY_PAGE_SIZE);
  displayBundle.replyPagination = {
    hasMore: page.hasMore,
    cursor: page.hasMore ? page.nextCursor : "",
    cursorId: page.hasMore ? page.nextCursorId : "",
  };
  displayBundle.linearReplyPage = page.items;
  return page;
}

async function renderRelayNativeThread(root, state) {
  rememberVisibleNoteProfiles(root);
  const { displayBundle, profiles } = await displayBundleForRelayNativeThread(state);
  applyRelayNativeReplyPage(displayBundle);
  const referencedByID = state.referencedByID || (await hydrateReferencedEvents(displayBundle.events));
  state.referencedByID = referencedByID;
  renderThreadIntoShell(root, displayBundle, profiles, referencedByID);
  refreshAsciiSync(root);
}

export async function loadMoreRelayNativeThreadReplies(button) {
  if (!relayNativeThreadState || !button || button.dataset.loading === "1") return;
  button.dataset.loading = "1";
  button.disabled = true;
  button.textContent = "Loading...";
  const list = document.querySelector("#thread-replies");
  try {
    const { displayBundle, profiles } = await displayBundleForRelayNativeThread(relayNativeThreadState);
    const page = pageDirectReplies(
      directRepliesForBundle(displayBundle),
      button.dataset.cursor || "",
      button.dataset.cursorId || "",
      THREAD_REPLY_PAGE_SIZE,
    );
    if (!page.items.length) {
      button.textContent = "No more replies";
      button.hidden = true;
      return;
    }
    const appended = appendDirectReplyShells(document, page.items, displayBundle, profiles, {
      hasMore: page.hasMore,
      referencedByID: relayNativeThreadState?.referencedByID || null,
    });
    if (appended > 0) {
      initViewMore(list);
      void refreshVisibleNoteProfiles(list);
      refreshAscii(list);
    }
    button.dataset.cursor = page.hasMore ? page.nextCursor : "";
    button.dataset.cursorId = page.hasMore ? page.nextCursorId : "";
    if (!page.hasMore) {
      button.textContent = "No more replies";
      button.disabled = true;
      button.hidden = true;
      return;
    }
    button.textContent = button.dataset.loadLabel || "Load more thread replies";
    button.disabled = false;
  } catch (error) {
    button.textContent = error?.message || "Load failed";
    button.disabled = false;
  } finally {
    button.dataset.loading = "0";
  }
}

export async function rerenderRelayNativeThread(root = document) {
  if (!relayNativeThreadState) return;
  await renderRelayNativeThread(root, relayNativeThreadState);
  afterRelayNativeThreadRendered(root);
}

export async function applyThreadViewModel(root = document, viewModel = {}, options = {}) {
  const bundle = viewModel?.bundle;
  if (!bundle?.root || !Array.isArray(bundle?.events)) return false;
  const viewer = String(options.viewer || normalizedPubkey() || "").trim().toLowerCase();
  const pubkeys = threadParticipantPubkeys(bundle.events);
  const cachedProfiles = await cachedProfilesByPubkey(pubkeys);
  relayNativeThreadState = {
    fullBundle: bundle,
    profiles: cachedProfiles,
    viewerPubkey: viewer,
    referencedByID: new Map(),
  };
  await renderRelayNativeThread(root, relayNativeThreadState);
  afterRelayNativeThreadRendered(root);
  initThreadPage();
  return true;
}

function feedRoot(root = document) {
  return root.querySelector("[data-feed]") || root.querySelector("[data-route-outlet]");
}

function feedNoteAuthorPubkeys(notes = []) {
  return [...new Set(
    (notes || [])
      .map((event) => normalizePubkey(event?.pubkey))
      .filter(Boolean),
  )];
}

function homeLoadMoreButton(root = document) {
  return root.querySelector('[data-load-more][data-feed-url="/feed"]');
}

function setHomeLoadMorePending(root = document, isPending = false) {
  const button = homeLoadMoreButton(root);
  if (!button) return;
  if (isPending) {
    button.dataset.pending = "1";
    button.hidden = true;
    button.disabled = true;
    return;
  }
  delete button.dataset.pending;
  if (button.dataset.loading !== "1") {
    button.disabled = false;
  }
}

function profilePostsFeed(root = document) {
  return root.querySelector("#user-panel-posts [data-feed]") || root.querySelector(".profile-feed[data-feed]");
}

export function isRelayNativeFeed(root = document) {
  const feed = homeFeed(root);
  return feed?.dataset.relayNativeFeed === "1";
}

export function isRelayNativeProfile(root = document) {
  const feed = profilePostsFeed(root);
  return feed?.dataset.relayNativeProfile === "1";
}

function currentFeedSort(viewer = normalizedPubkey()) {
  return feedSortForSession(viewer, getFeedSortPref()) || "recent";
}

export function homeFeedSessionChanged(root = document) {
  const feed = homeFeed(root);
  const viewer = normalizedPubkey();
  return renderedFeedSessionChanged(feed, {
    viewer,
    sort: currentFeedSort(viewer),
    wotEnabled: getWebOfTrustEnabledPref(),
    wotDepth: getWebOfTrustDepthPref(),
  });
}

export function relayNativeRouteEligible(route) {
  if (
    route === "feed" ||
    route === "thread" ||
    route === "read" ||
    route === "bookmarks" ||
    route === "notifications" ||
    route === "profile" ||
    route === "reads" ||
    route === "search" ||
    route === "tag"
  ) {
    return true;
  }
  return false;
}

function configureHomeLoadMore(root, notes, options = {}) {
  const button = homeLoadMoreButton(root);
  if (!button) return;
  const cursor = options.cursor || feedPageCursor(notes);
  button.dataset.cursor = cursor?.until > 0 ? String(cursor.until) : "";
  button.dataset.cursorId = cursor?.cursorId || "";
  const hasMore = typeof options.hasMore === "boolean" ? options.hasMore : feedPageMayHaveMore(notes);
  const isPending = button.dataset.pending === "1";
  const isLoading = button.dataset.loading === "1";
  const hasLoader = Boolean(homeFeed(root)?.querySelector("[data-feed-loader]"));
  button.dataset.hasMore = hasMore ? "1" : "0";
  button.hidden = homeFeedLoadMoreHidden({ hasMore, isPending, isLoading, hasLoader });
  if (!isLoading) {
    button.disabled = isPending ? true : !hasMore;
  }
  if (button.dataset.loading !== "1") {
    button.textContent = "Load more";
  }
}

export async function applyFeedViewModel(root = document, viewModel = {}, options = {}) {
  const feed = homeFeed(root);
  if (!feed || !viewModel || viewModel.route !== "feed") return false;
  const notes = Array.isArray(viewModel.notes) ? viewModel.notes : [];
  const viewer = String(options.viewer || normalizedPubkey() || "").trim().toLowerCase();
  if (!options.allowEmptyLoaderReplacement && shouldPreserveDeferredFeedLoader(feed, notes, viewer)) return false;
  const sort = String(viewModel.sort || currentFeedSort(viewer) || "recent");
  const profiles = viewModel?.profiles && typeof viewModel.profiles === "object"
    ? viewModel.profiles
    : await cachedProfilesByPubkey(feedNoteAuthorPubkeys(notes));
  feed.dataset.relayNativeFeed = "1";
  feed.dataset.relayNativeFeedSort = sort;
  feed.dataset.feedViewer = viewer;
  feed.dataset.feedWotEnabled = getWebOfTrustEnabledPref() ? "1" : "0";
  feed.dataset.feedWotDepth = String(getWebOfTrustDepthPref());
  delete feed.dataset.feedSort;
  renderNoteFeed(feed, notes, profiles, {
    emptyText: "No notes yet.",
    referencedByID: new Map(),
    replyCounts: serverReplyCountsForEvents(notes),
  });
  configureHomeLoadMore(root, notes, {
    cursor: viewModel.cursor,
    hasMore: Boolean(viewModel.hasMore),
  });
  afterFeedNotesRendered(root, feed, { metadataPrefetched: false });
  return true;
}

function patchRenderedFeedNotesInPlace(feed, notes, profiles, referencedByID, replyCounts, reactionStats, zapTotals) {
  if (!(feed instanceof HTMLElement) || !Array.isArray(notes) || notes.length < 1) return false;
  const noteByID = new Map(
    [...feed.querySelectorAll(".note[id^='note-']")]
      .map((node) => [node.id.replace(/^note-/, "").toLowerCase(), node]),
  );
  let changed = false;
  for (const event of notes) {
    const eventID = String(event?.id || "").toLowerCase();
    const note = noteByID.get(eventID);
    if (!(note instanceof HTMLElement)) continue;
    const reactionRow = reactionStats?.[eventID] || null;
    const nextReplyCount = Number.parseInt(`${replyCounts?.[eventID] ?? note.dataset.asciiReplyCount ?? 0}`, 10) || 0;
    const nextReactionTotal = Number.parseInt(`${reactionRow?.total ?? note.dataset.asciiReactionTotal ?? 0}`, 10) || 0;
    const nextReactionViewer = typeof reactionRow?.viewer === "string"
      ? reactionRow.viewer
      : String(note.dataset.asciiReactionViewer || "");
    const nextZapTotal = Number.parseInt(`${zapTotals?.[eventID] ?? note.dataset.asciiZapTotal ?? 0}`, 10) || 0;
    if (note.dataset.asciiReplyCount !== `${nextReplyCount}`) {
      note.dataset.asciiReplyCount = `${nextReplyCount}`;
      changed = true;
    }
    if (note.dataset.asciiReactionTotal !== `${nextReactionTotal}`) {
      note.dataset.asciiReactionTotal = `${nextReactionTotal}`;
      changed = true;
    }
    if (note.dataset.asciiReactionViewer !== nextReactionViewer) {
      note.dataset.asciiReactionViewer = nextReactionViewer;
      changed = true;
    }
    if (note.dataset.asciiZapTotal !== `${nextZapTotal}`) {
      note.dataset.asciiZapTotal = `${nextZapTotal}`;
      changed = true;
    }
    enrichNoteShell(note, event, referencedByID, profiles || {});
    refreshAscii(note);
  }
  wireAvatarImageFallbacks(feed);
  return changed;
}

async function enrichRenderedFeed(root, feed, notes, {
  hydrationGeneration = 0,
  sort = "",
  viewer = "",
  coldInitialPaint = false,
  replacingVisibleFeed = false,
  forceRefresh = false,
  sortChanged = false,
  metadataPrefetched = false,
} = {}) {
  const noteIDs = notes.map((event) => String(event.id || "").toLowerCase()).filter(Boolean);
  const needsPrefetchedMetadata =
    metadataPrefetched ||
    coldInitialPaint ||
    replacingVisibleFeed ||
    (isTrendingSort(sort) && (forceRefresh || sortChanged));
  try {
    const tasks = [
      fetchProfiles([...new Set(notes.map((event) => normalizePubkey(event.pubkey)).filter(Boolean))]),
      hydrateReferencedEvents(notes).catch(() => new Map()),
    ];
    if (needsPrefetchedMetadata && noteIDs.length) {
      tasks.push(fetchFeedNoteMetadataMaps(noteIDs, { viewerPubkey: viewer, sort }));
    }
    const results = await Promise.all(tasks);
    if (hydrationGeneration !== feedHydrationGeneration) return;
    const profiles = results[0];
    const referencedByID = results[1];
    let replyCounts = null;
    let reactionStats = null;
    let zapTotals = null;
    if (needsPrefetchedMetadata && noteIDs.length) {
      ({ replyCounts, reactionStats, zapTotals } = results[2]);
    }
    if (feed.dataset.relayNativeFeedSort !== sort) return;
    const currentIDs = [...feed.querySelectorAll(".note[id^='note-']")]
      .map((node) => node.id.replace(/^note-/, "").toLowerCase())
      .filter(Boolean);
    if (
      currentIDs.length !== noteIDs.length ||
      currentIDs.some((id, index) => id !== noteIDs[index])
    ) {
      return;
    }
    patchRenderedFeedNotesInPlace(feed, notes, profiles, referencedByID, replyCounts, reactionStats, zapTotals);
    configureHomeLoadMore(root, notes);
    afterFeedNotesRendered(root, feed, {
      metadataPrefetched: needsPrefetchedMetadata && Boolean(noteIDs.length),
    });
  } catch (error) {
    console.error("Feed profile/reference hydration failed", error);
  }
}

function deferFeedEnrichment(root, feed, options = {}) {
  const run = () => {
    void refreshVisibleFeedNoteMetadata(root, window.location.href, options).then(() => {
      void refreshVisibleNoteProfiles(feed ?? root);
    });
  };
  if (typeof requestIdleCallback === "function") {
    requestIdleCallback(run, { timeout: 1500 });
  } else {
    setTimeout(run, 0);
  }
}

function afterRelayNativeThreadRendered(root) {
  refreshAsciiSync(root);
  initViewMore(root);
  deferFeedEnrichment(root, root);
  wireAvatarImageFallbacks(root);
  void syncBookmarkState(document);
}

async function renderServerThreadHydrateFallback(root, pathNoteID, isCurrent) {
  const selectedID = canonicalHex64(pathNoteID);
  if (!selectedID) return false;
  if (typeof isCurrent === "function" && !isCurrent()) return false;
  try {
    const url = new URL(globalThis.location?.href || `/thread/${selectedID}`, globalThis.location?.origin || window.location.origin);
    url.searchParams.set("fragment", "hydrate");
    url.searchParams.delete("cursor");
    url.searchParams.delete("cursor_id");
    const href = `${url.pathname}${url.search}${url.hash}`;
    const response = await fetchWithSession(href, { headers: { Accept: "text/html" } });
    if (!response.ok) return false;
    const html = await response.text();
    if (!isThreadHydrateComplete(html, selectedID)) return false;
    if (typeof isCurrent === "function" && !isCurrent()) return false;
    relayNativeThreadState = null;
    replaceRouteOutletHTML(root, html);
    updateSessionLinks();
    updateRelayAwareLinks();
    initViewMore(root);
    wireAvatarImageFallbacks(root);
    refreshAsciiSync(root);
    initThreadPage();
    void syncBookmarkState(document);
    return true;
  } catch {
    return false;
  }
}

async function resolveThreadBundleAfterMiss(pathNoteID, preferredRelays, isCurrent) {
  for (const delay of THREAD_MISSING_BUNDLE_RETRY_DELAYS_MS) {
    if (typeof isCurrent === "function" && !isCurrent()) return null;
    if (delay > 0) {
      await new Promise((resolve) => window.setTimeout(resolve, delay));
    }
    if (typeof isCurrent === "function" && !isCurrent()) return null;
    const warmed = await warmThreadFromPath(pathNoteID, { preferredRelays }).catch(() => null);
    if (warmed?.root) return warmed;
    if (typeof isCurrent === "function" && !isCurrent()) return null;
    const resolved = await resolveThreadFromPath(pathNoteID, { preferredRelays }).catch(() => null);
    if (resolved?.root) return resolved;
  }
  return null;
}

async function cachedDirectParentEvent(event) {
  if (!event?.id) return null;
  const selectedID = canonicalHex64(event.id);
  const rootID = canonicalHex64(rootIDForEvent(event)) || selectedID;
  const directParent = canonicalHex64(parentID(rootID, event) || parentID("", event));
  if (!directParent || directParent === selectedID) return null;
  return getEvent(directParent).catch(() => null);
}

async function ensureFocusedReplyParent(bundle) {
  if (!bundle?.root?.id || !bundle?.selected?.id) return bundle;
  const rootID = canonicalHex64(bundle.rootID || bundle.root.id);
  const selectedID = canonicalHex64(bundle.selectedID || bundle.selected.id);
  if (!rootID || !selectedID || rootID === selectedID) return bundle;
  const directParentID = canonicalHex64(parentID(rootID, bundle.selected) || parentID("", bundle.selected));
  if (!directParentID || directParentID === rootID) return bundle;
  if ((bundle.events || []).some((event) => canonicalHex64(event.id) === directParentID)) return bundle;
  const parentEvent = await fetchDirectParentEvent(bundle.selected).catch(() => null);
  if (!parentEvent?.id) {
    bundle.selectedParentUnavailable = true;
    return bundle;
  }
  bundle.selectedParentUnavailable = false;
  bundle.events = [...bundle.events, parentEvent];
  bundle.parentByID = {
    ...(bundle.parentByID || {}),
    [selectedID]: canonicalHex64(parentEvent.id),
  };
  return bundle;
}

function replaceThreadParentSkeleton(focus, parentEvent, { rootID, selectedID }) {
  if (!parentEvent?.id || !(focus instanceof HTMLElement)) return;
  const parentShell = createReplyShell(
    parentEvent,
    parseProfile(parentEvent.pubkey, null),
    {
      rootID,
      selectedID,
      depth: 1,
      isLast: false,
      hasChildren: true,
      isFocused: false,
      extraClass: "thread-focus-parent",
      replyCount: 1,
    },
  );
  const skeleton = focus.querySelector(".thread-focus-parent--skeleton");
  if (skeleton instanceof HTMLElement) {
    skeleton.replaceWith(parentShell);
    return;
  }
  if (focus.querySelector(".thread-focus-parent")) return;
  const selected = focus.querySelector(".thread-focus-selected");
  if (selected instanceof HTMLElement) focus.insertBefore(parentShell, selected);
  else focus.prepend(parentShell);
}

function patchFocusedReplyParentFromEvent(root, selectedEvent, preview, options = {}) {
  if (!preview?.showsParentSkeleton || !selectedEvent?.id) return;
  const { preferredRelays = [], canRender = null } = options;
  void (async () => {
    const parentEvent = await fetchDirectParentEvent(selectedEvent, { preferredRelays }).catch(() => null);
    if (!parentEvent?.id) return;
    if (typeof canRender === "function" && !canRender()) return;
    const focus = root?.querySelector?.("#thread-focus");
    if (!(focus instanceof HTMLElement)) return;
    replaceThreadParentSkeleton(focus, parentEvent, {
      rootID: preview.rootID,
      selectedID: preview.selectedID,
    });
    refreshAsciiSync(focus);
    initViewMore(focus);
    wireAvatarImageFallbacks(focus);
  })();
}

function renderCachedSelectedThreadNote(root, event, parentEvent = null) {
  const preview = cachedThreadRoutePreviewState(event, parentEvent);
  if (!preview.rendered) return preview;
  const { selectedID, rootID, isReply } = preview;
  const column = activeThreadColumn(root);
  if (column) {
    column.dataset.threadSelectedId = selectedID;
  }
  renderCachedThreadSummaryPreview(root, {
    rootID: canonicalHex64(rootID || parentEvent?.id || event?.id),
    selectedID,
  });
  const focus = root.querySelector("#thread-focus");
  if (!focus) return false;
  const carried = focus.querySelector(`.ptxt-carried-thread-note#note-${selectedID}`);
  if (carried instanceof HTMLElement) {
    const profile = parseProfile(event.pubkey, null);
    const selectedShell = createSelectedNoteArticleFromShell(carried, event, profile, {
      rootID,
      isFocused: true,
      extraClass: isReply ? "thread-focus-selected" : "",
    });
    carried.replaceWith(selectedShell);
    if (isReply) replaceThreadParentSkeleton(focus, parentEvent, { rootID, selectedID });
    const replies = root.querySelector("#thread-replies");
    if (replies) {
      replies.replaceChildren();
      replies.classList.remove("thread-replies-skeleton");
      if (peekExpectedThreadReplies(selectedID, normalizedPubkey())) {
        replies.classList.add("thread-replies-skeleton");
        replies.innerHTML = threadRepliesSkeletonMarkup();
      }
    }
    refreshAsciiSync(focus);
    initViewMore(focus);
    wireAvatarImageFallbacks(focus);
    applyDestinationThreadTransition(root, selectedID);
    return preview;
  }
  const section = document.createElement("section");
  section.className = "thread-focus";
  section.id = "thread-focus";
  section.dataset.threadFragment = "focus";
  if (isReply) {
    if (parentEvent?.id) {
      section.append(
        createReplyShell(parentEvent, parseProfile(parentEvent.pubkey, null), {
          rootID,
          selectedID,
          depth: 1,
          isLast: false,
          hasChildren: true,
          isFocused: false,
          extraClass: "thread-focus-parent",
          replyCount: 1,
        }),
      );
    } else {
      section.append(trustedHTMLFragment(threadParentSkeletonMarkup()));
    }
  }
  const selectedShell = createSelectedNoteArticle(event, null, {
    rootID,
    isFocused: true,
    extraClass: isReply ? "thread-focus-selected" : "",
  });
  applyThreadTransitionNames(selectedShell, selectedID);
  section.append(selectedShell);
  focus.replaceWith(section);
  const replies = root.querySelector("#thread-replies");
  if (replies) {
    replies.replaceChildren();
    replies.classList.remove("thread-replies-skeleton");
    if (peekExpectedThreadReplies(selectedID, normalizedPubkey())) {
      replies.classList.add("thread-replies-skeleton");
      replies.innerHTML = threadRepliesSkeletonMarkup();
    }
  }
  refreshAsciiSync(section);
  initViewMore(section);
  wireAvatarImageFallbacks(section);
  applyDestinationThreadTransition(root, selectedID);
  return preview;
}

export async function renderCachedThreadRoutePreview(root = document, noteID = "", options = {}) {
  const { canRender = null, preferredRelays = [] } = options || {};
  const selectedID = canonicalHex64(noteID || threadPathNoteID(window.location.href));
  if (!selectedID) return cachedThreadRoutePreviewState(null, null);
  if (typeof canRender === "function" && !canRender()) {
    return cachedThreadRoutePreviewState(null, null);
  }
  const cachedSelected = await getEvent(selectedID).catch(() => null);
  if (!cachedSelected) return cachedThreadRoutePreviewState(null, null);
  const cachedParent = await cachedDirectParentEvent(cachedSelected);
  const preview = renderCachedSelectedThreadNote(root, cachedSelected, cachedParent);
  patchFocusedReplyParentFromEvent(root, cachedSelected, preview, { preferredRelays, canRender });
  return preview;
}

async function fetchServerThreadRoutePreview(selectedID) {
  try {
    const url = new URL("/api/thread-preview", window.location.origin);
    url.searchParams.set("id", selectedID);
    const response = await fetchWithSession(url.pathname + url.search, {
      headers: { Accept: "application/json" },
    });
    if (!response.ok) return null;
    const payload = await response.json();
    const events = Array.isArray(payload?.events) ? payload.events : [];
    if (!events.length) return null;
    await putEvents(events).catch(() => {});
    if (payload?.profiles && typeof payload.profiles === "object") rememberProfiles(payload.profiles);
    const byID = new Map(events.map((event) => [canonicalHex64(event?.id), event]).filter(([id]) => id));
    const selected = byID.get(selectedID) || byID.get(canonicalHex64(payload?.selected_id));
    if (!selected?.id) return null;
    const parentID = canonicalHex64(payload?.parent_id);
    return {
      selected,
      parent: parentID ? byID.get(parentID) || null : null,
    };
  } catch {
    return null;
  }
}

export async function renderServerThreadRoutePreview(root = document, noteID = "", options = {}) {
  const { canRender = null, preferredRelays = [] } = options || {};
  const selectedID = canonicalHex64(noteID || threadPathNoteID(window.location.href));
  if (!selectedID) return cachedThreadRoutePreviewState(null, null);
  const bundle = await fetchServerThreadRoutePreview(selectedID);
  if (!bundle?.selected?.id) return cachedThreadRoutePreviewState(null, null);
  if (typeof canRender === "function" && !canRender()) {
    return cachedThreadRoutePreviewState(null, null);
  }
  const preview = renderCachedSelectedThreadNote(root, bundle.selected, bundle.parent);
  patchFocusedReplyParentFromEvent(root, bundle.selected, preview, { preferredRelays, canRender });
  return preview;
}

export async function renderLiveThreadRoutePreview(root = document, noteID = "", { preferredRelays = [], canRender = null } = {}) {
  const selectedID = canonicalHex64(noteID || threadPathNoteID(window.location.href));
  if (!selectedID) return cachedThreadRoutePreviewState(null, null);
  const relayHintsByID = preferredRelays.length ? { [selectedID]: preferredRelays } : {};
  const liveSelected = (await fetchEventsByIDs([selectedID], { relayHintsByID }))[0] || null;
  if (!liveSelected?.id) return cachedThreadRoutePreviewState(null, null);
  const cachedParent = await cachedDirectParentEvent(liveSelected);
  if (typeof canRender === "function" && !canRender()) {
    return cachedThreadRoutePreviewState(null, null);
  }
  const preview = renderCachedSelectedThreadNote(root, liveSelected, cachedParent);
  patchFocusedReplyParentFromEvent(root, liveSelected, preview, { preferredRelays, canRender });
  return preview;
}

function afterFeedNotesRendered(root, feed, options = {}) {
  refreshAscii(feed);
  if (!options.metadataPrefetched) {
    deferFeedEnrichment(root, feed, { feedSelector: "#feed[data-feed]" });
  } else {
    void refreshVisibleNoteProfiles(feed ?? root);
  }
  initViewMore(feed);
  void syncBookmarkState(document);
  wireAvatarImageFallbacks(feed);
}

function applyProfileIdentity(root, profile, { revealShell = false } = {}) {
  if (!profile?.pubkey) return;
  const header = profileHeroScope(root);
  if (!(header instanceof HTMLElement)) return;
  const label = displayName(profile);
  header.querySelectorAll(".profile-display-name").forEach((node) => {
    if (!(node instanceof HTMLElement)) return;
    node.textContent = label;
    node.classList.remove("text-skeleton", "profile-skeleton-display-name");
  });
  const avatarURL = preferredAvatarURL(profile);
  const retryAvatar = avatarRetryURL(profile);
  header.querySelectorAll(".profile-avatar-wrap").forEach((wrap) => {
    if (!(wrap instanceof HTMLElement)) return;
    let img = wrap.querySelector(":scope > img.profile-avatar");
    if (avatarURL && !(img instanceof HTMLImageElement)) {
      img = document.createElement("img");
      img.className = "profile-avatar";
      img.alt = "";
      img.loading = "lazy";
      img.decoding = "async";
      wrap.replaceChildren(img);
    }
    if (avatarURL && img instanceof HTMLImageElement) setAvatarImageSource(img, avatarURL, { retryURL: retryAvatar });
  });
  wireAvatarImageFallbacks(header);
  if (revealShell) {
    header.querySelectorAll(".profile.profile-skeleton[aria-hidden='true']").forEach((section) => {
      section.removeAttribute("aria-hidden");
    });
  }
}

function applyProfileHero(root, profile) {
  if (!profile?.pubkey) return;
  const header = profileHeroScope(root);
  if (!(header instanceof HTMLElement)) return;
  const pk = normalizePubkey(profile.pubkey);
  applyProfileIdentity(root, profile);
  header.querySelectorAll("[data-profile-actions]").forEach((node) => {
    if (!(node instanceof HTMLElement)) return;
    node.dataset.profileActions = "";
    node.dataset.profilePubkey = pk;
    const menuList = node.querySelector(".ascii-action-menu-list");
    if (menuList instanceof HTMLElement && !node.querySelector("[data-follow-toggle]")) {
      menuList.innerHTML = profileHeroMenuListHTML(pk);
    }
  });
  header.querySelectorAll(".profile-npub-block--skeleton, [data-profile-npub-copy]").forEach((node) => {
    if (!(node instanceof HTMLElement)) return;
    if (node.classList.contains("profile-npub-block--skeleton") || !node.dataset.npub) {
      node.className = "profile-npub-block profile-npub-block--header profile-npub-copy";
      node.setAttribute("role", "button");
      node.tabIndex = 0;
      node.setAttribute("aria-label", "Copy npub to clipboard");
      node.removeAttribute("aria-hidden");
      node.dataset.profileNpubCopy = "";
      node.dataset.npub = encodeNpub(pk);
      node.innerHTML = profileNpubBlockHTML(pk);
    }
  });
  const ident = header.querySelector(".profile-ident");
  if (ident instanceof HTMLElement) {
    const identHTML = renderProfileIdentHTML(profile);
    if (identHTML) {
      ident.innerHTML = identHTML;
    } else {
      ident.replaceChildren();
    }
  }
  const profileMain = header.querySelector(".profile-main");
  const heroSide = header.querySelector(".profile-hero-side");
  const npubBlock = heroSide?.querySelector("[data-profile-npub-copy], .profile-npub-block");
  const websiteHTML = renderProfileHeroWebsiteHTML(profile.website);
  const paymentHTML = renderProfilePaymentHTML(profile);
  if (heroSide instanceof HTMLElement) {
    heroSide.querySelectorAll(":scope > .profile-hero-website, :scope > .profile-website-line, :scope > .profile-hero-meta-skeleton").forEach((node) => node.remove());
    let paymentNode = heroSide.querySelector(":scope > .profile-payment-line");
    if (paymentHTML) {
      if (paymentNode instanceof HTMLElement) {
        paymentNode.outerHTML = paymentHTML;
      } else if (npubBlock instanceof HTMLElement) {
        npubBlock.insertAdjacentHTML("beforebegin", paymentHTML);
      } else {
        heroSide.insertAdjacentHTML("beforeend", paymentHTML);
      }
      paymentNode = heroSide.querySelector(":scope > .profile-payment-line");
    } else if (paymentNode instanceof HTMLElement) {
      paymentNode.remove();
      paymentNode = null;
    }
    if (websiteHTML) {
      const existingWebsite = heroSide.querySelector(":scope > .profile-website-line");
      if (existingWebsite instanceof HTMLElement) {
        existingWebsite.outerHTML = websiteHTML;
      } else if (paymentNode instanceof HTMLElement) {
        paymentNode.insertAdjacentHTML("beforebegin", websiteHTML);
      } else if (npubBlock instanceof HTMLElement) {
        npubBlock.insertAdjacentHTML("beforebegin", websiteHTML);
      } else {
        heroSide.insertAdjacentHTML("beforeend", websiteHTML);
      }
    }
  }
  if (profileMain instanceof HTMLElement) {
    profileMain.querySelectorAll(":scope > .profile-hero-metadata").forEach((node) => node.remove());
  }
  const nip05 = String(profile.nip05 || "").trim();
  const nip05Skeleton = header.querySelector(".profile-hero-nip05-skeleton");
  if (nip05Skeleton instanceof HTMLElement) {
    if (nip05) nip05Skeleton.outerHTML = profileNip05HTML(nip05, pk);
    else nip05Skeleton.remove();
  } else if (nip05) {
    const existing = header.querySelector("[data-nip05-verify]");
    if (existing instanceof HTMLElement) {
      existing.setAttribute("data-nip05", nip05);
      existing.setAttribute("data-pubkey", pk);
      delete existing.dataset.nip05Loaded;
      const text = existing.querySelector(".profile-nip05-text");
      if (text) text.textContent = nip05;
    } else {
      if (npubBlock instanceof HTMLElement) npubBlock.insertAdjacentHTML("beforebegin", profileNip05HTML(nip05, pk));
      else if (heroSide instanceof HTMLElement) heroSide.insertAdjacentHTML("afterbegin", profileNip05HTML(nip05, pk));
    }
  } else {
    header.querySelectorAll(".profile-hero-nip05, [data-nip05-verify]").forEach((node) => node.remove());
  }
  finalizeProfileSkeletonShell(root, profile);
  bindProfileStatLinks(root);
  updateSessionLinks();
  if (nip05) refreshNIP05Verification(root);
}

function profileShell(root = document) {
  return root.querySelector("[data-profile-shell]") || root.querySelector(".user-profile-column");
}

function profileLoadMoreButton(root = document) {
  return root.querySelector('#user-panel-posts [data-load-more][data-feed-url^="/u/"]');
}

function profilePostsHaveRenderedNotes(root = document) {
  const feed = profilePostsFeed(root);
  return Boolean(feed?.querySelector('.note[id^="note-"]'));
}

function profilePostsRetroLoader(root = document) {
  const feed = profilePostsFeed(root);
  if (!(feed instanceof HTMLElement)) return null;
  const loader = feed.querySelector("[data-retro-loader]");
  return loader instanceof HTMLElement ? loader : null;
}

function ensureProfilePostsLoader(root = document) {
  const feed = profilePostsFeed(root);
  if (!(feed instanceof HTMLElement)) return null;
  if (profilePostsHaveRenderedNotes(root)) return null;
  let loader = profilePostsRetroLoader(root);
  if (loader) return loader;
  feed.insertAdjacentHTML("afterbegin", profileFeedLoaderMarkup("posts"));
  loader = profilePostsRetroLoader(root);
  if (loader) initRetroLoaders(feed);
  return loader;
}

function updateProfilePostsLoader(root, { statusMessage, percent, summary, title } = {}) {
  const loader = ensureProfilePostsLoader(root) || profilePostsRetroLoader(root);
  if (!(loader instanceof HTMLElement)) return;
  setRetroLoaderProgress(loader, { statusMessage, percent, summary, title });
}

function dismissProfilePostsLoader(root = document) {
  profilePostsRetroLoader(root)?.remove();
}

function profileTopCursor(root = document) {
  const first = root.querySelector("#user-panel-posts .note:first-of-type");
  return {
    cursor: first?.dataset?.createdAt || "",
    cursorID: first?.id?.replace(/^note-/, "") || "",
  };
}

function profileBootstrapRelays(root = document) {
  const shell = profileShell(root);
  const raw = [];
  if (shell?.dataset?.profileRelays) raw.push(...String(shell.dataset.profileRelays).split(","));
  const rightRail = root.querySelector("#user-right-relays");
  if (rightRail?.dataset?.profileRelays) raw.push(...String(rightRail.dataset.profileRelays).split(","));
  root.querySelectorAll("#user-panel-relays [data-check-relay], #user-right-relays [data-check-relay]").forEach((node) => {
    raw.push(node.textContent || "");
  });
  return normalizeRelayList(raw);
}

function mergeProfileSeedProfile(baseProfile, previewProfile) {
  if (!previewProfile) return baseProfile;
  return {
    ...previewProfile,
    ...baseProfile,
    about: String(baseProfile?.about || previewProfile?.about || "").trim(),
    display_name: String(baseProfile?.display_name || previewProfile?.display_name || "").trim(),
    name: String(baseProfile?.name || previewProfile?.name || "").trim(),
    avatar_url: String(baseProfile?.avatar_url || previewProfile?.avatar_url || "").trim(),
    picture: String(baseProfile?.picture || previewProfile?.picture || "").trim(),
    website: String(baseProfile?.website || previewProfile?.website || "").trim(),
    nip05: String(baseProfile?.nip05 || previewProfile?.nip05 || "").trim(),
    lud16: String(baseProfile?.lud16 || previewProfile?.lud16 || "").trim(),
    lud06: String(baseProfile?.lud06 || previewProfile?.lud06 || "").trim(),
    event_id: String(baseProfile?.event_id || previewProfile?.event_id || "").trim(),
    created_at: Number(baseProfile?.created_at || previewProfile?.created_at || 0) || 0,
  };
}

function promoteProfile(root, state, profile, previewProfile = null) {
  if (!state || !profile?.pubkey) return false;
  const mergedProfile = mergeProfileSeedProfile(profile, previewProfile);
  if (!shouldPromoteProfileMetadata(state.profile, mergedProfile)) return false;
  const cachedProfile = rememberProfile(mergedProfile);
  state.profile = cachedProfile;
  state.profiles = { ...state.profiles, [state.pubkey]: cachedProfile };
  applyProfileHero(root, cachedProfile);
  renderProfileIdentifiers(root, cachedProfile);
  return true;
}

async function refreshLiveProfileMetadata(root, state, {
  hydrationGeneration = 0,
  previewProfile = null,
  relayStages = [],
} = {}) {
  if (!profileStateIsCurrent(state, root)) return false;

  const cachedProfileEvent = await latestReplaceable(state.pubkey, KIND_PROFILE).catch(() => null);
  if (!profileStateIsCurrent(state, root) || hydrationGeneration !== profileHydrationGeneration) return false;
  if (cachedProfileEvent) {
    promoteProfile(root, state, parseProfile(state.pubkey, cachedProfileEvent), previewProfile);
  }

  let promoted = false;
  for (const relays of relayStages) {
    if (!profileStateIsCurrent(state, root) || hydrationGeneration !== profileHydrationGeneration) break;
    const candidate = await fetchProfile(state.pubkey, {
      relays,
      forceRefresh: true,
      includeViewerRelays: false,
    }).catch(() => null);
    if (!profileStateIsCurrent(state, root) || hydrationGeneration !== profileHydrationGeneration) break;
    if (!candidate?.pubkey) continue;
    if (promoteProfile(root, state, candidate, previewProfile)) {
      promoted = true;
      if (Array.isArray(relays) && relays.length) {
        applyProfileRelays(root, state, relays, {
          display: shouldDisplayProfileRelays(state, relays),
        });
      }
      if (hasAuthoritativeProfileEvent(candidate) && hasRenderableProfileMetadata(candidate)) {
        break;
      }
    }
  }
  return promoted;
}

function scheduleProfileMetadataRefresh(root, state, {
  hydrationGeneration = 0,
  previewProfile = null,
  relayStages = [],
} = {}) {
  if (!state || state.metadataRetryStarted) return;
  state.metadataRetryStarted = true;
  void (async () => {
    for (const delay of PROFILE_METADATA_RETRY_DELAYS_MS) {
      await new Promise((resolve) => window.setTimeout(resolve, delay));
      if (!profileStateIsCurrent(state, root) || hydrationGeneration !== profileHydrationGeneration) return;
      await refreshLiveProfileMetadata(root, state, {
        hydrationGeneration,
        previewProfile,
        relayStages: Array.isArray(state.followRelayStages) && state.followRelayStages.length
          ? state.followRelayStages
          : relayStages,
      });
      if (!profileStateIsCurrent(state, root) || hydrationGeneration !== profileHydrationGeneration) return;
      if (hasAuthoritativeProfileEvent(state.profile) && hasRenderableProfileMetadata(state.profile)) return;
    }
  })();
}

function applyProfileRelays(root, state, relays, { display = true, query = true } = {}) {
  const nextRelays = mergeProfileRelays(state?.relays, query ? relays : []);
  if (!state) return query ? nextRelays : normalizeRelayList(relays);
  if (query && !profileRelayListsMatch(state.relays || [], nextRelays)) {
    state.relays = nextRelays;
  }
  if (display) {
    const nextDisplayRelays = mergeProfileRelays(state?.displayRelays, relays);
    if (!profileRelayListsMatch(state.displayRelays || [], nextDisplayRelays)) {
      state.displayRelays = nextDisplayRelays;
    }
  }
  renderProfileRelayPanels(root, state.displayRelays || []);
  return state.relays || nextRelays;
}

function shouldDisplayProfileRelays(state, relays) {
  return profileDisplayRelays(relays, state?.fallbackRelays || []).length > 0;
}

function profileReplyEvent(event) {
  if (Number(event?.kind) !== KIND_NOTE) return false;
  let hasReference = false;
  for (const tag of event?.tags || []) {
    if (!Array.isArray(tag) || tag.length < 2 || tag[0] !== "e") continue;
    hasReference = true;
    const marker = String(tag[3] || "").toLowerCase();
    if (marker === "reply" || marker === "root") return true;
  }
  return hasReference;
}

function profilePostEvent(event) {
  return Number(event?.kind) === KIND_NOTE && !profileReplyEvent(event);
}

function profileMediaEvent(event) {
  const lower = String(event?.content || "").toLowerCase();
  if (!lower.includes("http://") && !lower.includes("https://")) return false;
  return [
    ".jpg", ".jpeg", ".png", ".gif", ".webp", ".avif", ".svg",
    ".mp4", ".webm", ".mov", ".m4v", ".mkv", ".mp3", ".wav", ".ogg",
    "youtube.com/", "youtu.be/", "vimeo.com/", "tenor.com/", "giphy.com/",
  ].some((marker) => lower.includes(marker));
}

function splitProfileTimeline(events) {
  const posts = [];
  const replies = [];
  const media = [];
  for (const event of events || []) {
    if (profilePostEvent(event)) posts.push(event);
    if (profileReplyEvent(event)) replies.push(event);
    if (profileMediaEvent(event)) media.push(event);
  }
  return { posts, replies, media };
}

function mergeEventsNewestFirst(existing = [], incoming = []) {
  const merged = new Map();
  [...existing, ...incoming].forEach((event) => {
    const id = canonicalHex64(event?.id);
    if (!id) return;
    const prev = merged.get(id);
    if (!prev || Number(event?.created_at || 0) >= Number(prev?.created_at || 0)) {
      merged.set(id, { ...event, id });
    }
  });
  return [...merged.values()].sort((a, b) => {
    const delta = Number(b?.created_at || 0) - Number(a?.created_at || 0);
    if (delta !== 0) return delta;
    return String(b?.id || "").localeCompare(String(a?.id || ""));
  });
}

function profileTimelineSeedEvents(pubkey, previewProfile = null) {
  const pk = normalizePubkey(pubkey);
  const event = previewProfile?.timeline_event;
  if (!pk || normalizePubkey(event?.pubkey) !== pk) return [];
  if (!canonicalHex64(event?.id)) return [];
  if (!PROFILE_TIMELINE_KINDS.includes(Number(event?.kind))) return [];
  return [{ ...event, id: canonicalHex64(event.id), pubkey: pk }];
}

function profileListItemsHTML(pubkeys, profilesByPubkey) {
  if (!pubkeys.length) return '<li class="muted profile-follow-empty-item">No profiles found from the connected relays yet.</li>';
  return pubkeys.map((pubkey) => {
    const profile = profilesByPubkey[pubkey] || parseProfile(pubkey, null);
    const name = escapeHTML(displayName(profile));
    const nip05 = String(profile?.nip05 || "").trim();
    const nip05Label = escapeHTML(nip05DisplayText(nip05));
    const npub = escapeHTML(encodeNpub(pubkey));
    const avatar = escapeHTML(preferredAvatarURL(profile));
    const searchText = escapeHTML([
      displayName(profile),
      String(profile?.name || ""),
      String(profile?.display_name || ""),
      String(profile?.nip05 || ""),
      encodeNpub(pubkey),
      pubkey,
    ].join(" ").toLowerCase());
    const avatarHTML = avatar
      ? `<img class="profile-follow-avatar-image" src="${avatar}" alt="" loading="lazy" decoding="async">`
      : '<span class="profile-follow-avatar-fallback" aria-hidden="true">@</span>';
    return `<li class="profile-follow-item" data-profile-follow-item data-profile-follow-search="${searchText}">
      <a href="${escapeHTML(profilePath(pubkey))}" class="profile-follow-link" data-relay-aware>
        <span class="profile-follow-avatar">${avatarHTML}</span>
        <span class="profile-follow-copy">
          <span class="profile-follow-display">${name}</span>
          <span class="muted profile-follow-secondary">${nip05Label || "no nip-05 published"}</span>
          <span class="muted profile-follow-npub mono">${npub}</span>
        </span>
      </a>
    </li>`;
  }).join("");
}

function renderSearchUserResults(root, pubkeys, profilesByPubkey) {
  if (!(root instanceof HTMLElement)) return;
  root.innerHTML = pubkeys.length
    ? `<ul class="profile-follow-list">${profileListItemsHTML(pubkeys, profilesByPubkey)}</ul>`
    : '<p class="muted">No cached profiles matched that query.</p>';
}

function renderProfileFollowPanel(root, kind, pubkeys, profilesByPubkey) {
  const panel = root.querySelector(kind === "following" ? "#user-panel-following" : "#user-panel-followers");
  if (!(panel instanceof HTMLElement)) return;
  const noun = kind === "following" ? "following" : "followers";
  const countNoun = kind === "following" ? "accounts followed" : "followers found";
  panel.dataset.loaded = "1";
  panel.innerHTML = `
    <div class="profile-follow-panel">
      <p class="muted">Loaded from the connected relays. Nostr does not provide a reliable network-wide ${noun} total.</p>
      <p class="muted profile-follow-summary"><strong>${pubkeys.length}</strong> ${countNoun} in this relay view.</p>
      ${pubkeys.length ? `<label class="profile-follow-search">
        <span class="muted">Search this ${noun} list</span>
        <input type="search" data-profile-follow-search-input placeholder="Search by display name, nip-05, or npub" aria-label="Search ${noun} list">
      </label>` : ""}
      <div class="profile-follow-results">
        <ul class="profile-follow-list" data-profile-follow-list>${profileListItemsHTML(pubkeys, profilesByPubkey)}</ul>
        ${pubkeys.length ? '<p class="muted profile-follow-filter-empty" data-profile-follow-filter-empty hidden>No profiles matched this search yet.</p>' : ""}
      </div>
    </div>
  `;
  bindProfileFollowSearch(panel);
  wireAvatarImageFallbacks(panel);
}

function bindProfileFollowSearch(root = document) {
  root.querySelectorAll("[data-profile-follow-search-input]").forEach((input) => {
    if (!(input instanceof HTMLInputElement)) return;
    if (input.dataset.boundProfileFollowSearch === "1") return;
    input.dataset.boundProfileFollowSearch = "1";
    const panel = input.closest(".profile-follow-panel");
    const list = panel?.querySelector("[data-profile-follow-list]");
    const empty = panel?.querySelector("[data-profile-follow-filter-empty]");
    if (!list || !panel) return;
    const applyFilter = () => {
      const query = input.value.trim().toLowerCase();
      let visible = 0;
      list.querySelectorAll("[data-profile-follow-item]").forEach((item) => {
        if (!(item instanceof HTMLElement)) return;
        const haystack = String(item.dataset.profileFollowSearch || "");
        const show = !query || haystack.includes(query);
        item.hidden = !show;
        if (show) visible += 1;
      });
      if (empty instanceof HTMLElement) empty.hidden = visible > 0;
    };
    input.addEventListener("input", applyFilter);
    applyFilter();
  });
}

function renderProfileRelayPanels(root, relays) {
  const listHTML = relays.length
    ? relays.map((relay) => `<li><code class="relay-url-popover" data-check-relay="${escapeHTML(relay)}" tabindex="0" role="button">${escapeHTML(relay)}</code></li>`).join("")
    : '<li class="muted">No relay hints discovered yet.</li>';
  const html = `<p class="muted">Suggested read/write relays for this profile.</p><ul class="relay-list">${listHTML}</ul>`;
  const shell = profileShell(root);
  if (shell instanceof HTMLElement) shell.dataset.profileRelays = relays.join(",");
  [root.querySelector("#user-panel-relays"), root.querySelector("#user-right-relays")].forEach((node) => {
    if (!(node instanceof HTMLElement)) return;
    node.dataset.profileRelays = relays.join(",");
    node.innerHTML = html;
  });
}

function renderProfileIdentifiers(root, profile) {
  const panel = root.querySelector("#user-panel-identifiers");
  const pk = normalizePubkey(profile?.pubkey);
  if (!(panel instanceof HTMLElement) || !pk) return;
  const eventID = String(profile?.event_id || "").trim();
  panel.innerHTML = `
    <div class="ascii-border">+------------------------------------------------------------------+</div>
    <div class="ascii-row">
      <span class="ascii-edge">|</span>
      <div class="ascii-content">
        <h2 class="user-tab-panel-heading">Identifiers</h2>
        <div class="profile-ident-grids">
          <div class="profile-ident-grid-column">
            <p class="muted profile-id-heading">npub</p>
            <div class="profile-ident-grid-wrap profile-npub-copy" data-profile-npub-copy data-npub="${escapeHTML(encodeNpub(pk))}" role="button" tabindex="0" aria-label="Copy npub to clipboard">
              ${profileNpubBlockHTML(pk)}
            </div>
          </div>
          <div class="profile-ident-grid-column">
            <p class="muted profile-id-heading">hex</p>
            <div class="profile-ident-grid-wrap">${keyGridHTML(pk)}</div>
          </div>
        </div>
        ${eventID ? `<p class="muted">metadata fetched from kind 0 event <a href="/thread/${escapeHTML(eventID)}" data-relay-aware>${escapeHTML(eventID.slice(0, 12))}</a></p>` : '<p class="muted">no metadata event discovered yet</p>'}
      </div>
      <span class="ascii-edge">|</span>
    </div>
    <div class="ascii-border">+------------------------------------------------------------------+</div>
  `;
  bindProfileStatLinks(panel);
}

function renderProfileTabCounts(root, counts) {
  const mapping = [
    { selector: "[data-profile-post-count]", value: counts.posts },
    { selector: "[data-profile-reply-count]", value: counts.replies },
    { selector: "[data-profile-media-count]", value: counts.media },
    {
      selector: "[data-profile-following-count]",
      value: counts.following,
      wrapperSelector: "[data-profile-following-count-wrap]",
    },
    {
      selector: "[data-profile-followers-count]",
      value: counts.followers,
      wrapperSelector: "[data-profile-followers-count-wrap]",
    },
  ];
  mapping.forEach(({ selector, value, wrapperSelector, hideWhenMissing }) => {
    const hasValue = typeof value === "string" ? value.length > 0 : Number.isFinite(value);
    if (wrapperSelector) {
      root.querySelectorAll(wrapperSelector).forEach((node) => {
        node.hidden = Boolean(hideWhenMissing && !hasValue);
      });
    }
    if (!hasValue) return;
    root.querySelectorAll(selector).forEach((node) => {
      node.textContent = `${value}`;
    });
  });
}

function profileCount(items) {
  return Array.isArray(items) ? items.length : undefined;
}

function withTimeout(promise, ms, label = "operation") {
  return new Promise((resolve, reject) => {
    const timer = window.setTimeout(() => reject(new Error(`${label} timed out`)), ms);
    promise.then(
      (value) => {
        window.clearTimeout(timer);
        resolve(value);
      },
      (error) => {
        window.clearTimeout(timer);
        reject(error);
      },
    );
  });
}

function setRelayNativeProfileFollowCountLoading(root, kind) {
  if (kind === "following") {
    renderProfileTabCounts(root, { following: "..." });
    return;
  }
  if (kind === "followers") {
    renderProfileTabCounts(root, { followers: "..." });
  }
}

async function ensureRelayNativeProfileFollowGraph(root, state, options = {}) {
  if (!state) return false;
  if (Array.isArray(state.following) && Array.isArray(state.followers)) return true;
  const requestedKind = options.requestedKind === "followers" ? "followers" : "following";
  setRelayNativeProfileFollowCountLoading(root, requestedKind);
  if (state.followGraphPromise) {
    await state.followGraphPromise;
    return Array.isArray(state.following) && Array.isArray(state.followers);
  }
  const hydrationGeneration = options.hydrationGeneration ?? profileHydrationGeneration;
  // WebKit can occasionally return an empty combined follow query even when
  // the local desktop relay client can retrieve the author's kind-3 event.
  // Start that loopback fallback alongside the browser relay reads so it does
  // not add a second timeout after the first result is empty.
  const desktopFollowGraphPromise = fetchDesktopProfileFollowGraph(state.pubkey).catch(() => null);
  state.followGraphPromise = withTimeout(
    (async () => {
      const initialStages = Array.isArray(state.followRelayStages) && state.followRelayStages.length
        ? state.followRelayStages
        : [state.relays];
      const relayStages = await Promise.resolve(state.followRelayStagesPromise)
        .then((stages) => (Array.isArray(stages) && stages.length ? stages : initialStages))
        .catch(() => initialStages);
      return fetchProfileFollowGraphAcrossRelayStages(
        state.pubkey,
        relayStages,
        {
          fetchFollowGraph: fetchProfileFollowGraph,
        },
      );
    })(),
    PROFILE_FOLLOW_TIMEOUT_MS,
    "profile follow graph",
  ).then(async (followGraph) => {
    if (hydrationGeneration !== profileHydrationGeneration || relayNativeProfileState !== state) return false;
    if (!followGraph?.followEvent) {
      const localGraph = await desktopFollowGraphPromise;
      if (localGraph?.followEvent) {
        followGraph = {
          ...localGraph,
          followers: [...new Set([...(followGraph?.followers || []), ...localGraph.followers])],
          relaysUsed: localGraph.relays,
        };
      }
    }
    if (hydrationGeneration !== profileHydrationGeneration || relayNativeProfileState !== state) return false;
    if (Array.isArray(followGraph?.relaysUsed) && followGraph.relaysUsed.length) {
      applyProfileRelays(root, state, followGraph.relaysUsed, {
        display: shouldDisplayProfileRelays(state, followGraph.relaysUsed),
      });
    }
    state.following = Array.isArray(followGraph.following) ? followGraph.following : [];
    state.followers = Array.isArray(followGraph.followers) ? followGraph.followers : [];
    renderProfileTabCounts(root, {
      following: state.following.length,
      followers: state.followers.length,
    });
    const activeFollowing = root.querySelector("#user-tab-following:checked");
    const activeFollowers = root.querySelector("#user-tab-followers:checked");
    if (activeFollowing) {
      await hydrateRelayNativeProfileTab("following", root);
      return true;
    }
    if (activeFollowers) {
      await hydrateRelayNativeProfileTab("followers", root);
    }
    return true;
  }).catch(() => {
    if (hydrationGeneration !== profileHydrationGeneration || relayNativeProfileState !== state) return false;
    state.following = [];
    state.followers = [];
    renderProfileTabCounts(root, {
      following: 0,
      followers: 0,
    });
    renderProfileFollowPanel(root, "following", state.following, state.profiles);
    renderProfileFollowPanel(root, "followers", state.followers, state.profiles);
    return false;
  }).finally(() => {
    if (relayNativeProfileState === state) {
      state.followGraphPromise = null;
    }
  });
  return state.followGraphPromise;
}

export async function hydrateRelayNativeProfileTab(kind, root = document) {
  const state = currentProfileState(root);
  if (!state || (kind !== "following" && kind !== "followers")) return false;
  const panel = root.querySelector(kind === "following" ? "#user-panel-following" : "#user-panel-followers");
  if (!(panel instanceof HTMLElement)) return false;
  if (!Array.isArray(state[kind])) {
    const loaded = await ensureRelayNativeProfileFollowGraph(root, state, { requestedKind: kind });
    if (!loaded || !Array.isArray(state[kind])) return false;
  }
  const pubkeys = state[kind];
  if (!pubkeys.length) {
    renderProfileFollowPanel(root, kind, [], state.profiles);
    return true;
  }
  // Make a large following list usable immediately with npub fallbacks. Profile
  // metadata can involve hundreds of authors and must not hold the entire tab
  // behind its loader while relay batches finish.
  renderProfileFollowPanel(root, kind, pubkeys, state.profiles);
  const missing = pubkeys.filter((pubkey) => !state.profiles[pubkey]);
  if (missing.length) {
    void fetchProfiles(missing, { relays: state.relays }).then((profiles) => {
      if (relayNativeProfileState !== state) return;
      state.profiles = { ...state.profiles, ...profiles };
      const input = panel.querySelector("[data-profile-follow-search-input]");
      const searchValue = input instanceof HTMLInputElement ? input.value : "";
      renderProfileFollowPanel(root, kind, pubkeys, state.profiles);
      const refreshedInput = panel.querySelector("[data-profile-follow-search-input]");
      if (refreshedInput instanceof HTMLInputElement && searchValue) {
        refreshedInput.value = searchValue;
        refreshedInput.dispatchEvent(new Event("input", { bubbles: true }));
      }
    }).catch(() => {});
  }
  return true;
}

function configureProfileLoadMore(root, notes) {
  const button = profileLoadMoreButton(root);
  if (!button) return;
  const cursor = feedPageCursor(notes);
  const hasMore = feedPageMayHaveMore(notes);
  button.dataset.cursor = cursor.until > 0 ? String(cursor.until) : "";
  button.dataset.cursorId = cursor.cursorId || "";
  button.dataset.hasMore = hasMore ? "1" : "0";
  button.hidden = !hasMore;
  button.disabled = false;
  button.textContent = "Load more";
}

async function renderProfilePostsFeed(root, postsFeed, state, emptyText = "No posts yet.") {
  const transition = currentThreadTransition();
  const selectedID = transition?.selectedNoteID || "";
  const visiblePosts = Array.isArray(state.posts) ? state.posts.slice(0, PROFILE_PAGE_SIZE) : [];
  const carried = selectedID
    ? postsFeed.querySelector(`.ptxt-carried-profile-note#note-${selectedID}, .ptxt-carried-thread-note#note-${selectedID}`)
    : null;

  postsFeed.dataset.relayNativeProfile = "1";
  dismissProfilePostsLoader(root);

  if (carried instanceof HTMLElement && selectedID) {
    const matchedEvent = visiblePosts.find((event) => String(event?.id || "").toLowerCase() === selectedID);
    if (matchedEvent) {
      const pk = normalizePubkey(matchedEvent.pubkey);
      const profile = state.profiles[pk] || parseProfile(pk, null);
      applyThreadTransitionNames(carried, selectedID);
      const feedNote = createNoteArticleFromThreadShell(carried, matchedEvent, profile, {
        referencedByID: state.referencedByID,
        profilesByPubkey: state.profiles,
      });
      applyThreadTransitionNames(feedNote, selectedID);
      const otherPosts = visiblePosts.filter((event) => String(event?.id || "").toLowerCase() !== selectedID);
      await runNoteViewTransition(transition, () => {
        carried.replaceWith(feedNote);
        if (otherPosts.length) {
          appendNoteFeed(postsFeed, otherPosts, state.profiles, {
            referencedByID: state.referencedByID,
          });
        } else if (!postsFeed.querySelector(".note[id^='note-']")) {
          const empty = document.createElement("p");
          empty.className = "muted";
          empty.textContent = emptyText;
          postsFeed.append(empty);
        }
      });
      applyDestinationProfileTransition(root, selectedID);
      clearThreadTransition(selectedID);
      return;
    }

    await runNoteViewTransition(transition, () => {
      carried.classList.add("ptxt-note-removing");
      carried.remove();
    });
    clearThreadTransition(selectedID);
  }

  renderNoteFeed(postsFeed, visiblePosts, state.profiles, {
    emptyText,
    referencedByID: state.referencedByID,
  });
}

async function renderProfileNotesPanels(root, state, options = {}) {
  const postsFeed = profilePostsFeed(root);
  const repliesFeed = root.querySelector('#user-panel-replies [data-profile-feed="replies"]');
  const mediaFeed = root.querySelector('#user-panel-media [data-profile-feed="media"]');
  const emptyText = options.emptyText || {};
  if (postsFeed) {
    await renderProfilePostsFeed(root, postsFeed, state, emptyText.posts || "No posts yet.");
  }
  if (repliesFeed) {
    renderNoteFeed(repliesFeed, state.replies, state.profiles, {
      emptyText: emptyText.replies || "No replies yet.",
      referencedByID: state.referencedByID,
    });
  }
  if (mediaFeed) {
    renderNoteFeed(mediaFeed, state.media, state.profiles, {
      emptyText: emptyText.media || "No media posts yet.",
      referencedByID: state.referencedByID,
    });
  }
  renderProfileTabCounts(root, {
    posts: state.posts.length,
    replies: state.replies.length,
    media: state.media.length,
    following: profileCount(state.following),
    followers: profileCount(state.followers),
  });
  configureProfileLoadMore(root, state.timeline);
  if (postsFeed) {
    refreshAscii(postsFeed);
    initViewMore(postsFeed);
    void refreshVisibleFeedNoteMetadata(root, window.location.href, { feedSelector: "#user-panel-posts [data-feed]" });
    void refreshVisibleNoteProfiles(root);
  }
  if (repliesFeed) refreshAscii(repliesFeed);
  if (mediaFeed) refreshAscii(mediaFeed);
}

function currentProfileState(root = document) {
  if (!isRelayNativeProfile(root)) return null;
  return relayNativeProfileState;
}

function profileStateIsCurrent(state, root = document) {
  if (!state) return false;
  if (relayNativeProfileState !== state) return false;
  if (!isRelayNativeProfile(root)) return false;
  return pubkeyFromProfilePath(window.location.pathname) === state.pubkey;
}

function feedInitialTimeoutViewModel(routeURL, sort) {
  const url = new URL(routeURL, globalThis.location?.origin || "http://localhost");
  return {
    route: "feed",
    url: url.toString(),
    path: `${url.pathname}${url.search}${url.hash}`,
    sort: sort || "recent",
    notes: [],
    cursor: "",
    hasMore: false,
    source: "timeout",
  };
}

function scheduleFeedEmptyFallback(root, feed, {
  hydrationGeneration = 0,
  viewer = "",
  sort = "recent",
  delayMs = FEED_REFRESH_EMPTY_FALLBACK_MS,
} = {}) {
  window.clearTimeout(feedEmptyFallbackTimer);
  feedEmptyFallbackTimer = window.setTimeout(() => {
    feedEmptyFallbackTimer = 0;
    if (hydrationGeneration !== feedHydrationGeneration) return;
    if (!feed?.isConnected || homeFeed(root) !== feed) return;
    if (!feed.querySelector("[data-feed-loader]")) return;
    if (feed.querySelector(".note[id^='note-']")) return;
    void applyFeedViewModel(root, {
      route: "feed",
      sort: sort || "recent",
      notes: [],
      cursor: "",
      hasMore: false,
    }, { viewer, allowEmptyLoaderReplacement: true });
    setHomeLoadMorePending(root, false);
  }, Math.max(0, Number(delayMs) || 0));
}

async function firstPaintFeedViewModel(routeURL, {
  viewer = "",
  sort = "recent",
} = {}) {
  const url = new URL(routeURL, globalThis.location?.origin || "http://localhost");
  const notes = await fetchFirstPaintFeedNotes({
    viewerPubkey: viewer,
    sort,
    limit: FEED_FIRST_PAINT_LIMIT,
  }).catch(() => []);
  if (!notes.length) return null;
  const pubkeys = feedNoteAuthorPubkeys(notes);
  const profiles = await fetchProfiles(pubkeys).catch(() => ({}));
  return {
    route: "feed",
    url: url.toString(),
    path: `${url.pathname}${url.search}${url.hash}`,
    sort: sort || "recent",
    notes,
    profiles,
    cursor: feedPageCursor(notes),
    hasMore: feedPageMayHaveMore(notes),
    source: "first-paint",
  };
}

function nonEmptyFirstPaintPromise(firstPaintPromise) {
  if (!firstPaintPromise) return null;
  return firstPaintPromise.then(
    (viewModel) => ((viewModel?.notes?.length || 0) > 0 ? viewModel : NEVER),
    () => NEVER,
  );
}

function loadFeedWithInitialPaintDeadline(loadPromise, firstPaintPromise = null) {
  const firstPaint = nonEmptyFirstPaintPromise(firstPaintPromise);
  const candidate = firstPaint ? Promise.race([loadPromise, firstPaint]) : loadPromise;
  return new Promise((resolve) => {
    let settled = false;
    const timer = window.setTimeout(() => {
      if (settled) return;
      settled = true;
      resolve(FEED_LOAD_TIMED_OUT);
    }, FEED_INITIAL_PAINT_TIMEOUT_MS);
    candidate.then(
      (value) => {
        if (settled) return;
        settled = true;
        window.clearTimeout(timer);
        resolve(value);
      },
      () => {
        if (settled) return;
        settled = true;
        window.clearTimeout(timer);
        resolve(null);
      },
    );
  });
}

function feedPageContextIsCurrent(root, feed, button, sort = "") {
  if (!feed || !button) return false;
  if (!feed.isConnected || !button.isConnected) return false;
  if (homeFeed(root) !== feed || homeLoadMoreButton(root) !== button) return false;
  if (!isRelayNativeFeed(root)) return false;
  return !sort || feed.dataset.relayNativeFeedSort === sort;
}

function applyFeedViewModelWhenReady(root, feed, loadPromise, {
  hydrationGeneration = 0,
  viewer = "",
  sort = "",
  forceFetch = false,
  sortChanged = false,
  allowEmptyFallback = false,
  coldInitialPaint = false,
} = {}) {
  void loadPromise.then(async (lateViewModel) => {
    if (hydrationGeneration !== feedHydrationGeneration) return;
    if (!feed?.isConnected || homeFeed(root) !== feed) return;
    const lateNotes = Array.isArray(lateViewModel?.notes) ? lateViewModel.notes : [];
    const lateSort = String(lateViewModel?.sort || sort || "recent");
    if (!lateNotes.length) {
      if (allowEmptyFallback && feed.querySelector("[data-feed-loader]")) {
        scheduleFeedEmptyFallback(root, feed, {
          hydrationGeneration,
          viewer,
          sort: lateSort,
        });
      }
      return;
    }
    await applyFeedViewModel(root, {
      route: "feed",
      sort: lateSort,
      notes: lateNotes,
      cursor: lateViewModel?.cursor || feedPageCursor(lateNotes),
      hasMore: typeof lateViewModel?.hasMore === "boolean" ? lateViewModel.hasMore : feedPageMayHaveMore(lateNotes),
    }, { viewer, allowEmptyLoaderReplacement: true });
    if (hydrationGeneration !== feedHydrationGeneration || homeFeed(root) !== feed) return;
    setHomeLoadMorePending(root, false);
    const metadataPrefetched = Boolean(
      lateViewModel?.profiles && typeof lateViewModel.profiles === "object" && Object.keys(lateViewModel.profiles).length,
    );
    void enrichRenderedFeed(root, feed, lateNotes, {
      hydrationGeneration,
      sort: lateSort,
      viewer,
      coldInitialPaint,
      replacingVisibleFeed: false,
      forceRefresh: forceFetch,
      sortChanged,
      metadataPrefetched,
    });
  }).catch(() => {});
}

function watchLateFeedViewModels(root, feed, promises, options = {}) {
  const seen = new Set();
  for (const promise of promises) {
    if (!promise || seen.has(promise)) continue;
    seen.add(promise);
    applyFeedViewModelWhenReady(root, feed, promise, options);
  }
}

export async function hydrateFeedRoute(root = document, options = {}) {
  const feed = homeFeed(root);
  if (!feed) return;

  if (options.skipHydrate) {
    if (feed.querySelector(".note[id^='note-']")) {
      afterFeedNotesRendered(root, feed);
    }
    return;
  }

  const viewer = normalizedPubkey();
  clearBootstrapPendingIfViewerChanged(viewer);
  const sort = currentFeedSort(viewer);
  const sortChanged = renderedFeedSortChanged(feed, sort);
  const sessionChanged = homeFeedSessionChanged(root);
  const hasNotes = Boolean(feed.querySelector(".note[id^='note-']"));
  const coldInitialPaint = !hasNotes && !isRelayNativeFeed(root);
  const preserveExistingNotes = options.preserveExistingNotes === true;
  const hydrationGeneration = ++feedHydrationGeneration;
  window.clearTimeout(feedEmptyFallbackTimer);
  feedEmptyFallbackTimer = 0;
  if (
    preserveExistingNotes &&
    !options.forceRefresh &&
    !sessionChanged &&
    hasNotes &&
    !isRelayNativeFeed(root)
  ) {
    afterFeedNotesRendered(root, feed);
    return;
  }
  if (
    !options.forceRefresh &&
    !sessionChanged &&
    feed.dataset.relayNativeFeed === "1" &&
    hasNotes
  ) {
    afterFeedNotesRendered(root, feed);
    return;
  }

  const forceFetch = Boolean(options.forceRefresh || sessionChanged);
  const replacingVisibleFeed = Boolean((sessionChanged || options.forceRefresh) && hasNotes);
  if (sessionChanged || options.forceRefresh) {
    setHomeLoadMorePending(root, true);
    showHomeFeedRefreshLoader(root, {
      percent: replacingVisibleFeed ? 12 : 8,
      statusMessage: sortChanged ? "reordering feed..." : sessionChanged ? "syncing feed preferences..." : "refreshing feed...",
    });
  }

  const pageLimit = powerLimitedCount(50, 30);
  const routeURL = globalThis.location?.href || "/";
  let viewModel = null;
  let latestForceFetch = forceFetch;
  for (const delay of FEED_INITIAL_BACKOFF_MS) {
    if (delay > 0) {
      await new Promise((resolve) => window.setTimeout(resolve, delay));
    }
    const loadPromise = loadFeed(routeURL, {
      viewerPubkey: viewer,
      limit: pageLimit,
      forceFetch: latestForceFetch,
    });
    const firstPaintPromise = firstPaintFeedViewModel(routeURL, { viewer, sort });
    const loaded = await loadFeedWithInitialPaintDeadline(loadPromise, firstPaintPromise);
    if (hydrationGeneration !== feedHydrationGeneration) return;
    const lateHydrationOpts = {
      hydrationGeneration,
      viewer,
      sort,
      forceFetch: latestForceFetch,
      sortChanged: sessionChanged,
      allowEmptyFallback: latestForceFetch || sortChanged,
      coldInitialPaint,
    };
    if (loaded?.source === "first-paint") {
      watchLateFeedViewModels(root, feed, [loadPromise], lateHydrationOpts);
    }
    if (loaded === FEED_LOAD_TIMED_OUT) {
      watchLateFeedViewModels(root, feed, [firstPaintPromise, loadPromise], lateHydrationOpts);
      viewModel = feedInitialTimeoutViewModel(routeURL, sort);
      break;
    }
    viewModel = loaded;
    if ((viewModel?.notes?.length || 0) > 0) {
      break;
    }
    latestForceFetch = true;
  }
  if (!feed.isConnected || homeFeed(root) !== feed) return;
  const notes = Array.isArray(viewModel?.notes) ? viewModel.notes : [];
  const resolvedSort = String(viewModel?.sort || sort || "recent");
  const initialPaintTimedOut = viewModel?.source === "timeout";
  const refreshPendingWithEmptyFeed = notes.length < 1 && (forceFetch || sortChanged) && feed.querySelector("[data-feed-loader]");
  const onboardingPending = shouldShowFirstLoginBootstrap(viewer);
  if (refreshPendingWithEmptyFeed) {
    scheduleFeedEmptyFallback(root, feed, {
      hydrationGeneration,
      viewer,
      sort: resolvedSort,
    });
    return;
  }
  if (
    (!initialPaintTimedOut && shouldPreserveDeferredFeedLoader(feed, notes, viewer)) ||
    (notes.length < 1 && !viewer && replacingVisibleFeed) ||
    (initialPaintTimedOut && feed.querySelector(".note[id^='note-']"))
  ) return;
  if (
    preserveExistingNotes &&
    !forceFetch &&
    !sessionChanged &&
    !isRelayNativeFeed(root) &&
    feed.querySelector(".note[id^='note-']")
  ) {
    afterFeedNotesRendered(root, feed);
    return;
  }
  if (notes.length > 0 && !forceFetch && !powerSaverActive() && !pageIsHidden()) {
    void fetchFeedNotes({ viewerPubkey: viewer, limit: 50, sort: resolvedSort, forceFetch: true }).catch(() => {});
  }

  await applyFeedViewModel(root, {
    route: "feed",
    sort: resolvedSort,
    notes,
    cursor: viewModel?.cursor || feedPageCursor(notes),
    hasMore: typeof viewModel?.hasMore === "boolean" ? viewModel.hasMore : feedPageMayHaveMore(notes),
  }, { viewer, allowEmptyLoaderReplacement: initialPaintTimedOut });
  setHomeLoadMorePending(root, false);
  if (
    onboardingPending &&
    viewer &&
    hydrationGeneration === feedHydrationGeneration &&
    homeFeed(root) === feed &&
    !feed.querySelector("[data-feed-loader]")
  ) {
    markBootstrapComplete(viewer);
  }
  if (sessionChanged || options.forceRefresh) {
    const button = homeLoadMoreButton(root);
    if (button) {
      button.dataset.cursor = "";
      button.dataset.cursorId = "";
    }
  }
  void enrichRenderedFeed(root, feed, notes, {
    hydrationGeneration,
    sort: resolvedSort,
    viewer,
    coldInitialPaint,
    replacingVisibleFeed,
    forceRefresh: Boolean(options.forceRefresh),
    sortChanged: sessionChanged,
    metadataPrefetched: Boolean(viewModel?.source === "network"),
  });
}

export async function appendClientFeedPage(root = document) {
  const feed = homeFeed(root);
  const button = homeLoadMoreButton(root);
  if (!feed || !button || !isRelayNativeFeed(root)) {
    return { appended: 0, hasMore: false, cursorAdvanced: false };
  }

  const viewer = normalizedPubkey();
  const sort = feed.dataset.relayNativeFeedSort || currentFeedSort(viewer);
  const until = Number.parseInt(button.dataset.cursor || "0", 10);
  const untilID = button.dataset.cursorId || "";
  const notes = await fetchFeedNotes({
    viewerPubkey: viewer,
    limit: powerLimitedCount(50, 30),
    sort,
    until: until > 0 ? until : undefined,
    untilID: untilID || undefined,
  });
  if (!feedPageContextIsCurrent(root, feed, button, sort)) {
    return { appended: 0, hasMore: false, cursorAdvanced: false };
  }
  if (!notes.length) {
    button.dataset.hasMore = "0";
    return { appended: 0, hasMore: false, cursorAdvanced: false };
  }

  const previousCursor = button.dataset.cursor || "";
  const previousCursorID = button.dataset.cursorId || "";
  const pubkeys = [...new Set(notes.map((event) => normalizePubkey(event.pubkey)).filter(Boolean))];
  const [profiles, referencedByID] = await Promise.all([
    fetchProfiles(pubkeys),
    hydrateReferencedEvents(notes),
  ]);
  if (!feedPageContextIsCurrent(root, feed, button, sort)) {
    return { appended: 0, hasMore: false, cursorAdvanced: false };
  }
  const appended = appendNoteFeed(feed, notes, profiles, { referencedByID });
  configureHomeLoadMore(root, notes);
  const cursorAdvanced =
    button.dataset.cursor !== previousCursor || button.dataset.cursorId !== previousCursorID;
  if (appended > 0) {
    afterFeedNotesRendered(root, feed);
  }
  return {
    appended,
    hasMore: feedPageMayHaveMore(notes),
    cursorAdvanced,
  };
}

export async function hydrateBookmarksRoute(root = document) {
  const pubkey = normalizedPubkey();
  const feed = feedRoot(root);
  if (!feed) return;
  if (!pubkey) {
    feed.replaceChildren();
    const prompt = document.createElement("p");
    prompt.innerHTML = '<a href="/login" data-relay-aware>Login to view bookmarks</a>';
    feed.append(prompt);
    return;
  }
  const payload = await fetchBookmarks(pubkey);
  const ids = Array.isArray(payload?.ids) ? payload.ids : [];
  const events = await fetchEventsByIDs(ids);
  const byID = new Map(events.map((event) => [event.id, event]));
  const ordered = ids.map((id) => byID.get(id)).filter(Boolean);
  const pubkeys = [...new Set(ordered.map((event) => normalizePubkey(event.pubkey)).filter(Boolean))];
  const [profiles, referencedByID] = await Promise.all([
    fetchProfiles(pubkeys),
    hydrateReferencedEvents(ordered),
  ]);
  renderNoteFeed(feed, ordered, profiles, { emptyText: "No bookmarks yet.", referencedByID });
  refreshAscii(root);
  void refreshVisibleFeedNoteMetadata(root, window.location.href, { feedSelector: "[data-feed]" });
}

export async function hydrateNotificationsRoute(root = document) {
  await hydrateNotificationsPage(root);
  void refreshVisibleFeedNoteMetadata(root, window.location.href, { feedSelector: "[data-feed]" });
}

async function fetchInitialProfilePosts(pubkey, { relays = [], kinds = PROFILE_TIMELINE_KINDS, preferCache = true } = {}) {
  if (preferCache) {
    const cached = await eventsByAuthors([pubkey], {
      kinds,
      limit: PROFILE_PAGE_SIZE,
    }).catch(() => []);
    if (cached.length > 0) {
      return cached;
    }
  }
  let latest = [];
  for (const delay of PROFILE_INITIAL_POST_BACKOFF_MS) {
    if (delay > 0) {
      await new Promise((resolve) => window.setTimeout(resolve, delay));
    }
    latest = await fetchNotesByAuthors([pubkey], {
      limit: PROFILE_PAGE_SIZE,
      relays,
      kinds,
      includeViewerRelays: false,
    });
    if (latest.length > 0) {
      return latest;
    }
  }
  return latest;
}

export async function hydrateProfileRoute(root = document, options = {}) {
  if (options.skipHydrate) return;
  const pubkey = pubkeyFromProfilePath(window.location.pathname);
  if (!pubkey) return;

  const hydrationGeneration = ++profileHydrationGeneration;
  if (relayNativeProfileState?.pubkey && relayNativeProfileState.pubkey !== pubkey) {
    relayNativeProfileState = null;
  }
  const viewerPubkey = normalizedPubkey();
  const bootstrapProfile = normalizePubkey(appBootstrap().initialProfile?.pubkey) === pubkey
    ? appBootstrap().initialProfile
    : null;
  const previewProfile = profileRoutePreview(pubkey) || bootstrapProfile;
  const fallbackRelays = readRelaysForViewer();
  const bootstrapRelays = profileBootstrapRelays(root);
  const previewRelays = normalizeRelayList(Array.isArray(previewProfile?.relay_hints) ? previewProfile.relay_hints : []);
  const preferredRelays = normalizeRelayList([
    ...profileDisplayRelays(bootstrapRelays, fallbackRelays),
    ...profileDisplayRelays(previewRelays, fallbackRelays),
  ]);
  const discoveryRelays = normalizeRelayList([...bootstrapRelays, ...previewRelays, ...fallbackRelays]);
  const initialQueryRelays = preferredRelays.length ? preferredRelays : fallbackRelays;
  const relayHintsPromise = withTimeout(fetchProfileRelayHints(pubkey, {
    relays: discoveryRelays,
    includeViewerRelays: false,
  }), PROFILE_INITIAL_HYDRATE_TIMEOUT_MS, "profile relay hints").catch(() => emptyProfileRelayHints());
  const followContactsPromise = viewerPubkey
    ? withTimeout(
      fetchFollowContacts(viewerPubkey),
      PROFILE_INITIAL_HYDRATE_TIMEOUT_MS,
      "profile follow contacts",
    ).catch(() => null)
    : Promise.resolve(null);
  const followRelayStagesPromise = Promise.all([relayHintsPromise, followContactsPromise])
    .then(([relayHints, followContacts]) => buildProfileHydrationRelayStages(
      initialQueryRelays,
      relayHints,
      followContacts?.relayHints,
      pubkey,
      fallbackRelays,
    ))
    .catch(() => [initialQueryRelays]);

  const immediateProfile = applyImmediateProfileShell(root, pubkey, previewProfile);
  if (immediateProfile && profileHasResolvedMetadata(immediateProfile)) {
    applyProfileHero(root, immediateProfile);
  }

  ensureProfilePostsLoader(root);
  updateProfilePostsLoader(root, {
    percent: 8,
    statusMessage: "reading cached profile...",
  });

  const cachedProfileEvent = await latestReplaceable(pubkey, KIND_PROFILE).catch(() => null);
  if (hydrationGeneration !== profileHydrationGeneration) return;
  if (pubkeyFromProfilePath(window.location.pathname) !== pubkey) return;
  updateProfilePostsLoader(root, {
    percent: 18,
    statusMessage: "checking user relays...",
  });
  const cachedProfile = rememberProfile(mergeProfileSeedProfile(parseProfile(pubkey, cachedProfileEvent), previewProfile));
  const state = {
    pubkey,
    relays: initialQueryRelays,
    displayRelays: preferredRelays,
    fallbackRelays,
    profile: cachedProfile,
    timeline: [],
    posts: [],
    replies: [],
    media: [],
    following: null,
    followers: null,
    followGraphPromise: null,
    followRelayStages: [initialQueryRelays],
    followRelayStagesPromise,
    profiles: { [pubkey]: cachedProfile },
    referencedByID: new Map(),
  };
  relayNativeProfileState = state;
  const seedEvents = profileTimelineSeedEvents(pubkey, previewProfile);
  if (seedEvents.length) {
    void putEvents(seedEvents).catch(() => {});
  }
  const postsFeed = profilePostsFeed(root);
  if (postsFeed instanceof HTMLElement) {
    postsFeed.dataset.relayNativeProfile = "1";
  }
  if (profileHasResolvedMetadata(cachedProfile)) {
    applyProfileHero(root, cachedProfile);
  }
  renderProfileIdentifiers(root, cachedProfile);
  renderProfileRelayPanels(root, preferredRelays);
  void relayHintsPromise.then((relayHints) => {
    if (hydrationGeneration !== profileHydrationGeneration || relayNativeProfileState !== state) return;
    applyProfileRelays(root, state, profileRelayHintsToList(relayHints), { query: false });
  });
  void followRelayStagesPromise.then((relayStages) => {
    if (hydrationGeneration !== profileHydrationGeneration || relayNativeProfileState !== state) return;
    if (Array.isArray(relayStages) && relayStages.length) {
      state.followRelayStages = relayStages;
    }
  });
  void ensureRelayNativeProfileFollowGraph(root, state, { hydrationGeneration });

  const initialPostsPromise = withTimeout(fetchInitialProfilePosts(pubkey, {
    relays: initialQueryRelays,
    kinds: PROFILE_TIMELINE_KINDS,
  }), PROFILE_INITIAL_HYDRATE_TIMEOUT_MS, "initial profile posts").catch(() => []);
  const initialProfilePromise = withTimeout(fetchProfile(pubkey, {
    relays: initialQueryRelays,
    forceRefresh: !hasAuthoritativeProfileEvent(cachedProfile),
    includeViewerRelays: false,
  }), PROFILE_INITIAL_HYDRATE_TIMEOUT_MS, "initial profile metadata").catch(() => null);
  const hintedPostsPromise = withTimeout(
    followRelayStagesPromise.then((stages) => fetchInitialProfilePostsAcrossRelayStages(pubkey, stages, {
      kinds: PROFILE_TIMELINE_KINDS,
      fetchPosts: (targetPubkey, { relays, kinds }) => fetchNotesByAuthors([targetPubkey], {
        limit: PROFILE_PAGE_SIZE,
        relays,
        kinds,
        includeViewerRelays: false,
      }),
    })),
    PROFILE_HINTED_POSTS_TIMEOUT_MS,
    "profile outbox posts",
  ).catch(() => ({ posts: [], relaysUsed: [] }));

  updateProfilePostsLoader(root, {
    percent: 34,
    statusMessage: "fetching recent notes...",
  });

  const isCurrent = () => (
    hydrationGeneration === profileHydrationGeneration &&
    relayNativeProfileState === state &&
    pubkeyFromProfilePath(window.location.pathname) === pubkey
  );
  const applyPosts = async (nextPosts) => {
    if (!isCurrent()) return false;
    const split = splitProfileTimeline(nextPosts);
    state.timeline = Array.isArray(nextPosts) ? nextPosts : [];
    state.posts = split.posts;
    state.replies = split.replies;
    state.media = split.media;
    const pubkeys = [...new Set(state.timeline.map((event) => normalizePubkey(event.pubkey)).filter(Boolean))];
    const cachedPostProfiles = await cachedProfilesByPubkey(pubkeys).catch(() => ({}));
    if (!isCurrent()) return false;
    state.profiles = { ...cachedPostProfiles, ...state.profiles };
    await renderProfileNotesPanels(root, state);
    return true;
  };
  const refreshPostContext = async (nextPosts = state.posts) => {
    if (!Array.isArray(nextPosts) || !nextPosts.length || !isCurrent()) return;
    const pubkeys = [...new Set(nextPosts.map((event) => normalizePubkey(event.pubkey)).filter(Boolean))];
    const [profiles, referencedByID] = await Promise.all([
      fetchProfiles(pubkeys, { relays: state.relays }).catch(() => ({})),
      hydrateReferencedEvents(nextPosts).catch(() => new Map()),
    ]);
    if (!isCurrent()) return;
    state.profiles = { ...state.profiles, ...profiles };
    state.referencedByID = referencedByID;
    await renderProfileNotesPanels(root, state);
  };

  let criticalProfile = cachedProfile;
  void initialProfilePromise.then((profile) => {
    if (!isCurrent() || !profile?.pubkey) return;
    criticalProfile = profile;
    promoteProfile(root, state, profile, previewProfile);
  });

  let posts = mergeEventsNewestFirst(seedEvents, await initialPostsPromise);
  if (!isCurrent()) return;

  updateProfilePostsLoader(root, {
    percent: 52,
    statusMessage: posts.length ? "assembling profile timeline..." : "checking alternate relays...",
  });

  let relayStages = [initialQueryRelays];
  if (posts.length) {
    updateProfilePostsLoader(root, {
      percent: 88,
      statusMessage: "rendering posts...",
    });
    await applyPosts(posts);
    if (!isCurrent()) return;
    void refreshPostContext(posts);
  } else {
    updateProfilePostsLoader(root, {
      percent: 64,
      statusMessage: "checking author outbox relays...",
    });
    const recovered = await hintedPostsPromise;
    if (!isCurrent()) return;
    posts = recovered.posts;
    if (recovered.relaysUsed.length) {
      applyProfileRelays(root, state, recovered.relaysUsed, {
        display: shouldDisplayProfileRelays(state, recovered.relaysUsed),
      });
    }
    updateProfilePostsLoader(root, {
      percent: 88,
      statusMessage: "rendering posts...",
    });
    await applyPosts(posts);
    if (!isCurrent()) return;
    void refreshPostContext(posts);
  }

  void followRelayStagesPromise.then((nextRelayStages) => {
    if (!isCurrent() || !Array.isArray(nextRelayStages) || !nextRelayStages.length) return;
    relayStages = nextRelayStages;
  });

  void (async () => {
    relayStages = await followRelayStagesPromise.catch(() => relayStages);
    if (!isCurrent() || hasAuthoritativeProfileEvent(criticalProfile)) return;
    for (const stageRelays of relayStages.slice(1)) {
      applyProfileRelays(root, state, stageRelays, {
        display: shouldDisplayProfileRelays(state, stageRelays),
      });
      const hintedProfile = await fetchProfile(pubkey, {
        relays: stageRelays,
        forceRefresh: true,
        includeViewerRelays: false,
      }).catch(() => null);
      if (!isCurrent()) return;
      if (!hintedProfile?.pubkey) continue;
      criticalProfile = hintedProfile;
      promoteProfile(root, state, hintedProfile, previewProfile);
      if (hasAuthoritativeProfileEvent(hintedProfile)) break;
    }
  })();

  void hintedPostsPromise.then(async ({ posts: freshPosts, relaysUsed }) => {
    if (hydrationGeneration !== profileHydrationGeneration || relayNativeProfileState !== state) return;
    if (!Array.isArray(freshPosts) || !freshPosts.length) return;
    if (Array.isArray(relaysUsed) && relaysUsed.length) {
      applyProfileRelays(root, state, relaysUsed, {
        display: shouldDisplayProfileRelays(state, relaysUsed),
      });
    }
    const merged = mergeEventsNewestFirst(freshPosts, state.timeline);
    if (merged.length === state.timeline.length && merged.every((event, index) => event.id === state.timeline[index]?.id)) return;
    const rendered = await applyPosts(merged);
    if (rendered) void refreshPostContext(merged);
  }).catch(() => {});

  scheduleProfileMetadataRefresh(root, state, {
    hydrationGeneration,
    previewProfile,
    relayStages,
  });

  ensureProfileHeroPainted(root, state.profile);
}

export async function appendClientProfilePage(root = document) {
  const state = currentProfileState(root);
  const button = profileLoadMoreButton(root);
  if (!state || !button) {
    return { appended: 0, hasMore: false, cursorAdvanced: false };
  }
  const until = Number.parseInt(button.dataset.cursor || "0", 10);
  const before = button.dataset.cursor || "";
  const beforeID = button.dataset.cursorId || "";
  const page = await fetchNotesByAuthors([state.pubkey], {
    limit: PROFILE_PAGE_SIZE,
    until: until > 0 ? until : undefined,
    relays: state.relays,
    kinds: PROFILE_TIMELINE_KINDS,
  });
  if (!profileStateIsCurrent(state, root) || profileLoadMoreButton(root) !== button) {
    return { appended: 0, hasMore: false, cursorAdvanced: false };
  }
  if (!page.length) {
    button.dataset.hasMore = "0";
    button.hidden = true;
    return { appended: 0, hasMore: false, cursorAdvanced: false };
  }
  const previousCount = state.posts.length;
  state.timeline = mergeEventsNewestFirst(state.timeline, page);
  const split = splitProfileTimeline(state.timeline);
  state.posts = split.posts;
  state.replies = split.replies;
  state.media = split.media;
  const pagePubkeys = [...new Set(page.map((event) => normalizePubkey(event.pubkey)).filter(Boolean))];
  const [profiles, referencedByID] = await Promise.all([
    fetchProfiles(pagePubkeys, { relays: state.relays }).catch(() => ({})),
    hydrateReferencedEvents(page).catch(() => new Map()),
  ]);
  if (!profileStateIsCurrent(state, root) || profileLoadMoreButton(root) !== button) {
    return { appended: 0, hasMore: false, cursorAdvanced: false };
  }
  state.profiles = { ...state.profiles, ...profiles };
  state.referencedByID = new Map([...(state.referencedByID?.entries?.() || []), ...referencedByID.entries()]);
  await renderProfileNotesPanels(root, state);
  const cursorAdvanced = button.dataset.cursor !== before || button.dataset.cursorId !== beforeID;
  return {
    appended: state.posts.length - previousCount,
    hasMore: button.dataset.hasMore === "1",
    cursorAdvanced,
  };
}

export async function fetchClientProfileNewer(root = document) {
  const state = currentProfileState(root);
  if (!state) return [];
  const top = profileTopCursor(root);
  const since = Number.parseInt(top.cursor || "0", 10);
  const events = await fetchNotesByAuthors([state.pubkey], {
    limit: PROFILE_PAGE_SIZE,
    since: since > 0 ? since : undefined,
    relays: state.relays,
    kinds: PROFILE_TIMELINE_KINDS,
  });
  return events.filter((event) => isNewerThanFeedCursor(event, top.cursor, top.cursorID));
}

export async function prependClientProfileNewer(root = document) {
  const state = currentProfileState(root);
  if (!state) return { count: 0 };
  const events = await fetchClientProfileNewer(root);
  if (!profileStateIsCurrent(state, root)) return { count: 0 };
  if (!events.length) return { count: 0 };
  state.timeline = mergeEventsNewestFirst(events, state.timeline);
  const split = splitProfileTimeline(state.timeline);
  state.posts = split.posts;
  state.replies = split.replies;
  state.media = split.media;
  const eventPubkeys = [...new Set(events.map((event) => normalizePubkey(event.pubkey)).filter(Boolean))];
  const [profiles, referencedByID] = await Promise.all([
    fetchProfiles(eventPubkeys, { relays: state.relays }).catch(() => ({})),
    hydrateReferencedEvents(events).catch(() => new Map()),
  ]);
  if (!profileStateIsCurrent(state, root)) return { count: 0 };
  state.profiles = { ...state.profiles, ...profiles };
  state.referencedByID = new Map([...(state.referencedByID?.entries?.() || []), ...referencedByID.entries()]);
  await renderProfileNotesPanels(root, state);
  return { count: events.length };
}

function serverThreadHydrateMatchesRoute(root, pathNoteID) {
  if (threadFocusNeedsFullHydrate(root)) return false;
  if (root.querySelector(".feed-column[data-relay-native-thread='1']")) return false;
  if (root.querySelector("#thread-focus[data-thread-preview-pending='1']")) return false;
  const column = activeThreadColumn(root);
  if (column?.dataset?.threadRoutePending) return false;
  const summaryReady = Boolean(column?.dataset?.threadRootId);
  if (!summaryReady) return false;
  const selectedID = canonicalHex64(column?.dataset?.threadSelectedId || "");
  const pathID = canonicalHex64(pathNoteID);
  if (selectedID && pathID) return selectedID === pathID;
  const html = root.querySelector("#thread-focus")?.outerHTML || "";
  return isThreadHydrateComplete(html, pathNoteID);
}

function serverThreadAlreadyRendered(root) {
  if (threadFocusNeedsFullHydrate(root)) return false;
  if (root.querySelector(".feed-column[data-relay-native-thread='1']")) return false;
  return Boolean(
    root.querySelector("#thread-focus .note, #thread-focus .comment, #thread-replies .comment"),
  );
}

export async function hydrateReadsRoute(root = document) {
  const list = root.querySelector("#reads-list[data-reads]") || root.querySelector("[data-reads]");
  if (!list) return;
  const viewer = normalizedPubkey();
  const sort = getReadsSortPref() || "recent";
  const cards = await fetchReadsPage({ sort, viewerPubkey: viewer, limit: 50 });
  const pubkeys = [...new Set(cards.map((card) => normalizePubkey(card.event?.pubkey)).filter(Boolean))];
  const fallbackProfiles = Object.fromEntries(pubkeys.map((pk) => [pk, { pubkey: pk }]));
  list.replaceChildren(renderReadsList(cards, fallbackProfiles));
  refreshAscii(list);
  void refreshVisibleFeedNoteMetadata(root, window.location.href, { feedSelector: "[data-reads]" });
  void refreshVisibleNoteProfiles(root);
  void fetchProfiles(pubkeys)
    .then((profiles) => {
      if (!list.isConnected || window.location.pathname !== "/reads") return;
      list.replaceChildren(renderReadsList(cards, { ...fallbackProfiles, ...profiles }));
      refreshAscii(list);
    })
    .catch(() => {});
  const { hydrateTrendingSidebar } = await import("./trending-render.js");
  const { trendingSortFromTimeframe, trendingTimeframeFromSort } = await import("./trending-service.js");
  const { getReadsTrendingTimeframePref } = await import("./sort-prefs.js");
  const tfSort = trendingSortFromTimeframe(getReadsTrendingTimeframePref() || trendingTimeframeFromSort(sort));
  void hydrateTrendingSidebar(root, { sort: tfSort, kindFilter: KIND_LONG_FORM });
}

export async function appendClientReadsPage(root = document) {
  const list = root.querySelector("#reads-list[data-reads]") || root.querySelector("[data-reads]");
  const button = root.querySelector('[data-load-more][data-feed-url="/reads"]');
  if (!(list instanceof HTMLElement) || !(button instanceof HTMLButtonElement)) {
    return { appended: 0, hasMore: false, cursorAdvanced: false };
  }

  const sort = getReadsSortPref() || "recent";
  const viewer = normalizedPubkey();
  const until = Number.parseInt(button.dataset.cursor || "0", 10);
  const untilID = button.dataset.cursorId || "";
  const previousCursor = button.dataset.cursor || "";
  const previousCursorID = button.dataset.cursorId || "";
  const cards = await fetchReadsPage({
    sort,
    viewerPubkey: viewer,
    limit: 50,
    until: until > 0 ? until : undefined,
    untilID: untilID || undefined,
  });
  if (!list.isConnected || !button.isConnected || window.location.pathname !== "/reads") {
    return { appended: 0, hasMore: false, cursorAdvanced: false };
  }
  if (!cards.length) {
    button.dataset.hasMore = "0";
    button.hidden = true;
    return { appended: 0, hasMore: false, cursorAdvanced: false };
  }

  const pubkeys = [...new Set(cards.map((card) => normalizePubkey(card.event?.pubkey)).filter(Boolean))];
  const fallbackProfiles = Object.fromEntries(pubkeys.map((pk) => [pk, { pubkey: pk }]));
  const profiles = await fetchProfiles(pubkeys).catch(() => ({}));
  if (!list.isConnected || !button.isConnected || window.location.pathname !== "/reads") {
    return { appended: 0, hasMore: false, cursorAdvanced: false };
  }

  const rendered = renderReadsList(cards, { ...fallbackProfiles, ...profiles });
  let appended = 0;
  [...rendered.childNodes].forEach((node) => {
    if (!(node instanceof HTMLElement) || !node.classList.contains("read-article")) return;
    if (node.id && document.getElementById(node.id)) return;
    list.append(node);
    appended += 1;
  });
  if (appended > 0) {
    refreshAscii(list);
    initViewMore(list);
    void syncBookmarkState(document);
  }

  const lastCard = cards[cards.length - 1];
  const nextCursor = Number(lastCard?.publishedAt || lastCard?.event?.created_at || 0);
  button.dataset.cursor = nextCursor > 0 ? String(nextCursor) : "";
  button.dataset.cursorId = String(lastCard?.event?.id || "").toLowerCase();
  const hasMore = cards.length >= 50;
  button.dataset.hasMore = hasMore ? "1" : "0";
  button.hidden = !hasMore;
  return {
    appended,
    hasMore,
    cursorAdvanced: button.dataset.cursor !== previousCursor || button.dataset.cursorId !== previousCursorID,
  };
}

export async function hydrateReadRoute(root = document) {
  const match = window.location.pathname.match(/^\/reads\/([^/?#]+)$/);
  const readID = String(match?.[1] || "").trim().toLowerCase();
  if (!readID) throw new Error("invalid read route");
  const card = await fetchReadDetail(readID);
  if (!card) throw new Error("read not found");
  const pk = normalizePubkey(card.event?.pubkey);
  const profile = pk ? await fetchProfile(pk).catch(() => ({})) : {};
  const view = renderReadDetailView(card, profile, []);
  const shell = root.querySelector(".app-shell");
  if (!shell) return;
  const currentFeedColumn = shell.querySelector(".feed-column");
  const currentRightRail = shell.querySelector(".right-rail");
  if (currentFeedColumn) currentFeedColumn.outerHTML = view.mainContent.trim();
  if (currentRightRail) currentRightRail.outerHTML = view.rightRail.trim();
}

export async function hydrateSearchRoute(root = document) {
  const url = new URL(window.location.href);
  const query = url.searchParams.get("q") || "";
  const mode = searchModeFromURL(url);
  const scope = searchScopeFromURL(url);
  const target = searchResultsFeed(root) || searchUsersResults(root) || feedRoot(root);
  if (!target) return;
  renderSearchHeading(root, query, url);
  if (!query.trim()) {
    target.replaceChildren();
    const empty = document.createElement("p");
    empty.className = "muted";
    empty.textContent = mode === "users" ? "Enter a search query for users." : "Enter a search query.";
    target.append(empty);
    configureSearchLoadMore(root, [], { query, scope, mode });
    return;
  }
  if (mode === "users") {
    const { loadSearch } = await import("./services/search-route-loader.js");
    const viewModel = await loadSearch(url);
    renderSearchUserResults(target, viewModel?.pubkeys || [], viewModel?.profiles || {});
    configureSearchLoadMore(root, [], { query, scope, mode });
    wireAvatarImageFallbacks(target);
    return;
  }
  const viewer = normalizedPubkey();
  const { searchNotes } = await import("./feed-service.js");
  const notes = await searchNotes(query, { viewerPubkey: viewer, limit: 50, scope });
  const pubkeys = [...new Set(notes.map((event) => normalizePubkey(event.pubkey)).filter(Boolean))];
  const [profiles, referencedByID] = await Promise.all([
    fetchProfiles(pubkeys),
    hydrateReferencedEvents(notes),
  ]);
  renderNoteFeed(target, notes, profiles, { emptyText: "No results.", referencedByID });
  configureSearchLoadMore(root, notes, { query, scope, mode });
  refreshAscii(target);
  void refreshVisibleFeedNoteMetadata(root, window.location.href, { feedSelector: "[data-search-results], [data-feed]" });
  void refreshVisibleNoteProfiles(root);
}

function searchResultsFeed(root = document) {
  return root.querySelector("[data-search-results]") || root.querySelector("[data-feed][data-search-results]");
}

function searchUsersResults(root = document) {
  return root.querySelector("[data-search-users]");
}

function searchLoadMoreButton(root = document) {
  return root.querySelector('[data-load-more][data-feed-url="/search"]');
}

export function searchModeFromURL(urlLike) {
  const url = new URL(urlLike, window.location.origin);
  return String(url.searchParams.get("mode") || "").trim().toLowerCase() === "users" ? "users" : "notes";
}

function searchScopeFromURL(urlLike) {
  const url = new URL(urlLike, window.location.origin);
  const scope = String(url.searchParams.get("scope") || "").trim().toLowerCase();
  return scope === "all" ? "all" : "network";
}

function searchScopeLabel(scope) {
  return scope === "all" ? "all notes" : "your network";
}

export function searchModeURLs(query, urlLike) {
  const trimmedQuery = String(query || "").trim();
  const scope = searchScopeFromURL(urlLike);
  const build = (mode) => {
    const next = new URL("/search", window.location.origin);
    if (trimmedQuery) next.searchParams.set("q", trimmedQuery);
    next.searchParams.set("mode", mode === "users" ? "users" : "notes");
    if (mode !== "users" && scope === "all") {
      next.searchParams.set("scope", "all");
    }
    return next.pathname + next.search;
  };
  return {
    mode: searchModeFromURL(urlLike),
    notesURL: build("notes"),
    usersURL: build("users"),
  };
}

function searchScopeURLs(query, urlLike) {
  const trimmedQuery = String(query || "").trim();
  const mode = searchModeFromURL(urlLike);
  const build = (scope) => {
    const next = new URL("/search", window.location.origin);
    if (trimmedQuery) next.searchParams.set("q", trimmedQuery);
    next.searchParams.set("mode", mode);
    next.searchParams.set("scope", scope === "all" ? "all" : "network");
    return next.pathname + next.search;
  };
  return {
    scope: searchScopeFromURL(urlLike),
    allURL: build("all"),
    networkURL: build("network"),
  };
}

export function renderSearchHeading(root, query, urlLike) {
  const heading = root.querySelector("[data-search-heading]") || root.querySelector(".search-heading");
  if (!heading) return;
  const trimmedQuery = String(query || "").trim();
  const mode = searchModeFromURL(urlLike);
  const { notesURL, usersURL } = searchModeURLs(trimmedQuery, urlLike);
  const { scope, allURL, networkURL } = searchScopeURLs(trimmedQuery, urlLike);
  const showScopeToggle = getWebOfTrustEnabledPref();
  const modeToggle = `
    <form action="/search" method="get" class="rail-search" role="search" aria-label="Search">
      <p class="search-mode-toggle" aria-label="Search mode">
        ${mode === "notes"
    ? '<strong class="search-mode-option is-active">Search notes</strong>'
    : `<a class="search-mode-option" href="${notesURL}" data-relay-aware>Search notes</a>`}
        <span class="search-mode-sep" aria-hidden="true">·</span>
        ${mode === "users"
    ? '<strong class="search-mode-option is-active">User search</strong>'
    : `<a class="search-mode-option" href="${usersURL}" data-relay-aware>User search</a>`}
      </p>
      <input type="hidden" name="mode" value="${mode}">
      <input type="search" name="q" placeholder="${mode === "users" ? "Search users" : "Search notes"}" value="${escapeHTML(trimmedQuery)}" aria-label="${mode === "users" ? "Search cached profiles" : "Search cached notes"}">
    </form>
  `;
  const scopeLinks = showScopeToggle
    ? scope === "network"
      ? `<p class="muted search-scope-links">Scope: <strong>current network</strong> <span aria-hidden="true">·</span> <a href="${allURL}" data-relay-aware>expand to all notes</a></p>`
      : `<p class="muted search-scope-links">Scope: <a href="${networkURL}" data-relay-aware>current network</a> <span aria-hidden="true">·</span> <strong>all notes</strong></p>`
    : "";
  if (mode === "users") {
    heading.innerHTML = trimmedQuery
      ? `
        <h1>Search</h1>
        ${modeToggle}
        <p class="muted">Search cached profiles by display name, npub, hex pubkey, or nip05.</p>
        <p class="muted">Showing user matches for <strong>"${escapeHTML(trimmedQuery)}"</strong>.</p>
      `
      : `
        <h1>Search</h1>
        ${modeToggle}
        <p class="muted">Search cached profiles by display name, npub, hex pubkey, or nip05.</p>
      `;
    return;
  }
  heading.innerHTML = trimmedQuery
    ? `
      <h1>Search</h1>
      ${modeToggle}
      ${scopeLinks}
      <p class="muted">Showing matches for <strong>"${escapeHTML(trimmedQuery)}"</strong> in ${searchScopeLabel(scope)}.</p>
    `
    : `
      <h1>Search</h1>
      ${modeToggle}
      <p class="muted">Enter terms in the search box to find notes from your relays and cache.</p>
    `;
}

function configureSearchLoadMore(root, notes, { query = "", scope = "network", mode = "notes" } = {}) {
  const button = searchLoadMoreButton(root);
  if (!button) return;
  if (mode === "users") {
    button.dataset.hasMore = "0";
    button.hidden = true;
    button.disabled = false;
    return;
  }
  const cursor = feedPageCursor(notes);
  button.dataset.cursor = cursor.until > 0 ? String(cursor.until) : "";
  button.dataset.cursorId = cursor.cursorId || "";
  button.dataset.searchQuery = String(query || "").trim();
  button.dataset.searchScope = searchScopeFromURL(`/search?scope=${encodeURIComponent(scope)}`);
  const hasMore = notes.length >= 50;
  button.dataset.hasMore = hasMore ? "1" : "0";
  button.hidden = !hasMore;
  button.disabled = false;
}

export async function appendClientSearchPage(root = document) {
  const feed = searchResultsFeed(root);
  const button = searchLoadMoreButton(root);
  if (!(feed instanceof HTMLElement) || !(button instanceof HTMLButtonElement)) {
    return { appended: 0, hasMore: false, cursorAdvanced: false };
  }
  const query = button.dataset.searchQuery || new URL(window.location.href).searchParams.get("q") || "";
  const mode = searchModeFromURL(window.location.href);
  if (mode === "users") {
    button.dataset.hasMore = "0";
    button.hidden = true;
    return { appended: 0, hasMore: false, cursorAdvanced: false };
  }
  const scope = button.dataset.searchScope || searchScopeFromURL(window.location.href);
  const viewer = normalizedPubkey();
  const until = Number.parseInt(button.dataset.cursor || "0", 10);
  const untilID = button.dataset.cursorId || "";
  const previousCursor = button.dataset.cursor || "";
  const previousCursorID = button.dataset.cursorId || "";
  const { searchNotes } = await import("./feed-service.js");
  const notes = await searchNotes(query, {
    viewerPubkey: viewer,
    limit: 50,
    scope,
    until: until > 0 ? until : undefined,
    untilID: untilID || undefined,
  });
  if (!feed.isConnected || !button.isConnected || window.location.pathname !== "/search") {
    return { appended: 0, hasMore: false, cursorAdvanced: false };
  }
  if (!notes.length) {
    button.dataset.hasMore = "0";
    button.hidden = true;
    return { appended: 0, hasMore: false, cursorAdvanced: false };
  }

  const pubkeys = [...new Set(notes.map((event) => normalizePubkey(event.pubkey)).filter(Boolean))];
  const [profiles, referencedByID] = await Promise.all([
    fetchProfiles(pubkeys),
    hydrateReferencedEvents(notes).catch(() => new Map()),
  ]);
  if (!feed.isConnected || !button.isConnected || window.location.pathname !== "/search") {
    return { appended: 0, hasMore: false, cursorAdvanced: false };
  }
  const appended = appendNoteFeed(feed, notes, profiles, { referencedByID });
  configureSearchLoadMore(root, notes, { query, scope });
  if (appended > 0) {
    refreshAscii(feed);
    void refreshVisibleFeedNoteMetadata(root, window.location.href, { feedSelector: "[data-search-results], [data-feed]" });
    void refreshVisibleNoteProfiles(root);
  }
  return {
    appended,
    hasMore: button.dataset.hasMore === "1",
    cursorAdvanced: button.dataset.cursor !== previousCursor || button.dataset.cursorId !== previousCursorID,
  };
}

function tagResultsFeed(root = document) {
  return root.querySelector("[data-tag-results]") || root.querySelector("[data-feed][data-tag-results]");
}

function tagLoadMoreButton(root = document) {
  const feed = tagResultsFeed(root);
  if (!feed) return null;
  const tagPath = parseTagFromPath(window.location.pathname);
  if (!tagPath) return null;
  return root.querySelector(`[data-load-more][data-feed-url="/tag/${encodeURIComponent(tagPath)}"]`)
    || root.querySelector('[data-load-more][data-feed-url^="/tag/"]');
}

export function isRelayNativeTag(root = document) {
  const feed = tagResultsFeed(root);
  return feed?.dataset.relayNativeTag === "1";
}

function renderTagHeading(root, tag, urlLike) {
  const heading = root.querySelector("[data-tag-heading]");
  if (!heading || !tag) return;
  const { scope, allURL, networkURL } = tagScopeToggleURLs(tag, urlLike);
  const showScopeToggle = getWebOfTrustEnabledPref();
  const scopeLinks = showScopeToggle
    ? scope === "network"
      ? `<p class="muted search-scope-links">Scope: <strong>current network</strong> <span aria-hidden="true">·</span> <a href="${allURL}" data-relay-aware>expand to all notes</a></p>`
      : `<p class="muted search-scope-links">Scope: <a href="${networkURL}" data-relay-aware>current network</a> <span aria-hidden="true">·</span> <strong>all notes</strong></p>`
    : "";
  heading.innerHTML = `
    <h1>#${tag}</h1>
    ${scopeLinks}
    <p class="muted">Notes with NIP-12 <code>t</code> tags or <code>#hashtags</code> in the body, fetched from your relays.</p>
    <p class="muted">Showing notes tagged <strong>#${tag}</strong> in ${tagScopeLabel(scope)}.</p>
  `;
}

function configureTagLoadMore(root, notes, tag) {
  const button = tagLoadMoreButton(root);
  if (!button) return;
  const cursor = feedPageCursor(notes);
  button.dataset.cursor = cursor.until > 0 ? String(cursor.until) : "";
  button.dataset.cursorId = cursor.cursorId || "";
  button.dataset.tagScope = tagScopeFromURL(window.location.href);
  const hasMore = notes.length >= 50;
  button.dataset.hasMore = hasMore ? "1" : "0";
  button.hidden = !hasMore;
  button.disabled = false;
}

export async function hydrateTagRoute(root = document, options = {}) {
  const tag = parseTagFromPath(window.location.pathname);
  const feed = tagResultsFeed(root);
  if (!feed) return;

  renderTagHeading(root, tag, window.location.href);

  if (!tag) {
    feed.replaceChildren();
    const empty = document.createElement("p");
    empty.className = "muted";
    empty.textContent = "Invalid hashtag.";
    feed.append(empty);
    return;
  }

  const viewer = normalizedPubkey();
  const scope = tagScopeFromURL(window.location.href);
  const notes = await fetchHashtagPage({
    tag,
    viewerPubkey: viewer,
    scope,
    limit: 50,
    forceFetch: Boolean(options.forceRefresh),
  });

  const pubkeys = [...new Set(notes.map((event) => normalizePubkey(event.pubkey)).filter(Boolean))];
  const [profiles, referencedByID] = await Promise.all([
    fetchProfiles(pubkeys),
    hydrateReferencedEvents(notes).catch(() => new Map()),
  ]);

  feed.dataset.relayNativeTag = "1";
  feed.dataset.relayNativeTagName = tag;
  renderNoteFeed(feed, notes, profiles, {
    emptyText: `No notes tagged #${tag} in ${tagScopeLabel(scope)}.`,
    referencedByID,
  });
  configureTagLoadMore(root, notes, tag);
  refreshAscii(feed);
  void refreshVisibleFeedNoteMetadata(root, window.location.href, { feedSelector: "[data-tag-results], [data-feed]" });
  void refreshVisibleNoteProfiles(root);
  initViewMore(feed);
  void syncBookmarkState(document);
  wireAvatarImageFallbacks(feed);
}

export async function appendClientTagPage(root = document) {
  const feed = tagResultsFeed(root);
  const button = tagLoadMoreButton(root);
  if (!feed || !button || !isRelayNativeTag(root)) {
    return { appended: 0, hasMore: false, cursorAdvanced: false };
  }
  const tag = feed.dataset.relayNativeTagName || parseTagFromPath(window.location.pathname);
  if (!tag) return { appended: 0, hasMore: false, cursorAdvanced: false };

  const viewer = normalizedPubkey();
  const scope = button.dataset.tagScope || tagScopeFromURL(window.location.href);
  const until = Number.parseInt(button.dataset.cursor || "0", 10);
  const untilID = button.dataset.cursorId || "";
  const notes = await fetchHashtagPage({
    tag,
    viewerPubkey: viewer,
    scope,
    limit: 50,
    until: until > 0 ? until : undefined,
    untilID: untilID || undefined,
  });
  if (!notes.length) {
    button.dataset.hasMore = "0";
    button.hidden = true;
    return { appended: 0, hasMore: false, cursorAdvanced: false };
  }

  const pubkeys = [...new Set(notes.map((event) => normalizePubkey(event.pubkey)).filter(Boolean))];
  const [profiles, referencedByID] = await Promise.all([
    fetchProfiles(pubkeys),
    hydrateReferencedEvents(notes).catch(() => new Map()),
  ]);
  const appended = appendNoteFeed(feed, notes, profiles, { referencedByID });
  const cursor = feedPageCursor(notes);
  const previousCursor = button.dataset.cursor || "";
  const previousCursorID = button.dataset.cursorId || "";
  button.dataset.cursor = cursor.until > 0 ? String(cursor.until) : "";
  button.dataset.cursorId = cursor.cursorId || "";
  const hasMore = notes.length >= 50;
  button.dataset.hasMore = hasMore ? "1" : "0";
  button.hidden = !hasMore;
  if (appended > 0) {
    refreshAscii(feed);
    void refreshVisibleFeedNoteMetadata(root, window.location.href, { feedSelector: "[data-tag-results], [data-feed]" });
  }
  return {
    appended,
    hasMore,
    cursorAdvanced: button.dataset.cursor !== previousCursor || button.dataset.cursorId !== previousCursorID,
  };
}

export async function hydrateThreadRoute(root = document, options = {}) {
  const pathNoteID = threadPathNoteID(window.location.href);
  if (!pathNoteID) return;
  const selectedID = canonicalHex64(pathNoteID);
  const threadRouteStillCurrent = () => {
    if (!root?.isConnected) return false;
    if (routeKind(globalThis.location?.pathname || "") !== "thread") return false;
    return canonicalHex64(threadPathNoteID(globalThis.location?.href || "")) === selectedID;
  };
  const preferredRelays = Array.isArray(options.preferredRelays)
    ? options.preferredRelays
    : [];

  if (serverThreadHydrateMatchesRoute(root, pathNoteID)) {
    initThreadPage();
    return;
  }

  // Paint a store-backed server preview immediately while relay-native reads
  // continue. Starting the live selected-note fetch before IndexedDB preview
  // lookup avoids serial cache/relay latency on a first-ever thread visit.
  await renderServerThreadHydrateFallback(root, pathNoteID, threadRouteStillCurrent).catch(() => false);
  if (!threadRouteStillCurrent()) return;
  const livePreviewPromise = renderLiveThreadRoutePreview(root, pathNoteID, {
    preferredRelays,
    canRender: threadRouteStillCurrent,
  }).catch(() => null);

  const preview = options.previewAlreadyRendered
    ? { rendered: !threadFocusNeedsFullHydrate(root) }
    : await renderCachedThreadRoutePreview(root, pathNoteID, {
      preferredRelays,
      canRender: threadRouteStillCurrent,
    });
  if (!threadRouteStillCurrent()) return;
  let effectivePreview = preview;
  if (threadFocusNeedsFullHydrate(root)) {
    const upgraded = await renderCachedThreadRoutePreview(root, pathNoteID, {
      preferredRelays,
      canRender: threadRouteStillCurrent,
    }).catch(() => null);
    if (!threadRouteStillCurrent()) return;
    if (upgraded?.rendered) effectivePreview = upgraded;
  }
  let bundle = await resolveThreadFromPath(pathNoteID, { preferredRelays });
  if (!threadRouteStillCurrent()) return;
  await livePreviewPromise;
  if (!threadRouteStillCurrent()) return;
  if (!bundle?.root) {
    bundle = await resolveThreadBundleAfterMiss(pathNoteID, preferredRelays, threadRouteStillCurrent);
    if (!threadRouteStillCurrent()) return;
  }
  if (!bundle?.root) {
    if (await renderServerThreadHydrateFallback(root, pathNoteID, threadRouteStillCurrent)) return;
    relayNativeThreadState = null;
    const previewRendered = Boolean(effectivePreview?.rendered);
    const previewComplete = Boolean(effectivePreview?.rendered && !threadFocusNeedsFullHydrate(root));
    const action = relayNativeThreadMissingBundleAction({
      previewRendered,
      previewComplete,
      serverRendered: serverThreadAlreadyRendered(root),
    });
    if (action === "keep-rendered") {
      initThreadPage();
      return;
    }
    const focus = root.querySelector("#thread-focus");
    if (focus) {
      focus.replaceChildren();
      const empty = document.createElement("p");
      empty.className = "muted";
      empty.textContent = "Thread not found on relays.";
      focus.append(empty);
    }
    return;
  }
  await ensureFocusedReplyParent(bundle);
  if (!threadRouteStillCurrent()) return;
  if (bundle.selectedParentUnavailable === true) {
    const warmed = await warmThreadFromPath(pathNoteID, { preferredRelays }).catch(() => null);
    if (!threadRouteStillCurrent()) return;
    if (warmed?.root) {
      await ensureFocusedReplyParent(warmed);
      if (!threadRouteStillCurrent()) return;
      if (warmed.selectedParentUnavailable !== true || (warmed.events?.length || 0) > (bundle.events?.length || 0)) {
        bundle = warmed;
      }
    }
  }

  const viewer = normalizedPubkey();
  const pubkeys = threadParticipantPubkeys(bundle.events);
  const cachedProfiles = await cachedProfilesByPubkey(pubkeys);
  relayNativeThreadState = {
    fullBundle: bundle,
    profiles: cachedProfiles,
    viewerPubkey: viewer,
    referencedByID: new Map(),
  };
  if (!threadRouteStillCurrent()) return;
  await renderRelayNativeThread(root, relayNativeThreadState);
  if (!threadRouteStillCurrent()) return;
  afterRelayNativeThreadRendered(root);
  initThreadPage();

  void Promise.all([
    fetchProfiles(pubkeys),
    hydrateReferencedEvents(bundle.events),
  ])
    .then(async ([profiles, referencedByID]) => {
      if (!threadRouteStillCurrent()) return;
      if (relayNativeThreadState?.fullBundle?.selectedID !== bundle.selectedID) return;
      relayNativeThreadState = {
        fullBundle: bundle,
        profiles: mergeCachedProfilesByPubkey(pubkeys, relayNativeThreadState?.profiles, profiles),
        viewerPubkey: viewer,
        referencedByID,
      };
      if (!threadRouteStillCurrent()) return;
      await renderRelayNativeThread(root, relayNativeThreadState);
      if (!threadRouteStillCurrent()) return;
      afterRelayNativeThreadRendered(root);
      initThreadPage();
    })
    .catch(() => {});
}

/** True when relay-native same-thread focus can update without a network fetch. */
export function relayNativeThreadFocusIsCached(pathNoteID, root = document) {
  const selectedID = canonicalHex64(threadPathNoteID(`/thread/${pathNoteID}`) || pathNoteID);
  if (!selectedID) return false;
  const state = relayNativeThreadState;
  if (!state?.fullBundle?.rootID) return false;
  const currentSelected = canonicalHex64(state.fullBundle.selectedID);
  if (currentSelected === selectedID) {
    const domSelected = canonicalHex64(
      activeThreadColumn(root)?.dataset?.threadSelectedId || "",
    );
    if (domSelected === selectedID) return true;
  }
  return Boolean(state.fullBundle.events.find((event) => canonicalHex64(event.id) === selectedID));
}

/** Same-thread focus change without a full document navigation. */
export async function tryRelayNativeThreadFocusUpdate(pathNoteID, root = document) {
  const selectedID = canonicalHex64(threadPathNoteID(`/thread/${pathNoteID}`) || pathNoteID);
  if (!selectedID) return false;

  const state = relayNativeThreadState;
  if (!state?.fullBundle?.rootID) return false;

  const currentRoot = canonicalHex64(state.fullBundle.rootID);
  const currentSelected = canonicalHex64(state.fullBundle.selectedID);
  if (currentSelected === selectedID) {
    const domSelected = canonicalHex64(
      activeThreadColumn(root)?.dataset?.threadSelectedId || "",
    );
    if (domSelected === selectedID) return true;
  }

  const cachedSelected = state.fullBundle.events.find(
    (event) => canonicalHex64(event.id) === selectedID,
  );
  if (cachedSelected) {
    relayNativeThreadState = {
      ...state,
      fullBundle: {
        ...state.fullBundle,
        selected: cachedSelected,
        selectedID,
        threadViewResolved: undefined,
        linearReplyPage: undefined,
      },
    };
    await renderRelayNativeThread(root, relayNativeThreadState);
    afterRelayNativeThreadRendered(root);
    initThreadPage();
    return true;
  }

  const bundle = await resolveThreadFromPath(selectedID);
  if (!bundle?.root || canonicalHex64(bundle.rootID) !== currentRoot) return false;

  const viewer = normalizedPubkey();
  const pubkeys = threadParticipantPubkeys(bundle.events);
  const [profiles, referencedByID] = await Promise.all([
    fetchProfiles(pubkeys),
    hydrateReferencedEvents(bundle.events),
  ]);
  relayNativeThreadState = {
    fullBundle: bundle,
    profiles: mergeCachedProfilesByPubkey(pubkeys, state.profiles, profiles),
    viewerPubkey: viewer,
    referencedByID,
  };
  await renderRelayNativeThread(root, relayNativeThreadState);
  afterRelayNativeThreadRendered(root);
  initThreadPage();
  return true;
}

export function relayNativeThreadRootID() {
  return relayNativeThreadState?.fullBundle?.rootID || "";
}

export async function hydrateClientRoute(route, root = document, options = {}) {
  const {
    forceRefresh = false,
    preserveExistingNotes = false,
    previewAlreadyRendered = false,
    preferredRelays = [],
  } = options;
  if (route === "feed") {
    return hydrateFeedRoute(root, {
      skipHydrate: false,
      forceRefresh,
      preserveExistingNotes,
    });
  }
  if (route === "thread") return hydrateThreadRoute(root, { previewAlreadyRendered, preferredRelays });
  if (route === "read") return hydrateReadRoute(root);
  if (route === "bookmarks") return hydrateBookmarksRoute(root);
  if (route === "notifications") return hydrateNotificationsRoute(root);
  if (route === "profile") return hydrateProfileRoute(root);
  if (route === "reads") return hydrateReadsRoute(root);
  if (route === "search") return hydrateSearchRoute(root);
  if (route === "tag") return hydrateTagRoute(root, { forceRefresh });
}
