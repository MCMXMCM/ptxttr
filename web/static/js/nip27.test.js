import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { nip19 } from "../lib/nostr-tools.js";

import {
  decodeNip19Ref,
  extractAllNIP27References,
  rewriteASCIIMentions,
  asciiMentionsJSONFor,
} from "./nip27.js";
import { profilePath } from "./relay-utils.js";

describe("nip27 mention rewriting", () => {
  it("extracts and rewrites nprofile references to @display names", () => {
    const pubkey = "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798";
    const encoded = nip19.nprofileEncode({ pubkey, relays: ["wss://relay.example"] });
    const content = `is this nostr:${encoded}'s alt?`;
    const refs = extractAllNIP27References(content);
    assert.equal(refs.length, 1);
    assert.equal(refs[0].pubkey, pubkey);
    assert.equal(refs[0].kind, "nprofile");

    const profiles = {
      [pubkey]: { pubkey, display_name: "Paul Keating" },
    };
    const { text, mentions } = rewriteASCIIMentions(content, profiles);
    assert.equal(text, "is this @Paul Keating's alt?");
    assert.equal(mentions.length, 1);
    assert.equal(mentions[0].label, "@Paul Keating");
    assert.equal(mentions[0].href, profilePath(pubkey, ["wss://relay.example"]));
  });

  it("shortens nevent references for column wrapping", () => {
    const eventID = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789";
    const encoded = nip19.neventEncode({ id: eventID });
    const content = `see nostr:${encoded}`;
    const { text, mentions } = rewriteASCIIMentions(content, {});
    assert.match(text, /^see note:/);
    assert.equal(mentions[0].href, `/thread/${eventID}`);
    assert.ok(mentions[0].label.length <= 24);
  });

  it("merges mentions from multiple sources", () => {
    const pk = "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798";
    const npub = nip19.npubEncode(pk);
    const main = `hello nostr:${npub}`;
    const quote = `from nostr:${npub} again`;
    const json = asciiMentionsJSONFor({}, main, quote);
    const parsed = JSON.parse(json);
    assert.equal(parsed.length, 1);
    assert.equal(parsed[0].href, profilePath(pk));
  });

  it("keeps relay hints in mention hrefs", () => {
    const pk = "79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798";
    const ref = decodeNip19Ref(nip19.nprofileEncode({ pubkey: pk, relays: ["wss://relay.example"] }));
    assert.deepEqual(ref?.relays, ["wss://relay.example"]);
  });
});
