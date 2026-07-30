import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  bookmarkEntries,
  authorReadRelaysFromKind10002,
  authorWriteRelaysFromKind10002,
  canonicalHex64,
  dedupeEventsByID,
  followPubkeys,
  mutePubkeys,
  normalizePubkey,
  relayHintsFromKind10002,
  resolveEventID,
  isCanonicalEventID,
} from "./relay-utils.js";

describe("relay-utils", () => {
  it("dedupes events by id keeping newest created_at", () => {
    const out = dedupeEventsByID([
      { id: "aa".repeat(32), created_at: 1, kind: 1 },
      { id: "AA".repeat(32), created_at: 2, kind: 1 },
    ]);
    assert.equal(out.length, 1);
    assert.equal(out[0].created_at, 2);
  });

  it("parses bookmark entries from kind 10003", () => {
    const id = "bb".repeat(32);
    const entries = bookmarkEntries({
      kind: 10003,
      tags: [
        ["e", id, "wss://relay.example"],
        ["e", id],
        ["p", "cc".repeat(32)],
      ],
    });
    assert.equal(entries.length, 1);
    assert.equal(entries[0].id, id);
    assert.equal(entries[0].relay, "wss://relay.example");
  });

  it("extracts mute and follow pubkeys", () => {
    const pk = "dd".repeat(32);
    assert.deepEqual(mutePubkeys({ kind: 10000, tags: [["p", pk]] }), [pk]);
    assert.deepEqual(followPubkeys({ kind: 3, tags: [["p", pk]] }), [pk]);
  });

  it("parses kind-10002 relay hints", () => {
    const event = {
      kind: 10002,
      tags: [
        ["r", "wss://write.example", "write"],
        ["r", "wss://read.example", "read"],
        ["r", "wss://any.example"],
      ],
    };
    const hints = relayHintsFromKind10002(event);
    assert.deepEqual(hints.write, ["wss://write.example"]);
    assert.deepEqual(hints.read, ["wss://read.example"]);
    assert.deepEqual(hints.any, ["wss://any.example"]);
    assert.deepEqual(authorWriteRelaysFromKind10002(event), ["wss://write.example", "wss://any.example"]);
    assert.deepEqual(authorReadRelaysFromKind10002(event), ["wss://read.example", "wss://any.example"]);
  });

  it("canonicalizes 64-char hex", () => {
    const id = "EE".repeat(32);
    assert.equal(canonicalHex64(id), id.toLowerCase());
  });

  it("decodes npub identifiers to hex pubkeys", () => {
    const hex = normalizePubkey("npub1sg6plzptd64u62a878hep2kev88swjh3tw00gjsfl8f237lmu63q0uf63m");
    assert.match(hex, /^[0-9a-f]{64}$/);
    assert.equal(hex, normalizePubkey(hex));
  });

  it("resolveEventID decodes nevent and hex ids", () => {
    const hex = "aa".repeat(32);
    assert.deepEqual(resolveEventID(hex), { eventID: hex, relays: [] });
    const nevent =
      "nevent1qgsp4lsvwn3aw7zwh2f6tcl6249xa6cpj2x3yuu6azaysvncdqywxmgqyz4xtntx4fe9sn9v2406e9a9g5gga0kucusc3f4hfl466rqkr7sh63tqv8h";
    const resolved = resolveEventID(nevent);
    assert.equal(resolved?.eventID, "aa65cd66aa72584cac555fac97a545108ebedcc72188a6b74febad0c161fa17d");
    assert.ok(isCanonicalEventID(resolved?.eventID));
  });
});
