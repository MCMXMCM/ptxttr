// Electron's default app loader expects a synchronous CommonJS entrypoint.
// The implementation remains ESM so it can share pure policy modules with
// Node tests without a transpilation step.
import("./main.mjs").catch((error) => {
  console.error("Electron main process failed to load", error);
  process.exitCode = 1;
  const { app } = require("electron");
  if (app.isReady()) app.quit();
  else app.once("ready", () => app.quit());
});
