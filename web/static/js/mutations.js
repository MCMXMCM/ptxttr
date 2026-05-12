import { fetchWithSession, getSession, normalizedPubkey, normalizeRelayURL, saveSelectedRelays, selectedRelays, withRelayParams } from "./session.js";
import { activeSignerState, signEventDraft } from "./signer.js";
import { initBookmarks, publishSignedEvent } from "./bookmarks.js";
import { MENTION_TOKEN_RE, mentionPubKey } from "./nip27.js";

const MENTION_LIMIT = 12;
const MENTION_CACHE_LIMIT = 24;
const MENTION_MENU_DEBOUNCE_MS = 30;
const FOLLOW_CONTACT_LIMIT = 600;
const MENTION_MENU_GAP_PX = 6;
const CARET_STYLE_KEYS = [
  "borderBottomWidth",
  "borderLeftWidth",
  "borderRightWidth",
  "borderTopWidth",
  "fontFamily",
  "fontSize",
  "fontStyle",
  "fontWeight",
  "letterSpacing",
  "lineHeight",
  "paddingBottom",
  "paddingLeft",
  "paddingRight",
  "paddingTop",
  "textTransform",
  "wordSpacing",
];
const mentionCandidateCache = new Map();
const followStateCache = new Map();

function composeState(root) {
  const dialog = root.querySelector("[data-composer-dialog]");
  if (!dialog) return null;
  return {
    dialog,
    form: dialog.querySelector("[data-composer-form]"),
    content: dialog.querySelector("[data-composer-content]"),
    status: dialog.querySelector("[data-composer-status]"),
    title: dialog.querySelector("[data-composer-title]"),
    rootID: dialog.querySelector("[data-composer-root-id]"),
    replyID: dialog.querySelector("[data-composer-reply-id]"),
    replyPubKey: dialog.querySelector("[data-composer-reply-pubkey]"),
    repostID: dialog.querySelector("[data-composer-repost-id]"),
    repostPubKey: dialog.querySelector("[data-composer-repost-pubkey]"),
    repostRelay: dialog.querySelector("[data-composer-repost-relay]"),
    mode: dialog.querySelector("[data-composer-mode]"),
    previewWrap: dialog.querySelector("[data-composer-preview]"),
    previewContent: dialog.querySelector("[data-composer-preview-content]"),
    submit: dialog.querySelector("[data-composer-submit]"),
    inputWrap: dialog.querySelector("[data-composer-input-wrap]"),
    overlay: dialog.querySelector("[data-composer-overlay]"),
    mentionMenu: dialog.querySelector("[data-composer-mentions]"),
    mentionList: dialog.querySelector("[data-composer-mention-list]"),
    mentionCandidates: [],
    mentionByPubKey: new Map(),
    mentionByCode: new Map(),
    mentionByName: new Map(),
    mentionContextKey: "",
    mentionActiveIndex: 0,
    mentionTrigger: null,
    mentionOptions: [],
    mentionMenuTimer: null,
    mentionMenuFrame: 0,
    caretMirror: null,
    backdropPointerDown: false,
    backdropPointerUp: false,
  };
}

function setStatus(state, message) {
  if (state?.status) state.status.textContent = message;
}

function clearComposeContext(state) {
  if (!state) return;
  state.rootID.value = "";
  state.replyID.value = "";
  state.replyPubKey.value = "";
}

function clearRepostContext(state) {
  if (!state) return;
  if (state.repostID) state.repostID.value = "";
  if (state.repostPubKey) state.repostPubKey.value = "";
  if (state.repostRelay) state.repostRelay.value = "";
  if (state.previewContent) state.previewContent.textContent = "";
  if (state.previewWrap) state.previewWrap.hidden = true;
}

function buildReferenceTags(kind, targetID, targetPubKey, targetRelay) {
  const relayHint = targetRelay || selectedRelays()[0] || "";
  const tags = [];
  if (targetID) {
    tags.push(kind === "quote"
      ? ["q", targetID, relayHint, targetPubKey || ""]
      : ["e", targetID, relayHint]);
  }
  if (targetPubKey) tags.push(["p", targetPubKey]);
  return dedupeTags(tags);
}

function dedupeTags(tags) {
  const deduped = [];
  const seen = new Set();
  tags.forEach((tag) => {
    const key = JSON.stringify(tag);
    if (seen.has(key)) return;
    seen.add(key);
    deduped.push(tag);
  });
  return deduped;
}

function normalizeFollowCandidate(raw) {
  const pubkey = String(raw?.pubkey || "").toLowerCase();
  const relays = Array.isArray(raw?.relays)
    ? raw.relays.map((relay) => String(relay || "").trim()).filter(Boolean)
    : [];
  if (!pubkey) return null;
  return { pubkey, relays };
}

function setFollowButtonState(button, following) {
  button.setAttribute("aria-pressed", following ? "true" : "false");
  button.textContent = following ? "Following" : "Follow";
}

function followStateFor(viewer) {
  if (!viewer) return { following: new Set(), relayHints: new Map() };
  const cached = followStateCache.get(viewer);
  if (cached) return cached;
  const state = { following: new Set(), relayHints: new Map() };
  followStateCache.set(viewer, state);
  return state;
}

async function loadFollowState(viewer) {
  if (!viewer) return followStateFor("");
  const state = followStateFor(viewer);
  if (state.loaded || state.loading) {
    if (state.loading) await state.loading;
    return state;
  }
  state.loading = fetchWithSession(`/api/mentions`)
    .then(async (response) => {
      if (!response.ok) return;
      const payload = await response.json();
      const candidates = Array.isArray(payload?.candidates) ? payload.candidates : [];
      state.following.clear();
      state.relayHints.clear();
      candidates.forEach((raw) => {
        if (String(raw?.source || "") !== "contact") return;
        const candidate = normalizeFollowCandidate(raw);
        if (!candidate) return;
        state.following.add(candidate.pubkey);
        if (candidate.relays.length > 0) state.relayHints.set(candidate.pubkey, candidate.relays[0]);
      });
    })
    .catch((error) => {
      console.warn("follow: /api/mentions request failed", error);
    })
    .finally(() => {
      state.loaded = true;
      state.loading = null;
    });
  await state.loading;
  return state;
}

