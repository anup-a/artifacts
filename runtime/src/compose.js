// Selection -> floating button -> popover -> POST. Also the element-pick mode
// driven by the panel's "Point at element" toggle.
import { anchorFromSelection, anchorFromElement } from "./dom.js";

const STYLE = `
.fab{position:absolute;z-index:2147483001;border:0;border-radius:6px;
padding:6px 11px;font-size:12px;font-weight:600;background:#1f2328;color:#fff;
cursor:pointer;box-shadow:0 2px 8px rgba(0,0,0,.3);
font-family:ui-sans-serif,system-ui,-apple-system,sans-serif}
.pop{position:absolute;z-index:2147483002;width:270px;background:#fff;
color:#1f2328;border:1px solid #d8dee4;border-radius:10px;padding:11px;
box-shadow:0 6px 24px rgba(0,0,0,.18);
font-family:ui-sans-serif,system-ui,-apple-system,sans-serif}
.pop p{margin:0 0 8px;font-size:11px;color:#656d76;border-left:2px solid #d8dee4;
padding-left:7px;overflow-wrap:anywhere;max-height:52px;overflow:hidden}
.pop input,.pop textarea{width:100%;font-size:12px;padding:6px 8px;margin-bottom:7px;
border:1px solid #d8dee4;border-radius:6px;background:#fff;color:#1f2328;
font-family:inherit;resize:vertical}
.pop textarea{min-height:62px}
.row{display:flex;gap:6px;justify-content:flex-end}
.row button{font-size:12px;padding:6px 11px;border-radius:6px;cursor:pointer}
.row .send{border:0;background:#1f2328;color:#fff}
.row .cancel{border:1px solid #d8dee4;background:#fff;color:#424a53}
.err{color:#b3261e;font-size:11px;margin:0 0 6px}
`;

function el(tag, props = {}, children = []) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k === "text") n.textContent = v;
    else if (k === "class") n.className = v;
    else if (k.startsWith("on")) n.addEventListener(k.slice(2), v);
    else n.setAttribute(k, v);
  }
  for (const c of children) n.appendChild(c);
  return n;
}

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
      type: "text",
      placeholder: "Your name",
      "aria-label": "Your name",
      value: store.get("name") || "",
    });
    const body = el("textarea", { placeholder: "Leave a comment…", "aria-label": "Comment" });
    const send = el("button", { class: "send", type: "button", text: "Comment" });
    const cancel = el("button", { class: "cancel", type: "button", text: "Cancel", onclick: hidePop });

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
    if (quoted) pop.appendChild(el("p", { text: quoted }));
    pop.appendChild(err);
    pop.appendChild(name);
    pop.appendChild(body);
    pop.appendChild(el("div", { class: "row" }, [cancel, send]));
    placeAt(pop, x, y);
    pop.style.display = "";
    body.focus();
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
      placeAt(fab, ev.clientX + 6, ev.clientY + 10);
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
      parseFloat(fab.style.top) - window.scrollY
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
