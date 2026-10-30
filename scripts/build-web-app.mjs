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
const outputForEntry = (suffix) => Object.entries(outputMap).find(([, meta]) => meta.entryPoint?.endsWith(suffix))?.[0];

// Static import closure of an output, minus chunks the page already loaded.
function staticOutputs(start, loaded = new Set()) {
	const outputs = new Set();
	const visit = (output) => {
		if (!output || outputs.has(output) || loaded.has(output)) return;
		outputs.add(output);
		for (const imported of outputMap[output]?.imports || []) {
			if (imported.kind === "dynamic-import" || imported.external) continue;
			visit(imported.path);
		}
	};
	visit(start);
	return outputs;
}

async function checkBudget(label, outputs, unit, maxGzip, maxRequests) {
	let gzip = 0;
	const report = [];
	for (const output of outputs) {
		const gzipBytes = gzipSync(await readFile(path.join(root, output))).byteLength;
		gzip += gzipBytes;
		report.push({ output, gzipBytes });
	}
	if (gzip > maxGzip || outputs.size > maxRequests) {
		console.error(report.sort((a, b) => b.gzipBytes - a.gzipBytes));
		throw new Error(`${label} budget exceeded: ${gzip} gzip bytes across ${outputs.size} ${unit} (limits ${maxGzip}/${maxRequests})`);
	}
	console.log(`${label} budget: ${gzip} gzip bytes across ${outputs.size} ${unit}`);
}

const initialOutputs = staticOutputs(outputForEntry("/app/guest-entry.js"));
await checkBudget("anonymous app", initialOutputs, "initial chunks", 150 * 1024, 20);
// Every guest page imports ascii.js once the browser is idle, so its chunks
// count against what an anonymous first visit downloads too.
const asciiOutputs = staticOutputs(outputForEntry("/js/ascii.js"), initialOutputs);
await checkBudget("anonymous idle ASCII", asciiOutputs, "more chunks", 64 * 1024, 12);
