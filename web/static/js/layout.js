import {
  fetchWithSession,
  normalizedPubkey,
  shouldSyncViewerPrefLocation,
  stripViewerPrefSearchParams,
} from "./session.js";
import {
  BLOSSOM_DEFAULT_SERVER_URLS,
  getBlossomPresetIdForURLs,
  getBlossomServerURLs,
  normalizeBlossomBaseUrl,
  resetBlossomServerURLsToDefaults,
  setBlossomPreset,
  setBlossomServerURLs,
  getEffectiveLoggedOutWebOfTrustSeed,
  getImageModePref,
  getWebOfTrustDepthPref,
  getWebOfTrustEnabledPref,
  ensureDefaultViewerPrefs,
  normalizeWebOfTrustDepth,
  setFeedSortPref,
  setImageModePref,
  setReadsSortPref,
  setReadsTrendingTimeframePref,
  setTrendingTimeframePref,
  setWebOfTrustDepthPref,
  setWebOfTrustEnabledPref,
} from "./sort-prefs.js";
import { setAvatarImageSource } from "./avatar-cache.js";
import { initRetroLoaders } from "./retro-loader.js";
import { initDesktopStorage } from "./desktop-storage.js";
import { desktopModeEnabled } from "./viewer-defaults.js";

let initialized = false;
let mobileMenuEscapeBound = false;
let mobileAppNavHeightBound = false;
let blossomVisibilityBound = false;
let feedWotControlsVisibilityBound = false;

/** Matches `app.css` @media (max-width: 700px) feed-shell layout. */
const mobileShellLayoutQuery = window.matchMedia("(max-width: 700px)");

function mobileFeedShellNarrow() {
  return document.body.classList.contains("feed-shell") && mobileShellLayoutQuery.matches;
}

/** Sets `--mobile-app-nav-height` from `.mobile-bar` and resizes the thread toolbar spacer (narrow feed-shell only). */
export function syncMobileAppNavHeight() {
  let navPx = "";
  if (mobileFeedShellNarrow()) {
    const bar = document.querySelector("#app-main .mobile-bar");
    if (bar) {
      navPx = `${Math.max(1, Math.ceil(bar.getBoundingClientRect().bottom))}px`;
    }
  }
  if (navPx) {
    document.body.style.setProperty("--mobile-app-nav-height", navPx);
  } else {
    document.body.style.removeProperty("--mobile-app-nav-height");
  }
  syncThreadToolbarSlot();
}

function syncThreadToolbarSlot() {
  const slot = document.querySelector("#thread-summary > .thread-toolbar-slot");
  if (slot instanceof HTMLElement) slot.style.height = "";
}

function bindMobileAppNavHeight() {
  if (mobileAppNavHeightBound) return;
  mobileAppNavHeightBound = true;
  let rafId = 0;
  const bar = document.querySelector("#app-main .mobile-bar");
  let ro = null;
  const schedule = () => {
    if (rafId) return;
    rafId = requestAnimationFrame(() => {
      rafId = 0;
      syncMobileAppNavHeight();
    });
  };
  ro = typeof ResizeObserver !== "undefined" ? new ResizeObserver(schedule) : null;
  mobileShellLayoutQuery.addEventListener("change", schedule);
  window.addEventListener("resize", schedule);
  window.addEventListener("orientationchange", schedule);
  window.visualViewport?.addEventListener("resize", schedule);
  if (bar && ro) ro.observe(bar);
  schedule();
}

function bindAvatarImgOnce(img, onFail) {
  if (!(img instanceof HTMLImageElement)) return;
  const src = img.dataset.ptxtAvatarOriginalSrc || img.currentSrc || img.getAttribute("src") || "";
  if (img.dataset.ptxtAvatarFallback === "1" && img.dataset.ptxtAvatarFallbackSrc === src) return;
  img.dataset.ptxtAvatarFallback = "1";
  img.dataset.ptxtAvatarFallbackSrc = src;
  let failed = false;
  const expectedSrc = src;
  const run = () => {
    if (failed) return;
    const currentSrc = img.dataset.ptxtAvatarOriginalSrc || img.currentSrc || img.getAttribute("src") || "";
    if (expectedSrc && currentSrc && currentSrc !== expectedSrc) return;
    failed = true;
    onFail();
  };
  img.addEventListener("error", run, { once: true });
  if (img.complete && img.naturalWidth === 0 && img.currentSrc) run();
}

