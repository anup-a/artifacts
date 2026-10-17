// Drives the real CLI with --local against the local server it starts. No Go
// server and no network: this is what someone without an account runs.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const CLI_DIR = dirname(dirname(fileURLToPath(import.meta.url)));
const BIN = join(CLI_DIR, "bin", "byagent.js");
const PORT = Number(process.env.LOCAL_TEST_PORT || 4799);
const BASE = `http://localhost:${PORT}`;
const DATA = mkdtempSync(join(tmpdir(), "byagent-local-"));
const HOME = mkdtempSync(join(tmpdir(), "byagent-home-"));

function run(args, { cwd = CLI_DIR } = {}) {
  const env = { ...process.env, HOME, BYAGENT_LOCAL_DIR: DATA, BYAGENT_LOCAL_PORT: String(PORT) };
  delete env.ARTIFACTS_TOKEN;
  delete env.ARTIFACTS_API;
  const r = spawnSync(process.execPath, [BIN, ...args], { cwd, encoding: "utf8", env });
  return { code: r.status, stdout: r.stdout.trim(), stderr: r.stderr.trim() };
}

function json(args, opts) {
  const r = run([...args, "--json"], opts);
  assert.equal(r.code, 0, `expected success from ${args.join(" ")}, got ${r.code}: ${r.stderr}`);
  return JSON.parse(r.stdout);
}

async function viewer(path, body) {
  const res = await fetch(BASE + path, body === undefined ? {} : {
    method: "POST", headers: { "Content-Type": "application/json", Origin: BASE }, body: JSON.stringify(body),
  });
  return { status: res.status, body: res.status === 204 ? null : await res.json() };
}

before(() => {
  assert.equal(json(["local", "status"]).running, false);
});

after(() => {
  run(["local", "stop"]);
});

let id;
let dir;

test("the first --local publish starts the server and serves the page with the comment panel", async () => {
  dir = mkdtempSync(join(tmpdir(), "byagent-page-"));
  writeFileSync(join(dir, "index.html"), "<!doctype html><html><head><title>t</title></head><body><h1>Plan v1</h1><p>Ship on Friday.</p></body></html>");
  mkdirSync(join(dir, "img"));
  writeFileSync(join(dir, "img", "a.svg"), "<svg xmlns='http://www.w3.org/2000/svg'/>");
  const pub = json(["publish", dir, "--title", "Rollout plan", "--local"]);
  id = pub.id;
  assert.equal(pub.version, 1);
  assert.equal(pub.files, 2);
  assert.equal(pub.url, `${BASE}/a/${id}/`);
  assert.equal(JSON.parse(readFileSync(join(dir, ".artifacts.json"), "utf8")).api, BASE);

  const page = await fetch(pub.url);
  const html = await page.text();
  assert.match(html, /<h1>Plan v1<\/h1>/);
  assert.match(html, new RegExp(`<script src="/_rt/comments.js" data-artifact="${id}" data-version="1"`));
  const rt = await fetch(`${BASE}/_rt/comments.js`);
  assert.equal(rt.status, 200);
  assert.equal((await fetch(`${pub.url}img/a.svg`)).headers.get("content-type"), "image/svg+xml");
  assert.equal(json(["local", "status"]).running, true);
  assert.match(await (await fetch(BASE + "/")).text(), /Rollout plan/);
});

test("a reader comment comes back through comments, reply, mark and resolve", async () => {
  const created = await viewer(`/c/${id}/comments`, {
    author_name: "Sam", body: "Friday is a holiday", anchor: { kind: "text", quote: "Ship on Friday." }, version: 1,
  });
  assert.equal(created.status, 201);
  const tid = created.body.id;

  const open = json(["comments", id, "--local"]);
  assert.equal(open.threads.length, 1);
  assert.equal(open.threads[0].comments[0].author_kind, "viewer");
  assert.equal(json(["get", id, "--local"]).open_comments, 1);

  json(["reply", id, tid, "Moving it to Monday", "--local"]);
  assert.equal(json(["mark", id, tid, "working", "--local"]).progress, "working");
  json(["resolve", id, tid, "--local"]);
  assert.equal(json(["comments", id, "--local"]).threads.length, 0);
  const all = json(["comments", id, "--all", "--local"]).threads[0];
  assert.equal(all.status, "resolved");
  assert.deepEqual(all.comments.map((c) => c.author_kind), ["viewer", "owner"]);

  const list = await viewer(`/c/${id}/comments?status=all`);
  assert.equal(list.body.threads[0].comments[1].body, "Moving it to Monday");
});

