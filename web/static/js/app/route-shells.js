import {
  feedLoaderMarkup,
  feedRightRail,
  notificationsLoaderMarkup,
  profileFeedLoaderMarkup,
  profileListLoaderMarkup,
  readsRightRail,
  staticRightRail,
  threadRouteLoaderMarkup,
  threadRepliesHostSkeletonMarkup,
  threadTreeSkeletonMarkup,
} from "../shell.js";
import { renderStaticStubMainContent } from "../static-stub-pages.js";
import { parseTagFromPath } from "../hashtag-utils.js";
import { pubkeyFromProfilePath } from "../nav-routing.js";

function outletMain(attrs = "") {
  return `data-route-outlet="main"${attrs ? ` ${attrs}` : ""}`;
}

function rightRail(html = "") {
  if (!html) return "";
  const trimmed = String(html || "").trim();
  if (!trimmed) return "";
  if (/^<aside\b/i.test(trimmed)) {
    return trimmed.replace(/^<aside\b/i, '<aside data-route-outlet="right-rail"');
  }
  return `<aside class="right-rail" data-route-outlet="right-rail">${trimmed}</aside>`;
}

function staticRail(url, options = {}) {
  return rightRail(staticRightRail(url.searchParams.get("q") || "", {
    mode: url.searchParams.get("mode") || "notes",
    ...options,
  }));
}

function feedShell(url) {
  return `
    <section class="feed-column" ${outletMain("data-shell-main")}>
      <section id="feed-heading" data-feed-heading></section>
      <div class="feed-status-slot">
        <button class="feed-new-notes" type="button" data-new-notes hidden>Load <span data-new-notes-count>0</span> new notes</button>
      </div>
      <section id="feed" class="feed" data-feed>
        ${feedLoaderMarkup()}
      </section>
      <button class="load-more" data-load-more data-feed-url="/feed" data-cursor="" data-cursor-id="" type="button" hidden>Load more</button>
    </section>
    ${rightRail(feedRightRail("24h", url.searchParams.get("q") || ""))}
  `;
}

