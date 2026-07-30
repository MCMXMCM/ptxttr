/**
 * Mobile Safari needs explicit inline playback flags on <video> for reliable
 * in-page controls. Videos also need enough data loaded to paint their first
 * frame before playback; metadata-only loading leaves many browsers with a
 * blank controls surface. Do not link users to the raw URL for Blossom-style
 * hosts: wrong Content-Type makes Safari offer a useless .bin download.
 */
export function prepareInlineVideo(video) {
  if (!(video instanceof HTMLVideoElement)) return;
  video.playsInline = true;
  video.setAttribute("playsinline", "");
  video.setAttribute("webkit-playsinline", "");
  video.preload = "auto";
  video.setAttribute("preload", "auto");
  video.addEventListener(
    "error",
    () => {
      const figure = video.closest("figure");
      if (!figure || figure.querySelector("[data-video-fallback]")) return;
      const wrap = document.createElement("p");
      wrap.className = "note-video-fallback muted";
      wrap.dataset.videoFallback = "1";
      wrap.textContent =
        "Could not play in the page (the file host often sends a non-video type to browsers).";
      figure.append(wrap);
    },
    { once: true },
  );
}
