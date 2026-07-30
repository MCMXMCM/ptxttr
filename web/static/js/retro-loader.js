const DEFAULT_PROGRESS_WIDTH = 30;
const DEFAULT_STATUS_WINDOW = 4;
const MAX_PENDING_PERCENT = 84;
const COMPLETE_SETTLE_DELAY_MS = 140;
const DEFAULT_QUIET_AFTER_MS = 0;

const loaderState = new WeakMap();
const inlineLoaderState = new WeakMap();
let animationTimer = 0;

function motionReduced() {
  return Boolean(globalThis.window?.matchMedia?.("(prefers-reduced-motion: reduce)")?.matches);
}

function clamp(value, min, max) {
  return Math.max(min, Math.min(max, value));
}

function parseStatusMessages(raw) {
  return String(raw || "")
    .split("||")
    .map((item) => item.trim())
    .filter(Boolean);
}

function parsePositiveInt(raw, fallback) {
  const value = Number.parseInt(String(raw || ""), 10);
  return Number.isFinite(value) && value > 0 ? value : fallback;
}

export function retroLoaderShouldQuiet({
  startedAt = 0,
  now = startedAt,
  quietAfterMs = DEFAULT_QUIET_AFTER_MS,
  isComplete = false,
} = {}) {
  if (isComplete) return false;
  const threshold = Math.max(0, Number(quietAfterMs) || 0);
  if (threshold < 1) return false;
  return Math.max(0, Number(now) - Number(startedAt)) >= threshold;
}

export function retroLoaderProgressState({
  startedAt = 0,
  now = startedAt,
  progressWidth = DEFAULT_PROGRESS_WIDTH,
  reduceMotion = false,
  isComplete = false,
  explicitPercent = null,
} = {}) {
  const width = Math.max(1, progressWidth);
  if (isComplete) {
    return {
      units: width,
      percent: 100,
    };
  }
  if (Number.isFinite(explicitPercent)) {
    const pendingPercent = clamp(Math.round(Number(explicitPercent)), 0, MAX_PENDING_PERCENT);
    return {
      units: Math.min(width - 1, Math.max(0, Math.floor((pendingPercent / 100) * width))),
      percent: pendingPercent,
    };
  }
  const estimatedDuration = reduceMotion ? 18000 : 45000;
  const elapsed = Math.max(0, Number(now) - Number(startedAt));
  const normalized = clamp(elapsed / estimatedDuration, 0, 1);
  const eased = 1 - Math.pow(1 - normalized, 1.9);
  const pendingPercent = Math.min(Math.round(MAX_PENDING_PERCENT * eased), MAX_PENDING_PERCENT);
  const units = Math.min(
    width - 1,
    Math.max(0, Math.floor((pendingPercent / 100) * width)),
  );
  return {
    units,
    percent: pendingPercent,
  };
}

export function retroLoaderActivityWindow(statusMessages = [], targetCount = DEFAULT_STATUS_WINDOW) {
  const statuses = Array.isArray(statusMessages) ? statusMessages.filter(Boolean) : [];
  if (!statuses.length) return [];
  const count = Math.max(1, targetCount);
  return statuses.slice(-count);
}

export function retroLoaderStatusWindow({
  statusMessages = [],
  explicitStatusMessage = "",
  startedAt = 0,
  now = startedAt,
  reduceMotion = false,
  windowSize = DEFAULT_STATUS_WINDOW,
  isComplete = false,
  completionMessage = "",
} = {}) {
  const statuses = Array.isArray(statusMessages) ? statusMessages.filter(Boolean) : [];
  if (!statuses.length && !completionMessage) return [];
  if (isComplete) {
    return retroLoaderActivityWindow(
      completionMessage ? [...statuses, completionMessage] : statuses,
      windowSize,
    );
  }
  const revealDelay = reduceMotion ? 180 : 520;
  const revealedCount = clamp(
    Math.max(1, Math.ceil((Math.max(0, Number(now) - Number(startedAt))) / revealDelay)),
    1,
    Math.max(statuses.length, 1),
  );
  const revealed = statuses.slice(0, revealedCount);
  const explicitStatus = String(explicitStatusMessage || "").trim();
  if (!explicitStatus) return retroLoaderActivityWindow(revealed, windowSize);
  return retroLoaderActivityWindow(
    [...revealed.filter((status) => status !== explicitStatus), explicitStatus],
    windowSize,
  );
}

export function retroLoaderProgressText({ units = 0, progressWidth = DEFAULT_PROGRESS_WIDTH, percent = 0 } = {}) {
  const width = Math.max(1, progressWidth);
  const filled = "█".repeat(clamp(units, 0, width));
  const empty = "░".repeat(Math.max(width - filled.length, 0));
  const pct = clamp(Math.round(Number(percent) || 0), 0, 100);
  return `${filled}${empty} ${pct}%`;
}

