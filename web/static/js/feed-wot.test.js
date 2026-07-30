import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { resolveFeedWoTFromInputs } from "./feed-wot.js";
import { normalizePubkey } from "./relay-utils.js";

const JACK_NPUB = "npub1sg6plzptd64u62a878hep2kev88swjh3tw00gjsfl8f237lmu63q0uf63m";
const JACK_HEX = normalizePubkey(JACK_NPUB);

describe("resolveFeedWoTFromInputs", () => {
  it("uses the logged-out default seed when WoT is on and seed pref is unset", () => {
    const mode = resolveFeedWoTFromInputs({
      viewerPubkey: "",
      wotEnabled: true,
      seedPref: "",
      loggedOutDefaultSeed: JACK_NPUB,
      depth: 1,
    });
    assert.equal(mode.kind, "wot");
    assert.equal(mode.seed, JACK_HEX);
    assert.equal(mode.depth, 1);
  });

  it("returns firehose mode when WoT is disabled", () => {
    assert.deepEqual(
      resolveFeedWoTFromInputs({ viewerPubkey: "", wotEnabled: false }),
      { kind: "firehose" },
    );
  });

  it("uses the signed-in viewer as seed when no seed pref is set", () => {
    const viewer = "aa".repeat(32);
    const mode = resolveFeedWoTFromInputs({
      viewerPubkey: viewer,
      wotEnabled: true,
      seedPref: "",
    });
    assert.equal(mode.kind, "wot");
    assert.equal(mode.seed, viewer);
  });
});
