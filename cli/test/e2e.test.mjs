// Drives the real CLI binary against a real compiled Go server. Nothing here is
// mocked: every assertion is about what the server actually stored.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn, spawnSync, execFileSync } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const CLI_DIR = dirname(dirname(fileURLToPath(import.meta.url)));
const REPO = dirname(CLI_DIR);
const BIN = join(CLI_DIR, "bin", "artifacts.js");
const PORT = 8123;
const BASE = `http://127.0.0.1:${PORT}`;
const TOKEN = "e2e-token";

let server;

function tmp(prefix) {
  return mkdtempSync(join(tmpdir(), prefix));
}

// run returns {code, stdout, stderr}; a failing command is a normal result here
// because several tests assert on the error envelope.
function run(args, { cwd = CLI_DIR, input } = {}) {
  const r = spawnSync(process.execPath, [BIN, ...args], {
    cwd,
    input,
    encoding: "utf8",
    env: { ...process.env, ARTIFACTS_TOKEN: TOKEN, ARTIFACTS_API: BASE },
  });
  return { code: r.status, stdout: r.stdout.trim(), stderr: r.stderr.trim() };
}

function json(args, opts) {
  const r = run([...args, "--json"], opts);
  assert.equal(r.code, 0, `expected success from ${args.join(" ")}, got ${r.code}: ${r.stderr}`);
  return JSON.parse(r.stdout);
}

before(async () => {
  const dir = tmp("artifacts-e2e-");
  const bin = join(dir, "server");
  execFileSync("go", ["build", "-o", bin, "./cmd/artifacts-server"], { cwd: REPO, stdio: "inherit" });
  server = spawn(bin, {
    stdio: "ignore",
    env: {
      ...process.env,
      DB: "sqlite",
      STORAGE: "local",
      DATA_DIR: dir,
      ADMIN_TOKEN: TOKEN,
      KEY_SIGNING_SECRET: "",
      EMAIL_PROVIDER: "",
      GITHUB_CLIENT_ID: "",
      GITHUB_CLIENT_SECRET: "",
      ADDR: `127.0.0.1:${PORT}`,
      PUBLIC_SHARE_ORIGIN: BASE,
      PUBLIC_API_ORIGIN: BASE,
      // The suite publishes and comments far faster than a human would; the
      // rate limiter itself is covered by the Go tests.
      LIMIT_PUBLISH_PER_MIN: "1000",
      LIMIT_COMMENTS_PER_MIN: "1000",
    },
  });
  for (let i = 0; i < 100; i++) {
    try {
      const res = await fetch(`${BASE}/healthz`);
      if (res.ok) return;
    } catch {
      // not listening yet
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error("server did not become healthy");
});

after(() => {
  if (server) server.kill();
});

function siteDir(body = "<h1>hello</h1>") {
  const dir = tmp("artifacts-site-");
  writeFileSync(join(dir, "index.html"), `<!doctype html><html><body>${body}</body></html>`);
  mkdirSync(join(dir, "assets"));
  writeFileSync(join(dir, "assets", "app.css"), "body{margin:0}");
  return dir;
}

test("publish a directory, then republish it in place", async () => {
  const dir = siteDir();
  const first = json(["publish", dir, "--title", "E2E"]);
  assert.match(first.id, /^[0-9A-Za-z]{8,}$/);
  assert.equal(first.version, 1);
  assert.equal(first.files, 2);
  assert.equal(first.uploaded, 2);
  assert.equal(first.url, `${BASE}/a/${first.id}/`);
  assert.equal(first.end_user, null);

  // The link file is what makes the next publish an update rather than a new artifact.
  assert.deepEqual(JSON.parse(readFileSync(join(dir, ".artifacts.json"), "utf8")), { id: first.id, api: BASE });

  writeFileSync(join(dir, "index.html"), "<!doctype html><html><body><h1>hello again</h1></body></html>");
  const second = json(["publish", dir]);
  assert.equal(second.id, first.id);
  assert.equal(second.version, 2);
  // Only the changed file is uploaded; the unchanged stylesheet is already stored.
  assert.equal(second.uploaded, 1);

  const page = await (await fetch(`${BASE}/a/${first.id}/`)).text();
  assert.match(page, /hello again/);
});

test("publish a single html file as index.html", async () => {
  const dir = tmp("artifacts-single-");
  const file = join(dir, "page.html");
  writeFileSync(file, "<!doctype html><html><body><h1>single</h1></body></html>");
  const res = json(["publish", file]);
  // A single file has no directory to remember, so no link file is written.
  assert.equal(existsSync(join(dir, ".artifacts.json")), false);
  const page = await (await fetch(`${BASE}/a/${res.id}/`)).text();
  assert.match(page, /single/);
});

test("get, list and versions report the published artifact", () => {
  const dir = siteDir("<h1>reporting</h1>");
  const pub = json(["publish", dir, "--title", "Reporting"]);

  const got = json(["get", pub.id]);
  assert.equal(got.id, pub.id);
  assert.equal(got.title, "Reporting");
  assert.equal(got.current_version, 1);
  assert.equal(got.open_comments, 0);

  const list = json(["list"]);
  assert.ok(list.artifacts.some((a) => a.id === pub.id));

  json(["publish", dir]);
  const vs = json(["versions", pub.id]);
  assert.deepEqual(vs.versions.map((v) => v.version), [1, 2]);
});

test("publish and list attribute artifacts to opaque end users", () => {
  const dir = siteDir("<h1>customer report</h1>");
  const externalID = "customer/42?scope=1";
  const pub = json(["publish", dir, "--title", "Customer report", "--end-user", externalID]);
  assert.equal(pub.end_user, externalID);
  const filtered = json(["list", "--end-user", externalID]);
  assert.deepEqual(filtered.artifacts.map((artifact) => artifact.id), [pub.id]);
  assert.equal(filtered.artifacts[0].end_user, externalID);
  assert.deepEqual(json(["list", "--end-user", "another-customer"]).artifacts, []);

  const updated = json(["publish", dir]);
  assert.equal(updated.end_user, externalID);
  const reassigned = run(["publish", dir, "--end-user", "another-customer", "--json"]);
  assert.equal(reassigned.code, 1);
  assert.equal(JSON.parse(reassigned.stderr).error.code, "FORBIDDEN");
});

test("whoami identifies the admin tenant without revealing the bearer key", () => {
  const identity = json(["whoami"]);
  assert.equal(identity.tenant.id, "admin");
  assert.equal(identity.tenant.kind, "personal");
  assert.equal(typeof identity.tenant.name, "string");
  assert.equal(typeof identity.key.id, "number");
  assert.equal(typeof identity.key.name, "string");
  assert.equal(JSON.stringify(identity).includes(TOKEN), false);
});

test("the removed key creation endpoint returns 404", async () => {
  const response = await fetch(`${BASE}/api/keys`, {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email: "removed@example.test" }),
  });
  assert.equal(response.status, 404);
});