function retryAvatarImageFallback(img, onFail) {
  if (!(img instanceof HTMLImageElement)) return false;
  const retryURL = String(img.dataset.ptxtAvatarRetrySrc || "").trim();
  if (!retryURL || img.dataset.ptxtAvatarRetryAttempted === "1") return false;
  img.dataset.ptxtAvatarRetryAttempted = "1";
  delete img.dataset.ptxtAvatarFallback;
  delete img.dataset.ptxtAvatarFallbackSrc;
  setAvatarImageSource(img, retryURL, { retryURL: "" });
  bindAvatarImgOnce(img, onFail);
  return true;
}

export function normalizeThreadPersonAvatars(root = document) {
  const links = [];
  if (root instanceof Element && root.matches("a.thread-person")) links.push(root);
  root.querySelectorAll?.("a.thread-person").forEach((link) => links.push(link));
  links.forEach((link) => {
    const avatarNodes = Array.from(link.children).filter(
      (child) => child.matches("img, .thread-person-avatar-fallback"),
    );
    if (!avatarNodes.length) return;
    const keeper = avatarNodes.find((node) => node instanceof HTMLImageElement) || avatarNodes[0];
    avatarNodes.forEach((node) => {
      if (node !== keeper) node.remove();
    });
    if (keeper !== link.firstElementChild) link.insertBefore(keeper, link.firstChild);
  });
}

/** Remove broken avatar images; thread rail uses an explicit @ span instead of :has() CSS. */
export function wireAvatarImageFallbacks(root = document) {
  normalizeThreadPersonAvatars(root);
  root.querySelectorAll(
    ".note-feed-avatar img, .comment-avatar img, .note-avatar img, a.thread-person > img",
  ).forEach((img) => {
    setAvatarImageSource(img, img.dataset.ptxtAvatarOriginalSrc || img.getAttribute("src"));
    const onFail =
      img.closest("a.thread-person") != null
        ? () => {
            if (retryAvatarImageFallback(img, onFail)) return;
            const link = img.closest("a.thread-person");
            const span = document.createElement("span");
            span.className = "thread-person-avatar-fallback";
            span.setAttribute("aria-hidden", "true");
            span.textContent = "@";
            img.replaceWith(span);
            if (link) normalizeThreadPersonAvatars(link);
          }
        : () => {
            if (retryAvatarImageFallback(img, onFail)) return;
            img.remove();
          };
    bindAvatarImgOnce(img, onFail);
  });
  root.querySelectorAll("img.thread-tree-avatar").forEach((img) => {
    setAvatarImageSource(img, img.dataset.ptxtAvatarOriginalSrc || img.getAttribute("src"));
    const onFail = () => {
      if (retryAvatarImageFallback(img, onFail)) return;
      const row = img.parentElement;
      img.remove();
      if (!row) return;
      const span = document.createElement("span");
      span.className = "thread-tree-avatar thread-tree-avatar-fallback";
      span.setAttribute("aria-hidden", "true");
      span.textContent = "@";
      row.insertBefore(span, row.firstChild);
    };
    bindAvatarImgOnce(img, onFail);
  });
  root.querySelectorAll(".profile-avatar-wrap > img.profile-avatar").forEach((img) => {
    setAvatarImageSource(img, img.dataset.ptxtAvatarOriginalSrc || img.getAttribute("src"));
    const onFail = () => {
      if (retryAvatarImageFallback(img, onFail)) return;
      const div = document.createElement("div");
      div.className = "profile-avatar-fallback";
      div.setAttribute("aria-hidden", "true");
      div.textContent = "@";
      img.replaceWith(div);
    };
    bindAvatarImgOnce(img, onFail);
  });
  root.querySelectorAll(".profile-follow-avatar > img.profile-follow-avatar-image").forEach((img) => {
    setAvatarImageSource(img, img.dataset.ptxtAvatarOriginalSrc || img.getAttribute("src"));
    const onFail = () => {
      if (retryAvatarImageFallback(img, onFail)) return;
      const span = document.createElement("span");
      span.className = "profile-follow-avatar-fallback";
      span.setAttribute("aria-hidden", "true");
      span.textContent = "@";
      img.replaceWith(span);
    };
    bindAvatarImgOnce(img, onFail);
  });
}

