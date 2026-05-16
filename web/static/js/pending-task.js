/** Tracks one in-flight async task (e.g. “load newer notes”) for await-before-navigation. */
export function createPendingTaskSlot() {
  let current = null;
  return {
    track(task) {
      current = task;
      return task.finally(() => {
        if (current === task) current = null;
      });
    },
    awaitPending() {
      return current ? current.catch(() => {}) : Promise.resolve();
    },
  };
}
