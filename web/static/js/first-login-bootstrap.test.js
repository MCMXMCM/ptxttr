import assert from "node:assert/strict";
import { beforeEach, describe, it } from "node:test";

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

const lifecycle = await import("./first-login-bootstrap.js");

const {
  bootstrapPendingViewer,
  clearBootstrapPending,
  clearBootstrapPendingIfViewerChanged,
  hasCompletedBootstrap,
  markBootstrapComplete,
  markBootstrapPending,
  shouldShowFirstLoginBootstrap,
} = lifecycle;

const VIEWER_A = "ab".repeat(32);
const VIEWER_B = "cd".repeat(32);

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
});

describe("first-login bootstrap lifecycle", () => {
  it("sets a pending marker for a new viewer", () => {
    assert.equal(markBootstrapPending(VIEWER_A), true);
    assert.equal(bootstrapPendingViewer(), VIEWER_A);
    assert.equal(shouldShowFirstLoginBootstrap(VIEWER_A), true);
  });

  it("skips the pending marker for a completed viewer", () => {
    markBootstrapComplete(VIEWER_A);
    assert.equal(hasCompletedBootstrap(VIEWER_A), true);
    assert.equal(markBootstrapPending(VIEWER_A), false);
    assert.equal(bootstrapPendingViewer(), "");
    assert.equal(shouldShowFirstLoginBootstrap(VIEWER_A), false);
  });

  it("clears pending state on completion while preserving completion history", () => {
    markBootstrapPending(VIEWER_A);
    markBootstrapComplete(VIEWER_A);
    assert.equal(bootstrapPendingViewer(), "");
    assert.equal(hasCompletedBootstrap(VIEWER_A), true);
    assert.equal(shouldShowFirstLoginBootstrap(VIEWER_A), false);
  });

  it("clears pending state when the active viewer changes", () => {
    markBootstrapPending(VIEWER_A);
    clearBootstrapPendingIfViewerChanged(VIEWER_B);
    assert.equal(bootstrapPendingViewer(), "");
    assert.equal(shouldShowFirstLoginBootstrap(VIEWER_A), false);
  });

  it("keeps pending state when the same viewer remains active", () => {
    markBootstrapPending(VIEWER_A);
    clearBootstrapPendingIfViewerChanged(VIEWER_A);
    assert.equal(bootstrapPendingViewer(), VIEWER_A);
  });

  it("clears only the pending marker without touching completed viewers", () => {
    markBootstrapComplete(VIEWER_A);
    markBootstrapPending(VIEWER_B);
    clearBootstrapPending();
    assert.equal(bootstrapPendingViewer(), "");
    assert.equal(hasCompletedBootstrap(VIEWER_A), true);
    assert.equal(hasCompletedBootstrap(VIEWER_B), false);
  });
});
