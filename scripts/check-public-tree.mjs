import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const listed = execFileSync("git", ["ls-files", "--cached", "--others", "--exclude-standard"], { cwd: root })
  .toString("utf8").split("\n").filter(Boolean);
const blockedPath = [
  /^deploy\//i,
  /cloudformation|cloudfront/i,
  /(^|\/)\.env(?:\.|$)/i,
  /\.(?:sqlite(?:-.+)?|db|log|dmg|zip|tar|tgz|patch|diff|woff2)$/i,
  /^(?:profiles|patches)\//i,
  /(^|\/)(?:production[-_ ]?snapshot|private[-_ ]?(?:plan|audit))(?:\/|\.|$)/i,
];
const blockedContent = [
  { name: "AWS access key", pattern: /AKIA[0-9A-Z]{16}/g },
  { name: "AWS ARN", pattern: /arn:aws:[a-z0-9-]+:[a-z0-9-]*:\d{12}:/gi },
  { name: "Nostr private key", pattern: new RegExp("nsec" + "1[023456789acdefghjklmnpqrstuvwxyz]{50,}", "gi") },
  { name: "private-key PEM", pattern: new RegExp("-----BEGIN " + "(?:RSA |EC |OPENSSH )?PRIVATE KEY-----", "g") },
  { name: "personal macOS path", pattern: new RegExp("/Users/" + "[^/\\s]+/", "g") },
  { name: "Apple signing identity", pattern: new RegExp("Developer ID " + "Application:[^\\n]+\\([A-Z0-9]{10}\\)", "g") },
];
const findings = [];

for (const file of listed) {
	const fullPath = path.join(root, file);
	if (!fs.existsSync(fullPath)) continue;
  if (blockedPath.some((pattern) => pattern.test(file))) {
    findings.push(`${file}: denied path`);
    continue;
  }
  const stat = fs.statSync(fullPath);
  if (!stat.isFile() || stat.size > 8 * 1024 * 1024) continue;
  const bytes = fs.readFileSync(fullPath);
  if (bytes.includes(0)) continue;
  const text = bytes.toString("utf8");
  for (const check of blockedContent) {
    check.pattern.lastIndex = 0;
    if (check.pattern.test(text)) findings.push(`${file}: ${check.name}`);
  }
}

if (findings.length) {
  console.error(`Public-tree check failed:\n${findings.join("\n")}`);
  process.exit(1);
}
console.log(`Public-tree check passed (${listed.length} files checked).`);