test("rollback republishes an older version as a new one", async () => {
  const dir = siteDir("<h1>v1 body</h1>");
  const pub = json(["publish", dir]);
  writeFileSync(join(dir, "index.html"), "<!doctype html><html><body><h1>v2 body</h1></body></html>");
  json(["publish", dir]);

  const back = json(["rollback", pub.id, "1"]);
  assert.equal(back.version, 3);
  const page = await (await fetch(`${BASE}/a/${pub.id}/`)).text();
  assert.match(page, /v1 body/);
});

test("comments, reply and resolve walk a viewer thread end to end", async () => {
  const dir = siteDir("<p>The quick brown fox.</p>");
  const pub = json(["publish", dir]);

  const created = await fetch(`${BASE}/c/${pub.id}/comments`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      author_name: "Reader",
      body: "this line reads oddly",
      anchor: { kind: "text", quote: "quick brown fox" },
      version: 1,
    }),
  });
  assert.equal(created.status, 201);
  const thread = await created.json();

  const open = json(["comments", pub.id]);
  assert.equal(open.threads.length, 1);
  assert.equal(open.threads[0].id, thread.id);
  assert.equal(open.threads[0].status, "open");
  assert.equal(open.threads[0].comments[0].body, "this line reads oddly");

  const replied = json(["reply", pub.id, thread.id, "fixed, thanks"]);
  assert.equal(replied.author_kind, "owner");

  const resolved = json(["resolve", pub.id, thread.id]);
  assert.equal(resolved.status, "resolved");
  assert.equal(resolved.comments.length, 2);

  assert.deepEqual(json(["comments", pub.id]).threads, []);
  assert.equal(json(["comments", pub.id, "--all"]).threads.length, 1);
});

test("human output is readable and --json stays a single object", () => {
  const dir = siteDir("<h1>human</h1>");
  const pub = json(["publish", dir]);
  const human = run(["get", pub.id]);
  assert.equal(human.code, 0);
  assert.match(human.stdout, new RegExp(pub.id));
  assert.match(human.stdout, /version 1 of 1/);
  assert.equal(run(["get", pub.id, "--json"]).stdout.split("\n").length, 1);
});

test("delete removes the artifact and later reads report it gone", () => {
  const dir = siteDir("<h1>doomed</h1>");
  const pub = json(["publish", dir]);
  assert.deepEqual(json(["delete", pub.id]), { id: pub.id, deleted: true });

  const after = run(["get", pub.id, "--json"]);
  assert.equal(after.code, 1);
  assert.equal(JSON.parse(after.stderr).error.code, "GONE");
});

test("errors: unknown command, missing config and a bad id", () => {
  const unknown = run(["frobnicate"]);
  assert.equal(unknown.code, 2);
  assert.match(unknown.stderr, /unknown command frobnicate/);

  const missing = spawnSync(process.execPath, [BIN, "list", "--json"], {
    encoding: "utf8",
    env: { ...process.env, ARTIFACTS_TOKEN: "", ARTIFACTS_API: "", HOME: tmp("artifacts-home-") },
  });
  assert.equal(missing.status, 2);
  assert.equal(JSON.parse(missing.stderr).error.code, "NOT_CONFIGURED");

  const bad = run(["get", "ar_does_not_exist", "--json"]);
  assert.equal(bad.code, 1);
  assert.equal(JSON.parse(bad.stderr).error.code, "NOT_FOUND");
});

test("login verifies the key before writing the config file", () => {
  const home = tmp("artifacts-home-");
  const ok = spawnSync(process.execPath, [BIN, "login", "--api", BASE, "--json"], {
    encoding: "utf8",
    input: TOKEN,
    env: { ...process.env, ARTIFACTS_TOKEN: "", ARTIFACTS_API: "", HOME: home },
  });
  assert.equal(ok.status, 0, ok.stderr);
  const saved = JSON.parse(readFileSync(join(home, ".artifacts", "config.json"), "utf8"));
  assert.deepEqual(saved, { token: TOKEN, api: BASE });

  const badHome = tmp("artifacts-home-");
  const bad = spawnSync(process.execPath, [BIN, "login", "--api", BASE, "--token", "nope", "--json"], {
    encoding: "utf8",
    env: { ...process.env, ARTIFACTS_TOKEN: "", ARTIFACTS_API: "", HOME: badHome },
  });
  assert.equal(bad.status, 1);
  assert.equal(JSON.parse(bad.stderr).error.code, "UNAUTHORIZED");
  assert.equal(existsSync(join(badHome, ".artifacts", "config.json")), false);
});
