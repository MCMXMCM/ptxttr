import test from "node:test";
import assert from "node:assert/strict";

import {
  appBootstrap,
  readAppBootstrap,
  readRouteContext,
  setAppBootstrapForTests,
} from "./app/bootstrap.js";

function installJSONScripts({ routeContext, appBootstrapJSON, desktopMode = false } = {}) {
  const scripts = new Map();
  if (routeContext) {
    scripts.set("ptxt-route-context", { textContent: JSON.stringify(routeContext) });
  }
  if (appBootstrapJSON) {
    scripts.set("ptxt-app-bootstrap", { textContent: JSON.stringify(appBootstrapJSON) });
  }
  globalThis.document = {
    documentElement: {
      dataset: desktopMode ? { ptxtDesktopMode: "1" } : {},
    },
    getElementById(id) {
      return scripts.get(id) || null;
    },
  };
}

test("readRouteContext normalizes shell route payload", () => {
  installJSONScripts({
    routeContext: {
      path: "/thread/abc",
      search: "foo=1",
      hash: "bar",
      route: "thread",
      isCrawler: true,
    },
  });
  assert.deepEqual(readRouteContext(), {
    path: "/thread/abc",
    search: "foo=1",
    hash: "bar",
    route: "thread",
    isCrawler: true,
  });
});

test("readAppBootstrap falls back to route context and exposes feature flags", () => {
  installJSONScripts({
    routeContext: {
      path: "/feed",
      search: "",
      hash: "",
      route: "feed",
      isCrawler: false,
    },
    appBootstrapJSON: {
      version: 1,
      assetBasePath: "/static/test",
      route: {
        path: "/feed",
        search: "",
        hash: "",
        route: "feed",
        isCrawler: false,
      },
      viewer: {
        pubkey: "abc",
        seedPubkey: "def",
        relays: ["wss://relay.example"],
      },
      features: {
        documentNavigation: true,
        indexedDb: true,
      },
      guest: {
        defaultWOTSeedNpub: "npub1seed",
        defaultWOTDepth: 3,
      },
    },
  });
  setAppBootstrapForTests(readAppBootstrap());
  assert.equal(appBootstrap().viewer.pubkey, "abc");
  assert.equal(appBootstrap().features.documentNavigation, true);
  assert.equal(appBootstrap().features.browserWrites, true);
});

test("readAppBootstrap keeps the sidecar authoritative for full desktop documents without bootstrap JSON", () => {
  installJSONScripts({
    desktopMode: true,
    routeContext: {
      path: "/",
      search: "",
      hash: "",
      route: "feed",
      isCrawler: false,
    },
  });
  const bootstrap = readAppBootstrap();
  assert.equal(bootstrap.features.directRelayReads, false);
  assert.equal(bootstrap.features.relayNativeRoutesPrimary, false);
  assert.equal(bootstrap.features.indexedDb, false);
});

test("readAppBootstrap keeps direct relay reads disabled for ordinary full documents", () => {
  installJSONScripts();
  const bootstrap = readAppBootstrap();
  assert.equal(bootstrap.features.directRelayReads, false);
  assert.equal(bootstrap.features.relayNativeRoutesPrimary, false);
});

test("readAppBootstrap normalizes an initial profile seed", () => {
  installJSONScripts({
    appBootstrapJSON: {
      version: 1,
      assetBasePath: "/static/test",
      route: {
        path: "/u/abc",
        search: "",
        hash: "",
        route: "profile",
        isCrawler: false,
      },
      viewer: {
        pubkey: "",
        seedPubkey: "",
        relays: [],
      },
      features: {
        documentNavigation: true,
      },
      guest: {
        defaultWOTSeedNpub: "npub1seed",
        defaultWOTDepth: 3,
      },
      initialProfile: {
        pubkey: "ABCD",
        display_name: "Alice",
        picture: "https://example.com/alice.png",
        relay_hints: ["wss://relay.example"],
      },
    },
  });
  const bootstrap = readAppBootstrap();
  assert.deepEqual(bootstrap.initialProfile, {
    pubkey: "abcd",
    name: "",
    display_name: "Alice",
    about: "",
    picture: "https://example.com/alice.png",
    website: "",
    nip05: "",
    lud16: "",
    lud06: "",
    event_id: "",
    created_at: 0,
    relay_hints: ["wss://relay.example"],
  });
});
