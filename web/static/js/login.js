import { getSession, loginCapabilities, loginMethodLabel, sessionFeedURL, setSession } from "./session.js";
import { generateSecretKey, getPublicKey, nip19 } from "../lib/nostr-tools.js";
import { pubkeyFromInput, secretFromInput } from "./key-input.js";

const state = document.querySelector("[data-session-state]");
const actions = document.querySelector("[data-session-actions]");

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
}

function completeLogin(session, redirect = true) {
  setSession(session);
  renderSession();
  if (redirect && session.pubkey) {
    window.location.href = sessionFeedURL();
  }
}

document.querySelector("[data-login-readonly]")?.addEventListener("submit", (event) => {
  event.preventDefault();
  try {
    const pubkey = pubkeyFromInput(new FormData(event.currentTarget).get("pubkey"));
    completeLogin({ method: "readonly", pubkey, npub: nip19.npubEncode(pubkey) });
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
    const pubkey = getPublicKey(secret);
    sessionStorage.setItem("ptxt_nsec", nip19.nsecEncode(secret));
    completeLogin({ method: "yolo", pubkey, npub: nip19.npubEncode(pubkey) });
  } catch (error) {
    alert(error.message);
  }
});

document.querySelector("[data-login-ephemeral]")?.addEventListener("click", () => {
  const secret = generateSecretKey();
  const pubkey = getPublicKey(secret);
  sessionStorage.setItem("ptxt_nsec", nip19.nsecEncode(secret));
  completeLogin({ method: "ephemeral", pubkey, npub: nip19.npubEncode(pubkey) });
});

window.addEventListener("ptxt:session", renderSession);

renderSession();
