import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  collectReferencedEventIDs,
  imetaMediaItemsJSON,
  isQuotePost,
  isSimpleRepost,
  noteMainBodySourceText,
  parseEmbeddedRepostEvent,
  referencedEventID,
  referencedEventRef,
  relayHintsByReferencedID,
  resolveReferencedEvent,
} from "./note-references.js";

describe("note-references", () => {
  it("detects quote and repost modes", () => {
    const quoteID = "aa".repeat(32);
    const quote = {
      kind: 1,
      tags: [["q", quoteID, "wss://filter.nostr.wine"]],
      content: "quoted text",
    };
    assert.equal(isQuotePost(quote), true);
    assert.equal(isSimpleRepost({ kind: 6, tags: [["e", quoteID]] }), true);
    assert.equal(referencedEventID(quote), quoteID);
    assert.deepEqual(referencedEventRef(quote), { id: quoteID, relay: "wss://filter.nostr.wine" });
  });

  it("strips quoted event refs from main body text", () => {
    const quoteID = "aa65cd66aa72584cac555fac97a545108ebedcc72188a6b74febad0c161fa17d";
    const nevent =
      "nevent1qgsp4lsvwn3aw7zwh2f6tcl6249xa6cpj2x3yuu6azaysvncdqywxmgqyz4xtntx4fe9sn9v2406e9a9g5gga0kucusc3f4hfl466rqkr7sh63tqv8h";
    const event = {
      kind: 1,
      tags: [["q", quoteID]],
      content: `see this nostr:${nevent} please`,
    };
    const body = noteMainBodySourceText(event);
    assert.equal(body.includes("nevent1"), false);
    assert.equal(body, "see this please");
  });

  it("collects referenced ids and relay hints", () => {
    const quoteID = "cc".repeat(32);
    const event = {
      kind: 1,
      tags: [["q", quoteID, "wss://relay.example"]],
      content: "hello",
    };
    assert.deepEqual(collectReferencedEventIDs([event]), [quoteID]);
    assert.deepEqual(relayHintsByReferencedID([event]), {
      [quoteID]: ["wss://relay.example"],
    });
  });

  it("clears repost main body text and parses embedded NIP-18 JSON", () => {
    const noteID = "fdffc1e0f60c1cfd45356bc5a95f5308184430a5b76a2f71f2e30978250a4260";
    const embedded = {
      kind: 1,
      id: noteID,
      pubkey: "14b55cd017eb033127ab4d0c8a50cd3d80dbaf4085e2ef3f13da9b1bf44831e6",
      content: "Approaching 250 years of freedom",
      created_at: 1781459759,
      tags: [],
    };
    const repost = {
      kind: 6,
      tags: [["e", noteID]],
      content: JSON.stringify(embedded),
    };
    assert.equal(noteMainBodySourceText(repost), "");
    const parsed = parseEmbeddedRepostEvent(repost.content, noteID);
    assert.equal(parsed.content, "Approaching 250 years of freedom");
    const resolved = resolveReferencedEvent(repost, new Map());
    assert.equal(resolved.reference.content, "Approaching 250 years of freedom");
  });

  it("serializes imeta media items for referenced notes", () => {
    const json = imetaMediaItemsJSON([
      ["imeta", "url https://cdn.example.com/burger.jpg", "m image/jpeg"],
      ["imeta", "url https://cdn.example.com/demo.mp4", "m video/mp4"],
      ["imeta", "url https://cdn.example.com/burger.jpg", "m image/jpeg"],
      ["imeta", "url javascript:alert(1)", "m image/png"],
    ]);
    assert.deepEqual(JSON.parse(json), [
      { url: "https://cdn.example.com/burger.jpg", type: "image" },
      { url: "https://cdn.example.com/demo.mp4", type: "video" },
    ]);
  });

  it("preserves valid imeta dimensions and omits invalid dimensions", () => {
    const json = imetaMediaItemsJSON([
      ["imeta", "url https://cdn.example.com/a.jpg", "m image/jpeg", "dim 1200x800"],
      ["imeta", "url https://cdn.example.com/b.jpg", "m image/jpeg", "dim 0x800"],
    ]);
    assert.deepEqual(JSON.parse(json), [
      { url: "https://cdn.example.com/a.jpg", type: "image", width: 1200, height: 800 },
      { url: "https://cdn.example.com/b.jpg", type: "image" },
    ]);
  });

  it("accepts shorthand imeta media types", () => {
    const json = imetaMediaItemsJSON([
      ["imeta", "url https://cdn.example.com/burger.jpg", "m jpeg"],
      ["imeta", "url https://cdn.example.com/demo.mp4", "m mp4"],
    ]);
    assert.deepEqual(JSON.parse(json), [
      { url: "https://cdn.example.com/burger.jpg", type: "image" },
      { url: "https://cdn.example.com/demo.mp4", type: "video" },
    ]);
  });
});
