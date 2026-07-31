import { createRequire } from "node:module";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";

const require = createRequire(import.meta.url);
const asar = require("@electron/asar");
const appPath = path.resolve(process.argv[2] || "");
if (!appPath.endsWith(".app") || !fs.existsSync(appPath)) {
  console.error("usage: node scripts/check-release-artifact.mjs /path/to/App.app");
  process.exit(2);
}

const findings = [];
const contentChecks = [
  ["AWS access key", /AKIA[0-9A-Z]{16}/g],
  ["AWS ARN", /arn:aws:[a-z0-9-]+:[a-z0-9-]*:\d{12}:/gi],
  ["Nostr private key", new RegExp("nsec" + "1[023456789acdefghjklmnpqrstuvwxyz]{50,}", "gi")],
  ["private-key PEM", new RegExp("-----BEGIN " + "(?:RSA |EC |OPENSSH )?PRIVATE KEY-----", "g")],
  ["personal macOS path", new RegExp("/Users/" + "[^/\\s]+/", "g")],
];
const deniedName = /(^|\/)(?:deploy|profiles|patches)(\/|$)|\.(?:env|sqlite(?:-.+)?|db|log|patch|diff|woff2)$/i;

function scanTree(directory, label) {
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const fullPath = path.join(directory, entry.name);
    const relative = path.relative(directory, fullPath).replaceAll(path.sep, "/");
    if (deniedName.test(relative)) findings.push(`${label}/${relative}: denied path`);
    if (entry.isDirectory()) scanTree(fullPath, `${label}/${entry.name}`);
    else if (entry.isFile() && entry.name !== "app.asar" && entry.name !== "ptxt-nstr-server" && entry.name !== "Plain Text Nostr") {
      const bytes = fs.readFileSync(fullPath);
      if (bytes.length <= 8 * 1024 * 1024 && !bytes.includes(0)) scanText(bytes.toString("utf8"), `${label}/${entry.name}`);
    }
  }
}

function scanText(text, label) {
  for (const [name, pattern] of contentChecks) {
    pattern.lastIndex = 0;
    if (pattern.test(text)) findings.push(`${label}: ${name}`);
  }
}

const resources = path.join(appPath, "Contents", "Resources");
const extracted = fs.mkdtempSync(path.join(os.tmpdir(), "ptxttr-asar-"));
try {
  asar.extractAll(path.join(resources, "app.asar"), extracted);
  scanTree(extracted, "app.asar");
  const asarFiles = [];
  const collect = (directory) => {
    for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
      const fullPath = path.join(directory, entry.name);
      if (entry.isDirectory()) collect(fullPath);
      else asarFiles.push(path.relative(extracted, fullPath).replaceAll(path.sep, "/"));
    }
  };
  collect(extracted);
  const expected = ["desktop/main.cjs", "desktop/main.mjs", "desktop/policy.mjs", "desktop/startup.html", "package.json"];
  if (asarFiles.sort().join("\n") !== expected.sort().join("\n")) {
    findings.push(`app.asar: unexpected files: ${asarFiles.join(", ")}`);
  }
  const sidecar = fs.readFileSync(path.join(resources, "bin", "ptxt-nstr-server"));
  const printable = sidecar.toString("latin1").match(/[ -~]{12,}/g)?.join("\n") || "";
  scanText(printable, "resources/bin/ptxt-nstr-server");
} finally {
  fs.rmSync(extracted, { recursive: true, force: true });
}

if (findings.length) {
  console.error(`Release-artifact check failed:\n${findings.join("\n")}`);
  process.exit(1);
}
console.log(`Release-artifact check passed: ${appPath}`);
