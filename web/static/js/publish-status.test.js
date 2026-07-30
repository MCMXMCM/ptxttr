import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { pendingPublishStatus, publishStatusRows } from "./publish-status.js";

describe("pendingPublishStatus", () => {
  it("captures the initial in-progress publish state", () => {
    const state = pendingPublishStatus({
      phaseTitle: "Broadcasting to relays",
      statusMessage: "Preparing relay broadcast...",
      plannedRelays: ["wss://relay.one", "wss://relay.two"],
      completionMessage: "publish complete.",
    });
    assert.equal(state.phaseTitle, "Broadcasting to relays");
    assert.equal(state.statusMessage, "Preparing relay broadcast...");
    assert.deepEqual(state.plannedRelays, ["wss://relay.one", "wss://relay.two"]);
    assert.equal(state.completionMessage, "publish complete.");
  });
});

describe("publishStatusRows", () => {
  it("renders pending planned relays before a payload arrives", () => {
    assert.deepEqual(
      publishStatusRows(null, ["wss://relay.one"]),
      [{
        relay: "wss://relay.one",
        badge: "[...]",
        detail: "pending",
        state: "pending",
      }],
    );
  });

  it("renders completed relay results with success and failure states", () => {
    const rows = publishStatusRows({
      relay_stats: [
        { relay_url: "wss://relay.one", accepted: true, message: "ok" },
        { relay_url: "wss://relay.two", accepted: false, error: "denied" },
      ],
    });
    assert.deepEqual(rows, [
      {
        relay: "wss://relay.one",
        badge: "[ OK ]",
        detail: "ok",
        state: "success",
      },
      {
        relay: "wss://relay.two",
        badge: "[ X ]",
        detail: "denied",
        state: "failed",
      },
    ]);
  });
});
