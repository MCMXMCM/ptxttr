import { positionPopoverNearAnchor } from "./popover_anchor.js";
import {
  fetchWithSession,
  getSession,
  normalizedPubkey,
  normalizePubkey,
  normalizeRelayURL,
  sessionAuthorLabelFromMetadata,
  setSession,
  shortPubkey,
  syncProfileFollowGuestAria,
  syncNIP07RelayConfigFromExtension,
  withRelayParams,
} from "./session.js";
import { activeSignerState, signEventDraft } from "./signer.js";
import { initBookmarks, publishSignedEvent } from "./bookmarks.js";
import { planPublishTargets } from "./publish.js";
import { fetchMentions, fetchMuteList, fetchViewerProfile, fetchViewerRelayPreferences } from "./relay-reads.js";
import { pendingPublishStatus, showPublishStatusSheet } from "./publish-status.js";
import { blossomUploadBlob } from "./blossom.js";
import { getBlossomServerURLs } from "./sort-prefs.js";
import { DEFAULT_LOGGED_OUT_WOT_SEED_NPUB, desktopModeEnabled } from "./viewer-defaults.js";
import { MENTION_TOKEN_RE, mentionPubKey } from "./nip27.js";
import { DEFAULT_RELAYS } from "./relay-config.js";
import { effectiveReadRelays, effectiveWriteRelays, loadRelayConfig, saveRelayConfig } from "./relay-state.js";
import { hydrateRelayInfoCards, relayHostLabel } from "./relays.js";

const MENTION_LIMIT = 12;
const MENTION_CACHE_LIMIT = 24;
const MENTION_MENU_DEBOUNCE_MS = 30;
// Must match nostrx.MaxMuteListTagRows (Go publish + store cap).
const MUTE_TAG_LIMIT = 2000;
const MUTE_P_TAG_LIMIT = MUTE_TAG_LIMIT - 1;
const MENTION_MENU_GAP_PX = 6;
/** Must match `THREAD_INLINE_REPLY_PENDING_KEY` in web/static/js/thread.js */
const THREAD_INLINE_REPLY_PENDING_KEY = "ptxt-inline-reply-v1";
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
const muteStateCache = new Map();
let followWriteChain = Promise.resolve();
let muteWriteChain = Promise.resolve();

function runSerializedFollowWrite(fn) {
  const run = followWriteChain.catch(() => {}).then(fn);
  followWriteChain = run.catch(() => {});
  return run;
}

function runSerializedMuteWrite(fn) {
  const run = muteWriteChain.catch(() => {}).then(fn);
  muteWriteChain = run.catch(() => {});
  return run;
}

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
    mediaRow: dialog.querySelector("[data-composer-media-row]"),
    imageInput: dialog.querySelector("[data-composer-image-input]"),
    addImageBtn: dialog.querySelector("[data-composer-add-image]"),
    attachmentStrip: dialog.querySelector("[data-composer-attachments]"),
    blossomAttachments: [],
  };
}

const COMPOSER_PLACEHOLDER_PREV = "ptxtPrevPlaceholder";

function inlineReplyStatusMirrorText(host, message) {
  const to = host?.dataset?.inlineReplyingTo;
  const tail = message || "";
  if (to) return `replying to @${to}\n${tail}`;
  return tail;
}

