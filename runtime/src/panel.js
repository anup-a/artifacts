// The right-hand thread panel: a Linear-style inspector. Everything lives in
// the shadow root, so the host page's CSS cannot reach in and ours cannot leak.
import { setActive } from "./highlight.js";
import { THEME, el } from "./theme.js";
import { relativeTime, avatarHue, initial } from "./format.js";

const WIDTH = 360;

const STYLE = `
.launcher{position:fixed;right:16px;bottom:16px;z-index:2147483000;display:flex;gap:6px;
align-items:center;padding:6px 8px 6px 12px;background:var(--card);border:1px solid var(--line);
border-radius:999px;box-shadow:var(--shadow);font-size:12px;font-weight:500}
.launcher:hover{background:var(--bg)}
.launcher[data-open="1"]{right:${WIDTH + 16}px;border-radius:8px;transition:right .18s ease}
.launcher .count{font-size:11px;font-weight:600;background:var(--fg);color:var(--bg);
border-radius:999px;padding:0 6px;line-height:16px}
.panel{position:fixed;top:0;right:0;height:100%;width:${WIDTH}px;z-index:2147483000;
background:var(--card);border-left:1px solid var(--line);display:flex;flex-direction:column;
transform:translateX(100%);transition:transform .18s ease}
.panel[data-open="1"]{transform:none}
header{display:flex;align-items:center;gap:8px;padding:10px 12px;border-bottom:1px solid var(--line)}
header h2{margin:0;font-size:13px;font-weight:600;flex:1;white-space:nowrap}
header .cnt{color:var(--muted);font-weight:500;margin-left:4px}
select.ver{appearance:none;-webkit-appearance:none;border:1px solid var(--line);background:var(--card);
color:var(--fg);border-radius:6px;padding:5px 22px 5px 10px;font-size:12px;font-weight:500;cursor:pointer;
background-image:url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='8' height='5'%3E%3Cpath d='M0 0l4 5 4-5z' fill='%23888'/%3E%3C/svg%3E");
background-repeat:no-repeat;background-position:right 8px center}
select.ver:hover{background-color:var(--bg)}
.note{padding:8px 12px;font-size:12px;color:var(--warn);background:var(--warn-soft);
border-bottom:1px solid var(--line)}
.filters{display:flex;gap:6px;padding:8px 12px;border-bottom:1px solid var(--line);align-items:center}
.chip{font-size:12px;border:1px solid var(--line);background:transparent;border-radius:999px;
padding:2px 9px;color:var(--muted);line-height:1.4}
.chip:hover{color:var(--fg)}
.chip[data-on="1"]{border-color:var(--fg);color:var(--fg);font-weight:500}
.list{flex:1;overflow:auto}
.empty{padding:28px 14px;color:var(--muted);text-align:center}
.thread{padding:10px 12px;border-bottom:1px solid var(--line);cursor:pointer;border-left:2px solid transparent}
.thread:hover{background:var(--bg)}
.thread[data-active="1"]{background:var(--accent-soft);border-left-color:var(--accent)}
.row{display:flex;align-items:center;gap:8px;min-width:0}
.row .grow{flex:1;min-width:0;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.msg{margin:4px 0 0;overflow-wrap:anywhere;white-space:pre-wrap}
.more{font-size:12px;color:var(--muted);margin-top:4px}
.replies{margin-top:8px;padding-left:10px;border-left:2px solid var(--line)}
.replies .r{margin-top:6px}
.replies .r .msg{margin-top:2px}
.badge{font-size:10px;font-weight:600;border-radius:4px;padding:1px 5px;background:var(--bg);
border:1px solid var(--line);color:var(--muted);white-space:nowrap}
.badge[data-s="drifted"]{color:var(--warn);border-color:var(--warn-line);background:var(--warn-soft)}
.reply{display:flex;gap:6px;margin-top:8px}
.reply .field{flex:1;min-width:0;padding:5px 8px;font-size:12px}
.err{font-size:12px;color:var(--err);margin-top:6px}
.section{font-size:11px;text-transform:uppercase;letter-spacing:.05em;color:var(--muted);
padding:10px 12px 4px;font-weight:600}
.foot{padding:8px 12px;border-top:1px solid var(--line);display:flex;gap:6px;align-items:center;
font-size:12px;color:var(--muted)}
.foot .grow{flex:1;color:var(--muted)}
`;

const BADGE = { attached: "attached", drifted: "moved", orphaned: "earlier version" };

