# Comments Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `/_rt/comments.js` — the vanilla-JS runtime injected into every served artifact page that lets an anonymous viewer select text or point at an element, leave an anchored comment, see existing threads highlighted, reply, and switch versions.

**Architecture:** ES modules under `runtime/src/` bundled by esbuild into a single committed `runtime/dist/comments.js`, which `runtime/embed.go` already `go:embed`s. The committed bundle keeps Go builds Node-free; CI rebuilds and diffs it to prove the checked-in artifact matches source. All UI lives inside a closed-ish shadow root so the host page's CSS cannot reach it and vice versa. Anchor resolution is client-side per spec §6 — the server stores raw anchors and never interprets them.

**Tech Stack:** Vanilla ES2020 JavaScript (no runtime dependencies), esbuild (build only), node:test (unit), Playwright (e2e), Go 1.26 for the one server-side addition.

## Global Constraints

- **No runtime dependencies.** The bundle must not import any npm package. esbuild and Playwright are `devDependencies` only.
- **Bundle budget: 25 KB minified.** Spec §7. Task 2 adds a hard size gate that fails the build above 25600 bytes.
- **Fails closed.** If any `/c/*` request fails, the host page must still render completely. Every network call is wrapped; no unhandled rejection, no thrown error escapes into page scope.
- **Plain text only.** Comment bodies and author names from the API are already tag-stripped server-side, but the runtime MUST render them with `textContent`/`createTextNode`, never `innerHTML`. No exceptions.
- **Shadow root isolation.** All runtime DOM lives under one `<div id="artifacts-rt">` host attached to `document.body` with `attachShadow({mode:'open'})`. Styles go in a single `<style>` inside that root. The runtime never adds a global stylesheet and never mutates host-page styles except for the highlight wrappers described in Task 6.
- **localStorage namespacing.** Every key is `artifacts:<artifactId>:<name>` per spec §3. Wrap all access in try/catch (Safari private mode throws).
- **CSP compatibility.** Served pages set `script-src 'self' 'unsafe-inline'`. The runtime is same-origin so it loads, but it must not use `eval`, `new Function`, or inline `<script>` injection.
- **Anchor JSON shapes are fixed by spec §6** and must be produced exactly:
  - `{"kind":"text","quote":"…","prefix":"…","suffix":"…"}` — prefix/suffix are 32 chars each.
  - `{"kind":"element","css":"…","text":"…"}` — text trimmed, max 120 chars.
