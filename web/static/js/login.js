import {
  getSession,
  loginCapabilities,
  loginMethodLabel,
  clearSessionScopedCaches,
  persistSigningAccount,
  recentSigningAccounts,
  removeStoredSigningAccount,
  sessionFeedURL,
  setSession,
  shortPubkey,
  syncNIP07RelayConfigFromExtension,
  switchToStoredSigningAccount,
} from "./session.js";
import { hasCompletedBootstrap, markBootstrapPending } from "./first-login-bootstrap.js";
import { generateSecretKey, getPublicKey, nip19 } from "../lib/nostr-tools.js";
import { pubkeyFromInput, secretFromInput } from "./key-input.js";

const copyRestoreTimers = new WeakMap();
let loginGlobalsBound = false;

function loginPageRoot(root = document) {
  if (root instanceof Document) {
    return root.querySelector("[data-shell-main]") || root.documentElement || null;
  }
  return root?.querySelector?.("[data-shell-main]") || root || null;
}

function loginDom(root = document) {
  const scope = loginPageRoot(root);
  const query = (selector) => scope?.querySelector?.(selector) || null;
  return {
    scope,
    state: query("[data-session-state]"),
    actions: query("[data-session-actions]"),
    signupIntroActions: query("[data-signup-intro-actions]"),
    signupCredentials: query("[data-signup-credentials]"),
    signupNsecInput: query("[data-signup-nsec-input]"),
    signupNpubInput: query("[data-signup-npub-input]"),
    recentAccountsSection: query("[data-recent-accounts-section]"),
    recentAccountsList: query("[data-recent-accounts-list]"),
  };
}

function resetSignupUI(root = document) {
  const { signupIntroActions, signupCredentials, signupNsecInput, signupNpubInput } = loginDom(root);
  if (signupIntroActions) signupIntroActions.hidden = false;
  if (signupCredentials) signupCredentials.hidden = true;
  if (signupNsecInput) signupNsecInput.value = "";
  if (signupNpubInput) signupNpubInput.value = "";
}

/** Ephemeral signup flow: show saved keys only when session and sessionStorage agree. */
function signupCredentialsWanted(session) {
  if (session.method !== "ephemeral" || !session.pubkey) return false;
  return Boolean(sessionStorage.getItem("ptxt_nsec"));
}

function maybeResetSignupForSession(session, root = document) {
  if (signupCredentialsWanted(session)) return;
  resetSignupUI(root);
}

function syncSignupCredentialsFromSession(session, root = document) {
  const { signupCredentials, signupIntroActions, signupNsecInput, signupNpubInput } = loginDom(root);
  if (!signupCredentials || !signupIntroActions) return;
  if (!signupCredentialsWanted(session)) return;
  const nsec = sessionStorage.getItem("ptxt_nsec") || "";
  if (signupNpubInput && session.npub) signupNpubInput.value = session.npub;
  if (signupNsecInput) signupNsecInput.value = nsec;
  signupIntroActions.hidden = true;
  signupCredentials.hidden = false;
}

async function copyLoginValue(text, button) {
  if (!text || !button) return;
  try {
    await navigator.clipboard.writeText(text);
    const previous = button.textContent;
    const prior = copyRestoreTimers.get(button);
    if (prior) clearTimeout(prior);
    button.textContent = "Copied";
    copyRestoreTimers.set(
      button,
      setTimeout(() => {
        button.textContent = previous;
        copyRestoreTimers.delete(button);
      }, 1500),
    );
  } catch {
    window.prompt("Copy this value", text);
  }
}

function renderSession(root = document) {
  const { state, actions } = loginDom(root);
  if (!state) return;
  const session = getSession();
  const capabilities = loginCapabilities(session);
  if (session.pubkey) {
    const summary = [
      `Logged in via ${loginMethodLabel(session)}.`,
      `Signer available: ${capabilities.canSign ? "yes" : "no"}.`,
      `Pubkey: ${session.pubkey}`,
    ];
    if (session.npub) summary.push(`Npub: ${session.npub}`);
    state.textContent = summary.join("\n");
  } else {
    state.textContent = "Not logged in.";
  }
  if (actions) actions.hidden = !session.pubkey;
  maybeResetSignupForSession(session, root);
  syncSignupCredentialsFromSession(session, root);
  renderRecentAccounts(root);
}