export async function viewerHasAtLeastOneFollow(viewer) {
  const state = await loadFollowState(viewer);
  return state.following.size > 0;
}

function followTagsForState(following, relayHints) {
  return [...following]
    .slice(0, FOLLOW_CONTACT_LIMIT)
    .sort()
    .map((pubkey) => {
      const relay = relayHints.get(pubkey) || "";
      if (!relay) return ["p", pubkey];
      return ["p", pubkey, relay];
    });
}

async function publishFollowList(viewer, following, relayHints) {
  const draft = {
    kind: 3,
    created_at: Math.floor(Date.now() / 1000),
    tags: followTagsForState(following, relayHints),
    content: "",
  };
  const signed = await signEventDraft(draft, getSession());
  await publishSignedEvent(signed);
}

function refreshFollowButtons(root, state) {
  root.querySelectorAll("[data-follow-toggle][data-pubkey]").forEach((button) => {
    const target = String(button.getAttribute("data-pubkey") || "").toLowerCase();
    if (!target) return;
    setFollowButtonState(button, state.following.has(target));
  });
}

function bindFollowActions(root) {
  const viewer = normalizedPubkey();
  if (!viewer) return;
  void loadFollowState(viewer).then((state) => refreshFollowButtons(root, state));
  if (root._ptxtFollowDelegateBound) return;
  root._ptxtFollowDelegateBound = true;
  root.addEventListener("click", (event) => {
    const button = event.target.closest?.("[data-follow-toggle][data-pubkey]");
    if (!(button instanceof HTMLButtonElement)) return;
    event.preventDefault();
    if (button.dataset.loading === "1") return;
    const target = String(button.getAttribute("data-pubkey") || "").toLowerCase();
    const currentViewer = normalizedPubkey();
    if (!target || !currentViewer || target === currentViewer) return;
    const signer = activeSignerState();
    if (!signer.isLoggedIn) {
      window.dispatchEvent(new CustomEvent("ptxt:navigate", { detail: { href: "/login" } }));
      return;
    }
    if (!signer.canSign) {
      button.dataset.error = "1";
      button.title = "A signing-capable login is required.";
      return;
    }
    button.dataset.loading = "1";
    button.classList.add("is-pressed");
    button.disabled = true;
    void loadFollowState(currentViewer)
      .then(async (state) => {
        const nextFollowing = new Set(state.following);
        const isFollowing = nextFollowing.has(target);
        if (isFollowing) nextFollowing.delete(target);
        else nextFollowing.add(target);
        await publishFollowList(currentViewer, nextFollowing, state.relayHints);
        state.following = nextFollowing;
        refreshFollowButtons(root, state);
      })
      .catch((error) => {
        console.warn("follow: publish failed", error);
      })
      .finally(() => {
        delete button.dataset.loading;
        button.classList.remove("is-pressed");
        button.disabled = false;
      });
  });
}

function escapeHTML(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll("\"", "&quot;")
    .replaceAll("'", "&#39;");
}

function extractMentionPubKeys(content) {
  if (!content || content.indexOf("nostr:") < 0) return [];
  const matches = content.match(MENTION_TOKEN_RE) || [];
  const seen = new Set();
  const pubkeys = [];
  matches.forEach((raw) => {
    const pubkey = mentionPubKey(raw);
    if (!pubkey || seen.has(pubkey)) return;
    seen.add(pubkey);
    pubkeys.push(pubkey);
  });
  return pubkeys;
}

function normalizeMentionCandidate(candidate, source) {
  const pubkey = String(candidate?.pubkey || "").toLowerCase();
  const name = String(candidate?.name || "").trim();
  const npub = String(candidate?.npub || "");
  const nref = String(candidate?.nref || npub || "");
  const relays = Array.isArray(candidate?.relays) ? candidate.relays.filter(Boolean) : [];
  if (!pubkey || !name || !npub || !nref) return null;
  const searchKey = `${name} ${npub.slice(0, 20)} ${pubkey.slice(0, 16)}`.toLowerCase();
  return {
    pubkey,
    name,
    npub,
    nref,
    relays,
    source: source || candidate?.source || "contact",
    searchKey,
  };
}

function resetMentionIndex(state) {
  state.mentionCandidates = [];
  state.mentionByCode = new Map();
  state.mentionByPubKey = new Map();
  if (state.mentionByName) state.mentionByName.clear();
  else state.mentionByName = new Map();
}

function setMentionCandidates(state, candidates) {
  resetMentionIndex(state);
  const deduped = [];
  const seen = new Set();
  candidates.forEach((candidate) => {
    if (!candidate || !candidate.pubkey || seen.has(candidate.pubkey)) return;
    seen.add(candidate.pubkey);
    deduped.push(candidate);
    state.mentionByPubKey.set(candidate.pubkey, candidate);
    state.mentionByCode.set(candidate.npub.toLowerCase(), candidate);
    state.mentionByCode.set(candidate.nref.toLowerCase(), candidate);
  });
  state.mentionCandidates = deduped;
}

function extractInlineThreadCandidates(root) {
  const seen = new Set();
  const candidates = [];
  root.querySelectorAll("[data-reply-pubkey]").forEach((node) => {
    const pubkey = String(node.getAttribute("data-reply-pubkey") || "").toLowerCase();
    if (!pubkey || seen.has(pubkey)) return;
    seen.add(pubkey);
    const npub = String(node.getAttribute("data-ascii-npub") || "");
    const name = String(node.getAttribute("data-ascii-author") || "").trim() || pubkey.slice(0, 12);
    candidates.push(normalizeMentionCandidate({ pubkey, name, npub, nref: npub, source: "thread" }, "thread"));
  });
  return candidates.filter(Boolean);
}