- **Anchor status vocabulary:** `attached` | `drifted` | `orphaned`. Reported via `POST /c/:id/comments/:thread/seen` only when the viewed version is the current one (the server enforces this too).
- Go additions follow existing repo conventions: `apierr` for errors, table-free focused tests in `internal/api/*_test.go`, `gofmt` clean.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/api/comments_viewer.go` (modify) | add `viewerMeta` handler |
| `internal/api/router.go` (modify) | register `GET /c/{id}/meta` |
| `runtime/package.json` (create) | devDeps + build/test scripts |
| `runtime/build.mjs` (create) | esbuild bundle + 25 KB size gate |
| `runtime/src/anchor.js` (create) | pure text-quote matching, no DOM |
| `runtime/src/dom.js` (create) | text-offset map, Range building, CSS path |
| `runtime/src/api.js` (create) | `/c/*` fetch wrappers, all fail-soft |
| `runtime/src/highlight.js` (create) | paint/clear anchor highlights |
| `runtime/src/panel.js` (create) | shadow-root side panel UI |
| `runtime/src/compose.js` (create) | selection button, popover, element-pick mode |
| `runtime/src/index.js` (create) | boot sequence, wiring, fail-closed guard |
| `runtime/dist/comments.js` (replace) | committed bundle, `go:embed`ed |
| `runtime/test/anchor.test.mjs` (create) | node:test units for anchor.js |
| `runtime/test/e2e/*.html` (create) | fixture pages |
| `runtime/test/e2e/comments.spec.mjs` (create) | Playwright e2e |
| `.github/workflows/ci.yml` (modify) | runtime job |

---

### Task 1: Viewer-facing version list (`GET /c/{id}/meta`)

The panel's version switcher (spec §7) needs to know which versions exist. `GET /api/artifacts/{id}/versions` requires a publisher key, which a viewer never has. Add the same information to the unauthenticated `/c/*` surface, where the artifact id is already the capability (thread bodies are exposed there too).

**Files:**
- Modify: `internal/api/comments_viewer.go` (append handler at end of file)
- Modify: `internal/api/router.go:34` (register route next to the other `/c/` routes)
- Test: `internal/api/comments_test.go` (append)

**Interfaces:**
- Consumes: `s.loadArtifactForViewer(r, id) (store.Artifact, *apierr.Error)`, `s.Store.Versions(ctx, artifactID) ([]store.Version, error)` — both exist.
- Produces: `GET /c/{id}/meta` → `200 {"current_version":3,"versions":[2,3]}`. Deleted artifact → `410`. Unknown id → `404`. The runtime (Task 5) consumes exactly this shape.

- [ ] **Step 1: Write the failing test**

Append to `internal/api/comments_test.go`:

```go
func TestViewerMeta(t *testing.T) {
	srv, _, tok := newTestServer(t, false)
	_, b := do(t, publishReq(t, "POST", srv.URL+"/api/artifacts", tok, "", []pubFile{{"index.html", "<p>v1</p>"}}, nil))
	id := b["id"].(string)
	do(t, publishReq(t, "PUT", srv.URL+"/api/artifacts/"+id, tok, "", []pubFile{{"index.html", "<p>v2</p>"}}, nil))

	code, m := do(t, jsonReq(t, "GET", srv.URL+"/c/"+id+"/meta", "", nil))
	if code != 200 {
		t.Fatalf("code %d: %v", code, m)
	}
	if m["current_version"].(float64) != 2 {
		t.Fatalf("current_version: %v", m)
	}
	vs := m["versions"].([]any)
	if len(vs) != 2 || vs[0].(float64) != 1 || vs[1].(float64) != 2 {
		t.Fatalf("versions: %v", vs)
	}

	// Unknown artifact is a 404, not a 500.
	if code, _ := do(t, jsonReq(t, "GET", srv.URL+"/c/nosuchid00000/meta", "", nil)); code != 404 {
		t.Fatalf("unknown id: %d", code)
	}

	// Deleted artifact is gone for viewers.
	do(t, authReq(t, "DELETE", srv.URL+"/api/artifacts/"+id, tok))
	if code, _ := do(t, jsonReq(t, "GET", srv.URL+"/c/"+id+"/meta", "", nil)); code != 410 {
		t.Fatalf("deleted: %d", code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd ~/orca/projects/artifacts && go test ./internal/api/ -run TestViewerMeta -v
```

Expected: FAIL — the route is unregistered, so the catch-all returns 404 and `current_version` assertion panics on a nil map entry.

- [ ] **Step 3: Write the handler**

Append to `internal/api/comments_viewer.go`:

```go
// viewerMeta exposes the version list to anonymous viewers so the runtime can
// render a version switcher. The artifact id is the capability here, exactly as
// it is for the thread list; no publisher-only fields are included.
func (s *server) viewerMeta(w http.ResponseWriter, r *http.Request) {
	a, e := s.loadArtifactForViewer(r, r.PathValue("id"))
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	vs, err := s.Store.Versions(r.Context(), a.ID)
	if err != nil {
		apierr.Write(w, r, apierr.Internal(err))
		return
	}
	ns := make([]int, 0, len(vs))
	for _, v := range vs {
		ns = append(ns, v.Version)
	}
	sort.Ints(ns)
	w.Header().Set("Cache-Control", "no-store")
	apierr.WriteJSON(w, 200, map[string]any{"current_version": a.CurrentVersion, "versions": ns})
}
```

Add `"sort"` to the import block in that file.

- [ ] **Step 4: Register the route**

In `internal/api/router.go`, immediately after the `GET /c/{id}/comments` line:

```go
	mux.HandleFunc("GET /c/{id}/meta", s.viewerMeta)
```

- [ ] **Step 5: Run test to verify it passes**

```bash
cd ~/orca/projects/artifacts && go test ./internal/api/ -run TestViewerMeta -v && gofmt -l internal/
```

Expected: PASS, and `gofmt -l` prints nothing.

- [ ] **Step 6: Document the route**

In `docs/superpowers/specs/2026-09-15-artifacts-service-design.md`, in the §4 API table, add immediately after the `GET /c/:id/comments` row:

```markdown
| `GET` | `/c/:id/meta` | Viewer. `{current_version, versions:[n]}` so the runtime can render a version switcher. |
```

Add the same row to the API table in `README.md`.

- [ ] **Step 7: Commit**

```bash
cd ~/orca/projects/artifacts
git add internal/api/comments_viewer.go internal/api/router.go internal/api/comments_test.go docs README.md
git commit -m "feat: GET /c/:id/meta exposes version list to viewers"
```

---

### Task 2: Runtime build pipeline

Establish esbuild bundling, the committed-dist convention, and the size gate before writing any runtime logic, so every later task has a working `npm run build`.

**Files:**
- Create: `runtime/package.json`, `runtime/build.mjs`, `runtime/.gitignore`
- Create: `runtime/src/index.js` (placeholder, real boot lands in Task 5)
- Replace: `runtime/dist/comments.js`

**Interfaces:**
- Produces: `npm --prefix runtime run build` writes `runtime/dist/comments.js`. Every later task ends by running it and committing the regenerated dist.

- [ ] **Step 1: Create `runtime/package.json`**

```json
{
  "name": "@artifacts/runtime",
  "private": true,
  "type": "module",
  "scripts": {
    "build": "node build.mjs",
    "test": "node --test test/*.test.mjs",
    "e2e": "playwright test"
  },
  "devDependencies": {
    "esbuild": "^0.24.0",
    "@playwright/test": "^1.48.0"
  }
}
```

- [ ] **Step 2: Create `runtime/build.mjs`**

```js
// Bundles src/index.js into the single file the Go binary embeds.
// dist/comments.js is COMMITTED so `go build` never needs Node; CI rebuilds it
// and fails if the checked-in copy drifted from source.
import { build } from "esbuild";
import { readFileSync } from "node:fs";

const LIMIT = 25600; // 25 KB, spec section 7

const result = await build({
  entryPoints: ["src/index.js"],
  bundle: true,
  minify: true,
  format: "iife",
  target: ["es2020"],
  outfile: "dist/comments.js",
  legalComments: "none",
});

if (result.errors.length) process.exit(1);

const bytes = readFileSync("dist/comments.js").byteLength;
console.log(`dist/comments.js  ${bytes} bytes  (limit ${LIMIT})`);
if (bytes > LIMIT) {
  console.error(`runtime bundle is ${bytes - LIMIT} bytes over the 25 KB budget`);
  process.exit(1);
}
```

- [ ] **Step 3: Create `runtime/.gitignore`**

```
node_modules/
test-results/
playwright-report/
```

- [ ] **Step 4: Create the placeholder entry `runtime/src/index.js`**

```js
// Entry point. Real boot sequence lands in Task 5.
export {};
```

- [ ] **Step 5: Install and build**

```bash
cd ~/orca/projects/artifacts/runtime && npm install && npm run build
```

Expected: prints a `dist/comments.js  NNN bytes  (limit 25600)` line and exits 0.

- [ ] **Step 6: Verify the Go embed still compiles**

```bash
cd ~/orca/projects/artifacts && go build ./... && go test ./internal/serve/
```

Expected: builds clean, serve tests pass (they assert the runtime body is served, not its content).

- [ ] **Step 7: Commit**

```bash
cd ~/orca/projects/artifacts
git add runtime/package.json runtime/package-lock.json runtime/build.mjs runtime/.gitignore runtime/src/index.js runtime/dist/comments.js
git commit -m "build: esbuild pipeline for the comments runtime with a 25 KB gate"
```

---

### Task 3: Pure anchor matching (`anchor.js`)

Spec §6 rule 1: exact `quote` found once → attached; found several times → pick the best prefix/suffix match; not found → fuzzy window → drifted; still nothing → orphaned. This is pure string work over the page's flattened text, so it is unit-testable with no DOM.

**Files:**
- Create: `runtime/src/anchor.js`
- Test: `runtime/test/anchor.test.mjs`

**Interfaces:**
- Produces:
  - `makeTextAnchor(text, start, end) → {kind:"text",quote,prefix,suffix}` — builds an anchor from a selection over the flattened page text.
  - `resolveTextAnchor(text, anchor) → {status:"attached"|"drifted"|"orphaned", start:number, end:number}` — `start`/`end` are offsets into `text`; both are `-1` when orphaned.
  - `CONTEXT = 32` — exported so `dom.js` and tests agree on the context length.
- Task 4 consumes both. Task 6 consumes `resolveTextAnchor`'s offsets.

- [ ] **Step 1: Write the failing test**

Create `runtime/test/anchor.test.mjs`:

```js
import { test } from "node:test";
import assert from "node:assert/strict";
import { makeTextAnchor, resolveTextAnchor, CONTEXT } from "../src/anchor.js";

test("makeTextAnchor captures quote with bounded context", () => {
  const text = "The quick brown fox jumps over the lazy dog";
  const start = text.indexOf("brown fox");
  const a = makeTextAnchor(text, start, start + "brown fox".length);
  assert.equal(a.kind, "text");
  assert.equal(a.quote, "brown fox");
  assert.equal(a.prefix, "The quick ");
  assert.equal(a.suffix, " jumps over the lazy dog");
  assert.ok(a.prefix.length <= CONTEXT);
  assert.ok(a.suffix.length <= CONTEXT);
});

test("unique quote attaches at the right offset", () => {
  const text = "alpha beta gamma";
  const r = resolveTextAnchor(text, { kind: "text", quote: "beta", prefix: "alpha ", suffix: " gamma" });
  assert.equal(r.status, "attached");
  assert.equal(r.start, 6);
  assert.equal(r.end, 10);
});

test("repeated quote disambiguates by context", () => {
  const text = "say hi to Ann. say hi to Bob.";
  const r = resolveTextAnchor(text, { kind: "text", quote: "say hi", prefix: "Ann. ", suffix: " to Bob" });
  assert.equal(r.status, "attached");
  assert.equal(r.start, text.lastIndexOf("say hi"));
});

test("repeated quote with no usable context still attaches to the first match", () => {
  const text = "one two one two";
  const r = resolveTextAnchor(text, { kind: "text", quote: "one", prefix: "", suffix: "" });
  assert.equal(r.status, "attached");
  assert.equal(r.start, 0);
});

test("quote gone but context intact drifts to the context window", () => {
  const text = "The quick brown cat jumps over the lazy dog";
  const r = resolveTextAnchor(text, {
    kind: "text",
    quote: "brown fox",
    prefix: "The quick ",
    suffix: " jumps over the lazy dog",
  });
  assert.equal(r.status, "drifted");
  assert.equal(r.start, 10);
  assert.ok(r.end > r.start);
});

test("quote and context both gone is orphaned", () => {
  const r = resolveTextAnchor("completely different content", {
    kind: "text",
    quote: "brown fox",
    prefix: "The quick ",
    suffix: " jumps",
  });
  assert.equal(r.status, "orphaned");
  assert.equal(r.start, -1);
  assert.equal(r.end, -1);
});

test("whitespace differences do not orphan an otherwise exact quote", () => {
  const r = resolveTextAnchor("hello    world again", {
    kind: "text",
    quote: "hello world",
    prefix: "",
    suffix: " again",
  });
  assert.notEqual(r.status, "orphaned");
});

test("empty quote is orphaned rather than matching everything", () => {
  const r = resolveTextAnchor("anything", { kind: "text", quote: "", prefix: "", suffix: "" });
  assert.equal(r.status, "orphaned");
});
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd ~/orca/projects/artifacts/runtime && npm test
```

Expected: FAIL — `Cannot find module '../src/anchor.js'`.

- [ ] **Step 3: Write `runtime/src/anchor.js`**

```js
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

/**
 * Resolve an anchor against the page text.
 * Returns {status, start, end} with offsets into `text`, or -1/-1 when orphaned.
 */
