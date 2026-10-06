// The landing page, and only that page: the bookcase behind the hero, the
// dashboard window that lands flat as you scroll, the sections settling onto
// planks, the "brrr" scribble, and the feature demos. Everything works and
// reads without it; this file only moves things. The CSP allows no inline
// script or style attribute, so colours and positions here are set through
// classes or the CSSOM.
(() => {
  "use strict";
  const still = matchMedia("(prefers-reduced-motion: reduce)").matches;
  const hero = document.querySelector(".lp3 .hero");
  if (!hero) return;

  // scroll: settle sections in, and drop a few spines onto each plank
  document.documentElement.classList.add("js-in");
  document.querySelectorAll(".plank").forEach(p => {
    const W = p.classList.contains("wide") ? 880 : 520, H = 70, NS = "http://www.w3.org/2000/svg";
    const svg = document.createElementNS(NS, "svg"); svg.setAttribute("viewBox", `0 0 ${W} ${H}`);
    const line = document.createElementNS(NS, "path"); line.setAttribute("d", `M0 ${H - 1} H ${W}`); line.setAttribute("pathLength", 1); line.setAttribute("class", "pl"); svg.appendChild(line);
    const n = p.classList.contains("wide") ? 26 : 7;
    const ws = Array.from({ length: n }, () => 11 + Math.random() * 9);
    let x = (W - ws.reduce((a, b) => a + b + 1.5, -1.5)) / 2, k = 0;
    while (k < n) {
      const w = ws[k], h = 34 + Math.random() * 30, r = document.createElementNS(NS, "rect");
      r.setAttribute("x", x); r.setAttribute("y", H - 1 - h); r.setAttribute("width", w); r.setAttribute("height", h); r.setAttribute("rx", 1.5); r.setAttribute("class", "sp2");
      r.style.setProperty("--d", (0.35 + k * 0.07) + "s"); svg.appendChild(r); x += w + 1.5; k++;
    }
    p.appendChild(svg);
  });
  const settle = new IntersectionObserver(es => es.forEach(e => { if (e.isIntersecting) { e.target.classList.add("in"); settle.unobserve(e.target); } }), { threshold: .18 });
  document.querySelectorAll("[data-in]").forEach(el => still ? el.classList.add("in") : settle.observe(el));
  const reveal = new IntersectionObserver(es => es.forEach(e => { if (e.isIntersecting) { e.target.classList.add("in"); reveal.unobserve(e.target); } }), { threshold: .3 });
  document.querySelectorAll("[data-reveal]").forEach(el => still ? el.classList.add("in") : reveal.observe(el));

  // the brrr scribble: a phone-cord coil that winds tighter, then knots
  (() => {
    let s = 11; const rnd = () => (s = (s * 16807) % 2147483647) / 2147483647;
    const P = [], Y = 85;
    for (let x = 160; x <= 190; x += 10) P.push([x, Y + Math.sin(x / 7) * 2]);
    for (let t = 0; t <= Math.PI * 2 * 5; t += .3) {
      const b = 3 + t * .95;
      P.push([190 + 6.4 * t + b * Math.sin(t), Y - 3 + b * .85 * Math.cos(t)]);
    }
    const [cx, cy] = [412, 84];
    for (let i = 0; i < 24; i++) {
      const a = rnd() * Math.PI * 2, r = 20 + rnd() * 40;
      P.push([cx + Math.cos(a) * r * 1.2, cy + Math.sin(a) * r * .95]);
    }
    P.push([478, 80], [500, 74], [516, 70]);
    let d = `M${P[0][0]} ${P[0][1]}`;
    for (let i = 0; i < P.length - 1; i++) {
      const p0 = P[i - 1] || P[i], p1 = P[i], p2 = P[i + 1], p3 = P[i + 2] || p2;
      const c1 = [p1[0] + (p2[0] - p0[0]) / 6, p1[1] + (p2[1] - p0[1]) / 6];
      const c2 = [p2[0] - (p3[0] - p1[0]) / 6, p2[1] - (p3[1] - p1[1]) / 6];
      d += ` C${c1.map(n => n.toFixed(1))} ${c2.map(n => n.toFixed(1))} ${p2.map(n => n.toFixed(1))}`;
    }
    document.getElementById("w-scrib").setAttribute("d", d);
  })();

  // the living shelf: a bookcase draws itself, pages drop onto it, agents keep adding and reading
  const NS = "http://www.w3.org/2000/svg";
  const bg = document.getElementById("bg"), copy = document.getElementById("hero-copy");
  const AGENTS = [["claude code", "--a-claude"], ["codex", "--a-codex"], ["cursor", "--a-cursor"], ["hermes", "--a-hermes"], ["cron job", "--a-cron"]];
  const pick = a => a[Math.floor(Math.random() * a.length)];
  const rnd = (a, b) => a + Math.random() * (b - a);
  const el = (tag, attrs) => { const e = document.createElementNS(NS, tag); for (const k in attrs) e.setAttribute(k, attrs[k]); return e; };
  let bays = [], books = [], tick = null, lastW = 0;

  function tint() { return `color-mix(in srgb, var(--ink) ${Math.round(rnd(2, 11))}%, var(--panel))`; }
  function spine(g, x, y, w, h, c, delay, cls) {
    const r = el("rect", { x, y, width: w, height: h, rx: 1.5, class: "bk" + (cls ? " " + cls : "") });
    r.style.setProperty("--d", delay + "s"); r.style.fill = tint(); if (c) r.style.setProperty("--c", `var(${c})`);
    g.appendChild(r);
    const deco = el("g", { class: "bk-cap" }); deco.style.setProperty("--d", delay + "s");
    const band = yy => deco.appendChild(el("path", { d: `M${x + 2} ${yy} H ${x + w - 2}`, class: "band" }));
    band(y + h * .1); band(y + h * .1 + 3); band(y + h * .88);
    if (w > 15 && h > 60) { const lh = Math.min(h * .34, 38); deco.appendChild(el("rect", { x: x + w / 2 - 1.5, y: y + h * .3, width: 3, height: lh, rx: 1.5, class: "label" })); }
    g.appendChild(deco);
    return r;
  }
  function book(bay, w, h, delay, opts = {}) {
    const x = bay.cursor, y = bay.base - h;
    const wrap = el("g", { class: "bkw" }), g = el("g", {});
    if (opts.lean) g.setAttribute("transform", `rotate(${opts.lean} ${x + w} ${bay.base})`);
    const r = spine(g, x, y, w, h, opts.c, delay, opts.cls);
    wrap.appendChild(g); bg.appendChild(wrap); bay.cursor += w + (opts.lean ? 6 : 1);
    const b = { r, g: wrap, x, y, w, h, bay }; books.push(b); return b;
  }
  function stack(bay, delay) {
    const n = 2 + Math.floor(Math.random() * 3), x = bay.cursor;
    let y = bay.base, maxW = 0; const g = el("g", {});
    for (let k = 0; k < n; k++) {
      const w = rnd(46, 70), h = rnd(8, 12), off = rnd(-4, 4);
      y -= h; maxW = Math.max(maxW, w + Math.abs(off));
      const r = el("rect", { x: x + off + 2, y, width: w, height: h, rx: 1.5, class: "bk" });
      r.style.fill = tint(); r.style.setProperty("--d", (delay + k * .04) + "s"); g.appendChild(r);
    }
    bg.appendChild(g); bay.cursor += maxW + 6;
  }

  function build() {
    clearInterval(tick); bg.innerHTML = ""; bays = []; books = [];
    const W = hero.clientWidth, H = hero.clientHeight; lastW = W;
    bg.setAttribute("viewBox", `0 0 ${W} ${H}`);
    const m = W < 700 ? 14 : 28, x0 = m, x1 = W - m, y0 = 84, y1 = H - m;
    const rows = Math.max(3, Math.round((y1 - y0) / 150)), rowH = (y1 - y0) / rows;
    const nb = Math.max(2, Math.round((x1 - x0) / (W < 700 ? 150 : 230))), bayW = (x1 - x0) / nb;
    const c = copy.getBoundingClientRect(), hr = hero.getBoundingClientRect();
    const clear = { x0: c.left - hr.left - 40, x1: c.right - hr.left + 40, y0: c.top - hr.top - 30, y1: c.bottom - hr.top + 30 };
    const line = (d, cls, delay) => { const p = el("path", { d, class: "ol " + cls, pathLength: 1 }); p.style.setProperty("--d", delay + "s"); bg.appendChild(p); };
    line(`M${x0} ${y0} H ${x1} V ${y1} H ${x0} Z`, "frame", 0);
    for (let r = 1; r < rows; r++) { const y = y0 + r * rowH; line(`M${x0} ${y} H ${x1} M ${x0} ${y + 5} H ${x1}`, "board", .3 + r * .12); }
    for (let b = 1; b < nb; b++) { const x = x0 + b * bayW; line(`M${x} ${y0} V ${y1}`, "div", .55 + b * .06); }
    let d = 1.3;
    for (let r = 0; r < rows; r++) for (let b = 0; b < nb; b++) {
      const bx0 = x0 + b * bayW + 10, bx1 = x0 + (b + 1) * bayW - 10, base = y0 + (r + 1) * rowH - 1, top = y0 + r * rowH + 6;
      const blocked = !(bx1 < clear.x0 || bx0 > clear.x1 || base < clear.y0 || top > clear.y1);
      const bay = { x0: bx0, x1: bx1, base, inner: base - top - 8, cursor: bx0, blocked };
      bays.push(bay);
      const fill = bx0 + (bx1 - bx0) * rnd(.55, .85);
      const tall = bay.inner * rnd(.82, .95);
      while (bay.cursor < fill) {
        if (Math.random() < .07 && fill - bay.cursor > 80) { stack(bay, d); d += still ? 0 : .05; continue; }
        const w = rnd(12, 24), h = tall * rnd(.78, 1);
        const a = Math.random() < .55 ? pick(AGENTS) : null;
        book(bay, w, h, d, { c: a && a[1] });
        d += still ? 0 : .012;
      }
      if (Math.random() < .3 && bx1 - bay.cursor > 40) { book(bay, rnd(13, 20), tall * rnd(.8, .95), d, { c: pick(AGENTS)[1], lean: rnd(9, 16) }); }
    }
    if (!still) tick = setInterval(step, 1700);
  }

  // every tick an agent publishes one more page onto a free shelf, away from the copy
  function step() {
    const open = bays.filter(b => b.x1 - b.cursor > 30);
    if (!open.length) { makeRoom(); return; }
    const edge = open.filter(b => !b.blocked);
    const bay = pick(edge.length ? edge : open), a = pick(AGENTS);
    book(bay, rnd(13, 22), bay.inner * rnd(.74, .92), 0, { c: a[1] });
  }

  // a full shelf never resets: the last book on one shelf slides off to make room
  function makeRoom() {
    const bay = pick(bays), mine = books.filter(b => b.bay === bay);
    const last = mine[mine.length - 1];
    if (!last) return;
    books.splice(books.indexOf(last), 1);
    bay.cursor = last.x;
    last.g.style.transition = "opacity .5s ease"; last.g.style.opacity = "0";
    setTimeout(() => last.g.remove(), 600);
  }

  // scroll: the shelf sinks a little and fades as the page moves past it
  let ticking = false;
  addEventListener("scroll", () => {
    if (ticking || still) return; ticking = true;
    requestAnimationFrame(() => {
      bg.style.setProperty("--p", Math.min(1, scrollY / hero.clientHeight).toFixed(3));
      tiltShot();
      ticking = false;
    });
  }, { passive: true });

  // the screenshot starts tilted back and lands flat as it reaches the middle of the screen
  const shotGlow = document.querySelector(".shot-glow");
  function tiltShot() {
    if (!shotGlow || still) return;
    const r = shotGlow.getBoundingClientRect();
    const t = Math.max(0, Math.min(1, (r.top - innerHeight * .18) / (innerHeight * .7)));
    shotGlow.style.transform = `perspective(1800px) rotateX(${(t * 18).toFixed(2)}deg) scale(${(1 - t * .08).toFixed(3)}) translateY(${(t * 30).toFixed(1)}px)`;
    shotGlow.style.opacity = (1 - t * .35).toFixed(3);
    shotGlow.classList.toggle("popped", t < .55);
  }
  tiltShot();

  // rebuild only when the width changes, not on every height wobble (mobile address bars)
  let rt;
  addEventListener("resize", () => { clearTimeout(rt); rt = setTimeout(() => { if (hero.clientWidth !== lastW) build(); }, 250); });
  (document.fonts ? document.fonts.ready : Promise.resolve()).then(build);
})();

