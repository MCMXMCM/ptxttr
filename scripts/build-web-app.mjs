import esbuild from "esbuild";
import { gzipSync } from "node:zlib";
import { readFile, rm } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const outdir = path.join(root, "web", "static", "build");
await rm(outdir, { recursive: true, force: true });

const buildOptions = {
	outdir,
	bundle: true,
	splitting: true,
	format: "esm",
	platform: "browser",
	target: "es2022",
	minify: true,
	sourcemap: false,
	legalComments: "none",
	entryNames: "[name]",
	chunkNames: "chunks/[name]-[hash]",
	assetNames: "assets/[name]-[hash]",
	metafile: true,
};

const result = await esbuild.build({
	...buildOptions,
	entryPoints: { guest: path.join(root, "web", "static", "js", "app", "guest-entry.js") },
});
await esbuild.build({
	...buildOptions,
	entryPoints: { entry: path.join(root, "web", "static", "js", "app", "entry.js") },
});

const outputMap = result.metafile.outputs;
const guestOutput = Object.entries(outputMap).find(([, meta]) => meta.entryPoint?.endsWith("/app/guest-entry.js"))?.[0];
const initialOutputs = new Set();
function visitStatic(output) {
	if (!output || initialOutputs.has(output)) return;
	initialOutputs.add(output);
	for (const imported of outputMap[output]?.imports || []) {
		if (imported.kind === "dynamic-import" || imported.external) continue;
		visitStatic(imported.path);
	}
}
visitStatic(guestOutput);
let initialGzip = 0;
const initialReport = [];
for (const output of initialOutputs) {
	const gzipBytes = gzipSync(await readFile(path.join(root, output))).byteLength;
	initialGzip += gzipBytes;
	initialReport.push({ output, gzipBytes });
}
const initialRequests = initialOutputs.size;
const maxGzip = 150 * 1024;
const maxRequests = 20;
if (initialGzip > maxGzip || initialRequests > maxRequests) {
	console.error(initialReport.sort((a, b) => b.gzipBytes - a.gzipBytes));
	throw new Error(`anonymous app budget exceeded: ${initialGzip} gzip bytes across ${initialRequests} initial chunks (limits ${maxGzip}/${maxRequests})`);
}
console.log(`anonymous app budget: ${initialGzip} gzip bytes across ${initialRequests} initial chunks`);
