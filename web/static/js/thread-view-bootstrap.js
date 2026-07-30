(function () {
  function resolveThreadViewMode() {
    try {
      if (sessionStorage.getItem("ptxtTreeToThreadLinear") === "1") {
        return "thread";
      }
      var state = history.state;
      if (state && typeof state === "object" && state.ptxt && typeof state.ptxt === "object") {
        if (state.ptxt.threadView === "tree") return "tree";
        if (state.ptxt.threadView === "linear") return "thread";
      }
      if (String(localStorage.getItem("ptxt_thread_render_mode") || "").trim().toLowerCase() === "tree") {
        return "tree";
      }
    } catch (error) {
      /* ignore quota / private mode */
    }
    return "thread";
  }

  window.__ptxtResolveThreadViewMode = resolveThreadViewMode;
  document.documentElement.dataset.ptxtThreadView = resolveThreadViewMode();

  try {
    var rawImageMode = String(localStorage.getItem("ptxt_image_mode") || "").trim().toLowerCase();
    document.documentElement.dataset.ptxtImageMode =
      rawImageMode === "0" || rawImageMode === "false" || rawImageMode === "off"
        ? "off"
        : "on";
  } catch (error) {
    document.documentElement.dataset.ptxtImageMode = "on";
  }
})();
