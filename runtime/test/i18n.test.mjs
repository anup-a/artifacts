import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { LANGS, ROWS, pickLang, tl, format } from "../src/i18n.js";

test("pickLang maps browser tags to the nine languages", () => {
  for (const tag of ["zh", "zh-CN", "zh-Hans", "zh-SG", "zh-Hans-HK", "zh_CN"]) assert.equal(pickLang([tag]), "zh-CN", tag);
  for (const tag of ["zh-TW", "zh-HK", "zh-MO", "zh-Hant", "zh-Hant-CN"]) assert.equal(pickLang([tag]), "zh-TW", tag);
  assert.equal(pickLang(["pt"]), "pt-BR");
  assert.equal(pickLang(["pt-PT"]), "pt-BR");
  assert.equal(pickLang(["es-MX"]), "es");
  assert.equal(pickLang(["de-AT"]), "de");
  assert.equal(pickLang(["ja-JP"]), "ja");
  assert.equal(pickLang(["ko-KR"]), "ko");
  assert.equal(pickLang(["fr-CA"]), "fr");
  // The first language we speak wins.
  assert.equal(pickLang(["nl", "zh-TW", "en"]), "zh-TW");
  assert.equal(pickLang(["en-GB", "zh-CN"]), "en");
  assert.equal(pickLang(["ru"]), "en");
  assert.equal(pickLang([]), "en");
  assert.equal(pickLang(undefined), "en");
});

test("every row has all nine languages, non-empty, with the English %s count", () => {
  const verbs = (s) => (s.match(/%s/g) || []).length;
  const keys = new Set();
  for (const row of ROWS) {
    assert.equal(row.length, LANGS.length, row[0]);
    assert.ok(!keys.has(row[0]), `duplicate key ${row[0]}`);
    keys.add(row[0]);
    for (const [i, s] of row.entries()) {
      assert.ok(typeof s === "string" && s.trim(), `${LANGS[i]}: ${row[0]}`);
      assert.equal(verbs(s), verbs(row[0]), `${LANGS[i]}: ${row[0]}`);
      assert.ok(!s.includes("—"), `no em dash: ${s}`);
    }
  }
});

test("every t() message in src has a row", () => {
  const keys = new Set(ROWS.map((r) => r[0]));
  const dir = new URL("../src/", import.meta.url);
  for (const name of readdirSync(dir)) {
    const src = readFileSync(new URL(name, dir), "utf8");
    // t("…"), t(cond ? "…" : "…"), tn(n, "…", "…") and the BADGE/PROGRESS tables.
    for (const m of src.matchAll(/\bt\(([^)]*)\)|\btn\(([^)]*)\)/g)) {
      // Literals after === are conditions (filter === "open"), not messages.
      for (const lit of (m[1] ?? m[2] ?? "").replace(/=== "[^"]*"/g, "").matchAll(/"((?:[^"\\]|\\.)*)"/g)) {
        assert.ok(keys.has(lit[1]), `${name}: missing row for "${lit[1]}"`);
      }
    }
  }
  for (const k of ["Moved", "Earlier version", "Seen", "Working on it", "Declined", "Anonymous", "Suggested edit", "timed out", "network error"]) {
    assert.ok(keys.has(k), k);
  }
});

test("tl translates, substitutes and falls back to English", () => {
  assert.equal(tl("zh-CN", "Comments"), "评论");
  assert.equal(tl("zh-TW", "Comments"), "留言");
  assert.equal(tl("zh-CN", "Reply as %s…", "Dana"), "以 Dana 的身份回复…");
  assert.equal(tl("en", "Reply as %s…", "Dana"), "Reply as Dana…");
  assert.equal(tl("de", "Viewing v%s. The current version is v%s.", 2, 3), "Du siehst v2. Die aktuelle Version ist v3.");
  assert.equal(tl("fr", "Not a known message"), "Not a known message");
  assert.equal(tl("en", "[send] Comment"), "Comment");
  assert.equal(tl("de", "[send] Comment"), "Senden");
  assert.equal(format("%s and %s", ["a"]), "a and %s");
});
