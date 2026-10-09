import { Marked } from "marked";

// Markdown → a self-contained HTML page. `byagent publish notes.md` renders
// the file here and publishes the result as index.html, so the server never
// sees Markdown and the page needs nothing from the CLI at view time.

const HLJS_VERSION = "11.12.0";
const HLJS_BASE = `https://cdnjs.cloudflare.com/ajax/libs/highlight.js/${HLJS_VERSION}`;

const FONTS_URL =
  "https://fonts.googleapis.com/css2?family=IBM+Plex+Sans:ital,wght@0,400;0,500;0,600;0,700;1,400&family=IBM+Plex+Mono:wght@400;500&display=swap";

// The page's own words, by language. Detection only ever yields zh-CN, zh-TW,
// ja, ko or en; the rest are reachable with an explicit `lang`.
const CHROME = {
  en: { contents: "Contents", anchor: "Link to this section" },
  "zh-CN": { contents: "目录", anchor: "链接到此部分" },
  "zh-TW": { contents: "目錄", anchor: "連結到此段落" },
  ja: { contents: "目次", anchor: "このセクションへのリンク" },
  ko: { contents: "목차", anchor: "이 섹션 링크" },
  es: { contents: "Contenido", anchor: "Enlace a esta sección" },
  "pt-BR": { contents: "Sumário", anchor: "Link para esta seção" },
  fr: { contents: "Sommaire", anchor: "Lien vers cette section" },
  de: { contents: "Inhalt", anchor: "Link zu diesem Abschnitt" },
};

// IBM Plex has no CJK glyphs, so Chinese, Japanese and Korean text falls
// through to these system faces (macOS, Windows, then Linux/Android). The
// stack follows the page language so shared Han characters get the right
// regional forms.
const CJK_FONTS = {
  "zh-CN": '"PingFang SC","Hiragino Sans GB","Microsoft YaHei","Noto Sans CJK SC","Source Han Sans SC"',
  "zh-TW": '"PingFang TC","Microsoft JhengHei","Noto Sans CJK TC","Source Han Sans TC"',
  ja: '"Hiragino Sans","Hiragino Kaku Gothic ProN","Yu Gothic","Meiryo","Noto Sans CJK JP","Source Han Sans JP"',
  ko: '"Apple SD Gothic Neo","Malgun Gothic","Noto Sans CJK KR","Source Han Sans KR"',
};

// Characters written one way in Simplified Chinese and another in
// Traditional, common enough to tell a document's script apart.
const SIMPLIFIED = "这个们来说时会为对国发过还没经与种开关让点问题实现应该学图数据设计认识务业产动线页码档进运处";
const TRADITIONAL = "這個們來說時會為對國發過還沒經與種開關讓點問題實現應該學圖數據設計認識務業產動線頁碼檔進運處";

/**
 * The language of a Markdown document, from its prose (code is ignored):
 * "zh-CN" or "zh-TW" when Chinese characters outnumber Latin words, "ja" when
 * kana is a real share of that text, "ko" when Hangul leads, else "en".
 */
export function detectLang(source) {
  const text = String(source)
    .replace(/^ {0,3}(`{3,}|~{3,})[^\n]*\n[\s\S]*?(?:^ {0,3}\1[^\n]*$|(?![\s\S]))/gm, " ")
    .replace(/`+[^`\n]*`+/g, " ")
    .replace(/<[^>]*>/g, " ")
    .replace(/\]\([^)]*\)|https?:\/\/\S+/g, " ");
  const count = (re) => (text.match(re) || []).length;
  const han = count(/\p{Script=Han}/gu);
  const kana = count(/[\p{Script=Hiragana}\p{Script=Katakana}]/gu);
  const hangul = count(/\p{Script=Hangul}/gu);
  const latinWords = count(/\p{Script=Latin}+/gu);
  const cjk = han + kana + hangul;
  // One CJK character carries about as much as one English word.
  if (cjk < 2 || cjk <= latinWords) return "en";
  if (hangul >= han + kana) return "ko";
  if (kana >= 3 && kana >= (han + kana) * 0.1) return "ja";
  let simp = 0;
  let trad = 0;
  for (const ch of text) {
    if (SIMPLIFIED.includes(ch)) simp++;
    else if (TRADITIONAL.includes(ch)) trad++;
  }
  return trad >= 3 && trad > simp * 2 ? "zh-TW" : "zh-CN";
}

