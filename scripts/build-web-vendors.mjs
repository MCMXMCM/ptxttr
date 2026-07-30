import esbuild from "esbuild";
import path from "node:path";
import { fileURLToPath } from "node:url";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const root = path.resolve(__dirname, "..");
const outdir = path.join(root, "web", "static", "lib");

const shared = {
  bundle: true,
  format: "esm",
  platform: "browser",
  target: "es2022",
  sourcemap: false,
  minify: false,
  legalComments: "none",
};

await Promise.all([
  esbuild.build({
    ...shared,
    entryPoints: [path.join(root, "node_modules", "@nostrify", "nostrify", "dist", "mod.js")],
    outfile: path.join(outdir, "nostrify.js"),
  }),
  esbuild.build({
    ...shared,
    entryPoints: [path.join(root, "node_modules", "@tanstack", "query-core", "build", "modern", "index.js")],
    outfile: path.join(outdir, "query-core.js"),
  }),
  esbuild.build({
    ...shared,
    entryPoints: [path.join(root, "node_modules", "@chenglou", "pretext", "dist", "layout.js")],
    outfile: path.join(outdir, "pretext.js"),
  }),
]);
