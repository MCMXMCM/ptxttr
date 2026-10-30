import assert from "node:assert/strict";
import path from "node:path";
import { describe, it } from "node:test";
import { fileURLToPath } from "node:url";
import esbuild from "esbuild";

const staticRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

// Guest-v2 documents never talk to relays or sign events. These modules (and
// the libraries behind them) may load after a viewer acts, but nothing an
// anonymous page loads at startup, on idle or for thread controls may import
// them statically.
const relayAndSigningModules = [
  "lib/nostrify.js",
  "lib/query-core.js",
  "js/relay-pool.js",
  "js/relay-reads.js",
  "js/event-store.js",
  "js/publish.js",
  "js/signer.js",
  "js/mutations.js",
];

const { metafile } = await esbuild.build({
  absWorkingDir: staticRoot,
  entryPoints: ["js/app/guest-entry.js"],
  outdir: "build",
  bundle: true,
  splitting: true,
  format: "esm",
  platform: "browser",
  target: "es2022",
  write: false,
  metafile: true,
  logLevel: "silent",
});

function staticInputs(entryPoint) {
  const outputs = metafile.outputs;
  const start = Object.keys(outputs).find((name) => outputs[name].entryPoint === entryPoint);
  assert.ok(start, `no output for ${entryPoint}`);
  const visited = new Set();
  const inputs = new Set();
  const visit = (name) => {
    if (visited.has(name)) return;
    visited.add(name);
    Object.keys(outputs[name].inputs).forEach((input) => inputs.add(input));
    outputs[name].imports.forEach((entry) => {
      if (entry.kind === "import-statement" && !entry.external) visit(entry.path);
    });
  };
  visit(start);
  return inputs;
}

function relayAndSigningModulesIn(entryPoint) {
  const inputs = staticInputs(entryPoint);
  return relayAndSigningModules.filter((name) => inputs.has(name));
}

describe("anonymous bundle", () => {
  it("keeps relay and signing modules out of the startup chunks", () => {
    assert.deepEqual(relayAndSigningModulesIn("js/app/guest-entry.js"), []);
  });

  it("keeps them out of the idle ASCII enhancement", () => {
    assert.deepEqual(relayAndSigningModulesIn("js/ascii.js"), []);
  });

  it("keeps them out of guest thread controls and tree mode", () => {
    assert.deepEqual(relayAndSigningModulesIn("js/thread.js"), []);
  });
});
