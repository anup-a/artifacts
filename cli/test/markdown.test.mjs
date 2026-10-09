import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync, mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { renderMarkdown, slug, detectLang } from "../src/markdown.js";
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

const ZH = `# 季度计划

这是一份用 React 和 TypeScript 构建的项目说明，我们来看它的设计与实现。

## 背景

\`\`\`js
// English-only code does not count towards the language
const message = "hello world, this is a long English sentence in code";
\`\`\`

## 目标

提升性能。

## 风险

时间不够。
`;

test("a Chinese document gets lang zh-CN, Chinese chrome and the CJK font stack", () => {
  const { html, lang, headings } = renderMarkdown(ZH);
  assert.equal(lang, "zh-CN");
  assert.match(html, /<html lang="zh-CN">/);
  assert.match(html, /<nav class="toc" aria-label="目录"><p class="toc-title">目录<\/p>/);
  assert.match(html, /aria-label="链接到此部分">#<\/a>/);
  assert.match(html, /:root\{--cjk:"PingFang SC","Hiragino Sans GB","Microsoft YaHei","Noto Sans CJK SC"/);
  assert.match(html, /font:16px\/1\.65 "IBM Plex Sans",[^;]*var\(--cjk\),sans-serif/);
  // Headings keep their Chinese characters in their ids.
  assert.deepEqual(headings.map((h) => h.id), ["季度计划", "背景", "目标", "风险"]);
  assert.match(html, /<h2 id="背景">背景<a class="anchor" href="#背景"/);
});

test("an English document is unchanged: lang en, English chrome", () => {
  const { html, lang } = renderMarkdown(DOC);
  assert.equal(lang, "en");
  assert.match(html, /<html lang="en">/);
  assert.match(html, /<nav class="toc" aria-label="Contents"><p class="toc-title">Contents<\/p>/);
  assert.match(html, /aria-label="Link to this section">#<\/a>/);
});

test("detectLang reads the prose, not the code", () => {
  assert.equal(detectLang("# 專案計畫\n\n這是一個使用 React 的專案，我們來說明它的設計與實現。"), "zh-TW");
  assert.equal(detectLang("# プロジェクト計画\n\nこれは React で作ったプロジェクトです。"), "ja");
  assert.equal(detectLang("# 프로젝트 계획\n\n이것은 React로 만든 프로젝트입니다."), "ko");
  assert.equal(detectLang("# Notes\n\n```\n这是代码里的中文注释这是代码里的中文注释\n```\n\nEnglish text only, `中文中文中文`."), "en");
  assert.equal(detectLang("An English note that mentions 中文 once among many other words."), "en");
});

test("an explicit lang wins over detection and picks that language's chrome and fonts", () => {
  const ja = renderMarkdown(ZH, { lang: "ja" }).html;
  assert.match(ja, /<html lang="ja">/);
  assert.match(ja, /aria-label="目次"/);
  assert.match(ja, /--cjk:"Hiragino Sans"/);
  const de = renderMarkdown(DOC, { lang: "de" }).html;
  assert.match(de, /<html lang="de">/);
  assert.match(de, /<p class="toc-title">Inhalt<\/p>/);
  assert.match(de, /aria-label="Link zu diesem Abschnitt"/);
  const tw = renderMarkdown(ZH, { lang: "zh-Hant" }).html;
  assert.match(tw, /<html lang="zh-Hant">/);
  assert.match(tw, /<p class="toc-title">目錄<\/p>/);
  assert.match(tw, /--cjk:"PingFang TC"/);
});

test("the Google Fonts stylesheet never blocks rendering", () => {
  const { html } = renderMarkdown(ZH);
  const links = html.match(/<link [^>]*fonts\.googleapis\.com\/css2[^>]*>/g);
  assert.equal(links.length, 2);
  assert.match(links[0], /media="print" onload="this\.media='all'"/);
  assert.match(html, /<noscript><link rel="stylesheet" href="https:\/\/fonts\.googleapis\.com\/css2[^"]*"><\/noscript>/);
  // No render-blocking font stylesheet outside <noscript>.
  const outside = html.replace(/<noscript>[\s\S]*?<\/noscript>/g, "");
  for (const link of outside.match(/<link [^>]*fonts\.googleapis\.com\/css2[^>]*>/g)) assert.match(link, /media="print"/);
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