function ensureState(loader) {
  const existing = loaderState.get(loader);
  if (existing) return existing;
  const state = {
    startedAt: Date.now(),
    progressWidth: parsePositiveInt(loader.dataset.retroLoaderProgressWidth, DEFAULT_PROGRESS_WIDTH),
    windowSize: parsePositiveInt(loader.dataset.retroLoaderStatusWindow, DEFAULT_STATUS_WINDOW),
    quietAfterMs: parsePositiveInt(loader.dataset.retroLoaderQuietAfterMs, DEFAULT_QUIET_AFTER_MS),
    statusMessages: parseStatusMessages(loader.dataset.retroLoaderStatuses),
    completionMessage: String(loader.dataset.retroLoaderComplete || "").trim(),
    completeAt: 0,
    explicitPercent: null,
    explicitStatusMessage: "",
  };
  loaderState.set(loader, state);
  return state;
}

function renderLoader(loader, state, now, reduceMotion) {
  const titleNode = loader.querySelector("[data-retro-loader-title]");
  const progressNode = loader.querySelector("[data-retro-loader-progress]");
  const activityNode = loader.querySelector("[data-retro-loader-activity]");
  const summaryNode = loader.querySelector("[data-retro-loader-summary]");
  const isComplete = loader.dataset.retroLoaderCompleteState === "1";
  const shouldQuiet = retroLoaderShouldQuiet({
    startedAt: state.startedAt,
    now,
    quietAfterMs: state.quietAfterMs,
    isComplete,
  });
  const progress = retroLoaderProgressState({
    startedAt: state.startedAt,
    now,
    progressWidth: state.progressWidth,
    reduceMotion,
    isComplete,
    explicitPercent: state.explicitPercent,
  });
  if (titleNode && loader.dataset.retroLoaderType === "thread") {
    titleNode.textContent = "";
    titleNode.hidden = true;
  } else if (titleNode && loader.dataset.retroLoaderTitle) {
    titleNode.textContent = loader.dataset.retroLoaderTitle;
  }
  if (summaryNode && loader.dataset.retroLoaderSummary) {
    summaryNode.textContent = loader.dataset.retroLoaderSummary;
  }
  if (progressNode) {
    const progressBlock = progressNode.closest(".retro-loader-progress-block");
    if (progressBlock instanceof HTMLElement) {
      progressBlock.hidden = shouldQuiet && loader.dataset.retroLoaderHideProgressWhenQuiet === "1";
    }
    progressNode.textContent = retroLoaderProgressText({
      units: progress.units,
      progressWidth: state.progressWidth,
      percent: progress.percent,
    });
  }
  if (activityNode) {
    activityNode.hidden = shouldQuiet;
    const lines = retroLoaderStatusWindow({
      statusMessages: state.statusMessages,
      explicitStatusMessage: state.explicitStatusMessage,
      startedAt: state.startedAt,
      now,
      reduceMotion,
      windowSize: state.windowSize,
      isComplete,
      completionMessage: state.completionMessage,
    });
    activityNode.textContent = lines.join("\n");
  }
}

function queryRetroLoaders(root = document) {
  if (root === document) return [...document.querySelectorAll("[data-retro-loader]")];
  if (!(root instanceof Element)) return [];
  const matches = root.matches("[data-retro-loader]") ? [root] : [];
  matches.push(...root.querySelectorAll("[data-retro-loader]"));
  return matches;
}

export function refreshRetroLoaders(root = document) {
  const reduceMotion = motionReduced();
  const now = Date.now();
  const loaders = queryRetroLoaders(root);
  loaders.forEach((loader) => renderLoader(loader, ensureState(loader), now, reduceMotion));
  return loaders.length;
}

function retroLoadersRemain() {
  return document.querySelector("[data-retro-loader]") !== null;
}

function startRetroLoaderAnimation() {
  if (animationTimer || typeof window === "undefined") return;
  animationTimer = window.setInterval(() => {
    refreshRetroLoaders(document);
    if (retroLoadersRemain()) return;
    window.clearInterval(animationTimer);
    animationTimer = 0;
  }, motionReduced() ? 220 : 140);
}

export function initRetroLoaders(root = document) {
  refreshRetroLoaders(root);
  startRetroLoaderAnimation();
}