/** The CHROME/CJK_FONTS key for a language tag: zh-Hant and zh-HK are zh-TW, any pt is pt-BR. */
function localeOf(tag) {
  const parts = String(tag || "").toLowerCase().replace(/_/g, "-").split("-");
  const base = parts[0];
  if (base === "zh") {
    if (parts.includes("hans")) return "zh-CN";
    return parts.some((p) => p === "hant" || p === "tw" || p === "hk" || p === "mo") ? "zh-TW" : "zh-CN";
  }
  if (base === "pt") return "pt-BR";
  return CHROME[base] ? base : "en";
}

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

// renderMarkdown returns { html, title, headings, lang }. The title is the
// explicit one, else the first H1, else the fallback. Heading ids are
// deduplicated so a document with two "Notes" sections links to both. `lang`
// (a BCP 47 tag) overrides the language detected from the text; it sets
// <html lang>, the contents label and the CJK font stack.
export function renderMarkdown(source, { title, fallbackTitle = "Untitled", lang } = {}) {
  const pageLang = (lang && String(lang).trim()) || detectLang(source);
  const locale = localeOf(pageLang);
  const chrome = CHROME[locale];
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
      return `<h${depth} id="${id}">${inner}<a class="anchor" href="#${id}" aria-label="${escapeHtml(chrome.anchor)}">#</a></h${depth}>\n`;
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
  const html = page({ title: pageTitle, body, toc: toc.length >= TOC_MIN_HEADINGS ? toc : [], needCode, lang: pageLang, locale });
  return { html, title: pageTitle, headings, lang: pageLang };
}

function tocHtml(toc, label) {
  if (!toc.length) return "";
  const items = toc.map((h) => `<li class="d${h.depth}"><a href="#${h.id}">${escapeHtml(h.text)}</a></li>`).join("\n");
  return `<nav class="toc" aria-label="${escapeHtml(label)}"><p class="toc-title">${escapeHtml(label)}</p><ol>\n${items}\n</ol></nav>`;
}

function page({ title, body, toc, needCode, lang, locale }) {
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
<html lang="${escapeHtml(lang)}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>${escapeHtml(title)}</title>
<meta name="color-scheme" content="light dark">
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link rel="stylesheet" href="${FONTS_URL}" media="print" onload="this.media='all'">
<noscript><link rel="stylesheet" href="${FONTS_URL}"></noscript>
${hljs}
<style>:root{--cjk:${CJK_FONTS[locale] || CJK_FONTS["zh-CN"]}}${CSS}</style>
</head>
<body>
<div class="page${toc.length ? " with-toc" : ""}">
${tocHtml(toc, CHROME[locale].contents)}
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
body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.65 "IBM Plex Sans",system-ui,-apple-system,"Segoe UI",var(--cjk),sans-serif;
  -webkit-font-smoothing:antialiased;text-rendering:optimizeLegibility}
.page{max-width:calc(var(--w) + 48px);margin:0 auto;padding:48px 24px 96px}
/* Symmetric gutters keep the text centred whether or not the contents list is shown. */
.page.with-toc{max-width:none;display:grid;grid-template-columns:1fr minmax(0,var(--w)) 1fr;column-gap:24px}
.page.with-toc .doc{grid-column:2}
.page.with-toc .toc{grid-column:1;justify-self:end;width:220px}
@media (min-width:1100px){.page.with-toc{grid-template-columns:minmax(244px,1fr) minmax(0,var(--w)) minmax(244px,1fr);column-gap:32px}}
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
code,pre,kbd{font-family:"IBM Plex Mono",ui-monospace,SFMono-Regular,Menlo,var(--cjk),monospace}
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
/* CJK: stricter line breaking, Korean breaks between words, no negative tracking on Han, kana or Hangul. */
:lang(zh),:lang(ja){line-break:strict;text-autospace:normal}
:lang(ko){word-break:keep-all}
:lang(zh) :is(h1,h2,h3,h4),:lang(ja) :is(h1,h2,h3,h4),:lang(ko) :is(h1,h2,h3,h4){letter-spacing:0}
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
