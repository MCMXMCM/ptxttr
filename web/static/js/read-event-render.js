import { displayName, shortNpubLabel } from "./profile-parse.js";
import { normalizePubkey, profilePath } from "./relay-utils.js";

function formatDate(unix) {
  const ts = Number(unix) || 0;
  if (!ts) return "";
  return new Date(ts * 1000).toLocaleDateString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
  });
}

export function renderReadArticle(card, profile = {}) {
  const event = card.event;
  const id = String(event?.id || "");
  const article = document.createElement("article");
  article.className = "read-article";
  article.id = `read-${id}`;

  const pk = normalizePubkey(event?.pubkey);
  const author = displayName(profile) || shortNpubLabel(pk);
  const href = `/reads/${id}`;
  const userHref = profilePath(pk, event?.relay_url ? [event.relay_url] : []);

  article.innerHTML = `
    <div class="ascii-border"></div>
    <div class="ascii-row read-ascii-row">
      <span class="ascii-edge">|</span>
      <div class="ascii-content read-ascii-content">
        <header class="read-meta">
          <div class="read-meta-copy">
            <h2><a href="${href}" data-relay-aware>${escapeHtml(card.title)}</a></h2>
            <p class="muted"><a href="${userHref}" data-relay-aware>${escapeHtml(author)}</a> · <span class="mono">${formatDate(card.publishedAt)}</span></p>
            ${card.summary ? `<p class="read-summary">${escapeHtml(card.summary)}</p>` : ""}
          </div>
          ${
            card.imageURL
              ? `<a class="read-thumb" href="${href}" data-relay-aware><img src="${escapeHtml(card.imageURL)}" alt="" loading="lazy" decoding="async"></a>`
              : ""
          }
        </header>
        <section class="read-body">${escapeHtml(String(event?.content || "").slice(0, 800))}</section>
        <p class="reads-actions"><a href="${href}" data-relay-aware>Open article</a></p>
      </div>
      <span class="ascii-edge">|</span>
    </div>
    <div class="ascii-border"></div>
  `;
  return article;
}

function escapeHtml(value) {
  return String(value || "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

export function renderReadsList(cards, profilesByPubkey = {}) {
  const fragment = document.createDocumentFragment();
  if (!cards?.length) {
    const empty = document.createElement("p");
    empty.className = "muted";
    empty.textContent = "No long-form reads found yet.";
    fragment.append(empty);
    return fragment;
  }
  cards.forEach((card) => {
    const pk = normalizePubkey(card.event?.pubkey);
    fragment.append(renderReadArticle(card, profilesByPubkey[pk] || {}));
  });
  return fragment;
}

function articleBodyHTML(content = "") {
  return escapeHtml(String(content || ""))
    .split(/\n{2,}/)
    .map((block) => `<p>${block.replace(/\n/g, "<br>")}</p>`)
    .join("");
}

export function renderReadDetailView(card, profile = {}, moreCards = []) {
  const event = card?.event || {};
  const id = String(event.id || "");
  const pk = normalizePubkey(event.pubkey);
  const author = displayName(profile) || shortNpubLabel(pk);
  const userHref = profilePath(pk, event?.relay_url ? [event.relay_url] : []);
  const moreItems = moreCards.length
    ? moreCards.map((item) => `
        <li>
          <a href="/reads/${escapeHtml(item.event?.id || "")}" data-relay-aware>
            <strong>${escapeHtml(item.title)}</strong>
            ${item.summary ? `<span>${escapeHtml(item.summary)}</span>` : ""}
            <em>${formatDate(item.publishedAt)}</em>
          </a>
        </li>`).join("")
    : '<li class="muted">No additional reads yet.</li>';
  return {
    mainContent: `
      <section class="feed-column reads-column read-detail-column" data-shell-main>
        <section class="page-heading">
          <p><a href="/reads" data-relay-aware data-session-reads-link>&lt;-- Back to reads</a></p>
        </section>
        <article class="read-article is-full" id="read-${escapeHtml(id)}">
          <div class="ascii-border"></div>
          <div class="ascii-row read-ascii-row">
            <span class="ascii-edge">|</span>
            <div class="ascii-content read-ascii-content">
              <header class="read-meta">
                <div class="read-meta-copy">
                  <h1 class="read-title">${escapeHtml(card.title)}</h1>
                  <p class="muted"><a href="${userHref}" data-relay-aware>${escapeHtml(author)}</a> · <span class="mono">${formatDate(card.publishedAt)}</span></p>
                </div>
                ${card.imageURL ? `<div class="read-thumb read-thumb-full"><img src="${escapeHtml(card.imageURL)}" alt="" loading="lazy" decoding="async"></div>` : ""}
              </header>
              <section class="read-body read-body-full">${articleBodyHTML(event.content)}</section>
              <p class="reads-actions"><a href="/thread/${escapeHtml(id)}?back_read=${escapeHtml(id)}" data-relay-aware>View replies</a></p>
            </div>
            <span class="ascii-edge">|</span>
          </div>
          <div class="ascii-border"></div>
        </article>
      </section>
    `,
    rightRail: `
      <aside class="right-rail reads-right-rail">
        <section class="trending-panel">
          <h2>More reads from ${escapeHtml(author || "this author")}</h2>
          <ol class="trending-list">${moreItems}</ol>
        </section>
      </aside>
    `,
  };
}
