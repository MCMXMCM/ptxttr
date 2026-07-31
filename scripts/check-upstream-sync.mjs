import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const manifest = JSON.parse(fs.readFileSync(path.join(root, "upstream-sync.json"), "utf8"));
const source = path.resolve(root, process.env.PTXT_UPSTREAM_DIR || manifest.sourceRepository);

function globRegex(glob) {
  let value = "";
  for (let index = 0; index < glob.length; index += 1) {
    const char = glob[index];
    if (char === "*" && glob[index + 1] === "*") {
      value += ".*";
      index += 1;
    } else if (char === "*") value += "[^/]*";
    else if (char === "?") value += "[^/]";
    else value += char.replace(/[|\\{}()[\]^$+?.]/g, "\\$&");
  }
  return new RegExp(`^${value}$`);
}

const matches = (file, patterns) => patterns.some((pattern) => globRegex(pattern).test(file));
const git = (...args) => execFileSync("git", ["-C", source, ...args], { maxBuffer: 128 * 1024 * 1024 });

if (!fs.existsSync(source)) throw new Error(`Upstream repository is missing: ${source}`);
git("cat-file", "-e", `${manifest.sourceRevision}^{commit}`);
const files = git("ls-tree", "-r", "--name-only", manifest.sourceRevision)
  .toString("utf8").split("\n").filter(Boolean)
  .filter((file) => matches(file, manifest.syncAllowlist))
  .filter((file) => !matches(file, manifest.denylist));

const drift = [];
for (const file of files) {
  if (matches(file, manifest.localOverlays)) continue;
  const localPath = path.join(root, file);
  if (!fs.existsSync(localPath)) {
    drift.push(`${file}: missing locally`);
    continue;
  }
  const upstream = git("show", `${manifest.sourceRevision}:${file}`);
  const local = fs.readFileSync(localPath);
  // A few pinned Go sources have either no final newline or multiple final
  // newlines. Treat newline count at EOF as formatting, while retaining exact
  // byte comparison for every meaningful byte.
  const comparable = (buffer) => file.endsWith(".go")
    ? Buffer.from(buffer.toString("utf8").replace(/\n*$/, "\n"))
    : buffer;
  if (!comparable(upstream).equals(comparable(local))) drift.push(`${file}: differs from pinned upstream`);
}

if (drift.length) {
  console.error(`Unexpected upstream drift (${drift.length} file(s)):\n${drift.join("\n")}`);
  process.exit(1);
}
console.log(`Upstream sync check passed at ${manifest.sourceRevision.slice(0, 12)} (${files.length} allowlisted files).`);
