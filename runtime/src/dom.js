// Bridges the live DOM and the pure matching in anchor.js.
import { makeTextAnchor, resolveTextAnchor } from "./anchor.js";

const RT_HOST_ID = "artifacts-rt";

/** Flatten visible page text, remembering which text node each offset came from. */
export function pageText() {
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT, {
    acceptNode(node) {
      // Never anchor into our own UI, scripts, or styles.
      const p = node.parentElement;
      if (!p) return NodeFilter.FILTER_REJECT;
      if (p.closest(`#${RT_HOST_ID}`)) return NodeFilter.FILTER_REJECT;
      const tag = p.tagName;
      if (tag === "SCRIPT" || tag === "STYLE" || tag === "NOSCRIPT") return NodeFilter.FILTER_REJECT;
      if (!node.nodeValue) return NodeFilter.FILTER_REJECT;
      return NodeFilter.FILTER_ACCEPT;
    },
  });
  let text = "";
  const nodes = [];
  let n;
  while ((n = walker.nextNode())) {
    const start = text.length;
    text += n.nodeValue;
    nodes.push({ node: n, start, end: text.length });
  }
  return { text, nodes };
}

/** Build a live Range spanning [start,end) of the flattened text. */
export function rangeFromOffsets(map, start, end) {
  if (start < 0 || end <= start) return null;
  const from = map.nodes.find((e) => start >= e.start && start < e.end);
  const to = map.nodes.find((e) => end > e.start && end <= e.end);
  if (!from || !to) return null;
  const r = document.createRange();
  try {
    r.setStart(from.node, start - from.start);
    r.setEnd(to.node, end - to.start);
  } catch {
    return null;
  }
  return r;
}

/** Flat offset of a (node, offset) DOM position, or -1 if it is not in the map. */
function offsetOf(map, node, offset) {
  const entry = map.nodes.find((e) => e.node === node);
  return entry ? entry.start + offset : -1;
}

/** Text anchor from the user's current selection, or null if unusable. */
export function anchorFromSelection(sel) {
  if (!sel || sel.isCollapsed || sel.rangeCount === 0) return null;
  const r = sel.getRangeAt(0);
  const map = pageText();
  const start = offsetOf(map, r.startContainer, r.startOffset);
  const end = offsetOf(map, r.endContainer, r.endOffset);
  if (start === -1 || end === -1 || end <= start) return null;
  const a = makeTextAnchor(map.text, start, end);
  return a.quote.trim() ? a : null;
}

/** A stable-ish CSS path: ids win, otherwise nth-of-type down from body. */
export function cssPath(el) {
  if (!el || el.nodeType !== 1) return "";
  const parts = [];
  let cur = el;
  while (cur && cur.nodeType === 1 && cur !== document.body) {
    if (cur.id) {
      parts.unshift(`#${CSS.escape(cur.id)}`);
      return parts.join(" > ");
    }
    const tag = cur.tagName.toLowerCase();
    const parent = cur.parentElement;
    if (!parent) break;
    const sibs = Array.from(parent.children).filter((c) => c.tagName === cur.tagName);
    parts.unshift(sibs.length > 1 ? `${tag}:nth-of-type(${sibs.indexOf(cur) + 1})` : tag);
    cur = parent;
  }
  return parts.length ? `body > ${parts.join(" > ")}` : "body";
}

/** Element anchor per spec section 6. */
export function anchorFromElement(el) {
  return {
    kind: "element",
    css: cssPath(el),
    text: (el.textContent || "").trim().slice(0, 120),
  };
}

function resolveElementAnchor(anchor) {
  const want = (anchor.text || "").trim();
  let el = null;
  try {
    el = anchor.css ? document.querySelector(anchor.css) : null;
  } catch {
    el = null; // malformed selector from an older runtime
  }
  if (el && !el.closest(`#${RT_HOST_ID}`)) {
    const got = (el.textContent || "").trim().slice(0, 120);
    if (!want || got === want) return { status: "attached", element: el, range: null };
  }
  // Selector missed. A unique element with the same text is a drift, not a loss.
  if (want) {
    const matches = Array.from(document.body.querySelectorAll("*")).filter(
      (c) =>
        !c.closest(`#${RT_HOST_ID}`) &&
        c.children.length === 0 &&
        (c.textContent || "").trim().slice(0, 120) === want
    );
    if (matches.length === 1) return { status: "drifted", element: matches[0], range: null };
  }
  return { status: "orphaned", element: null, range: null };
}

/**
 * Resolve any anchor against the current page.
 * Returns {status:"attached"|"drifted"|"orphaned", range, element}.
 */
export function resolveAnchor(anchor) {
  if (!anchor || typeof anchor !== "object") {
    return { status: "orphaned", range: null, element: null };
  }
  if (anchor.kind === "element") return resolveElementAnchor(anchor);
  const map = pageText();
  const r = resolveTextAnchor(map.text, anchor);
  if (r.status === "orphaned") return { status: "orphaned", range: null, element: null };
  const range = rangeFromOffsets(map, r.start, r.end);
  if (!range) return { status: "orphaned", range: null, element: null };
  return { status: r.status, range, element: null };
}
