import { appBootstrap } from "./bootstrap.js";
import {
  readAppBootstrapSnapshot,
  saveAppBootstrapSnapshot,
} from "../store/bootstrap-store.js";

async function primeShellBootstrap() {
  const bootstrap = appBootstrap();
  await saveAppBootstrapSnapshot(bootstrap).catch(() => {});
  const previous = await readAppBootstrapSnapshot().catch(() => null);
  if (previous && !globalThis.window?.__ptxtPreviousAppBootstrap) {
    globalThis.window.__ptxtPreviousAppBootstrap = previous;
  }
}

export function initAppLifecycle() {
  if (!globalThis.document?.body?.classList?.contains?.("feed-shell")) return;
  void primeShellBootstrap();
}
