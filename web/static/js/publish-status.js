import { initRetroLoaders, markRetroLoaderComplete } from "./retro-loader.js";
import { retroLoaderMarkup } from "./shell.js";

let dialog = null;

const DEFAULT_PENDING_STATUSES = [
  "signing event...",
  "preparing relay broadcast...",
  "broadcasting to relays...",
];

export function pendingPublishStatus({
  phaseTitle = "Broadcasting to relays",
  statusMessage = "Preparing relay broadcast...",
  plannedRelays = [],
  statusMessages = DEFAULT_PENDING_STATUSES,
  completionMessage = "publish complete.",
} = {}) {
  return {
    phaseTitle,
    statusMessage,
    plannedRelays,
    statusMessages,
    completionMessage,
  };
}

function ensurePublishStatusDialog() {
  if (dialog) return dialog;
  dialog = document.createElement("dialog");
  dialog.className = "publish-status-dialog";
  dialog.dataset.publishStatusDialog = "";
  dialog.innerHTML = `
    <form method="dialog" class="publish-status-close-row">
      <button type="submit" class="link-button">Close</button>
    </form>
    <h2 class="publish-status-heading">Publish status</h2>
    <div class="publish-status-shell" data-publish-status-shell></div>
    <ul class="publish-status-list" data-publish-status-list></ul>
  `;
  dialog.addEventListener("click", (event) => {
    if (event.target === dialog) dialog.close();
  });
  document.body.append(dialog);
  return dialog;
}

function relayStatusLabel(result) {
  if (result?.accepted) return "[ OK ]";
  const reason = String(result?.error || result?.message || "").trim();
  return reason ? "[ X ]" : "[ X ]";
}

function relayDetailLabel(result) {
  if (result?.accepted) return String(result?.message || "accepted").trim() || "accepted";
  const reason = String(result?.error || result?.message || "").trim();
  return reason || "failed";
}

function summarizePublishPayload(payload) {
  const stats = Array.isArray(payload?.relay_stats) ? payload.relay_stats : [];
  const accepted = stats.filter((row) => row?.accepted).length;
  const total = stats.length;
  if (total === 0) {
    if (payload?.accepted > 0) return `${payload.accepted} relay(s) accepted this event.`;
    return "No relay results returned.";
  }
  return `${accepted} of ${total} relay(s) accepted this event.`;
}

export function publishStatusRows(payload, planned = []) {
  const stats = Array.isArray(payload?.relay_stats) ? payload.relay_stats : [];
  if (stats.length) {
    return stats.map((row) => ({
      relay: String(row?.relay_url || row?.RelayURL || "").trim() || "relay",
      badge: relayStatusLabel(row),
      detail: relayDetailLabel(row),
      state: row?.accepted ? "success" : "failed",
    }));
  }
  return planned.map((relayURL) => ({
    relay: relayURL,
    badge: "[...]",
    detail: "pending",
    state: "pending",
  }));
}

function renderRelayRows(list, rows = []) {
  list.replaceChildren();
  rows.forEach((row) => {
    const li = document.createElement("li");
    li.className = `publish-status-row publish-status-row--${row.state || "pending"}`;

    const badge = document.createElement("span");
    badge.className = "publish-status-row-badge";
    badge.textContent = row.badge || "[...]";

    const body = document.createElement("span");
    body.className = "publish-status-row-body";

    const relay = document.createElement("strong");
    relay.className = "publish-status-row-relay";
    relay.textContent = row.relay || "relay";

    const detail = document.createElement("span");
    detail.className = "publish-status-row-detail";
    detail.textContent = row.detail || "";

    body.append(relay, detail);
    li.append(badge, body);
    list.append(li);
  });
}

function ensurePublishShellMarkup(shell, {
  title = "Publishing",
  summary = "",
  statusMessages = DEFAULT_PENDING_STATUSES,
  completionMessage = "publish complete.",
} = {}) {
  shell.innerHTML = retroLoaderMarkup({
    loaderType: "publish-status",
    title,
    summary,
    statusMessages,
    completionMessage,
    showCards: false,
    extraClass: " retro-loader--compact publish-status-loader",
  }).trim();
  initRetroLoaders(shell);
  return shell.querySelector("[data-retro-loader]");
}

export function showPublishStatusSheet(payload, {
  title = "Publish status",
  initialState = null,
} = {}) {
  const dlg = ensurePublishStatusDialog();
  const heading = dlg.querySelector(".publish-status-heading");
  const shell = dlg.querySelector("[data-publish-status-shell]");
  const list = dlg.querySelector("[data-publish-status-list]");
  if (heading) heading.textContent = title;

  const phaseTitle = initialState?.phaseTitle || title;
  const summary = initialState?.statusMessage || summarizePublishPayload(payload);
  const loader = ensurePublishShellMarkup(shell, {
    title: phaseTitle,
    summary,
    statusMessages: initialState?.statusMessages || DEFAULT_PENDING_STATUSES,
    completionMessage: initialState?.completionMessage || "publish complete.",
  });

  const plannedRelays = Array.isArray(initialState?.plannedRelays)
    ? initialState.plannedRelays
    : Array.isArray(payload?.planned_relays)
      ? payload.planned_relays
      : [];

  if (payload) {
    markRetroLoaderComplete(loader, {
      summary: summarizePublishPayload(payload),
      completionMessage: initialState?.completionMessage || "publish complete.",
    });
  }

  renderRelayRows(list, publishStatusRows(payload, plannedRelays));
  if (dlg.open) return dlg;
  if (typeof dlg.showModal === "function") {
    dlg.showModal();
  } else {
    dlg.setAttribute("open", "");
  }
  return dlg;
}
