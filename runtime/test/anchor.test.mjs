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