async function loadMentionCandidates(root, state, rootID) {
  const viewer = normalizedPubkey();
  if (!viewer) {
    resetMentionIndex(state);
    return;
  }
  const contextKey = `${viewer}|${rootID || "post"}`;
  if (state.mentionContextKey === contextKey && state.mentionCandidates.length > 0) return;
  state.mentionContextKey = contextKey;
  if (mentionCandidateCache.has(contextKey)) {
    setMentionCandidates(state, mentionCandidateCache.get(contextKey));
    return;
  }
  const params = new URLSearchParams();
  if (rootID) params.set("root_id", rootID);
  let normalized = [];
  try {
    const queryString = params.toString();
    const url = queryString ? `/api/mentions?${queryString}` : "/api/mentions";
    const response = await fetchWithSession(url);
    if (response.ok) {
      const payload = await response.json();
      const rawCandidates = Array.isArray(payload?.candidates) ? payload.candidates : [];
      normalized = rawCandidates
        .map((candidate) => normalizeMentionCandidate(candidate, candidate?.source))
        .filter(Boolean);
    } else {
      console.warn("mentions: /api/mentions returned", response.status);
    }
  } catch (error) {
    console.warn("mentions: /api/mentions request failed", error);
    normalized = [];
  }
  if (normalized.length === 0) {
    normalized = extractInlineThreadCandidates(root);
  }
  if (mentionCandidateCache.size >= MENTION_CACHE_LIMIT) {
    const oldestKey = mentionCandidateCache.keys().next().value;
    if (oldestKey !== undefined) mentionCandidateCache.delete(oldestKey);
  }
  mentionCandidateCache.set(contextKey, normalized);
  setMentionCandidates(state, normalized);
}

function syncComposerScroll(state) {
  if (!state.overlay || !state.content) return;
  state.overlay.scrollTop = state.content.scrollTop;
  state.overlay.scrollLeft = state.content.scrollLeft;
  if (!state.mentionMenu?.hidden) positionMentionMenu(state);
}

function expandMentionsForPublish(state, content) {
  if (!state.mentionByName || state.mentionByName.size === 0) return content;
  let out = "";
  let cursor = 0;
  while (cursor <= content.length) {
    const match = findNamedMentionMatch(state, content, cursor);
    if (!match) {
      out += content.slice(cursor);
      break;
    }
    out += content.slice(cursor, match.start);
    out += `nostr:${match.candidate.nref}`;
    cursor = match.end;
  }
  return out;
}

function findNamedMentionMatch(state, value, fromIndex) {
  if (!state.mentionByName || state.mentionByName.size === 0) return null;
  const at = value.indexOf("@", fromIndex);
  if (at < 0) return null;
  const before = at > 0 ? value[at - 1] : "";
  if (before && /[A-Za-z0-9_]/.test(before)) {
    return findNamedMentionMatch(state, value, at + 1);
  }
  const rest = value.slice(at + 1).toLowerCase();
  let bestName = "";
  let bestCandidate = null;
  for (const [name, candidate] of state.mentionByName) {
    if (name.length <= bestName.length) continue;
    if (rest.startsWith(name)) {
      bestName = name;
      bestCandidate = candidate;
    }
  }
  if (!bestCandidate) return findNamedMentionMatch(state, value, at + 1);
  return { start: at, end: at + 1 + bestName.length, candidate: bestCandidate };
}

function renderComposerOverlay(state) {
  if (!state.overlay || !state.content) return;
  const value = state.content.value || "";
  const parts = [];
  let cursor = 0;
  while (cursor <= value.length) {
    const match = findNamedMentionMatch(state, value, cursor);
    if (!match) {
      parts.push(escapeHTML(value.slice(cursor)));
      break;
    }
    parts.push(escapeHTML(value.slice(cursor, match.start)));
    const visible = value.slice(match.start, match.end);
    parts.push(`<span class="composer-overlay-mention">${escapeHTML(visible)}</span>`);
    cursor = match.end;
  }
  state.overlay.innerHTML = parts.join("") || " ";
  syncComposerScroll(state);
}

function findMentionTrigger(text, caret) {
  const prefix = text.slice(0, caret);
  const at = prefix.lastIndexOf("@");
  if (at < 0) return null;
  const before = at > 0 ? prefix[at - 1] : "";
  if (before && /[A-Za-z0-9_]/.test(before)) return null;
  const query = prefix.slice(at + 1);
  if (/\s/.test(query)) return null;
  return { start: at, end: caret, query: query.toLowerCase() };
}

function filteredMentionCandidates(state, query) {
  if (!query) return state.mentionCandidates.slice(0, MENTION_LIMIT);
  return state.mentionCandidates
    .filter((candidate) => candidate.searchKey.includes(query))
    .slice(0, MENTION_LIMIT);
}

function ensureCaretMirror(state) {
  if (!(state?.inputWrap instanceof HTMLElement) || !(state?.content instanceof HTMLTextAreaElement)) return null;
  if (state.caretMirror?.isConnected) return state.caretMirror;
  const mirror = document.createElement("div");
  mirror.className = "composer-caret-mirror";
  state.inputWrap.append(mirror);
  state.caretMirror = mirror;
  return mirror;
}

