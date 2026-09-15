import { createHash } from "node:crypto";
import { readFileSync, readdirSync, statSync, writeFileSync } from "node:fs";
import { join, relative, sep } from "node:path";
import { CliError } from "./errors.js";

export const LINK_FILE = ".artifacts.json";

// Never uploaded: version control, installed dependencies, and the link file
// itself (it names the artifact, so shipping it would leak the id into the page).
const SKIP = new Set([".git", "node_modules", LINK_FILE]);

function sha256(buf) {
  return createHash("sha256").update(buf).digest("hex");
}

function walk(dir, base, out) {
  for (const entry of readdirSync(dir, { withFileTypes: true }).sort((a, b) => (a.name < b.name ? -1 : 1))) {
    if (SKIP.has(entry.name)) continue;
    const abs = join(dir, entry.name);
    if (entry.isDirectory()) {
      walk(abs, base, out);
      continue;
    }
    if (!entry.isFile()) continue;
    const body = readFileSync(abs);
    out.push({
      path: relative(base, abs).split(sep).join("/"),
      abs,
      body,
      sha256: sha256(body),
      size: body.length,
    });
  }
  return out;
}

// collectFiles turns a target into the manifest file list. A single .html file
// is published as index.html so `artifacts publish page.html` just works.
export function collectFiles(target) {
  let st;
  try {
    st = statSync(target);
  } catch {
    throw new CliError(`no such file or directory: ${target}`, { exit: 2, code: "USAGE" });
  }
  if (st.isDirectory()) {
    const files = walk(target, target, []);
    if (files.length === 0) throw new CliError(`${target} has no files to publish`, { exit: 2, code: "USAGE" });
    return { files, dir: target };
  }
  const body = readFileSync(target);
  return {
    dir: null,
    files: [{ path: "index.html", abs: target, body, sha256: sha256(body), size: body.length }],
  };
}

export function readLink(dir) {
  if (!dir) return {};
  try {
    const parsed = JSON.parse(readFileSync(join(dir, LINK_FILE), "utf8"));
    return parsed && typeof parsed === "object" ? parsed : {};
  } catch {
    return {};
  }
}

export function writeLink(dir, { id, api }) {
  if (!dir) return;
  writeFileSync(join(dir, LINK_FILE), JSON.stringify({ id, api }, null, 2) + "\n");
}
