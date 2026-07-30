import assert from "node:assert/strict";
import { beforeEach, describe, it } from "node:test";

import { getPublicKey, nip19 } from "../lib/nostr-tools.js";

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
    clear() {
      data.clear();
    },
  };
}

globalThis.localStorage = makeStorage();
globalThis.sessionStorage = makeStorage();
globalThis.CustomEvent = class CustomEvent {
  constructor(type, init = {}) {
    this.type = type;
    this.detail = init.detail;
  }
};
const windowListeners = new Map();
globalThis.window = Object.assign(globalThis, {
  addEventListener(type, listener) {
    const listeners = windowListeners.get(type) || [];
    listeners.push(listener);
    windowListeners.set(type, listeners);
  },
  dispatchEvent() {},
  clearTimeout,
  setTimeout,
  location: {
    origin: "https://example.com",
    href: "https://example.com/login",
  },
});
globalThis.document = {
  addEventListener() {},
  querySelector() {
    return null;
  },
  querySelectorAll() {
    return [];
  },
};

const sessionModule = await import("./session.js");
const signerModule = await import("./signer.js");

const {
  clearSession,
  applyViewerQueryOverrides,
  getSession,
  getSessionSecretNsec,
  loginCapabilities,
  persistSigningAccount,
  relayEntriesFromNIP07Relays,
  recentSigningAccounts,
  sessionHeaders,
  removeStoredSigningAccount,
  setSession,
  switchToStoredSigningAccount,
  syncNIP07RelayConfigFromExtension,
} = sessionModule;
const { RELAY_CONFIG_KEY } = await import("./relay-state.js");
const { activeSignerState, CLIENT_METADATA_TAG, signEventDraft, withClientMetadataTag } = signerModule;
const {
  bootstrapPendingViewer,
  hasCompletedBootstrap,
  markBootstrapComplete,
  markBootstrapPending,
} = await import("./first-login-bootstrap.js");

function fixedSecret(seed) {
  return Uint8Array.from({ length: 32 }, (_, index) => (seed + index) % 256);
}

function makeSigningSession(seed, method = "yolo") {
  const secret = fixedSecret(seed);
  const pubkey = getPublicKey(secret);
  return {
    secret,
    nsec: nip19.nsecEncode(secret),
    session: {
      method,
      pubkey,
      npub: nip19.npubEncode(pubkey),
    },
  };
}

beforeEach(() => {
  delete window.nostr;
  localStorage.clear();
  sessionStorage.clear();
  clearSession();
});