function caretAnchorInWrap(state, caretIndex) {
  if (!(state?.content instanceof HTMLTextAreaElement) || !(state?.inputWrap instanceof HTMLElement)) return null;
  const mirror = ensureCaretMirror(state);
  if (!mirror) return null;
  const computed = window.getComputedStyle(state.content);
  CARET_STYLE_KEYS.forEach((key) => {
    mirror.style[key] = computed[key];
  });
  mirror.style.boxSizing = computed.boxSizing;
  mirror.style.width = `${state.content.clientWidth}px`;
  const boundedIndex = Math.max(0, Math.min(caretIndex, state.content.value.length));
  mirror.textContent = state.content.value.slice(0, boundedIndex);
  const marker = document.createElement("span");
  marker.textContent = "\u200b";
  mirror.append(marker);
  const wrapRect = state.inputWrap.getBoundingClientRect();
  const contentRect = state.content.getBoundingClientRect();
  const mirrorRect = mirror.getBoundingClientRect();
  const markerRect = marker.getBoundingClientRect();
  const lineHeight = Number.parseFloat(computed.lineHeight) || 16;
  const offsetX = contentRect.left - wrapRect.left - state.content.scrollLeft;
  const offsetY = contentRect.top - wrapRect.top - state.content.scrollTop;
  return {
    x: markerRect.left - mirrorRect.left + offsetX,
    y: markerRect.top - mirrorRect.top + offsetY,
    lineHeight,
  };
}

function positionMentionMenu(state) {
  if (!state?.mentionMenu || !state?.mentionTrigger || !state?.inputWrap || state.mentionMenu.hidden) return;
  const anchor = caretAnchorInWrap(state, state.mentionTrigger.end);
  if (!anchor) return;
  const menu = state.mentionMenu;
  menu.style.left = "0px";
  menu.style.top = "0px";
  const wrapRect = state.inputWrap.getBoundingClientRect();
  const menuRect = menu.getBoundingClientRect();
  let left = Math.max(0, anchor.x);
  let top = Math.max(0, anchor.y + anchor.lineHeight + MENTION_MENU_GAP_PX);
  const maxLeft = Math.max(0, wrapRect.width - menuRect.width - 4);
  if (left > maxLeft) left = maxLeft;
  const maxTop = Math.max(0, wrapRect.height - menuRect.height - 4);
  if (top > maxTop) {
    const aboveTop = anchor.y - menuRect.height - MENTION_MENU_GAP_PX;
    top = aboveTop >= 0 ? aboveTop : maxTop;
  }
  menu.style.left = `${Math.round(left)}px`;
  menu.style.top = `${Math.round(Math.max(0, top))}px`;
}

function requestMentionMenuPosition(state) {
  if (state.mentionMenuFrame) cancelAnimationFrame(state.mentionMenuFrame);
  state.mentionMenuFrame = requestAnimationFrame(() => {
    state.mentionMenuFrame = 0;
    positionMentionMenu(state);
  });
}

function closeMentionMenu(state) {
  if (state.mentionMenuTimer) {
    clearTimeout(state.mentionMenuTimer);
    state.mentionMenuTimer = null;
  }
  if (state.mentionMenuFrame) {
    cancelAnimationFrame(state.mentionMenuFrame);
    state.mentionMenuFrame = 0;
  }
  if (!state.mentionMenu || !state.mentionList) return;
  state.mentionMenu.hidden = true;
  state.mentionList.innerHTML = "";
  state.mentionMenu.style.left = "";
  state.mentionMenu.style.top = "";
  state.mentionActiveIndex = 0;
  state.mentionTrigger = null;
  state.mentionOptions = [];
}

function openMentionMenu(state, options) {
  if (!state.mentionMenu || !state.mentionList) return;
  state.mentionOptions = options;
  state.mentionMenu.hidden = options.length === 0;
  if (options.length === 0) {
    state.mentionList.innerHTML = "";
    return;
  }
  state.mentionActiveIndex = Math.min(state.mentionActiveIndex, options.length - 1);
  state.mentionList.innerHTML = options.map((candidate, index) => {
    const active = index === state.mentionActiveIndex ? " is-active" : "";
    return `<li><button type="button" class="composer-mention-option${active}" data-mention-index="${index}">
      <span>${escapeHTML(candidate.name)}</span>
      <span class="composer-mention-option-meta">${escapeHTML(candidate.npub.slice(0, 24))}</span>
    </button></li>`;
  }).join("");
  requestMentionMenuPosition(state);
}

function insertMentionAtTrigger(state, candidate) {
  if (!state.content || !state.mentionTrigger) return;
  const before = state.content.value.slice(0, state.mentionTrigger.start);
  const after = state.content.value.slice(state.mentionTrigger.end);
  const token = `@${candidate.name}`;
  state.content.value = `${before}${token} ${after}`;
  const caret = before.length + token.length + 1;
  state.content.setSelectionRange(caret, caret);
  if (!state.mentionByName) state.mentionByName = new Map();
  state.mentionByName.set(candidate.name.toLowerCase(), candidate);
  closeMentionMenu(state);
  renderComposerOverlay(state);
}

function updateMentionMenu(state) {
  if (!state.content) return;
  state.mentionTrigger = findMentionTrigger(state.content.value, state.content.selectionStart || 0);
  if (!state.mentionTrigger) {
    closeMentionMenu(state);
    return;
  }
  const options = filteredMentionCandidates(state, state.mentionTrigger.query);
  openMentionMenu(state, options);
}

// scheduleMentionMenuUpdate coalesces repeated input/keyup/click events so the
// candidate filter + DOM rebuild only runs once per animation frame, even with
// fast typing on a list of hundreds of contacts.
function scheduleMentionMenuUpdate(state) {
  if (state.mentionMenuTimer) return;
  state.mentionMenuTimer = setTimeout(() => {
    state.mentionMenuTimer = null;
    updateMentionMenu(state);
  }, MENTION_MENU_DEBOUNCE_MS);
}

