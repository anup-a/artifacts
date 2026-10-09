import test from "node:test";
import assert from "node:assert/strict";
import { relativeTime, avatarHue, initial } from "../src/format.js";

const NOW = Date.parse("2026-09-16T12:00:00Z");

test("relativeTime compresses to the largest sensible unit", () => {
  assert.equal(relativeTime("2026-09-16T11:59:40Z", NOW, "en"), "now");
  assert.equal(relativeTime("2026-09-16T11:40:00Z", NOW, "en"), "20m");
  assert.equal(relativeTime("2026-09-16T10:00:00Z", NOW, "en"), "2h");
  assert.equal(relativeTime("2026-09-13T12:00:00Z", NOW, "en"), "3d");
  assert.equal(relativeTime("2026-07-01T12:00:00Z", NOW, "en"), "Jul 1");
  assert.equal(relativeTime("2025-07-01T12:00:00Z", NOW, "en"), "Jul 2025");
  assert.equal(relativeTime("not a date", NOW, "en"), "");
});

test("relativeTime speaks the reader's language", () => {
  assert.equal(relativeTime("2026-09-16T11:40:00Z", NOW, "zh-CN"), "20分钟前");
  assert.equal(relativeTime("2026-09-16T10:00:00Z", NOW, "zh-CN"), "2小时前");
  assert.equal(relativeTime("2026-07-01T12:00:00Z", NOW, "zh-CN"), "7月1日");
  assert.equal(relativeTime("2025-07-01T12:00:00Z", NOW, "zh-CN"), "2025年7月");
  assert.equal(relativeTime("2026-09-16T11:40:00Z", NOW, "de"), "vor 20 Min.");
  assert.equal(relativeTime("2026-07-01T12:00:00Z", NOW, "de"), "1. Juli");
  assert.ok(relativeTime("2026-09-16T11:59:40Z", NOW, "ja"));
});

test("avatarHue is stable per name and spread across the palette", () => {
  assert.equal(avatarHue("Dana"), avatarHue("Dana"));
  const hues = new Set(["Dana", "Priya", "Lee", "Anup", "Sam", "Ravi"].map(avatarHue));
  assert.ok(hues.size >= 4);
  for (const h of hues) assert.ok(h >= 0 && h < 6);
});

test("initial takes the first letter, upper-cased, with a fallback", () => {
  assert.equal(initial("dana"), "D");
  assert.equal(initial("  priya"), "P");
  assert.equal(initial(""), "?");
  assert.equal(initial(undefined), "?");
});