function renderRecentAccounts(root = document) {
  const { recentAccountsSection, recentAccountsList } = loginDom(root);
  if (!recentAccountsSection || !recentAccountsList) return;
  const accounts = recentSigningAccounts();
  recentAccountsSection.hidden = accounts.length === 0;
  recentAccountsList.textContent = "";
  const activeSession = getSession();
  const activePubkey = activeSession.pubkey;
  const activeCanResumeSigning = loginCapabilities(activeSession).hasSessionSecret;
  for (const account of accounts) {
    const isActive = account.pubkey === activePubkey
      && (account.method === activeSession.method || activeCanResumeSigning);
    const item = document.createElement("li");
    item.className = "login-recent-account";
    item.dataset.pubkey = account.pubkey;

    const meta = document.createElement("div");
    meta.className = "login-recent-account-meta";

    const avatar = document.createElement("span");
    avatar.className = "login-recent-account-avatar";
    if (account.picture) {
      const img = document.createElement("img");
      img.alt = "";
      img.loading = "lazy";
      img.decoding = "async";
      img.src = account.picture;
      avatar.append(img);
    } else {
      avatar.textContent = "@";
    }

    const title = document.createElement("strong");
    title.textContent = account.profileLabel || shortPubkey(account.pubkey);

    const detail = document.createElement("small");
    detail.className = "muted";
    detail.textContent = account.npub || shortPubkey(account.pubkey);

    meta.append(avatar, title, detail);

    const actionsWrap = document.createElement("div");
    actionsWrap.className = "login-recent-account-actions";

    const switchButton = document.createElement("button");
    switchButton.type = "button";
    switchButton.dataset.switchAccount = account.pubkey;
    switchButton.textContent = isActive ? "Active" : "Switch";
    switchButton.disabled = isActive;

    const removeButton = document.createElement("button");
    removeButton.type = "button";
    removeButton.dataset.removeAccount = account.pubkey;
    removeButton.className = "button-danger";
    removeButton.textContent = "Remove";

    actionsWrap.append(switchButton, removeButton);
    item.append(meta, actionsWrap);
    recentAccountsList.append(item);
  }
}

function completeLogin(session, redirect = true) {
  setSession(session);
  if (session.pubkey && !hasCompletedBootstrap(session.pubkey)) {
    markBootstrapPending(session.pubkey);
  }
  if (redirect && session.pubkey) {
    window.location.href = sessionFeedURL();
  }
}

function completeLoginWithStoredNsec(secret, method, redirect = true) {
  const pubkey = getPublicKey(secret);
  const npub = nip19.npubEncode(pubkey);
  const nsec = nip19.nsecEncode(secret);
  const session = { method, pubkey, npub };
  persistSigningAccount(session, nsec);
  completeLogin(session, redirect);
}

function bindLoginGlobals() {
  if (loginGlobalsBound) return;
  loginGlobalsBound = true;

  document.addEventListener("click", async (event) => {
    const switchButton = event.target.closest?.("[data-switch-account]");
    if (switchButton) {
      try {
        switchToStoredSigningAccount(switchButton.getAttribute("data-switch-account"));
        await clearSessionScopedCaches();
        window.location.href = sessionFeedURL();
      } catch (error) {
        alert(error.message);
      }
      return;
    }
    const removeButton = event.target.closest?.("[data-remove-account]");
    if (!removeButton) return;
    event.preventDefault();
    removeStoredSigningAccount(removeButton.getAttribute("data-remove-account"));
    renderRecentAccounts();
  });

  window.addEventListener("ptxt:session", () => renderSession());
  window.addEventListener("ptxt:signing-accounts", () => renderRecentAccounts());
}

export function initLoginPage(root = document) {
  bindLoginGlobals();
  const { scope, signupNsecInput, signupNpubInput } = loginDom(root);
  if (!scope || scope.dataset.loginPageBound === "1") {
    renderSession(root);
    return;
  }
  scope.dataset.loginPageBound = "1";

  scope.querySelector("[data-login-readonly]")?.addEventListener("submit", (event) => {
    event.preventDefault();
    try {
      const pubkey = pubkeyFromInput(new FormData(event.currentTarget).get("pubkey"));
      completeLogin({ method: "readonly", pubkey, npub: nip19.npubEncode(pubkey) }, true);
    } catch (error) {
      alert(error.message);
    }
  });

  scope.querySelector("[data-login-nip07]")?.addEventListener("click", async () => {
    if (!window.nostr?.getPublicKey) {
      alert("No NIP-07 extension was found.");
      return;
    }
    const pubkey = await window.nostr.getPublicKey();
    await syncNIP07RelayConfigFromExtension({ pubkey, force: true });
    completeLogin({ method: "nip07", pubkey, npub: nip19.npubEncode(pubkey) });
  });

  scope.querySelector("[data-login-yolo]")?.addEventListener("submit", (event) => {
    event.preventDefault();
    try {
      const secret = secretFromInput(new FormData(event.currentTarget).get("secret"));
      completeLoginWithStoredNsec(secret, "yolo");
    } catch (error) {
      alert(error.message);
    }
  });

  scope.querySelector("[data-signup-generate]")?.addEventListener("click", () => {
    completeLoginWithStoredNsec(generateSecretKey(), "ephemeral", false);
  });

  scope.querySelector("[data-signup-copy-nsec]")?.addEventListener("click", (event) => {
    copyLoginValue(signupNsecInput?.value ?? "", event.currentTarget);
  });

  scope.querySelector("[data-signup-copy-npub]")?.addEventListener("click", (event) => {
    copyLoginValue(signupNpubInput?.value ?? "", event.currentTarget);
  });

  scope.querySelector("[data-signup-continue]")?.addEventListener("click", () => {
    const session = getSession();
    if (!session.pubkey) return;
    window.location.href = sessionFeedURL();
  });

  renderSession(root);
}

initLoginPage(document);