function setMobileBarGlyphForMenuOpen(root, open) {
  if (!root) return;
  const label = open ? "Close menu" : "Open menu";
  root.querySelectorAll(".mobile-menu-bar-glyph").forEach((btn) => {
    btn.setAttribute("aria-label", label);
  });
}

/**
 * Closes the open mobile menu under root and clears body scroll lock.
 * Used after in-app navigation so the overlay never dismisses to the
 * previous route before the next shell is ready (and so body state is
 * correct when the shell is not replaced, e.g. feed restore).
 */
export function dismissOpenMobileMenuForNavigation(root) {
  if (!root) return;
  document.body.classList.remove("mobile-menu-open");
  const menu = root.querySelector("[data-mobile-menu].is-open");
  if (menu) {
    menu.classList.remove("is-open");
    menu.setAttribute("aria-hidden", "true");
    menu.hidden = true;
    delete menu._ptxtLastOpenTrigger;
  }
  root.querySelectorAll("[data-mobile-menu-trigger]").forEach((t) => {
    t.setAttribute("aria-expanded", "false");
  });
  setMobileBarGlyphForMenuOpen(root, false);
}

function ensureMobileMenuEscapeDelegate() {
  if (mobileMenuEscapeBound) return;
  mobileMenuEscapeBound = true;
  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || !document.body.classList.contains("mobile-menu-open")) return;
    const navRoot = document.querySelector("[data-nav-root]");
    const menu = navRoot?.querySelector("[data-mobile-menu].is-open");
    if (!menu) return;
    event.preventDefault();
    const triggers = navRoot?.querySelectorAll("[data-mobile-menu-trigger]") ?? [];
    const focusTarget =
      (menu._ptxtLastOpenTrigger && document.contains(menu._ptxtLastOpenTrigger)
        ? menu._ptxtLastOpenTrigger
        : triggers[0]) ?? null;
    menu.classList.remove("is-open");
    menu.setAttribute("aria-hidden", "true");
    menu.hidden = true;
    delete menu._ptxtLastOpenTrigger;
    triggers.forEach((t) => t.setAttribute("aria-expanded", "false"));
    document.body.classList.remove("mobile-menu-open");
    setMobileBarGlyphForMenuOpen(navRoot, false);
    queueMicrotask(() => {
      if (!focusTarget || !document.contains(focusTarget)) return;
      try {
        focusTarget.focus({ preventScroll: true });
      } catch {
        focusTarget.focus();
      }
    });
  });
}

export function initLayoutUI(root = document) {
  initDesktopStorage(root);
  initRetroLoaders(root);
  wireAvatarImageFallbacks(root);
  bindMobileMenu(root);
  bindMobileAppNavHeight();
	if (normalizedPubkey()) {
		void import("./mutations.js").then(({ initMutations }) => initMutations(root));
	}
  bindTrendingTimeframe(root);
  bindFeedSortSelect(root);
  bindReadsSortSelect(root);
  bindReadsTrendingTimeframe(root);
  bindImageModeToggle(root);
  syncBlossomSettingsVisibility(root);
  bindBlossomSettings(root);
  void maybeHydrateTrendingSidebar(root);
  if (!blossomVisibilityBound) {
    blossomVisibilityBound = true;
    window.addEventListener("ptxt:session", () => syncBlossomSettingsVisibility(document));
  }
  bindWebOfTrustControls(root);
  bindFeedWebOfTrustControls(root);
  syncStoredWebOfTrustAwareLinks(root);
  if (shouldSyncViewerPrefLocation(window.location.pathname)) {
    syncLocationFromStoredPrefs();
  }
}