export function markRetroLoaderComplete(loader, { summary, completionMessage } = {}) {
  if (!(loader instanceof Element)) return;
  const state = ensureState(loader);
  loader.dataset.retroLoaderCompleteState = "1";
  state.explicitPercent = 100;
  state.explicitStatusMessage = "";
  if (typeof summary === "string" && summary.trim()) loader.dataset.retroLoaderSummary = summary.trim();
  if (typeof completionMessage === "string" && completionMessage.trim()) {
    state.completionMessage = completionMessage.trim();
    loader.dataset.retroLoaderComplete = state.completionMessage;
  }
  state.completeAt = Date.now();
  renderLoader(loader, state, state.completeAt, motionReduced());
}

export function settleRetroLoader(loader, options = {}) {
  if (!(loader instanceof Element)) return Promise.resolve();
  markRetroLoaderComplete(loader, options);
  if (typeof window === "undefined") return Promise.resolve();
  const delayMs = Number.isFinite(options.delayMs)
    ? Math.max(0, options.delayMs)
    : (motionReduced() ? 0 : COMPLETE_SETTLE_DELAY_MS);
  return new Promise((resolve) => {
    window.setTimeout(() => {
      window.requestAnimationFrame(() => resolve());
    }, delayMs);
  });
}

function retroLoaderRootMarkup({
  loaderType = "inline",
  title = "loading",
  summary = "",
  statusMessages = [],
  completionMessage = "done.",
  progressWidth = 24,
  statusWindow = 3,
  hideActivity = false,
} = {}) {
  const section = document.createElement("section");
  section.className = `feed-loader retro-loader inline-retro-loader${hideActivity ? " inline-retro-loader--progress-only" : ""}`;
  section.dataset.feedLoader = "";
  section.dataset.retroLoader = "";
  section.dataset.retroLoaderType = loaderType;
  section.dataset.retroLoaderTitle = loaderType === "thread" ? "" : title;
  section.dataset.retroLoaderStatuses = (statusMessages || []).filter(Boolean).join("||");
  section.dataset.retroLoaderComplete = completionMessage;
  section.dataset.retroLoaderProgressWidth = String(progressWidth);
  section.dataset.retroLoaderStatusWindow = String(statusWindow);
  if (hideActivity) section.dataset.retroLoaderHideActivity = "1";
  section.setAttribute("aria-busy", "true");
  const activityMarkup = hideActivity ? "" : `
      <div class="retro-loader-activity-block">
        <pre class="retro-loader-activity" data-retro-loader-activity aria-live="polite"></pre>
      </div>
  `;
  section.innerHTML = `
    <div class="retro-loader-block">
      ${loaderType === "thread" ? "" : `<p class="retro-loader-title" data-retro-loader-title>${title}</p>`}
      <p class="muted retro-loader-summary" data-retro-loader-summary${summary ? "" : " hidden"}>${summary}</p>
      <div class="retro-loader-progress-block">
        <pre class="retro-loader-progress" data-retro-loader-progress aria-live="polite"></pre>
      </div>
      ${activityMarkup}
    </div>
  `;
  return section;
}

export function setRetroLoaderProgress(loader, { percent, summary, statusMessage, title } = {}) {
  if (!(loader instanceof Element)) return;
  const state = ensureState(loader);
  loader.dataset.retroLoaderCompleteState = "0";
  if (Number.isFinite(percent)) {
    state.explicitPercent = clamp(Math.round(Number(percent)), 0, MAX_PENDING_PERCENT);
  }
  if (typeof title === "string" && title.trim()) {
    loader.dataset.retroLoaderTitle = title.trim();
  }
  if (typeof summary === "string") {
    const summaryNode = loader.querySelector("[data-retro-loader-summary]");
    loader.dataset.retroLoaderSummary = summary.trim();
    if (summaryNode) {
      summaryNode.hidden = !summary.trim();
    }
  }
  if (typeof statusMessage === "string" && statusMessage.trim()) {
    const next = statusMessage.trim();
    const last = state.statusMessages[state.statusMessages.length - 1] || "";
    if (last !== next) state.statusMessages.push(next);
    state.explicitStatusMessage = next;
  }
  renderLoader(loader, state, Date.now(), motionReduced());
}

export function showInlineRetroLoader(target, options = {}) {
  if (!(target instanceof Element)) return null;
  const existing = inlineLoaderState.get(target);
  if (existing?.loader?.isConnected) {
    return existing.loader;
  }
  const loader = retroLoaderRootMarkup(options);
  target.hidden = true;
  target.insertAdjacentElement("afterend", loader);
  inlineLoaderState.set(target, { loader });
  refreshRetroLoaders(loader);
  startRetroLoaderAnimation();
  return loader;
}

export function hideInlineRetroLoader(target, { keepTargetHidden = false } = {}) {
  if (!(target instanceof Element)) return;
  const existing = inlineLoaderState.get(target);
  existing?.loader?.remove();
  inlineLoaderState.delete(target);
  target.hidden = Boolean(keepTargetHidden);
}
