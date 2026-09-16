// Selection -> floating "Add comment" button -> popover -> POST. Also the
// element-pick mode driven by the panel's footer button.
import { anchorFromSelection, anchorFromElement } from "./dom.js";
import { el, autogrow, snippet } from "./theme.js";
import { icon } from "./icons.js";

const GAP = 8;
const POP_W = 320;

const STYLE = `
.fab{position:absolute;z-index:2147483001;display:flex;gap:6px;align-items:center;padding:6px 12px 6px 10px;
border-radius:999px;background:var(--fg);color:var(--bg);font-size:12px;font-weight:600;
box-shadow:var(--shadow);white-space:nowrap}
.fab *{color:var(--bg)}
.fab:hover{opacity:.92}
.pop{position:absolute;z-index:2147483002;width:${POP_W}px;max-width:calc(100vw - 16px);background:var(--card);
border:1px solid var(--line);border-radius:12px;padding:12px;box-shadow:var(--shadow-lg)}
.pop .snip{margin:0 0 10px}
.pop .who{display:flex;align-items:center;gap:6px;font-size:12px;color:var(--muted);margin-bottom:8px}
.pop .who b{color:var(--fg);font-weight:500}
.pop .who .link{color:var(--muted);text-decoration:underline;text-underline-offset:2px;font-size:12px}
.pop .who .link:hover{color:var(--fg)}
.pop .name{margin-bottom:8px}
.pop textarea.field{min-height:64px;max-height:200px}
.pop .row{display:flex;gap:6px;justify-content:flex-end;align-items:center;margin-top:10px}
.pop .row .hint{flex:1;font-size:11px;color:var(--muted);display:flex;gap:4px;align-items:center}
.pop .err{margin:0 0 6px}
`;