export function resolveTextAnchor(text, anchor) {
  const miss = { status: "orphaned", start: -1, end: -1 };
  const quote = anchor && anchor.quote;
  if (!quote) return miss;

  // 1. Exact hits first — cheapest and most precise.
  let hits = allIndexesOf(text, quote);
  let len = quote.length;

  // 2. Fall back to whitespace-normalized matching so re-indented or re-wrapped
  //    markup does not orphan an otherwise identical quote.
  if (hits.length === 0) {
    const nText = normalize(text);
    const nQuote = normalize(quote);
    const nHits = allIndexesOf(nText, nQuote);
    if (nHits.length > 0) {
      // Map the normalized offset back to a raw offset by counting characters.
      const mapped = nHits.map((n) => mapNormalizedOffset(text, n));
      return pick(mapped, nQuote.length, "attached", text, anchor);
    }
  }

  if (hits.length > 0) return pick(hits, len, "attached", text, anchor);

  // 3. Quote is gone. If the surrounding context survives, the comment still has
  //    a meaningful home: report drifted and point at the gap between them.
  const prefix = (anchor.prefix || "").trim();
  const suffix = (anchor.suffix || "").trim();
  if (prefix) {
    const p = text.indexOf(prefix);
    if (p !== -1) {
      const from = p + prefix.length;
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
```

- [ ] **Step 4: Run test to verify it passes**

```bash
cd ~/orca/projects/artifacts/runtime && npm test
```

Expected: all 8 tests pass.

- [ ] **Step 5: Rebuild and commit**

```bash
cd ~/orca/projects/artifacts/runtime && npm run build
cd ~/orca/projects/artifacts
git add runtime/src/anchor.js runtime/test/anchor.test.mjs runtime/dist/comments.js
git commit -m "feat(runtime): text anchor matching with drift and orphan detection"
```

---

### Task 4: DOM bridge (`dom.js`)

Turn the live page into the flat string `anchor.js` operates on, and turn offsets back into live `Range`s. Also implement the element-anchor half of spec §6 rule 2.

**Files:**
- Create: `runtime/src/dom.js`
- Test: exercised through Playwright in Task 9 (this module is pure DOM and cannot be node-tested without a DOM shim, which the no-dependency rule forbids).

**Interfaces:**
- Consumes: `makeTextAnchor`, `resolveTextAnchor`, `CONTEXT` from `./anchor.js`.
- Produces:
  - `pageText() → {text, nodes}` — `nodes` is `[{node, start, end}]` mapping flat offsets to text nodes.
  - `rangeFromOffsets(map, start, end) → Range|null`
  - `anchorFromSelection(sel) → anchor|null` — text anchor from a live `Selection`.
  - `anchorFromElement(el) → anchor` — `{kind:"element",css,text}`.
  - `resolveAnchor(anchor) → {status, range|element|null}` — the single entry point Task 6 and Task 7 call.
  - `cssPath(el) → string`
- Task 6 consumes `resolveAnchor`. Task 8 consumes `anchorFromSelection` and `anchorFromElement`.

- [ ] **Step 1: Write `runtime/src/dom.js`**

```js
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
```

- [ ] **Step 2: Verify it bundles**

```bash
cd ~/orca/projects/artifacts/runtime && npm run build
```

Expected: build succeeds and stays under the limit. (Behaviour is proven in Task 9's Playwright run; there is no node-testable surface here.)

- [ ] **Step 3: Commit**

```bash
cd ~/orca/projects/artifacts
git add runtime/src/dom.js runtime/dist/comments.js
git commit -m "feat(runtime): DOM bridge for text and element anchors"
```

---

### Task 5: API client and fail-closed boot

**Files:**
- Create: `runtime/src/api.js`
- Rewrite: `runtime/src/index.js`
- Test: `runtime/test/e2e/fixture-basic.html`, first Playwright spec

**Interfaces:**
- Produces from `api.js`: `createClient({artifactId, version, origin}) → {listThreads, createThread, reply, seen, meta}`. Every method resolves to `{ok:true, data}` or `{ok:false, error}` — none ever reject.
- Produces from `index.js`: the boot sequence. Reads `data-artifact` / `data-version` off its own `<script>` tag (`document.currentScript`), and exposes nothing on `window` except `window.__artifactsRuntime` (a small object used only by tests).
- Task 7 and Task 8 consume the client.

- [ ] **Step 1: Write `runtime/src/api.js`**

```js
// Thin wrappers over the /c/* viewer API. Every call is fail-soft: the host page
// must render correctly even when the comments backend is unreachable.

const TIMEOUT_MS = 8000;

async function request(url, options) {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), TIMEOUT_MS);
  try {
    const res = await fetch(url, { ...options, signal: ctrl.signal, credentials: "omit" });
    const text = await res.text();
    let body = null;
    try {
      body = text ? JSON.parse(text) : null;
    } catch {
      body = null;
    }
    if (!res.ok) {
      const message = (body && body.error && body.error.message) || `HTTP ${res.status}`;
      return { ok: false, error: message, status: res.status };
    }
    return { ok: true, data: body };
  } catch (err) {
    return { ok: false, error: err && err.name === "AbortError" ? "timed out" : "network error", status: 0 };
  } finally {
    clearTimeout(timer);
  }
}

function json(body) {
  return { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) };
}

export function createClient({ artifactId, version, origin = "" }) {
  const base = `${origin}/c/${encodeURIComponent(artifactId)}`;
  return {
    meta: () => request(`${base}/meta`, { method: "GET" }),
    listThreads: (status = "open") =>
      request(`${base}/comments?status=${encodeURIComponent(status)}`, { method: "GET" }),
    createThread: (author_name, body, anchor, idempotencyKey) => {
      const opts = json({ author_name, body, anchor, version });
      if (idempotencyKey) opts.headers["Idempotency-Key"] = idempotencyKey;
      return request(`${base}/comments`, opts);
    },
    reply: (threadId, author_name, body) =>
      request(`${base}/comments/${encodeURIComponent(threadId)}/reply`, json({ author_name, body })),
    seen: (threadId, anchor_status) =>
      request(`${base}/comments/${encodeURIComponent(threadId)}/seen`, json({ version, anchor_status })),
  };
}
```

- [ ] **Step 2: Rewrite `runtime/src/index.js`**

```js
// Boot sequence for the injected comments runtime.
//
// Hard rule: nothing here may break the host page. Every step is wrapped, and a
// failure leaves the page exactly as the publisher wrote it.
import { createClient } from "./api.js";
import { mountPanel } from "./panel.js";
import { mountCompose } from "./compose.js";
import { resolveAnchor } from "./dom.js";
import { paintThreads } from "./highlight.js";