describe("session signing account persistence", () => {
  it("adds Plain Text Nostr client metadata to signed publish drafts", async () => {
    const { session, nsec } = makeSigningSession(99);
    persistSigningAccount(session, nsec);
    setSession(session);
    const draft = {
      kind: 1,
      created_at: 123,
      tags: [["p", "aa".repeat(32)]],
      content: "hello",
    };

    const signed = await signEventDraft(draft, getSession());

    assert.deepEqual(signed.tags.at(-1), CLIENT_METADATA_TAG);
    assert.deepEqual(draft.tags, [["p", "aa".repeat(32)]]);
  });

  it("adds client metadata once when preparing unsigned drafts", () => {
    const draft = {
      kind: 1,
      created_at: 123,
      tags: [["client", "Plain Text Nostr"]],
      content: "hello",
    };

    const withTag = withClientMetadataTag(draft);

    assert.deepEqual(withTag.tags, [["client", "Plain Text Nostr"]]);
    assert.notEqual(withTag.tags, draft.tags);
  });

  it("normalizes NIP-07 extension relay metadata", () => {
    assert.deepEqual(
      relayEntriesFromNIP07Relays({
        "wss://write.example/": { read: false, write: true },
        "wss://read.example": { read: true, write: false },
        "wss://both.example": {},
        "https://not-a-relay.example": { read: true, write: true },
      }),
      [
        { url: "wss://write.example", read: false, write: true },
        { url: "wss://read.example", read: true, write: false },
        { url: "wss://both.example", read: true, write: true },
      ],
    );
  });

  it("handles relay config storage changes without a ReferenceError", () => {
    assert.doesNotThrow(() => {
      for (const listener of windowListeners.get("storage") || []) {
        listener({ key: RELAY_CONFIG_KEY });
      }
    });
  });

  it("restores a persisted nsec for the active session after sessionStorage is lost", () => {
    const { session, nsec } = makeSigningSession(1);
    persistSigningAccount(session, nsec);
    setSession(session);

    sessionStorage.removeItem("ptxt_nsec");

    assert.equal(getSession().pubkey, session.pubkey);
    assert.equal(getSessionSecretNsec(session), nsec);
    assert.equal(loginCapabilities(session).hasSessionSecret, true);
    assert.equal(activeSignerState(session).canSign, true);
  });

  it("keeps only the most recent three signing accounts and switches between them", () => {
    const first = makeSigningSession(11);
    const second = makeSigningSession(22);
    const third = makeSigningSession(33);
    const fourth = makeSigningSession(44);

    persistSigningAccount(first.session, first.nsec);
    persistSigningAccount(second.session, second.nsec);
    persistSigningAccount(third.session, third.nsec);
    persistSigningAccount(fourth.session, fourth.nsec);

    const recent = recentSigningAccounts();
    assert.equal(recent.length, 3);
    assert.deepEqual(
      recent.map((account) => account.pubkey),
      [fourth.session.pubkey, third.session.pubkey, second.session.pubkey],
    );

    switchToStoredSigningAccount(second.session.pubkey);
    assert.equal(getSession().pubkey, second.session.pubkey);
    assert.equal(recentSigningAccounts()[0].pubkey, second.session.pubkey);
    assert.equal(getSessionSecretNsec(), second.nsec);
    assert.equal(bootstrapPendingViewer(), second.session.pubkey);
  });

  it("removing the active stored account falls forward to the next recent account", () => {
    const first = makeSigningSession(55);
    const second = makeSigningSession(66);

    persistSigningAccount(first.session, first.nsec);
    persistSigningAccount(second.session, second.nsec);
    switchToStoredSigningAccount(second.session.pubkey);

    removeStoredSigningAccount(second.session.pubkey);

    assert.equal(getSession().pubkey, first.session.pubkey);
    assert.equal(recentSigningAccounts().length, 1);
  });

  it("persists optional account picture metadata", () => {
    const { session, nsec } = makeSigningSession(77);
    persistSigningAccount({ ...session, picture: "https://example.com/avatar.png", profileLabel: "Alice" }, nsec);
    const [stored] = recentSigningAccounts();
    assert.equal(stored.picture, "https://example.com/avatar.png");
    assert.equal(stored.profileLabel, "Alice");
  });

  it("logout clears pending first-login bootstrap but preserves completed viewers", () => {
    const { session } = makeSigningSession(88);
    markBootstrapComplete(session.pubkey);
    markBootstrapPending("ff".repeat(32));

    clearSession();

    assert.equal(bootstrapPendingViewer(), "");
    assert.equal(hasCompletedBootstrap(session.pubkey), true);
  });

  it("uses effective read and write relays in session headers", () => {
    localStorage.setItem("ptxt_relay_config", JSON.stringify({
      useAppRelays: false,
      useUserRelays: true,
      userRelayMetadata: {
        updatedAt: 0,
        relays: [
          { url: "wss://read.example", read: true, write: false },
          { url: "wss://write.example", read: false, write: true },
        ],
      },
    }));

    const headers = sessionHeaders(undefined, "/feed");

    assert.equal(headers.get("X-Ptxt-Relays"), "wss://read.example,wss://write.example");
  });

  it("uses the server-aligned guest WoT depth for first fragment requests", () => {
    localStorage.setItem("ptxt_wot_depth", "3");
    const headers = sessionHeaders(undefined, "/feed");

    assert.equal(headers.get("X-Ptxt-Wot"), "1");
    assert.equal(headers.get("X-Ptxt-Wot-Depth"), "1");
    assert.equal(localStorage.getItem("ptxt_wot_depth"), "1");
  });

  it("keeps explicit legacy URL preferences authoritative over transport defaults", () => {
    const headers = sessionHeaders(undefined, "/thread/abc");
    applyViewerQueryOverrides(headers, "/thread/abc?wot=0&wot_depth=2");

    assert.equal(headers.get("X-Ptxt-Wot"), "0");
    assert.equal(headers.get("X-Ptxt-Wot-Depth"), "2");
  });

  it("caches automatic NIP-07 relay sync until forced", async () => {
    const pubkey = "aa".repeat(32);
    let calls = 0;
    window.nostr = {
      async getRelays() {
        calls += 1;
        return { "wss://extension.example/": { read: true, write: false } };
      },
    };

    await syncNIP07RelayConfigFromExtension({ pubkey });
    await syncNIP07RelayConfigFromExtension({ pubkey });

    assert.equal(calls, 1);
    assert.deepEqual(JSON.parse(localStorage.getItem(RELAY_CONFIG_KEY)).userRelayMetadata.relays, [
      { url: "wss://extension.example", read: true, write: false },
    ]);

    await syncNIP07RelayConfigFromExtension({ pubkey, force: true });

    assert.equal(calls, 2);
  });

  it("does not repeat automatic NIP-07 relay sync prompts after rejection", async () => {
    const pubkey = "bb".repeat(32);
    let calls = 0;
    window.nostr = {
      async getRelays() {
        calls += 1;
        throw new Error("denied");
      },
    };

    await syncNIP07RelayConfigFromExtension({ pubkey });
    await syncNIP07RelayConfigFromExtension({ pubkey });

    assert.equal(calls, 1);
  });
});
