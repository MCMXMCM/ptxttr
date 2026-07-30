import test from "node:test";
import assert from "node:assert/strict";
import { allowSpeculativeWork, powerSaverActive } from "./power-mode.js";

function withBrowserGlobals(globals, fn) {
  const previous = {
    window: globalThis.window,
    document: globalThis.document,
    navigator: globalThis.navigator,
    localStorage: globalThis.localStorage,
    matchMedia: globalThis.matchMedia,
  };
  Object.entries(globals).forEach(([key, value]) => {
    Object.defineProperty(globalThis, key, {
      configurable: true,
      value,
    });
  });
  try {
    fn();
  } finally {
    Object.entries(previous).forEach(([key, value]) => {
      if (value === undefined) {
        delete globalThis[key];
      } else {
        Object.defineProperty(globalThis, key, {
          configurable: true,
          value,
        });
      }
    });
  }
}

test("narrow mobile viewport no longer auto-enables power saver", () => {
  withBrowserGlobals(
    {
      window: { innerWidth: 390 },
      document: { visibilityState: "visible" },
      navigator: { connection: {}, hardwareConcurrency: 8, maxTouchPoints: 0 },
      localStorage: { getItem: () => "auto" },
      matchMedia: (query) => ({ matches: query.includes("max-width") }),
    },
    () => {
      assert.equal(powerSaverActive(), false);
      assert.equal(allowSpeculativeWork(), true);
    },
  );
});

test("save-data still auto-enables power saver", () => {
  withBrowserGlobals(
    {
      window: { innerWidth: 390 },
      document: { visibilityState: "visible" },
      navigator: { connection: { saveData: true }, hardwareConcurrency: 8, maxTouchPoints: 1 },
      localStorage: { getItem: () => "auto" },
      matchMedia: () => ({ matches: true }),
    },
    () => {
      assert.equal(powerSaverActive(), true);
      assert.equal(allowSpeculativeWork(), false);
    },
  );
});

test("full power override keeps speculative work enabled", () => {
  withBrowserGlobals(
    {
      window: { innerWidth: 390 },
      document: { visibilityState: "visible" },
      navigator: { connection: { saveData: true }, maxTouchPoints: 1 },
      localStorage: { getItem: () => "full" },
      matchMedia: () => ({ matches: true }),
    },
    () => {
      assert.equal(powerSaverActive(), false);
      assert.equal(allowSpeculativeWork(), true);
    },
  );
});
