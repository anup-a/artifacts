// Colour tokens and shared atoms for everything inside the shadow root.
//
// The runtime never inherits the host page's colours: the scheme follows the
// viewer's prefers-color-scheme, so the panel reads the same on a dark page
// and a light one. Neutral greys, one accent, 13px UI type, no gradients.
export const THEME = `
:host{all:initial;
--bg:#fafafa;--fg:#171717;--muted:#666;--line:#e5e5e5;--card:#fff;
--accent:#0070f3;--accent-soft:rgba(0,112,243,.10);--hl-line:#e0a800;--ok:#0a7b3e;
--ok-soft:rgba(10,123,62,.10);--warn:#8a4b00;--warn-line:#f0c48a;--warn-soft:rgba(240,196,138,.18);
--err:#c62828;--shadow:0 1px 2px rgba(0,0,0,.06),0 8px 24px rgba(0,0,0,.10)}
@media (prefers-color-scheme:dark){:host{
--bg:#0a0a0a;--fg:#ededed;--muted:#a1a1a1;--line:#262626;--card:#111;
--accent:#3291ff;--accent-soft:rgba(50,145,255,.16);--ok:#3ecf7a;--ok-soft:rgba(62,207,122,.14);
--warn:#f0c48a;--warn-line:#6b4a1c;--warn-soft:rgba(240,196,138,.10);--err:#ff6b6b;
--shadow:0 1px 2px rgba(0,0,0,.4),0 8px 24px rgba(0,0,0,.5)}}
*{box-sizing:border-box;font:13px/1.45 Inter,ui-sans-serif,system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;
-webkit-font-smoothing:antialiased;color:var(--fg)}
button{cursor:pointer}
.btn{border:1px solid var(--line);background:var(--card);color:var(--fg);border-radius:6px;
padding:5px 10px;font-size:12px;font-weight:500;line-height:1.2}
.btn:hover{background:var(--bg)}
.btn.pri{background:var(--fg);color:var(--bg);border-color:var(--fg)}
.btn.pri:hover{background:var(--fg);opacity:.9}
.btn.icon{padding:5px 7px;min-width:26px;text-align:center}
.btn.icon[data-on="1"]{background:var(--accent-soft);color:var(--accent);border-color:var(--accent)}
.btn:disabled{opacity:.5;cursor:default}
.field{width:100%;border:1px solid var(--line);border-radius:6px;background:var(--bg);
color:var(--fg);font-size:13px;padding:7px 9px;resize:none}
.field::placeholder{color:var(--muted)}
.field:focus{outline:2px solid var(--accent-soft);border-color:var(--accent)}
.av{width:22px;height:22px;border-radius:50%;display:inline-flex;align-items:center;
justify-content:center;font-size:10px;font-weight:600;color:#fff;flex:none}
.av[data-h="0"]{background:#7c3aed}.av[data-h="1"]{background:#0ea5e9}.av[data-h="2"]{background:#f97316}
.av[data-h="3"]{background:#10b981}.av[data-h="4"]{background:#e11d48}.av[data-h="5"]{background:#6366f1}
.name{font-weight:600}
.time{color:var(--muted);font-size:12px;margin-left:4px}
.owner{font-size:10px;font-weight:600;color:var(--ok);background:var(--ok-soft);
border-radius:4px;padding:1px 5px;margin-left:6px;vertical-align:1px}
.quote{font-size:12px;color:var(--muted);border-left:2px solid var(--hl-line);
padding:0 0 0 8px;margin:6px 0 0;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.kbd{font:11px ui-monospace,SFMono-Regular,Menlo,monospace;color:var(--muted);
border:1px solid var(--line);border-radius:4px;padding:0 4px}
`;

export function el(tag, props = {}, children = []) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k === "text") n.textContent = v; // always text, never markup
    else if (k === "class") n.className = v;
    else if (k.startsWith("on")) n.addEventListener(k.slice(2), v);
    else n.setAttribute(k, v);
  }
  for (const c of children) n.appendChild(c);
  return n;
}
