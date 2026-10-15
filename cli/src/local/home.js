// indexPage is what http://localhost:<port>/ shows: every local page, newest
// first, with its open comment count. It uses the dashboard's palette.

function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
}

function when(iso) {
  const t = Date.parse(iso);
  return Number.isNaN(t) ? "" : new Date(t).toLocaleString("en", { dateStyle: "medium", timeStyle: "short" });
}

export function indexPage(rows, dir) {
  const items = rows.length
    ? rows.map((a) => `<a class="row" href="/a/${esc(a.id)}/">
  <span class="title">${esc(a.title || "(untitled)")}</span>
  <span class="meta">${a.project ? `<span class="tag">${esc(a.project)}</span>` : ""}v${a.current_version} · ${a.open_comments} open · ${esc(when(a.updated_at))}</span>
  <span class="id">${esc(a.id)}</span>
</a>`).join("\n")
    : `<div class="empty"><p>No local pages yet.</p><pre>byagent publish page.html --local</pre></div>`;
  return `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>byagent local</title>
<style>
:root{color-scheme:light dark;--bg:light-dark(#f0f0f5,#08080a);--panel:light-dark(#fff,#111114);--fg:light-dark(#16161a,#f4f4f6);
--muted:light-dark(#71717f,#76767f);--line:light-dark(#e4e4ea,#222228);--accent:light-dark(#5b3df5,#a78bfa);
--mono:ui-monospace,SFMono-Regular,Menlo,monospace}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:15px/1.5 Geist,Inter,system-ui,sans-serif}
main{max-width:760px;margin:0 auto;padding:56px 20px}
.eyebrow{font:500 12px/1 var(--mono);letter-spacing:.06em;text-transform:uppercase;color:var(--accent)}
h1{font-size:32px;letter-spacing:-.03em;margin:10px 0 6px}
.lede{color:var(--muted);margin:0 0 28px}.lede code{font-family:var(--mono);font-size:13px}
.list{background:var(--panel);border:1px solid var(--line);border-radius:14px;overflow:hidden}
.row{display:grid;grid-template-columns:1fr auto;gap:2px 16px;padding:14px 18px;color:inherit;text-decoration:none;border-top:1px solid var(--line)}
.row:first-child{border-top:0}.row:hover{background:color-mix(in srgb,var(--accent) 6%,transparent)}
.title{font-weight:600}.meta{color:var(--muted);font-size:13px;grid-column:1}.id{grid-row:1/3;grid-column:2;align-self:center;font:12px var(--mono);color:var(--muted)}
.tag{display:inline-block;margin-right:8px;padding:0 7px;border-radius:99px;background:color-mix(in srgb,var(--accent) 14%,transparent);color:var(--accent);font-size:12px}
.empty{padding:28px 18px;text-align:center;color:var(--muted)}.empty pre{display:inline-block;font:13px var(--mono);color:var(--fg);background:var(--bg);padding:8px 12px;border-radius:8px}
</style></head><body><main>
<div class="eyebrow">Local mode</div>
<h1>Pages on this machine</h1>
<p class="lede">Published with <code>--local</code>. These pages are stored only on this computer, in <code>${esc(dir)}</code>.</p>
<div class="list">
${items}
</div>
</main></body></html>`;
}
