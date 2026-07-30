import { closeActionMenus } from "./ascii.js";
import { bindProfilePaymentCopy, refreshNIP05Verification } from "./nip05-verify.js";

const desktopProfileMedia = window.matchMedia("(min-width: 701px)");

const NPUB_COPY_TOAST_MS = 2200;

const npubCopyToastTimers = new WeakMap();

function replaceProfileHash(panelID) {
  if (!panelID || !window.history?.replaceState) return;
  const url = new URL(window.location.href);
  if (url.hash === `#${panelID}`) return;
  url.hash = panelID;
  window.history.replaceState(window.history.state, "", `${url.pathname}${url.search}${url.hash}`);
}

function profileScopeFor(node, fallbackRoot = document) {
  const root = fallbackRoot && typeof fallbackRoot.closest === "function"
    ? fallbackRoot.closest("[data-profile-shell]")
    : null;
  if (root) return root;
  if (node && typeof node.closest === "function") {
    const scoped = node.closest("[data-profile-shell]");
    if (scoped) return scoped;
  }
  return fallbackRoot?.ownerDocument || node?.ownerDocument || document;
}

function bindProfileHexCopy(root = document) {
  root.querySelectorAll("[data-profile-hex-copy]").forEach((btn) => {
    if (!(btn instanceof HTMLButtonElement)) return;
    if (btn.dataset.boundHexCopy === "1") return;
    btn.dataset.boundHexCopy = "1";
    const hex = String(btn.getAttribute("data-pubkey") || "").trim().toLowerCase();
    btn.addEventListener("click", async (event) => {
      event.preventDefault();
      if (!hex) return;
      closeActionMenus();
      const prev = btn.textContent;
      try {
        await navigator.clipboard.writeText(hex);
        btn.textContent = "Copied hex pubkey";
      } catch {
        window.prompt("Copy hex pubkey", hex);
        btn.textContent = "Copied hex pubkey";
      }
      window.setTimeout(() => {
        btn.textContent = prev;
      }, NPUB_COPY_TOAST_MS);
    });
  });
}

function bindProfileNpubCopy(root = document) {
  root.querySelectorAll("[data-profile-npub-copy]").forEach((el) => {
    if (el.dataset.boundNpubCopy === "1") return;
    el.dataset.boundNpubCopy = "1";
    const npub = String(el.getAttribute("data-npub") || "").trim();
    const status = el.querySelector("[data-profile-npub-copy-status]");

    const hideToast = () => {
      el.classList.remove("profile-npub-copy--toast");
      if (status) {
        status.textContent = "";
        status.hidden = true;
      }
    };

    const showToast = () => {
      el.classList.add("profile-npub-copy--toast");
      if (status) {
        status.textContent = "Copied to clipboard";
        status.hidden = false;
      }
      const prev = npubCopyToastTimers.get(el);
      if (prev) window.clearTimeout(prev);
      npubCopyToastTimers.set(el, window.setTimeout(hideToast, NPUB_COPY_TOAST_MS));
    };

    const runCopy = async (event) => {
      event.preventDefault();
      if (!npub) return;
      try {
        await navigator.clipboard.writeText(npub);
        showToast();
      } catch {
        window.prompt("Copy npub", npub);
        showToast();
      }
    };

    el.addEventListener("click", (event) => {
      void runCopy(event);
    });
    el.addEventListener("keydown", (event) => {
      if (event.key !== "Enter" && event.key !== " ") return;
      void runCopy(event);
    });
  });
}

export function bindProfileStatLinks(root = document) {
  bindProfileNpubCopy(root);
  bindProfileHexCopy(root);
  bindProfilePaymentCopy(root);
  refreshNIP05Verification(root);
  const links = root.querySelectorAll("[data-profile-tab]");
  links.forEach((link) => {
    if (link.dataset.bound === "1") return;
    link.dataset.bound = "1";
    link.addEventListener("click", (event) => {
      const tabID = link.getAttribute("data-profile-tab") || "";
      const scope = profileScopeFor(link, root);
      if (tabID === "user-tab-relays" && desktopProfileMedia.matches) {
        event.preventDefault();
        closeActionMenus();
        scope.querySelector?.("#user-right-relays")?.scrollIntoView({ block: "nearest", behavior: "smooth" });
        return;
      }
      const input = tabID ? scope.querySelector?.(`#${tabID}`) : null;
      if (!(input instanceof HTMLInputElement)) return;
      event.preventDefault();
      input.checked = true;
      input.dispatchEvent(new Event("change", { bubbles: true }));
      closeActionMenus();
      const panelID = tabID.replace("tab", "panel");
      const panel = scope.querySelector?.(`#${panelID}`);
      if (panel) {
        replaceProfileHash(panelID);
        panel.scrollIntoView({ block: "start", behavior: "smooth" });
      }
    });
  });
}