const HOST_ID = "artifacts-rt";

function readConfig() {
  const el =
    document.currentScript ||
    document.querySelector('script[src*="/_rt/comments.js"][data-artifact]');
  if (!el) return null;
  const artifactId = el.getAttribute("data-artifact");
  const version = parseInt(el.getAttribute("data-version") || "0", 10);
  if (!artifactId || !Number.isFinite(version) || version <= 0) return null;
  return { artifactId, version };
}

function storage(artifactId) {
  const key = (name) => `artifacts:${artifactId}:${name}`;
  return {
    get(name) {
      try {
        return localStorage.getItem(key(name));
      } catch {
        return null;
      }
    },
    set(name, value) {
      try {
        localStorage.setItem(key(name), value);
      } catch {
        /* private mode: remembering the name is a nicety, not a requirement */
      }
    },
  };
}

async function boot() {
  const cfg = readConfig();
  if (!cfg) return;

  const host = document.createElement("div");
  host.id = HOST_ID;
  const shadow = host.attachShadow({ mode: "open" });
  document.body.appendChild(host);

  const client = createClient({ artifactId: cfg.artifactId, version: cfg.version, origin: "" });
  const store = storage(cfg.artifactId);

  const state = {
    threads: [],
    resolved: new Map(), // threadId -> {status, range, element}
    versions: [],
    currentVersion: cfg.version,
    unavailable: false,
  };

  const panel = mountPanel(shadow, { state, client, store, config: cfg, onRefresh: refresh });

  async function refresh() {
    const [list, meta] = await Promise.all([client.listThreads("open"), client.meta()]);
    if (meta.ok && meta.data) {
      state.versions = Array.isArray(meta.data.versions) ? meta.data.versions : [];
      state.currentVersion = meta.data.current_version || cfg.version;
    }
    if (!list.ok) {
      state.unavailable = true;
      state.threads = [];
      panel.render();
      return;
    }
    state.unavailable = false;
    state.threads = (list.data && list.data.threads) || [];
    state.resolved = new Map();
    for (const t of state.threads) {
      state.resolved.set(t.id, resolveAnchor(t.anchor));
    }
    paintThreads(state, (id) => panel.focusThread(id));
    panel.render();
    reportSeen();
  }

  // Tell the owner where each thread actually landed, so `artifacts comments`
  // shows live anchor health. Only meaningful on the current version; the server
  // ignores the beacon otherwise, and we skip it to save the request.
  function reportSeen() {
    if (cfg.version !== state.currentVersion) return;
    for (const t of state.threads) {
      const r = state.resolved.get(t.id);
      if (r && r.status !== t.anchor_status) client.seen(t.id, r.status);
    }
  }

  mountCompose(shadow, { state, client, store, config: cfg, onCreated: refresh, panel });

  await refresh();

  // Test hook: never relied upon by the UI itself.
  window.__artifactsRuntime = { state, refresh, client };
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", () => {
    boot().catch(() => {});
  });
} else {
  boot().catch(() => {});
}
```

- [ ] **Step 3: Create `runtime/test/e2e/fixture-basic.html`**

```html
<!doctype html>
<html>
  <head>
    <meta charset="utf-8" />
    <title>Fixture</title>
  </head>
  <body>
    <h1 id="title">Quarterly Report</h1>
    <main>
      <section>
        <p id="p1">The quick brown fox jumps over the lazy dog.</p>
        <p id="p2">Revenue grew by twelve percent this quarter.</p>
      </section>
    </main>
  </body>
</html>
```

- [ ] **Step 4: Create the Playwright config `runtime/playwright.config.mjs`**

```js
import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./test/e2e",
  timeout: 30000,
  use: { baseURL: process.env.ARTIFACTS_BASE || "http://127.0.0.1:8099" },
  reporter: [["list"]],
});
```

- [ ] **Step 5: Write the boot spec `runtime/test/e2e/comments.spec.mjs`**

This file grows across Tasks 5, 7, 8 and 9. Start with the harness plus the two boot tests.

```js
import { test, expect } from "@playwright/test";
import { spawn, execFileSync } from "node:child_process";
import { mkdtempSync, writeFileSync, readFileSync, mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const REPO = resolve(process.cwd(), "..");
const PORT = 8099;
const BASE = `http://127.0.0.1:${PORT}`;
const TOKEN = "e2e-admin-token";

let server;
let dataDir;

/** Publish a directory of files, returning the new artifact id. */
function publish(files, id) {
  const dir = mkdtempSync(join(tmpdir(), "artifacts-e2e-"));
  for (const [name, body] of Object.entries(files)) {
    writeFileSync(join(dir, name), body);
  }
  const args = ["run", "./scripts/e2e-publish.go", BASE, TOKEN, dir];
  if (id) args.push(id);
  const out = execFileSync("go", args, { cwd: REPO, encoding: "utf8" });
  return JSON.parse(out).id;
}

test.beforeAll(async () => {
  dataDir = mkdtempSync(join(tmpdir(), "artifacts-data-"));
  mkdirSync(join(dataDir, "blobs"), { recursive: true });
  execFileSync("go", ["build", "-o", join(dataDir, "server"), "./cmd/artifacts-server"], { cwd: REPO });
  server = spawn(join(dataDir, "server"), [], {
    env: {
      ...process.env,
      DB: "sqlite",
      STORAGE: "local",
      DATA_DIR: dataDir,
      ADMIN_TOKEN: TOKEN,
      PORT: String(PORT),
      PUBLIC_SHARE_ORIGIN: BASE,
      PUBLIC_API_ORIGIN: BASE,
    },
    stdio: "ignore",
  });
  for (let i = 0; i < 100; i++) {
    try {
      const res = await fetch(`${BASE}/healthz`);
      if (res.ok) return;
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error("server did not become healthy");
});

test.afterAll(() => {
  if (server) server.kill();
});

test("runtime boots and mounts a shadow-root panel", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await page.goto(`${BASE}/a/${id}/`);

  await expect(page.locator("#artifacts-rt")).toBeAttached();
  const hasShadow = await page.evaluate(
    () => !!document.getElementById("artifacts-rt").shadowRoot
  );
  expect(hasShadow).toBe(true);

  // The host page is untouched.
  await expect(page.locator("#p1")).toHaveText(
    "The quick brown fox jumps over the lazy dog."
  );
});

test("page still renders when the comments API is unreachable", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await page.route("**/c/**", (route) => route.abort());

  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));

  await page.goto(`${BASE}/a/${id}/`);
  await expect(page.locator("#p1")).toBeVisible();
  await expect(page.locator("#artifacts-rt")).toBeAttached();

  const label = await page.evaluate(() =>
    document.getElementById("artifacts-rt").shadowRoot.textContent
  );
  expect(label).toContain("unavailable");
  expect(errors).toEqual([]);
});
```

- [ ] **Step 6: Write the publish helper `scripts/e2e-publish.go`**

The Node CLI is plan 3; the e2e suite needs a publisher now. This is a `go run` script, not part of the built binary.

```go
//go:build ignore

