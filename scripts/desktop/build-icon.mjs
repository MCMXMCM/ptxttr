import fs from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
// This is the established Plain Text Nostr app mark, used by the prior native
// client and distinct from the in-product Ascritch artwork.
const source = path.join(root, "web", "static", "img", "ascritch_icon_black.png");
const target = path.join(root, "desktop", "icon.icns");
const png = await fs.readFile(source);

if (png.readUInt32BE(0) !== 0x89504e47) {
  throw new Error(`Expected a PNG app icon at ${source}`);
}

// `ic10` is Apple's native 1024px PNG ICNS representation. macOS generates
// the smaller Dock, Finder, and Spotlight renditions from it without requiring
// an image tool or a fragile iconset conversion step.
const chunk = Buffer.alloc(8);
chunk.write("ic10", 0, "ascii");
chunk.writeUInt32BE(chunk.length + png.length, 4);
const header = Buffer.alloc(8);
header.write("icns", 0, "ascii");
header.writeUInt32BE(header.length + chunk.length + png.length, 4);

await fs.writeFile(target, Buffer.concat([header, chunk, png]), { mode: 0o644 });
