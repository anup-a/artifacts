// The right-hand thread panel. Everything lives in the shadow root, so the host
// page's CSS cannot reach in and our styles cannot leak out.
import { setActive } from "./highlight.js";

const STYLE = `
:host{all:initial}
*{box-sizing:border-box;font-family:ui-sans-serif,system-ui,-apple-system,"Segoe UI",Roboto,sans-serif}
.launcher{position:fixed;right:16px;bottom:16px;z-index:2147483000;
border:0;border-radius:999px;padding:10px 16px;font-size:13px;font-weight:600;
background:#1f2328;color:#fff;cursor:pointer;box-shadow:0 2px 10px rgba(0,0,0,.28)}
.launcher:hover{background:#32383f}
.count{margin-left:6px;background:#ffd54f;color:#1f2328;border-radius:999px;
padding:1px 7px;font-size:11px}
.panel{position:fixed;top:0;right:0;height:100%;width:340px;z-index:2147483000;
background:#fff;color:#1f2328;border-left:1px solid #d8dee4;display:flex;
flex-direction:column;box-shadow:-2px 0 16px rgba(0,0,0,.12);
transform:translateX(100%);transition:transform .18s ease}
.panel[data-open="1"]{transform:translateX(0)}
header{display:flex;align-items:center;gap:8px;padding:12px 14px;
border-bottom:1px solid #e6eaef}
header h2{margin:0;font-size:13px;font-weight:600;flex:1}
select,button.ghost{font-size:12px;border:1px solid #d8dee4;border-radius:6px;
background:#fff;color:#1f2328;padding:4px 7px;cursor:pointer}
.list{flex:1;overflow:auto;padding:8px}
.empty{padding:24px 14px;color:#656d76;font-size:13px;text-align:center}
.thread{border:1px solid #e6eaef;border-radius:8px;padding:10px;margin-bottom:8px;
cursor:pointer;background:#fff}
.thread:hover{border-color:#b9c1ca}
.thread[data-active="1"]{border-color:#f0a500;box-shadow:0 0 0 2px rgba(255,193,7,.25)}
.badge{display:inline-block;font-size:10px;padding:1px 6px;border-radius:999px;
background:#eef1f4;color:#57606a;margin-left:6px}
.badge[data-s="drifted"]{background:#ffe0b2;color:#8a4b00}
.badge[data-s="orphaned"]{background:#f1f3f5;color:#8b949e}
.quote{font-size:11px;color:#656d76;border-left:2px solid #d8dee4;
padding-left:7px;margin:0 0 7px;overflow-wrap:anywhere}
.c{margin:6px 0;font-size:13px;overflow-wrap:anywhere}
.c .who{font-weight:600;font-size:11px;color:#424a53}
.c .who[data-kind="owner"]{color:#0a6a3a}
.reply{display:flex;gap:6px;margin-top:8px}
.reply input{flex:1;min-width:0;font-size:12px;padding:5px 7px;
border:1px solid #d8dee4;border-radius:6px;background:#fff;color:#1f2328}
.reply button{font-size:12px;padding:5px 9px;border:0;border-radius:6px;
background:#1f2328;color:#fff;cursor:pointer}
.section{font-size:11px;text-transform:uppercase;letter-spacing:.04em;
color:#8b949e;margin:12px 4px 6px}
.note{padding:10px 14px;font-size:12px;color:#8a4b00;background:#fff8e1;
border-bottom:1px solid #ffe0b2}
`;

