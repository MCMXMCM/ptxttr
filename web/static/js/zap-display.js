import { zapTotals } from "./event-store.js";
import { fetchZapReceipts } from "./relay-reads.js";
import { canonicalHex64 } from "./relay-utils.js";
import { refreshAscii } from "./ascii.js";

async function applyZapTotals(root = document, { fetchNetwork = false } = {}) {
  const notes = [...root.querySelectorAll("[data-ascii-kind][id^='note-']")];
  const ids = [...new Set(notes.map((node) => canonicalHex64(node.id.replace(/^note-/, ""))).filter(Boolean))];
  if (!ids.length) return;
  if (fetchNetwork) await fetchZapReceipts(ids).catch(() => {});
  const totals = await zapTotals(ids).catch(() => new Map());
  notes.forEach((node) => {
    const id = canonicalHex64(node.id.replace(/^note-/, ""));
    if (!id) return;
    const next = String(Number.parseInt(`${totals.get(id) ?? 0}`, 10) || 0);
    if (node.dataset.asciiZapTotal === next) return;
    node.dataset.asciiZapTotal = next;
    refreshAscii(node);
  });
}

export async function hydrateVisibleZapTotals(root = document) {
  await applyZapTotals(root, { fetchNetwork: false });
  await applyZapTotals(root, { fetchNetwork: true });
}