function bindMobileMenu(root) {
  const triggers = Array.from(root.querySelectorAll("[data-mobile-menu-trigger]"));
  const menu = root.querySelector("[data-mobile-menu]");
  if (!triggers.length || !menu || menu._ptxtMobileMenuBound) return;
  menu._ptxtMobileMenuBound = true;
  const backdrop = menu.querySelector("[data-mobile-menu-backdrop]");
  const closeButton = menu.querySelector("[data-mobile-menu-close]");

  let lastOpenTrigger = null;

  const setExpanded = (open) => {
    const v = open ? "true" : "false";
    triggers.forEach((t) => t.setAttribute("aria-expanded", v));
  };

  const openMenu = (fromTrigger) => {
    if (menu.classList.contains("is-open")) return;
    if (fromTrigger) {
      lastOpenTrigger = fromTrigger;
      menu._ptxtLastOpenTrigger = fromTrigger;
    } else if (!lastOpenTrigger) {
      [lastOpenTrigger] = triggers;
      if (lastOpenTrigger) menu._ptxtLastOpenTrigger = lastOpenTrigger;
    }
    ensureMobileMenuEscapeDelegate();
    menu.hidden = false;
    menu.classList.add("is-open");
    menu.setAttribute("aria-hidden", "false");
    setExpanded(true);
    document.body.classList.add("mobile-menu-open");
    setMobileBarGlyphForMenuOpen(root, true);
  };

  const closeMenu = (focusTrigger) => {
    if (!menu.classList.contains("is-open")) return;
    menu.classList.remove("is-open");
    menu.setAttribute("aria-hidden", "true");
    setExpanded(false);
    document.body.classList.remove("mobile-menu-open");
    setMobileBarGlyphForMenuOpen(root, false);
    menu.hidden = true;
    const focusTarget =
      (focusTrigger && document.contains(focusTrigger)
        ? focusTrigger
        : lastOpenTrigger && document.contains(lastOpenTrigger)
          ? lastOpenTrigger
          : triggers[0]) ?? null;
    delete menu._ptxtLastOpenTrigger;
    queueMicrotask(() => {
      if (!focusTarget || !document.contains(focusTarget)) return;
      try {
        focusTarget.focus({ preventScroll: true });
      } catch {
        focusTarget.focus();
      }
    });
  };

  triggers.forEach((trigger) => {
    trigger.setAttribute("aria-expanded", "false");
    trigger.addEventListener("click", (event) => {
      const el = event.currentTarget;
      if (menu.classList.contains("is-open")) {
        closeMenu(el);
        return;
      }
      openMenu(el);
    });
  });
  backdrop?.addEventListener("click", closeMenu);
  closeButton?.addEventListener("click", closeMenu);
  // Intentionally do not close on nav link click: the document navigation
  // handler must run first (in-app) or the page will unload (full load)
  // with the overlay still up, avoiding a flash of the previous screen.
}

async function maybeHydrateTrendingSidebar(root) {
	if (document.body?.dataset?.guestV2) return;
  const target = root.querySelector("[data-trending-target]");
  if (!target) return;
  const { directRelaysEnabled } = await import("./relay-config.js");
  if (!directRelaysEnabled()) return;
  if (target.querySelector(".trending-list")) return;
  const { hydrateTrendingSidebar } = await import("./trending-render.js");
  void hydrateTrendingSidebar(root);
}

function bindTrendingTimeframe(root) {
  const select = root.querySelector("[data-trending-timeframe]");
  const target = root.querySelector("[data-trending-target]");
  if (!select || !target || select._ptxtTrendingBound) return;
  select._ptxtTrendingBound = true;
  select.addEventListener("change", async () => {
    const tf = select.value || "24h";
    setTrendingTimeframePref(tf);
    target.replaceChildren();
    const { hydrateTrendingSidebar } = await import("./trending-render.js");
    const { trendingSortFromTimeframe } = await import("./trending-service.js");
    void hydrateTrendingSidebar(root, { sort: trendingSortFromTimeframe(tf), force: true });
  });
}

/**
 * Bind a select that persists a preference and triggers an in-place refresh
 * of the current route. Because the preference now travels as an X-Ptxt-*
 * request header (not a URL query param), we re-navigate to the same URL
 * with cursors cleared so client hydration refetches with the new header.
 */
function bindNavigatingSelect(root, { selector, boundFlag, defaultValue, persist, beforeRefresh = null }) {
  const select = root.querySelector(selector);
  if (!select || select[boundFlag]) return;
  select[boundFlag] = true;
  select.addEventListener("change", () => {
    const value = select.value || defaultValue;
    persist(value);
    if (typeof beforeRefresh === "function") beforeRefresh(value);
    refreshCurrentRouteForPrefChange();
  });
}

