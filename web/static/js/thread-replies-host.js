/** Resolve `#thread-replies`, reusing the app-shell placeholder host when present. */
export function ensureThreadRepliesHost(repliesSection) {
  const existing = repliesSection.querySelector("#thread-replies");
  if (existing) {
    repliesSection.querySelector("[data-thread-replies-host]")?.remove();
    return existing;
  }

  const legacyHost = repliesSection.querySelector("[data-thread-replies-host]");
  if (legacyHost) {
    legacyHost.removeAttribute("data-thread-replies-host");
    legacyHost.className = "comments";
    legacyHost.id = "thread-replies";
    legacyHost.dataset.threadFragment = "replies";
    legacyHost.replaceChildren();
    return legacyHost;
  }

  const host = document.createElement("div");
  host.className = "comments";
  host.id = "thread-replies";
  host.dataset.threadFragment = "replies";
  repliesSection.append(host);
  return host;
}
