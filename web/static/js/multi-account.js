import {
  clearSessionScopedCaches,
  getSession,
  recentSigningAccounts,
  removeStoredSigningAccount,
  sessionFeedURL,
  shortPubkey,
  switchToStoredSigningAccount,
  updateStoredSigningAccountProfile,
} from "./session.js";
import { fetchProfiles } from "./relay-reads.js";

function renderAccountList(root = document) {
  const list = root.querySelector("[data-account-switcher-list]");
  if (!(list instanceof HTMLElement)) return;
  const active = getSession();
  const accounts = recentSigningAccounts();
  list.textContent = "";
  if (!accounts.length) {
    const empty = document.createElement("li");
    empty.className = "muted";
    empty.textContent = "No recent signing accounts yet.";
    list.append(empty);
    return;
  }
  accounts.forEach((account) => {
    const item = document.createElement("li");
    item.className = "settings-account-item";
    item.dataset.pubkey = account.pubkey;

    const avatar = document.createElement("span");
    avatar.className = "settings-account-avatar";
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

    const meta = document.createElement("div");
    meta.className = "settings-account-meta";
    const title = document.createElement("strong");
    title.textContent = account.profileLabel || shortPubkey(account.pubkey);
    const detail = document.createElement("small");
    detail.className = "muted";
    detail.textContent = `${account.npub || shortPubkey(account.pubkey)} • ${account.method}`;
    meta.append(title, detail);

    const actions = document.createElement("div");
    actions.className = "settings-account-actions";
    const switchBtn = document.createElement("button");
    switchBtn.type = "button";
    switchBtn.dataset.switchAccount = account.pubkey;
    switchBtn.textContent = account.pubkey === active.pubkey ? "Active" : "Switch";
    switchBtn.disabled = account.pubkey === active.pubkey;
    const removeBtn = document.createElement("button");
    removeBtn.type = "button";
    removeBtn.dataset.removeAccount = account.pubkey;
    removeBtn.className = "button-danger";
    removeBtn.textContent = "Remove";
    actions.append(switchBtn, removeBtn);

    item.append(avatar, meta, actions);
    list.append(item);
  });
}

async function hydrateStoredAccountProfiles() {
  const accounts = recentSigningAccounts();
  const missing = accounts.filter((account) => !account.profileLabel || !account.picture).map((account) => account.pubkey);
  if (!missing.length) return;
  const profiles = await fetchProfiles(missing).catch(() => ({}));
  Object.entries(profiles).forEach(([pubkey, profile]) => {
    updateStoredSigningAccountProfile(pubkey, profile);
  });
}

document.addEventListener("click", async (event) => {
  const switchButton = event.target.closest("[data-account-switcher-root] [data-switch-account]");
  if (switchButton) {
    switchToStoredSigningAccount(switchButton.getAttribute("data-switch-account"));
    await clearSessionScopedCaches();
    window.location.href = sessionFeedURL();
    return;
  }
  const removeButton = event.target.closest("[data-account-switcher-root] [data-remove-account]");
  if (!removeButton) return;
  removeStoredSigningAccount(removeButton.getAttribute("data-remove-account"));
  renderAccountList(document);
});

window.addEventListener("ptxt:session", () => renderAccountList(document));
window.addEventListener("ptxt:signing-accounts", () => renderAccountList(document));

void hydrateStoredAccountProfiles().finally(() => {
  renderAccountList(document);
});
