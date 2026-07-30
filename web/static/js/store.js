export function createStore(initialState = {}) {
  let state = { ...(initialState || {}) };
  const listeners = new Set();

  function getState() {
    return state;
  }

  function setState(partial) {
    const nextPartial = typeof partial === "function" ? partial(state) : partial;
    if (!nextPartial || typeof nextPartial !== "object") return state;
    const nextState = { ...state, ...nextPartial };
    if (nextState === state) return state;
    state = nextState;
    listeners.forEach((listener) => listener(state));
    return state;
  }

  function subscribe(listener) {
    if (typeof listener !== "function") return () => {};
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
    };
  }

  return {
    getState,
    setState,
    subscribe,
  };
}