function bindFeedSortSelect(root) {
  bindNavigatingSelect(root, {
    selector: "[data-feed-sort-select]",
    boundFlag: "_ptxtFeedSortBound",
    defaultValue: "recent",
    persist: setFeedSortPref,
    beforeRefresh: () => {
		void import("./feed-refresh-loader.js").then(({ showHomeFeedRefreshLoader }) => showHomeFeedRefreshLoader(root, {
			percent: 10,
			statusMessage: "reordering feed...",
		}));
    },
  });
}

function bindReadsSortSelect(root) {
  bindNavigatingSelect(root, {
    selector: "[data-reads-sort-select]",
    boundFlag: "_ptxtReadsSortBound",
    defaultValue: "recent",
    persist: setReadsSortPref,
  });
}

function bindReadsTrendingTimeframe(root) {
  bindNavigatingSelect(root, {
    selector: "[data-reads-trending-timeframe]",
    boundFlag: "_ptxtReadsTfBound",
    defaultValue: "24h",
    persist: setReadsTrendingTimeframePref,
  });
}

/**
 * Asks the client router to re-run the current route's hydration pipeline so the new
 * `X-Ptxt-*` header values (read from localStorage by fetchWithSession()) are
 * applied. The document router listens for `ptxt:viewer-prefs-changed`.
 */
function refreshCurrentRouteForPrefChange() {
  window.dispatchEvent(new CustomEvent("ptxt:viewer-prefs-changed"));
}


function syncStoredWebOfTrustAwareLinks(root = document) {
  // The original implementation copied WoT + relay params onto server-rendered
  // navigation links so route fetches keyed cleanly off the URL. After moving
  // prefs to X-Ptxt-* headers we only need old base hrefs (data-ptxt-wot-base-href)
  // and scrubbing any stale legacy query keys (sort, wot, pubkey, relays, …).
  root.querySelectorAll("[data-feed-home], [data-session-reads-link], [data-session-notifications-link], a[href='/settings'], a[href^='/settings?']").forEach((link) => {
    if (!(link instanceof HTMLAnchorElement)) return;
    const base = link.dataset.ptxtWotBaseHref || link.getAttribute("href") || "/";
    link.dataset.ptxtWotBaseHref = base;
    const url = new URL(base, window.location.origin);
    stripViewerPrefSearchParams(url);
    link.href = `${url.pathname}${url.search}${url.hash}`;
  });
}

/** Strips any stale `?sort=`, `?tf=`, `?reads_tf=`, `?wot=`, `?wot_depth=`,
 *  `?seed_pubkey=`, `?relays=` params from the address bar (those prefs now
 *  travel as X-Ptxt-* headers). Called on feed-like + settings routes so old
 *  bookmarked URLs upgrade quietly. */
export function syncLocationFromStoredPrefs() {
  const url = new URL(window.location.href);
  const current = `${url.pathname}${url.search}${url.hash}`;
  stripViewerPrefSearchParams(url);
  const next = `${url.pathname}${url.search}${url.hash}`;
  if (next !== current) history.replaceState(history.state, "", next);
}

function syncBlossomSettingsVisibility(root) {
	const section = root.querySelector("[data-blossom-settings-section]");
	if (!section) return;
	section.hidden = true;
	if (!normalizedPubkey()) return;
	void import("./signer.js").then(({ activeSignerState }) => {
		const signer = activeSignerState();
		section.hidden = !(signer.isLoggedIn && signer.canSign);
	});
}

