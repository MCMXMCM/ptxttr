import {
  fetchWithSession,
  normalizedPubkey,
  shouldSyncViewerPrefLocation,
  stripViewerPrefSearchParams,
} from "./session.js";
import { initMutations, viewerHasAtLeastOneFollow } from "./mutations.js";
import { activeSignerState } from "./signer.js";
import {
  BLOSSOM_DEFAULT_SERVER_URLS,
  getBlossomPresetIdForURLs,
  getBlossomServerURLs,
  normalizeBlossomBaseUrl,
  resetBlossomServerURLsToDefaults,
  setBlossomPreset,
  setBlossomServerURLs,
  WEB_OF_TRUST_SEED_PRESETS,
  getEffectiveLoggedOutWebOfTrustSeed,
  getImageModePref,
  getWebOfTrustDepthPref,
  getWebOfTrustEnabledPref,
  getWebOfTrustSeedPref,
  normalizeWebOfTrustDepth,
  setFeedSortPref,
  setImageModePref,
  setReadsSortPref,
  setReadsTrendingTimeframePref,
  setTrendingTimeframePref,
  setWebOfTrustDepthPref,
  setWebOfTrustEnabledPref,
  setWebOfTrustSeedPref,
} from "./sort-prefs.js";

let initialized = false;
let mobileMenuEscapeBound = false;
let mobileAppNavHeightBound = false;
let blossomVisibilityBound = false;

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
  const header = document.querySelector("#thread-summary > .thread-header");
  if (!(slot instanceof HTMLElement)) return;
  if (!mobileFeedShellNarrow() || !(header instanceof HTMLElement)) {
    slot.style.height = "";
    return;
  }
  slot.style.height = `${Math.max(0, Math.ceil(header.getBoundingClientRect().height))}px`;
}