function setStatus(state, message) {
  if (state?.status) state.status.textContent = message;
  const inlineHost = state?.form?.closest(".thread-inline-reply");
  const mirror = inlineHost?.querySelector("[data-inline-composer-status]");
  if (mirror) mirror.textContent = inlineReplyStatusMirrorText(inlineHost, message);
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

function clearBlossomComposer(state) {
  if (!state) return;
  state.blossomAttachments = [];
  if (state.attachmentStrip) state.attachmentStrip.innerHTML = "";
  if (state.imageInput) state.imageInput.value = "";
}

/** Shared content/attachment merge and publish validation rules for composer UI + submit. */
function composerPublishContent(state) {
  const rawContent = state?.content instanceof HTMLTextAreaElement ? state.content.value.trim() : "";
  const mode = state.mode?.value || "post";
  const atts = state.blossomAttachments || [];
  const blossomLines = atts.map((a) => a.descriptor?.url).filter(Boolean).join("\n");
  const mergedRaw = [rawContent, blossomLines].filter(Boolean).join("\n").trim();
  let validityMsg = "";
  if (mode === "repost" && !rawContent && atts.length > 0) {
    validityMsg = "Remove attached images for a pure repost, or add text for a quote post.";
  } else if (mode !== "repost" && !mergedRaw) {
    validityMsg = "Content or at least one image is required.";
  }
  return { rawContent, mode, atts, mergedRaw, validityMsg };
}

/** HTML5 constraint validation for publish (supports image-only posts; mirrors submit rules). */
function syncComposerPublishValidity(state, signer = activeSignerState()) {
  const ta = state?.content;
  const form = state?.form;
  if (!(ta instanceof HTMLTextAreaElement) || !(form instanceof HTMLFormElement)) return;
  const { validityMsg } = composerPublishContent(state);
  ta.setCustomValidity(validityMsg);
  const canSign = signer.isLoggedIn && signer.canSign;
  if (state.submit instanceof HTMLButtonElement) {
    state.submit.disabled = !canSign || !form.checkValidity();
  }
}

function syncComposerMediaRow(state, signer = activeSignerState()) {
  if (!state?.mediaRow) return;
  const can = signer.isLoggedIn && signer.canSign;
  const mode = state.mode?.value || "post";
  const repost = mode === "repost";
  const quote = repost && Boolean((state.content?.value || "").trim());
  state.mediaRow.hidden = !can || (repost && !quote);
}

function syncComposerControls(state) {
  const signer = activeSignerState();
  syncComposerMediaRow(state, signer);
  syncComposerPublishValidity(state, signer);
}

function renderComposerAttachments(state) {
  if (!state?.attachmentStrip) return;
  state.attachmentStrip.innerHTML = (state.blossomAttachments || [])
    .map(
      (att, index) => `<li class="composer-attachment-item">
      <span class="composer-attachment-label">${escapeHTML(att.name || "image")}</span>
      <button type="button" class="link-button" data-composer-remove-attachment="${index}">remove</button>
    </li>`,
    )
    .join("");
  syncComposerPublishValidity(state);
}

function buildReferenceTags(kind, targetID, targetPubKey, targetRelay) {
  const relayHint = targetRelay || effectiveReadRelays(loadRelayConfig())[0] || "";
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
  button.textContent = following ? "Unfollow" : "Follow";
}

let loginRequiredFollowPopover = null;
let loginRequiredFollowPopoverOutsideWired = false;
let followClickDelegateBound = false;

function ensureLoginRequiredFollowPopover() {
  if (loginRequiredFollowPopover) return loginRequiredFollowPopover;
  loginRequiredFollowPopover = document.createElement("div");
  loginRequiredFollowPopover.id = "ptxt-login-required-popover";
  loginRequiredFollowPopover.textContent = "Login required";
  loginRequiredFollowPopover.setAttribute("role", "status");
  loginRequiredFollowPopover.setAttribute("aria-live", "polite");
  loginRequiredFollowPopover.hidden = true;
  document.body.appendChild(loginRequiredFollowPopover);
  return loginRequiredFollowPopover;
}

function wireLoginRequiredFollowPopoverOutsideClose() {
  if (loginRequiredFollowPopoverOutsideWired) return;
  loginRequiredFollowPopoverOutsideWired = true;
  document.addEventListener(
    "pointerdown",
    (event) => {
      const p = ensureLoginRequiredFollowPopover();
      if (p.hidden) return;
      const t = event.target;
      if (t && (p === t || p.contains(t))) return;
      if (t && t.closest?.("[data-follow-toggle][data-pubkey]")) return;
      p.hidden = true;
    },
    true,
  );
}

function showLoginRequiredFollowPopover(anchor) {
  const pop = ensureLoginRequiredFollowPopover();
  positionPopoverNearAnchor(anchor, pop, { maxWidth: 320, fallbackHeight: 48 });
  pop.hidden = false;
  wireLoginRequiredFollowPopoverOutsideClose();
}

function followStateFor(viewer) {
  if (!viewer) return {
    following: new Set(), relayHints: new Map(), contactTags: new Map(), preservedTags: [], content: "",
  };
  const cached = followStateCache.get(viewer);
  if (cached) return cached;
  const state = {
    following: new Set(),
    relayHints: new Map(),
    contactTags: new Map(),
    preservedTags: [],
    content: "",
    loaded: false,
    loading: null,
    loadError: null,
  };
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
  state.loading = fetchMentions(viewer)
    .then(async (payload) => {
      const candidates = Array.isArray(payload?.candidates) ? payload.candidates : [];
      state.following.clear();
      state.relayHints.clear();
      state.contactTags.clear();
      const sourceTags = Array.isArray(payload?.follow_event?.tags) ? payload.follow_event.tags : [];
      state.preservedTags = sourceTags
        .filter((tag) => !Array.isArray(tag) || tag[0] !== "p")
        .map((tag) => (Array.isArray(tag) ? [...tag] : tag));
      state.content = String(payload?.follow_event?.content ?? "");
      sourceTags.forEach((tag) => {
        if (!Array.isArray(tag) || tag[0] !== "p") return;
        const pubkey = normalizePubkey(tag[1]);
        if (pubkey && !state.contactTags.has(pubkey)) state.contactTags.set(pubkey, [...tag]);
      });
      candidates.forEach((raw) => {
        if (String(raw?.source || "") !== "contact") return;
        const candidate = normalizeFollowCandidate(raw);
        if (!candidate) return;
        state.following.add(candidate.pubkey);
        if (candidate.relays.length > 0) state.relayHints.set(candidate.pubkey, candidate.relays[0]);
      });
      state.loaded = true;
      state.loadError = null;
    })
    .catch((error) => {
      console.warn("follow: mention candidates request failed", error);
      state.loaded = false;
      state.loadError = error;
    })
    .finally(() => {
      state.loading = null;
    });
  await state.loading;
  return state;
}

export async function viewerHasAtLeastOneFollow(viewer) {
  const state = await loadFollowState(viewer);
  return state.following.size > 0;
}

function followTagsForState(following, relayHints, contactTags, preservedTags) {
  const contacts = [...following]
    .sort()
    .map((pubkey) => {
      const existing = contactTags.get(pubkey);
      if (existing) return [...existing];
      const relay = relayHints.get(pubkey) || "";
      if (!relay) return ["p", pubkey];
      return ["p", pubkey, relay];
    });
  return [...contacts, ...preservedTags.map((tag) => (Array.isArray(tag) ? [...tag] : tag))];
}

async function publishFollowList(state, following) {
  const draft = {
    kind: 3,
    created_at: Math.floor(Date.now() / 1000),
    tags: followTagsForState(following, state.relayHints, state.contactTags, state.preservedTags),
    content: state.content,
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
  if (viewer) {
    void loadFollowState(viewer).then((state) => refreshFollowButtons(root, state));
  }
  syncProfileFollowGuestAria(root);
  if (followClickDelegateBound) return;
  followClickDelegateBound = true;
  document.addEventListener("click", (event) => {
    const button = event.target.closest?.("[data-follow-toggle][data-pubkey]");
    if (!(button instanceof HTMLButtonElement)) return;
    event.preventDefault();
    const target = String(button.getAttribute("data-pubkey") || "").toLowerCase();
    if (!target) return;
    const signer = activeSignerState();
    if (!signer.isLoggedIn) {
      showLoginRequiredFollowPopover(button);
      return;
    }
    if (button.dataset.loading === "1") return;
    const currentViewer = normalizedPubkey();
    if (!currentViewer || target === currentViewer) return;
    if (!signer.canSign) {
      button.dataset.error = "1";
      button.title = "A signing-capable login is required.";
      return;
    }
    button.dataset.loading = "1";
    button.classList.add("is-pressed");
    button.disabled = true;
    void runSerializedFollowWrite(async () => {
      const state = await loadFollowState(currentViewer);
      if (!state.loaded) throw state.loadError || new Error("Could not load the current follow list.");
      const nextFollowing = new Set(state.following);
      const isFollowing = nextFollowing.has(target);
      if (isFollowing) nextFollowing.delete(target);
      else nextFollowing.add(target);
      await publishFollowList(state, nextFollowing);
      if (isFollowing) {
        state.contactTags.delete(target);
      } else if (!state.contactTags.has(target)) {
        const relay = state.relayHints.get(target) || "";
        state.contactTags.set(target, relay ? ["p", target, relay] : ["p", target]);
      }
      state.following = nextFollowing;
      refreshFollowButtons(document, state);
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

function muteStateFor(viewer) {
  if (!viewer) return { muted: new Set(), loaded: false, loading: null, loadError: null };
  const cached = muteStateCache.get(viewer);
  if (cached) return cached;
  const state = { muted: new Set(), loaded: false, loading: null, loadError: null };
  muteStateCache.set(viewer, state);
  return state;
}

/** Kind-10000 `p` tags from the muted set (sorted hex). Reserve one tag for client metadata. */
function tagsFromMutedPubkeys(mutedSet) {
  const keys = [...mutedSet].sort();
  const sliced = keys.slice(0, MUTE_P_TAG_LIMIT);
  return sliced.map((pk) => ["p", pk]);
}

function ensureMuteSessionListener() {
  if (ensureMuteSessionListener.done) return;
  ensureMuteSessionListener.done = true;
  window.addEventListener("ptxt:session", () => {
    muteStateCache.clear();
    const viewer = normalizedPubkey();
    if (!viewer) return;
    refreshMuteButtonsAfterLoad(viewer, document);
  });
}

function refreshMuteButtonsAfterLoad(viewer, root) {
  if (!viewer) return;
  void loadMuteState(viewer).then((state) => refreshMuteButtons(root, state));
}

async function loadMuteState(viewer) {
  if (!viewer) return muteStateFor("");
  const state = muteStateFor(viewer);
  if (state.loaded || state.loading) {
    if (state.loading) await state.loading;
    return state;
  }
  state.loading = (async () => {
    try {
      const payload = await fetchMuteList(viewer);
      if (!Array.isArray(payload?.muted_pubkeys)) {
        console.warn("mute: mute list missing or invalid muted_pubkeys");
        muteStateCache.delete(viewer);
        return;
      }
      const respHex = normalizePubkey(payload?.pubkey);
      const viewerHex = normalizePubkey(viewer);
      if (!respHex || !viewerHex || respHex !== viewerHex) {
        console.warn("mute: mute list pubkey mismatch");
        muteStateCache.delete(viewer);
        return;
      }
      const rawList = payload.muted_pubkeys;
      state.muted = new Set();
      for (const pk of rawList) {
        const h = String(pk || "").toLowerCase();
        if (h) state.muted.add(h);
      }
      state.loaded = true;
      state.loadError = null;
    } catch (error) {
      console.warn("mute: mute list request failed", error);
      state.loaded = false;
      state.loadError = error;
    } finally {
      state.loading = null;
    }
  })();
  await state.loading;
  return state;
}

function setMuteButtonState(button, muted) {
  button.setAttribute("aria-pressed", muted ? "true" : "false");
  if (button.hasAttribute("data-mute-bracket-labels")) {
    button.textContent = muted ? "[unmute]" : "[mute]";
  } else {
    button.textContent = muted ? "Unmute" : "Mute";
  }
}

/** Hide or show note/reply shells whose `data-reply-pubkey` matches (linear layout / feed). */
function setReplyPubkeyElementsHidden(pubkeyHex, hidden) {
  const target = normalizePubkey(pubkeyHex) || String(pubkeyHex || "").trim().toLowerCase();
  if (!target) return;
  document.querySelectorAll("[data-reply-pubkey]").forEach((el) => {
    const pk = normalizePubkey(el.getAttribute("data-reply-pubkey") || "");
    if (pk !== target) return;
    el.toggleAttribute("hidden", hidden);
  });
}

export function syncMuteToggleButtons(root = document) {
  const viewer = normalizedPubkey();
  if (!viewer) {
    root.querySelectorAll("[data-mute-toggle][data-pubkey]").forEach((node) => {
      if (node instanceof HTMLElement) node.hidden = true;
    });
    return;
  }
  refreshMuteButtonsAfterLoad(viewer, root);
}

async function publishMuteList(tags, content) {
  const tagRows = (tags || []).map((row) => (Array.isArray(row) ? row.slice() : row));
  const draft = {
    kind: 10000,
    created_at: Math.floor(Date.now() / 1000),
    tags: tagRows.length > MUTE_P_TAG_LIMIT ? tagRows.slice(0, MUTE_P_TAG_LIMIT) : tagRows,
    content: String(content ?? ""),
  };
  const signed = await signEventDraft(draft, getSession());
  await publishSignedEvent(signed);
}

function refreshMuteButtons(root, state) {
  const viewer = normalizedPubkey();
  root.querySelectorAll("[data-mute-toggle][data-pubkey]").forEach((button) => {
    if (!(button instanceof HTMLButtonElement)) return;
    const target = String(button.getAttribute("data-pubkey") || "").toLowerCase();
    if (!target) return;
    button.hidden = !viewer || target === viewer;
    if (state.loaded) setMuteButtonState(button, state.muted.has(target));
  });
}

function bindMuteActions(root) {
  ensureMuteSessionListener();
  const viewer = normalizedPubkey();
  if (!viewer) return;
  refreshMuteButtonsAfterLoad(viewer, root);
  if (root._ptxtMuteDelegateBound) return;
  root._ptxtMuteDelegateBound = true;
  root.addEventListener("click", (event) => {
    const button = event.target.closest?.("[data-mute-toggle][data-pubkey]");
    if (!(button instanceof HTMLButtonElement)) return;
    event.preventDefault();
    if (button.dataset.loading === "1") return;
    const target = String(button.getAttribute("data-pubkey") || "").toLowerCase();
    const currentViewer = normalizedPubkey();
    if (!target || !currentViewer || target === currentViewer) return;
    const signer = activeSignerState();
    if (!signer.isLoggedIn) {
      window.location.assign(withRelayParams("/login"));
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
    void runSerializedMuteWrite(async () => {
      const state = await loadMuteState(currentViewer);
      if (!state.loaded) throw state.loadError || new Error("Could not load the current mute list.");
      const shouldMute = !state.muted.has(target);
      const nextMuted = new Set(state.muted);
      if (shouldMute) {
        if (nextMuted.size >= MUTE_P_TAG_LIMIT) {
          throw new Error("mute list exceeds tag limit");
        }
        nextMuted.add(target);
      } else {
        nextMuted.delete(target);
      }
      const nextTags = tagsFromMutedPubkeys(nextMuted);
      if (
        !window.confirm(
          "Publish this mute list as public hex pubkeys only (p tags). Other fields on your kind 10000 event from other clients (for example encrypted entries) will be removed. Continue?",
        )
      ) {
        return;
      }
      await publishMuteList(nextTags, "");
      state.muted = nextMuted;
      refreshMuteButtons(root, state);
      setReplyPubkeyElementsHidden(target, shouldMute);
    })
      .catch((error) => {
        console.warn("mute: publish failed", error);
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
  if (!content) return [];
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
  let normalized = [];
  try {
    const payload = await fetchMentions(viewer, { rootID });
    const rawCandidates = Array.isArray(payload?.candidates) ? payload.candidates : [];
    normalized = rawCandidates
      .map((candidate) => normalizeMentionCandidate(candidate, candidate?.source))
      .filter(Boolean);
  } catch (error) {
    console.warn("mentions: request failed", error);
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
  if (!state?.content) return;
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
  if (state?.overlay instanceof HTMLElement) {
    state.overlay.hidden = true;
    state.overlay.textContent = "";
  }
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
  syncComposerPublishValidity(state);
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

/** Clears document-level Escape listener for the thread inline composer (at most one). */
let threadInlineEscTeardown = null;

/** ResizeObserver + textarea listener for the thread inline reply rail column. */
let threadInlineRailTeardown = null;

/**
 * True when this draft sits among other direct replies to the same parent (show a full
 * descending rail beside the form). False for a lone direct reply (short connector above
 * the avatar only).
 */
function threadInlineHasSiblingBranch(anchorEl) {
  if (!(anchorEl instanceof Element)) return false;
  const replyRoot = document.getElementById("thread-replies");
  if (!replyRoot) return false;
  if (anchorEl.matches?.("article.note")) {
    return replyRoot.querySelectorAll(":scope > .comment").length >= 1;
  }
  if (anchorEl.classList?.contains?.("comment")) {
    const parent = anchorEl.parentElement;
    if (parent?.matches?.(".comments")) {
      return parent.querySelectorAll(":scope > .comment").length >= 2;
    }
  }
  return false;
}

function syncThreadInlineRailGlyphs(host) {
  if (host?.dataset?.inlineRailMode !== "branch") return;
  const pre = host?.querySelector?.(".thread-inline-reply__rail-pre");
  const row = host?.querySelector?.(".thread-inline-reply__rail-row");
  if (!(pre instanceof HTMLElement) || !(row instanceof HTMLElement)) return;
  const h = row.clientHeight;
  const cs = getComputedStyle(pre);
  const fs = parseFloat(cs.fontSize) || 16;
  const lhRaw = cs.lineHeight;
  const lhParsed = parseFloat(lhRaw);
  const linePx = Number.isFinite(lhParsed) && lhParsed > 0 ? lhParsed : fs * 1.25;
  let lines = Math.ceil(h / linePx) + 4;
  if (!Number.isFinite(lines) || lines < 12) lines = 12;
  lines = Math.min(lines, 400);
  pre.textContent = Array.from({ length: lines }, () => "|").join("\n");
}

function bindThreadInlineRailSync(host, state) {
  threadInlineRailTeardown?.();
  threadInlineRailTeardown = null;
  const preClear = host.querySelector(".thread-inline-reply__rail-pre");
  if (host.dataset.inlineRailMode !== "branch") {
    if (preClear) preClear.textContent = "";
    return;
  }
  const row = host.querySelector(".thread-inline-reply__rail-row");
  const sync = () => {
    if (!host.isConnected) return;
    syncThreadInlineRailGlyphs(host);
  };
  let ro;
  if (row && typeof ResizeObserver !== "undefined") {
    ro = new ResizeObserver(() => {
      sync();
    });
    ro.observe(row);
  }
  state.content?.addEventListener("input", sync);
  threadInlineRailTeardown = () => {
    ro?.disconnect();
    state.content?.removeEventListener("input", sync);
    threadInlineRailTeardown = null;
  };
  queueMicrotask(() => {
    sync();
    requestAnimationFrame(sync);
  });
}

function restoreComposerFormToDialog(state) {
  if (!state?.form || !state.dialog) return;
  const status = state.status;
  if (status?.parentNode === state.dialog) {
    status.insertAdjacentElement("afterend", state.form);
  } else {
    state.dialog.append(state.form);
  }
}

function dismissInlineComposerUI(state) {
  if (!state?.form) return;
  const host = state.form.closest(".thread-inline-reply");
  if (!host) return;
  threadInlineRailTeardown?.();
  threadInlineRailTeardown = null;
  threadInlineEscTeardown?.();
  threadInlineEscTeardown = null;
  if (state.content && Object.prototype.hasOwnProperty.call(state.content.dataset, COMPOSER_PLACEHOLDER_PREV)) {
    state.content.placeholder = state.content.dataset[COMPOSER_PLACEHOLDER_PREV] || "";
    delete state.content.dataset[COMPOSER_PLACEHOLDER_PREV];
  }
  restoreComposerFormToDialog(state);
  host.remove();
  state.form.classList.remove("composer-form--thread-inline");
}

async function refreshComposerMentionsAndOverlay(root, state, mentionRootId) {
  await loadMentionCandidates(root, state, mentionRootId || "");
  renderComposerOverlay(state);
  closeMentionMenu(state);
}

function applyComposerReadyStatus(state, mode) {
  const signer = activeSignerState();
  if (!signer.isLoggedIn) {
    setStatus(state, "Log in first to publish.");
  } else if (!signer.canSign) {
    setStatus(state, desktopModeEnabled()
      ? "Use Nsec login or Sign up to sign events."
      : "Use Browser Extension, Nsec login, or Sign up to sign events.");
  } else if (mode === "repost") {
    setStatus(state, "Leave content blank for a repost, or add text for a quote post.");
  } else {
    setStatus(state, mode === "reply" ? "Publishing a signed reply event." : "Publishing a signed kind 1 note.");
  }
}

/**
 * Opens the reply composer docked under a thread note (ASCII-style shell).
 * Reuses the shared composer form; restores it to the dialog on cancel/submit/close.
 */
export async function openThreadInlineComposer(root, opts) {
  const state = composeState(root);
  if (!state?.form || !opts?.anchorEl) return;
  dismissInlineComposerUI(state);
  clearBlossomComposer(state);
  if (state.mode) state.mode.value = "reply";
  if (state.title) state.title.textContent = "Write a reply";
  state.rootID.value = opts.rootID || opts.targetID || "";
  state.replyID.value = opts.targetID || "";
  state.replyPubKey.value = opts.pubkey || "";
  clearRepostContext(state);
  if (state.previewWrap) state.previewWrap.hidden = true;
  if (state.previewContent) state.previewContent.textContent = "";
  // Opening the composer must not wait on relay-backed contact/profile reads.
  // Real signed-in accounts can take several seconds to hydrate mention
  // candidates; build and focus the form first, then enrich it in place.
  renderComposerOverlay(state);
  closeMentionMenu(state);
  applyComposerReadyStatus(state, "reply");
  syncComposerControls(state);
  state.form.classList.add("composer-form--thread-inline");
  const host = buildThreadInlineComposerHost(state, opts);
  host.querySelector("[data-inline-composer-upload]")?.addEventListener("click", () => {
    state.addImageBtn?.click();
  });
  host.querySelector("[data-inline-composer-cancel]")?.addEventListener("click", () => {
    closeComposer(state);
  });
  host.querySelector("[data-inline-composer-publish]")?.addEventListener("click", () => {
    state.form.requestSubmit();
  });
  const mirror = host.querySelector("[data-inline-composer-status]");
  if (mirror && state.status) {
    mirror.textContent = inlineReplyStatusMirrorText(host, state.status.textContent);
  }
  if (state.content) {
    if (!Object.prototype.hasOwnProperty.call(state.content.dataset, COMPOSER_PLACEHOLDER_PREV)) {
      state.content.dataset[COMPOSER_PLACEHOLDER_PREV] = state.content.getAttribute("placeholder") || "";
    }
    state.content.placeholder = "type your reply here";
  }
  threadInlineEscTeardown?.();
  const escHandler = (e) => {
    if (e.key !== "Escape") return;
    if (!state.form.isConnected || !state.form.closest(".thread-inline-reply")) {
      threadInlineEscTeardown?.();
      threadInlineEscTeardown = null;
      return;
    }
    closeComposer(state);
  };
  document.addEventListener("keydown", escHandler, true);
  threadInlineEscTeardown = () => {
    document.removeEventListener("keydown", escHandler, true);
    threadInlineEscTeardown = null;
  };
  bindThreadInlineRailSync(host, state);
  closeComposerDialogShell(state);
  const focusSigner = activeSignerState();
  if (state.content && focusSigner.isLoggedIn && focusSigner.canSign) {
    queueMicrotask(() => state.content.focus());
  }
  void refreshComposerMentionsAndOverlay(root, state, state.rootID.value).catch((error) => {
    console.warn("mentions: inline composer hydration failed", error);
  });
}

function closeComposerDialogShell(state) {
  if (!state?.dialog) return;
  if (typeof state.dialog.close === "function") {
    if (state.dialog.open) state.dialog.close();
  } else if (state.dialog.hasAttribute("open")) {
    state.dialog.removeAttribute("open");
  }
}

function buildThreadInlineComposerHost(state, opts) {
  const { anchorEl, replyingToLabel = "" } = opts;
  const host = document.createElement("section");
  host.className = "thread-inline-reply";
  host.setAttribute("role", "region");
  host.setAttribute("aria-label", "Reply composer");
  const depthRaw =
    anchorEl.getAttribute?.("data-depth") ||
    (anchorEl.dataset && anchorEl.dataset.depth) ||
    anchorEl.closest?.("[data-depth]")?.getAttribute("data-depth") ||
    "";
  let anchorDepth = Number.parseInt(String(depthRaw), 10);
  if (!Number.isFinite(anchorDepth)) {
    anchorDepth = anchorEl.matches?.("article.note") ? 0 : 1;
  }
  /* The inline form is inserted as a sibling of its anchor, so its rail must
     stay in the anchor's existing reply column. Root articles have no comment
     depth and begin the reply rail at depth 1. */
  let composeDepth;
  if (anchorEl.matches?.("article.thread-focus-selected")) {
    composeDepth = 1;
  } else if (anchorEl.classList?.contains?.("comment")) {
    composeDepth = Math.min(Math.max(anchorDepth, 1), 20);
  } else {
    composeDepth = Math.min(Math.max(anchorDepth + 1, 1), 20);
  }
  host.dataset.depth = String(composeDepth);
  host.style.setProperty("--depth", String(composeDepth));
  host.dataset.inlineRailMode = threadInlineHasSiblingBranch(anchorEl) ? "branch" : "leaf";

  const displayName =
    document.querySelector("[data-session-display-name]")?.textContent?.trim() || "you";
  const profileHref =
    document.querySelector("a[data-session-user-link]")?.getAttribute("href") || "/settings";

  const railImg = document.querySelector(".rail-user img[data-session-avatar]");
  const showRailAvatar = railImg instanceof HTMLImageElement && railImg.src && !railImg.hidden;
  host.innerHTML = `
    <a class="comment-avatar thread-inline-reply__avatar" href="${escapeHTML(profileHref)}" data-relay-aware aria-hidden="true" tabindex="-1"></a>
    <div class="thread-inline-reply__card">
      <div class="thread-inline-reply__header">
        <strong class="thread-inline-reply__header-name" data-inline-composer-display-name></strong>
        <span class="thread-inline-reply__header-age muted"> -- now</span>
        <span class="thread-inline-reply__header-pad" aria-hidden="true"></span>
        <span class="thread-inline-reply__header-dots muted">[...] +</span>
      </div>
      <div class="thread-inline-reply__rail-row">
        <div class="thread-inline-reply__rail-track" aria-hidden="true">
          <pre class="thread-inline-reply__rail-pre"></pre>
        </div>
        <div class="thread-inline-reply__form-col">
          <div class="thread-inline-reply__form-host" data-inline-composer-form-host></div>
          <p class="thread-inline-reply__status muted" data-inline-composer-status></p>
          <div class="thread-inline-reply__footer">
            <button type="button" class="link-button thread-inline-reply__bracket-btn" data-inline-composer-upload>[upload]</button>
            <span class="thread-inline-reply__footer-rule" aria-hidden="true"></span>
            <span class="thread-inline-reply__footer-actions">
              <button type="button" class="link-button thread-inline-reply__bracket-btn" data-inline-composer-cancel>[cancel]</button>
              <button type="button" class="link-button thread-inline-reply__bracket-btn" data-inline-composer-publish>[publish]</button>
            </span>
          </div>
        </div>
      </div>
    </div>
  `;
  const nameEl = host.querySelector("[data-inline-composer-display-name]");
  if (nameEl) nameEl.textContent = displayName;
  const av = host.querySelector(".thread-inline-reply__avatar");
  if (showRailAvatar) {
    const img = document.createElement("img");
    img.src = railImg.src;
    img.alt = "";
    img.loading = "lazy";
    img.decoding = "async";
    av?.replaceChildren(img);
  } else if (av) {
    av.textContent = "";
  }
  const cleaned = String(replyingToLabel || "").replace(/^@+/, "").trim();
  if (cleaned) {
    host.dataset.inlineReplyingTo = cleaned;
  } else {
    delete host.dataset.inlineReplyingTo;
  }
  const formHost = host.querySelector("[data-inline-composer-form-host]");
  formHost.append(state.form);
  anchorEl.insertAdjacentElement("afterend", host);
  return host;
}

async function openComposer(root, state, mode, context = {}) {
  if (!state) return;
  if (mode === "reply") {
    const targetID = context.targetID || "";
    const anchor = context.anchorEl || visibleReplyAnchorForTarget(targetID);
    if (document.getElementById("thread-summary") && anchor) {
      await openThreadInlineComposer(root, {
        anchorEl: anchor,
        rootID: context.rootID || targetID || "",
        targetID,
        pubkey: context.pubkey || "",
        replyingToLabel: context.replyingTo || "",
      });
      return;
    }
    return;
  }
  dismissInlineComposerUI(state);
  clearBlossomComposer(state);
  if (state.mode) {
    state.mode.value = mode === "repost" ? "repost" : "post";
  }
  if (state.title) {
    if (mode === "repost") {
      state.title.textContent = "Repost";
    } else {
      state.title.textContent = "Write a post";
    }
  }
  if (mode === "repost") {
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
  await refreshComposerMentionsAndOverlay(root, state, "");
  applyComposerReadyStatus(state, mode);
  syncComposerControls(state);
  if (typeof state.dialog.showModal === "function") {
    state.dialog.showModal();
  } else {
    state.dialog.setAttribute("open", "");
  }
  const focusSigner = activeSignerState();
  if (state.content && focusSigner.isLoggedIn && focusSigner.canSign) {
    queueMicrotask(() => state.content.focus());
  }
}

function visibleReplyAnchorForTarget(targetID) {
  if (!targetID) return null;
  const selector = `#note-${CSS.escape(targetID)}`;
  const candidates = [...document.querySelectorAll(selector)];
  return candidates.find((node) => {
    if (!(node instanceof HTMLElement)) return false;
    if (node.hidden || node.closest("[hidden]")) return false;
    const rect = node.getBoundingClientRect();
    return rect.width > 0 && rect.height > 0;
  }) || (document.getElementById(`note-${targetID}`) instanceof HTMLElement
    ? document.getElementById(`note-${targetID}`)
    : null);
}

function closeComposer(state) {
  if (!state) return;
  closeMentionMenu(state);
  clearBlossomComposer(state);
  dismissInlineComposerUI(state);
  closeComposerDialogShell(state);
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

function hrefForThreadInlineReplyNavigation(rootId, targetId) {
  const pathId = rootId || targetId;
  if (!pathId) return "";
  const cur = new URL(window.location.href);
  const next = new URL(`/thread/${pathId}`, cur.origin);
  next.search = cur.search;
  if (targetId) {
    next.searchParams.set("selected", targetId);
    next.hash = `#note-${targetId}`;
  }
  return next.toString();
}

function bindReplyActions(root) {
  if (!root || root._ptxtReplyDelegateBound) return;
  root._ptxtReplyDelegateBound = true;
  root.addEventListener("click", (event) => {
    const link = event.target.closest("a[data-reply-action],a[href^='/thread/'],button[data-reply-action],button[data-repost-action]");
    if (!link) return;
    const inReplyContainer =
      link.closest(".comment, article.note, [data-thread-tree-note]") ||
      link.closest("[data-reply-target-id]");
    if (!inReplyContainer) return;
    const state = composeState(root) || composeState(document);
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
    const rootID = link.getAttribute("data-reply-root-id") || inReplyContainer.getAttribute("data-reply-root-id") || "";
    const targetID = link.getAttribute("data-reply-target-id") || inReplyContainer.getAttribute("data-reply-target-id") || "";
    const pubkey = link.getAttribute("data-reply-pubkey") || inReplyContainer.getAttribute("data-reply-pubkey") || "";
    event.preventDefault();
    const replyingTo = inReplyContainer.getAttribute("data-ascii-author") || "";
    if (!document.getElementById("thread-summary") && targetID) {
      try {
        sessionStorage.setItem(
          THREAD_INLINE_REPLY_PENDING_KEY,
          JSON.stringify({
            targetID,
            rootID,
            pubkey,
            replyingTo,
          }),
        );
      } catch {
        /* ignore */
      }
      const href = hrefForThreadInlineReplyNavigation(rootID, targetID);
      if (href) {
        window.location.assign(withRelayParams(href));
      }
      return;
    }
    const anchor = inReplyContainer instanceof HTMLElement
      ? inReplyContainer
      : visibleReplyAnchorForTarget(targetID);
    if (anchor) {
      void openThreadInlineComposer(root, {
        anchorEl: anchor,
        rootID,
        targetID,
        pubkey,
        replyingToLabel: replyingTo,
      });
    }
  });
}

function bindComposer(root) {
  const state = composeState(root) || composeState(document);
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
      syncComposerControls(state);
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
  if (!state.form._ptxtBlossomUiBound) {
    state.form._ptxtBlossomUiBound = true;
    state.addImageBtn?.addEventListener("click", () => state.imageInput?.click());
    state.imageInput?.addEventListener("change", async () => {
      const input = state.imageInput;
      if (!(input instanceof HTMLInputElement) || !input.files?.length) return;
      const files = [...input.files];
      input.value = "";
      const signer = activeSignerState();
      if (!signer.isLoggedIn || !signer.canSign) {
        setStatus(state, "Login required to upload.");
        return;
      }
      const servers = getBlossomServerURLs();
      let uploadFailed = false;
      for (const file of files) {
        if (!file.type.startsWith("image/")) continue;
        try {
          setStatus(state, "Uploading image…");
          const { descriptor, imetaTag } = await blossomUploadBlob(file, { servers });
          state.blossomAttachments.push({ descriptor, imetaTag, name: file.name });
          renderComposerAttachments(state);
        } catch (err) {
          uploadFailed = true;
          setStatus(state, err instanceof Error ? err.message : "Upload failed.");
          break;
        }
      }
      if (!uploadFailed) {
        setStatus(state, "Ready to publish.");
      }
      syncComposerMediaRow(state);
    });
    state.attachmentStrip?.addEventListener("click", (event) => {
      const btn = event.target.closest("[data-composer-remove-attachment]");
      if (!(btn instanceof HTMLElement)) return;
      const idx = Number.parseInt(btn.getAttribute("data-composer-remove-attachment") || "", 10);
      if (!Number.isFinite(idx)) return;
      state.blossomAttachments.splice(idx, 1);
      renderComposerAttachments(state);
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
    const { mode, atts, mergedRaw, validityMsg } = composerPublishContent(state);
    if (validityMsg) {
      setStatus(state, validityMsg);
      return;
    }
    const content = expandMentionsForPublish(state, mergedRaw);
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
    const isQuote = isRepost && mergedRaw.length > 0;
    const draftKind = isRepost && !isQuote ? 6 : 1;
    let tags = [];
    if (isReply) {
      tags = buildReplyTags(state.rootID.value, state.replyID.value, state.replyPubKey.value);
    } else if (isRepost) {
      tags = buildReferenceTags(isQuote ? "quote" : "repost", repostID, repostPubKey, repostRelay);
    }
    atts.forEach((a) => {
      if (Array.isArray(a.imetaTag)) tags.push(a.imetaTag);
    });
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
      const plannedRelays = await planPublishTargets(signed).catch(() => []);
      const pendingState = pendingPublishStatus({
        phaseTitle: "Broadcasting to relays",
        statusMessage: "Preparing relay broadcast...",
        plannedRelays,
        statusMessages: [
          "signing event...",
          "preparing relay broadcast...",
          "broadcasting to relays...",
        ],
        completionMessage: "publish complete.",
      });
      showPublishStatusSheet(null, { initialState: pendingState });
      const payload = await publishSignedEvent(signed);
      showPublishStatusSheet(payload, { initialState: pendingState });
      state.content.value = "";
      if (state.mentionByName) state.mentionByName.clear();
      renderComposerOverlay(state);
      clearComposeContext(state);
      clearRepostContext(state);
      closeComposer(state);
      const href = withRelayParams(`/thread/${encodeURIComponent(signed.id)}`);
      window.location.assign(href);
    } catch (error) {
      setStatus(state, error instanceof Error ? error.message : "Failed to publish event.");
    } finally {
      syncComposerPublishValidity(state);
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
    const metadata = await fetchViewerProfile(pubkey);
    assignIfEmpty(form, "display_name", metadata.display_name);
    assignIfEmpty(form, "name", metadata.name);
    assignIfEmpty(form, "about", metadata.about);
    assignIfEmpty(form, "picture", metadata.picture);
    assignIfEmpty(form, "website", metadata.website);
    assignIfEmpty(form, "nip05", metadata.nip05);
    assignIfEmpty(form, "lud16", metadata.lud16);
    const pk = normalizedPubkey();
    if (pk) {
      const nextLabel = sessionAuthorLabelFromMetadata(metadata, pk);
      if (nextLabel !== shortPubkey(pk)) setSession({ ...getSession(), profileLabel: nextLabel });
    }
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

function relayUsageFromEntry(entry) {
  if (entry?.read === true && entry?.write === false) return "read";
  if (entry?.write === true && entry?.read === false) return "write";
  return "any";
}

function relayFallbackGlyph(relay) {
  const label = relayHostLabel(relay).replace(/^www\./, "");
  const first = label.match(/[a-z0-9]/i)?.[0] || "r";
  return first.toUpperCase();
}

function relayCardHTML(relay, {
  meta = "",
  actionHTML = "",
  itemAttrs = "",
  title = "",
} = {}) {
  const displayTitle = title || relayHostLabel(relay);
  const metaHTML = meta ? `<span class="muted relay-card-meta">${escapeHTML(meta)}</span>` : "";
  const actionsHTML = actionHTML ? `<span class="relay-card-actions">${actionHTML}</span>` : "";
  return `<li class="relay-card" data-relay-info-card data-relay-url="${escapeHTML(relay)}"${itemAttrs ? ` ${itemAttrs}` : ""}>
    <span class="relay-card-icon" data-relay-icon aria-hidden="true">${escapeHTML(relayFallbackGlyph(relay))}</span>
    <span class="relay-card-main">
      <span class="relay-card-header">
        <strong class="relay-card-title" data-relay-title>${escapeHTML(displayTitle)}</strong>
        ${metaHTML}
      </span>
      <code class="relay-card-url relay-url-popover" data-check-relay="${escapeHTML(relay)}" tabindex="0" role="button">${escapeHTML(relay)}</code>
      <span class="muted relay-card-description" data-relay-description hidden></span>
      <span class="relay-card-nips" data-relay-nips hidden></span>
    </span>
    ${actionsHTML}
  </li>`;
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

function relayEntriesFromPreferences(preferences = []) {
  return preferences.map((item) => ({
    url: item.url,
    read: item.usage !== "write",
    write: item.usage !== "read",
  }));
}

function relayPreferencesFromEntries(entries = []) {
  return (Array.isArray(entries) ? entries : [])
    .map((entry) => ({
      url: normalizeRelayURL(entry?.url || ""),
      usage: relayUsageFromEntry(entry),
    }))
    .filter((entry) => entry.url);
}

function renderSimpleRelayList(node, relays, emptyMessage) {
  if (!node) return;
  if (!Array.isArray(relays) || relays.length === 0) {
    node.innerHTML = `<li class="muted">${emptyMessage}</li>`;
    return;
  }
  node.innerHTML = relays
    .map((relay) => {
      const normalized = normalizeRelayURL(relay);
      return normalized ? relayCardHTML(normalized) : "";
    })
    .filter(Boolean)
    .join("");
  hydrateRelayInfoCards(node);
}

function renderOutboxSummary(node, payload) {
  if (!node) return;
  const active = Array.isArray(payload?.active_outbox_relays) ? payload.active_outbox_relays : [];
  const feedAuthorCount = Number(payload?.feed_author_count || 0);
  const cachedHintAuthorCount = Number(payload?.cached_hint_author_count || 0);
  if (feedAuthorCount <= 0) {
    node.textContent = "Outbox routing activates once there are feed authors to route.";
    return;
  }
  if (active.length === 0) {
    node.textContent = `${cachedHintAuthorCount} of ${feedAuthorCount} feed authors have cached NIP-65 hints. Using effective defaults until outbox routes warm up.`;
    return;
  }
  node.textContent = `${cachedHintAuthorCount} of ${feedAuthorCount} feed authors have cached NIP-65 hints. ${active.length} active outbox relay${active.length === 1 ? "" : "s"} currently cover those routes.`;
}

function renderInsightRelayList(node, relays, emptyMessage, options = {}) {
  if (!node) return;
  if (!Array.isArray(relays) || relays.length === 0) {
    node.innerHTML = `<li class="muted">${emptyMessage}</li>`;
    return;
  }
  const showAdd = options.showAdd === true;
  node.innerHTML = relays.map((item) => {
    const relay = normalizeRelayURL(item?.url || "");
    if (!relay) return "";
    const usage = normalizeRelayUsage(item?.usage);
    const sources = Array.isArray(item?.sources) ? item.sources.map((source) => String(source || "").trim()).filter(Boolean) : [];
    const status = String(item?.status || "").trim();
    const confidence = String(item?.confidence || "").trim();
    const meta = [relayUsageLabel(usage), sources.join("+"), confidence, status].filter(Boolean).join(" | ");
    const addButton = showAdd
      ? `<button type="button" data-relay-preference-suggested="${escapeHTML(relay)}" data-relay-preference-suggested-usage="${escapeHTML(usage)}">add</button>`
      : "";
    return relayCardHTML(relay, { meta, actionHTML: addButton });
  }).filter(Boolean).join("");
  hydrateRelayInfoCards(node);
}

function renderRelayPreferenceDraft(state) {
  if (!state.preferencesList) return;
  if (state.preferences.length === 0) {
    state.preferencesList.innerHTML = `<li class="muted">Add relay preferences here.</li>`;
    return;
  }
  state.preferences.sort((a, b) => a.url.localeCompare(b.url));
  state.preferencesList.innerHTML = state.preferences.map((item) => relayCardHTML(item.url, {
    meta: relayUsageLabel(item.usage),
    actionHTML: `<button type="button" data-relay-preference-remove="${escapeHTML(item.url)}">remove</button>`,
    itemAttrs: `data-relay-preference="${escapeHTML(item.url)}"`,
  })).join("");
  hydrateRelayInfoCards(state.preferencesList);
}

function syncRelayEditVisibility(state, signer = activeSignerState()) {
  const canPublish = Boolean(signer.isLoggedIn && signer.canSign);
  state.canPublish = canPublish;
  if (state.editSection instanceof HTMLElement) {
    state.editSection.hidden = !canPublish;
  }
  if (state.publishActions instanceof HTMLElement) {
    state.publishActions.hidden = !canPublish;
  }
  if (state.submit instanceof HTMLButtonElement) {
    state.submit.hidden = !canPublish;
  }
}

function relayTagsFromPreferences(preferences) {
  return preferences.map((item) => {
    if (item.usage === "read" || item.usage === "write") return ["r", item.url, item.usage];
    return ["r", item.url];
  });
}

export function seedRelayPreferenceDraft({
  publishedRelays = [],
  sessionRelays = [],
  userRelays = [],
  effectiveDefaultRelays = [],
} = {}) {
  if (Array.isArray(publishedRelays) && publishedRelays.length > 0) {
    return publishedRelays
      .map((entry) => ({
        url: normalizeRelayURL(entry?.url || ""),
        usage: normalizeRelayUsage(entry?.usage || "any"),
      }))
      .filter((entry) => entry.url);
  }
  const preferredUserRelays = relayPreferencesFromEntries(
    Array.isArray(userRelays) && userRelays.length > 0
      ? userRelays
      : (Array.isArray(sessionRelays) ? sessionRelays.map((url) => ({ url, read: true, write: true })) : []),
  );
  if (preferredUserRelays.length > 0) {
    return preferredUserRelays;
  }
  const fallbackRelays = Array.isArray(effectiveDefaultRelays) && effectiveDefaultRelays.length > 0
    ? effectiveDefaultRelays
    : DEFAULT_RELAYS;
  return fallbackRelays
    .map((relay) => normalizeRelayURL(relay))
    .filter(Boolean)
    .map((relay) => ({ url: relay, usage: "write" }));
}

function persistRelayPreferences(state, preferences, options = {}) {
  state.preferences = [...preferences];
  state.config = saveRelayConfig({
    ...state.config,
    useUserRelays: options.enableUserRelays === true ? true : state.config.useUserRelays,
    userRelayMetadata: {
      ...state.config.userRelayMetadata,
      relays: relayEntriesFromPreferences(state.preferences),
      updatedAt: options.updatedAt ?? state.config.userRelayMetadata.updatedAt,
    },
  });
  renderRelayPreferenceDraft(state);
}

function syncRelayMetadataFromInsightPayload(state, payload) {
  if (!normalizedPubkey()) return;
  const publishedCreatedAt = Math.max(0, Number.parseInt(`${payload?.published_created_at ?? 0}`, 10) || 0);
  if (publishedCreatedAt < 1) return;
  const current = loadRelayConfig();
  if (publishedCreatedAt <= current.userRelayMetadata.updatedAt) {
    state.config = current;
    return;
  }
  const publishedRelays = Array.isArray(payload?.published_relays) ? payload.published_relays : [];
  const publishedPreferences = relayPreferencesFromEntries(publishedRelays);
  const hadOwnRelays = relayPreferencesFromEntries(current.userRelayMetadata.relays).length > 0;
  state.config = saveRelayConfig({
    ...current,
    useUserRelays: !hadOwnRelays && publishedPreferences.length > 0 ? true : current.useUserRelays,
    userRelayMetadata: {
      relays: relayEntriesFromPreferences(publishedPreferences),
      updatedAt: publishedCreatedAt,
    },
  });
}

function directRelayPreferencesInsightPayload(direct) {
  const relays = Array.isArray(direct?.relays) ? direct.relays : [];
  if (!relays.length) return null;
  return {
    published_event_id: String(direct?.event_id || ""),
    published_created_at: Number(direct?.created_at || 0) || 0,
    published_relays: relays.map((entry) => ({
      url: entry.url,
      usage: normalizeRelayUsage(entry.usage),
      sources: ["published_kind10002"],
      confidence: "high",
      status: "local",
    })),
  };
}

function mergeDirectRelayPreferencesPayload(payload, directPayload) {
  if (!directPayload?.published_relays?.length) return payload;
  const currentCreatedAt = Number(payload?.published_created_at || 0) || 0;
  const directCreatedAt = Number(directPayload.published_created_at || 0) || 0;
  const currentRelays = Array.isArray(payload?.published_relays) ? payload.published_relays : [];
  if (currentRelays.length > 0 && currentCreatedAt > directCreatedAt) return payload;
  return {
    ...payload,
    published_event_id: directPayload.published_event_id || payload?.published_event_id || "",
    published_created_at: Math.max(currentCreatedAt, directCreatedAt),
    published_relays: directPayload.published_relays,
  };
}

async function hydrateDirectRelayPreferences(state, { payload = null, render = false } = {}) {
  const viewer = normalizedPubkey();
  if (!viewer) return payload;
  try {
    const direct = await fetchViewerRelayPreferences(viewer, { forceRefresh: true });
    const directPayload = directRelayPreferencesInsightPayload(direct);
    const merged = mergeDirectRelayPreferencesPayload(payload || {}, directPayload);
    if (!render || merged === payload) return merged;
    syncRelayMetadataFromInsightPayload(state, merged);
    state.config = loadRelayConfig();
    renderInsightRelayList(state.publishedList, merged.published_relays, "No published relay preferences yet.", { showAdd: state.canPublish === true });
    renderSimpleRelayList(state.effectiveList, effectiveReadRelays(state.config), "No effective read relays.");
    if (state.preferences.length === 0) {
      state.preferences = seedRelayPreferenceDraft({
        publishedRelays: merged.published_relays,
        userRelays: state.config.userRelayMetadata.relays,
        effectiveDefaultRelays: state.defaultRelays,
      });
      renderRelayPreferenceDraft(state);
    }
    if (state.statusNode) state.statusNode.textContent = "Loaded published relay preferences from your relays.";
    return merged;
  } catch {
    return payload;
  }
}

async function loadRelayInsight(state) {
  try {
    const viewer = normalizedPubkey();
    const params = new URLSearchParams();
    if (!viewer) params.set("seed_pubkey", DEFAULT_LOGGED_OUT_WOT_SEED_NPUB);
    const url = `/api/relay-insight${params.size ? `?${params}` : ""}`;
    const response = await fetchWithSession(url, { headers: { Accept: "application/json" } });
    if (!response.ok) throw new Error("relay insight request failed");
    let payload = await response.json();
    payload = await hydrateDirectRelayPreferences(state, { payload });
    syncRelayMetadataFromInsightPayload(state, payload);
    const listOptions = { showAdd: state.canPublish === true };
    renderInsightRelayList(state.publishedList, payload.published_relays, "No published relay preferences yet.", listOptions);
    renderInsightRelayList(state.discoveredList, payload.discovered_relays, "No discovered relay hints yet.", listOptions);
    renderInsightRelayList(state.recommendedList, payload.recommended_relays, "No recommendations available yet.", listOptions);
    renderSimpleRelayList(state.effectiveList, effectiveReadRelays(state.config), "No effective read relays.");
    renderSimpleRelayList(state.defaultList, payload.effective_default_relays, "No effective defaults available yet.");
    renderSimpleRelayList(state.outboxList, payload.active_outbox_relays, "No active outbox relays yet.");
    renderOutboxSummary(state.outboxSummary, payload);
    return payload;
  } catch {
    const payload = {
      published_created_at: Number(state.config?.userRelayMetadata?.updatedAt || 0),
      published_relays: relayPreferencesFromEntries(state.config?.userRelayMetadata?.relays || []),
      discovered_relays: [],
      recommended_relays: DEFAULT_RELAYS.map((url) => ({
        url,
        usage: "any",
        sources: ["default"],
        confidence: "default",
        status: "local",
      })),
      effective_default_relays: [...DEFAULT_RELAYS],
      active_outbox_relays: effectiveWriteRelays(state.config),
      feed_author_count: 0,
      cached_hint_author_count: 0,
    };
    const listOptions = { showAdd: state.canPublish === true };
    renderInsightRelayList(state.publishedList, payload.published_relays, "No published relay preferences yet.", listOptions);
    renderInsightRelayList(state.discoveredList, payload.discovered_relays, "No discovered relay hints yet.", listOptions);
    renderInsightRelayList(state.recommendedList, payload.recommended_relays, "No recommendations available yet.", listOptions);
    renderSimpleRelayList(state.effectiveList, effectiveReadRelays(state.config), "No effective read relays.");
    renderSimpleRelayList(state.defaultList, payload.effective_default_relays, "No effective defaults available yet.");
    renderSimpleRelayList(state.outboxList, payload.active_outbox_relays, "No active outbox relays yet.");
    renderOutboxSummary(state.outboxSummary, payload);
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
    publishActions: form.querySelector("[data-relay-preferences-publish-actions]"),
    editSection: root.querySelector("[data-relay-edit-section]"),
    addButton: form.querySelector("[data-relay-preference-add]"),
    fetchButton: form.querySelector("[data-relay-preference-fetch]"),
    useDefaults: form.querySelector("[data-relay-preference-use-defaults]"),
    useRecommended: form.querySelector("[data-relay-preference-use-recommended]"),
    preferencesList: form.querySelector("[data-relay-preferences-list]"),
    publishedList: root.querySelector("[data-relay-insight-published]"),
    discoveredList: root.querySelector("[data-relay-insight-discovered]"),
    recommendedList: root.querySelector("[data-relay-insight-recommended]"),
    effectiveList: root.querySelector("[data-relay-insight-effective]"),
    defaultList: root.querySelector("[data-relay-insight-defaults]"),
    outboxList: root.querySelector("[data-relay-insight-outbox]"),
    outboxSummary: root.querySelector("[data-relay-outbox-summary]"),
    statusNode: root.querySelector("[data-relay-insight-status]"),
    config: loadRelayConfig(),
    preferences: relayPreferencesFromEntries(loadRelayConfig().userRelayMetadata.relays),
    latestRecommended: [],
    defaultRelays: [...DEFAULT_RELAYS],
  };
  syncRelayEditVisibility(state);
  renderRelayPreferenceDraft(state);
  renderSimpleRelayList(state.effectiveList, effectiveReadRelays(state.config), "No effective read relays.");
  if (state.statusNode) state.statusNode.textContent = "Loading relay insight...";
  const relayConfigReady = getSession().method === "nip07"
    ? syncNIP07RelayConfigFromExtension().catch(() => loadRelayConfig())
    : Promise.resolve(loadRelayConfig());
  void relayConfigReady.then((config) => {
    state.config = config;
    renderSimpleRelayList(state.effectiveList, effectiveReadRelays(state.config), "No effective read relays.");
    return hydrateDirectRelayPreferences(state, { render: true });
  }).then(() => loadRelayInsight(state)).then((payload) => {
    if (!payload) return;
    const published = Array.isArray(payload.published_relays) ? payload.published_relays : [];
    const recommended = Array.isArray(payload.recommended_relays) ? payload.recommended_relays : [];
    const effectiveDefaults = Array.isArray(payload.effective_default_relays) ? payload.effective_default_relays : [];
    state.latestRecommended = recommended;
    state.defaultRelays = effectiveDefaults.length > 0 ? effectiveDefaults : [...DEFAULT_RELAYS];
    state.config = loadRelayConfig();
    state.preferences = relayPreferencesFromEntries(state.config.userRelayMetadata.relays);
    if (state.preferences.length === 0) {
      state.preferences = seedRelayPreferenceDraft({
        publishedRelays: published,
        userRelays: state.config.userRelayMetadata.relays,
        effectiveDefaultRelays: effectiveDefaults,
      });
    }
    renderRelayPreferenceDraft(state);
    renderSimpleRelayList(state.effectiveList, effectiveReadRelays(state.config), "No effective read relays.");
    if (state.statusNode) {
      state.statusNode.textContent = normalizedPubkey()
        ? "Relay insight loaded."
        : "Showing relay insight for the fixed Gigi guest feed.";
    }
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
    persistRelayPreferences(state, state.preferences, { enableUserRelays: true });
    renderSimpleRelayList(state.effectiveList, effectiveReadRelays(state.config), "No effective read relays.");
    if (state.statusNode) state.statusNode.textContent = "Relay preference added.";
  });

  state.fetchButton?.addEventListener("click", async () => {
    const viewer = normalizedPubkey();
    if (!viewer) {
      if (state.statusNode) state.statusNode.textContent = "Log in to fetch your relay list.";
      return;
    }
    if (state.fetchButton instanceof HTMLButtonElement) state.fetchButton.disabled = true;
    if (state.statusNode) state.statusNode.textContent = "Fetching relay list...";
    try {
      if (getSession().method === "nip07") {
        state.config = await syncNIP07RelayConfigFromExtension({ pubkey: viewer, force: true });
      } else {
        state.config = loadRelayConfig();
      }
      await hydrateDirectRelayPreferences(state, { render: true });
      const payload = await loadRelayInsight(state);
      state.config = loadRelayConfig();
      state.preferences = relayPreferencesFromEntries(state.config.userRelayMetadata.relays);
      if (state.preferences.length === 0 && payload) {
        state.preferences = seedRelayPreferenceDraft({
          publishedRelays: payload.published_relays,
          userRelays: state.config.userRelayMetadata.relays,
          effectiveDefaultRelays: payload.effective_default_relays,
        });
      }
      renderRelayPreferenceDraft(state);
      renderSimpleRelayList(state.effectiveList, effectiveReadRelays(state.config), "No effective read relays.");
      if (state.statusNode) state.statusNode.textContent = "Relay list fetched.";
    } catch (error) {
      if (state.statusNode) state.statusNode.textContent = error instanceof Error ? error.message : "Relay list fetch failed.";
    } finally {
      if (state.fetchButton instanceof HTMLButtonElement) state.fetchButton.disabled = false;
    }
  });

  state.useDefaults?.addEventListener("click", () => {
    const next = state.defaultRelays
      .map((relay) => normalizeRelayURL(relay))
      .filter(Boolean)
      .map((relay) => ({ url: relay, usage: "write" }));
    persistRelayPreferences(state, next, { enableUserRelays: true });
    renderSimpleRelayList(state.effectiveList, effectiveReadRelays(state.config), "No effective read relays.");
    if (state.statusNode) state.statusNode.textContent = "Your relay list replaced with effective defaults.";
  });

  state.useRecommended?.addEventListener("click", () => {
    const next = [];
    state.latestRecommended.forEach((entry) => upsertRelayPreference({ preferences: next }, entry.url, entry.usage || "write"));
    persistRelayPreferences(state, next, { enableUserRelays: true });
    renderSimpleRelayList(state.effectiveList, effectiveReadRelays(state.config), "No effective read relays.");
    if (state.statusNode) state.statusNode.textContent = "Your relay list replaced with backend recommendations.";
  });

  form.addEventListener("click", (event) => {
    const remove = event.target.closest?.("[data-relay-preference-remove]");
    if (remove) {
      const relay = normalizeRelayURL(remove.getAttribute("data-relay-preference-remove") || "");
      state.preferences = state.preferences.filter((item) => item.url !== relay);
      persistRelayPreferences(state, state.preferences);
      renderSimpleRelayList(state.effectiveList, effectiveReadRelays(state.config), "No effective read relays.");
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
      persistRelayPreferences(state, state.preferences, { enableUserRelays: true });
      renderSimpleRelayList(state.effectiveList, effectiveReadRelays(state.config), "No effective read relays.");
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
      persistRelayPreferences(state, state.preferences, {
        enableUserRelays: true,
        updatedAt: Number(signed.created_at) || Math.floor(Date.now() / 1000),
      });
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
      try {
        const meta = JSON.parse(signed.content || "{}");
        const pk = normalizedPubkey();
        const display = String(meta.display_name ?? "").trim();
        const name = String(meta.name ?? "").trim();
        if (pk && (display || name)) {
          setSession({ ...getSession(), profileLabel: display || name });
        }
      } catch {
        /* ignore */
      }
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
  bindMuteActions(root);
  initBookmarks(root);
}
