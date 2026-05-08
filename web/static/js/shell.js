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

function railSearch(placeholder, value = "") {
  const safeValue = escapeAttr(value);
  return `
    <form action="/search" method="get" class="rail-search">
      <input type="search" name="q" placeholder="${placeholder}" value="${safeValue}">
    </form>
  `;
}

export const FEED_LOADER_STATUSES = [
  "gathering notes...",
  "following trends...",
  "nostrating...",
  "warming the cache...",
];

/** Number of stacked skeleton `<pre>` cards (must match `buildFeedLoaderCardText` card index range). */
export const FEED_LOADER_CARD_COUNT = 2;

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

/** Appended to `#thread-replies` while paginating thread replies. */
export function threadRepliesPageSkeletonMarkup() {
  return `<div class="thread-replies-page-skeleton" aria-hidden="true">${skeletonWaveStackMarkup()}</div>`;
}

export function feedLoaderMarkup() {
  return `
    <section class="feed-loader" data-feed-loader aria-busy="true">
      <p class="muted feed-loader-status" data-feed-loader-status>${FEED_LOADER_STATUSES[0]}</p>
      ${skeletonWaveStackMarkup("data-feed-loader-card")}
    </section>
  `;
}

export function shellMobileBar() {
  return `
    <header class="mobile-bar">
      <a href="/login" class="mobile-menu-trigger" data-session-user-link aria-label="Open profile">
        <img src="" alt="" loading="lazy" decoding="async" data-session-avatar hidden>
        <span class="mobile-trigger-fallback" data-session-avatar-fallback>@</span>
      </a>
      <a href="/" data-relay-aware class="mobile-brand" data-feed-home>Plain Text Nostr</a>
      <button type="button" class="mobile-menu-trigger mobile-menu-bar-glyph" data-mobile-menu-trigger aria-label="Open menu">
        <span class="mobile-menu-bar-icon" aria-hidden="true">≡</span>
      </button>
    </header>
  `;
}

export function leftRail(active = "") {
  return `
    <aside class="left-rail">
      <a class="rail-brand" href="/" data-relay-aware data-feed-home>Plain Text Nostr</a>
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
        <img src="" alt="" loading="lazy" decoding="async" data-session-avatar hidden>
        <span class="rail-avatar-fallback" data-session-avatar-fallback>@</span>
        <div class="rail-user-copy" data-session-user-copy hidden>
          <strong data-session-display-name>Guest</strong>
        </div>
        <a href="/login" class="rail-login" data-session-cta>Login</a>
      </div>
    </aside>
  `;
}

export function mobileMenu(searchQuery = "") {
  const safeValue = escapeAttr(searchQuery);
  return `
    <div class="mobile-menu" data-mobile-menu hidden aria-hidden="true">
      <div class="mobile-menu-backdrop" data-mobile-menu-backdrop></div>
      <div class="mobile-menu-panel" role="dialog" aria-modal="true" aria-label="Menu">
        <div class="mobile-menu-header">
          <div class="mobile-menu-title">
            <span class="about-page-logo" aria-hidden="true">
              <img src="/static/img/ascritch_icon_black.png" alt="" width="32" height="32" decoding="async" class="about-logo about-logo-light-scheme">
              <img src="/static/img/ascritch_icon_white.png" alt="" width="32" height="32" decoding="async" class="about-logo about-logo-dark-scheme">
            </span>
            <strong>Plain Text Nostr</strong>
          </div>
        </div>
        <section class="mobile-menu-intro" aria-label="Plain Text Nostr summary">
          <p class="mobile-menu-intro-copy muted">A plain-text Nostr reader: relay aggregation, web of trust, local cache.</p>
        </section>
        <div class="mobile-menu-search">
          <form action="/search" method="get" class="rail-search" role="search" aria-label="Search cached notes">
            <input type="search" name="q" placeholder="Search" value="${safeValue}" aria-label="Search cached notes">
          </form>
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
          <textarea id="composer-content" name="content" rows="6" maxlength="64000" data-composer-content required></textarea>
          <div class="composer-mention-menu" data-composer-mentions hidden>
            <ul class="composer-mention-list" data-composer-mention-list role="listbox" aria-label="Mention suggestions"></ul>
          </div>
        </div>
        <section class="composer-repost-preview" data-composer-preview hidden>
          <p class="muted">Reposting</p>
          <pre class="composer-repost-preview-content" data-composer-preview-content></pre>
        </section>
        <div class="toolbar">
          <button type="submit" data-composer-submit>Publish</button>
          <button type="button" data-composer-cancel>Cancel</button>
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
        <div class="text-skeleton-stack" aria-hidden="true">
          <p class="text-skeleton text-skeleton-block">----------------------------</p>
          <p class="text-skeleton text-skeleton-block">------------------------</p>
          <p class="text-skeleton text-skeleton-block">------------------------------</p>
        </div>
      </section>
    </aside>
  `;
}

export function staticRightRail(searchQuery = "") {
  return `
    <aside class="right-rail">
      ${railSearch("Search", searchQuery)}
      <section class="trending-panel">
        <h2>Trending</h2>
        <label class="trending-filter">Timeframe
          <select disabled>
            <option>24hr</option>
            <option>1 Week</option>
          </select>
        </label>
        <p class="muted">Trending placeholders appear here outside the feed page.</p>
      </section>
    </aside>
  `;
}

/** Route-only markup (inside `[data-route-outlet]`). Persistent chrome lives in `base.html`. */
export function renderRouteOutletLayout({ active = "", shellClass = "", mainContent = "", rightRail = "" }) {
  const shellClassName = shellClass ? `app-shell ${shellClass}` : "app-shell";
  return `
    <div class="${shellClassName}">
      ${leftRail(active)}
      ${mainContent}
      ${rightRail}
    </div>
  `;
}

/** Full `<main>` subtree for tests or non–feed-shell pages; SPA navigations use {@link renderRouteOutletLayout}. */
export function renderShellLayout({ active = "", shellClass = "", mainContent = "", rightRail = "", menuSearchQuery = "" }) {
  return `
    ${shellMobileBar()}
    ${renderRouteOutletLayout({ active, shellClass, mainContent, rightRail })}
    ${mobileMenu(menuSearchQuery)}
    ${postPlaceholderDialog()}
  `;
}