async function openComposer(root, state, mode, context = {}) {
  if (!state) return;
  const signer = activeSignerState();
  const canSign = signer.isLoggedIn && signer.canSign;
  if (state.mode) {
    state.mode.value = mode === "reply" || mode === "repost" ? mode : "post";
  }
  if (state.title) {
    if (mode === "reply") {
      state.title.textContent = "Write a reply";
    } else if (mode === "repost") {
      state.title.textContent = "Repost";
    } else {
      state.title.textContent = "Write a post";
    }
  }
  state.content.required = mode !== "repost";
  if (mode === "reply") {
    state.rootID.value = context.rootID || context.targetID || "";
    state.replyID.value = context.targetID || "";
    state.replyPubKey.value = context.pubkey || "";
    clearRepostContext(state);
  } else if (mode === "repost") {
    clearComposeContext(state);
    if (state.content) state.content.value = "";
    if (state.repostID) state.repostID.value = context.targetID || "";
    if (state.repostPubKey) state.repostPubKey.value = context.pubkey || "";
    if (state.repostRelay) state.repostRelay.value = context.relay || "";
    if (state.previewContent) state.previewContent.textContent = context.source || "(no note content)";
    if (state.previewWrap) state.previewWrap.hidden = false;
  } else {
    clearComposeContext(state);
    clearRepostContext(state);
  }
  await loadMentionCandidates(root, state, mode === "reply" ? state.rootID.value : "");
  renderComposerOverlay(state);
  closeMentionMenu(state);
  state.submit.disabled = !canSign;
  if (!signer.isLoggedIn) {
    setStatus(state, "Log in first to publish.");
  } else if (!signer.canSign) {
    setStatus(state, "Switch to Browser Extension, Nsec, or Ephemeral login to sign events.");
  } else if (mode === "repost") {
    setStatus(state, "Leave content blank for a repost, or add text for a quote post.");
  } else {
    setStatus(state, mode === "reply" ? "Publishing a signed reply event." : "Publishing a signed kind 1 note.");
  }
  if (typeof state.dialog.showModal === "function") {
    state.dialog.showModal();
  } else {
    state.dialog.setAttribute("open", "");
  }
  if (state.content && canSign) {
    queueMicrotask(() => state.content.focus());
  }
}

function closeComposer(state) {
  if (!state) return;
  closeMentionMenu(state);
  if (typeof state.dialog.close === "function") {
    state.dialog.close();
  } else {
    state.dialog.removeAttribute("open");
  }
}

function buildReplyTags(rootID, replyID, replyPubKey) {
  const tags = [];
  if (rootID) tags.push(["e", rootID, "", "root"]);
  if (replyID) tags.push(["e", replyID, "", "reply"]);
  if (replyPubKey) tags.push(["p", replyPubKey]);
  return dedupeTags(tags);
}

function bindPostTriggers(root, state) {
  root.querySelectorAll("[data-post-trigger]").forEach((button) => {
    if (button._ptxtComposeBound) return;
    button._ptxtComposeBound = true;
    button.addEventListener("click", () => {
      void openComposer(root, state, "post");
    });
  });
}

function bindReplyActions(root) {
  if (!root || root._ptxtReplyDelegateBound) return;
  root._ptxtReplyDelegateBound = true;
  root.addEventListener("click", (event) => {
    const link = event.target.closest("a[data-reply-action],a[href^='/thread/'],button[data-reply-action],button[data-repost-action]");
    if (!link) return;
    const inReplyContainer = link.closest("[data-reply-target-id]");
    if (!inReplyContainer) return;
    const state = composeState(root);
    if (!state) return;
    if (link.hasAttribute("data-repost-action")) {
      event.preventDefault();
      void openComposer(root, state, "repost", {
        targetID: link.getAttribute("data-repost-target-id") || inReplyContainer.getAttribute("data-reply-target-id") || "",
        pubkey: link.getAttribute("data-repost-pubkey") || inReplyContainer.getAttribute("data-reply-pubkey") || "",
        relay: link.getAttribute("data-repost-relay") || inReplyContainer.getAttribute("data-ascii-relay") || "",
        source: inReplyContainer.querySelector(":scope > .ascii-source")?.content?.textContent?.trim() || "",
      });
      return;
    }
    const linkText = (link.textContent || "").trim().toLowerCase();
    if (!link.hasAttribute("data-reply-action") && !linkText.startsWith("reply")) return;
    event.preventDefault();
    void openComposer(root, state, "reply", {
      rootID: link.getAttribute("data-reply-root-id") || inReplyContainer.getAttribute("data-reply-root-id") || "",
      targetID: link.getAttribute("data-reply-target-id") || inReplyContainer.getAttribute("data-reply-target-id") || "",
      pubkey: link.getAttribute("data-reply-pubkey") || inReplyContainer.getAttribute("data-reply-pubkey") || "",
    });
  });
}

