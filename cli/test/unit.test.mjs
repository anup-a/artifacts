import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { parseArgs } from "../src/args.js";
import { resolveConfig, saveConfig, configPath } from "../src/config.js";
import { collectFiles, readLink, writeLink } from "../src/files.js";

function tmp() {
  return mkdtempSync(join(tmpdir(), "artifacts-cli-"));
}

test("parseArgs splits command, positionals and flags", () => {
  const a = parseArgs(["publish", "./site", "--id", "ar_1", "--title=Hi", "--json"]);
  assert.equal(a.command, "publish");
  assert.deepEqual(a.positional, ["./site"]);
  assert.deepEqual(a.flags, { id: "ar_1", title: "Hi", json: true });
});

test("parseArgs keeps text after -- as positional", () => {
  const a = parseArgs(["reply", "ar_1", "th_1", "--", "--not-a-flag"]);
  assert.deepEqual(a.positional, ["ar_1", "th_1", "--not-a-flag"]);
});

test("parseArgs rejects a value-less flag and a valued boolean", () => {
  assert.throws(() => parseArgs(["get", "--id"]), /--id needs a value/);
  assert.throws(() => parseArgs(["get", "--json=1"]), /--json does not take a value/);
});

test("resolveConfig prefers env over the config file", () => {
  const home = tmp();
  saveConfig({ token: "file-token", api: "https://file.example" }, { home });
  const cfg = resolveConfig({ env: { ARTIFACTS_TOKEN: "env-token" }, home });
  assert.deepEqual(cfg, { token: "env-token", api: "https://file.example" });
});

test("resolveConfig trims the trailing slash and fails when unconfigured", () => {
  const home = tmp();
  assert.deepEqual(resolveConfig({ env: { ARTIFACTS_TOKEN: "t", ARTIFACTS_API: "https://a.example/" }, home }), {
    token: "t",
    api: "https://a.example",
  });
  assert.throws(() => resolveConfig({ env: {}, home }), (e) => e.exit === 2 && e.code === "NOT_CONFIGURED");
});

test("saveConfig writes 0600 JSON", () => {
  const home = tmp();
  const path = saveConfig({ token: "t", api: "https://a.example/" }, { home });
  assert.equal(path, configPath(home));
  assert.deepEqual(JSON.parse(readFileSync(path, "utf8")), { token: "t", api: "https://a.example" });
  assert.equal(statSync(path).mode & 0o777, 0o600);
});

test("collectFiles publishes a lone html file as index.html", () => {
  const dir = tmp();
  writeFileSync(join(dir, "page.html"), "<h1>hi</h1>");
  const { files, dir: d } = collectFiles(join(dir, "page.html"));
  assert.equal(d, null);
  assert.equal(files.length, 1);
  assert.equal(files[0].path, "index.html");
  assert.equal(files[0].size, 11);
  assert.match(files[0].sha256, /^[0-9a-f]{64}$/);
});

test("collectFiles walks nested dirs and skips .git, node_modules and the link file", () => {
  const dir = tmp();
  writeFileSync(join(dir, "index.html"), "<h1>hi</h1>");
  writeFileSync(join(dir, ".artifacts.json"), "{}");
  mkdirSync(join(dir, "assets"));
  writeFileSync(join(dir, "assets", "app.css"), "body{}");
  mkdirSync(join(dir, ".git"));
  writeFileSync(join(dir, ".git", "HEAD"), "ref");
  mkdirSync(join(dir, "node_modules"));
  writeFileSync(join(dir, "node_modules", "x.js"), "x");
  const { files } = collectFiles(dir);
  assert.deepEqual(files.map((f) => f.path).sort(), ["assets/app.css", "index.html"]);
});

test("collectFiles rejects an empty directory and a missing path", () => {
  assert.throws(() => collectFiles(tmp()), /no files to publish/);
  assert.throws(() => collectFiles(join(tmp(), "nope")), /no such file or directory/);
});

test("readLink returns {} without a link file and round-trips through writeLink", () => {
  const dir = tmp();
  assert.deepEqual(readLink(dir), {});
  writeLink(dir, { id: "ar_1", api: "https://a.example" });
  assert.deepEqual(readLink(dir), { id: "ar_1", api: "https://a.example" });
});
