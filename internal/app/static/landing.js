// The landing page, and only that page: sections settling in, the loop in
// the hero (an agent publishes, people comment, the agent revises), the CLI
// tabs and the works-with row. Everything reads without it; this file only
// moves things. The CSP allows no inline script or style attribute, so
// positions here are set through classes or the CSSOM.
(() => {
  "use strict";
  const still = matchMedia("(prefers-reduced-motion: reduce)").matches;
  if (!document.querySelector(".lp3 .hero")) return;

  // Sections settle in as they arrive.
  document.documentElement.classList.add("js-in");
  const settle = new IntersectionObserver(es => es.forEach(e => {
    if (e.isIntersecting) { e.target.classList.add("in"); settle.unobserve(e.target); }
  }), { threshold: .12 });
  document.querySelectorAll(".lp3 [data-in]").forEach(el => still ? el.classList.add("in") : settle.observe(el));

  // The bar gets its rule once the page has scrolled under it.
  const bar = document.querySelector(".lp3 .lp-bar");
  if (bar) {
    const edge = () => bar.style.borderBottomColor = scrollY > 8 ? "var(--line)" : "";
    addEventListener("scroll", edge, { passive: true }); edge();
  }
})();

// ---- The loop ---------------------------------------------------------------
// A list of beats. Each lasts ms, then the next starts. A beat can show a
// group of terminal lines (typing the commands and the prompt), set what the
// browser shows, light a step, or hand the work across the gap. The page as
// served is the last beat, so without script, or with reduced motion, it
// simply stays there. It plays only while it is on screen.
(() => {
  "use strict";
  const loop = document.getElementById("loop");
  if (!loop || matchMedia("(prefers-reduced-motion: reduce)").matches) return;
  const term = document.getElementById("loop-term"), web = document.getElementById("loop-web");
  const wind = document.getElementById("loop-wind"), typed = document.getElementById("loop-typed");
  const steps = [...document.querySelectorAll("#loop-steps li")];
  const lines = [...loop.querySelectorAll(".tline")];
  lines.forEach(l => { l._span = l.querySelector("span"); l._text = l._span.textContent; });
  const comment = typed.textContent;
  const TYPE = 14; // ms a character, for the agent; people type three times slower
  const STATES = ["is-pub", "is-sel", "is-comp", "is-open", "is-v2"];
  const typedLine = l => l.classList.contains("k-prompt") || l.classList.contains("k-cmd");
  const group = g => lines.filter(l => l.dataset.g === String(g));
  const say = g => ({ g, ms: group(g).reduce((ms, l) => Math.max(ms, typedLine(l) ? l._text.length * TYPE + 350 : 450), 0) });

  const beats = [
    { ms: 600, step: 0 },
    say(1), say(2), say(3), say(4),
    { ms: 900, send: "web" },
    { ms: 1200, add: "is-pub", step: 1 },
    { ms: 1000, add: "is-sel" },
    { ms: comment.length * TYPE * 3 + 700, add: "is-comp", type: true },
    { ms: 900, add: "is-open", drop: ["is-comp", "is-sel"] },
    { ms: 900, send: "term" },
    { ...say(5), step: 2 }, say(6), say(7), say(8), say(9), say(10), say(11), say(12), say(13),
    { ms: 900, send: "web" },
    { ms: 5000, add: "is-v2" },
  ];

  let at = 0, timer = 0, playing = false, gen = 0;
  const caret = () => Object.assign(document.createElement("span"), { className: "caret" });
  // Reveal text one character at a time; a newer run stops an older one.
  function typeInto(el, text, ms) {
    const my = gen; let n = 0;
    const c = caret();
    const tick = () => {
      if (my !== gen) return;
      el.textContent = text.slice(0, n); if (n < text.length) { el.append(c); n++; setTimeout(tick, ms); }
    };
    tick();
  }
  function reset() {
    gen++;
    loop.classList.remove(...STATES);
    lines.forEach(l => { l.classList.add("off"); l._span.textContent = l._text; });
    typed.textContent = "";
  }
  function gust(to) {
    wind.replaceChildren(); wind.classList.toggle("back", to === "term");
    // [where %, length %, delay ms, tilt deg] of each streak
    [[6, 55, 0, -1.5], [14, 35, 90, 1], [25, 30, 40, 2], [34, 25, 160, -2], [47, 50, 20, -1], [58, 30, 130, .5], [68, 60, 60, -.5], [78, 35, 190, -1.5], [87, 55, 110, -2], [95, 65, 30, -1]]
      .forEach(([p, len, d, tilt]) => {
        const i = document.createElement("i");
        i.style.setProperty("--at", p + "%"); i.style.setProperty("--len", len + "%");
        i.style.setProperty("--delay", d + "ms"); i.style.setProperty("--tilt", tilt + "deg");
        wind.append(i);
      });
    [term, web].forEach(w => { w.classList.remove("buzz"); void w.offsetWidth; w.classList.add("buzz"); });
    setTimeout(() => wind.replaceChildren(), 900);
  }
  function run(b) {
    if (b === beats[0]) reset();
    if (b.g) group(b.g).forEach(l => { l.classList.remove("off"); if (typedLine(l)) typeInto(l._span, l._text, TYPE); });
    if (b.add) loop.classList.add(b.add);
    if (b.drop) loop.classList.remove(...b.drop);
    if (b.type) typeInto(typed, comment, TYPE * 3);
    if (b.send) gust(b.send);
    if (b.step != null) steps.forEach((s, k) => s.classList.toggle("on", k === b.step));
  }
  function next() {
    timer = 0;
    if (!playing) return;
    const b = beats[at];
    run(b);
    at = (at + 1) % beats.length;
    timer = setTimeout(next, b.ms);
  }
  new IntersectionObserver(([e]) => {
    playing = e.isIntersecting;
    if (playing && !timer) next();
  }, { threshold: .3 }).observe(loop);
})();

// ---- The CLI tabs -------------------------------------------------------------
// Without script every panel shows; with it, one at a time, arrow keys move.
(() => {
  "use strict";
  const tabs = [...document.querySelectorAll("#cli-tabs [role=tab]")];
  if (!tabs.length) return;
  const show = (t, focus) => tabs.forEach(b => {
    const on = b === t;
    b.setAttribute("aria-selected", on); b.tabIndex = on ? 0 : -1;
    document.getElementById(b.getAttribute("aria-controls")).hidden = !on;
    if (on && focus) b.focus();
  });
  tabs.forEach((b, i) => {
    b.addEventListener("click", () => show(b));
    b.addEventListener("keydown", e => {
      const d = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
      if (d) { e.preventDefault(); show(tabs[(i + d + tabs.length) % tabs.length], true); }
    });
  });
  show(tabs[0]);
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
