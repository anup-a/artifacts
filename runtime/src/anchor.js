// Text anchoring per design spec section 6. Pure string logic over the page's
// flattened text so it can be unit-tested without a DOM.

export const CONTEXT = 32;

/** Build a TextQuoteSelector-style anchor from a range over `text`. */
export function makeTextAnchor(text, start, end) {
  return {
    kind: "text",
    quote: text.slice(start, end),
    prefix: text.slice(Math.max(0, start - CONTEXT), start),
    suffix: text.slice(end, end + CONTEXT),
  };
}

/** Collapse runs of whitespace so re-flowed markup still matches. */
function normalize(s) {
  return s.replace(/\s+/g, " ");
}

function allIndexesOf(haystack, needle) {
  const out = [];
  if (!needle) return out;
  let i = haystack.indexOf(needle);
  while (i !== -1) {
    out.push(i);
    i = haystack.indexOf(needle, i + 1);
  }
  return out;
}

/** How many characters `a` and `b` share, anchored at the given end. */
function overlap(a, b, fromEnd) {
  const max = Math.min(a.length, b.length);
  let n = 0;
  while (n < max) {
    const ca = fromEnd ? a[a.length - 1 - n] : a[n];
    const cb = fromEnd ? b[b.length - 1 - n] : b[n];
    if (ca !== cb) break;
    n++;
  }
  return n;
}

/** Translate an offset in the whitespace-normalized text back to the raw text. */
function mapNormalizedOffset(text, target) {
  let seen = 0;
  let prevWasSpace = false;
  for (let i = 0; i < text.length; i++) {
    const isSpace = /\s/.test(text[i]);
    if (isSpace && prevWasSpace) continue; // collapsed away in normalize()
    if (seen === target) return i;
    seen++;
    prevWasSpace = isSpace;
  }
  return text.length;
}

/** Choose the best of several candidate offsets using prefix/suffix context. */
function pick(starts, len, status, text, anchor) {
  if (starts.length === 1) return { status, start: starts[0], end: starts[0] + len };
  const prefix = anchor.prefix || "";
  const suffix = anchor.suffix || "";
  let best = starts[0];
  let bestScore = -1;
  for (const s of starts) {
    const before = text.slice(Math.max(0, s - CONTEXT), s);
    const after = text.slice(s + len, s + len + CONTEXT);
    const score = overlap(before, prefix, true) + overlap(after, suffix, false);
    if (score > bestScore) {
      bestScore = score;
      best = s;
    }
  }
  return { status, start: best, end: best + len };
}

/**
 * Resolve an anchor against the page text.
 * Returns {status, start, end} with offsets into `text`, or -1/-1 when orphaned.
 */
export function resolveTextAnchor(text, anchor) {
  const miss = { status: "orphaned", start: -1, end: -1 };
  const quote = anchor && anchor.quote;
  if (!quote) return miss;

  // 1. Exact hits first: cheapest and most precise.
  const hits = allIndexesOf(text, quote);
  if (hits.length > 0) return pick(hits, quote.length, "attached", text, anchor);

  // 2. Fall back to whitespace-normalized matching so re-indented or re-wrapped
  //    markup does not orphan an otherwise identical quote.
  const nText = normalize(text);
  const nQuote = normalize(quote);
  const nHits = allIndexesOf(nText, nQuote);
  if (nHits.length > 0) {
    const mapped = nHits.map((n) => mapNormalizedOffset(text, n));
    const r = pick(mapped, nQuote.length, "attached", text, anchor);
    // The raw span may be longer than the normalized one when whitespace was
    // collapsed, so walk forward until the slice normalizes back to the quote.
    let end = r.start + nQuote.length;
    while (end < text.length && normalize(text.slice(r.start, end)) !== nQuote) end++;
    return { status: "attached", start: r.start, end };
  }

  // 3. Quote is gone. If the surrounding context survives, the comment still has
  //    a meaningful home: report drifted and point at the gap between them.
  const prefix = (anchor.prefix || "").trim();
  const suffix = (anchor.suffix || "").trim();
  if (prefix) {
    const p = text.indexOf(prefix);
    if (p !== -1) {
      // The prefix was trimmed so that re-wrapped markup still matches, which
      // leaves the separating whitespace out of it. Skip past it so the drifted
      // span starts on real content rather than a leading space.
      let from = p + prefix.length;
      while (from < text.length && /\s/.test(text[from])) from++;
      const s = suffix ? text.indexOf(suffix, from) : -1;
      const to = s !== -1 ? s : Math.min(text.length, from + quote.length);
      if (to > from) return { status: "drifted", start: from, end: to };
      return { status: "drifted", start: p, end: p + prefix.length };
    }
  }
  if (suffix) {
    const s = text.indexOf(suffix);
    if (s !== -1) return { status: "drifted", start: s, end: s + suffix.length };
  }
  return miss;
}
