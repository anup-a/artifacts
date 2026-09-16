// Paints anchor highlights onto the host page.
//
// Text anchors are wrapped in a <mark> via Range.surroundContents where the range
// allows it; when it spans element boundaries we fall back to extract-and-insert,
// which handles the multi-node case without corrupting the page.
const CLASS = "artifacts-hl";
const ATTR = "data-artifacts-thread";

const COLORS = {
  attached: "rgba(255, 213, 79, 0.38)",
  drifted: "rgba(255, 138, 101, 0.32)",
};

let styleEl = null;

function ensureStyle() {
  if (styleEl && styleEl.isConnected) return;
  styleEl = document.createElement("style");
  styleEl.setAttribute("data-artifacts", "highlight");
  // Passive marks are a soft tint with a hairline underline; the active one is
  // saturated and ringed so the eye lands on it straight from the panel.
  styleEl.textContent = `
.${CLASS}{background:${COLORS.attached};color:inherit;cursor:pointer;border-radius:2px;
box-shadow:0 1px 0 rgba(224,168,0,.9);transition:background .12s ease,box-shadow .12s ease}
.${CLASS}[data-status="drifted"]{background:${COLORS.drifted};
box-shadow:0 1px 0 rgba(191,54,12,.7)}
.${CLASS}:hover{background:rgba(255,193,7,.6)}
.${CLASS}[data-active="1"]{background:rgba(255,193,7,.8);
box-shadow:0 0 0 2px rgba(224,168,0,.95),0 0 0 5px rgba(224,168,0,.2)}`;
  document.head.appendChild(styleEl);
}

export function clearHighlights() {
  for (const el of Array.from(document.querySelectorAll(`.${CLASS}`))) {
    const parent = el.parentNode;
    if (!parent) continue;
    while (el.firstChild) parent.insertBefore(el.firstChild, el);
    parent.removeChild(el);
    parent.normalize();
  }
}

function markRange(range, threadId, status, onClick) {
  const mark = document.createElement("mark");
  mark.className = CLASS;
  mark.setAttribute(ATTR, threadId);
  mark.setAttribute("data-status", status);
  mark.addEventListener("click", (ev) => {
    ev.preventDefault();
    ev.stopPropagation();
    onClick(threadId);
  });
  try {
    range.surroundContents(mark);
    return true;
  } catch {
    try {
      mark.appendChild(range.extractContents());
      range.insertNode(mark);
      return true;
    } catch {
      return false;
    }
  }
}

function outlineElement(el, threadId, status, onClick) {
  el.setAttribute(ATTR, threadId);
  el.style.outline = status === "drifted" ? "2px dashed rgba(191,54,12,.7)" : "2px solid rgba(255,193,7,.9)";
  el.style.outlineOffset = "2px";
  el.style.cursor = "pointer";
  el.addEventListener("click", (ev) => {
    ev.preventDefault();
    ev.stopPropagation();
    onClick(threadId);
  });
}

/** Paint every resolved, non-orphaned thread. Orphans live in the panel only. */
export function paintThreads(state, onClick) {
  clearHighlights();
  ensureStyle();
  for (const t of state.threads) {
    const r = state.resolved.get(t.id);
    if (!r || r.status === "orphaned") continue;
    if (r.range) {
      markRange(r.range, t.id, r.status, onClick);
    } else if (r.element) {
      outlineElement(r.element, t.id, r.status, onClick);
    }
  }
}

/** Visually emphasise one thread's highlight. */
export function setActive(threadId) {
  for (const el of Array.from(document.querySelectorAll(`.${CLASS}`))) {
    el.setAttribute("data-active", el.getAttribute(ATTR) === threadId ? "1" : "0");
  }
}
