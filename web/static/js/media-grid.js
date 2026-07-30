import { prepareInlineVideo } from "./inline-video.js";

export const MEDIA_GRID_LIMIT = 6;

function gridClass(count) {
  return `note-media-grid note-media-grid-${Math.max(1, Math.min(MEDIA_GRID_LIMIT, count))}`;
}

export function mediaGridSignature(items) {
  if (!Array.isArray(items) || items.length === 0) return "";
  return items
    .map((item) => `${item?.type === "video" ? "video" : "image"}:${String(item?.url || "")}`)
    .join("|");
}

export function mediaGridImageAspectRatio(width, height) {
  if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) {
    return "";
  }
  return `${Math.round(width)} / ${Math.round(height)}`;
}

export function mediaGridVideoAspectRatio(video) {
  if (!video || typeof video !== "object") return "";
  if (!("videoWidth" in video) || !("videoHeight" in video)) return "";
  return mediaGridImageAspectRatio(video.videoWidth, video.videoHeight);
}

function applyImageAspectRatio(grid, ratio) {
  if (!grid || !ratio) return;
  grid.style.setProperty("--note-media-image-aspect-ratio", ratio);
  grid.dataset.mediaGridAspectRatio = ratio;
}

export function mediaGridItemAspectRatio(item) {
  return mediaGridImageAspectRatio(Number(item?.width), Number(item?.height));
}

function gridVideo(url) {
  const video = document.createElement("video");
  video.src = url;
  video.controls = true;
  video.preload = "metadata";
  prepareInlineVideo(video);
  return video;
}

function bindMediaGrid(grid, items, {
  onOpen,
  stopPropagation = false,
} = {}) {
  if (!(grid instanceof HTMLElement)) return;
  grid._ptxtMediaGridOptions = { items, onOpen, stopPropagation };
  if (grid.dataset.mediaGridBound !== "1") {
    grid.dataset.mediaGridBound = "1";
    grid.addEventListener("click", (event) => {
      const trigger = event.target instanceof Element
        ? event.target.closest("[data-media-grid-open]")
        : null;
      if (!trigger || !grid.contains(trigger)) return;
      event.preventDefault();
      const options = grid._ptxtMediaGridOptions || {};
      if (options.stopPropagation) event.stopPropagation();
      const index = Number.parseInt(trigger.getAttribute("data-media-grid-open") || "0", 10) || 0;
      options.onOpen?.(index);
    });
  }
  grid.querySelectorAll("video").forEach((video) => prepareInlineVideo(video));
}

export function hydrateMediaGrid(wrap, items, {
  onOpen,
  stopPropagation = false,
} = {}) {
  if (!(wrap instanceof HTMLElement) || !items?.length) return null;
  if (wrap.dataset.mediaGridSignature !== mediaGridSignature(items)) return null;
  const grid = wrap.querySelector(":scope > .note-media-grid");
  if (!(grid instanceof HTMLElement)) return null;
  const singleItem = items.length === 1 ? items[0] : null;
  if (singleItem?.type === "video") {
    grid.classList.add("note-media-grid-single-video");
  }
  const ratio = mediaGridItemAspectRatio(singleItem);
  if (ratio) applyImageAspectRatio(grid, ratio);
  bindMediaGrid(grid, items, { onOpen, stopPropagation });
  return wrap;
}

export function createMediaGrid(items, {
  onOpen,
  wrapperTag = "span",
  gridTag = "span",
  wrapperClass = "",
  stopPropagation = false,
} = {}) {
  if (!items?.length) return null;
  const wrap = document.createElement(wrapperTag);
  wrap.className = `note-media-grid-wrap ${wrapperClass}`.trim();
  wrap.dataset.mediaGridSignature = mediaGridSignature(items);
  const grid = document.createElement(gridTag);
  const singleItem = items.length === 1 ? items[0] : null;
  grid.className = gridClass(items.length > MEDIA_GRID_LIMIT ? MEDIA_GRID_LIMIT : items.length);
  if (singleItem?.type === "video") {
    grid.classList.add("note-media-grid-single-video");
  }
  const ratio = mediaGridItemAspectRatio(singleItem);
  if (ratio) applyImageAspectRatio(grid, ratio);
  const visibleItems = items.length > MEDIA_GRID_LIMIT
    ? items.slice(0, MEDIA_GRID_LIMIT - 1)
    : items.slice(0, MEDIA_GRID_LIMIT);
  visibleItems.forEach((item, mediaIndex) => {
    if (item.type === "image") {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "note-media-tile note-media-image-tile";
      button.dataset.mediaGridOpen = `${mediaIndex}`;
      button.setAttribute("aria-label", `Open media ${mediaIndex + 1} of ${items.length}`);
      const img = document.createElement("img");
      img.alt = "";
      img.loading = "lazy";
      img.decoding = "async";
      img.src = item.url;
      button.append(img);
      grid.append(button);
      return;
    }
    const tile = document.createElement("span");
    tile.className = "note-media-tile note-media-video-tile";
    tile.setAttribute("aria-label", `Video ${mediaIndex + 1} of ${items.length}`);
    if (singleItem?.type === "video") {
      tile.classList.add("note-media-video-tile-single");
    }
    tile.append(gridVideo(item.url));
    grid.append(tile);
  });
  if (items.length > MEDIA_GRID_LIMIT) {
    const more = document.createElement("button");
    more.type = "button";
    more.className = "note-media-tile note-media-more-tile";
    more.dataset.mediaGridOpen = `${MEDIA_GRID_LIMIT - 1}`;
    more.textContent = "+";
    more.setAttribute("aria-label", `View all ${items.length} media items`);
    grid.append(more);
  }
  wrap.append(grid);
  bindMediaGrid(grid, items, { onOpen, stopPropagation });
  return wrap;
}
