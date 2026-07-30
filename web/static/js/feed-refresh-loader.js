import { refreshAscii } from "./ascii.js";
import { homeFeedElement } from "./feed-dom.js";
import { initRetroLoaders, setRetroLoaderProgress } from "./retro-loader.js";
import { feedLoaderMarkup } from "./shell.js";

export function showHomeFeedRefreshLoader(root = document, options = {}) {
  const {
    percent = 12,
    statusMessage = "refreshing feed...",
    replaceNotes = true,
  } = options;
  const feed = homeFeedElement(root);
  if (!(feed instanceof HTMLElement)) return false;

  const existingLoader = feed.querySelector("[data-feed-loader]");
  if (existingLoader instanceof HTMLElement) {
    const loadMore = root.querySelector?.('[data-load-more][data-feed-url="/feed"]');
    if (loadMore instanceof HTMLButtonElement) {
      loadMore.hidden = true;
      loadMore.disabled = true;
    }
    setRetroLoaderProgress(existingLoader, { percent, statusMessage });
    return true;
  }

  if (!replaceNotes && feed.querySelector(".note[id^='note-']")) return false;
  const loadMore = root.querySelector?.('[data-load-more][data-feed-url="/feed"]');
  if (loadMore instanceof HTMLButtonElement) {
    loadMore.hidden = true;
    loadMore.disabled = true;
  }
  feed.innerHTML = feedLoaderMarkup({ showActivity: true });
  initRetroLoaders(feed);
  const loader = feed.querySelector("[data-feed-loader]");
  if (loader instanceof HTMLElement) {
    setRetroLoaderProgress(loader, { percent, statusMessage });
  }
  refreshAscii(root);
  return true;
}
