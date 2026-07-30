import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { nip19 } from "../lib/nostr-tools.js";

import { routeKind, isClientRoutePath } from "./nav-routing.js";
import { pubkeyFromProfilePath } from "./relay-utils.js";

describe("pubkeyFromProfilePath", () => {
  it("extracts hex pubkey from profile URLs", () => {
    const pk = "aa".repeat(32);
    assert.equal(pubkeyFromProfilePath(`/u/${pk}`), pk);
    assert.equal(pubkeyFromProfilePath(`/u/${encodeURIComponent(pk)}?tab=posts`), pk);
  });

  it("extracts pubkey from nprofile URLs", () => {
    const pk = "aa".repeat(32);
    const nprofile = nip19.nprofileEncode({ pubkey: pk, relays: ["wss://relay.example"] });
    assert.equal(pubkeyFromProfilePath(`/u/${encodeURIComponent(nprofile)}`), pk);
  });
});

describe("routeKind", () => {
  it("maps login to the client stub route", () => {
    assert.equal(routeKind("/login"), "stub");
  });

  it("recognizes primary client-hydrated document routes", () => {
    assert.equal(routeKind("/feed"), "feed");
    assert.equal(routeKind("/bookmarks"), "bookmarks");
    assert.equal(routeKind("/notifications"), "notifications");
  });

  it("recognizes reads list and detail routes", () => {
    assert.equal(routeKind("/reads"), "reads");
    assert.equal(routeKind("/reads/demo"), "read");
  });
});

describe("isClientRoutePath", () => {
  it("matches routeKind for client-hydrated document routes", () => {
    assert.equal(isClientRoutePath("/"), true);
    assert.equal(isClientRoutePath("/thread/abc"), true);
    assert.equal(isClientRoutePath("/u/aa"), true);
    assert.equal(isClientRoutePath("/login"), true);
    assert.equal(isClientRoutePath("/missing"), false);
    assert.equal(isClientRoutePath("/thread/abc"), Boolean(routeKind("/thread/abc")));
  });
});
