import assert from "node:assert/strict";
import { describe, it } from "node:test";

function makeStorage() {
  const data = new Map();
  return {
    getItem(key) {
      return data.has(key) ? data.get(key) : null;
    },
    setItem(key, value) {
      data.set(key, String(value));
    },
    removeItem(key) {
      data.delete(key);
    },
  };
}

if (typeof globalThis.localStorage === "undefined") globalThis.localStorage = makeStorage();
if (typeof globalThis.sessionStorage === "undefined") globalThis.sessionStorage = makeStorage();
if (typeof globalThis.window === "undefined") {
  globalThis.window = { addEventListener() {}, dispatchEvent() {}, location: { origin: "https://example.com", href: "https://example.com" } };
}
if (typeof globalThis.document === "undefined") {
  globalThis.document = {
    addEventListener() {},
    querySelectorAll() { return []; },
  };
}

const {
  PollType,
  parsePollEvent,
  dedupePollVotes,
  selectedOptionIDs,
  tallyPollVotes,
} = await import("./poll.js");

describe("poll", () => {
  it("parses poll metadata from kind 1068", () => {
    const poll = parsePollEvent({
      id: "a".repeat(64),
      kind: 1068,
      pubkey: "b".repeat(64),
      content: "Best client?",
      tags: [
        ["option", "1", "ptxt"],
        ["option", "2", "other"],
        ["polltype", "multiplechoice"],
        ["endsAt", "2000000000"],
        ["relay", "wss://relay.example.com"],
      ],
    });
    assert.equal(poll.question, "Best client?");
    assert.equal(poll.pollType, PollType.MULTIPLE);
    assert.equal(poll.options.length, 2);
    assert.equal(poll.relays[0], "wss://relay.example.com");
  });

  it("dedupes votes by latest pubkey and tallies multiple-choice selections", () => {
    const votes = dedupePollVotes([
      { pubkey: "a".repeat(64), created_at: 1, tags: [["response", "1"]] },
      { pubkey: "a".repeat(64), created_at: 2, tags: [["response", "2"], ["response", "3"]] },
      { pubkey: "b".repeat(64), created_at: 1, tags: [["response", "2"]] },
    ]);
    const tally = tallyPollVotes(votes, PollType.MULTIPLE);
    assert.deepEqual(tally, { 2: 2, 3: 1 });
    assert.deepEqual([...selectedOptionIDs(votes[0], PollType.MULTIPLE)], ["2", "3"]);
  });
});
