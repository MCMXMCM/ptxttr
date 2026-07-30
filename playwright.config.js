// @ts-check
import { defineConfig, devices } from "@playwright/test";

const port = Number(process.env.PTXT_E2E_PORT || 18080);
const explicitBaseURL = String(process.env.PTXT_E2E_BASE_URL || "").trim();
const baseURL = explicitBaseURL || `http://127.0.0.1:${port}`;
const attachToExistingServer =
  String(process.env.PTXT_E2E_ATTACH || "").trim() === "1" ||
  String(process.env.PTXT_E2E_REUSE_SERVER || "").trim() === "1";

export default defineConfig({
  testDir: "e2e",
  timeout: 60_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  retries: process.env.CI ? 1 : 0,
  reporter: [["list"]],
  projects: [
    {
      name: "chromium",
      use: { browserName: "chromium" },
    },
    {
      name: "webkit-iphone",
      testMatch: /(?:guest-(?:state-regressions|v2-documents)|thread-wot)\.spec\.js/,
      use: { ...devices["iPhone 13"], browserName: "webkit" },
    },
    {
      name: "webkit-desktop",
      testMatch: /guest-v2-documents\.spec\.js/,
      use: { browserName: "webkit" },
    },
    {
      name: "chromium-no-js",
      testMatch: /guest-v2-documents\.spec\.js/,
      use: { browserName: "chromium", javaScriptEnabled: false },
    },
  ],
  use: {
    baseURL,
    trace: "on-first-retry",
  },
  webServer: attachToExistingServer
    ? undefined
    : {
      command: "bash scripts/e2e-webserver.sh",
      url: baseURL,
      reuseExistingServer: false,
      timeout: 180_000,
      cwd: import.meta.dirname,
    },
});
