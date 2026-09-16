// Colour tokens and shared atoms for everything inside the shadow root.
//
// The runtime never inherits the host page's colours: the scheme follows the
// viewer's prefers-color-scheme, so the panel reads the same on a dark page
// and a light one. Neutral greys, one accent, 13px UI type, no gradients.
export const THEME = `
:host{all:initial;
--bg:#fafafa;--fg:#171717;--muted:#737373;--line:#e5e5e5;--card:#fff;--hover:#f5f5f5;
--accent:#0070f3;--accent-soft:rgba(0,112,243,.10);--ring:rgba(0,112,243,.35);
--hl:rgba(255,213,79,.38);--hl-line:#e0a800;--ok:#0a7b3e;--ok-soft:rgba(10,123,62,.10);
--warn:#8a4b00;--warn-line:#f0c48a;--warn-soft:rgba(240,196,138,.18);
--err:#c62828;--shadow:0 1px 2px rgba(0,0,0,.06),0 8px 24px rgba(0,0,0,.10);
--shadow-lg:0 2px 4px rgba(0,0,0,.06),0 16px 48px rgba(0,0,0,.16)}
@media (prefers-color-scheme:dark){:host{
--bg:#0a0a0a;--fg:#ededed;--muted:#a1a1a1;--line:#262626;--card:#111;--hover:#1a1a1a;
--accent:#3291ff;--accent-soft:rgba(50,145,255,.16);--ring:rgba(50,145,255,.45);
--hl:rgba(255,213,79,.24);--ok:#3ecf7a;--ok-soft:rgba(62,207,122,.14);
--warn:#f0c48a;--warn-line:#6b4a1c;--warn-soft:rgba(240,196,138,.10);--err:#ff6b6b;
--shadow:0 1px 2px rgba(0,0,0,.4),0 8px 24px rgba(0,0,0,.5);
--shadow-lg:0 2px 4px rgba(0,0,0,.5),0 16px 48px rgba(0,0,0,.6)}}
*{box-sizing:border-box;font:13px/1.45 Inter,ui-sans-serif,system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;
-webkit-font-smoothing:antialiased;color:var(--fg);margin:0}
[hidden]{display:none!important}
button{cursor:pointer;background:none;border:0;padding:0;font:inherit;color:inherit}
:focus-visible{outline:2px solid var(--ring);outline-offset:1px;border-radius:6px}
.ic{display:inline-flex;flex:none;line-height:0}
.btn{display:inline-flex;align-items:center;gap:6px;border:1px solid var(--line);background:var(--card);
color:var(--fg);border-radius:6px;padding:5px 10px;font-size:12px;font-weight:500;line-height:1.2;
transition:background .12s ease,border-color .12s ease}
.btn:hover{background:var(--hover)}
.btn.pri{background:var(--fg);color:var(--bg);border-color:var(--fg)}
.btn.pri:hover{opacity:.88}
.btn.icon{padding:5px;min-width:26px;justify-content:center}
.btn.ghost{border-color:transparent;background:transparent;color:var(--muted)}
.btn.ghost:hover{background:var(--hover);color:var(--fg)}
.btn[data-on="1"]{background:var(--accent-soft);color:var(--accent);border-color:var(--accent)}
.btn:disabled{opacity:.45;cursor:default}
.field{width:100%;border:1px solid var(--line);border-radius:8px;background:var(--card);
color:var(--fg);font-size:13px;padding:7px 10px;resize:none;line-height:1.45;display:block}
.field::placeholder{color:var(--muted)}
.field:focus{outline:0;border-color:var(--accent);box-shadow:0 0 0 3px var(--accent-soft)}
.av{width:24px;height:24px;border-radius:50%;display:inline-flex;align-items:center;
justify-content:center;font-size:11px;font-weight:600;color:#fff;flex:none;letter-spacing:.02em}
.av.sm{width:20px;height:20px;font-size:10px}
.av[data-h="0"]{background:#7c3aed}.av[data-h="1"]{background:#0ea5e9}.av[data-h="2"]{background:#f97316}
.av[data-h="3"]{background:#10b981}.av[data-h="4"]{background:#e11d48}.av[data-h="5"]{background:#6366f1}
.name{font-weight:600}
.time{color:var(--muted);font-size:12px}
.owner{font-size:10px;font-weight:600;color:var(--ok);background:var(--ok-soft);
border-radius:4px;padding:1px 5px;line-height:1.4}
.snip{display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden;
font-size:12px;line-height:1.5;color:var(--fg);margin:8px 0 0}
.snip span{background:var(--hl);box-shadow:0 1px 0 var(--hl-line);border-radius:2px;
-webkit-box-decoration-break:clone;box-decoration-break:clone;padding:0 1px}
.kbd{font:11px/1.4 ui-monospace,SFMono-Regular,Menlo,monospace;color:var(--muted);
border:1px solid var(--line);border-radius:4px;padding:0 4px;background:var(--bg)}
.err{font-size:12px;color:var(--err);margin-top:6px}
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

/** A textarea that grows with its content instead of scrolling. */
export function autogrow(ta) {
  const fit = () => {
    ta.style.height = "auto";
    ta.style.height = `${ta.scrollHeight}px`;
  };
  ta.addEventListener("input", fit);
  return fit;
}

/** Quoted anchor text rendered as a two-line highlighted snippet. */
export function snippet(text) {
  return el("p", { class: "snip" }, [el("span", { text })]);
}
