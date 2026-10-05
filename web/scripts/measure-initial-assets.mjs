// Run after `npm run build -- --manifest`. Only static imports belong to the
// initial JS/CSS closure; dynamic entry points are reported separately.
import { readFileSync, statSync } from "node:fs";
import { resolve } from "node:path";
import { gzipSync } from "node:zlib";

const directory = resolve(process.argv[2] ?? "dist");
const manifest = JSON.parse(readFileSync(resolve(directory, ".vite/manifest.json"), "utf8"));
const entry = Object.keys(manifest).find((key) => manifest[key].isEntry && key === "index.html");
if (!entry) throw new Error("Missing index.html entry in the Vite manifest");
const visited = new Set();
const scripts = new Set();
const styles = new Set();
function visit(key) {
  if (visited.has(key)) return;
  visited.add(key);
  const chunk = manifest[key];
  if (!chunk) throw new Error(`Missing static import: ${key}`);
  if (chunk.file.endsWith(".js")) scripts.add(chunk.file);
  for (const style of chunk.css ?? []) styles.add(style);
  for (const dependency of chunk.imports ?? []) visit(dependency);
}
visit(entry);
function measure(files) {
  const assets = [...files].sort().map((file) => {
    const content = readFileSync(resolve(directory, file));
    return { file, bytes: content.length, gzipBytes: gzipSync(content).length };
  });
  return { bytes: assets.reduce((sum, asset) => sum + asset.bytes, 0),
    gzipBytes: assets.reduce((sum, asset) => sum + asset.gzipBytes, 0), assets };
}
const deferredEntryPoints = Object.entries(manifest)
  .filter(([key, chunk]) => key.startsWith("src/") && chunk.isDynamicEntry)
  .map(([source, chunk]) => ({ source, file: chunk.file, inInitialClosure: visited.has(source) }));
const fonts = Object.entries(manifest).filter(([key]) => /HarmonyOSSansSC-.+\.ttf$/u.test(key))
  .map(([source, chunk]) => ({ source, bytes: statSync(resolve(directory, chunk.file)).size }));
console.log(JSON.stringify({ initialJS: measure(scripts), initialCSS: measure(styles),
  deferredEntryPoints, officialFonts: fonts }, null, 2));