// e2e-publish posts a directory to a running artifacts server.
// Usage: go run ./scripts/e2e-publish.go <base> <token> <dir> [artifact-id]
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: e2e-publish <base> <token> <dir> [id]")
		os.Exit(2)
	}
	base, token, dir := os.Args[1], os.Args[2], os.Args[3]
	id := ""
	if len(os.Args) > 4 {
		id = os.Args[4]
	}

	entries, err := os.ReadDir(dir)
	check(err)

	type mf struct {
		Path   string `json:"path"`
		Sha256 string `json:"sha256"`
		Size   int    `json:"size"`
	}
	var files []mf
	bodies := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		check(err)
		sum := sha256.Sum256(b)
		h := hex.EncodeToString(sum[:])
		files = append(files, mf{Path: e.Name(), Sha256: h, Size: len(b)})
		bodies[h] = b
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	manifest, _ := json.Marshal(map[string]any{"files": files})
	fw, err := w.CreateFormField("manifest")
	check(err)
	fw.Write(manifest)
	for h, b := range bodies {
		fw, err := w.CreateFormFile(h, h)
		check(err)
		fw.Write(b)
	}
	check(w.Close())

	method, url := http.MethodPost, base+"/api/artifacts"
	if id != "" {
		method, url = http.MethodPut, base+"/api/artifacts/"+id
	}
	req, err := http.NewRequest(method, url, &buf)
	check(err)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", w.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	check(err)
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "publish failed %d: %s\n", res.StatusCode, out)
		os.Exit(1)
	}
	os.Stdout.Write(out)
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

- [ ] **Step 7: Run the boot tests**

These need `panel.js`, `compose.js` and `highlight.js` to exist. Create minimal stubs now so the bundle links; Tasks 6-8 fill them in.

`runtime/src/highlight.js`:

```js
export function paintThreads() {}
export function clearHighlights() {}
```

`runtime/src/panel.js`:

```js
export function mountPanel(shadow, ctx) {
  const wrap = document.createElement("div");
  shadow.appendChild(wrap);
  return {
    render() {
      wrap.textContent = ctx.state.unavailable ? "comments unavailable" : "";
    },
    focusThread() {},
  };
}
```

`runtime/src/compose.js`:

```js
export function mountCompose() {}
```

Then:

```bash
cd ~/orca/projects/artifacts/runtime && npm run build && npx playwright install --with-deps chromium && npm run e2e
```

Expected: both tests PASS.

- [ ] **Step 8: Commit**

```bash
cd ~/orca/projects/artifacts
git add runtime/src scripts/e2e-publish.go runtime/playwright.config.mjs runtime/test/e2e runtime/dist/comments.js
git commit -m "feat(runtime): fail-soft API client and shadow-root boot"
```

---

### Task 6: Highlights

**Files:**
- Rewrite: `runtime/src/highlight.js`

**Interfaces:**
- Consumes: `state.threads`, `state.resolved` from Task 5.
- Produces: `paintThreads(state, onClick)` and `clearHighlights()`. Highlight elements carry `data-artifacts-thread="<id>"` so Playwright and the panel can find them.

- [ ] **Step 1: Write `runtime/src/highlight.js`**

```js
// Paints anchor highlights onto the host page.
//
// Text anchors are wrapped in a <mark> via Range.surroundContents where the range
// allows it; when it spans element boundaries we fall back to outlining the
// common ancestor, which is visually cruder but never corrupts the page.
const CLASS = "artifacts-hl";
const ATTR = "data-artifacts-thread";

const COLORS = {
  attached: "rgba(255, 213, 79, 0.45)",
  drifted: "rgba(255, 138, 101, 0.40)",
};

let styleEl = null;

function ensureStyle() {
  if (styleEl && styleEl.isConnected) return;
  styleEl = document.createElement("style");
  styleEl.setAttribute("data-artifacts", "highlight");
  styleEl.textContent = `
.${CLASS}{background:${COLORS.attached};cursor:pointer;border-radius:2px;
box-shadow:0 0 0 1px rgba(0,0,0,.06);transition:background .12s ease}
.${CLASS}[data-status="drifted"]{background:${COLORS.drifted};
outline:1px dashed rgba(191,54,12,.55)}
.${CLASS}:hover{background:rgba(255,193,7,.65)}
.${CLASS}[data-active="1"]{background:rgba(255,193,7,.85);
box-shadow:0 0 0 2px rgba(191,120,0,.55)}`;
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
    // The range crosses element boundaries; surroundContents refuses. Extract
    // and re-insert instead, which handles the multi-node case safely.
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
```

- [ ] **Step 2: Rebuild and commit**

```bash
cd ~/orca/projects/artifacts/runtime && npm run build
cd ~/orca/projects/artifacts
git add runtime/src/highlight.js runtime/dist/comments.js
git commit -m "feat(runtime): paint attached and drifted anchor highlights"
```

---

### Task 7: Side panel

**Files:**
- Rewrite: `runtime/src/panel.js`

**Interfaces:**
- Consumes: `state`, `client`, `store`, `config` from Task 5; `setActive` from `./highlight.js`.
- Produces: `mountPanel(shadow, ctx) → {render(), focusThread(id), open(), close(), setPickMode(on)}`. Task 8 calls `open()` and reads `pickMode`.

- [ ] **Step 1: Write `runtime/src/panel.js`**