function uuid() {
  if (crypto && crypto.randomUUID) return crypto.randomUUID();
  return `k-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

const IS_MAC = /Mac|iPhone|iPad/.test(navigator.platform || "");

export function mountCompose(shadow, ctx) {
  const { client, store, panel } = ctx;

  shadow.appendChild(el("style", { text: STYLE }));

  const fab = el("button", { class: "fab", type: "button", "aria-label": "Add comment" }, [
    icon("plus", 14),
    el("span", { text: "Add comment" }),
  ]);
  fab.hidden = true;
  const pop = el("div", { class: "pop", role: "dialog", "aria-label": "New comment" });
  pop.hidden = true;
  shadow.appendChild(fab);
  shadow.appendChild(pop);

  let pending = null; // the anchor awaiting a body
  let rect = null; // viewport rect of the selection or picked element

  function hideFab() {
    fab.hidden = true;
  }
  function hidePop() {
    pop.hidden = true;
    pop.replaceChildren();
    pending = null;
  }
  panel.isComposing = () => !pop.hidden;

  // Absolute coordinates so the UI stays glued to the text when the page scrolls.
  function placeAt(node, x, y) {
    node.style.left = `${Math.round(x + window.scrollX)}px`;
    node.style.top = `${Math.round(y + window.scrollY)}px`;
  }

  /** Centre the button above the selection, or below it near the top of the page. */
  function placeFab(r) {
    fab.style.visibility = "hidden";
    fab.hidden = false;
    const w = fab.offsetWidth;
    const h = fab.offsetHeight;
    const x = Math.min(Math.max(GAP, r.left + r.width / 2 - w / 2), window.innerWidth - w - GAP);
    const y = r.top - h - GAP >= GAP ? r.top - h - GAP : r.bottom + GAP;
    placeAt(fab, x, y);
    fab.style.visibility = "";
  }

  /** Below the selection when it fits, above it otherwise; never on top of it. */
  function placePop(r) {
    pop.style.visibility = "hidden";
    pop.hidden = false;
    const w = pop.offsetWidth;
    const h = pop.offsetHeight;
    const x = Math.min(Math.max(GAP, r.left), window.innerWidth - w - GAP);
    let y = r.bottom + GAP;
    if (y + h > window.innerHeight - GAP && r.top - h - GAP >= GAP) y = r.top - h - GAP;
    placeAt(pop, x, y);
    pop.style.visibility = "";
    pop.scrollIntoView({ block: "nearest" });
  }

  function openPopover(anchor, r) {
    pending = anchor;
    rect = r;
    const quoted = anchor.kind === "element" ? anchor.text : anchor.quote;
    const known = store.get("name") || "";

    const err = el("p", { class: "err" });
    err.hidden = true;
    const name = el("input", {
      class: "field name",
      type: "text",
      placeholder: "Your name",
      "aria-label": "Your name",
      value: known,
      onkeydown: (ev) => {
        if (ev.key === "Enter") {
          ev.preventDefault();
          body.focus();
        }
      },
    });
    const who = el("p", { class: "who" }, [
      document.createTextNode("Commenting as "),
      el("b", { text: known }),
      document.createTextNode("·"),
      el("button", {
        class: "link",
        type: "button",
        text: "change",
        onclick: () => {
          who.hidden = true;
          name.hidden = false;
          name.focus();
          name.select();
        },
      }),
    ]);
    name.hidden = !!known;
    who.hidden = !known;

    const body = el("textarea", {
      class: "field",
      placeholder: "Add a comment…",
      "aria-label": "Comment",
      rows: "3",
      onkeydown: (ev) => {
        if (ev.key === "Enter" && (ev.metaKey || ev.ctrlKey)) {
          ev.preventDefault();
          send.click();
        }
      },
    });
    autogrow(body);
    const send = el("button", { class: "btn pri send", type: "button", text: "Comment" });
    const cancel = el("button", { class: "btn cancel", type: "button", text: "Cancel", onclick: hidePop });

    send.addEventListener("click", async () => {
      const author = name.value.trim() || "Anonymous";
      const what = body.value.trim();
      if (!what) {
        err.textContent = "Write something first.";
        err.hidden = false;
        body.focus();
        return;
      }
      store.set("name", author);
      send.disabled = true;
      send.textContent = "Posting…";
      const res = await client.createThread(author, what, pending, uuid());
      send.disabled = false;
      send.textContent = "Comment";
      if (!res.ok) {
        err.textContent = res.error || "Could not post.";
        err.hidden = false;
        return;
      }
      hidePop();
      window.getSelection().removeAllRanges();
      await ctx.onCreated();
      panel.open();
    });

    const hint = el("span", { class: "hint" }, [
      el("span", { class: "kbd", text: IS_MAC ? "⌘↵" : "Ctrl+↵" }),
      el("span", { text: "to post" }),
    ]);
    pop.replaceChildren();
    if (quoted) pop.appendChild(snippet(quoted));
    pop.appendChild(who);
    pop.appendChild(name);
    pop.appendChild(body);
    pop.appendChild(err);
    pop.appendChild(el("div", { class: "row" }, [hint, cancel, send]));
    placePop(rect);
    body.focus();
  }

  // Selection -> floating button.
  document.addEventListener("mouseup", (ev) => {
    if (panel.pickMode) return;
    if (ev.target && ev.target.closest && ev.target.closest("#artifacts-rt")) return;
    // Let the browser finish updating the selection before reading it.
    setTimeout(() => {
      if (!pop.hidden) return;
      const sel = window.getSelection();
      const anchor = anchorFromSelection(sel);
      if (!anchor) {
        hideFab();
        return;
      }
      rect = sel.getRangeAt(0).getBoundingClientRect();
      fab.dataset.pending = JSON.stringify(anchor);
      placeFab(rect);
    }, 0);
  });

  document.addEventListener("mousedown", (ev) => {
    if (ev.target && ev.target.closest && ev.target.closest("#artifacts-rt")) return;
    hideFab();
  });

  fab.addEventListener("click", () => {
    const anchor = JSON.parse(fab.dataset.pending || "null");
    if (!anchor) return;
    hideFab();
    openPopover(anchor, rect);
  });

  // Element-pick mode.
  document.addEventListener(
    "click",
    (ev) => {
      if (!panel.pickMode) return;
      if (ev.target && ev.target.closest && ev.target.closest("#artifacts-rt")) return;
      ev.preventDefault();
      ev.stopPropagation();
      const anchor = anchorFromElement(ev.target);
      panel.setPickMode(false);
      openPopover(anchor, ev.target.getBoundingClientRect());
    },
    true // capture, so the host page's own handlers do not swallow the click
  );

  document.addEventListener("scroll", hideFab, { passive: true });
  document.addEventListener("keydown", (ev) => {
    if (ev.key === "Escape") {
      hideFab();
      hidePop();
      panel.setPickMode(false);
    }
  });
}
