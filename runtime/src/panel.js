// The right-hand thread panel: a Linear-style inspector. Everything lives in
// the shadow root, so the host page's CSS cannot reach in and ours cannot leak.
import { setActive, setShown } from "./highlight.js";
import { THEME, el, autogrow, snippet } from "./theme.js";
import { icon } from "./icons.js";
import { relativeTime, avatarHue, initial } from "./format.js";

const WIDTH = 380;

const STYLE = `
.launcher{position:fixed;right:20px;bottom:20px;z-index:2147483000;display:flex;gap:8px;
align-items:center;padding:8px 14px 8px 12px;background:var(--card);border:1px solid var(--line);
border-radius:999px;box-shadow:var(--shadow);font-size:13px;font-weight:500;
transition:transform .12s ease,box-shadow .12s ease}
.launcher:hover{transform:translateY(-1px);box-shadow:var(--shadow-lg)}
.launcher[data-open="1"]{display:none}
.launcher .count{font-size:11px;font-weight:600;background:var(--fg);color:var(--bg);
border-radius:999px;padding:0 6px;line-height:18px;min-width:18px;text-align:center}
.panel{position:fixed;top:0;right:0;height:100%;width:${WIDTH}px;max-width:100vw;z-index:2147483000;
background:var(--card);border-left:1px solid var(--line);display:flex;flex-direction:column;
transform:translateX(100%);transition:transform .2s cubic-bezier(.2,.8,.2,1);box-shadow:var(--shadow)}
.panel[data-open="1"]{transform:none}
header{display:flex;align-items:center;gap:8px;padding:12px 12px 12px 16px;border-bottom:1px solid var(--line)}
header h2{font-size:14px;font-weight:600;flex:1;display:flex;align-items:center;gap:8px;white-space:nowrap}
header .cnt{font-size:11px;font-weight:600;color:var(--muted);background:var(--bg);border:1px solid var(--line);
border-radius:999px;padding:0 7px;line-height:18px}
header .cnt:empty{display:none}
.verwrap{position:relative;display:inline-flex;align-items:center}
select.ver{appearance:none;-webkit-appearance:none;border:1px solid var(--line);background:var(--card);
color:var(--fg);border-radius:999px;padding:3px 24px 3px 10px;font-size:12px;font-weight:500;cursor:pointer;
line-height:1.4;font-family:inherit}
select.ver:hover{background:var(--hover)}
.verwrap .ic{position:absolute;right:7px;pointer-events:none;color:var(--muted)}
.note{padding:8px 16px;font-size:12px;color:var(--warn);background:var(--warn-soft);border-bottom:1px solid var(--line)}
.seg{display:flex;gap:2px;margin:12px 16px;padding:3px;background:var(--bg);border:1px solid var(--line);
border-radius:8px}
.chip{flex:1;display:inline-flex;align-items:center;justify-content:center;gap:5px;font-size:12px;
border-radius:6px;padding:4px 8px;color:var(--muted);font-weight:500;transition:background .12s ease}
.chip:hover{color:var(--fg)}
.chip[aria-selected="true"]{background:var(--card);color:var(--fg);box-shadow:0 1px 2px rgba(0,0,0,.08),0 0 0 1px var(--line)}
.chip .n{font-size:11px;color:var(--muted);font-variant-numeric:tabular-nums}
.chip[aria-selected="true"] .n{color:var(--fg)}
.list{flex:1;overflow:auto;padding:0 12px 12px}
.empty{padding:48px 24px;text-align:center;display:flex;flex-direction:column;align-items:center;gap:6px}
.empty>.ic{width:40px;height:40px;border-radius:12px;background:var(--bg);border:1px solid var(--line);
align-items:center;justify-content:center;color:var(--muted);margin-bottom:6px}
.empty h3{font-size:13px;font-weight:600}
.empty p{font-size:12px;color:var(--muted);max-width:220px;line-height:1.5}
.empty .btn{margin-top:10px}
.thread{padding:12px;margin-top:8px;border:1px solid var(--line);border-radius:10px;cursor:pointer;
background:var(--card);transition:border-color .12s ease,box-shadow .12s ease}
.thread[data-active="0"]:hover{background:var(--hover)}
.thread[data-active="1"]{border-color:var(--accent);box-shadow:0 0 0 3px var(--accent-soft)}
.head{display:flex;align-items:center;gap:8px;min-width:0}
.meta{flex:1;min-width:0;display:flex;align-items:baseline;gap:6px;white-space:nowrap;overflow:hidden}
.meta .name{overflow:hidden;text-overflow:ellipsis}
.msg{margin-top:8px;overflow-wrap:anywhere;white-space:pre-wrap;line-height:1.5}
.more{display:inline-flex;align-items:center;gap:5px;font-size:12px;color:var(--muted);margin-top:8px}
.thread:hover .more{color:var(--accent)}
.gone{font-size:12px;color:var(--muted);margin-top:8px;font-style:italic}
.badge{font-size:11px;font-weight:500;border-radius:999px;padding:1px 7px;background:var(--bg);
border:1px solid var(--line);color:var(--muted);white-space:nowrap;line-height:1.5;cursor:help}
.badge[data-s="drifted"]{color:var(--warn);border-color:var(--warn-line);background:var(--warn-soft)}
.badge[data-p="seen"]{color:var(--accent);border-color:var(--accent);background:var(--accent-soft)}
.badge[data-p="working"]{color:var(--ok);border-color:var(--ok);background:var(--ok-soft)}
.sug{margin-top:8px;font-size:13px;line-height:1.5;overflow-wrap:anywhere;white-space:pre-wrap}
.sug del{color:var(--muted)}
.sug ins{text-decoration:none;background:var(--ok-soft);color:var(--ok)}
.replies{margin:10px 0 0 11px;padding-left:12px;border-left:2px solid var(--line)}
.r{display:flex;gap:8px;padding:10px 0 0}
.r .body{flex:1;min-width:0}
.r .meta{margin-top:1px}
.r .msg{margin-top:2px}
.reply{display:flex;gap:8px;margin-top:12px;align-items:flex-end}
.reply .box{flex:1;min-width:0;position:relative}
.reply textarea{padding-right:34px;min-height:32px;max-height:160px}
.reply .send{position:absolute;right:4px;bottom:4px;width:24px;height:24px;border-radius:6px;
display:inline-flex;align-items:center;justify-content:center;color:var(--muted)}
.reply .send:not(:disabled):hover{background:var(--accent-soft);color:var(--accent)}
.section{font-size:11px;text-transform:uppercase;letter-spacing:.05em;color:var(--muted);
padding:18px 2px 2px;font-weight:600}
.foot{padding:10px 16px;border-top:1px solid var(--line);display:flex;gap:8px;align-items:center;
font-size:12px;color:var(--muted);background:var(--bg)}
.foot .pick{padding:3px 8px;font-size:12px}
`;