function bindMobileAppNavHeight() {
  if (mobileAppNavHeightBound) return;
  mobileAppNavHeightBound = true;
  let observedThreadToolbar = null;
  let rafId = 0;
  const bar = document.querySelector("#app-main .mobile-bar");
  let ro = null;
  const schedule = () => {
    if (rafId) return;
    rafId = requestAnimationFrame(() => {
      rafId = 0;
      syncMobileAppNavHeight();
      const th = document.querySelector("#thread-summary > .thread-header");
      if (!ro) return;
      if (!(th instanceof HTMLElement)) {
        if (observedThreadToolbar) {
          ro.unobserve(observedThreadToolbar);
          observedThreadToolbar = null;
        }
        return;
      }
      if (observedThreadToolbar !== th) {
        if (observedThreadToolbar) ro.unobserve(observedThreadToolbar);
        ro.observe(th);
        observedThreadToolbar = th;
      }
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
  if (!(img instanceof HTMLImageElement) || img.dataset.ptxtAvatarFallback) return;
  img.dataset.ptxtAvatarFallback = "1";
  let failed = false;
  const run = () => {
    if (failed) return;
    failed = true;
    onFail();
  };
  img.addEventListener("error", run, { once: true });
  if (img.complete && img.naturalWidth === 0 && img.currentSrc) run();
}

/** Remove broken avatar images; thread rail uses an explicit @ span instead of :has() CSS. */
export function wireAvatarImageFallbacks(root = document) {
  root.querySelectorAll(
    ".note-feed-avatar img, .comment-avatar img, .note-avatar img, a.thread-person > img",
  ).forEach((img) => {
    const onFail =
      img.closest("a.thread-person") != null
        ? () => {
            const span = document.createElement("span");
            span.className = "thread-person-avatar-fallback";
            span.setAttribute("aria-hidden", "true");
            span.textContent = "@";
            img.replaceWith(span);
          }
        : () => img.remove();
    bindAvatarImgOnce(img, onFail);
  });
  root.querySelectorAll("img.thread-tree-avatar").forEach((img) => {
    bindAvatarImgOnce(img, () => {
      const row = img.parentElement;
      img.remove();
      if (!row) return;
      const span = document.createElement("span");
      span.className = "thread-tree-avatar thread-tree-avatar-fallback";
      span.setAttribute("aria-hidden", "true");
      span.textContent = "@";
      row.insertBefore(span, row.firstChild);
    });
  });
  root.querySelectorAll(".profile-avatar-wrap > img.profile-avatar").forEach((img) => {
    bindAvatarImgOnce(img, () => {
      const div = document.createElement("div");
      div.className = "profile-avatar-fallback";
      div.setAttribute("aria-hidden", "true");
      div.textContent = "@";
      img.replaceWith(div);
    });
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
  wireAvatarImageFallbacks(root);
  bindMobileMenu(root);
  bindMobileAppNavHeight();
  initMutations(root);
  bindTrendingTimeframe(root);
  bindFeedSortSelect(root);
  bindReadsSortSelect(root);
  bindReadsTrendingTimeframe(root);
  bindImageModeToggle(root);
  syncBlossomSettingsVisibility(root);
  bindBlossomSettings(root);
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

function bindTrendingTimeframe(root) {
  const select = root.querySelector("[data-trending-timeframe]");
  const target = root.querySelector("[data-trending-target]");
  if (!select || !target || select._ptxtTrendingBound) return;
  select._ptxtTrendingBound = true;
  select.addEventListener("change", async () => {
    const tf = select.value || "24h";
    setTrendingTimeframePref(tf);
    try {
      // X-Ptxt-Tf header (sessionHeaders) carries the new timeframe.
      const response = await fetchWithSession(`/trending?fragment=1`);
      if (!response.ok) throw new Error("trending request failed");
      target.innerHTML = await response.text();
    } catch {
      target.innerHTML = `<p class="muted">Trending unavailable.</p>`;
    }
  });
}

/**
 * Bind a select that persists a preference and triggers an in-place refresh
 * of the current route. Because the preference now travels as an X-Ptxt-*
 * request header (not a URL query param), we re-navigate to the same URL
 * with cursors cleared so the SPA refetches with the new header.
 */
function bindNavigatingSelect(root, { selector, boundFlag, defaultValue, persist }) {
  const select = root.querySelector(selector);
  if (!select || select[boundFlag]) return;
  select[boundFlag] = true;
  select.addEventListener("change", () => {
    const value = select.value || defaultValue;
    persist(value);
    refreshCurrentRouteForPrefChange();
  });
}

function bindFeedSortSelect(root) {
  bindNavigatingSelect(root, {
    selector: "[data-feed-sort-select]",
    boundFlag: "_ptxtFeedSortBound",
    defaultValue: "recent",
    persist: setFeedSortPref,
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
 * Asks the SPA to re-run the current route's hydration pipeline so the new
 * `X-Ptxt-*` header values (read from localStorage by fetchWithSession()) are
 * applied. The handler lives in navigation.js, which listens for
 * `ptxt:viewer-prefs-changed`.
 */
function refreshCurrentRouteForPrefChange() {
  window.dispatchEvent(new CustomEvent("ptxt:viewer-prefs-changed"));
}


function syncStoredWebOfTrustAwareLinks(root = document) {
  // The original implementation copied WoT + relay params onto SSR-rendered
  // navigation links so SPA fetches keyed cleanly off the URL. After moving
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
  if (next !== current) history.replaceState({}, "", next);
}

function syncBlossomSettingsVisibility(root) {
  const section = root.querySelector("[data-blossom-settings-section]");
  if (!section) return;
  const signer = activeSignerState();
  section.hidden = !(signer.isLoggedIn && signer.canSign);
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
  const toggle = root.querySelector("[data-image-mode-toggle]");
  if (!toggle || toggle._ptxtImageModeBound) return;
  toggle._ptxtImageModeBound = true;
  const modeButtons = Array.from(root.querySelectorAll("[data-image-mode-set]"));
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
  const currentURL = new URL(window.location.href);
  const toggle = settingsRoot.querySelector("[data-wot-toggle]");
  const depthSelect = settingsRoot.querySelector("[data-wot-depth]");
  const output = settingsRoot.querySelector("[data-wot-depth-label]");
  const note = settingsRoot.querySelector("[data-wot-eligibility-note]");
  const seedRow = settingsRoot.querySelector("[data-wot-seed-row]");
  const seedGroup = settingsRoot.querySelector("[data-wot-seed-group]");
  const switchGroup = settingsRoot.querySelector(".settings-mode-switch[aria-label='Web of Trust toggle']");
  if ((!toggle && !depthSelect) || settingsRoot._ptxtWotSettingsBound) return;
  settingsRoot._ptxtWotSettingsBound = true;
  const modeButtons = Array.from(settingsRoot.querySelectorAll("[data-wot-set]"));
  const currentSeedFromURL = String(currentURL.searchParams.get("seed_pubkey") || "").trim();
  const currentDepthFromURL = currentURL.searchParams.get("wot_depth");
  const currentEnabledFromURL = currentURL.searchParams.get("wot") === "1";
  const currentHasWOTParams = currentURL.searchParams.has("wot") || currentURL.searchParams.has("wot_depth") || currentURL.searchParams.has("seed_pubkey");
  const presetsByID = new Map(WEB_OF_TRUST_SEED_PRESETS.map((preset) => [preset.id, preset]));
  const presetIDByValue = new Map(WEB_OF_TRUST_SEED_PRESETS.map((preset) => [preset.value.toLowerCase(), preset.id]));
  const noneSeedID = "none";
  const radioName = "settings-wot-seed-profile";
  const selectSeedRadio = (seedID) => {
    if (!seedGroup) return;
    const next = seedGroup.querySelector(`[data-wot-seed-radio="${seedID}"]`);
    if (next instanceof HTMLInputElement) next.checked = true;
  };
  const seedForSelectedRadio = () => {
    const selected = seedGroup?.querySelector(`[name="${radioName}"]:checked`);
    if (!(selected instanceof HTMLInputElement)) return "";
    if (selected.value === noneSeedID) return "";
    return presetsByID.get(selected.value)?.value || "";
  };
  const resolvePresetID = (seed) => {
    const normalized = String(seed || "").trim().toLowerCase();
    if (!normalized) return WEB_OF_TRUST_SEED_PRESETS[0]?.id || "";
    return presetIDByValue.get(normalized) || (WEB_OF_TRUST_SEED_PRESETS[0]?.id || "");
  };
  const effectiveLoggedOutSeed = () => {
    const stored = getWebOfTrustSeedPref();
    if (stored) return stored;
    if (currentSeedFromURL) return currentSeedFromURL;
    return getEffectiveLoggedOutWebOfTrustSeed();
  };
  const renderSeedRadios = () => {
    if (!seedGroup || seedGroup.dataset.wotSeedBound === "1") return;
    const cards = [
      `<label class="settings-wot-seed-card">
        <input type="radio" name="${radioName}" value="${noneSeedID}" data-wot-seed-radio="${noneSeedID}">
        <span class="settings-wot-seed-meta">
          <strong>None</strong>
          <small class="muted">WOT off. No seed profile is used.</small>
        </span>
      </label>`,
      ...WEB_OF_TRUST_SEED_PRESETS.map((preset) => `
      <label class="settings-wot-seed-card">
        <input type="radio" name="${radioName}" value="${preset.id}" data-wot-seed-radio="${preset.id}">
        <span class="settings-wot-seed-avatar-wrap" aria-hidden="true">
          <img class="settings-wot-seed-avatar" src="/avatar/${preset.value}" alt="">
        </span>
        <span class="settings-wot-seed-meta">
          <strong>${preset.label}</strong>
          <small class="muted">${preset.bio || ""}</small>
        </span>
      </label>`),
    ];
    seedGroup.innerHTML = cards.join("");
    seedGroup.dataset.wotSeedBound = "1";
  };
  const syncSeedControlsFromPref = (enabled) => {
    renderSeedRadios();
    if (!seedGroup) return;
    const radios = Array.from(seedGroup.querySelectorAll(`[name="${radioName}"]`));
    const chooseNone = !enabled;
    if (chooseNone) {
      selectSeedRadio(noneSeedID);
    } else {
      const seed = effectiveLoggedOutSeed();
      const presetID = resolvePresetID(seed);
      selectSeedRadio(presetID || (WEB_OF_TRUST_SEED_PRESETS[0]?.id || noneSeedID));
      if (!getWebOfTrustSeedPref()) {
        const selectedSeed = seedForSelectedRadio() || seed;
        if (selectedSeed) setWebOfTrustSeedPref(selectedSeed);
      }
    }
    radios.forEach((radio) => {
      const disabled = !enabled || radio.value === noneSeedID;
      radio.disabled = disabled;
    });
  };
  const syncLoggedOutSeedState = (enabled) => {
    syncSeedControlsFromPref(enabled);
    if (!enabled) return;
    const seed = effectiveLoggedOutSeed();
    const presetID = resolvePresetID(seed);
    selectSeedRadio(presetID);
  };
  const setEligible = (eligible, options = {}) => {
    const {
      showEligibilityNote = false,
      showSeed = false,
    } = options;
    if (switchGroup) switchGroup.classList.toggle("is-disabled", !eligible);
    if (note) note.hidden = !showEligibilityNote;
    if (seedRow) seedRow.hidden = !showSeed;
    modeButtons.forEach((button) => {
      button.disabled = !eligible;
      button.setAttribute("aria-disabled", eligible ? "false" : "true");
      if (eligible) button.removeAttribute("tabindex");
      else button.tabIndex = -1;
    });
    if (toggle) toggle.disabled = !eligible;
    if (depthSelect) depthSelect.disabled = !eligible;
    if (!seedGroup) return;
    seedGroup.querySelectorAll(`[name="${radioName}"]`).forEach((radio) => {
      if (!(radio instanceof HTMLInputElement)) return;
      radio.disabled = !eligible || !showSeed || radio.value === noneSeedID;
    });
  };
  const syncButtons = (enabled) => {
    modeButtons.forEach((button) => {
      const isOn = button.dataset.wotSet === "on";
      const active = enabled ? isOn : !isOn;
      button.classList.toggle("is-active", active);
      button.setAttribute("aria-pressed", active ? "true" : "false");
    });
  };
  const syncDepth = (depth) => {
    const next = `${normalizeWebOfTrustDepth(depth)}`;
    if (depthSelect) depthSelect.value = next;
    if (output) output.textContent = next;
  };
  const apply = ({ enabled, depth, announce = true, persist = true }) => {
    const nextEnabled = Boolean(enabled);
    const nextDepth = normalizeWebOfTrustDepth(depth);
    if (toggle) toggle.checked = nextEnabled;
    if (persist) {
      setWebOfTrustEnabledPref(nextEnabled);
      setWebOfTrustDepthPref(nextDepth);
    }
    if (!normalizedPubkey()) {
      syncLoggedOutSeedState(nextEnabled);
    }
    syncStoredWebOfTrustAwareLinks(document);
    if (window.location.pathname === "/settings") {
      syncLocationFromStoredPrefs();
    }
    syncButtons(nextEnabled);
    syncDepth(nextDepth);
    if (announce) {
      window.dispatchEvent(new CustomEvent("ptxt:web-of-trust-changed", {
        detail: { enabled: nextEnabled, depth: nextDepth, seedPubkey: getWebOfTrustSeedPref() || getEffectiveLoggedOutWebOfTrustSeed() },
      }));
    }
  };
  const initialDepth = getWebOfTrustDepthPref();
  setEligible(false, { showEligibilityNote: false, showSeed: false });
  apply({ enabled: false, depth: initialDepth, announce: false, persist: false });
  const viewer = normalizedPubkey();
  if (!viewer) {
    if (currentHasWOTParams) {
      setWebOfTrustEnabledPref(currentEnabledFromURL);
      if (currentDepthFromURL) setWebOfTrustDepthPref(currentDepthFromURL);
      if (currentSeedFromURL) setWebOfTrustSeedPref(currentSeedFromURL);
    }
    setEligible(true, { showSeed: true });
    apply({ enabled: getWebOfTrustEnabledPref(), depth: getWebOfTrustDepthPref(), announce: false });
  } else {
    void viewerHasAtLeastOneFollow(viewer).then((hasFollows) => {
      if (!hasFollows) {
        setWebOfTrustEnabledPref(false);
        setEligible(false, { showEligibilityNote: true, showSeed: false });
        return;
      }
      setEligible(true, { showEligibilityNote: false, showSeed: false });
      apply({ enabled: getWebOfTrustEnabledPref(), depth: getWebOfTrustDepthPref(), announce: false });
    });
  }
  modeButtons.forEach((button) => {
    button.addEventListener("click", () => {
      apply({ enabled: button.dataset.wotSet === "on", depth: getWebOfTrustDepthPref() });
    });
  });
  toggle?.addEventListener("change", () => {
    apply({ enabled: Boolean(toggle.checked), depth: getWebOfTrustDepthPref() });
  });
  depthSelect?.addEventListener("change", () => {
    apply({ enabled: getWebOfTrustEnabledPref(), depth: depthSelect.value });
  });
  seedGroup?.addEventListener("change", (event) => {
    const radio = event.target;
    if (!(radio instanceof HTMLInputElement) || radio.name !== radioName) return;
    if (!getWebOfTrustEnabledPref()) {
      selectSeedRadio(noneSeedID);
      syncStoredWebOfTrustAwareLinks(document);
      if (window.location.pathname === "/settings") syncLocationFromStoredPrefs();
      return;
    }
    const selected = presetsByID.get(radio.value);
    if (!selected) return;
    setWebOfTrustSeedPref(selected.value);
    syncStoredWebOfTrustAwareLinks(document);
    if (window.location.pathname === "/settings") syncLocationFromStoredPrefs();
    window.dispatchEvent(new CustomEvent("ptxt:web-of-trust-changed", {
      detail: {
        enabled: getWebOfTrustEnabledPref(),
        depth: getWebOfTrustDepthPref(),
        seedPubkey: selected.value,
      },
    }));
  });
}

function bindFeedWebOfTrustControls(root) {
  const control = root.querySelector("[data-feed-wot-controls]");
  if (!control || control._ptxtFeedWotBound) return;
  control._ptxtFeedWotBound = true;
  const depthSelect = control.querySelector("[data-feed-wot-depth-select]");
  if (!depthSelect) return;
  const syncSelect = (depth) => {
    const nextDepth = `${normalizeWebOfTrustDepth(depth)}`;
    control.dataset.wotDepth = nextDepth;
    depthSelect.value = nextDepth;
  };
  const applyDepth = (depth) => {
    const nextDepth = normalizeWebOfTrustDepth(depth);
    setWebOfTrustEnabledPref(true);
    setWebOfTrustDepthPref(nextDepth);
    syncSelect(nextDepth);
    window.dispatchEvent(new CustomEvent("ptxt:web-of-trust-changed", {
      detail: { enabled: true, depth: nextDepth },
    }));
  };
  syncSelect(control.dataset.wotDepth || getWebOfTrustDepthPref());
  depthSelect.addEventListener("change", () => {
    applyDepth(depthSelect.value);
  });
}

if (!initialized) {
  initialized = true;
  initLayoutUI(document);
}
