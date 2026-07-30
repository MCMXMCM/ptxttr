/** Relay hints from a note/card DOM node (data-ascii-relay + ancestor profile relays). */
export function relayHintsFromNoteElement(note) {
  if (!note || typeof note !== "object") return [];
  const hasElementLikeAPI =
    typeof note.getAttribute === "function" ||
    typeof note.closest === "function" ||
    note.dataset;
  if (!hasElementLikeAPI) return [];
  const hints = [];
  const seen = new Set();
  const add = (value) => {
    const relay = String(value || "").trim();
    if (!relay || seen.has(relay)) return;
    seen.add(relay);
    hints.push(relay);
  };
  add(note.getAttribute?.("data-ascii-relay") || note.dataset?.asciiRelay || "");
  add(note.getAttribute?.("data-ascii-ref-relay") || note.dataset?.asciiRefRelay || "");
  note
    .closest?.("[data-profile-relays]")
    ?.getAttribute?.("data-profile-relays")
    ?.split?.(",")
    ?.forEach?.((relay) => add(relay));
  return hints;
}