const BADGE = {
  drifted: ["Moved", "The quoted text changed, so this comment now points at the closest match."],
  orphaned: ["Earlier version", "The quoted text is not on this version of the page."],
};

// PROGRESS is what the page's author (usually an agent) has said it is doing
// about a thread, set with byagent mark. Declined threads are also resolved,
// and the reason is the author's reply.
const PROGRESS = {
  seen: ["Seen", "The author has read this comment."],
  working: ["Working on it", "The author is changing the page for this comment."],
  declined: ["Declined", "The author decided not to change this. Their reason is in the replies."],
};

export function mountPanel(shadow, ctx) {
  const { state, client, store, config } = ctx;

  shadow.appendChild(el("style", { text: THEME + STYLE }));

  const launcher = el("button", { class: "launcher", type: "button", "aria-label": "Open comments", onclick: () => toggle() });
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
  const verWrap = el("span", { class: "verwrap" }, [versionSel, icon("chevron", 12)]);
  const closeBtn = el("button", {
    class: "btn icon ghost close",
    type: "button",
    title: "Close",
    "aria-label": "Close comments",
    onclick: () => close(),
  });
  closeBtn.appendChild(icon("close"));
  const note = el("div", { class: "note" });
  const seg = el("div", { class: "seg", role: "tablist", "aria-label": "Filter comments" });
  const list = el("div", { class: "list" });
  const pickBtn = pickButton("pick");
  const foot = el("div", { class: "foot" }, [
    document.createTextNode("Select text or"),
    pickBtn,
  ]);
  const panel = el("div", { class: "panel", "data-open": "0", role: "complementary", "aria-label": "Comments" }, [
    el("header", {}, [title, verWrap, closeBtn]),
    note,
    seg,
    list,
    foot,
  ]);

  shadow.appendChild(launcher);
  shadow.appendChild(panel);

  let filter = "open"; // open | resolved | all
  let activeId = null;

  function pickButton(cls) {
    const b = el("button", {
      class: `btn ${cls}`,
      type: "button",
      "data-on": "0",
      "aria-pressed": "false",
      onclick: () => setPickMode(!api.pickMode),
    });
    b.appendChild(icon("target", 14));
    b.appendChild(el("span", { text: "Point at an element" }));
    return b;
  }

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
    setShown(true);
    reserveSpace(true);
  }
  function close() {
    panel.setAttribute("data-open", "0");
    launcher.setAttribute("data-open", "0");
    setShown(false);
    reserveSpace(false);
  }
  function toggle() {
    if (panel.getAttribute("data-open") === "1") close();
    else open();
  }

  function setPickMode(on) {
    api.pickMode = on;
    for (const b of shadow.querySelectorAll(".pick")) {
      b.setAttribute("data-on", on ? "1" : "0");
      b.setAttribute("aria-pressed", on ? "true" : "false");
      b.lastChild.textContent = on ? "Click an element to comment" : "Point at an element";
    }
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

  function avatar(name, small) {
    return el("span", { class: small ? "av sm" : "av", "data-h": String(avatarHue(name)), text: initial(name) });
  }

  function meta(c) {
    const author = c.author_name || "Anonymous";
    const parts = [el("span", { class: "name", text: author })];
    if (c.author_kind === "owner") parts.push(el("span", { class: "owner", text: "Author" }));
    parts.push(el("span", { class: "time", text: relativeTime(c.created_at) }));
    return el("span", { class: "meta" }, parts);
  }

  function threadRow(t, resolved) {
    const first = t.comments[0] || {};
    const author = first.author_name || "Anonymous";
    const anchor = t.anchor || {};
    const quoted = anchor.kind === "element" ? anchor.text : anchor.quote;
    const active = t.id === activeId;
    const replies = t.comments.slice(1);

    const head = el("div", { class: "head" }, [avatar(author), meta(first)]);
    const badge = BADGE[resolved.status];
    if (badge) {
      head.appendChild(el("span", { class: "badge", "data-s": resolved.status, title: badge[1], text: badge[0] }));
    }
    // "Seen" and "Working on it" describe an open thread. Once it is resolved
    // they are stale; "Declined" stays because it explains the outcome.
    const progress = t.status === "resolved" && t.progress !== "declined" ? null : PROGRESS[t.progress];
    if (progress) {
      head.appendChild(el("span", { class: "badge", "data-p": t.progress, title: progress[1], text: progress[0] }));
    }

    const body = [head];
    if (quoted) body.push(snippet(quoted));
    if (t.suggestion) {
      body.push(el("p", { class: "sug" }, [el("del", { text: anchor.quote || "" }), document.createTextNode(" → "), el("ins", { text: t.suggestion })]));
    }
    if (resolved.status === "orphaned" && !active) {
      body.push(el("p", { class: "gone", text: "The quoted text is not on this version." }));
    } else {
      body.push(el("p", { class: "msg", text: first.body || "" }));
    }
    if (active) {
      if (replies.length) body.push(el("div", { class: "replies" }, replies.map(replyRow)));
      body.push(replyBox(t));
    } else if (replies.length) {
      const n = replies.length;
      body.push(el("span", { class: "more" }, [icon("reply", 13), el("span", { text: n === 1 ? "1 reply" : `${n} replies` })]));
    }

    return el(
      "div",
      { class: "thread", "data-thread": t.id, "data-active": active ? "1" : "0", onclick: () => focusThread(t.id) },
      body
    );
  }

  function replyRow(c) {
    return el("div", { class: "r" }, [
      avatar(c.author_name || "Anonymous", true),
      el("div", { class: "body" }, [meta(c), el("p", { class: "msg", text: c.body })]),
    ]);
  }

  function replyBox(t) {
    const who = store.get("name") || "Anonymous";
    const input = el("textarea", {
      class: "field",
      rows: "1",
      placeholder: `Reply as ${who}…`,
      "aria-label": "Reply",
      onkeydown: (ev) => {
        if (ev.key === "Enter" && !ev.shiftKey) {
          ev.preventDefault();
          send.click();
        }
      },
      oninput: () => {
        send.disabled = !input.value.trim();
      },
    });
    autogrow(input);
    const err = el("p", { class: "err" });
    err.hidden = true;
    const send = el("button", {
      class: "send",
      type: "button",
      title: "Send reply",
      "aria-label": "Send reply",
      disabled: "",
      onclick: async (ev) => {
        ev.stopPropagation();
        const text = input.value.trim();
        if (!text) return;
        send.disabled = true;
        const res = await client.reply(t.id, who, text);
        if (res.ok) {
          input.value = "";
          ctx.onRefresh();
        } else {
          send.disabled = false;
          err.textContent = `Could not post reply: ${res.error}`;
          err.hidden = false;
        }
      },
    });
    send.appendChild(icon("send", 14));
    const box = el("div", { class: "box" }, [input, send]);
    return el("div", { onclick: (ev) => ev.stopPropagation() }, [
      el("div", { class: "reply" }, [avatar(who, true), box]),
      err,
    ]);
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
      const input = card.querySelector(".reply textarea");
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
    verWrap.style.display = vs.length > 1 ? "" : "none";
  }

  function renderFilters() {
    const count = (s) => state.threads.filter((t) => (t.status || "open") === s).length;
    const chips = [
      ["open", "Open", count("open")],
      ["resolved", "Resolved", count("resolved")],
      ["all", "All", state.threads.length],
    ];
    seg.replaceChildren(
      ...chips.map(([f, label, n]) =>
        el(
          "button",
          { class: "chip", type: "button", role: "tab", "data-f": f, "aria-selected": f === filter ? "true" : "false", onclick: () => setFilter(f) },
          [el("span", { text: label }), el("span", { class: "n", text: String(n) })]
        )
      )
    );
  }

  function emptyState() {
    const none = state.threads.length === 0;
    const box = el("div", { class: "empty" }, [
      icon("comment", 18),
      el("h3", { text: none ? "No comments yet" : filter === "open" ? "No open comments" : "No resolved comments" }),
      el("p", { text: none ? "Select any text on the page to start a thread." : "Anything new you comment on will show up here." }),
    ]);
    if (none) box.appendChild(pickButton("pick"));
    return box;
  }

  function renderList() {
    const here = [];
    const earlier = [];
    for (const t of visibleThreads()) {
      const r = state.resolved.get(t.id) || { status: "orphaned" };
      (r.status === "orphaned" ? earlier : here).push([t, r]);
    }
    list.replaceChildren();
    if (here.length === 0 && earlier.length === 0) list.appendChild(emptyState());
    for (const [t, r] of here) list.appendChild(threadRow(t, r));
    if (earlier.length) {
      list.appendChild(el("div", { class: "section", text: "On an earlier version" }));
      for (const [t, r] of earlier) list.appendChild(threadRow(t, r));
    }
  }

  function renderLauncher(openN) {
    launcher.replaceChildren(icon("comment", 15), el("span", { text: openN ? "Comments" : "Comment" }));
    if (openN) launcher.appendChild(el("span", { class: "count", text: String(openN) }));
  }

  function render() {
    renderVersions();

    if (state.unavailable) {
      note.textContent = "Comments are unavailable right now.";
      note.style.display = "";
      cnt.textContent = "";
      seg.replaceChildren();
      list.replaceChildren(el("div", { class: "empty" }, [el("p", { text: "Comments unavailable." })]));
      renderLauncher(0);
      return;
    }

    const pinned = config.version !== state.currentVersion;
    note.style.display = pinned ? "" : "none";
    if (pinned) {
      note.textContent = `Viewing v${config.version}. The current version is v${state.currentVersion}.`;
    }

    const openN = state.threads.filter((t) => (t.status || "open") === "open").length;
    cnt.textContent = openN ? String(openN) : "";
    renderFilters();
    renderList();
    renderLauncher(openN);
  }

  document.addEventListener("keydown", (ev) => {
    if (ev.key !== "Escape") return;
    if (api.isComposing && api.isComposing()) return; // compose.js owns that Escape
    close();
  });

  const api = { render, focusThread, open, close, setPickMode, visibleThreads, pickMode: false, isComposing: null };
  return api;
}