export function mountPanel(shadow, ctx) {
  const { state, client, store, config } = ctx;

  shadow.appendChild(el("style", { text: THEME + STYLE }));

  const launcher = el("button", { class: "launcher", type: "button", onclick: () => toggle() });
  const cnt = el("span", { class: "cnt" });
  const title = el("h2", {}, [document.createTextNode("Comments"), cnt]);
  const versionSel = el("select", {
    class: "ver",
    "aria-label": "Version",
    onchange: (e) => {
      const v = e.target.value;
      const m = location.pathname.match(/^\/a\/([^/]+)\//);
      if (!m) return;
      location.href =
        String(v) === String(state.currentVersion) ? `/a/${m[1]}/` : `/a/${m[1]}/v/${v}/`;
    },
  });
  const closeBtn = el("button", {
    class: "btn icon close",
    type: "button",
    title: "Close",
    "aria-label": "Close",
    text: "✕",
    onclick: () => close(),
  });
  const note = el("div", { class: "note" });
  const filters = el("div", { class: "filters" });
  const list = el("div", { class: "list" });
  const pickBtn = el("button", {
    class: "btn icon pick",
    type: "button",
    title: "Point at element",
    "aria-label": "Point at element",
    "data-on": "0",
    text: "⌖",
    onclick: () => setPickMode(!api.pickMode),
  });
  const foot = el("div", { class: "foot" }, [
    el("span", { class: "grow", text: "Select text to comment" }),
    pickBtn,
    el("span", { class: "kbd", text: "esc" }),
  ]);
  const panel = el("div", { class: "panel", "data-open": "0" }, [
    el("header", {}, [title, versionSel, closeBtn]),
    note,
    filters,
    list,
    foot,
  ]);

  shadow.appendChild(launcher);
  shadow.appendChild(panel);

  let filter = "open"; // open | resolved | all
  let activeId = null;

  // On wide viewports the page slides left to make room, as the mockup does;
  // on narrow ones the panel overlays instead of squeezing the content.
  let savedMargin = null;
  function reserveSpace(on) {
    const root = document.documentElement;
    if (on && window.innerWidth >= 900) {
      if (savedMargin === null) savedMargin = root.style.marginRight;
      root.style.marginRight = `${WIDTH}px`;
    } else if (savedMargin !== null) {
      root.style.marginRight = savedMargin;
      savedMargin = null;
    }
  }
  function open() {
    panel.setAttribute("data-open", "1");
    launcher.setAttribute("data-open", "1");
    reserveSpace(true);
  }
  function close() {
    panel.setAttribute("data-open", "0");
    launcher.setAttribute("data-open", "0");
    reserveSpace(false);
  }
  function toggle() {
    if (panel.getAttribute("data-open") === "1") close();
    else open();
  }

  function setPickMode(on) {
    api.pickMode = on;
    pickBtn.setAttribute("data-on", on ? "1" : "0");
    pickBtn.title = on ? "Stop pointing" : "Point at element";
    document.body.style.cursor = on ? "crosshair" : "";
  }

  function visibleThreads() {
    if (filter === "all") return state.threads;
    return state.threads.filter((t) => (t.status || "open") === filter);
  }

  function setFilter(f) {
    if (f === filter) return;
    filter = f;
    activeId = null;
    setActive(null);
    if (ctx.onFilter) ctx.onFilter(visibleThreads());
    render();
  }

  function threadRow(t, resolved) {
    const first = t.comments[0] || {};
    const author = first.author_name || "Anonymous";
    const anchor = t.anchor || {};
    const quoted = anchor.kind === "element" ? anchor.text : anchor.quote;
    const active = t.id === activeId;
    const replies = t.comments.slice(1);

    const row = el("div", { class: "row" }, [
      el("span", { class: "av", "data-h": String(avatarHue(author)), text: initial(author) }),
      el("span", { class: "grow" }, [
        el("span", { class: "name", text: author }),
        el("span", { class: "time", text: `· ${relativeTime(first.created_at)}` }),
      ]),
      el("span", { class: "badge", "data-s": resolved.status, text: BADGE[resolved.status] || resolved.status }),
    ]);

    const body = [row];
    if (quoted) body.push(el("p", { class: "quote", text: quoted }));
    if (resolved.status === "orphaned" && !active) {
      body.push(el("p", { class: "more", text: "The quoted text is not on this version." }));
    } else {
      body.push(el("p", { class: "msg", text: first.body || "" }));
    }
    if (active) {
      if (replies.length) body.push(el("div", { class: "replies" }, replies.map(replyRow)));
      body.push(replyBox(t));
    } else if (replies.length) {
      body.push(el("p", { class: "more", text: replies.length === 1 ? "1 reply" : `${replies.length} replies` }));
    }

    return el(
      "div",
      { class: "thread", "data-thread": t.id, "data-active": active ? "1" : "0", onclick: () => focusThread(t.id) },
      body
    );
  }

  function replyRow(c) {
    const head = [el("span", { class: "name", text: c.author_name || "Anonymous" })];
    if (c.author_kind === "owner") head.push(el("span", { class: "owner", text: "owner" }));
    head.push(el("span", { class: "time", text: `· ${relativeTime(c.created_at)}` }));
    return el("div", { class: "r" }, [el("div", {}, head), el("p", { class: "msg", text: c.body })]);
  }

  function replyBox(t) {
    const who = store.get("name") || "Anonymous";
    const input = el("input", {
      class: "field",
      type: "text",
      placeholder: `Reply as ${who}…`,
      "aria-label": "Reply",
      onkeydown: (ev) => {
        if (ev.key === "Enter") {
          ev.preventDefault();
          send.click();
        }
      },
    });
    const err = el("p", { class: "err" });
    err.style.display = "none";
    const send = el("button", {
      class: "btn pri",
      type: "button",
      text: "Reply",
      onclick: async (ev) => {
        ev.stopPropagation();
        const text = input.value.trim();
        if (!text) return;
        send.disabled = true;
        const res = await client.reply(t.id, who, text);
        send.disabled = false;
        if (res.ok) {
          input.value = "";
          ctx.onRefresh();
        } else {
          err.textContent = `Could not post reply: ${res.error}`;
          err.style.display = "";
        }
      },
    });
    return el("div", {}, [el("div", { class: "reply" }, [input, send]), err]);
  }

  function focusThread(id) {
    open();
    if (id !== activeId) {
      activeId = id;
      setActive(id);
      renderList();
    }
    const r = state.resolved.get(id);
    const target = r && (r.element || (r.range && r.range.startContainer.parentElement));
    if (target && target.scrollIntoView) target.scrollIntoView({ block: "center", behavior: "smooth" });
    const card = list.querySelector(`.thread[data-thread="${CSS.escape(id)}"]`);
    if (card) {
      card.scrollIntoView({ block: "nearest" });
      const input = card.querySelector(".reply input");
      if (input && !card.contains(shadow.activeElement)) input.focus({ preventScroll: true });
    }
  }

  function renderVersions() {
    const vs = state.versions.length ? state.versions : [config.version];
    versionSel.replaceChildren();
    for (const v of vs) {
      versionSel.appendChild(el("option", { value: String(v), text: `v${v}` }));
    }
    versionSel.value = String(config.version);
    versionSel.style.display = vs.length > 1 ? "" : "none";
  }

  function renderFilters() {
    const resolvedN = state.threads.filter((t) => t.status === "resolved").length;
    const chips = [
      ["open", "Open"],
      ["resolved", resolvedN ? `Resolved ${resolvedN}` : "Resolved"],
      ["all", "All"],
    ];
    filters.replaceChildren(
      ...chips.map(([f, label]) =>
        el("button", { class: "chip", type: "button", "data-f": f, "data-on": f === filter ? "1" : "0", text: label, onclick: () => setFilter(f) })
      )
    );
  }

  function renderList() {
    const here = [];
    const earlier = [];
    for (const t of visibleThreads()) {
      const r = state.resolved.get(t.id) || { status: "orphaned" };
      (r.status === "orphaned" ? earlier : here).push([t, r]);
    }
    list.replaceChildren();
    if (here.length === 0 && earlier.length === 0) {
      const copy =
        filter === "open" ? "No open comments. Select some text to leave one." : "Nothing here yet.";
      list.appendChild(el("div", { class: "empty", text: copy }));
    }
    for (const [t, r] of here) list.appendChild(threadRow(t, r));
    if (earlier.length) {
      list.appendChild(el("div", { class: "section", text: "On an earlier version" }));
      for (const [t, r] of earlier) list.appendChild(threadRow(t, r));
    }
  }

  function render() {
    renderVersions();

    if (state.unavailable) {
      note.textContent = "Comments are unavailable right now.";
      note.style.display = "";
      cnt.textContent = "";
      filters.replaceChildren();
      list.replaceChildren(el("div", { class: "empty", text: "Comments unavailable." }));
      launcher.replaceChildren(document.createTextNode("Comments"));
      return;
    }

    const pinned = config.version !== state.currentVersion;
    note.style.display = pinned ? "" : "none";
    if (pinned) {
      note.textContent = `Viewing v${config.version}. The current version is v${state.currentVersion}.`;
    }

    const openN = state.threads.filter((t) => (t.status || "open") === "open").length;
    cnt.textContent = ` ${openN} open`;
    renderFilters();
    renderList();

    launcher.replaceChildren(document.createTextNode("Comments"));
    if (openN) launcher.appendChild(el("span", { class: "count", text: String(openN) }));
  }

  document.addEventListener("keydown", (ev) => {
    if (ev.key !== "Escape") return;
    if (api.isComposing && api.isComposing()) return; // compose.js owns that Escape
    close();
  });

  const api = { render, focusThread, open, close, setPickMode, visibleThreads, pickMode: false, isComposing: null };
  return api;
}
