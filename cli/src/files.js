import { createHash } from "node:crypto";
import { readFileSync, readdirSync, statSync, writeFileSync } from "node:fs";
import { basename, extname, join, relative, sep } from "node:path";
import { CliError } from "./errors.js";
import { renderMarkdown } from "./markdown.js";

export const LINK_FILE = ".artifacts.json";

// Never uploaded: version control, installed dependencies, the link file
// itself (it names the artifact, so shipping it would leak the id into the
// page) and the project config, which is about the folder, not the page.
const SKIP = new Set([".git", "node_modules", LINK_FILE, ".byagent.json"]);

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

const MARKDOWN_EXTS = new Set([".md", ".markdown"]);
// A directory without index.html is served from the first of these, rendered.
const MARKDOWN_INDEX = ["index.md", "README.md", "readme.md"];

function isMarkdown(path) {
  return MARKDOWN_EXTS.has(extname(path).toLowerCase());
}

const ENTITIES = { amp: "&", lt: "<", gt: ">", quot: '"', apos: "'", nbsp: " " };
// The server refuses a title over this many bytes.
const MAX_TITLE_BYTES = 200;

// htmlTitle reads an HTML page's <title>, so a page published without --title
// is named the way the browser tab names it rather than "Untitled".
export function htmlTitle(body) {
  const m = /<title[^>]*>([\s\S]*?)<\/title>/i.exec(body.toString("utf8"));
  if (!m) return undefined;
  let t = m[1]
    .replace(/&(#x[0-9a-f]+|#\d+|[a-z]+);/gi, (all, e) => {
      if (e[0] !== "#") return ENTITIES[e.toLowerCase()] ?? all;
      const n = e[1] === "x" || e[1] === "X" ? parseInt(e.slice(2), 16) : parseInt(e.slice(1), 10);
      return n > 0 && n <= 0x10ffff ? String.fromCodePoint(n) : all;
    })
    .replace(/\s+/g, " ")
    .trim();
  while (Buffer.byteLength(t) > MAX_TITLE_BYTES) t = [...t].slice(0, -1).join("").trimEnd();
  return t || undefined;
}

function renderedIndex(abs, body, title, lang) {
  const { html, title: pageTitle } = renderMarkdown(body.toString("utf8"), { title, lang, fallbackTitle: basename(abs, extname(abs)) });
  const buf = Buffer.from(html, "utf8");
  return { file: { path: "index.html", abs, body: buf, sha256: sha256(buf), size: buf.length }, title: pageTitle };
}

// collectFiles turns a target into the manifest file list. A single .html file
// is published as index.html so `byagent publish page.html` just works. A
// single .md file is rendered to a page first; a directory with no index.html
// but an index.md or README.md is served from that, rendered, with the other
// files kept as assets so relative images and links still resolve. `title`
// is the --title flag; the result's title is what the page ended up with: an
// HTML page without the flag takes its <title>.
// `source` names a path to ship a single .md file's own text under, next to
// the rendered page, so it can be read back as Markdown. `lang` is the --lang
// flag: the rendered page's language when detection from its text is wrong.
export function collectFiles(target, { title, source, lang } = {}) {
  let st;
  try {
    st = statSync(target);
  } catch {
    throw new CliError(`no such file or directory: ${target}`, { exit: 2, code: "USAGE" });
  }
  if (st.isDirectory()) {
    const files = walk(target, target, []);
    if (files.length === 0) throw new CliError(`${target} has no files to publish`, { exit: 2, code: "USAGE" });
    const index = files.find((f) => f.path === "index.html");
    if (index) return { files, dir: target, title: title || htmlTitle(index.body) };
    const md = MARKDOWN_INDEX.map((name) => files.find((f) => f.path === name)).find(Boolean);
    if (!md) return { files, dir: target, title };
    const rendered = renderedIndex(md.abs, md.body, title, lang);
    return { files: [rendered.file, ...files], dir: target, title: rendered.title };
  }
  const body = readFileSync(target);
  if (isMarkdown(target)) {
    const rendered = renderedIndex(target, body, title, lang);
    const files = [rendered.file];
    if (source) files.push({ path: source, abs: target, body, sha256: sha256(body), size: body.length });
    return { dir: null, files, title: rendered.title };
  }
  return {
    dir: null,
    title: title || htmlTitle(body),
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

// writeLink remembers the artifact and, when known, its project and tags so
// a republish keeps them without repeating the flags. Absent labels are left
// out rather than written as null.
export function writeLink(dir, { id, api, project, tags }) {
  if (!dir) return;
  const link = { id, api };
  if (project) link.project = project;
  if (Array.isArray(tags) && tags.length) link.tags = tags;
  writeFileSync(join(dir, LINK_FILE), JSON.stringify(link, null, 2) + "\n");
}
