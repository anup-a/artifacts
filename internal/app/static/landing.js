// The landing page, and only that page: the feature cards in the hero, the
// dashboard window that lands flat as you scroll, the sections settling in,
// the pager, the "brrr" scribble, and the feature demos. Everything works
// and reads without it; this file only moves things. The CSP allows no
// inline script or style attribute, so colours and positions here are set
// through classes or the CSSOM.
(() => {
  "use strict";
  const still = matchMedia("(prefers-reduced-motion: reduce)").matches;
  const hero = document.querySelector(".lp3 .hero");
  if (!hero) return;

  // scroll: settle sections in as they arrive
  document.documentElement.classList.add("js-in");
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
    for (let i = 0; i < 14; i++) {
      const a = rnd() * Math.PI * 2, r = 14 + rnd() * 26;
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

  // the cards: a soft light follows the pointer across the one it is over
  document.querySelectorAll(".lp3 .card").forEach(c => c.addEventListener("pointermove", e => {
    const r = c.getBoundingClientRect();
    c.style.setProperty("--mx", (e.clientX - r.left) + "px"); c.style.setProperty("--my", (e.clientY - r.top) + "px");
  }));

  // the publish card: a different agent publishes a different page every few seconds
  const pubAgent = document.getElementById("pub-agent"), pubDir = document.getElementById("pub-dir"), pubLink = document.getElementById("pub-link");
  if (pubAgent && !still) {
    const host = pubLink.textContent.split("/a/")[0];
    const runs = [["claude code", "./launch-plan", "k4m2Qv9pXz1A"], ["codex", "./q3-report", "Hr8LmZ2xQa0c"], ["cursor", "./pricing-draft", "zMzQTdMFg9ng"], ["hermes", "./moodboard", "Az5naoIGGjHk"]];
    let k = 0;
    setInterval(() => {
      const [a, d, id] = runs[k = (k + 1) % runs.length];
      [[pubAgent, a], [pubDir, d], [pubLink, host + "/a/" + id]].forEach(([el, v]) => { el.textContent = v; el.classList.remove("swap"); void el.offsetWidth; el.classList.add("swap"); });
    }, 2800);
  }

  // the pager: a dash per section, the one you are in lit
  const pager = document.getElementById("pager");
  const secs = [hero, ...document.querySelectorAll(".lp3 > section:not(.hero)")];
  const dots = secs.map(() => pager.querySelector(".dots").appendChild(document.createElement("i")));
  let at = -1;
  function where() {
    const line = innerHeight * .45;
    let i = 0; secs.forEach((s, k) => { if (s.getBoundingClientRect().top <= line) i = k; });
    const end = innerHeight + scrollY >= document.documentElement.scrollHeight - 4;
    if (end) i = secs.length - 1;
    if (i !== at) { at = i; dots.forEach((d, k) => d.classList.toggle("on", k === i)); }
    pager.classList.toggle("done", i === secs.length - 1);
  }
  pager.hidden = false; where();

  // scroll: the pager follows, and the dashboard window lands
  let ticking = false;
  addEventListener("scroll", () => {
    if (ticking) return; ticking = true;
    requestAnimationFrame(() => {
      where(); tiltShot();
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
  }
  tiltShot();

  addEventListener("resize", where);
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
  // The demo speaks the page's language: every line a person reads goes
  // through t(). Agent names, CLI commands and hosts stay as they are.
  const { t, tn } = window.byagentI18n;
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
  // A project name in a CLI line, quoted only when it needs to be, so the
  // command shows the same name the mock does in every language.
  const arg = s => /^[\w.-]+$/.test(s) ? s : '"' + s + '"';
  // A compact age, as the dashboard's cards show it.
  const AGO = { m: n => t("%dm", n), h: n => t("%dh", n), d: n => t("%dd", n) };

  // ---- 1. The shelf: a compact copy of the real byagent dashboard ----
  // Project dots are classes, not inline colours: the CSP allows no style attributes.
  const P_DATA = t("Dashboards & data"), P_DESIGN = t("Design studio"), P_GUIDES = t("Explainers & guides");
  const PROJ = { [P_DATA]: "pj-data", [P_DESIGN]: "pj-design", [P_GUIDES]: "pj-guides" };
  const LISBON = t("Lisbon rides"), MOOD = t("Studio moodboard");
  // The search term is the first word of the short title, so the search
  // finds the page in every language: "lisbon" in English.
  const LISBON_Q = LISBON.split(/\s+/)[0].toLowerCase();
  const ICON = {
    ext: '<svg viewBox="0 0 16 16"><path d="M9 3h4v4M13 3 7.5 8.5M11 9.5V13H3V5h3.5"/></svg>',
  };
  F.shelf = {
    label: t("Projects"), agent: "claude code", title: t("Every page, filed by project"),
    blurb: t("Each page your agents publish gets its own link and lands under a project. Agents list and search your pages before they start something new."),
    init: () => ({ proj: "All", q: "", hit: null, pages: [
      ["lisbon", t("Lisbon rides: the weekend edition"), P_DATA, "codex", AGO.m(2)], ["signups", t("Signups, week 41"), P_DATA, "codex", AGO.m(14)],
      ["garden", t("Small bottles, big garden"), P_DESIGN, "opencode", AGO.m(14)], ["retention", t("Q3 retention by cohort"), P_DATA, "claude code", AGO.h(1)],
      ["kyoto", t("Five days in Kyoto"), P_GUIDES, "claude code", AGO.h(2)], ["noodles", t("Charred corn & miso butter noodles"), P_GUIDES, "claude code", AGO.h(3)],
      ["coffee", t("Coffee prices, 2015 to 2026"), P_DATA, "opencode", AGO.h(5)], ["nimbus", t("Nimbus 2.0 release notes"), P_GUIDES, "codex", AGO.d(1)], ["orbits", t("Orbits are just falling sideways"), P_GUIDES, "claude code", AGO.d(2)],
    ].map(([f, title, p, a, ago]) => ({ f, t: title, p, a, ago })) }),
    render(el, s, redraw) {
      const q = s.q.trim().toLowerCase();
      const rows = s.pages.filter(x => (s.proj === "All" || x.p === s.proj) && (!q || x.t.toLowerCase().includes(q)));
      const search = h("input", { cls: "fx-search", placeholder: t("Search titles…"), value: s.q, "aria-label": t("Search titles"),
        on: { input: e => { s.q = e.target.value; s.hit = null; redraw(true); } } });
      const col = p => h("button", { type: "button", "data-key": p, cls: "dd-col" + (s.proj === p ? " on" : ""), on: { click: () => { s.proj = p; s.hit = null; redraw(); } } },
        p === "All" ? h("i", { cls: "all" }) : h("i", { cls: PROJ[p] }), h("span", {}, p === "All" ? t("All pages") : p),
        h("em", {}, String(p === "All" ? s.pages.length : s.pages.filter(x => x.p === p).length)));
      put(el, h("div", { cls: "dd" },
        h("aside", { cls: "dd-side" }, h("div", { cls: "dd-h" }, t("Collections")), ["All", ...Object.keys(PROJ)].map(col)),
        h("div", { cls: "dd-main" },
          h("div", { cls: "dd-top" }, h("b", {}, s.proj === "All" ? t("All pages") : s.proj), search),
          rows.length ? h("div", { cls: "dd-grid" }, rows.map(x => h("div", { "data-key": x.f, cls: "dd-card" + (s.hit === x.f ? " hit" : "") + (x.fresh ? " fresh" : "") },
              h("div", { cls: "dd-cov" }, h("img", { src: COVERS[x.f] || "", alt: "", width: "640", height: "400", loading: "lazy" })),
              h("div", { cls: "dd-foot" }, h("b", {}, x.t), h("div", { cls: "dd-meta" }, h("i", { cls: PROJ[x.p] }), h("span", { cls: "dd-tag" }, x.a), h("small", {}, x.ago), h("span", { cls: "dd-ext", html: ICON.ext }))))))
            : h("div", { cls: "fx-empty" }, t("No page matches that.")))));
      return search;
    },
    asks: [
      { q: t("What's in %s?", P_DATA), cli: ["byagent list --project " + arg(P_DATA)], run: s => { s.proj = P_DATA; s.q = ""; s.hit = null; const n = s.pages.filter(x => x.p === s.proj); return tn(n.length, "%s page: %s.", "%s pages: %s.", n.map(x => x.t.split(/[:：]/)[0]).join(", ")); } },
      { q: t("Find the %s page", LISBON), cli: ['byagent list --search "' + LISBON_Q + '"'], run: s => { s.proj = "All"; s.q = LISBON_Q; s.hit = "lisbon"; return t("Found it: %s, by %s, in %s. %s", LISBON, "codex", P_DATA, HOST + "/a/zMzQTdMFg9ng/"); } },
      { q: t("File the moodboard under %s", P_DESIGN), cli: ["byagent publish ./moodboard --project " + arg(P_DESIGN)], run: s => {
          if (!s.pages.some(x => x.f === "moodboard")) s.pages.unshift({ f: "moodboard", t: t("Studio moodboard, autumn"), p: P_DESIGN, a: "claude code", ago: t("now"), fresh: true });
          s.proj = P_DESIGN; s.q = ""; s.hit = "moodboard"; return t("Published %s and filed it under %s. %s", MOOD, P_DESIGN, HOST + "/a/Az5naoIGGjHk/"); } },
    ],
  };

  // ---- 2. Comments ----
  const LINES = [t("Goal: every team on the new build by month end."), t("Step 1: move every team to the new build in one go."), t("Step 2: watch error rates for 48 hours before the next wave.")];
  const FIXED = t("Step 1: pilot with one team for a week, then roll out to the rest.");
  const ROLLOUT = t("Rollout plan"), GOOD_CALL = t("Good call, piloting first.");
  F.notes = {
    label: t("Comments"), agent: "codex", title: t("Anyone comments on the line they mean"),
    blurb: t("Readers select a sentence and leave a note under a name they type. No account, no seat. Your agent reads the notes, fixes the page and resolves them."),
    init: () => ({ v: 1, fixed: false, draft: null, notes: [{ who: "Maya", s: 1, txt: t("Can we pilot one team for a week first?"), open: true, replies: [] }] }),
    render(el, s, redraw) {
      put(el, h("div", { cls: "fx-page" },
        h("div", { cls: "fx-url" }, h("span", {}, HOST + "/a/k4m2Qv9pXz1A/"), h("span", { cls: "fx-pill" + (s.v > 1 ? " ok" : "") }, "v" + s.v)),
        h("h4", {}, ROLLOUT),
        LINES.map((line, i) => {
          const txt = i === 1 && s.fixed ? FIXED : line, open = s.notes.some(n => n.s === i && n.open);
          return h("p", { cls: "fx-line" + (open ? " noted" : "") + (i === 1 && s.fixed ? " fixed" : ""), title: t("Click to leave a note"), on: { click: () => { s.draft = i; redraw(); } } }, txt);
        }),
        s.draft != null ? h("div", { cls: "fx-compose" },
          h("input", { cls: "fx-in sm", value: t("You"), "aria-label": t("Your name") }),
          h("input", { cls: "fx-in", value: t("Add a rollback step here?"), "aria-label": t("Your note") }),
          h("button", { type: "button", cls: "fx-btn", on: { click: e => { const [name, note] = e.target.closest(".fx-compose").querySelectorAll("input"); s.notes.push({ who: name.value || t("Someone"), s: s.draft, txt: note.value, open: true, replies: [] }); s.draft = null; redraw(); } } }, t("Post")),
          h("small", {}, t("no account"))) : h("small", { cls: "fx-hint" }, t("Click any line to leave a note, no account."))),
        h("div", { cls: "fx-notes" }, s.notes.map((n, k) => h("div", { "data-key": "n" + k, cls: "fx-bub" + (n.open ? "" : " done") },
          h("div", {}, h("b", {}, n.who), h("em", {}, n.open ? t("no account · open") : t("resolved"))), h("div", {}, n.txt),
          n.replies.map(r => h("div", { cls: "fx-reply" }, h("b", {}, r[0]), " ", r[1]))))));
    },
    asks: [
      { q: t("Read the open notes and fix them"), cli: ["byagent comments k4m2Qv9pXz1A --open --json", "byagent publish ./rollout-plan", "byagent resolve k4m2Qv9pXz1A th_…"], run: s => {
          const open = s.notes.filter(n => n.open); if (!open.length) return t("No open notes left.");
          if (open.some(n => n.s === 1) && !s.fixed) { s.fixed = true; s.v++; }
          open.forEach(n => { n.open = false; n.replies.push(["codex", n.s === 1 ? t("Done in v%s: one team first.", s.v) : t("Fixed, see the latest version.")]); });
          return tn(open.length, "Fixed %s note, republished as v%s on the same link, and resolved it.", "Fixed %s notes, republished as v%s on the same link, and resolved them.", s.v); } },
      { q: t("Reply to Maya and resolve it"), cli: ['byagent reply k4m2Qv9pXz1A th_… "' + GOOD_CALL + '"', "byagent resolve k4m2Qv9pXz1A th_…"], run: s => {
          const m = s.notes.find(n => n.who === "Maya"); if (!m.open) return t("Maya's note is already resolved.");
          m.replies.push(["codex", GOOD_CALL]); m.open = false; return t("Replied to Maya and resolved her note."); } },
      { q: t("What did people say since yesterday?"), cli: ["byagent comments k4m2Qv9pXz1A --since 2026-10-04T00:00:00Z"], run: s => {
          const open = s.notes.filter(n => n.open);
          return open.length ? open.map(n => t('%s on "%s…": %s', n.who, LINES[n.s].slice(0, 24), n.txt)).join(" · ") : t("Nothing new. Every note is resolved."); } },
    ],
  };

  // ---- 3. Private pages ----
  const Q3 = t("Q3 report");
  F.private = {
    label: t("Private pages"), agent: "claude code", title: t("Public by link, or private with a code"),
    blurb: t("A private page is visible to your workspace and to anyone you give its six-digit share code. Rotate the code or withdraw it at any time."),
    init: () => ({ access: "public", code: null, typed: "" }),
    render(el, s, redraw) {
      const unlocked = s.access === "public" || (s.code && s.typed === s.code);
      const opt = (k, label, sub) => h("button", { type: "button", cls: "fx-opt" + (s.access === k ? " on" : ""), on: { click: () => { s.access = k; if (k === "public") s.code = null; s.typed = ""; redraw(); } } }, h("i"), label, sub ? h("em", {}, sub) : null);
      put(el, h("div", { cls: "fx-two" },
        h("div", { cls: "fx-card" }, h("div", { cls: "fx-url" }, h("span", {}, HOST + "/a/Hr8LmZ2xQa0c/")),
          unlocked ? h("div", { cls: "fx-open" }, h("b", {}, Q3), h("span", { cls: "fx-bar" }), h("span", { cls: "fx-bar s" }), h("span", { cls: "fx-bar" }),
              h("small", {}, s.access === "public" ? t("Anyone with the link can read this.") : t("Unlocked with the share code.")))
            : h("div", { cls: "fx-lock" }, h("div", { cls: "fx-lock-ic", html: lock }),
              s.code ? h("input", { cls: "fx-code", inputmode: "numeric", maxlength: "6", placeholder: "••••••", value: s.typed, "aria-label": t("Share code"),
                on: { input: e => { s.typed = e.target.value.replace(/\D/g, ""); if (s.typed.length === 6) redraw(); } } }) : null,
              h("small", {}, s.code ? (s.typed.length === 6 && s.typed !== s.code ? t("That code doesn't match.") : t("Enter the six-digit code")) : t("Private. Only your workspace can open it.")))),
        h("div", { cls: "fx-card" }, h("h6", {}, t("Access")), opt("public", t("Anyone with the link")), opt("private", t("Private"), t("share code")),
          s.access === "private" ? h("div", { cls: "fx-coderow" },
            h("span", { cls: "fx-chip" + (s.code ? "" : " off") }, s.code || t("no code")),
            h("button", { type: "button", cls: "fx-link", on: { click: () => { s.code = code6(); s.typed = ""; redraw(); } } }, s.code ? t("Rotate") : t("Create code")),
            s.code ? h("button", { type: "button", cls: "fx-link", on: { click: () => { s.code = null; s.typed = ""; redraw(); } } }, t("Withdraw")) : null) : null)));
    },
    asks: [
      { q: t("Make the Q3 report private"), cli: ["byagent publish ./q3-report --id Hr8LmZ2xQa0c --private"], run: s => { s.access = "private"; s.code = null; s.typed = ""; return t("Q3 report is private. Only your workspace can open it."); } },
      { q: t("Give me a share code for it"), cli: ["byagent share Hr8LmZ2xQa0c"], run: s => { s.access = "private"; s.code = code6(); s.typed = ""; return t("Share code %s. Anyone with the link and this code can open it.", s.code); } },
      { q: t("Withdraw the code"), cli: ["byagent share Hr8LmZ2xQa0c --off"], run: s => { if (!s.code) return t("There's no code to withdraw."); s.code = null; s.typed = ""; return t("Withdrawn. The old code stops working right away."); } },
    ],
  };

  // ---- 4. Versions ----
  F.versions = {
    label: t("Versions"), agent: "codex", title: t("Same link, every version"),
    blurb: t("Publish again and the link stays put. Each version records the agent that published it, and any recent version can be rolled back to."),
    init: () => ({ view: 3, live: 3, vs: [[1, "claude code", t("Mon %s", "10:02"), LINES[1]], [2, "codex", t("Tue %s", "09:15"), t("Step 1: pilot with one team for a week.")], [3, "claude code", t("Wed %s", "16:40"), t("Step 1: pilot with two teams for a week.")]] }),
    render(el, s, redraw) {
      const cur = s.live, [v, a, at, step] = s.vs.find(x => x[0] === s.view);
      const rollback = () => { s.live = s.view; redraw(); };
      put(el, h("div", { cls: "fx-two v" },
        h("div", { cls: "fx-vers" }, s.vs.map(([n, ag, when]) => h("button", { type: "button", "data-key": "v" + n, cls: "fx-v" + (s.view === n ? " on" : ""), on: { click: () => { s.view = n; redraw(); } } },
            h("i", {}, "v" + n), h("span", {}, ag), h("span", { cls: "t" }, when), n === cur ? h("em", {}, t("live")) : null)),
          s.view !== cur ? h("button", { type: "button", cls: "fx-btn ghost", on: { click: rollback } }, t("Roll back to v%s", s.view)) : h("small", { cls: "fx-hint" }, t("Click a version to preview it"))),
        h("div", { cls: "fx-page sm" }, h("div", { cls: "fx-url" }, h("span", {}, HOST + "/a/k4m2Qv9pXz1A/"), h("span", { cls: "fx-pill" + (v === cur ? " ok" : "") }, "v" + v)),
          h("h4", {}, ROLLOUT), h("p", {}, step), h("span", { cls: "fx-bar light" }), h("span", { cls: "fx-bar light s" }), h("small", { cls: "fx-by" }, t("Published by %s, %s", a, at)))));
    },
    asks: [
      { q: t("Show me the versions of the rollout plan"), cli: ["byagent versions k4m2Qv9pXz1A"], run: s => { s.view = s.live; return s.vs.map(([n, a, at]) => n === s.live ? t("%s (live)", `v${n} ${a}, ${at}`) : `v${n} ${a}, ${at}`).join(" · "); } },
      { q: t("Roll back to v%s", 2), cli: ["byagent rollback k4m2Qv9pXz1A 2"], run: s => { s.live = 2; s.view = 2; return t("v2 is live again on the same link. Nothing was re-uploaded, and v3 is still there."); } },
      { q: t("Who published the last version?"), cli: ["byagent versions k4m2Qv9pXz1A --json"], run: s => { const [n, a, at] = s.vs[s.vs.length - 1]; s.view = n; return t("v%s was published by %s, %s.", n, a, at); } },
    ],
  };

  // ---- 5. Expiry ----
  const ORDER = ["never", "24h", "7d", "30d"], ELABEL = { never: t("never expires"), "24h": t("expires in 24 hours"), "7d": t("expires in 7 days"), "30d": t("expires in 30 days") };
  const ESHORT = { "24h": t("24 hours"), "7d": t("7 days") };
  F.expiry = {
    label: t("Expiry"), agent: "cursor", title: t("Pages that clean up after themselves"),
    blurb: t("Set a page to expire in a few days, or keep it for good. Guest pages made without an account last 24 hours unless you claim them."),
    init: () => ({ hit: [], rows: [[t("Pricing draft"), "7d"], [ROLLOUT, "never"], [t("Guest page"), "24h"], [Q3, "30d"]].map(([title, e]) => ({ t: title, e })) }),
    render(el, s, redraw) {
      put(el, h("div", { cls: "fx-page" },
        h("div", { cls: "fx-url" }, h("span", {}, t("%s · expiry", "Acme")), h("span", { cls: "fx-pill" }, tn(s.rows.length, "%s page", "%s pages"))),
        h("h4", {}, t("When pages expire")),
        h("div", { cls: "fx-table" }, s.rows.map(r => h("div", { "data-key": r.t, cls: "fx-tr" + (s.hit.includes(r.t) ? " hit" : "") }, h("b", {}, r.t),
          h("button", { type: "button", cls: "fx-pill fx-click" + (r.e === "never" ? " ok" : r.e === "30d" ? "" : " warn"), on: { click: () => { r.e = ORDER[(ORDER.indexOf(r.e) + 1) % ORDER.length]; s.hit = [r.t]; redraw(); } } }, ELABEL[r.e])))),
        h("small", { cls: "fx-hint" }, t("Click a badge to change when a page expires"))));
    },
    asks: [
      { q: t("Let the rollout plan expire in 7 days"), cli: ["byagent expire k4m2Qv9pXz1A 7d"], run: s => { s.rows.find(r => r.t === ROLLOUT).e = "7d"; s.hit = [ROLLOUT]; return t("Rollout plan now expires in 7 days. The link stops working after that."); } },
      { q: t("Keep the rollout plan for good"), cli: ["byagent expire k4m2Qv9pXz1A never"], run: s => { s.rows.find(r => r.t === ROLLOUT).e = "never"; s.hit = [ROLLOUT]; return t("Done. Rollout plan never expires."); } },
      { q: t("Which pages expire this week?"), cli: ["byagent list --json"], run: s => { const soon = s.rows.filter(r => r.e === "24h" || r.e === "7d"); s.hit = soon.map(r => r.t); return soon.length ? soon.map(r => `${r.t} (${ESHORT[r.e]})`).join(", ") : t("Nothing expires this week."); } },
    ],
  };

  // ---- 6. Project brief ----
  const LAUNCH = t("Launch");
  F.brief = {
    label: t("Project brief"), agent: "hermes", title: t("A handoff note for the next agent"),
    blurb: t("Every project has a BRIEF.md: the goal, where it stands and the next move. The next agent reads it, with its open notes, before it starts."),
    init: () => ({ v: 3, hit: [], goal: t("every team on the new build by month end."), stands: t("pilot team on v2 since Tuesday."), next: t("roll out to the next two teams."), notes: 2 }),
    render(el, s) {
      const field = (k, label) => h("p", { cls: "fx-field" + (s.hit.includes(k) ? " hit" : "") }, h("b", {}, label), " ",
        h("span", { contenteditable: "true", spellcheck: "false", on: { input: e => { s[k] = e.target.textContent; } } }, s[k]));
      put(el, h("div", { cls: "fx-page" }, h("div", { cls: "fx-url" }, h("span", {}, LAUNCH + " / BRIEF.md"), h("span", { cls: "fx-pill" }, "v" + s.v)), h("h4", {}, t("%s brief", LAUNCH)),
        field("goal", t("Goal")), field("stands", t("Where it stands")), field("next", t("Next move")),
        h("div", { cls: "fx-foot" }, h("span", { cls: "fx-pill warn" }, tn(s.notes, "%s open note", "%s open notes")), h("small", { cls: "fx-hint" }, t("Click any line to edit it")))));
    },
    asks: [
      { q: t("Read the %s brief before you start", LAUNCH), cli: ["byagent brief --project " + arg(LAUNCH) + " --json"], run: s => { s.hit = ["goal", "stands", "next"]; return t("Read BRIEF.md v%s and %s open notes. Picking up: %s", s.v, s.notes, s.next); } },
      { q: t("Update the project brief"), cli: ["byagent brief push --project " + arg(LAUNCH)], run: s => { s.stands = t("two teams on v2, error rates flat."); s.v++; s.hit = ["stands"]; return t('Updated "Where it stands" and pushed BRIEF.md v%s.', s.v); } },
      { q: t("What's the next move on %s?", LAUNCH), cli: ["byagent brief --project " + arg(LAUNCH)], run: s => { s.hit = ["next"]; return t("Next move: %s", s.next); } },
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
      const row = add(h("div", { cls: "tool wip" }, h("b", {}, "●"), c)); await wait(560);
      if (my !== token) return;
      row.className = "tool"; row.firstChild.textContent = "✓";
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
    // On a narrow screen the tab row scrolls; centre the chosen tab in it
    // without moving the page, which scrollIntoView would also do.
    const on = seg.querySelector("button.on");
    if (on && seg.scrollWidth > seg.clientWidth) seg.scrollTo({ left: on.offsetLeft - seg.offsetLeft - (seg.clientWidth - on.offsetWidth) / 2, behavior: "smooth" });
    document.getElementById("fc-aname").textContent = F[k].agent.replace(/\b\w/g, c => c.toUpperCase());
    document.getElementById("fc-who").style.setProperty("--dot", DOT[F[k].agent]);
    document.getElementById("fc-ttl").textContent = F[k].title; mock.replaceChildren(); mock.classList.remove("swap"); void mock.offsetWidth; mock.classList.add("swap"); document.getElementById("fc-bl").textContent = F[k].blurb;
    put(log); put(chips, F[k].asks.map((a, i) => h("button", { type: "button", on: { click: () => ask(i) } }, a.q)));
    redraw(); if (autoplay) ask(0);
  }
  TABS.forEach(k => seg.append(h("button", { type: "button", role: "tab", "data-k": k, on: { click: () => pick(k, true) } }, F[k].label)));
  // Fade an edge of the tab row only while there are tabs past it.
  const fade = () => {
    const more = seg.scrollWidth - seg.clientWidth - seg.scrollLeft;
    seg.style.setProperty("--fl", seg.scrollLeft > 1 ? "28px" : "0px");
    seg.style.setProperty("--fr", more > 1 ? "28px" : "0px");
  };
  seg.addEventListener("scroll", fade, { passive: true });
  addEventListener("resize", fade);
  pick(key, false); fade();
  // a card in the hero opens the demo on its feature; its link does the scrolling
  document.querySelectorAll(".lp3 .card[data-feat]").forEach(c => c.addEventListener("click", () => pick(c.dataset.feat, true)));
  new IntersectionObserver((e, o) => { if (e[0].isIntersecting) { ask(0); o.disconnect(); } }, { threshold: .4 }).observe(document.getElementById("fc-duo"));
})();

// The "why" drawings have fixed-width boxes, and a translated label can be
// longer than the English one they were drawn around. A [data-fit] label
// fits the box beside it (the parent's rect of that class, else its first
// rect): its type shrinks until it does.
(() => {
  "use strict";
  document.querySelectorAll("#why text[data-fit]").forEach(text => {
    const box = text.parentNode.querySelector(text.dataset.fit ? "rect." + text.dataset.fit : "rect");
    if (!box) return;
    const room = box.x.baseVal.value + box.width.baseVal.value - text.x.baseVal[0].value - 8;
    const len = text.getComputedTextLength();
    if (len > room) text.style.fontSize = (parseFloat(getComputedStyle(text).fontSize) * room / len).toFixed(2) + "px";
  });
})();

// The "Works with" row stays on one line. When it overflows, its end fades,
// and the fade lifts once it has been scrolled to the last chip.
(() => {
  const row = document.querySelector(".lp3 .works");
  if (!row) return;
  const sync = () => {
    row.classList.toggle("over", row.scrollWidth > row.clientWidth + 1);
    row.classList.toggle("end", row.scrollLeft + row.clientWidth >= row.scrollWidth - 1);
  };
  row.addEventListener("scroll", sync, { passive: true });
  new ResizeObserver(sync).observe(row);
  sync();
})();
