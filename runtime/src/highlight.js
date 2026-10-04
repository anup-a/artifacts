// Paints anchor highlights onto the host page.
//
// A text anchor inside one text node is wrapped in a single <mark>. One that
// spans elements gets a <mark> per text piece, so the page structure is never
// changed: no element is moved or split.
const CLASS = "artifacts-hl";
const ATTR = "data-artifacts-thread";
// Highlights stay in the DOM but are only drawn while this attribute is on
// <html>, i.e. while the comments panel is open. Reviewers see a clean page.
const SHOW = "data-artifacts-hl";
const ON = `[${SHOW}="on"]`;

const shown = () => document.documentElement.getAttribute(SHOW) === "on";

/** Show or hide every highlight without repainting. */
export function setShown(on) {
  if (on) document.documentElement.setAttribute(SHOW, "on");
  else document.documentElement.removeAttribute(SHOW);
}

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
  // Hidden marks are fully inert so the page reads exactly as published.
  styleEl.textContent = `
.${CLASS}{background:none;color:inherit;font:inherit;padding:0}
${ON} .${CLASS}{background:${COLORS.attached};cursor:pointer;border-radius:2px;
box-shadow:0 1px 0 rgba(224,168,0,.9);transition:background .12s ease,box-shadow .12s ease}
${ON} .${CLASS}[data-status="drifted"]{background:${COLORS.drifted};
box-shadow:0 1px 0 rgba(191,54,12,.7)}
${ON} .${CLASS}:hover{background:rgba(255,193,7,.6)}
${ON} .${CLASS}[data-active="1"]{background:rgba(255,193,7,.8);
box-shadow:0 0 0 2px rgba(224,168,0,.95),0 0 0 5px rgba(224,168,0,.2)}
${ON} [${ATTR}]:not(mark){outline:2px solid rgba(255,193,7,.9)!important;outline-offset:2px!important;cursor:pointer!important}
${ON} [${ATTR}][data-artifacts-status="drifted"]:not(mark){outline:2px dashed rgba(191,54,12,.7)!important}`;
  document.head.appendChild(styleEl);
}

// Outlines live on the publisher's own elements, so undoing them needs the
// listener they added. The outline itself comes from the stylesheet.
const outlined = new Map(); // element -> onClick

export function clearHighlights() {
  for (const el of Array.from(document.querySelectorAll(`.${CLASS}`))) {
    const parent = el.parentNode;
    if (!parent) continue;
    while (el.firstChild) parent.insertBefore(el.firstChild, el);
    parent.removeChild(el);
    parent.normalize();
  }
  for (const [el, onClick] of outlined) {
    el.removeAttribute(ATTR);
    el.removeAttribute("data-artifacts-status");
    el.removeEventListener("click", onClick);
  }
  outlined.clear();
}

function makeMark(threadId, status, onClick) {
  const mark = document.createElement("mark");
  mark.className = CLASS;
  mark.setAttribute(ATTR, threadId);
  mark.setAttribute("data-status", status);
  mark.addEventListener("click", (ev) => {
    if (!shown()) return;
    ev.preventDefault();
    ev.stopPropagation();
    onClick(threadId);
  });
  return mark;
}

function markRange(range, threadId, status, onClick) {
  // One text node: a single <mark> around exactly the quoted characters.
  if (range.startContainer === range.endContainer && range.startContainer.nodeType === Node.TEXT_NODE) {
    try {
      range.surroundContents(makeMark(threadId, status, onClick));
      return true;
    } catch {
      return false;
    }
  }
  // Across elements (two list items, a table row, text around a <code>): wrap
  // each text piece in its own <mark>. Moving elements into one <mark> would
  // split them and leave a <mark> where only <li>/<tr> may go.
  const root =
    range.commonAncestorContainer.nodeType === Node.TEXT_NODE
      ? range.commonAncestorContainer.parentNode
      : range.commonAncestorContainer;
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  const pieces = [];
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    if (!range.intersectsNode(n)) continue;
    const start = n === range.startContainer ? range.startOffset : 0;
    const end = n === range.endContainer ? range.endOffset : n.data.length;
    // Whitespace between block elements (e.g. inside <ol>) is not content.
    if (start < end && n.data.slice(start, end).trim()) pieces.push({ n, start, end });
  }
  for (const { n, start, end } of pieces) {
    let target = n;
    if (start > 0) target = target.splitText(start);
    if (end - start < target.data.length) target.splitText(end - start);
    const mark = makeMark(threadId, status, onClick);
    target.parentNode.insertBefore(mark, target);
    mark.appendChild(target);
  }
  return pieces.length > 0;
}

function outlineElement(el, threadId, status, onClick) {
  if (outlined.has(el)) return;
  const handler = (ev) => {
    if (!shown()) return;
    ev.preventDefault();
    ev.stopPropagation();
    onClick(threadId);
  };
  outlined.set(el, handler);
  el.setAttribute(ATTR, threadId);
  el.setAttribute("data-artifacts-status", status);
  el.addEventListener("click", handler);
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