```js
// The right-hand thread panel. Everything lives in the shadow root, so the host
// page's CSS cannot reach in and our styles cannot leak out.
import { setActive } from "./highlight.js";

const STYLE = `
:host{all:initial}
*{box-sizing:border-box;font-family:ui-sans-serif,system-ui,-apple-system,"Segoe UI",Roboto,sans-serif}
.launcher{position:fixed;right:16px;bottom:16px;z-index:2147483000;
border:0;border-radius:999px;padding:10px 16px;font-size:13px;font-weight:600;
background:#1f2328;color:#fff;cursor:pointer;box-shadow:0 2px 10px rgba(0,0,0,.28)}
.launcher:hover{background:#32383f}
.count{margin-left:6px;background:#ffd54f;color:#1f2328;border-radius:999px;
padding:1px 7px;font-size:11px}
.panel{position:fixed;top:0;right:0;height:100%;width:340px;z-index:2147483000;
background:#fff;color:#1f2328;border-left:1px solid #d8dee4;display:flex;
flex-direction:column;box-shadow:-2px 0 16px rgba(0,0,0,.12);
transform:translateX(100%);transition:transform .18s ease}
.panel[data-open="1"]{transform:translateX(0)}
header{display:flex;align-items:center;gap:8px;padding:12px 14px;
border-bottom:1px solid #e6eaef}
header h2{margin:0;font-size:13px;font-weight:600;flex:1}
select,button.ghost{font-size:12px;border:1px solid #d8dee4;border-radius:6px;
background:#fff;color:#1f2328;padding:4px 7px;cursor:pointer}
.list{flex:1;overflow:auto;padding:8px}
.empty{padding:24px 14px;color:#656d76;font-size:13px;text-align:center}
.thread{border:1px solid #e6eaef;border-radius:8px;padding:10px;margin-bottom:8px;
cursor:pointer;background:#fff}
.thread:hover{border-color:#b9c1ca}
.thread[data-active="1"]{border-color:#f0a500;box-shadow:0 0 0 2px rgba(255,193,7,.25)}
.badge{display:inline-block;font-size:10px;padding:1px 6px;border-radius:999px;
background:#eef1f4;color:#57606a;margin-left:6px}
.badge[data-s="drifted"]{background:#ffe0b2;color:#8a4b00}
.badge[data-s="orphaned"]{background:#f1f3f5;color:#8b949e}
.quote{font-size:11px;color:#656d76;border-left:2px solid #d8dee4;
padding-left:7px;margin:0 0 7px;overflow-wrap:anywhere}
.c{margin:6px 0;font-size:13px;overflow-wrap:anywhere}
.c .who{font-weight:600;font-size:11px;color:#424a53}
.c .who[data-kind="owner"]{color:#0a6a3a}
.reply{display:flex;gap:6px;margin-top:8px}
.reply input{flex:1;min-width:0;font-size:12px;padding:5px 7px;
border:1px solid #d8dee4;border-radius:6px;background:#fff;color:#1f2328}
.reply button{font-size:12px;padding:5px 9px;border:0;border-radius:6px;
background:#1f2328;color:#fff;cursor:pointer}
.section{font-size:11px;text-transform:uppercase;letter-spacing:.04em;
color:#8b949e;margin:12px 4px 6px}
.note{padding:10px 14px;font-size:12px;color:#8a4b00;background:#fff8e1;
border-bottom:1px solid #ffe0b2}
`;

function el(tag, props = {}, children = []) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k === "text") n.textContent = v; // always text, never markup
    else if (k === "class") n.className = v;
    else if (k.startsWith("on")) n.addEventListener(k.slice(2), v);
    else n.setAttribute(k, v);
  }
  for (const c of children) n.appendChild(c);
  return n;
}

function when(iso) {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : d.toLocaleString();
}

export function mountPanel(shadow, ctx) {
  const { state, client, store, config } = ctx;

  shadow.appendChild(el("style", { text: STYLE }));

  const launcher = el("button", { class: "launcher", type: "button", onclick: () => toggle() });
  const title = el("h2", { text: "Comments" });
  const versionSel = el("select", {
    "aria-label": "Version",
    onchange: (e) => {
      const v = e.target.value;
      const base = location.pathname.replace(/\/v\/\d+\//, "/");
      const m = base.match(/^\/a\/([^/]+)\//);
      if (!m) return;
      location.href =
        String(v) === String(state.currentVersion)
          ? `/a/${m[1]}/`
          : `/a/${m[1]}/v/${v}/`;
    },
  });
  const pickBtn = el("button", {
    class: "ghost",
    type: "button",
    text: "Point at element",
    onclick: () => setPickMode(!api.pickMode),
  });
  const closeBtn = el("button", { class: "ghost", type: "button", text: "Close", onclick: () => close() });
  const note = el("div", { class: "note" });
  const list = el("div", { class: "list" });
  const panel = el("div", { class: "panel", "data-open": "0" }, [
    el("header", {}, [title, versionSel, pickBtn, closeBtn]),
    note,
    list,
  ]);

  shadow.appendChild(launcher);
  shadow.appendChild(panel);

  function open() {
    panel.setAttribute("data-open", "1");
  }
  function close() {
    panel.setAttribute("data-open", "0");
  }
  function toggle() {
    panel.getAttribute("data-open") === "1" ? close() : open();
  }

  function setPickMode(on) {
    api.pickMode = on;
    pickBtn.textContent = on ? "Cancel pointing" : "Point at element";
    document.body.style.cursor = on ? "crosshair" : "";
  }

  function threadCard(t) {
    const r = state.resolved.get(t.id) || { status: "orphaned" };
    const anchor = t.anchor || {};
    const quoted = anchor.kind === "element" ? anchor.text : anchor.quote;

    const head = el("div", {}, [
      el("span", { class: "who", text: (t.comments[0] && t.comments[0].author_name) || "Anonymous" }),
      el("span", { class: "badge", "data-s": r.status, text: r.status }),
    ]);

    const body = [head];
    if (quoted) body.push(el("p", { class: "quote", text: quoted }));
    for (const c of t.comments) {
      body.push(
        el("div", { class: "c" }, [
          el("div", { class: "who", "data-kind": c.author_kind, text: `${c.author_name} · ${when(c.created_at)}` }),
          el("div", { text: c.body }),
        ])
      );
    }

    const input = el("input", { type: "text", placeholder: "Reply…", "aria-label": "Reply" });
    const send = el("button", {
      type: "button",
      text: "Reply",
      onclick: async (ev) => {
        ev.stopPropagation();
        const text = input.value.trim();
        if (!text) return;
        const name = store.get("name") || "Anonymous";
        send.disabled = true;
        const res = await client.reply(t.id, name, text);
        send.disabled = false;
        if (res.ok) {
          input.value = "";
          ctx.onRefresh();
        } else {
          note.textContent = `Could not post reply: ${res.error}`;
        }
      },
    });
    body.push(el("div", { class: "reply" }, [input, send]));

    const card = el(
      "div",
      {
        class: "thread",
        "data-thread": t.id,
        onclick: () => focusThread(t.id),
      },
      body
    );
    return card;
  }

  function focusThread(id) {
    open();
    setActive(id);
    for (const c of Array.from(list.querySelectorAll(".thread"))) {
      c.setAttribute("data-active", c.getAttribute("data-thread") === id ? "1" : "0");
    }
    const r = state.resolved.get(id);
    const target = r && (r.element || (r.range && r.range.startContainer.parentElement));
    if (target && target.scrollIntoView) target.scrollIntoView({ block: "center", behavior: "smooth" });
    const card = list.querySelector(`.thread[data-thread="${CSS.escape(id)}"]`);
    if (card) card.scrollIntoView({ block: "nearest" });
  }

  function renderVersions() {
    const vs = state.versions.length ? state.versions : [config.version];
    versionSel.replaceChildren();
    for (const v of vs) {
      versionSel.appendChild(
        el("option", {
          value: String(v),
          text: v === state.currentVersion ? `v${v} (current)` : `v${v}`,
        })
      );
    }
    versionSel.value = String(config.version);
    versionSel.style.display = vs.length > 1 ? "" : "none";
  }

  function render() {
    renderVersions();

    if (state.unavailable) {
      note.textContent = "Comments are unavailable right now.";
      note.style.display = "";
      list.replaceChildren(el("div", { class: "empty", text: "Comments unavailable." }));
      launcher.replaceChildren(document.createTextNode("Comments"));
      return;
    }

    note.style.display = config.version !== state.currentVersion ? "" : "none";
    if (config.version !== state.currentVersion) {
      note.textContent = `Viewing v${config.version}. The current version is v${state.currentVersion}.`;
    }

    const here = [];
    const earlier = [];
    for (const t of state.threads) {
      const r = state.resolved.get(t.id) || { status: "orphaned" };
      (r.status === "orphaned" ? earlier : here).push(t);
    }

    list.replaceChildren();
    if (here.length === 0 && earlier.length === 0) {
      list.appendChild(
        el("div", { class: "empty", text: "No comments yet. Select some text to leave one." })
      );
    }
    for (const t of here) list.appendChild(threadCard(t));
    if (earlier.length) {
      list.appendChild(el("div", { class: "section", text: "On earlier version" }));
      for (const t of earlier) list.appendChild(threadCard(t));
    }

    launcher.replaceChildren(document.createTextNode("Comments"));
    if (state.threads.length) {
      launcher.appendChild(el("span", { class: "count", text: String(state.threads.length) }));
    }
  }

  const api = { render, focusThread, open, close, setPickMode, pickMode: false };
  return api;
}
```

- [ ] **Step 2: Add the panel spec**

Append to `runtime/test/e2e/comments.spec.mjs`:

```js
test("panel lists an existing thread and shows its highlight", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });

  const res = await fetch(`${BASE}/c/${id}/comments`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      author_name: "Dana",
      body: "Is twelve percent right?",
      anchor: {
        kind: "text",
        quote: "twelve percent",
        prefix: "Revenue grew by ",
        suffix: " this quarter.",
      },
      version: 1,
    }),
  });
  expect(res.status).toBe(201);

  await page.goto(`${BASE}/a/${id}/`);

  const mark = page.locator("mark.artifacts-hl");
  await expect(mark).toHaveCount(1);
  await expect(mark).toHaveText("twelve percent");
  await expect(mark).toHaveAttribute("data-status", "attached");

  const shadow = page.locator("#artifacts-rt");
  await shadow.locator(".launcher").click();
  await expect(shadow.locator(".thread")).toHaveCount(1);
  await expect(shadow.locator(".thread")).toContainText("Is twelve percent right?");
  await expect(shadow.locator(".thread")).toContainText("Dana");
});

test("viewer can reply from the panel", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await fetch(`${BASE}/c/${id}/comments`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      author_name: "Dana",
      body: "First",
      anchor: { kind: "text", quote: "brown fox", prefix: "The quick ", suffix: " jumps" },
      version: 1,
    }),
  });

  await page.goto(`${BASE}/a/${id}/`);
  const shadow = page.locator("#artifacts-rt");
  await shadow.locator(".launcher").click();
  await shadow.locator(".reply input").fill("Second");
  await shadow.locator(".reply button").click();

  await expect(shadow.locator(".thread")).toContainText("Second");
});
```

- [ ] **Step 3: Run**

```bash
cd ~/orca/projects/artifacts/runtime && npm run build && npm run e2e
```

Expected: all specs PASS.

- [ ] **Step 4: Commit**

```bash
cd ~/orca/projects/artifacts
git add runtime/src/panel.js runtime/test/e2e/comments.spec.mjs runtime/dist/comments.js
git commit -m "feat(runtime): shadow-root thread panel with replies and version switcher"
```

---

### Task 8: Compose flow

**Files:**
- Rewrite: `runtime/src/compose.js`

**Interfaces:**
- Consumes: `anchorFromSelection`, `anchorFromElement` from `./dom.js`; `panel.open()`, `panel.pickMode`, `panel.setPickMode` from Task 7.
- Produces: `mountCompose(shadow, ctx)`. No return value; it wires document-level listeners.

- [ ] **Step 1: Write `runtime/src/compose.js`**

```js
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
```

- [ ] **Step 2: Add the compose spec**

Append to `runtime/test/e2e/comments.spec.mjs`:

```js
/** Select the text of an element by its id, in the page. */
async function selectText(page, selector) {
  await page.evaluate((sel) => {
    const node = document.querySelector(sel).firstChild;
    const r = document.createRange();
    r.setStart(node, 0);
    r.setEnd(node, node.nodeValue.length);
    const s = window.getSelection();
    s.removeAllRanges();
    s.addRange(r);
  }, selector);
  const box = await page.locator(selector).boundingBox();
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.up();
}

test("select text, comment, and see it persist after reload", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await page.goto(`${BASE}/a/${id}/`);

  await selectText(page, "#p2");

  const shadow = page.locator("#artifacts-rt");
  await shadow.locator(".fab").click();
  await shadow.locator(".pop input").fill("Ravi");
  await shadow.locator(".pop textarea").fill("Check this number.");
  await shadow.locator(".pop .send").click();

  await expect(shadow.locator(".thread")).toContainText("Check this number.");

  // Survives a reload, with a highlight and a remembered name.
  await page.reload();
  await expect(page.locator("mark.artifacts-hl")).toHaveCount(1);
  await shadow.locator(".launcher").click();
  await expect(shadow.locator(".thread")).toContainText("Ravi");

  await selectText(page, "#p1");
  await shadow.locator(".fab").click();
  await expect(shadow.locator(".pop input")).toHaveValue("Ravi");
});

test("element pick mode anchors to an element", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await page.goto(`${BASE}/a/${id}/`);

  const shadow = page.locator("#artifacts-rt");
  await shadow.locator(".launcher").click();
  await shadow.locator(".ghost", { hasText: "Point at element" }).click();
  await page.locator("#title").click();

  await shadow.locator(".pop input").fill("Sam");
  await shadow.locator(".pop textarea").fill("Rename this heading.");
  await shadow.locator(".pop .send").click();

  await expect(shadow.locator(".thread")).toContainText("Rename this heading.");
  await expect(page.locator('#title[data-artifacts-thread]')).toHaveCount(1);
});
```

- [ ] **Step 3: Run**

```bash
cd ~/orca/projects/artifacts/runtime && npm run build && npm run e2e
```

Expected: all specs PASS.

- [ ] **Step 4: Commit**

```bash
cd ~/orca/projects/artifacts
git add runtime/src/compose.js runtime/test/e2e/comments.spec.mjs runtime/dist/comments.js
git commit -m "feat(runtime): selection and element comment composition"
```

---

### Task 9: Drift, orphan, and dark-page coverage

Spec §11 requires: "republish with changed text → drifted and orphaned states; dark-page fixture".

**Files:**
- Create: `runtime/test/e2e/fixture-dark.html`
- Modify: `runtime/test/e2e/comments.spec.mjs`

**Interfaces:** none new; this task only adds coverage.

- [ ] **Step 1: Create `runtime/test/e2e/fixture-dark.html`**

```html
<!doctype html>
<html>
  <head>
    <meta charset="utf-8" />
    <title>Dark Fixture</title>
    <style>
      body {
        background: #0d1117;
        color: #e6edf3;
        font-family: ui-sans-serif, system-ui, sans-serif;
        margin: 0;
        padding: 32px;
      }
      mark { color: inherit; }
    </style>
  </head>
  <body>
    <h1 id="title">Dark Report</h1>
    <p id="p1">Latency dropped to forty milliseconds after the rollout.</p>
  </body>
</html>
```

- [ ] **Step 2: Add the drift, orphan, and dark specs**

Append to `runtime/test/e2e/comments.spec.mjs`:

```js
test("republishing changed text drifts the anchor", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await fetch(`${BASE}/c/${id}/comments`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      author_name: "Dana",
      body: "Fox or cat?",
      anchor: {
        kind: "text",
        quote: "brown fox",
        prefix: "The quick ",
        suffix: " jumps over the lazy dog.",
      },
      version: 1,
    }),
  });

  // v2 keeps the surrounding sentence but replaces the quoted words.
  publish({ "index.html": html.replace("brown fox", "brown cat") }, id);

  await page.goto(`${BASE}/a/${id}/`);
  const shadow = page.locator("#artifacts-rt");
  await shadow.locator(".launcher").click();
  await expect(shadow.locator(".badge")).toHaveText("drifted");
  await expect(page.locator('mark.artifacts-hl[data-status="drifted"]')).toHaveCount(1);
});

test("replacing the page orphans the anchor and files it under earlier version", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await fetch(`${BASE}/c/${id}/comments`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      author_name: "Dana",
      body: "Gone soon",
      anchor: {
        kind: "text",
        quote: "brown fox",
        prefix: "The quick ",
        suffix: " jumps over the lazy dog.",
      },
      version: 1,
    }),
  });

  publish(
    { "index.html": "<!doctype html><html><body><p>Entirely different copy.</p></body></html>" },
    id
  );

  await page.goto(`${BASE}/a/${id}/`);
  const shadow = page.locator("#artifacts-rt");
  await shadow.locator(".launcher").click();
  await expect(shadow.locator(".section")).toHaveText("On earlier version");
  await expect(shadow.locator(".badge")).toHaveText("orphaned");
  await expect(page.locator("mark.artifacts-hl")).toHaveCount(0);
});

test("pinned old version is viewable and flagged", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  publish({ "index.html": html.replace("Quarterly Report", "Annual Report") }, id);

  await page.goto(`${BASE}/a/${id}/v/1/`);
  await expect(page.locator("#title")).toHaveText("Quarterly Report");

  const shadow = page.locator("#artifacts-rt");
  await shadow.locator(".launcher").click();
  await expect(shadow.locator(".note")).toContainText("Viewing v1");
  await expect(shadow.locator("select")).toBeVisible();
});

test("panel is legible on a dark page and does not leak styles", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-dark.html", "utf8");
  const id = publish({ "index.html": html });
  await page.goto(`${BASE}/a/${id}/`);

  const shadow = page.locator("#artifacts-rt");
  await shadow.locator(".launcher").click();

  const panelBg = await page.evaluate(() => {
    const p = document.getElementById("artifacts-rt").shadowRoot.querySelector(".panel");
    return getComputedStyle(p).backgroundColor;
  });
  expect(panelBg).toBe("rgb(255, 255, 255)");

  // The host page keeps its own colours.
  const bodyColor = await page.evaluate(() => getComputedStyle(document.body).color);
  expect(bodyColor).toBe("rgb(230, 237, 243)");
});

test("seen beacon reports anchor status back to the owner API", async ({ page }) => {
  const html = readFileSync("./test/e2e/fixture-basic.html", "utf8");
  const id = publish({ "index.html": html });
  await fetch(`${BASE}/c/${id}/comments`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      author_name: "Dana",
      body: "status check",
      anchor: { kind: "text", quote: "brown fox", prefix: "The quick ", suffix: " jumps" },
      version: 1,
    }),
  });

  await page.goto(`${BASE}/a/${id}/`);
  await expect(page.locator("mark.artifacts-hl")).toHaveCount(1);

  await expect
    .poll(async () => {
      const res = await fetch(`${BASE}/api/artifacts/${id}/comments`, {
        headers: { Authorization: `Bearer ${TOKEN}` },
      });
      const body = await res.json();
      return body.threads[0].anchor_status;
    })
    .toBe("attached");
});
```

- [ ] **Step 3: Run the whole suite**

```bash
cd ~/orca/projects/artifacts/runtime && npm run build && npm test && npm run e2e
```

Expected: node units and all Playwright specs PASS.

- [ ] **Step 4: Commit**

```bash
cd ~/orca/projects/artifacts
git add runtime/test/e2e runtime/dist/comments.js
git commit -m "test(runtime): drift, orphan, pinned-version and dark-page coverage"
```

---

### Task 10: CI wiring and docs

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `README.md`
- Modify: `scripts/smoke.sh`

**Interfaces:** none new.

- [ ] **Step 1: Add the runtime job to `.github/workflows/ci.yml`**

Insert as a new top-level job after the `go` job:

```yaml
  runtime:
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: runtime
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with:
          node-version: "22"
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - run: npm ci
      - run: npm test
      - name: build and verify the committed bundle is current
        run: |
          npm run build
          if ! git diff --quiet -- dist/comments.js; then
            echo "dist/comments.js is stale; run 'npm --prefix runtime run build' and commit"
            git diff --stat -- dist/comments.js
            exit 1
          fi
      - run: npx playwright install --with-deps chromium
      - run: npm run e2e
```

Then make the `image` job wait for both:

```yaml
  image:
    needs: [go, runtime]
```

- [ ] **Step 2: Extend `scripts/smoke.sh`**

After the existing check that the runtime script tag is injected, assert the served runtime is the real bundle rather than the placeholder. Add immediately after that check:

```bash
# The embedded runtime must be the real build, not the placeholder.
rt=$(curl -fsS "$BASE/_rt/comments.js")
case "$rt" in
  *placeholder*) echo "FAIL: server is serving the runtime placeholder"; exit 1 ;;
esac
if [ "${#rt}" -lt 2000 ]; then
  echo "FAIL: runtime bundle looks truncated (${#rt} bytes)"
  exit 1
fi
echo "runtime bundle served: ${#rt} bytes"
```

- [ ] **Step 3: Document the runtime in `README.md`**

Add a section after the API table:

```markdown
## Comments runtime

Every served HTML page gets `<script src="/_rt/comments.js" data-artifact="…" data-version="…" defer>`
injected before `</body>`. The runtime is vanilla JS with no dependencies, lives
entirely in a shadow root, and fails closed — if the comments API is unreachable
the page renders exactly as published.

Viewers can select text or point at an element to leave an anchored comment under
a display name; no account is involved. Anchors are re-resolved in the browser on
every load and reported back as `attached`, `drifted` or `orphaned`, which is what
`GET /api/artifacts/:id/comments` shows the publisher.

Working on it:

```bash
cd runtime
npm install
npm test        # node:test units for anchor matching
npm run build   # esbuild -> dist/comments.js (committed, 25 KB budget)
npm run e2e     # Playwright against a real server
```

`runtime/dist/comments.js` is committed so `go build` never needs Node. CI rebuilds
it and fails if the checked-in copy is stale.
```

- [ ] **Step 4: Verify everything together**

```bash
cd ~/orca/projects/artifacts
go vet ./... && test -z "$(gofmt -l .)" && CGO_ENABLED=0 go test ./... && ./scripts/smoke.sh
cd runtime && npm test && npm run build && npm run e2e
```

Expected: all green, `SMOKE OK` printed, and the smoke script reports a runtime bundle of several thousand bytes.

- [ ] **Step 5: Commit**

```bash
cd ~/orca/projects/artifacts
git add .github/workflows/ci.yml README.md scripts/smoke.sh
git commit -m "ci: run runtime units, bundle freshness check and Playwright e2e"
```

---

## Self-Review

**Spec coverage (§6, §7, §11):**

| Spec requirement | Task |
|---|---|
| §6 text anchor: exact → attached, several → best context, fuzzy → drifted, none → orphaned | 3 |
| §6 element anchor: css+text → attached, unique text → drifted, else orphaned | 4 |
| §6 `seen` beacon reports status | 5 (`reportSeen`), 9 (test) |
| §6 orphans listed under "On earlier version", never dropped | 7, 9 |
| §7 selection → floating Comment button → popover | 8 |
| §7 name remembered in `localStorage` under `artifacts:<id>:name` | 5 (`storage`), 8, 9 |
| §7 click-to-comment element mode | 7 (toggle), 8 (handler) |
| §7 highlights for attached/drifted | 6 |
| §7 side panel: threads, replies, resolve state | 7 |
| §7 viewer replies inline | 7 |
| §7 version switcher when >1 version | 1 (data), 7 (UI), 9 (test) |
| §7 light and dark pages, shadow root | 7 (`:host{all:initial}`), 9 (dark fixture) |
| §7 fails closed, "comments unavailable" | 5, 7, and the Task 5 abort test |
| §7 under 25 KB minified | 2 (build gate) |
| §11 select → comment → reload → highlight present | 8 |
| §11 republish changed text → drifted and orphaned | 9 |
| §11 dark-page fixture | 9 |

**Known gaps, deliberately deferred:** resolved threads are not rendered (the viewer list defaults to `status=open`, and the panel offers no resolve control — resolving is a publisher action per spec §4). Real-time updates are out of scope per §12.

**Type consistency check:** `resolveAnchor` returns `{status, range, element}` in Task 4 and is destructured with those exact names in Tasks 5, 6, and 7. `createClient` methods return `{ok, data, error}` in Task 5 and are checked with `.ok` / `.error` in Tasks 7 and 8. `mountPanel` returns `{render, focusThread, open, close, setPickMode, pickMode}` in Task 7; Task 5 calls `render` and `focusThread`, Task 8 calls `open`, `setPickMode` and reads `pickMode`. `CONTEXT` is exported from `anchor.js` (Task 3) and imported nowhere else, but is asserted in the Task 3 tests.
