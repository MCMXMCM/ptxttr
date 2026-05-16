import { getSession, loginCapabilities, loginMethodLabel, sessionFeedURL, setSession } from "./session.js";
import { generateSecretKey, getPublicKey, nip19 } from "../lib/nostr-tools.js";
import { pubkeyFromInput, secretFromInput } from "./key-input.js";

const state = document.querySelector("[data-session-state]");
const actions = document.querySelector("[data-session-actions]");
const signupIntroActions = document.querySelector("[data-signup-intro-actions]");
const signupCredentials = document.querySelector("[data-signup-credentials]");
const signupNsecInput = document.querySelector("[data-signup-nsec-input]");
const signupNpubInput = document.querySelector("[data-signup-npub-input]");

const copyRestoreTimers = new WeakMap();

function resetSignupUI() {
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

function maybeResetSignupForSession(session) {
  if (signupCredentialsWanted(session)) return;
  resetSignupUI();
}

function syncSignupCredentialsFromSession(session) {
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

function renderSession() {
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
  maybeResetSignupForSession(session);
  syncSignupCredentialsFromSession(session);
}

function completeLogin(session, redirect = true) {
  setSession(session);
  if (redirect && session.pubkey) {
    window.location.href = sessionFeedURL();
  }
}

function completeLoginWithStoredNsec(secret, method, redirect = true) {
  const pubkey = getPublicKey(secret);
  const npub = nip19.npubEncode(pubkey);
  sessionStorage.setItem("ptxt_nsec", nip19.nsecEncode(secret));
  completeLogin({ method, pubkey, npub }, redirect);
}

document.querySelector("[data-login-readonly]")?.addEventListener("submit", (event) => {
  event.preventDefault();
  try {
    const pubkey = pubkeyFromInput(new FormData(event.currentTarget).get("pubkey"));
    completeLogin({ method: "readonly", pubkey, npub: nip19.npubEncode(pubkey) }, true);
  } catch (error) {
    alert(error.message);
  }
});

document.querySelector("[data-login-nip07]")?.addEventListener("click", async () => {
  if (!window.nostr?.getPublicKey) {
    alert("No NIP-07 extension was found.");
    return;
  }
  const pubkey = await window.nostr.getPublicKey();
  completeLogin({ method: "nip07", pubkey, npub: nip19.npubEncode(pubkey) });
});

document.querySelector("[data-login-yolo]")?.addEventListener("submit", (event) => {
  event.preventDefault();
  try {
    const secret = secretFromInput(new FormData(event.currentTarget).get("secret"));
    completeLoginWithStoredNsec(secret, "yolo");
  } catch (error) {
    alert(error.message);
  }
});

document.querySelector("[data-signup-generate]")?.addEventListener("click", () => {
  completeLoginWithStoredNsec(generateSecretKey(), "ephemeral", false);
});

document.querySelector("[data-signup-copy-nsec]")?.addEventListener("click", (event) => {
  copyLoginValue(signupNsecInput?.value ?? "", event.currentTarget);
});

document.querySelector("[data-signup-copy-npub]")?.addEventListener("click", (event) => {
  copyLoginValue(signupNpubInput?.value ?? "", event.currentTarget);
});

document.querySelector("[data-signup-continue]")?.addEventListener("click", () => {
  const session = getSession();
  if (!session.pubkey) return;
  window.location.href = sessionFeedURL();
});

window.addEventListener("ptxt:session", renderSession);

renderSession();