function threadShell(url) {
  const selected = (url.pathname + url.search + url.hash).replace(/"/g, "&quot;");
  return `
    <section class="feed-column" ${outletMain(`data-shell-main data-thread-route-pending="${selected}"`)}>
      <section id="thread-summary" data-thread-fragment="summary">
        ${threadRouteLoaderMarkup({ showActivity: true })}
      </section>
      <section id="thread-tree-view" data-thread-fragment="tree" hidden>${threadTreeSkeletonMarkup()}</section>
      <section id="thread-ancestors" data-thread-fragment="ancestors"></section>
      <section id="thread-focus" data-thread-fragment="focus"></section>
      <section class="thread-replies">
        ${threadRepliesHostSkeletonMarkup({ expectReplies: false })}
        <button class="load-more" type="button" data-thread-load-more data-load-label="Load more thread replies" data-cursor="" data-cursor-id="" hidden>Load more thread replies</button>
      </section>
    </section>
    <aside class="right-rail" data-route-outlet="right-rail" data-thread-fragment="participants">
      <section class="thread-people-panel">
        <h2>People in this thread</h2>
        <ul class="thread-people" aria-hidden="true">
          <li><div class="thread-person"><span class="thread-person-avatar-skeleton" aria-hidden="true">@</span><div class="thread-person-meta"><strong class="text-skeleton">---------</strong><span class="text-skeleton text-skeleton-block">-------------------------</span><em class="text-skeleton">------</em></div></div></li>
          <li><div class="thread-person"><span class="thread-person-avatar-skeleton" aria-hidden="true">@</span><div class="thread-person-meta"><strong class="text-skeleton">--------</strong><span class="text-skeleton text-skeleton-block">-----------------------</span><em class="text-skeleton">------</em></div></div></li>
        </ul>
      </section>
    </aside>
  `;
}

function readsShell(url) {
  return `
    <section class="feed-column reads-column" ${outletMain()}>
      <section id="reads-heading" data-reads-heading>
        <p class="text-skeleton text-skeleton-block" aria-hidden="true">----------------------------------------------</p>
        <p class="text-skeleton text-skeleton-block" aria-hidden="true">----------------------------------------------</p>
      </section>
      <section id="reads-list" class="reads-list" data-reads>
        <div class="text-skeleton-stack" aria-hidden="true">
          <p class="text-skeleton text-skeleton-block">----------------------------</p>
          <p class="text-skeleton text-skeleton-block">------------------------</p>
        </div>
      </section>
      <p class="reads-more"><button class="load-more" type="button" data-load-more data-feed-url="/reads" data-cursor="" data-cursor-id="" hidden>Load more reads</button></p>
    </section>
    ${rightRail(readsRightRail("24h", url.searchParams.get("q") || ""))}
  `;
}

function readShell(url) {
  return `
    <section class="feed-column reads-column read-detail-column" ${outletMain("data-shell-main")}>
      <section class="page-heading"><p class="text-skeleton text-skeleton-block" aria-hidden="true">----------------------</p></section>
      <article class="read-article is-full">
        <div class="ascii-border"></div>
        <div class="ascii-row read-ascii-row">
          <span class="ascii-edge">|</span>
          <div class="ascii-content read-ascii-content">
            <p class="text-skeleton text-skeleton-block" aria-hidden="true">------------------------------------------</p>
            <p class="text-skeleton text-skeleton-block" aria-hidden="true">------------------------------------------</p>
            <p class="text-skeleton text-skeleton-block" aria-hidden="true">------------------------------------------</p>
          </div>
          <span class="ascii-edge">|</span>
        </div>
        <div class="ascii-border"></div>
      </article>
    </section>
    ${rightRail(readsRightRail("24h", url.searchParams.get("q") || ""))}
  `;
}

function timelineShell(url, { title = "", feedAttrs = "", loadMoreURL = "" } = {}) {
  const loadMore = loadMoreURL
    ? `<button class="load-more" data-load-more data-feed-url="${loadMoreURL}" data-cursor="" data-cursor-id="" type="button" hidden>Load more</button>`
    : "";
  return `
    <section class="feed-column shell-main-top" ${outletMain("data-shell-main")}>
      <section class="page-heading">${title ? `<h1>${title}</h1>` : '<h1 class="text-skeleton">---------</h1>'}</section>
      <section class="feed" data-feed${feedAttrs ? ` ${feedAttrs}` : ""}>
        <div class="text-skeleton-stack" aria-hidden="true">
          <pre class="ascii-card text-skeleton-note">+- ---------------- -- ---- --------------------------------------+
| --------------------------------------------------------------- |
| -----------------------------------------------                 |
+-- ----- [-- -------] -------------------------------------- ----+</pre>
        </div>
      </section>
      ${loadMore}
    </section>
    ${staticRail(url, { trending: false })}
  `;
}

function notificationsShell(url) {
  return `
    <section class="feed-column shell-main-top" ${outletMain("data-shell-main")}>
      <section class="page-heading"><h1>Notifications</h1></section>
      <section class="notifications-toolbar" data-notifications-toolbar aria-hidden="true"></section>
      <section class="feed" data-feed data-notifications-feed>${notificationsLoaderMarkup()}</section>
    </section>
    ${staticRail(url, { trending: false })}
  `;
}

function searchShell(url) {
  return `
    <section class="feed-column shell-main-top" ${outletMain("data-shell-main")}>
      <section class="page-heading search-heading" data-search-heading>
        <h1 class="text-skeleton">------</h1>
        <p class="text-skeleton text-skeleton-block" aria-hidden="true">------------------------------------------</p>
      </section>
      <section class="feed search-results" data-feed data-search-results></section>
      <button class="load-more" data-load-more data-feed-url="/search" data-cursor="" data-cursor-id="" type="button" hidden>Load more</button>
    </section>
    ${staticRail(url, { trending: false })}
  `;
}

function tagShell(url) {
  const tag = encodeURIComponent(parseTagFromPath(url.pathname) || "tag");
  return `
    <section class="feed-column shell-main-top" ${outletMain("data-shell-main")}>
      <section class="page-heading search-heading" data-tag-heading>
        <h1 class="text-skeleton">---------</h1>
        <p class="text-skeleton text-skeleton-block" aria-hidden="true">------------------------------------------</p>
      </section>
      <section class="feed search-results" data-feed data-tag-results></section>
      <button class="load-more" data-load-more data-feed-url="/tag/${tag}" data-cursor="" data-cursor-id="" type="button" hidden>Load more</button>
    </section>
    ${staticRail(url)}
  `;
}

function profileShell(url) {
  const pubkey = pubkeyFromProfilePath(url.pathname) || "";
  return `
    <section class="feed-column user-profile-column" ${outletMain(`data-shell-main data-profile-shell="1" data-profile-pubkey="${pubkey}" data-profile-relays=""`)}>
      <section id="user-header" data-user-fragment="header">
        <section class="profile profile-modern profile-skeleton" aria-hidden="true">
          <div class="profile-hero">
            <div class="profile-hero-body">
              <div class="profile-hero-head">
                <div class="profile-hero-top">
                  <span class="profile-display-name text-skeleton profile-skeleton-display-name">----------------</span>
                  <span class="text-skeleton profile-hero-options-skeleton" aria-hidden="true">[...]</span>
                </div>
              </div>
              <div class="profile-hero-media-row">
                <div class="profile-avatar-wrap"><div class="profile-avatar-fallback profile-skeleton-avatar">@</div></div>
                <div class="profile-hero-side">
                  <p class="text-skeleton text-skeleton-block profile-hero-nip05-skeleton" aria-hidden="true">-----------------------------</p>
                  <p class="text-skeleton text-skeleton-block profile-hero-meta-skeleton" aria-hidden="true">---------------------</p>
                  <p class="text-skeleton text-skeleton-block profile-hero-meta-skeleton" aria-hidden="true">-----------------------------</p>
                  <div class="profile-npub-block profile-npub-block--header profile-npub-block--skeleton" aria-hidden="true"><div class="profile-npub-grid"><div class="profile-npub-grid-row"><span class="text-skeleton profile-npub-skel-cell">----</span><span class="text-skeleton profile-npub-skel-cell">----</span><span class="text-skeleton profile-npub-skel-cell">----</span><span class="text-skeleton profile-npub-skel-cell">----</span></div></div></div>
                </div>
              </div>
            </div>
          </div>
          <div class="profile-main"><div class="profile-ident"><p class="text-skeleton text-skeleton-block">-----------------------------</p></div></div>
        </section>
      </section>
      <section id="user-stats" class="stats profile-stats-row" data-user-fragment="stats">
        <button type="button" class="link-button profile-stat-link" data-profile-tab="user-tab-following" data-profile-following-count-wrap aria-label="Following">Following <span class="muted">(<span data-profile-following-count>...</span>)</span></button>
        <span class="muted profile-stats-sep" aria-hidden="true">•</span>
        <button type="button" class="link-button profile-stat-link" data-profile-tab="user-tab-followers" data-profile-followers-count-wrap aria-label="Followed by">Followed <span class="muted">(<span data-profile-followers-count>...</span>)</span></button>
      </section>
      <div class="user-tabs profile-tabs">
        <nav class="user-tab-nav" aria-label="Profile timeline">
          <label class="user-tab-label" for="user-tab-posts">Posts</label><span class="user-tab-sep" aria-hidden="true">·</span>
          <label class="user-tab-label" for="user-tab-replies">Replies</label><span class="user-tab-sep" aria-hidden="true">·</span>
          <label class="user-tab-label" for="user-tab-media">Media</label>
        </nav>
        <input type="radio" name="user-tab" id="user-tab-posts" class="user-tab-state" checked>
        <section class="user-tab-panel" id="user-panel-posts" data-user-fragment="posts"><button class="feed-new-notes" type="button" data-profile-new-notes hidden>Show <span data-profile-new-notes-count>0</span> newer posts</button><div class="feed" data-feed data-profile-feed="posts">${profileFeedLoaderMarkup("posts")}</div><button class="load-more" data-load-more data-feed-url="${url.pathname}" data-cursor="" data-cursor-id="" type="button" hidden>Load more</button></section>
        <input type="radio" name="user-tab" id="user-tab-replies" class="user-tab-state"><section class="user-tab-panel" id="user-panel-replies" data-user-fragment="replies"><div class="feed" data-profile-feed="replies">${profileFeedLoaderMarkup("replies")}</div></section>
        <input type="radio" name="user-tab" id="user-tab-media" class="user-tab-state"><section class="user-tab-panel" id="user-panel-media" data-user-fragment="media"><div class="feed" data-profile-feed="media">${profileFeedLoaderMarkup("media")}</div></section>
        <input type="radio" name="user-tab" id="user-tab-following" class="user-tab-state"><section class="user-tab-panel" id="user-panel-following" data-user-fragment="following">${profileListLoaderMarkup("following")}</section>
        <input type="radio" name="user-tab" id="user-tab-followers" class="user-tab-state"><section class="user-tab-panel" id="user-panel-followers" data-user-fragment="followers">${profileListLoaderMarkup("followers")}</section>
        <input type="radio" name="user-tab" id="user-tab-identifiers" class="user-tab-state"><section class="user-tab-panel" id="user-panel-identifiers" data-user-fragment="identifiers"></section>
        <input type="radio" name="user-tab" id="user-tab-relays" class="user-tab-state"><section class="user-tab-panel profile-mobile-only-panel" id="user-panel-relays" data-user-fragment="relays"></section>
      </div>
    </section>
    <aside class="right-rail profile-right-rail" data-route-outlet="right-rail"><section class="profile-card profile-right-panel profile-right-panel-skeleton" id="user-right-relays" data-user-fragment="relays" data-profile-relays=""></section></aside>
  `;
}

function stubShell(url) {
  const mainContent = renderStaticStubMainContent(url.pathname) || `
    <section class="feed-column shell-main-top" ${outletMain("data-shell-main")}>
      <section class="page-heading"><h1 class="text-skeleton">--------------</h1></section>
    </section>`;
  const hideTrending = ["/about", "/settings", "/support", "/ios-plain-text-nostr", "/terms", "/privacy", "/relays"].includes(url.pathname);
  return `${mainContent.replace(/<section\b/, `<section ${outletMain("data-shell-main")}`)}${staticRail(url, { trending: !hideTrending })}`;
}

export function renderShellForRoute(route, url) {
  if (route === "feed") return feedShell(url);
  if (route === "thread") return threadShell(url);
  if (route === "profile") return profileShell(url);
  if (route === "reads") return readsShell(url);
  if (route === "read") return readShell(url);
  if (route === "bookmarks") return timelineShell(url, { title: "", loadMoreURL: "" });
  if (route === "notifications") return notificationsShell(url);
  if (route === "search") return searchShell(url);
  if (route === "tag") return tagShell(url);
  if (route === "relays") return stubShell(new URL("/relays", url.origin));
  if (route === "stub") return stubShell(url);
  return "";
}
