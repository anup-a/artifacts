import { Marked } from "marked";

// Markdown → a self-contained HTML page. `byagent publish notes.md` renders
// the file here and publishes the result as index.html, so the server never
// sees Markdown and the page needs nothing from the CLI at view time.

const HLJS_VERSION = "11.12.0";
const HLJS_BASE = `https://cdnjs.cloudflare.com/ajax/libs/highlight.js/${HLJS_VERSION}`;

// Headings of these depths appear in the table of contents.
const TOC_DEPTHS = new Set([2, 3]);
// Fewer headings than this and the page is short enough to skim without one.
const TOC_MIN_HEADINGS = 3;

export function escapeHtml(s) {
  return String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

// slug turns heading text into a stable, URL-safe id (GitHub style).
export function slug(text) {
  return (
    String(text)
      .toLowerCase()
      .replace(/<[^>]+>/g, "")
      .replace(/&[a-z]+;/g, "")
      .replace(/[^\p{L}\p{N}\s-]/gu, "")
      .trim()
      .replace(/\s+/g, "-") || "section"
  );
}

// plainText flattens inline tokens for the <title> and the TOC.
function plainText(tokens) {
  if (!Array.isArray(tokens)) return "";
  return tokens
    .map((t) => {
      if (t.type === "text" || t.type === "codespan") return t.raw ? t.text ?? t.raw : t.text ?? "";
      if (t.type === "escape") return t.text;
      return plainText(t.tokens);
    })
    .join("");
}

function hasCode(tokens) {
  return tokens.some((t) => t.type === "code" || (t.tokens && hasCode(t.tokens)));
}

// renderMarkdown returns { html, title, headings }. The title is the explicit
// one, else the first H1, else the fallback. Heading ids are deduplicated so a
// document with two "Notes" sections links to both.
export function renderMarkdown(source, { title, fallbackTitle = "Untitled" } = {}) {
  const seen = new Map();
  const headings = [];
  let codeSeen = false;

  const renderer = {
    heading({ tokens, depth }) {
      const text = plainText(tokens);
      const base = slug(text);
      const n = seen.get(base) || 0;
      seen.set(base, n + 1);
      const id = n ? `${base}-${n}` : base;
      headings.push({ depth, id, text });
      const inner = this.parser.parseInline(tokens);
      return `<h${depth} id="${id}">${inner}<a class="anchor" href="#${id}" aria-label="Link to this section">#</a></h${depth}>\n`;
    },
    code({ text, lang }) {
      codeSeen = true;
      const language = (lang || "").trim().split(/\s+/)[0];
      const cls = language ? ` class="language-${escapeHtml(language)}"` : "";
      return `<pre><code${cls}>${escapeHtml(text)}\n</code></pre>\n`;
    },
    link({ href, title: t, tokens }) {
      const inner = this.parser.parseInline(tokens);
      const external = /^[a-z][a-z0-9+.-]*:/i.test(href) && !href.startsWith("#");
      const attrs = [`href="${escapeHtml(href)}"`];
      if (t) attrs.push(`title="${escapeHtml(t)}"`);
      if (external) attrs.push('target="_blank" rel="noopener"');
      return `<a ${attrs.join(" ")}>${inner}</a>`;
    },
  };

  const parser = new Marked({ gfm: true, breaks: false });
  parser.use({ renderer });
  const tokens = parser.lexer(source);
  const body = parser.parser(tokens).replace(/<table>/g, '<div class="table-wrap"><table>').replace(/<\/table>/g, "</table></div>");
  const h1 = headings.find((h) => h.depth === 1);
  const pageTitle = (title && title.trim()) || (h1 && h1.text.trim()) || fallbackTitle;
  const toc = headings.filter((h) => TOC_DEPTHS.has(h.depth));
  const needCode = codeSeen || hasCode(tokens);
  return { html: page({ title: pageTitle, body, toc: toc.length >= TOC_MIN_HEADINGS ? toc : [], needCode }), title: pageTitle, headings };
}

function tocHtml(toc) {
  if (!toc.length) return "";
  const items = toc.map((h) => `<li class="d${h.depth}"><a href="#${h.id}">${escapeHtml(h.text)}</a></li>`).join("\n");
  return `<nav class="toc" aria-label="Contents"><p class="toc-title">Contents</p><ol>\n${items}\n</ol></nav>`;
}

function page({ title, body, toc, needCode }) {
  const hljs = needCode
    ? `<link rel="stylesheet" href="${HLJS_BASE}/styles/github.min.css" media="(prefers-color-scheme: light)">
<link rel="stylesheet" href="${HLJS_BASE}/styles/github-dark.min.css" media="(prefers-color-scheme: dark)">
<script src="${HLJS_BASE}/highlight.min.js" defer></script>
<script>addEventListener("DOMContentLoaded",()=>{if(window.hljs)document.querySelectorAll("pre code").forEach(e=>hljs.highlightElement(e))});</script>`
    : "";
  const tocScript = toc.length
    ? `<script>
(()=>{const links=[...document.querySelectorAll(".toc a")];const byId=new Map(links.map(a=>[a.getAttribute("href").slice(1),a]));
const hs=[...byId.keys()].map(id=>document.getElementById(id)).filter(Boolean);let active=null;
const set=el=>{if(active)active.classList.remove("active");active=byId.get(el.id);if(active)active.classList.add("active")};
const io=new IntersectionObserver(es=>{for(const e of es)if(e.isIntersecting)set(e.target)},{rootMargin:"0px 0px -70% 0px"});
hs.forEach(h=>io.observe(h));})();
</script>`
    : "";
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${escapeHtml(title)}</title>
<meta name="color-scheme" content="light dark">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=IBM+Plex+Sans:ital,wght@0,400;0,500;0,600;0,700;1,400&family=IBM+Plex+Mono:wght@400;500&display=swap">
${hljs}
<style>${CSS}</style>
</head>
<body>
<div class="page${toc.length ? " with-toc" : ""}">
${tocHtml(toc)}
<main class="doc">
${body}
</main>
</div>
${tocScript}
</body>
</html>
`;
}

const CSS = `
:root{
  --bg:#ffffff;--fg:#1c1e21;--muted:#5d6470;--rule:#e6e8eb;--soft:#f5f6f8;
  --accent:#1f5eff;--accent-ink:#1749c7;--code-bg:#f5f6f8;--mark:#fff3a8;
  --quote:#8a94a6;--w:72ch;
}
@media (prefers-color-scheme: dark){:root{
  --bg:#0f1115;--fg:#e6e8ec;--muted:#9aa3b2;--rule:#262a33;--soft:#171a21;
  --accent:#7aa2ff;--accent-ink:#9db8ff;--code-bg:#151821;--mark:#5a4a00;--quote:#6b7686;
}}
*{box-sizing:border-box}
html{-webkit-text-size-adjust:100%}
body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.65 "IBM Plex Sans",system-ui,-apple-system,"Segoe UI",sans-serif;
  -webkit-font-smoothing:antialiased;text-rendering:optimizeLegibility}
.page{max-width:calc(var(--w) + 48px);margin:0 auto;padding:48px 24px 96px}
.page.with-toc{max-width:none;display:grid;grid-template-columns:minmax(0,var(--w));justify-content:center;gap:56px}
@media (min-width:1100px){.page.with-toc{grid-template-columns:220px minmax(0,var(--w));}}
.doc{min-width:0}
.doc>*:first-child{margin-top:0}
h1,h2,h3,h4,h5,h6{line-height:1.25;font-weight:600;letter-spacing:-.01em;position:relative;scroll-margin-top:24px}
h1{font-size:2.1rem;margin:0 0 .6em;letter-spacing:-.02em}
h2{font-size:1.45rem;margin:2.2em 0 .6em;padding-top:.6em;border-top:1px solid var(--rule)}
h3{font-size:1.15rem;margin:1.8em 0 .5em}
h4{font-size:1rem;margin:1.5em 0 .4em}
h5,h6{font-size:.92rem;margin:1.3em 0 .3em;color:var(--muted);text-transform:uppercase;letter-spacing:.04em}
.anchor{position:absolute;left:-1.1em;top:0;opacity:0;color:var(--muted);text-decoration:none;font-weight:400;transition:opacity .12s}
h1:hover .anchor,h2:hover .anchor,h3:hover .anchor,h4:hover .anchor{opacity:1}
@media (max-width:760px){.anchor{display:none}}
p{margin:0 0 1.1em}
a{color:var(--accent-ink);text-decoration:underline;text-decoration-color:color-mix(in srgb,var(--accent) 35%,transparent);text-underline-offset:3px}
a:hover{text-decoration-color:var(--accent)}
strong{font-weight:600}
ul,ol{padding-left:1.5em;margin:0 0 1.1em}
li{margin:.25em 0}
li>p{margin-bottom:.5em}
li:has(>input[type=checkbox]){list-style:none;margin-left:-1.5em;padding-left:1.5em;position:relative}
li>input[type=checkbox]{position:absolute;left:0;top:.42em;margin:0;accent-color:var(--accent)}
hr{border:0;border-top:1px solid var(--rule);margin:2.5em 0}
blockquote{margin:1.4em 0;padding:.2em 0 .2em 1.1em;border-left:3px solid var(--quote);color:var(--muted)}
blockquote>p:last-child{margin-bottom:0}
code,pre,kbd{font-family:"IBM Plex Mono",ui-monospace,SFMono-Regular,Menlo,monospace}
code{font-size:.88em;background:var(--code-bg);padding:.12em .4em;border-radius:5px}
pre{margin:1.3em 0;padding:1em 1.1em;background:var(--code-bg);border:1px solid var(--rule);border-radius:10px;overflow-x:auto;line-height:1.55;font-size:.88rem}
pre code{background:none;padding:0;border-radius:0;font-size:inherit}
.hljs{background:transparent!important;padding:0!important}
.table-wrap{overflow-x:auto;margin:1.3em 0;border:1px solid var(--rule);border-radius:10px}
table{border-collapse:collapse;width:100%;font-size:.94rem}
th,td{padding:.6em .9em;text-align:left;vertical-align:top;border-bottom:1px solid var(--rule)}
th{background:var(--soft);font-weight:600;white-space:nowrap}
tr:last-child td{border-bottom:0}
img,video{max-width:100%;height:auto;border-radius:8px}
mark{background:var(--mark);color:inherit;padding:0 .15em;border-radius:3px}
kbd{font-size:.85em;padding:.1em .45em;border:1px solid var(--rule);border-bottom-width:2px;border-radius:5px;background:var(--soft)}
del{color:var(--muted)}
sup{line-height:0}
.toc{display:none}
@media (min-width:1100px){
  .toc{display:block;position:sticky;top:48px;align-self:start;font-size:.86rem;line-height:1.5;max-height:calc(100vh - 96px);overflow:auto;padding-right:8px}
  .toc-title{margin:0 0 .6em;font-size:.72rem;font-weight:600;letter-spacing:.08em;text-transform:uppercase;color:var(--muted)}
  .toc ol{list-style:none;margin:0;padding:0;border-left:1px solid var(--rule)}
  .toc li{margin:0}
  .toc a{display:block;color:var(--muted);text-decoration:none;padding:.22em 0 .22em .9em;border-left:2px solid transparent;margin-left:-1px}
  .toc li.d3 a{padding-left:1.9em;font-size:.82rem}
  .toc a:hover{color:var(--fg)}
  .toc a.active{color:var(--accent-ink);border-left-color:var(--accent)}
}
@media print{
  body{font-size:11pt}.page{padding:0;max-width:none}.toc,.anchor{display:none!important}
  a{color:inherit;text-decoration:none}pre{border:1px solid #bbb;white-space:pre-wrap}
}
`;
