// Bundles src/index.js into the single file the Go binary embeds.
// dist/comments.js is COMMITTED so `go build` never needs Node; CI rebuilds it
// and fails if the checked-in copy drifted from source.
import { build } from "esbuild";
import { readFileSync } from "node:fs";

// Spec section 7 said 25 KB; the 2026-09 panel redesign (segmented filters,
// threaded replies, inline icons) needed the extra headroom, and suggested
// edits (2026-10) took it from 32 KB to 36 KB. The built-in translations for
// nine languages (src/i18n.js, 2026-10) add about 15 KB, 6 KB gzipped.
// Page state (src/state.js, window.byagent.state) adds about 3.5 KB.
const LIMIT = 55296;

const result = await build({
  entryPoints: ["src/index.js"],
  bundle: true,
  minify: true,
  format: "iife",
  target: ["es2020"],
  outfile: "dist/comments.js",
  legalComments: "none",
  // Keep the translations as UTF-8 rather than \uXXXX escapes, which double
  // the size of every CJK character. The runtime is served as
  // text/javascript; charset=utf-8, so the page's own encoding never applies.
  charset: "utf8",
});

if (result.errors.length) process.exit(1);

const bytes = readFileSync("dist/comments.js").byteLength;
console.log(`dist/comments.js  ${bytes} bytes  (limit ${LIMIT})`);
if (bytes > LIMIT) {
  console.error(`runtime bundle is ${bytes - LIMIT} bytes over the budget`);
  process.exit(1);
}
