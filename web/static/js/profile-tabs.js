import { closeActionMenus } from "./ascii.js";

const desktopProfileMedia = window.matchMedia("(min-width: 701px)");

const NPUB_COPY_TOAST_MS = 2200;

const npubCopyToastTimers = new WeakMap();

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

function bindProfileStatsMenus(root = document) {
  root.querySelectorAll(".profile-stats-menu").forEach((wrap) => {
    const trigger = wrap.querySelector(".profile-stats-menu-trigger");
    if (!trigger || trigger.dataset.boundStatsMenu === "1") return;
    trigger.dataset.boundStatsMenu = "1";
    trigger.addEventListener("click", (event) => {
      event.stopPropagation();
      const isOpen = wrap.classList.toggle("is-open");
      closeActionMenus(isOpen ? wrap : null);
      trigger.setAttribute("aria-expanded", isOpen ? "true" : "false");
    });
  });
}

export function bindProfileStatLinks(root = document) {
  bindProfileNpubCopy(root);
  bindProfileStatsMenus(root);
  const links = root.querySelectorAll("[data-profile-tab]");
  links.forEach((link) => {
    if (link.dataset.bound === "1") return;
    link.dataset.bound = "1";
    link.addEventListener("click", (event) => {
      const tabID = link.getAttribute("data-profile-tab") || "";
      if (tabID === "user-tab-relays" && desktopProfileMedia.matches) {
        event.preventDefault();
        closeActionMenus();
        document.querySelector("#user-right-relays")?.scrollIntoView({ block: "nearest", behavior: "smooth" });
        return;
      }
      const input = tabID ? root.querySelector(`#${tabID}`) : null;
      if (!(input instanceof HTMLInputElement)) return;
      event.preventDefault();
      input.checked = true;
      input.dispatchEvent(new Event("change", { bubbles: true }));
      closeActionMenus();
      const panelID = tabID.replace("tab", "panel");
      const panel = root.querySelector(`#${panelID}`);
      if (panel) panel.scrollIntoView({ block: "start", behavior: "smooth" });
    });
  });
}
