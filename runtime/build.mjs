// Bundles src/index.js into the single file the Go binary embeds.
// dist/comments.js is COMMITTED so `go build` never needs Node; CI rebuilds it
// and fails if the checked-in copy drifted from source.
import { build } from "esbuild";
import { readFileSync } from "node:fs";

// Spec section 7 said 25 KB; the 2026-09 panel redesign (segmented filters,
// threaded replies, inline icons) needed the extra headroom, and suggested
// edits (2026-10) took it from 32 KB to 36 KB.
const LIMIT = 36864;

const result = await build({
  entryPoints: ["src/index.js"],
  bundle: true,
  minify: true,
  format: "iife",
  target: ["es2020"],
  outfile: "dist/comments.js",
  legalComments: "none",
});

if (result.errors.length) process.exit(1);

const bytes = readFileSync("dist/comments.js").byteLength;
console.log(`dist/comments.js  ${bytes} bytes  (limit ${LIMIT})`);
if (bytes > LIMIT) {
  console.error(`runtime bundle is ${bytes - LIMIT} bytes over the budget`);
  process.exit(1);
}
