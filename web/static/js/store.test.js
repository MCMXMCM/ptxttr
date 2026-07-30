import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { createStore } from "./store.js";

describe("createStore", () => {
  it("supports getState, setState, and subscribe", () => {
    const store = createStore({ count: 0, label: "a" });
    const seen = [];
    const unsubscribe = store.subscribe((state) => {
      seen.push({ ...state });
    });

    store.setState({ count: 1 });
    assert.deepEqual(store.getState(), { count: 1, label: "a" });
    assert.deepEqual(seen, [{ count: 1, label: "a" }]);

    unsubscribe();
    store.setState((state) => ({ count: state.count + 1 }));
    assert.deepEqual(store.getState(), { count: 2, label: "a" });
    assert.deepEqual(seen, [{ count: 1, label: "a" }]);
  });
});