function bindComposer(root) {
  const state = composeState(root);
  if (!state || !state.form || !state.content) return;
  bindPostTriggers(root, state);
  bindReplyActions(root);
  if (!state.dialog._ptxtComposerCloseBound) {
    state.dialog._ptxtComposerCloseBound = true;
    state.dialog.addEventListener("click", (event) => {
      const target = event.target;
      const isBackdropClick = target === state.dialog;
      if (isBackdropClick && state.backdropPointerDown && state.backdropPointerUp) {
        closeComposer(state);
        state.backdropPointerDown = false;
        state.backdropPointerUp = false;
        return;
      }
      state.backdropPointerDown = false;
      state.backdropPointerUp = false;
      if (!target.closest?.("[data-composer-mentions]")) {
        closeMentionMenu(state);
      }
    });
    state.dialog.addEventListener("pointerdown", (event) => {
      state.backdropPointerDown = event.target === state.dialog;
    });
    state.dialog.addEventListener("pointerup", (event) => {
      state.backdropPointerUp = event.target === state.dialog;
    });
    state.dialog.querySelector("[data-composer-cancel]")?.addEventListener("click", () => closeComposer(state));
    state.dialog.querySelector("[data-close-composer]")?.addEventListener("click", () => closeComposer(state));
    state.mentionList?.addEventListener("click", (event) => {
      const option = event.target.closest("[data-mention-index]");
      if (!(option instanceof HTMLElement)) return;
      const index = Number(option.getAttribute("data-mention-index"));
      if (Number.isNaN(index)) return;
      const options = state.mentionOptions.length
        ? state.mentionOptions
        : filteredMentionCandidates(state, state.mentionTrigger?.query || "");
      if (!options[index]) return;
      insertMentionAtTrigger(state, options[index]);
      state.content?.focus();
    });
  }
  if (!state.content._ptxtComposerMentionsBound) {
    state.content._ptxtComposerMentionsBound = true;
    state.content.addEventListener("input", () => {
      renderComposerOverlay(state);
      scheduleMentionMenuUpdate(state);
    });
    state.content.addEventListener("scroll", () => syncComposerScroll(state));
    state.content.addEventListener("click", () => scheduleMentionMenuUpdate(state));
    state.content.addEventListener("keyup", () => scheduleMentionMenuUpdate(state));
    state.content.addEventListener("keydown", (event) => {
      if (state.mentionMenu?.hidden) return;
      const options = state.mentionOptions.length
        ? state.mentionOptions
        : filteredMentionCandidates(state, state.mentionTrigger?.query || "");
      if (!options.length) return;
      if (event.key === "ArrowDown") {
        event.preventDefault();
        state.mentionActiveIndex = (state.mentionActiveIndex + 1) % options.length;
        openMentionMenu(state, options);
        return;
      }
      if (event.key === "ArrowUp") {
        event.preventDefault();
        state.mentionActiveIndex = (state.mentionActiveIndex - 1 + options.length) % options.length;
        openMentionMenu(state, options);
        return;
      }
      if (event.key === "Escape") {
        event.preventDefault();
        closeMentionMenu(state);
        return;
      }
      if (event.key === "Enter" || event.key === "Tab") {
        event.preventDefault();
        insertMentionAtTrigger(state, options[state.mentionActiveIndex] || options[0]);
      }
    });
    window.addEventListener("resize", () => {
      if (!state.mentionMenu?.hidden) requestMentionMenuPosition(state);
    });
  }
  renderComposerOverlay(state);
  if (state.form._ptxtComposerFormBound) return;
  state.form._ptxtComposerFormBound = true;
  state.form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const signer = activeSignerState();
    if (!signer.isLoggedIn || !signer.canSign) {
      setStatus(state, "A signing-capable login is required.");
      return;
    }
    const rawContent = state.content.value.trim();
    const mode = state.mode?.value || "post";
    if (mode !== "repost" && !rawContent) {
      setStatus(state, "Content is required.");
      return;
    }
    const content = expandMentionsForPublish(state, rawContent);
    const repostID = state.repostID?.value || "";
    const repostPubKey = state.repostPubKey?.value || "";
    const repostRelay = state.repostRelay?.value || "";
    if (mode === "repost" && !repostID) {
      setStatus(state, "Missing repost target.");
      return;
    }
    const createdAt = Math.floor(Date.now() / 1000);
    const isReply = mode === "reply" && Boolean(state.replyID.value);
    const isRepost = mode === "repost";
    const isQuote = isRepost && Boolean(content);
    const draftKind = isRepost && !isQuote ? 6 : 1;
    let tags = [];
    if (isReply) {
      tags = buildReplyTags(state.rootID.value, state.replyID.value, state.replyPubKey.value);
    } else if (isRepost) {
      tags = buildReferenceTags(isQuote ? "quote" : "repost", repostID, repostPubKey, repostRelay);
    }
    extractMentionPubKeys(content).forEach((pubkey) => tags.push(["p", pubkey]));
    tags = dedupeTags(tags);
    const draft = {
      kind: draftKind,
      created_at: createdAt,
      tags,
      content: isRepost && !isQuote ? "" : content,
    };
    state.submit.disabled = true;
    setStatus(state, "Signing event...");
    try {
      const signed = await signEventDraft(draft, getSession());
      setStatus(state, "Publishing event...");
      await publishSignedEvent(signed);
      state.content.value = "";
      if (state.mentionByName) state.mentionByName.clear();
      renderComposerOverlay(state);
      clearComposeContext(state);
      clearRepostContext(state);
      closeComposer(state);
      const href = withRelayParams(`/thread/${encodeURIComponent(signed.id)}`);
      window.dispatchEvent(new CustomEvent("ptxt:navigate", { detail: { href } }));
    } catch (error) {
      setStatus(state, error instanceof Error ? error.message : "Failed to publish event.");
    } finally {
      state.submit.disabled = false;
    }
  });
}

function assignIfEmpty(form, name, value) {
  const input = form.elements.namedItem(name);
  if (!(input instanceof HTMLInputElement || input instanceof HTMLTextAreaElement)) return;
  if (input.value.trim() !== "") return;
  input.value = String(value || "");
}

async function loadProfileMetadata(form, statusNode) {
  const pubkey = normalizedPubkey();
  if (!pubkey) {
    if (statusNode) statusNode.textContent = "Log in with a signing-capable method to edit profile metadata.";
    return;
  }
  try {
    const response = await fetchWithSession(`/api/profile`);
    if (!response.ok) throw new Error("profile metadata request failed");
    const metadata = await response.json();
    assignIfEmpty(form, "display_name", metadata.display_name);
    assignIfEmpty(form, "name", metadata.name);
    assignIfEmpty(form, "about", metadata.about);
    assignIfEmpty(form, "picture", metadata.picture);
    assignIfEmpty(form, "website", metadata.website);
    assignIfEmpty(form, "nip05", metadata.nip05);
    if (statusNode) statusNode.textContent = "Profile metadata loaded. Publish to update relays.";
  } catch {
    if (statusNode) statusNode.textContent = "Could not load cached profile metadata.";
  }
}

function collectMetadataContent(form) {
  const values = Object.fromEntries(new FormData(form).entries());
  const output = {};
  Object.entries(values).forEach(([key, raw]) => {
    const value = String(raw || "").trim();
    if (!value) return;
    output[key] = value;
  });
  return output;
}