// ---- The feature demos ---------------------------------------------------
// Each feature has a state, a render() for its mock, direct interactions in
// the mock, and three requests the agent can run. A request lists the real
// CLI lines it stands for and what the agent says back, and changes the
// state so the mock shows the result.
(() => {
  "use strict";
  const how = document.getElementById("how");
  if (!how) return;
  const still = matchMedia("(prefers-reduced-motion: reduce)").matches;
  const HOST = how.dataset.host || "byagent.dev";
  let COVERS = {};
  try { COVERS = JSON.parse(how.dataset.covers || "{}"); } catch (e) { /* the cards show without covers */ }

  const h = (t, a = {}, ...kids) => {
    const e = document.createElement(t);
    for (const k in a) {
      if (k === "on") { e._on = a.on; for (const ev in a.on) e.addEventListener(ev, x => e._on && e._on[ev] && e._on[ev](x)); }
      else if (k === "cls") e.className = a.cls;
      else if (k === "html") e.innerHTML = a.html;
      else if (a[k] != null && a[k] !== false) e.setAttribute(k, a[k]);
    }
    kids.flat().forEach(c => c != null && c !== false && e.append(c));
    return e;
  };
  // put(): patch the container to match the new children instead of rebuilding it.
  // Only nodes that changed are touched, so images don't reload, inputs keep focus
  // and only genuinely new items animate in.
  const keyOf = n => n.nodeType === 1 ? n.getAttribute("data-key") : null;
  function morph(a, b) {
    if (a.nodeType !== b.nodeType || a.nodeName !== b.nodeName) { a.replaceWith(b); return b; }
    if (a.nodeType === 3) { if (a.nodeValue !== b.nodeValue) a.nodeValue = b.nodeValue; return a; }
    for (const { name, value } of b.attributes) if (a.getAttribute(name) !== value) a.setAttribute(name, value);
    for (const { name } of [...a.attributes]) if (!b.hasAttribute(name)) a.removeAttribute(name);
    if (b._on) { if (!a._on) for (const ev in b._on) a.addEventListener(ev, x => a._on && a._on[ev] && a._on[ev](x)); a._on = b._on; }
    if (a.nodeName === "INPUT" && document.activeElement !== a && a.value !== (b.getAttribute("value") || "")) a.value = b.getAttribute("value") || "";
    if (a.isContentEditable && document.activeElement === a) return a;
    patch(a, [...b.childNodes]);
    return a;
  }
  function patch(parent, kids) {
    const old = [...parent.childNodes], byKey = new Map(), loose = [];
    old.forEach(n => { const k = keyOf(n); k ? byKey.set(k, n) : loose.push(n); });
    const out = kids.map(n => {
      const k = keyOf(n);
      const m = k ? byKey.get(k) : loose.find(o => o.nodeName === n.nodeName && !o._used);
      if (!m) return n;
      m._used = true; if (k) byKey.delete(k);
      return morph(m, n);
    });
    old.forEach(n => { delete n._used; if (!out.includes(n) && n.parentNode === parent) n.remove(); });
    out.forEach((n, i) => { if (parent.childNodes[i] !== n) parent.insertBefore(n, parent.childNodes[i] || null); });
  }
  const put = (el, ...k) => patch(el, k.flat().filter(x => x != null && x !== false).map(x => typeof x === "string" ? document.createTextNode(x) : x));
  const lock = '<svg viewBox="0 0 24 24"><rect x="5" y="11" width="14" height="9" rx="2"/><path d="M8 11V8a4 4 0 0 1 8 0v3"/></svg>';
  const code6 = () => String(Math.floor(100000 + Math.random() * 900000));

  const F = {};

  // ---- 1. The shelf: a compact copy of the real byagent dashboard ----
  // Project dots are classes, not inline colours: the CSP allows no style attributes.
  const PROJ = { "Dashboards & data": "pj-data", "Design studio": "pj-design", "Explainers & guides": "pj-guides" };
  const ICON = {
    ext: '<svg viewBox="0 0 16 16"><path d="M9 3h4v4M13 3 7.5 8.5M11 9.5V13H3V5h3.5"/></svg>',
  };
  F.shelf = {
    label: "The shelf", agent: "claude code", title: "Every page, filed by project",
    blurb: "Each page your agents publish gets its own link and lands under a project. Agents list and search the shelf before they start something new.",
    init: () => ({ proj: "All", q: "", hit: null, pages: [
      ["lisbon", "Lisbon rides: the weekend edition", "Dashboards & data", "codex", "2m"], ["signups", "Signups, week 41", "Dashboards & data", "codex", "14m"],
      ["garden", "Small bottles, big garden", "Design studio", "opencode", "14m"], ["retention", "Q3 retention by cohort", "Dashboards & data", "claude code", "1h"],
      ["kyoto", "Five days in Kyoto", "Explainers & guides", "claude code", "2h"], ["noodles", "Charred corn & miso butter noodles", "Explainers & guides", "claude code", "3h"],
      ["coffee", "Coffee prices, 2015 to 2026", "Dashboards & data", "opencode", "5h"], ["nimbus", "Nimbus 2.0 release notes", "Explainers & guides", "codex", "1d"], ["orbits", "Orbits are just falling sideways", "Explainers & guides", "claude code", "2d"],
    ].map(([f, t, p, a, ago]) => ({ f, t, p, a, ago })) }),
    render(el, s, redraw) {
      const q = s.q.trim().toLowerCase();
      const rows = s.pages.filter(x => (s.proj === "All" || x.p === s.proj) && (!q || x.t.toLowerCase().includes(q)));
      const search = h("input", { cls: "fx-search", placeholder: "Search titles…", value: s.q, "aria-label": "Search titles",
        on: { input: e => { s.q = e.target.value; s.hit = null; redraw(true); } } });
      const col = p => h("button", { type: "button", "data-key": p, cls: "dd-col" + (s.proj === p ? " on" : ""), on: { click: () => { s.proj = p; s.hit = null; redraw(); } } },
        p === "All" ? h("i", { cls: "all" }) : h("i", { cls: PROJ[p] }), h("span", {}, p === "All" ? "All pages" : p),
        h("em", {}, String(p === "All" ? s.pages.length : s.pages.filter(x => x.p === p).length)));
      put(el, h("div", { cls: "dd" },
        h("aside", { cls: "dd-side" }, h("div", { cls: "dd-h" }, "Collections"), ["All", ...Object.keys(PROJ)].map(col)),
        h("div", { cls: "dd-main" },
          h("div", { cls: "dd-top" }, h("b", {}, s.proj === "All" ? "All pages" : s.proj), search),
          rows.length ? h("div", { cls: "dd-grid" }, rows.map(x => h("div", { "data-key": x.f, cls: "dd-card" + (s.hit === x.f ? " hit" : "") + (x.fresh ? " fresh" : "") },
              h("div", { cls: "dd-cov" }, h("img", { src: COVERS[x.f] || "", alt: "", width: "640", height: "400", loading: "lazy" })),
              h("div", { cls: "dd-foot" }, h("b", {}, x.t), h("div", { cls: "dd-meta" }, h("i", { cls: PROJ[x.p] }), h("span", { cls: "dd-tag" }, x.a), h("small", {}, x.ago), h("span", { cls: "dd-ext", html: ICON.ext }))))))
            : h("div", { cls: "fx-empty" }, "Nothing on the shelf matches that."))));
      return search;
    },
    asks: [
      { q: "What's in Dashboards & data?", cli: ['byagent list --project "Dashboards & data"'], run: s => { s.proj = "Dashboards & data"; s.q = ""; s.hit = null; const n = s.pages.filter(x => x.p === s.proj); return `${n.length} pages: ` + n.map(x => x.t.split(":")[0]).join(", ") + "."; } },
      { q: "Find the Lisbon rides page", cli: ['byagent list --search "lisbon"'], run: s => { s.proj = "All"; s.q = "lisbon"; s.hit = "lisbon"; return "Found it: Lisbon rides, by codex, in Dashboards & data. " + HOST + "/a/zMzQTdMFg9ng/"; } },
      { q: "File the moodboard under Design studio", cli: ['byagent publish ./moodboard --project "Design studio"'], run: s => {
          if (!s.pages.some(x => x.f === "moodboard")) s.pages.unshift({ f: "moodboard", t: "Studio moodboard, autumn", p: "Design studio", a: "claude code", ago: "now", fresh: true });
          s.proj = "Design studio"; s.q = ""; s.hit = "moodboard"; return "Published Studio moodboard and filed it under Design studio. " + HOST + "/a/Az5naoIGGjHk/"; } },
    ],
  };

  // ---- 2. Comments ----
  const LINES = ["Goal: every team on the new build by month end.", "Step 1: move every team to the new build in one go.", "Step 2: watch error rates for 48 hours before the next wave."];
  const FIXED = "Step 1: pilot with one team for a week, then roll out to the rest.";
  F.notes = {
    label: "Comments", agent: "codex", title: "Anyone comments on the line they mean",
    blurb: "Readers select a sentence and leave a note under a name they type. No account, no seat. Your agent reads the notes, fixes the page and resolves them.",
    init: () => ({ v: 1, fixed: false, draft: null, notes: [{ who: "Maya", s: 1, txt: "Can we pilot one team for a week first?", open: true, replies: [] }] }),
    render(el, s, redraw) {
      put(el, h("div", { cls: "fx-page" },
        h("div", { cls: "fx-url" }, h("span", {}, HOST + "/a/k4m2Qv9pXz1A/"), h("span", { cls: "fx-pill" + (s.v > 1 ? " ok" : "") }, "v" + s.v)),
        h("h4", {}, "Rollout plan"),
        LINES.map((t, i) => {
          const txt = i === 1 && s.fixed ? FIXED : t, open = s.notes.some(n => n.s === i && n.open);
          return h("p", { cls: "fx-line" + (open ? " noted" : "") + (i === 1 && s.fixed ? " fixed" : ""), title: "Click to leave a note", on: { click: () => { s.draft = i; redraw(); } } }, txt);
        }),
        s.draft != null ? h("div", { cls: "fx-compose" },
          h("input", { cls: "fx-in sm", value: "You", "aria-label": "Your name" }),
          h("input", { cls: "fx-in", value: "Add a rollback step here?", "aria-label": "Your note" }),
          h("button", { type: "button", cls: "fx-btn", on: { click: e => { const [n, t] = e.target.closest(".fx-compose").querySelectorAll("input"); s.notes.push({ who: n.value || "Someone", s: s.draft, txt: t.value, open: true, replies: [] }); s.draft = null; redraw(); } } }, "Post"),
          h("small", {}, "no account")) : h("small", { cls: "fx-hint" }, "Click any line to leave a note, no account.")),
        h("div", { cls: "fx-notes" }, s.notes.map((n, k) => h("div", { "data-key": "n" + k, cls: "fx-bub" + (n.open ? "" : " done") },
          h("div", {}, h("b", {}, n.who), h("em", {}, n.open ? "no account · open" : "resolved")), h("div", {}, n.txt),
          n.replies.map(r => h("div", { cls: "fx-reply" }, h("b", {}, r[0]), " ", r[1]))))));
    },
    asks: [
      { q: "Read the open notes and fix them", cli: ["byagent comments k4m2Qv9pXz1A --open --json", "byagent publish ./rollout-plan", "byagent resolve k4m2Qv9pXz1A th_…"], run: s => {
          const open = s.notes.filter(n => n.open); if (!open.length) return "No open notes left.";
          if (open.some(n => n.s === 1) && !s.fixed) { s.fixed = true; s.v++; }
          open.forEach(n => { n.open = false; n.replies.push(["codex", n.s === 1 ? `Done in v${s.v}: one team first.` : "Fixed, see the latest version."]); });
          return `Fixed ${open.length} note${open.length > 1 ? "s" : ""}, republished as v${s.v} on the same link, and resolved ${open.length > 1 ? "them" : "it"}.`; } },
      { q: "Reply to Maya and resolve it", cli: ['byagent reply k4m2Qv9pXz1A th_… "Good call, piloting first."', "byagent resolve k4m2Qv9pXz1A th_…"], run: s => {
          const m = s.notes.find(n => n.who === "Maya"); if (!m.open) return "Maya's note is already resolved.";
          m.replies.push(["codex", "Good call, piloting first."]); m.open = false; return "Replied to Maya and resolved her note."; } },
      { q: "What did people say since yesterday?", cli: ["byagent comments k4m2Qv9pXz1A --since 2026-10-04T00:00:00Z"], run: s => {
          const open = s.notes.filter(n => n.open);
          return open.length ? open.map(n => `${n.who} on "${LINES[n.s].slice(0, 24)}…": ${n.txt}`).join(" · ") : "Nothing new. Every note is resolved."; } },
    ],
  };

  // ---- 3. Private pages ----
  F.private = {
    label: "Private pages", agent: "claude code", title: "Public by link, or private with a code",
    blurb: "A private page is visible to your workspace and to anyone you give its six-digit share code. Rotate the code or withdraw it at any time.",
    init: () => ({ access: "public", code: null, typed: "" }),
    render(el, s, redraw) {
      const unlocked = s.access === "public" || (s.code && s.typed === s.code);
      const opt = (k, label, sub) => h("button", { type: "button", cls: "fx-opt" + (s.access === k ? " on" : ""), on: { click: () => { s.access = k; if (k === "public") s.code = null; s.typed = ""; redraw(); } } }, h("i"), label, sub ? h("em", {}, sub) : null);
      put(el, h("div", { cls: "fx-two" },
        h("div", { cls: "fx-card" }, h("div", { cls: "fx-url" }, h("span", {}, HOST + "/a/Hr8LmZ2xQa0c/")),
          unlocked ? h("div", { cls: "fx-open" }, h("b", {}, "Q3 report"), h("span", { cls: "fx-bar" }), h("span", { cls: "fx-bar s" }), h("span", { cls: "fx-bar" }),
              h("small", {}, s.access === "public" ? "Anyone with the link can read this." : "Unlocked with the share code."))
            : h("div", { cls: "fx-lock" }, h("div", { cls: "fx-lock-ic", html: lock }),
              s.code ? h("input", { cls: "fx-code", inputmode: "numeric", maxlength: "6", placeholder: "••••••", value: s.typed, "aria-label": "Share code",
                on: { input: e => { s.typed = e.target.value.replace(/\D/g, ""); if (s.typed.length === 6) redraw(); } } }) : null,
              h("small", {}, s.code ? (s.typed.length === 6 && s.typed !== s.code ? "That code doesn't match." : "Enter the six-digit code") : "Private. Only your workspace can open it."))),
        h("div", { cls: "fx-card" }, h("h6", {}, "Access"), opt("public", "Anyone with the link"), opt("private", "Private", "share code"),
          s.access === "private" ? h("div", { cls: "fx-coderow" },
            h("span", { cls: "fx-chip" + (s.code ? "" : " off") }, s.code || "no code"),
            h("button", { type: "button", cls: "fx-link", on: { click: () => { s.code = code6(); s.typed = ""; redraw(); } } }, s.code ? "Rotate" : "Create code"),
            s.code ? h("button", { type: "button", cls: "fx-link", on: { click: () => { s.code = null; s.typed = ""; redraw(); } } }, "Withdraw") : null) : null)));
    },
    asks: [
      { q: "Make the Q3 report private", cli: ["byagent publish ./q3-report --id Hr8LmZ2xQa0c --private"], run: s => { s.access = "private"; s.code = null; s.typed = ""; return "Q3 report is private. Only your workspace can open it."; } },
      { q: "Give me a share code for it", cli: ["byagent share Hr8LmZ2xQa0c"], run: s => { s.access = "private"; s.code = code6(); s.typed = ""; return `Share code ${s.code}. Anyone with the link and this code can open it.`; } },
      { q: "Withdraw the code", cli: ["byagent share Hr8LmZ2xQa0c --off"], run: s => { if (!s.code) return "There's no code to withdraw."; s.code = null; s.typed = ""; return "Withdrawn. The old code stops working right away."; } },
    ],
  };

  // ---- 4. Versions ----
  F.versions = {
    label: "Versions", agent: "codex", title: "Same link, every version",
    blurb: "Publish again and the link stays put. Each version records the agent that published it, and any recent version can be rolled back to.",
    init: () => ({ view: 3, live: 3, vs: [[1, "claude code", "Mon 10:02", "move every team to the new build in one go."], [2, "codex", "Tue 09:15", "pilot with one team for a week."], [3, "claude code", "Wed 16:40", "pilot with two teams for a week."]] }),
    render(el, s, redraw) {
      const cur = s.live, [v, a, t, step] = s.vs.find(x => x[0] === s.view);
      const rollback = () => { s.live = s.view; redraw(); };
      put(el, h("div", { cls: "fx-two v" },
        h("div", { cls: "fx-vers" }, s.vs.map(([n, ag, when]) => h("button", { type: "button", "data-key": "v" + n, cls: "fx-v" + (s.view === n ? " on" : ""), on: { click: () => { s.view = n; redraw(); } } },
            h("i", {}, "v" + n), h("span", {}, ag), h("span", { cls: "t" }, when), n === cur ? h("em", {}, "live") : null)),
          s.view !== cur ? h("button", { type: "button", cls: "fx-btn ghost", on: { click: rollback } }, `Roll back to v${s.view}`) : h("small", { cls: "fx-hint" }, "Click a version to preview it")),
        h("div", { cls: "fx-page sm" }, h("div", { cls: "fx-url" }, h("span", {}, HOST + "/a/k4m2Qv9pXz1A/"), h("span", { cls: "fx-pill" + (v === cur ? " ok" : "") }, "v" + v)),
          h("h4", {}, "Rollout plan"), h("p", {}, "Step 1: " + step), h("span", { cls: "fx-bar light" }), h("span", { cls: "fx-bar light s" }), h("small", { cls: "fx-by" }, `Published by ${a}, ${t}`))));
    },
    asks: [
      { q: "Show me the versions of the rollout plan", cli: ["byagent versions k4m2Qv9pXz1A"], run: s => { s.view = s.live; return s.vs.map(([n, a, t]) => `v${n} ${a}, ${t}` + (n === s.live ? " (live)" : "")).join(" · "); } },
      { q: "Roll back to v2", cli: ["byagent rollback k4m2Qv9pXz1A 2"], run: s => { s.live = 2; s.view = 2; return "v2 is live again on the same link. Nothing was re-uploaded, and v3 is still there."; } },
      { q: "Who published the last version?", cli: ["byagent versions k4m2Qv9pXz1A --json"], run: s => { const [n, a, t] = s.vs[s.vs.length - 1]; s.view = n; return `v${n} was published by ${a}, ${t}.`; } },
    ],
  };

  // ---- 5. Expiry ----
  const ORDER = ["never", "24h", "7d", "30d"], ELABEL = { never: "never expires", "24h": "expires in 24 hours", "7d": "expires in 7 days", "30d": "expires in 30 days" };
  F.expiry = {
    label: "Expiry", agent: "cursor", title: "Pages that clean up after themselves",
    blurb: "Set a page to expire in a few days, or keep it for good. Guest pages made without an account last 24 hours unless you claim them.",
    init: () => ({ hit: [], rows: [["Pricing draft", "7d"], ["Rollout plan", "never"], ["Guest page", "24h"], ["Q3 report", "30d"]].map(([t, e]) => ({ t, e })) }),
    render(el, s, redraw) {
      put(el, h("div", { cls: "fx-page" },
        h("div", { cls: "fx-url" }, h("span", {}, "Acme · expiry"), h("span", { cls: "fx-pill" }, s.rows.length + " pages")),
        h("h4", {}, "When pages expire"),
        h("div", { cls: "fx-table" }, s.rows.map(r => h("div", { "data-key": r.t, cls: "fx-tr" + (s.hit.includes(r.t) ? " hit" : "") }, h("b", {}, r.t),
          h("button", { type: "button", cls: "fx-pill fx-click" + (r.e === "never" ? " ok" : r.e === "30d" ? "" : " warn"), on: { click: () => { r.e = ORDER[(ORDER.indexOf(r.e) + 1) % ORDER.length]; s.hit = [r.t]; redraw(); } } }, ELABEL[r.e])))),
        h("small", { cls: "fx-hint" }, "Click a badge to change when a page expires")));
    },
    asks: [
      { q: "Let the rollout plan expire in 7 days", cli: ["byagent expire k4m2Qv9pXz1A 7d"], run: s => { s.rows.find(r => r.t === "Rollout plan").e = "7d"; s.hit = ["Rollout plan"]; return "Rollout plan now expires in 7 days. The link stops working after that."; } },
      { q: "Keep the rollout plan for good", cli: ["byagent expire k4m2Qv9pXz1A never"], run: s => { s.rows.find(r => r.t === "Rollout plan").e = "never"; s.hit = ["Rollout plan"]; return "Done. Rollout plan never expires."; } },
      { q: "Which pages expire this week?", cli: ["byagent list --json"], run: s => { const soon = s.rows.filter(r => r.e === "24h" || r.e === "7d"); s.hit = soon.map(r => r.t); return soon.length ? soon.map(r => `${r.t} (${ELABEL[r.e].replace("expires in ", "")})`).join(", ") : "Nothing expires this week."; } },
    ],
  };

  // ---- 6. Project brief ----
  F.brief = {
    label: "Project brief", agent: "hermes", title: "A handoff note for the next agent",
    blurb: "Every project has a BRIEF.md: the goal, where it stands and the next move. The next agent reads it, with its open notes, before it starts.",
    init: () => ({ v: 3, hit: [], goal: "every team on the new build by month end.", stands: "pilot team on v2 since Tuesday.", next: "roll out to the next two teams.", notes: 2 }),
    render(el, s) {
      const field = (k, label) => h("p", { cls: "fx-field" + (s.hit.includes(k) ? " hit" : "") }, h("b", {}, label), " ",
        h("span", { contenteditable: "true", spellcheck: "false", on: { input: e => { s[k] = e.target.textContent; } } }, s[k]));
      put(el, h("div", { cls: "fx-page" }, h("div", { cls: "fx-url" }, h("span", {}, "Launch / BRIEF.md"), h("span", { cls: "fx-pill" }, "v" + s.v)), h("h4", {}, "Launch brief"),
        field("goal", "Goal"), field("stands", "Where it stands"), field("next", "Next move"),
        h("div", { cls: "fx-foot" }, h("span", { cls: "fx-pill warn" }, s.notes + " open notes"), h("small", { cls: "fx-hint" }, "Click any line to edit it"))));
    },
    asks: [
      { q: "Read the Launch brief before you start", cli: ["byagent brief --project Launch --json"], run: s => { s.hit = ["goal", "stands", "next"]; return `Read BRIEF.md v${s.v} and ${s.notes} open notes. Picking up: ${s.next}`; } },
      { q: "Update the project brief", cli: ["byagent brief push --project Launch"], run: s => { s.stands = "two teams on v2, error rates flat."; s.v++; s.hit = ["stands"]; return `Updated "Where it stands" and pushed BRIEF.md v${s.v}.`; } },
      { q: "What's the next move on Launch?", cli: ["byagent brief --project Launch"], run: s => { s.hit = ["next"]; return "Next move: " + s.next; } },
    ],
  };

  const TABS = ["shelf", "notes", "private", "versions", "expiry", "brief"];

  const wait = ms => new Promise(r => setTimeout(r, still ? 0 : ms));
  const DOT = { "claude code": "var(--a-claude)", codex: "var(--a-codex)", cursor: "var(--a-cursor)", hermes: "var(--a-hermes)" };
  const seg = document.getElementById("fc-seg"), log = document.getElementById("fc-log"), chips = document.getElementById("fc-chips"), mock = document.getElementById("fc-mock");
  let key = TABS[0], state = {}, busy = false, token = 0;
  const redraw = keepFocus => { const r = F[key].render(mock, state[key], redraw); if (keepFocus && r) { r.focus(); r.setSelectionRange(r.value.length, r.value.length); } };
  const add = el => { log.append(el); log.scrollTop = log.scrollHeight; return el; };

  async function ask(i) {
    if (busy) return; busy = true; const my = token, f = F[key], a = f.asks[i];
    [...chips.children].forEach((b, j) => { b.disabled = true; if (j === i) b.classList.add("used"); });
    add(h("div", { cls: "m me" }, a.q)); await wait(450);
    for (const c of a.cli) {
      const t = add(h("div", { cls: "tool wip" }, h("b", {}, "●"), c)); await wait(560);
      if (my !== token) return;
      t.className = "tool"; t.firstChild.textContent = "✓";
    }
    const dots = add(h("div", { cls: "typing" }, h("i"), h("i"), h("i"))); await wait(520);
    if (my !== token) return;
    const said = a.run(state[key]); redraw(); dots.remove();
    add(h("div", { cls: "m ag" }, said));
    [...chips.children].forEach(b => b.disabled = false); busy = false;
  }
  function pick(k, autoplay) {
    token++; busy = false; key = k; state[k] = F[k].init();
    seg.querySelectorAll("button").forEach(b => { b.classList.toggle("on", b.dataset.k === k); b.setAttribute("aria-selected", b.dataset.k === k); });
    document.getElementById("fc-aname").textContent = F[k].agent.replace(/\b\w/g, c => c.toUpperCase());
    document.getElementById("fc-who").style.setProperty("--dot", DOT[F[k].agent]);
    document.getElementById("fc-ttl").textContent = F[k].title; mock.replaceChildren(); mock.classList.remove("swap"); void mock.offsetWidth; mock.classList.add("swap"); document.getElementById("fc-bl").textContent = F[k].blurb;
    put(log); put(chips, F[k].asks.map((a, i) => h("button", { type: "button", on: { click: () => ask(i) } }, a.q)));
    redraw(); if (autoplay) ask(0);
  }
  TABS.forEach(k => seg.append(h("button", { type: "button", role: "tab", "data-k": k, on: { click: () => pick(k, true) } }, F[k].label)));
  pick(key, false);
  new IntersectionObserver((e, o) => { if (e[0].isIntersecting) { ask(0); o.disconnect(); } }, { threshold: .4 }).observe(document.getElementById("fc-duo"));
})();
