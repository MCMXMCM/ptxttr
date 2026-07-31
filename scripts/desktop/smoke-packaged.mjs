import { spawn } from "node:child_process";
import fs from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";

const root = path.resolve(import.meta.dirname, "..", "..");
const appPath = path.join(root, "out", "Plain Text Nostr-darwin-universal", "Plain Text Nostr.app");
const executablePath = path.join(appPath, "Contents", "MacOS", "Plain Text Nostr");
const userData = fs.mkdtempSync(path.join(os.tmpdir(), "ptxttr-packaged-smoke-"));
const logs = path.join(userData, "logs");
const logPath = path.join(logs, "desktop.log");
let child;

if (!fs.existsSync(executablePath)) {
  throw new Error(`Packaged application is missing: ${appPath}`);
}

async function waitFor(label, predicate, timeoutMS) {
  const deadline = Date.now() + timeoutMS;
  while (Date.now() < deadline) {
    if (await predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`Timed out: ${label}`);
}

async function portIsAvailable() {
  const probe = net.createServer();
  return new Promise((resolve) => {
    probe.once("error", () => resolve(false));
    probe.listen(24787, "127.0.0.1", () => probe.close(() => resolve(true)));
  });
}

function signalGroup(signal) {
  if (!child?.pid) return;
  try {
    process.kill(-child.pid, signal);
  } catch (error) {
    if (error.code !== "ESRCH") throw error;
  }
}

try {
  child = spawn(executablePath, [], {
    detached: true,
    env: {
      ...process.env,
      PTXT_ELECTRON_USER_DATA: userData,
      PTXT_ELECTRON_LOG_DIR: logs,
      PTXT_REQUEST_TIMEOUT_MS: "1000",
    },
    stdio: "ignore",
  });
  child.unref();
  await waitFor("packaged renderer loopback request", () => {
    if (!fs.existsSync(logPath)) return false;
    const log = fs.readFileSync(logPath, "utf8");
    return log.includes(`db_path=${path.join(userData, "local", "ptxt-nstr.sqlite")}`)
      && /method=GET path=\/ status=200/.test(log);
  }, 70_000);

  signalGroup("SIGTERM");
  await waitFor("packaged sidecar cleanup", portIsAvailable, 10_000);
  child = undefined;
  console.log(`Packaged application smoke test passed: ${appPath}`);
} finally {
  signalGroup("SIGKILL");
  fs.rmSync(userData, { recursive: true, force: true, maxRetries: 5, retryDelay: 100 });
}