function normalizeRelayUsage(raw) {
  const value = String(raw || "").toLowerCase();
  if (value === "read" || value === "write") return value;
  return "any";
}

function relayUsageLabel(usage) {
  if (usage === "read") return "read";
  if (usage === "write") return "write";
  return "read+write";
}

function upsertRelayPreference(state, relay, usage) {
  const normalizedRelay = normalizeRelayURL(relay);
  if (!normalizedRelay) return false;
  const normalizedUsage = normalizeRelayUsage(usage);
  const index = state.preferences.findIndex((item) => item.url === normalizedRelay);
  if (index >= 0) {
    state.preferences[index].usage = normalizedUsage;
    return true;
  }
  state.preferences.push({ url: normalizedRelay, usage: normalizedUsage });
  return true;
}

function renderSimpleRelayList(node, relays, emptyMessage) {
  if (!node) return;
  if (!Array.isArray(relays) || relays.length === 0) {
    node.innerHTML = `<li class="muted">${emptyMessage}</li>`;
    return;
  }
  node.innerHTML = relays
    .map((relay) => `<li><code>${escapeHTML(relay)}</code></li>`)
    .join("");
}

function renderInsightRelayList(node, relays, emptyMessage) {
  if (!node) return;
  if (!Array.isArray(relays) || relays.length === 0) {
    node.innerHTML = `<li class="muted">${emptyMessage}</li>`;
    return;
  }
  node.innerHTML = relays.map((item) => {
    const relay = normalizeRelayURL(item?.url || "");
    if (!relay) return "";
    const usage = normalizeRelayUsage(item?.usage);
    const sources = Array.isArray(item?.sources) ? item.sources.map((source) => String(source || "").trim()).filter(Boolean) : [];
    const status = String(item?.status || "").trim();
    const confidence = String(item?.confidence || "").trim();
    const meta = [relayUsageLabel(usage), sources.join("+"), confidence, status].filter(Boolean).join(" | ");
    return `<li>
      <code>${escapeHTML(relay)}</code>
      <span class="muted">${escapeHTML(meta)}</span>
      <button type="button" data-relay-preference-suggested="${escapeHTML(relay)}" data-relay-preference-suggested-usage="${escapeHTML(usage)}">add</button>
    </li>`;
  }).filter(Boolean).join("");
}

function renderRelayPreferenceDraft(state) {
  if (!state.preferencesList) return;
  if (state.preferences.length === 0) {
    state.preferencesList.innerHTML = `<li class="muted">Add relay preferences, then publish kind 10002.</li>`;
    return;
  }
  state.preferences.sort((a, b) => a.url.localeCompare(b.url));
  state.preferencesList.innerHTML = state.preferences.map((item) => `<li data-relay-preference="${escapeHTML(item.url)}">
    <code>${escapeHTML(item.url)}</code>
    <span class="muted">${escapeHTML(relayUsageLabel(item.usage))}</span>
    <button type="button" data-relay-preference-remove="${escapeHTML(item.url)}">remove</button>
  </li>`).join("");
}

function relayTagsFromPreferences(preferences) {
  return preferences.map((item) => {
    if (item.usage === "read" || item.usage === "write") return ["r", item.url, item.usage];
    return ["r", item.url];
  });
}

async function loadRelayInsight(state) {
  const pubkey = normalizedPubkey();
  if (!pubkey) {
    if (state.statusNode) state.statusNode.textContent = "Log in with a signing-capable method to inspect relay insight.";
    return null;
  }
  try {
    const response = await fetchWithSession(`/api/relay-insight`);
    if (!response.ok) throw new Error("relay insight request failed");
    const payload = await response.json();
    renderInsightRelayList(state.publishedList, payload.published_relays, "No published relay preferences yet.");
    renderInsightRelayList(state.discoveredList, payload.discovered_relays, "No discovered relay hints yet.");
    renderInsightRelayList(state.recommendedList, payload.recommended_relays, "No recommendations available yet.");
    renderSimpleRelayList(state.sessionList, selectedRelays(), "No selected session relays.");
    return payload;
  } catch {
    if (state.statusNode) state.statusNode.textContent = "Could not load relay insight.";
    return null;
  }
}

