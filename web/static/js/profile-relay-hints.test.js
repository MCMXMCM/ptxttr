import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  mergeProfileRelays,
  profileAuthorWriteRelays,
  profileDisplayRelays,
  profileFallbackRelays,
  profileRelayHintsToList,
  profileRelaysFromHTML,
} from "./profile-relay-hints.js";

describe("profile relay hint recovery", () => {
  it("parses relay hints from the server relays fragment", () => {
    const html = `
      <p class="muted">Suggested read/write relays from this user's latest kind 10002 event.</p>
      <ul class="relay-list">
        <li><code data-check-relay="wss://relay.example.com">wss://relay.example.com</code></li>
        <li><code data-check-relay="wss://relay.primal.net">wss://relay.primal.net</code></li>
      </ul>
    `;
    assert.deepEqual(profileRelaysFromHTML(html), [
      "wss://relay.example.com",
      "wss://relay.primal.net",
    ]);
  });

  it("merges server relay hints with the existing client relay set", () => {
    assert.deepEqual(
      mergeProfileRelays(
        ["wss://relay.example.com", "wss://relay.primal.net"],
        ["wss://relay.primal.net", "wss://nos.lol"],
      ),
      ["wss://relay.example.com", "wss://relay.primal.net", "wss://nos.lol"],
    );
  });

  it("does not treat fallback-only relays as displayable profile relay hints", () => {
    const defaults = [
      "wss://relay.primal.net",
      "wss://relay.damus.io",
      "wss://nos.lol",
    ];
    assert.deepEqual(profileDisplayRelays(defaults, defaults), []);
    assert.deepEqual(
      profileDisplayRelays(["wss://relay.primal.net", "wss://author.example"], defaults),
      ["wss://relay.primal.net", "wss://author.example"],
    );
  });

  it("includes the viewer's follow-list relay hint for the target profile in fallback relays", () => {
    const target = "ab".repeat(32);
    const followHints = new Map([[target, "wss://follow-hint.example"]]);
    assert.deepEqual(
      profileFallbackRelays(
        { read: ["wss://profile-read.example"], write: [], any: ["wss://profile-any.example"] },
        followHints,
        target,
      ),
      [
        "wss://profile-read.example",
        "wss://profile-any.example",
        "wss://follow-hint.example",
      ],
    );
  });

  it("flattens kind-10002 relay hints into a normalized relay list", () => {
    const hints = {
      read: ["wss://read.example", "wss://shared.example"],
      write: ["wss://write.example"],
      any: ["wss://shared.example", "wss://any.example"],
    };
    assert.deepEqual(
      profileRelayHintsToList(hints),
      [
        "wss://read.example",
        "wss://shared.example",
        "wss://write.example",
        "wss://any.example",
      ],
    );
    assert.deepEqual(profileAuthorWriteRelays(hints), [
      "wss://write.example",
      "wss://shared.example",
      "wss://any.example",
    ]);
  });
});
