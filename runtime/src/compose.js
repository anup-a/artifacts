// Selection -> floating button -> popover -> POST. Also the element-pick mode
// driven by the panel's footer button.
import { anchorFromSelection, anchorFromElement } from "./dom.js";
import { el } from "./theme.js";

const STYLE = `
.fab{position:absolute;z-index:2147483001;display:flex;gap:6px;align-items:center;padding:6px 10px;
border:0;border-radius:8px;background:var(--fg);color:var(--bg);font-size:12px;font-weight:600;
box-shadow:var(--shadow)}
.fab:after{content:"";position:absolute;left:14px;bottom:-5px;border:5px solid transparent;
border-top-color:var(--fg);border-bottom:0}
.fab:hover{opacity:.92}
.pop{position:absolute;z-index:2147483002;width:300px;background:var(--card);
border:1px solid var(--line);border-radius:12px;padding:12px 12px 10px;box-shadow:var(--shadow)}
.pop .quote{margin:0 0 10px;white-space:normal;max-height:54px;overflow:hidden}
.pop .field{margin-bottom:8px}
.pop textarea.field{min-height:66px}
.pop .row{display:flex;gap:6px;justify-content:flex-end;align-items:center}
.pop .row .grow{flex:1}
.pop .err{color:var(--err);font-size:12px;margin:0 0 6px}
`;

function uuid() {
  if (crypto && crypto.randomUUID) return crypto.randomUUID();
  return `k-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

export function mountCompose(shadow, ctx) {
  const { client, store, panel } = ctx;

  shadow.appendChild(el("style", { text: STYLE }));

  const fab = el("button", { class: "fab", type: "button", text: "Comment" });
  fab.style.display = "none";
  const pop = el("div", { class: "pop" });
  pop.style.display = "none";
  shadow.appendChild(fab);
  shadow.appendChild(pop);

  let pending = null; // the anchor awaiting a body

  function hideFab() {
    fab.style.display = "none";
  }
  function hidePop() {
    pop.style.display = "none";
    pop.replaceChildren();
    pending = null;
  }
  panel.isComposing = () => pop.style.display !== "none";

  function placeAt(node, x, y) {
    node.style.left = `${Math.max(8, x + window.scrollX)}px`;
    node.style.top = `${Math.max(8, y + window.scrollY)}px`;
  }

  function openPopover(anchor, x, y) {
    pending = anchor;
    const quoted = anchor.kind === "element" ? anchor.text : anchor.quote;

    const err = el("p", { class: "err" });
    err.style.display = "none";
    const name = el("input", {
      class: "field",
      type: "text",
      placeholder: "Your name",
      "aria-label": "Your name",
      value: store.get("name") || "",
    });
    const body = el("textarea", { class: "field", placeholder: "Leave a comment…", "aria-label": "Comment" });
    const send = el("button", { class: "btn pri send", type: "button", text: "Comment" });
    const cancel = el("button", { class: "btn cancel", type: "button", text: "Cancel", onclick: hidePop });

    send.addEventListener("click", async () => {
      const who = name.value.trim() || "Anonymous";
      const what = body.value.trim();
      if (!what) {
        err.textContent = "Write something first.";
        err.style.display = "";
        return;
      }
      store.set("name", who);
      send.disabled = true;
      send.textContent = "Posting…";
      const res = await client.createThread(who, what, pending, uuid());
      send.disabled = false;
      send.textContent = "Comment";
      if (!res.ok) {
        err.textContent = res.error || "Could not post.";
        err.style.display = "";
        return;
      }
      hidePop();
      window.getSelection().removeAllRanges();
      await ctx.onCreated();
      panel.open();
    });

    pop.replaceChildren();
    if (quoted) pop.appendChild(el("p", { class: "quote", text: quoted }));
    pop.appendChild(err);
    pop.appendChild(name);
    pop.appendChild(body);
    pop.appendChild(el("div", { class: "row" }, [el("span", { class: "kbd", text: "esc" }), el("span", { class: "grow" }), cancel, send]));
    placeAt(pop, x, y);
    pop.style.display = "";
    clampToViewport(pop, x, y);
    body.focus();
  }

  // Keep the popover on screen: flip above the selection when it would run
  // off the bottom, and pull it left when it would run off the right.
  function clampToViewport(node, x, y) {
    const w = node.offsetWidth;
    const h = node.offsetHeight;
    const maxX = window.innerWidth - w - 8;
    const top = y + h > window.innerHeight - 8 ? Math.max(8, y - h - 52) : y;
    placeAt(node, Math.min(x, maxX), top);
  }

  // Selection -> floating Comment button.
  document.addEventListener("mouseup", (ev) => {
    if (panel.pickMode) return;
    if (ev.target && ev.target.closest && ev.target.closest("#artifacts-rt")) return;
    // Let the browser finish updating the selection before reading it.
    setTimeout(() => {
      if (pop.style.display !== "none") return;
      const anchor = anchorFromSelection(window.getSelection());
      if (!anchor) {
        hideFab();
        return;
      }
      fab.dataset.pending = JSON.stringify(anchor);
      placeAt(fab, ev.clientX + 6, ev.clientY - 40);
      fab.style.display = "";
    }, 0);
  });

  fab.addEventListener("click", () => {
    const anchor = JSON.parse(fab.dataset.pending || "null");
    if (!anchor) return;
    hideFab();
    openPopover(
      anchor,
      parseFloat(fab.style.left) - window.scrollX,
      parseFloat(fab.style.top) - window.scrollY + 36
    );
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
      openPopover(anchor, ev.clientX + 6, ev.clientY + 10);
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