function bindBlossomSettings(root) {
  const wrap = root.querySelector("[data-blossom-settings]");
  if (!wrap || wrap._ptxtBlossomBound) return;
  wrap._ptxtBlossomBound = true;
  const radios = Array.from(wrap.querySelectorAll("input[data-blossom-preset]"));
  const customInput = wrap.querySelector("[data-blossom-custom-url]");
  const resetBtn = wrap.querySelector("[data-blossom-reset]");

  const syncFromStorage = () => {
    const urls = getBlossomServerURLs();
    const preset = getBlossomPresetIdForURLs(urls);
    radios.forEach((r) => {
      if (r instanceof HTMLInputElement) {
        r.checked = r.dataset.blossomPreset === preset;
      }
    });
    if (customInput instanceof HTMLInputElement) {
      const isCustom = preset === "custom";
      customInput.disabled = !isCustom;
      if (isCustom) {
        customInput.value = urls[0] || "";
      }
    }
  };

  radios.forEach((r) => {
    r.addEventListener("change", () => {
      if (!(r instanceof HTMLInputElement) || !r.checked) return;
      const id = r.dataset.blossomPreset || "";
      if (id === "custom") {
        if (customInput instanceof HTMLInputElement) {
          customInput.disabled = false;
          const urls = getBlossomServerURLs();
          customInput.value = urls[0] || "";
          customInput.focus();
        }
        return;
      }
      setBlossomPreset(id === "nostr_build" ? "nostr_build" : "primal");
      syncFromStorage();
    });
  });

  customInput?.addEventListener("change", () => {
    if (!(customInput instanceof HTMLInputElement) || customInput.disabled) return;
    const v = normalizeBlossomBaseUrl(customInput.value);
    if (!v) return;
    const rest = BLOSSOM_DEFAULT_SERVER_URLS.filter((x) => x !== v);
    setBlossomServerURLs([v, ...rest]);
    syncFromStorage();
  });

  resetBtn?.addEventListener("click", () => {
    resetBlossomServerURLsToDefaults();
    syncFromStorage();
  });

  syncFromStorage();
}

function bindImageModeToggle(root) {
  const settingsRoot = root.querySelector(".settings-preferences");
  const scope = settingsRoot || root;
  const toggle = scope.querySelector("[data-image-mode-toggle]");
  if (!toggle || toggle._ptxtImageModeBound) return;
  toggle._ptxtImageModeBound = true;
  ensureDefaultViewerPrefs();
  const modeButtons = Array.from(scope.querySelectorAll("[data-image-mode-set]"));
  const syncModeButtons = (enabled) => {
    modeButtons.forEach((button) => {
      const isOn = button.dataset.imageModeSet === "on";
      const isActive = enabled ? isOn : !isOn;
      button.classList.toggle("is-active", isActive);
      button.setAttribute("aria-pressed", isActive ? "true" : "false");
    });
  };
  const applyImageMode = (enabled) => {
    const next = Boolean(enabled);
    toggle.checked = next;
    setImageModePref(next);
    syncModeButtons(next);
    window.dispatchEvent(new CustomEvent("ptxt:image-mode-changed", { detail: { enabled: next } }));
  };
  toggle.checked = getImageModePref();
  syncModeButtons(Boolean(toggle.checked));
  modeButtons.forEach((button) => {
    button.addEventListener("click", () => {
      applyImageMode(button.dataset.imageModeSet === "on");
    });
  });
  toggle.addEventListener("change", () => {
    const enabled = Boolean(toggle.checked);
    setImageModePref(enabled);
    syncModeButtons(enabled);
    window.dispatchEvent(new CustomEvent("ptxt:image-mode-changed", { detail: { enabled } }));
  });
}

