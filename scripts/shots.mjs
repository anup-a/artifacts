#!/usr/bin/env node
// Screenshots a page at several widths and themes, for checking a UI change.
//
//   node scripts/shots.mjs [url] [--widths 1440,640,390] [--themes light,dark]
//     [--selector "#how .seg"] [--out /tmp/byagent-shots] [--height 900]
//     [--wait 600] [--full]
//
// url defaults to the scripts/dev.sh server. Each shot is <out>/<theme>-<width>.png,
// clipped to --selector when given (scrolled into view first), else the
// viewport (or the whole page with --full). Animations are frozen through
// prefers-reduced-motion, so a section caught mid fade-in doesn't look washed
// out. The theme is set the way the site stores it, before any script runs.
//
// Uses runtime/'s Playwright, the one CI runs, so its pinned browser is the
// one already installed. First time in a checkout: npm --prefix runtime ci
import { createRequire } from "node:module";
import { mkdirSync } from "node:fs";

const require = createRequire(new URL("../runtime/package.json", import.meta.url));
let chromium;
try {
  ({ chromium } = require("@playwright/test"));
} catch {
  console.error("Playwright not installed: run npm --prefix runtime ci");
  process.exit(1);
}

const args = process.argv.slice(2);
const opt = (name, def) => {
  const i = args.indexOf(`--${name}`);
  return i === -1 ? def : args.splice(i, 2)[1];
};
const flag = name => {
  const i = args.indexOf(`--${name}`);
  return i !== -1 && args.splice(i, 1).length > 0;
};
const widths = opt("widths", "1440,390").split(",").map(Number);
const themes = opt("themes", "light,dark").split(",");
const selector = opt("selector", "");
const out = opt("out", "/tmp/byagent-shots");
const height = Number(opt("height", "900"));
const wait = Number(opt("wait", "600"));
const full = flag("full");
const url = args[0] || "http://localhost:8792/";
mkdirSync(out, { recursive: true });

const browser = await chromium.launch();
const errors = [];
for (const theme of themes) {
  for (const width of widths) {
    const ctx = await browser.newContext({
      viewport: { width, height }, deviceScaleFactor: 2, reducedMotion: "reduce",
    });
    await ctx.addInitScript(t => localStorage.setItem("byagent-theme", t), theme);
    const page = await ctx.newPage();
    page.on("pageerror", e => errors.push(`${theme} ${width}: ${e.message}`));
    await page.goto(url, { waitUntil: "networkidle" });
    const path = `${out}/${theme}-${width}.png`;
    if (selector) {
      const el = page.locator(selector).first();
      await el.scrollIntoViewIfNeeded();
      await page.waitForTimeout(wait);
      await el.screenshot({ path });
    } else {
      await page.waitForTimeout(wait);
      await page.screenshot({ path, fullPage: full });
    }
    console.log(path);
    await ctx.close();
  }
}
await browser.close();
if (errors.length) {
  console.error("page errors:\n" + errors.join("\n"));
  process.exit(1);
}