function el(tag, props = {}, children = []) {
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

function when(iso) {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : d.toLocaleString();
}

export function mountPanel(shadow, ctx) {
  const { state, client, store, config } = ctx;

  shadow.appendChild(el("style", { text: STYLE }));

  const launcher = el("button", { class: "launcher", type: "button", onclick: () => toggle() });
  const title = el("h2", { text: "Comments" });
  const versionSel = el("select", {
    "aria-label": "Version",
    onchange: (e) => {
      const v = e.target.value;
      const m = location.pathname.match(/^\/a\/([^/]+)\//);
      if (!m) return;
      location.href =
        String(v) === String(state.currentVersion) ? `/a/${m[1]}/` : `/a/${m[1]}/v/${v}/`;
    },
  });
  const pickBtn = el("button", {
    class: "ghost",
    type: "button",
    text: "Point at element",
    onclick: () => setPickMode(!api.pickMode),
  });
  const closeBtn = el("button", { class: "ghost", type: "button", text: "Close", onclick: () => close() });
  const note = el("div", { class: "note" });
  const list = el("div", { class: "list" });
  const panel = el("div", { class: "panel", "data-open": "0" }, [
    el("header", {}, [title, versionSel, pickBtn, closeBtn]),
    note,
    list,
  ]);

  shadow.appendChild(launcher);
  shadow.appendChild(panel);

  function open() {
    panel.setAttribute("data-open", "1");
  }
  function close() {
    panel.setAttribute("data-open", "0");
  }
  function toggle() {
    if (panel.getAttribute("data-open") === "1") close();
    else open();
  }

  function setPickMode(on) {
    api.pickMode = on;
    pickBtn.textContent = on ? "Cancel pointing" : "Point at element";
    document.body.style.cursor = on ? "crosshair" : "";
  }

  function threadCard(t) {
    const r = state.resolved.get(t.id) || { status: "orphaned" };
    const anchor = t.anchor || {};
    const quoted = anchor.kind === "element" ? anchor.text : anchor.quote;

    const head = el("div", {}, [
      el("span", { class: "who", text: (t.comments[0] && t.comments[0].author_name) || "Anonymous" }),
      el("span", { class: "badge", "data-s": r.status, text: r.status }),
    ]);

    const body = [head];
    if (quoted) body.push(el("p", { class: "quote", text: quoted }));
    for (const c of t.comments) {
      body.push(
        el("div", { class: "c" }, [
          el("div", { class: "who", "data-kind": c.author_kind, text: `${c.author_name} · ${when(c.created_at)}` }),
          el("div", { text: c.body }),
        ])
      );
    }

    const input = el("input", { type: "text", placeholder: "Reply…", "aria-label": "Reply" });
    const send = el("button", {
      type: "button",
      text: "Reply",
      onclick: async (ev) => {
        ev.stopPropagation();
        const text = input.value.trim();
        if (!text) return;
        const name = store.get("name") || "Anonymous";
        send.disabled = true;
        const res = await client.reply(t.id, name, text);
        send.disabled = false;
        if (res.ok) {
          input.value = "";
          ctx.onRefresh();
        } else {
          note.textContent = `Could not post reply: ${res.error}`;
          note.style.display = "";
        }
      },
    });
    body.push(el("div", { class: "reply" }, [input, send]));

    return el("div", { class: "thread", "data-thread": t.id, onclick: () => focusThread(t.id) }, body);
  }

  function focusThread(id) {
    open();
    setActive(id);
    for (const c of Array.from(list.querySelectorAll(".thread"))) {
      c.setAttribute("data-active", c.getAttribute("data-thread") === id ? "1" : "0");
    }
    const r = state.resolved.get(id);
    const target = r && (r.element || (r.range && r.range.startContainer.parentElement));
    if (target && target.scrollIntoView) target.scrollIntoView({ block: "center", behavior: "smooth" });
    const card = list.querySelector(`.thread[data-thread="${CSS.escape(id)}"]`);
    if (card) card.scrollIntoView({ block: "nearest" });
  }

  function renderVersions() {
    const vs = state.versions.length ? state.versions : [config.version];
    versionSel.replaceChildren();
    for (const v of vs) {
      versionSel.appendChild(
        el("option", { value: String(v), text: v === state.currentVersion ? `v${v} (current)` : `v${v}` })
      );
    }
    versionSel.value = String(config.version);
    versionSel.style.display = vs.length > 1 ? "" : "none";
  }

  function render() {
    renderVersions();

    if (state.unavailable) {
      note.textContent = "Comments are unavailable right now.";
      note.style.display = "";
      list.replaceChildren(el("div", { class: "empty", text: "Comments unavailable." }));
      launcher.replaceChildren(document.createTextNode("Comments"));
      return;
    }

    const pinned = config.version !== state.currentVersion;
    note.style.display = pinned ? "" : "none";
    if (pinned) {
      note.textContent = `Viewing v${config.version}. The current version is v${state.currentVersion}.`;
    }

    const here = [];
    const earlier = [];
    for (const t of state.threads) {
      const r = state.resolved.get(t.id) || { status: "orphaned" };
      (r.status === "orphaned" ? earlier : here).push(t);
    }

    list.replaceChildren();
    if (here.length === 0 && earlier.length === 0) {
      list.appendChild(el("div", { class: "empty", text: "No comments yet. Select some text to leave one." }));
    }
    for (const t of here) list.appendChild(threadCard(t));
    if (earlier.length) {
      list.appendChild(el("div", { class: "section", text: "On earlier version" }));
      for (const t of earlier) list.appendChild(threadCard(t));
    }

    launcher.replaceChildren(document.createTextNode("Comments"));
    if (state.threads.length) {
      launcher.appendChild(el("span", { class: "count", text: String(state.threads.length) }));
    }
  }

  const api = { render, focusThread, open, close, setPickMode, pickMode: false };
  return api;
}
