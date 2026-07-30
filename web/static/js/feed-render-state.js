export function renderedFeedSort(feed) {
  return String(feed?.dataset?.relayNativeFeedSort || feed?.dataset?.feedSort || "").trim();
}

export function renderedFeedSortChanged(feed, sort) {
  const previousSort = renderedFeedSort(feed);
  return Boolean(previousSort) && previousSort !== String(sort || "").trim();
}

/**
 * True when server-rendered feed rows belong to a different browser-local
 * viewer or preference scope. Top-level document requests cannot carry the
 * X-Ptxt-* headers that later fragment requests use, so this check prevents a
 * cursor from one scope being reused in another.
 */
export function renderedFeedSessionChanged(feed, {
  viewer = "",
  sort = "recent",
  wotEnabled = true,
  wotDepth = 1,
} = {}) {
  if (!feed?.dataset) return false;
  if (renderedFeedSortChanged(feed, sort)) return true;

  const hasViewerScope = Object.hasOwn(feed.dataset, "feedViewer");
  if (hasViewerScope && String(feed.dataset.feedViewer || "").trim().toLowerCase() !== String(viewer || "").trim().toLowerCase()) {
    return true;
  }

  const hasWotScope = Object.hasOwn(feed.dataset, "feedWotEnabled");
  if (hasWotScope && (feed.dataset.feedWotEnabled === "1") !== Boolean(wotEnabled)) {
    return true;
  }

  const hasDepthScope = Object.hasOwn(feed.dataset, "feedWotDepth");
  if (hasDepthScope && Number.parseInt(feed.dataset.feedWotDepth || "0", 10) !== Number(wotDepth || 1)) {
    return true;
  }
  return false;
}