function bindRelayPreferences(root) {
  const form = root.querySelector("[data-relay-preferences-form]");
  if (!(form instanceof HTMLFormElement) || form._ptxtRelayPreferencesBound) return;
  form._ptxtRelayPreferencesBound = true;
  const state = {
    form,
    input: form.querySelector("[data-relay-preference-input]"),
    usage: form.querySelector("[data-relay-preference-usage]"),
    submit: form.querySelector("[data-relay-preferences-submit]"),
    addButton: form.querySelector("[data-relay-preference-add]"),
    useSession: form.querySelector("[data-relay-preference-use-session]"),
    useRecommended: form.querySelector("[data-relay-preference-use-recommended]"),
    preferencesList: form.querySelector("[data-relay-preferences-list]"),
    publishedList: root.querySelector("[data-relay-insight-published]"),
    discoveredList: root.querySelector("[data-relay-insight-discovered]"),
    recommendedList: root.querySelector("[data-relay-insight-recommended]"),
    sessionList: root.querySelector("[data-relay-insight-session]"),
    statusNode: root.querySelector("[data-relay-insight-status]"),
    preferences: [],
    latestRecommended: [],
  };
  renderSimpleRelayList(state.sessionList, selectedRelays(), "No selected session relays.");
  if (state.statusNode) state.statusNode.textContent = "Loading relay insight...";
  void loadRelayInsight(state).then((payload) => {
    if (!payload) return;
    const published = Array.isArray(payload.published_relays) ? payload.published_relays : [];
    const recommended = Array.isArray(payload.recommended_relays) ? payload.recommended_relays : [];
    state.latestRecommended = recommended;
    state.preferences = [];
    if (published.length > 0) {
      published.forEach((entry) => upsertRelayPreference(state, entry.url, entry.usage));
    } else {
      selectedRelays().forEach((relay) => upsertRelayPreference(state, relay, "write"));
    }
    renderRelayPreferenceDraft(state);
    if (state.statusNode) state.statusNode.textContent = "Relay insight loaded.";
  });

  state.addButton?.addEventListener("click", () => {
    const relay = normalizeRelayURL(state.input?.value || "");
    if (!relay) {
      if (state.statusNode) state.statusNode.textContent = "Relay URL must start with ws:// or wss://.";
      return;
    }
    const usage = normalizeRelayUsage(state.usage?.value || "any");
    upsertRelayPreference(state, relay, usage);
    if (state.input instanceof HTMLInputElement) state.input.value = "";
    renderRelayPreferenceDraft(state);
    if (state.statusNode) state.statusNode.textContent = "Relay preference added.";
  });

  state.useSession?.addEventListener("click", () => {
    state.preferences = [];
    selectedRelays().forEach((relay) => upsertRelayPreference(state, relay, "write"));
    renderRelayPreferenceDraft(state);
    if (state.statusNode) state.statusNode.textContent = "Draft replaced with current session relays.";
  });

  state.useRecommended?.addEventListener("click", () => {
    state.preferences = [];
    state.latestRecommended.forEach((entry) => upsertRelayPreference(state, entry.url, entry.usage || "write"));
    renderRelayPreferenceDraft(state);
    if (state.statusNode) state.statusNode.textContent = "Draft replaced with backend recommendations.";
  });

  form.addEventListener("click", (event) => {
    const remove = event.target.closest?.("[data-relay-preference-remove]");
    if (remove) {
      const relay = normalizeRelayURL(remove.getAttribute("data-relay-preference-remove") || "");
      state.preferences = state.preferences.filter((item) => item.url !== relay);
      renderRelayPreferenceDraft(state);
    }
  });

  if (!form._ptxtRelaySuggestionDelegateBound) {
    form._ptxtRelaySuggestionDelegateBound = true;
    root.addEventListener("click", (event) => {
      const suggested = event.target.closest?.("[data-relay-preference-suggested]");
      if (!suggested) return;
      const relay = normalizeRelayURL(suggested.getAttribute("data-relay-preference-suggested") || "");
      const usage = normalizeRelayUsage(suggested.getAttribute("data-relay-preference-suggested-usage") || "any");
      if (!relay) return;
      upsertRelayPreference(state, relay, usage);
      renderRelayPreferenceDraft(state);
      if (state.statusNode) state.statusNode.textContent = "Suggested relay added to draft.";
    });
  }

  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const signer = activeSignerState();
    if (!signer.isLoggedIn || !signer.canSign) {
      if (state.statusNode) state.statusNode.textContent = "A signing-capable login is required.";
      return;
    }
    if (state.preferences.length === 0) {
      if (state.statusNode) state.statusNode.textContent = "Add at least one relay before publishing kind 10002.";
      return;
    }
    const draft = {
      kind: 10002,
      created_at: Math.floor(Date.now() / 1000),
      tags: relayTagsFromPreferences(state.preferences),
      content: "",
    };
    if (state.submit instanceof HTMLButtonElement) state.submit.disabled = true;
    if (state.statusNode) state.statusNode.textContent = "Signing relay preferences...";
    try {
      const signed = await signEventDraft(draft, getSession());
      if (state.statusNode) state.statusNode.textContent = "Publishing relay preferences...";
      await publishSignedEvent(signed);
      const nextSessionRelays = state.preferences
        .filter((item) => item.usage !== "read")
        .map((item) => item.url)
        .slice(0, 8);
      if (nextSessionRelays.length > 0) saveSelectedRelays(nextSessionRelays);
      await loadRelayInsight(state);
      renderRelayPreferenceDraft(state);
      if (state.statusNode) state.statusNode.textContent = "Relay preferences published as kind 10002.";
    } catch (error) {
      if (state.statusNode) state.statusNode.textContent = error instanceof Error ? error.message : "Relay preference publish failed.";
    } finally {
      if (state.submit instanceof HTMLButtonElement) state.submit.disabled = false;
    }
  });
}

function bindProfileMetadataForm(root) {
  const form = root.querySelector("[data-profile-metadata-form]");
  if (!(form instanceof HTMLFormElement) || form._ptxtProfileFormBound) return;
  form._ptxtProfileFormBound = true;
  const statusNode = form.querySelector("[data-profile-metadata-status]");
  const submitButton = form.querySelector("[data-profile-metadata-submit]");
  void loadProfileMetadata(form, statusNode);
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const signer = activeSignerState();
    if (!signer.isLoggedIn || !signer.canSign) {
      if (statusNode) statusNode.textContent = "A signing-capable login is required.";
      return;
    }
    const content = collectMetadataContent(form);
    if (Object.keys(content).length === 0) {
      if (statusNode) statusNode.textContent = "Add at least one metadata field before publishing.";
      return;
    }
    const draft = {
      kind: 0,
      created_at: Math.floor(Date.now() / 1000),
      tags: [],
      content: JSON.stringify(content),
    };
    if (submitButton instanceof HTMLButtonElement) submitButton.disabled = true;
    if (statusNode) statusNode.textContent = "Signing metadata event...";
    try {
      const signed = await signEventDraft(draft, getSession());
      if (statusNode) statusNode.textContent = "Publishing metadata...";
      await publishSignedEvent(signed);
      if (statusNode) statusNode.textContent = "Profile metadata published.";
    } catch (error) {
      if (statusNode) statusNode.textContent = error instanceof Error ? error.message : "Metadata publish failed.";
    } finally {
      if (submitButton instanceof HTMLButtonElement) submitButton.disabled = false;
    }
  });
}

export function initMutations(root = document) {
  bindComposer(root);
  bindProfileMetadataForm(root);
  bindRelayPreferences(root);
  bindFollowActions(root);
  initBookmarks(root);
}