function bindWebOfTrustControls(root) {
  const settingsRoot = root.querySelector(".settings-preferences");
  if (!settingsRoot) return;
  const depthSelect = settingsRoot.querySelector("[data-wot-depth]");
  const output = settingsRoot.querySelector("[data-wot-depth-label]");
  const note = settingsRoot.querySelector("[data-wot-eligibility-note]");
  if (!depthSelect || settingsRoot._ptxtWotSettingsBound) return;
  settingsRoot._ptxtWotSettingsBound = true;
  const setEligible = (eligible, options = {}) => {
    const { showEligibilityNote = false } = options;
    if (note) note.hidden = !showEligibilityNote;
    if (depthSelect) depthSelect.disabled = !eligible;
  };
  const syncDepth = (depth) => {
    const next = `${normalizeWebOfTrustDepth(depth)}`;
    if (depthSelect) depthSelect.value = next;
    if (output) output.textContent = `${next}°`;
  };
  const apply = ({ depth, announce = true, persist = true }) => {
    const nextDepth = normalizeWebOfTrustDepth(depth);
    if (persist) {
      setWebOfTrustEnabledPref(true);
      setWebOfTrustDepthPref(nextDepth);
    }
    syncStoredWebOfTrustAwareLinks(document);
    if (window.location.pathname === "/settings") {
      syncLocationFromStoredPrefs();
    }
    syncDepth(nextDepth);
    if (announce) {
      window.dispatchEvent(new CustomEvent("ptxt:web-of-trust-changed", {
        detail: { enabled: true, depth: nextDepth, seedPubkey: getEffectiveLoggedOutWebOfTrustSeed() },
      }));
    }
  };
  ensureDefaultViewerPrefs();
  const syncFromStorage = (announce = false, persist = false) => {
    apply({
      depth: getWebOfTrustDepthPref(),
      announce,
      persist,
    });
  };
  const viewer = normalizedPubkey();
  if (!viewer && !desktopModeEnabled()) {
    syncDepth(1);
    setEligible(false);
  } else if (!viewer) {
    setEligible(true, { showEligibilityNote: false });
  } else {
    setEligible(true, { showEligibilityNote: false });
		void import("./mutations.js").then(({ viewerHasAtLeastOneFollow }) => viewerHasAtLeastOneFollow(viewer)).then((hasFollows) => {
      setEligible(true, {
        showEligibilityNote: !hasFollows,
      });
    });
  }
  syncFromStorage(false, true);
  depthSelect?.addEventListener("change", () => {
    apply({ depth: depthSelect.value });
  });
}

function bindFeedWebOfTrustControls(root) {
  const controls = Array.from(root.querySelectorAll("[data-feed-wot-controls]"));
  if (!controls.length) return;
  if (!normalizedPubkey() && !desktopModeEnabled()) {
    controls.forEach((control) => { control.hidden = true; });
    return;
  }
  const syncControls = (depth = getWebOfTrustDepthPref(), enabled = getWebOfTrustEnabledPref()) => {
    const nextDepth = `${normalizeWebOfTrustDepth(depth)}`;
    controls.forEach((control) => {
      control.hidden = !enabled;
      control.dataset.wotDepth = nextDepth;
      const depthSelect = control.querySelector("[data-feed-wot-depth-select]");
      if (depthSelect) depthSelect.value = nextDepth;
    });
  };
  const applyDepth = (depth) => {
    const nextDepth = normalizeWebOfTrustDepth(depth);
    setWebOfTrustEnabledPref(true);
    setWebOfTrustDepthPref(nextDepth);
    syncControls(nextDepth, true);
		void import("./feed-refresh-loader.js").then(({ showHomeFeedRefreshLoader }) => showHomeFeedRefreshLoader(root, {
			percent: 10,
			statusMessage: "building trust graph...",
		}));
    window.dispatchEvent(new CustomEvent("ptxt:web-of-trust-changed", {
      detail: { enabled: true, depth: nextDepth },
    }));
  };
  // Server-rendered guest HTML starts at the hosted-safe one-hop default.
  // Desktop must restore its device-local selection after every full reload.
  syncControls(getWebOfTrustDepthPref(), getWebOfTrustEnabledPref());
  controls.forEach((control) => {
    if (control._ptxtFeedWotBound) return;
    control._ptxtFeedWotBound = true;
    const depthSelect = control.querySelector("[data-feed-wot-depth-select]");
    if (!depthSelect) return;
    depthSelect.addEventListener("change", () => {
      applyDepth(depthSelect.value);
    });
  });
  if (!feedWotControlsVisibilityBound) {
    feedWotControlsVisibilityBound = true;
    window.addEventListener("ptxt:web-of-trust-changed", (event) => {
      const enabled = event.detail?.enabled ?? getWebOfTrustEnabledPref();
      const depth = event.detail?.depth ?? getWebOfTrustDepthPref();
      document.querySelectorAll("[data-feed-wot-controls]").forEach((control) => {
        control.hidden = !enabled;
        control.dataset.wotDepth = `${normalizeWebOfTrustDepth(depth)}`;
        const depthSelect = control.querySelector("[data-feed-wot-depth-select]");
        if (depthSelect) depthSelect.value = `${normalizeWebOfTrustDepth(depth)}`;
      });
    });
  }
}

if (!initialized) {
  initialized = true;
  initLayoutUI(document);
}
