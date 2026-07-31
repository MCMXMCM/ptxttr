import { assetURL } from "./asset-paths.js";
import { shouldShowFirstLoginBootstrap } from "./first-login-bootstrap.js";
import { normalizedPubkey } from "./session.js";
import { getWebOfTrustDepthPref, getWebOfTrustEnabledPref } from "./sort-prefs.js";
import { desktopModeEnabled } from "./viewer-defaults.js";

function navLink(href, icon, label, active, extraAttrs = "") {
  const current = active === href ? ' aria-current="page"' : "";
  return `<a href="${href}" data-relay-aware data-main-menu-link${extraAttrs}${current}><span class="rail-icon" aria-hidden="true">${icon}</span><span class="rail-label">${label}</span></a>`;
}

function mobileNavLink(href, label, extraAttrs = "") {
  return `<a href="${href}" data-relay-aware data-main-menu-link${extraAttrs}>${label}</a>`;
}

function escapeAttr(value) {
  return `${value || ""}`
    .replaceAll("&", "&amp;")
    .replaceAll('"', "&quot;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

function railSearch(placeholder, value = "", mode = "notes") {
  const safeValue = escapeAttr(value);
  const normalizedMode = mode === "users" ? "users" : "notes";
  const notesURL = `/search?${safeValue ? `q=${encodeURIComponent(value)}&` : ""}mode=notes`;
  const usersURL = `/search?${safeValue ? `q=${encodeURIComponent(value)}&` : ""}mode=users`;
  return `
    <form action="/search" method="get" class="rail-search">
      <p class="search-mode-toggle" aria-label="Search mode">
        ${normalizedMode === "notes"
    ? '<strong class="search-mode-option is-active">Search notes</strong>'
    : `<a class="search-mode-option" href="${notesURL}" data-relay-aware>Search notes</a>`}
        <span class="search-mode-sep" aria-hidden="true">·</span>
        ${normalizedMode === "users"
    ? '<strong class="search-mode-option is-active">User search</strong>'
    : `<a class="search-mode-option" href="${usersURL}" data-relay-aware>User search</a>`}
      </p>
      <input type="hidden" name="mode" value="${normalizedMode}">
      <input type="search" name="q" placeholder="${escapeAttr(placeholder)}" value="${safeValue}">
    </form>
  `;
}

export const FEED_LOADER_STATUSES = [
  "reading local notes...",
  "building trust graph...",
  "checking relays...",
  "assembling timeline...",
  "merging relay updates...",
  "preparing feed...",
];

const FIRST_LOGIN_FEED_LOADER_STATUSES = [
  "registering this npub with the server...",
  "resolving your follow graph...",
  "expanding Web of Trust to 3 degrees...",
  "discovering relay hints from contacts...",
  "syncing recent notes into the server cache...",
  "warming active threads and reply counts...",
  "saving your reusable feed slice...",
];

export const PROFILE_POSTS_LOADER_STATUSES = [
  "showing cached profile identity...",
  "loading profile details...",
  "reading cached posts...",
  "checking for newer relay notes...",
  "assembling profile timeline...",
];

export const PROFILE_REPLIES_LOADER_STATUSES = [
  "reading cached profile...",
  "loading reply history...",
  "checking relay replies...",
  "assembling reply timeline...",
];

export const PROFILE_MEDIA_LOADER_STATUSES = [
  "reading cached profile...",
  "loading media posts...",
  "checking media relays...",
  "assembling media timeline...",
];

export const PROFILE_FOLLOWING_LOADER_STATUSES = [
  "retrieving current following count...",
  "reading cached contacts...",
  "checking relay follows...",
  "assembling profile cards...",
];

export const PROFILE_FOLLOWERS_LOADER_STATUSES = [
  "retrieving current followed count...",
  "reading cached followers...",
  "checking relay followers...",
  "assembling profile cards...",
];

export const NOTIFICATIONS_LOADER_STATUSES = [
  "reading cached notifications...",
  "checking relay updates...",
  "filtering notification types...",
  "assembling notifications...",
];

export const THREAD_LOADER_STATUSES = [
  "reading cached thread...",
  "hydrating parent notes...",
  "checking relay replies...",
  "assembling thread view...",
];

/** Number of stacked skeleton `<pre>` cards (must match `buildFeedLoaderCardText` card index range). */
export const FEED_LOADER_CARD_COUNT = 2;

function retroLoaderStatusAttr(statuses = []) {
  return escapeAttr((statuses || []).filter(Boolean).join("||"));
}

export function retroLoaderMarkup({
  loaderType = "feed",
  title = "loading feed",
  summary = "",
  statusMessages = FEED_LOADER_STATUSES,
  completionMessage = "ready.",
  progressWidth = 30,
  statusWindow = 4,
  showCards = false,
  showActivity = true,
  quietAfterMs = 0,
  hideProgressWhenQuiet = false,
  cardAttr = "data-feed-loader-card",
  cardClass = "",
  extraClass = "",
  busy = true,
} = {}) {
  const cards = showCards
    ? `<div class="text-skeleton-stack feed-loader-stack" aria-hidden="true">
        ${Array.from({ length: FEED_LOADER_CARD_COUNT }, (_, index) => `<pre class="ascii-card text-skeleton-note feed-loader-card${cardClass ? ` ${cardClass}` : ""}" ${cardAttr}="${index}"></pre>`).join("")}
      </div>`
    : "";
  const summaryMarkup = summary
    ? `<p class="muted retro-loader-summary" data-retro-loader-summary>${summary}</p>`
    : `<p class="muted retro-loader-summary" data-retro-loader-summary hidden></p>`;
  const activityMarkup = showActivity
    ? `<div class="retro-loader-activity-block">
        <pre class="retro-loader-activity" data-retro-loader-activity aria-live="polite"></pre>
      </div>`
    : "";
  const titleMarkup = loaderType === "thread"
    ? ""
    : `<p class="retro-loader-title" data-retro-loader-title>${title}</p>`;
  return `
    <section class="feed-loader retro-loader${extraClass ? ` ${extraClass}` : ""}" data-feed-loader data-retro-loader data-retro-loader-type="${escapeAttr(loaderType)}" data-retro-loader-title="${escapeAttr(title)}" data-retro-loader-statuses="${retroLoaderStatusAttr(statusMessages)}" data-retro-loader-complete="${escapeAttr(completionMessage)}" data-retro-loader-progress-width="${escapeAttr(progressWidth)}" data-retro-loader-status-window="${escapeAttr(statusWindow)}" data-retro-loader-quiet-after-ms="${escapeAttr(quietAfterMs)}"${hideProgressWhenQuiet ? ' data-retro-loader-hide-progress-when-quiet="1"' : ""}${busy ? ' aria-busy="true"' : ""}>
      <div class="retro-loader-block">
        ${titleMarkup}
        ${summaryMarkup}
        <div class="retro-loader-progress-block">
          <pre class="retro-loader-progress" data-retro-loader-progress aria-live="polite"></pre>
        </div>
        ${activityMarkup}
      </div>
      ${cards}
    </section>
  `;
}

/**
 * Same ~ / dash wave as the home feed loader, for profile/thread shells (no status line).
 * Card text is filled client-side at the measured column width (see ascii.js).
 * @param {string} cardAttr Attribute name for frame index (`data-feed-loader-card` on home, `data-skeleton-wave-card` elsewhere).
 */
export function skeletonWaveStackMarkup(cardAttr = "data-skeleton-wave-card") {
  return `<div class="text-skeleton-stack feed-loader-stack" aria-hidden="true">
        ${Array.from({ length: FEED_LOADER_CARD_COUNT }, (_, index) => `<pre class="ascii-card text-skeleton-note feed-loader-card" ${cardAttr}="${index}"></pre>`).join("")}
      </div>`;
}

function threadTreeSkeletonNoteRow(cardIndex) {
  const idx = cardIndex % FEED_LOADER_CARD_COUNT;
  return `<div class="hn-default">
              <p class="hn-comhead"><strong class="text-skeleton">---------</strong> <span class="muted text-skeleton">-- ------</span></p>
              <pre class="ascii-card text-skeleton-note feed-loader-card thread-tree-skeleton-wave" data-skeleton-wave-card="${idx}"></pre>
            </div>`;
}

function threadTreeSkeletonAvatar() {
  return `<a class="hn-tree-avatar hn-tree-avatar--skeleton" aria-hidden="true"><span class="hn-tree-avatar-skel" aria-hidden="true"></span></a>`;
}

function threadTreeSkeletonRootBlock(cardIndex) {
  return `${threadTreeSkeletonAvatar()}<div class="hn-root-stack">${threadTreeSkeletonNoteRow(cardIndex)}</div>`;
}

/**
 * Placeholder tree chrome (no `data-thread-tree-view`) so the tree fragment is refetched once on thread load.
 * Wave `<pre>` cards are filled by `ascii.js` like other skeleton stacks.
 */
export function threadTreeSkeletonMarkup() {
  return `<section class="thread-tree-mode hn-thread-tree-mode thread-tree-skeleton" aria-hidden="true">
    <div class="thread-tree-root-note hn-story thread-tree-skeleton-root">
      ${threadTreeSkeletonRootBlock(0)}
    </div>
    <div class="hn-comment-tree thread-tree thread-tree-skeleton-branch">
      <ul class="hn-tree-ul">
        <li class="hn-comtr thread-tree-item thread-tree-skeleton-item" aria-hidden="true">
          <div class="hn-li-body">${threadTreeSkeletonAvatar()}${threadTreeSkeletonNoteRow(1)}</div>
        </li>
        <li class="hn-comtr thread-tree-item thread-tree-skeleton-item" aria-hidden="true">
          <div class="hn-li-body">${threadTreeSkeletonAvatar()}${threadTreeSkeletonNoteRow(0)}</div>
        </li>
      </ul>
    </div>
  </section>`;
}

function threadReplySkeletonItemMarkup({ depth = 1, isLast = false } = {}) {
  const contentPrefix = isLast ? "     " : "|    ";
  const railSuffix = isLast ? "" : "\n|";
  return `<div class="comment thread-reply-skeleton-item" data-depth="${depth}" style="--depth: ${depth}" aria-hidden="true">
    <span class="comment-avatar thread-parent-skeleton-avatar" aria-hidden="true"></span>
    <pre class="ascii-reply text-skeleton-note">     ░░░░░░░░ -- ░░░░░ -----------------[...]
${contentPrefix}░░░░░░░░░░░░░░░░░░░░░░░░░
${contentPrefix}░░░░░░░░░░░░░░░░░░░░░░░░░
${contentPrefix}[Δ] ░ [∇] -------------------------- [reply] ---+${railSuffix}</pre>
  </div>`;
}

/** Placeholder reply rows for `#thread-replies` (matches thread comment chrome, not feed cards). */
export function threadRepliesSkeletonMarkup({ count = 3 } = {}) {
  return Array.from({ length: count }, (_, index) =>
    threadReplySkeletonItemMarkup({ depth: 1, isLast: index === count - 1 }),
  ).join("");
}

/** `#thread-replies` host; omit inner skeleton unless replies are expected to appear. */
export function threadRepliesHostSkeletonMarkup({ expectReplies = false } = {}) {
  const skeletonClass = expectReplies ? " thread-replies-skeleton" : "";
  const inner = expectReplies ? threadRepliesSkeletonMarkup() : "";
  return `<div class="comments${skeletonClass}" id="thread-replies" data-thread-fragment="replies">${inner}</div>`;
}

/** Appended to `#thread-replies` while paginating thread replies. */
export function threadRepliesPageSkeletonMarkup() {
  return `<div class="thread-replies-page-skeleton" aria-hidden="true">${threadRepliesSkeletonMarkup({ count: 2 })}</div>`;
}

export function threadParentSkeletonMarkup() {
  return `<div class="comment thread-focus-parent thread-focus-parent--skeleton" data-depth="1" style="--depth: 1" aria-hidden="true">
    <span class="comment-avatar thread-parent-skeleton-avatar" aria-hidden="true"></span>
    <pre class="ascii-reply text-skeleton-note">     ░░░░░░░░ -- ░░░░░ -----------------[...]
|    ░░░░░░░░░░░░░░░░░░░░░░░░
|    ░░░░░░░░░░░░░░░░░░░░░░░░
|    ░░░░░░░░░░░░░░░░░░░░░░░░
|    --- ░░░ ------------------------------ ---+</pre>
  </div>`;
}

export function threadFocusSkeletonMarkup() {
  return `<section class="thread-focus thread-focus-skeleton" aria-hidden="true">
    ${threadParentSkeletonMarkup()}
    <article class="note is-focused thread-focus-selected thread-selected-skeleton">
      <span class="note-avatar thread-parent-skeleton-avatar" aria-hidden="true"></span>
      <pre class="ascii-reply text-skeleton-note">     ░░░░░░░░ -- ░░░░░ -----------------[...]+
                              |
░░░░░░░░░░░░░░░░░░░░░░░░       |
░░░░░░░░░░░░░░░░░░░░░░░░       |
--- ░░░ --------------------------------- ---+</pre>
    </article>
  </section>`;
}

export function feedLoaderMarkup(options = {}) {
  const { showActivity = true } = options || {};
  let title = "loading feed";
  let summary = "gathering notes from relays and local cache.";
  let statusMessages = FEED_LOADER_STATUSES;
  if (shouldShowFirstLoginBootstrap(normalizedPubkey())) {
    title = "building your network";
    summary = "First login takes a bit longer because the server is building your Web of Trust, finding relay hints from your contacts, and caching your slice of Nostr. Later visits should be much faster.";
    statusMessages = FIRST_LOGIN_FEED_LOADER_STATUSES;
  }
  return retroLoaderMarkup({
    loaderType: "feed",
    title,
    summary,
    statusMessages,
    completionMessage: "notes incoming...",
    showActivity,
  });
}

export function profileFeedLoaderMarkup(kind = "posts") {
  const normalized = String(kind || "posts").toLowerCase();
  if (normalized === "replies") {
    return retroLoaderMarkup({
      loaderType: "profile-replies",
      title: "loading replies",
      summary: "collecting reply history from cache and relays.",
      statusMessages: PROFILE_REPLIES_LOADER_STATUSES,
      completionMessage: "replies incoming...",
    });
  }
  if (normalized === "media") {
    return retroLoaderMarkup({
      loaderType: "profile-media",
      title: "loading media",
      summary: "collecting media notes from cache and relays.",
      statusMessages: PROFILE_MEDIA_LOADER_STATUSES,
      completionMessage: "media incoming...",
    });
  }
  return retroLoaderMarkup({
    loaderType: "profile-posts",
    title: "loading posts",
    summary: "collecting profile posts from cache and relays.",
    statusMessages: PROFILE_POSTS_LOADER_STATUSES,
    completionMessage: "posts incoming...",
  });
}

export function profileListLoaderMarkup(kind = "following") {
  const isFollowers = String(kind || "").toLowerCase() === "followers";
  return retroLoaderMarkup({
    loaderType: isFollowers ? "profile-followers" : "profile-following",
    title: isFollowers ? "loading followed-by" : "loading following",
    summary: isFollowers
      ? "retrieving current followed count and profile cards from relays."
      : "retrieving current following count and profile cards from relays.",
    statusMessages: isFollowers ? PROFILE_FOLLOWERS_LOADER_STATUSES : PROFILE_FOLLOWING_LOADER_STATUSES,
    completionMessage: isFollowers ? "followers incoming..." : "follows incoming...",
    showCards: false,
    extraClass: " retro-loader--compact",
  });
}

export function notificationsLoaderMarkup() {
  return retroLoaderMarkup({
    loaderType: "notifications",
    title: "loading notifications",
    summary: "collecting mentions and reactions from cache and relays.",
    statusMessages: NOTIFICATIONS_LOADER_STATUSES,
    completionMessage: "notifications incoming...",
  });
}

export function threadRouteLoaderMarkup(options = {}) {
  const { showActivity = true } = options || {};
  return retroLoaderMarkup({
    loaderType: "thread",
    title: "",
    summary: "hydrating the thread from cache and relays.",
    statusMessages: THREAD_LOADER_STATUSES,
    completionMessage: "thread ready.",
    showCards: false,
    showActivity,
    quietAfterMs: 0,
    hideProgressWhenQuiet: false,
    extraClass: " retro-loader--compact thread-telemetry-loader",
  });
}

/** Single-word thread/tree toggle showing the active view (tap switches mode). */
export function threadViewToggleMarkup({ showTree = false } = {}) {
  const mode = showTree ? "tree" : "thread";
  const other = showTree ? "thread" : "tree";
  return `<button type="button" class="link-button thread-view-toggle" data-thread-view-toggle data-thread-view-current="${mode}" aria-label="Viewing ${mode}. Tap to switch to ${other}.">${mode}</button>`;
}

export function threadViewToggleDesktopMarkup({ showTree = false } = {}) {
  return `<p class="thread-view-toggle-desktop" data-thread-view-mode aria-label="Thread view mode">${threadViewToggleMarkup({ showTree })}</p>`;
}

export function threadViewToggleMobileBarMarkup({ showTree = false } = {}) {
  return `<div class="mobile-bar-center" data-thread-view-mode aria-label="Thread view mode">${threadViewToggleMarkup({ showTree })}</div>`;
}

/** @deprecated Use {@link threadViewToggleMarkup}. */
export function threadViewModeToggleMarkup(options = {}) {
  return threadViewToggleDesktopMarkup(options);
}

/** Pending thread routes no longer render a summary sub-header (toggle lives in the app bar). */
export function threadHeaderSkeletonMarkup() {
  return "";
}

export function shellMobileBar() {
  const loggedIn = Boolean(normalizedPubkey());
  const wotEnabled = getWebOfTrustEnabledPref();
  const wotDepth = getWebOfTrustDepthPref();
  const d1 = wotDepth === 1 ? " selected" : "";
  const d2 = wotDepth === 2 ? " selected" : "";
  const d3 = wotDepth === 3 ? " selected" : "";
  return `
    <header class="mobile-bar">
      <a href="/" data-relay-aware class="mobile-brand" data-feed-home><span class="mobile-brand-text">Plain Text Nostr</span></a>
      ${threadViewToggleMobileBarMarkup()}
      <div class="feed-wot-quick mobile-bar-wot" data-feed-wot-controls data-wot-depth="${wotDepth}"${wotEnabled && (loggedIn || desktopModeEnabled()) ? "" : " hidden"}>
        <label class="feed-wot-quick-label" for="feed-wot-depth-header">WOT</label>
        <select id="feed-wot-depth-header" class="feed-wot-depth-select" data-feed-wot-depth-select aria-label="Web of Trust depth">
          <option value="1"${d1}>wot: 1°</option>
          <option value="2"${d2}>wot: 2°</option>
          <option value="3"${d3}>wot: 3°</option>
        </select>
      </div>
      <button type="button" class="mobile-menu-trigger mobile-menu-bar-glyph" data-mobile-menu-trigger aria-label="Open menu">
        <span class="mobile-menu-bar-icon" aria-hidden="true">≡</span>
      </button>
    </header>
  `;
}

export function leftRail(active = "") {
  return `
    <button type="button" class="rail-collapse-toggle" data-sidebar-collapse-toggle aria-label="Collapse sidebar" aria-expanded="true" title="Collapse sidebar">
      <span class="rail-collapse-icon" aria-hidden="true"></span>
    </button>
    <aside class="left-rail">
      <div class="rail-header">
        <a class="rail-brand" href="/" data-relay-aware data-feed-home>Plain Text Nostr</a>
      </div>
      <nav class="rail-nav" aria-label="Primary">
        ${navLink("/", "~", "Home", active, " data-feed-home")}
        ${navLink("/reads", "?", "Reads", active, " data-session-reads-link")}
        ${navLink("/bookmarks", "*", "Bookmarks", active, " data-session-bookmarks-link")}
        ${navLink("/notifications", "!", "Notifications", active, " data-session-notifications-link")}
        ${navLink("/settings", "=", "Settings", active)}
        ${navLink("/about", "i", "About", active)}
      </nav>
      <button type="button" class="rail-post" data-post-trigger>Post</button>
      <div class="rail-user">
        <a href="/login" class="rail-user-profile" data-session-user-link data-relay-aware aria-label="Log in">
          <img src="" alt="" loading="lazy" decoding="async" data-session-avatar hidden>
          <span class="rail-avatar-fallback" data-session-avatar-fallback>@</span>
          <span class="rail-user-copy" data-session-user-copy hidden>
            <strong data-session-display-name>Guest</strong>
          </span>
        </a>
        <a href="/login" class="rail-login" data-session-cta>Login</a>
      </div>
    </aside>
  `;
}

export function mobileMenu(searchQuery = "") {
  return `
    <div class="mobile-menu" data-mobile-menu hidden aria-hidden="true">
      <div class="mobile-menu-backdrop" data-mobile-menu-backdrop></div>
      <div class="mobile-menu-panel" role="dialog" aria-modal="true" aria-label="Menu">
        <div class="mobile-menu-header">
          <div class="mobile-menu-header-logo" aria-hidden="true">
            <span class="about-page-logo">
              <img src="${assetURL("img/ascritch_icon_black.png")}" alt="" width="80" height="80" decoding="async" class="about-logo about-logo-light-scheme">
              <img src="${assetURL("img/ascritch_icon_white.png")}" alt="" width="80" height="80" decoding="async" class="about-logo about-logo-dark-scheme">
            </span>
          </div>
          <section class="mobile-menu-intro" aria-label="Plain Text Nostr summary">
            <p class="mobile-menu-intro-copy muted">A simple and fast Nostr web reader.</p>
          </section>
          <div class="mobile-menu-session">
            <a href="/login" class="mobile-menu-login-cta" data-session-cta>Login</a>
            <div class="mobile-menu-current-user" data-session-user-copy hidden>
              <strong data-session-display-name>Guest</strong>
              <button type="button" class="link-button mobile-menu-logout" data-logout data-session-logout-wrap hidden>Log out</button>
            </div>
          </div>
        </div>
        <div class="mobile-menu-search">
          ${railSearch("Search notes", searchQuery, "notes")}
        </div>
        <nav class="mobile-menu-nav" aria-label="Mobile menu">
          ${mobileNavLink("/", "Home", " data-feed-home")}
          ${mobileNavLink("/reads", "Reads", " data-session-reads-link")}
          ${mobileNavLink("/bookmarks", "Bookmarks", " data-session-bookmarks-link")}
          ${mobileNavLink("/notifications", "Notifications", " data-session-notifications-link")}
          ${mobileNavLink("/settings", "Settings")}
          ${mobileNavLink("/about", "About")}
        </nav>
        <button type="button" class="mobile-menu-close" data-mobile-menu-close>Close</button>
      </div>
    </div>
  `;
}

export function postPlaceholderDialog() {
  return `
    <dialog class="composer-dialog" data-composer-dialog>
      <form method="dialog" class="composer-close-row">
        <button type="submit" class="composer-close-button" data-close-composer aria-label="Close composer">X</button>
      </form>
      <h2 data-composer-title>Write a post</h2>
      <p class="muted" data-composer-status>Sign in with a signing-capable method to publish.</p>
      <form class="composer-form" data-composer-form>
        <input type="hidden" name="mode" value="post" data-composer-mode>
        <input type="hidden" name="root_id" data-composer-root-id>
        <input type="hidden" name="reply_id" data-composer-reply-id>
        <input type="hidden" name="reply_pubkey" data-composer-reply-pubkey>
        <input type="hidden" name="repost_id" data-composer-repost-id>
        <input type="hidden" name="repost_pubkey" data-composer-repost-pubkey>
        <input type="hidden" name="repost_relay" data-composer-repost-relay>
        <label class="composer-label" for="composer-content">Content</label>
        <div class="composer-input-wrap" data-composer-input-wrap>
          <pre class="composer-overlay" data-composer-overlay aria-hidden="true"></pre>
          <textarea id="composer-content" name="content" rows="6" maxlength="64000" data-composer-content></textarea>
          <div class="composer-mention-menu" data-composer-mentions hidden>
            <ul class="composer-mention-list" data-composer-mention-list role="listbox" aria-label="Mention suggestions"></ul>
          </div>
        </div>
        <section class="composer-repost-preview" data-composer-preview hidden>
          <p class="muted">Reposting</p>
          <pre class="composer-repost-preview-content" data-composer-preview-content></pre>
        </section>
        <div class="composer-media-row" data-composer-media-row hidden>
          <input type="file" accept="image/*" multiple data-composer-image-input hidden>
          <button type="button" class="link-button composer-add-image" data-composer-add-image>Add image</button>
          <ul class="composer-attachment-strip" data-composer-attachments></ul>
        </div>
        <div class="toolbar dialog-actions">
          <button type="button" data-composer-cancel>Cancel</button>
          <button type="submit" data-composer-submit>Publish</button>
        </div>
      </form>
    </dialog>
  `;
}

export function feedRightRail(timeframe, searchQuery = "") {
  return `
    <aside class="right-rail">
      ${railSearch("Search", searchQuery)}
      <section class="trending-panel">
        <h2>Trending</h2>
        <label class="trending-filter">Timeframe
          <select data-trending-timeframe>
            <option value="24h"${timeframe === "24h" ? " selected" : ""}>24hr</option>
            <option value="1w"${timeframe === "1w" ? " selected" : ""}>1 Week</option>
          </select>
        </label>
        <div data-trending-target>
          <div class="text-skeleton-stack" aria-hidden="true">
            <p class="text-skeleton text-skeleton-block">----------------------------</p>
            <p class="text-skeleton text-skeleton-block">------------------------</p>
            <p class="text-skeleton text-skeleton-block">------------------------------</p>
          </div>
        </div>
      </section>
    </aside>
  `;
}

export function readsRightRail(timeframe, searchQuery = "") {
  return `
    <aside class="right-rail reads-right-rail" data-reads-right-rail>
      ${railSearch("Search reads", searchQuery)}
      <section class="trending-panel">
        <h2>Trending Reads</h2>
        <label class="trending-filter">Timeframe
          <select data-reads-trending-timeframe>
            <option value="24h"${timeframe === "24h" ? " selected" : ""}>24hr</option>
            <option value="1w"${timeframe === "1w" ? " selected" : ""}>1 Week</option>
          </select>
        </label>
        <div data-trending-target>
          <div class="text-skeleton-stack" aria-hidden="true">
            <p class="text-skeleton text-skeleton-block">----------------------------</p>
            <p class="text-skeleton text-skeleton-block">------------------------</p>
            <p class="text-skeleton text-skeleton-block">------------------------------</p>
          </div>
        </div>
      </section>
    </aside>
  `;
}

/**
 * Static right rail for non-feed routes. Set `trending: false` to match server
 * pages that set HideTrendingRail until client hydration fills the route.
 * @param {string} searchQuery
 * @param {{ trending?: boolean, mode?: string }} [opts]
 */
export function staticRightRail(searchQuery = "", { trending = true, mode = "notes" } = {}) {
  const trendingPanel = trending
    ? `
      <section class="trending-panel">
        <h2>Trending</h2>
        <label class="trending-filter">Timeframe
          <select disabled>
            <option>24hr</option>
            <option>1 Week</option>
          </select>
        </label>
        <p class="muted">Trending placeholders appear here outside the feed page.</p>
      </section>`
    : "";
  return `
    <aside class="right-rail">
      ${railSearch(mode === "users" ? "Search users" : "Search notes", searchQuery, mode)}
      ${trendingPanel}
    </aside>
  `;
}

/** Route-only markup (inside `[data-route-outlet]`). Persistent chrome lives in `base.html`. */
export function renderRouteOutletLayout({ shellClass = "", mainContent = "", rightRail = "" }) {
  void shellClass;
  return `
    ${mainContent}
    ${rightRail}
  `;
}

/** Full `<main>` subtree for tests or non–feed-shell pages. */
export function renderShellLayout({ active = "", shellClass = "", mainContent = "", rightRail = "", menuSearchQuery = "" }) {
  return `
    ${shellMobileBar()}
    <div class="app-shell">
      ${leftRail(active)}
      <div data-route-outlet>
        ${renderRouteOutletLayout({ shellClass, mainContent, rightRail })}
      </div>
    </div>
    ${mobileMenu(menuSearchQuery)}
    ${postPlaceholderDialog()}
  `;
}
