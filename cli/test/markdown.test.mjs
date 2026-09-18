import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync, mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { renderMarkdown, slug } from "../src/markdown.js";
import { collectFiles } from "../src/files.js";

const DOC = `# Jev integration audit

Intro paragraph with **bold**, \`code\` and a [link](https://example.com).

## Search

| Lane | top-1 |
| --- | --- |
| hybrid | 0.53 |

## Tagging

- [x] done
- [ ] todo

### Notes

\`\`\`js
const x = 1;
\`\`\`

## Notes
`;

test("title comes from the first H1 unless given", () => {
  assert.equal(renderMarkdown(DOC).title, "Jev integration audit");
  assert.equal(renderMarkdown(DOC, { title: "Override" }).title, "Override");
  assert.equal(renderMarkdown("just text", { fallbackTitle: "notes" }).title, "notes");
});

test("headings get deduplicated anchors and a table of contents", () => {
  const { html, headings } = renderMarkdown(DOC);
  assert.deepEqual(
    headings.map((h) => h.id),
    ["jev-integration-audit", "search", "tagging", "notes", "notes-1"],
  );
  assert.match(html, /<h2 id="search">Search<a class="anchor" href="#search"/);
  assert.match(html, /<nav class="toc"/);
  assert.match(html, /<li class="d3"><a href="#notes">Notes<\/a>/);
  assert.match(html, /<li class="d2"><a href="#notes-1">Notes<\/a>/);
});

test("short documents get no table of contents", () => {
  const { html } = renderMarkdown("# One\n\ntext\n\n## Two\n\nmore");
  assert.doesNotMatch(html, /<nav class="toc"/);
});

test("gfm tables, task lists, code fences and external links render", () => {
  const { html } = renderMarkdown(DOC);
  assert.match(html, /<div class="table-wrap"><table>/);
  assert.match(html, /<input checked="" disabled="" type="checkbox">/);
  assert.match(html, /<pre><code class="language-js">const x = 1;\n<\/code><\/pre>/);
  assert.match(html, /highlight\.js\/11\.12\.0\/highlight\.min\.js/);
  assert.match(html, /<a href="https:\/\/example\.com" target="_blank" rel="noopener">link<\/a>/);
  assert.match(html, /<title>Jev integration audit<\/title>/);
  assert.match(html, /prefers-color-scheme: dark/);
});

test("code is escaped and highlight.js is skipped without fences", () => {
  const { html } = renderMarkdown("# T\n\n```\n<script>alert(1)</script>\n```\n");
  assert.match(html, /&lt;script&gt;alert\(1\)&lt;\/script&gt;/);
  const plain = renderMarkdown("# T\n\nno code here").html;
  assert.doesNotMatch(plain, /highlight\.min\.js/);
});

test("slug is stable and url safe", () => {
  assert.equal(slug("Search: D1 candidates → Jev rerank"), "search-d1-candidates-jev-rerank");
  assert.equal(slug("!!!"), "section");
});

test("a single .md file publishes as a rendered index.html", () => {
  const dir = mkdtempSync(join(tmpdir(), "byagent-md-"));
  const file = join(dir, "notes.md");
  writeFileSync(file, "# Hello notes\n\nbody\n");
  const out = collectFiles(file);
  assert.equal(out.dir, null);
  assert.equal(out.title, "Hello notes");
  assert.equal(out.files.length, 1);
  assert.equal(out.files[0].path, "index.html");
  assert.match(out.files[0].body.toString("utf8"), /<h1 id="hello-notes">Hello notes/);
});

test("a directory with README.md and no index.html is served from the rendered README", () => {
  const dir = mkdtempSync(join(tmpdir(), "byagent-mddir-"));
  mkdirSync(join(dir, "img"));
  writeFileSync(join(dir, "README.md"), "# Readme\n\n![shot](img/a.png)\n");
  writeFileSync(join(dir, "img", "a.png"), "png");
  const out = collectFiles(dir, { title: "Given" });
  assert.equal(out.title, "Given");
  assert.deepEqual(out.files.map((f) => f.path).sort(), ["README.md", "img/a.png", "index.html"]);
  assert.match(out.files.find((f) => f.path === "index.html").body.toString("utf8"), /<img src="img\/a.png" alt="shot">/);
});

test("a directory that already has index.html is left alone", () => {
  const dir = mkdtempSync(join(tmpdir(), "byagent-html-"));
  writeFileSync(join(dir, "index.html"), "<h1>hi</h1>");
  writeFileSync(join(dir, "README.md"), "# nope");
  const out = collectFiles(dir);
  assert.equal(out.files.find((f) => f.path === "index.html").body.toString("utf8"), "<h1>hi</h1>");
});
