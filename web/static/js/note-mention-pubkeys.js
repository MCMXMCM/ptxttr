import { extractAllNIP27References } from "./nip27.js";
import { noteMainBodySourceText } from "./note-references.js";
import { normalizePubkey } from "./relay-utils.js";

export function mentionPubkeysForEvent(event) {
  const seen = new Set();
  const pubkeys = [];
  const content = noteMainBodySourceText(event);
  extractAllNIP27References(content).forEach((ref) => {
    const pubkey = normalizePubkey(ref?.pubkey);
    if (!pubkey || seen.has(pubkey)) return;
    seen.add(pubkey);
    pubkeys.push(pubkey);
  });
  return pubkeys;
}
