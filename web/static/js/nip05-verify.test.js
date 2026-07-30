import assert from "node:assert/strict";
import { afterEach, describe, it } from "node:test";

if (typeof globalThis.localStorage === "undefined") {
  globalThis.localStorage = {
    getItem() { return ""; },
    setItem() {},
    removeItem() {},
  };
}

if (typeof globalThis.sessionStorage === "undefined") {
  globalThis.sessionStorage = {
    getItem() { return ""; },
    setItem() {},
    removeItem() {},
  };
}

if (typeof globalThis.document === "undefined") {
  globalThis.document = {
    querySelectorAll() { return []; },
    addEventListener() {},
  };
}

if (typeof globalThis.window === "undefined") {
  globalThis.window = {
    location: { origin: "https://plaintextnostr.com" },
    addEventListener() {},
    dispatchEvent() {},
  };
}

const { queryNIP05Profile, refreshNIP05Verification } = await import("./nip05-verify.js");

const originalFetch = globalThis.fetch;
const originalDocument = globalThis.document;

afterEach(() => {
  globalThis.fetch = originalFetch;
  globalThis.document = originalDocument;
});

describe("queryNIP05Profile", () => {
  it("matches a lowercase identifier directly", async () => {
    globalThis.fetch = async (input) => {
      assert.equal(String(input), "https://example.com/.well-known/nostr.json?name=matt");
      return {
        ok: true,
        async json() {
          return { names: { matt: "ab".repeat(32) } };
        },
      };
    };

    const profile = await queryNIP05Profile("matt@example.com");
    assert.equal(profile?.pubkey, "ab".repeat(32));
  });

  it("matches case-insensitively when the document uses different capitalization", async () => {
    globalThis.fetch = async () => ({
      ok: true,
      async json() {
        return { names: { Matt: "cd".repeat(32) } };
      },
    });

    const profile = await queryNIP05Profile("matt@example.com");
    assert.equal(profile?.pubkey, "cd".repeat(32));
  });

  it("normalizes uppercase identifiers before requesting the well-known document", async () => {
    globalThis.fetch = async (input) => {
      assert.equal(String(input), "https://example.com/.well-known/nostr.json?name=matt");
      return {
        ok: true,
        async json() {
          return { names: { matt: "ef".repeat(32) } };
        },
      };
    };

    const profile = await queryNIP05Profile("Matt@Example.com");
    assert.equal(profile?.pubkey, "ef".repeat(32));
  });

  it("returns null when the document does not contain the requested name", async () => {
    globalThis.fetch = async () => ({
      ok: true,
      async json() {
        return { names: { alice: "ab".repeat(32) } };
      },
    });

    const profile = await queryNIP05Profile("matt@example.com");
    assert.equal(profile, null);
  });

  it("dedupes concurrent verification for matching profile nodes", async () => {
    let fetchCalls = 0;
    globalThis.fetch = async () => {
      fetchCalls += 1;
      return {
        ok: true,
        async json() {
          return { names: { matt: "ab".repeat(32) } };
        },
      };
    };

    function makeStatusNode() {
      return {
        classList: { add() {}, remove() {} },
        textContent: "",
        innerHTML: "",
      };
    }

    function makeNode() {
      const statusNode = makeStatusNode();
      return {
        dataset: {},
        getAttribute(name) {
          if (name === "data-nip05") return "matt@example.com";
          if (name === "data-pubkey") return "ab".repeat(32);
          return "";
        },
        querySelector(selector) {
          return selector === "[data-nip05-status]" ? statusNode : null;
        },
      };
    }

    const first = makeNode();
    const second = makeNode();
    globalThis.document = {
      querySelectorAll() {
        return [first, second];
      },
      addEventListener() {},
    };

    refreshNIP05Verification(globalThis.document);
    await new Promise((resolve) => setTimeout(resolve, 0));
    await new Promise((resolve) => setTimeout(resolve, 0));

    assert.equal(fetchCalls, 1);
    assert.equal(first.dataset.nip05Status, "verified");
    assert.equal(second.dataset.nip05Status, "verified");
  });

  it("caches unreachable results so repeated refreshes do not refetch immediately", async () => {
    let fetchCalls = 0;
    globalThis.fetch = async () => {
      fetchCalls += 1;
      throw new TypeError("Failed to fetch");
    };

    function makeStatusNode() {
      return {
        classList: { add() {}, remove() {} },
        textContent: "",
        innerHTML: "",
      };
    }

    function makeNode() {
      const statusNode = makeStatusNode();
      return {
        dataset: {},
        getAttribute(name) {
          if (name === "data-nip05") return "matt-unreachable@example.com";
          if (name === "data-pubkey") return "cd".repeat(32);
          return "";
        },
        querySelector(selector) {
          return selector === "[data-nip05-status]" ? statusNode : null;
        },
      };
    }

    const first = makeNode();
    globalThis.document = {
      querySelectorAll() {
        return [first];
      },
      addEventListener() {},
    };

    refreshNIP05Verification(globalThis.document);
    await new Promise((resolve) => setTimeout(resolve, 0));
    await new Promise((resolve) => setTimeout(resolve, 0));

    assert.equal(fetchCalls, 1);
    assert.equal(first.dataset.nip05Status, "unreachable");

    const second = makeNode();
    globalThis.document = {
      querySelectorAll() {
        return [second];
      },
      addEventListener() {},
    };

    refreshNIP05Verification(globalThis.document);
    await new Promise((resolve) => setTimeout(resolve, 0));
    await new Promise((resolve) => setTimeout(resolve, 0));

    assert.equal(fetchCalls, 1);
    assert.equal(second.dataset.nip05Status, "unreachable");
  });

  it("keeps server-rendered NIP-5 status instead of refetching", async () => {
    let fetchCalls = 0;
    globalThis.fetch = async () => {
      fetchCalls += 1;
      throw new Error("should not fetch");
    };

    const statusNode = {
      dataset: {
        nip05StatusKind: "verified",
        nip05StatusDetail: "NIP-5 verified for this profile.",
      },
      classList: { add() {}, remove() {} },
      textContent: "✓",
      hidden: false,
    };
    const node = {
      dataset: {},
      getAttribute(name) {
        if (name === "data-nip05") return "matt@example.com";
        if (name === "data-pubkey") return "ab".repeat(32);
        return "";
      },
      querySelector(selector) {
        return selector === "[data-nip05-status]" ? statusNode : null;
      },
    };
    globalThis.document = {
      querySelectorAll() {
        return [node];
      },
      addEventListener() {},
    };

    refreshNIP05Verification(globalThis.document);
    await new Promise((resolve) => setTimeout(resolve, 0));

    assert.equal(fetchCalls, 0);
    assert.equal(node.dataset.nip05Loaded, "1");
    assert.equal(node.dataset.nip05Status, "verified");
  });
});
