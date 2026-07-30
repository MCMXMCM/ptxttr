import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { nip19 } from "../lib/nostr-tools.js";
import { mentionPubkeysForEvent } from "./note-mention-pubkeys.js";

describe("mentionPubkeysForEvent", () => {
  it("collects unique tagged profile pubkeys from note content", () => {
    const alice = "11".repeat(32);
    const bob = "22".repeat(32);
    const note = "33".repeat(32);
    const content = [
      `hi nostr:${nip19.npubEncode(alice)}`,
      `and @${nip19.nprofileEncode({ pubkey: bob, relays: ["wss://relay.example"] })}`,
      `and again nostr:${nip19.npubEncode(alice)}`,
      `plus nostr:${nip19.noteEncode(note)}`,
    ].join(" ");

    const pubkeys = mentionPubkeysForEvent({
      kind: 1,
      content,
      tags: [],
    });

    assert.deepEqual(pubkeys, [alice, bob]);
  });

  it("ignores quoted event references stripped from the main note body", () => {
    const alice = "44".repeat(32);
    const quote = "55".repeat(32);
    const content = `hello nostr:${nip19.npubEncode(alice)} nostr:${nip19.neventEncode({ id: quote })}`;

    const pubkeys = mentionPubkeysForEvent({
      kind: 1,
      content,
      tags: [["q", quote]],
    });

    assert.deepEqual(pubkeys, [alice]);
  });
});
