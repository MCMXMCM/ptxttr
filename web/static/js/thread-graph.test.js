import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { relayHintsForThreadReference, resolveBundleSelectedEvent, resolveKnownThreadEndpoints } from "./thread-graph.js";

describe("relayHintsForThreadReference", () => {
  it("prefers relay hints for the direct parent reference and keeps the event relay", () => {
    const parentID = "b".repeat(64);
    const otherID = "c".repeat(64);
    const event = {
      relay_url: "wss://event-relay.example",
      tags: [
        ["e", otherID, "wss://other-relay.example", "root"],
        ["e", parentID, "wss://parent-relay.example", "reply"],
      ],
    };

    assert.deepEqual(relayHintsForThreadReference(event, parentID), [
      "wss://event-relay.example",
      "wss://parent-relay.example",
    ]);
  });

  it("returns all referenced e-tag relays when no target is provided", () => {
    const event = {
      tags: [
        ["e", "b".repeat(64), "wss://one.example", "root"],
        ["e", "c".repeat(64), "wss://two.example", "reply"],
      ],
    };

    assert.deepEqual(relayHintsForThreadReference(event), [
      "wss://one.example",
      "wss://two.example",
    ]);
  });
});

describe("resolveKnownThreadEndpoints", () => {
  it("does not fall back the selected note to the root when the selected event is missing", () => {
    const rootID = "a".repeat(64);
    const selectedID = "b".repeat(64);
    const known = [{ id: rootID }];

    const { rootEvent, selectedEvent } = resolveKnownThreadEndpoints(known, rootID, selectedID);

    assert.equal(rootEvent?.id, rootID);
    assert.equal(selectedEvent, null);
  });
});

describe("resolveBundleSelectedEvent", () => {
  it("prefers the event matching selectedID over a stale selected object", () => {
    const rootID = "a".repeat(64);
    const selectedID = "b".repeat(64);
    const root = { id: rootID };
    const selected = { id: selectedID };
    const staleSelected = { id: rootID };

    const resolved = resolveBundleSelectedEvent({
      root,
      selected: staleSelected,
      selectedID,
      events: [root, selected],
    });

    assert.equal(resolved?.id, selectedID);
  });
});
