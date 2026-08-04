/**
 * Mobile Safari needs explicit inline playback flags on <video> for reliable
 * in-page controls. Keep idle feed videos on metadata-only loading: eagerly
 * buffering every video in a feed competes with the one the user is actually
 * playing and can starve its audio buffer even while video frames stay ahead.
 * Do not link users to the raw URL for Blossom-style hosts: wrong Content-Type
 * makes Safari offer a useless .bin download.
 */
export function prepareInlineVideo(video) {
  if (!(video instanceof HTMLVideoElement)) return;
  video.playsInline = true;
  video.setAttribute("playsinline", "");
  video.setAttribute("webkit-playsinline", "");
  const activelyLoading = video.autoplay || video.paused === false;
  video.preload = activelyLoading ? "auto" : "metadata";
  video.setAttribute("preload", video.preload);
  if (video.dataset.ptxtInlineVideoPrepared === "1") return;
  video.dataset.ptxtInlineVideoPrepared = "1";
  video.addEventListener("play", () => {
    video.preload = "auto";
    video.setAttribute("preload", "auto");
    const ownerDocument = video.ownerDocument;
    ownerDocument?.querySelectorAll?.("video, audio").forEach((media) => {
      if (media !== video && media.paused === false) media.pause();
    });
  });
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