test("the viewer API refuses bad input and writes from other sites", async () => {
  assert.equal((await viewer(`/c/${id}/comments`, { author_name: "", body: "x", anchor: { kind: "text", quote: "q" }, version: 1 })).status, 400);
  assert.equal((await viewer(`/c/${id}/comments`, { author_name: "a", body: "x", anchor: { kind: "nope" }, version: 1 })).status, 400);
  const res = await fetch(`${BASE}/c/${id}/comments`, {
    method: "POST", headers: { "Content-Type": "application/json", Origin: "https://evil.example" },
    body: JSON.stringify({ author_name: "a", body: "x", anchor: { kind: "text", quote: "q" }, version: 1 }),
  });
  assert.equal(res.status, 403);
  const owner = await fetch(`${BASE}/api/artifacts/${id}`);
  assert.equal(owner.status, 401);
});

test("republishing makes v2; versions, diff, rollback and old-version links work", async () => {
  writeFileSync(join(dir, "index.html"), "<html><body><h1>Plan v2</h1></body></html>");
  writeFileSync(join(dir, "notes.txt"), "new");
  const v2 = json(["publish", dir, "--local"]);
  assert.equal(v2.id, id);
  assert.equal(v2.version, 2);
  assert.equal(v2.uploaded, 2);

  const vs = json(["versions", id, "--local"]);
  assert.deepEqual(vs.versions.map((v) => [v.version, v.current]), [[1, false], [2, true]]);
  const d = json(["diff", id, "1", "--local"]);
  assert.deepEqual([d.added.length, d.removed.length, d.changed.length, d.unchanged], [1, 0, 1, 1]);
  assert.match(await (await fetch(`${BASE}/a/${id}/v/1/`)).text(), /Plan v1/);
  const out = join(mkdtempSync(join(tmpdir(), "byagent-pull-")), "page");
  const p = json(["pull", id, out, "--at", "1", "--local"]);
  assert.equal(p.version, 1);
  assert.match(readFileSync(join(out, "index.html"), "utf8"), /Plan v1/);

  assert.equal(json(["rollback", id, "v1", "--local"]).previous_version, 2);
  assert.match(await (await fetch(`${BASE}/a/${id}/`)).text(), /Plan v1/);
  assert.equal(json(["version", "rm", id, "2", "--local"]).versions_count, 1);
});

test("a markdown file, --no-comments and hosted-only commands", async () => {
  const md = join(mkdtempSync(join(tmpdir(), "byagent-md-")), "notes.md");
  writeFileSync(md, "# Field notes\n\nHello.\n");
  const pub = json(["publish", md, "--no-comments", "--local"]);
  assert.equal(pub.comments_enabled, false);
  const html = await (await fetch(pub.url)).text();
  assert.match(html, /Field notes/);
  assert.doesNotMatch(html, /_rt\/comments\.js/);
  assert.equal((await viewer(`/c/${pub.id}/meta`)).status, 404);

  const share = run(["share", pub.id, "--local", "--json"]);
  assert.equal(share.code, 1);
  assert.equal(JSON.parse(share.stderr).error.code, "LOCAL_UNSUPPORTED");
  assert.equal(run(["publish", md, "--local", "--expires", "1h"]).code, 2);

  assert.equal(json(["list", "--local"]).artifacts.length, 2);
  json(["delete", pub.id, "--local"]);
  assert.equal(json(["list", "--local"]).artifacts.length, 1);
});

test("local stop ends the server", async () => {
  assert.equal(json(["local", "stop"]).running, false);
  for (let i = 0; i < 20 && json(["local", "status"]).running; i++) await new Promise((r) => setTimeout(r, 100));
  assert.equal(json(["local", "status"]).running, false);
  assert.ok(existsSync(join(DATA, "db.json")));
});
