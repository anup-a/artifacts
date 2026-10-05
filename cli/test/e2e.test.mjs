// Drives the real CLI binary against a real compiled Go server. Nothing here is
// mocked: every assertion is about what the server actually stored.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn, spawnSync, execFileSync } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, existsSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const CLI_DIR = dirname(dirname(fileURLToPath(import.meta.url)));
const REPO = dirname(CLI_DIR);
const BIN = join(CLI_DIR, "bin", "byagent.js");
// E2E_PORT lets two checkouts run the suite side by side.
const PORT = Number(process.env.E2E_PORT || 8123);
const BASE = `http://127.0.0.1:${PORT}`;
const TOKEN = "e2e-token";

let server;

function tmp(prefix) {
  return mkdtempSync(join(tmpdir(), prefix));
}

// run returns {code, stdout, stderr}; a failing command is a normal result here
// because several tests assert on the error envelope.
function run(args, { cwd = CLI_DIR, input, home } = {}) {
  const r = spawnSync(process.execPath, [BIN, ...args], {
    cwd,
    input,
    encoding: "utf8",
    env: { ...process.env, ARTIFACTS_TOKEN: TOKEN, ARTIFACTS_API: BASE, ...(home ? { HOME: home } : {}) },
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
      // Sign-in is on only so guest keys can be issued (claiming needs it).
      KEY_SIGNING_SECRET: "e2e-secret",
      EMAIL_PROVIDER: "log",
      GUEST_PUBLISH: "on",
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

test("publish a markdown file renders it and titles the artifact from the H1", async () => {
  const dir = tmp("artifacts-md-");
  const file = join(dir, "notes.md");
  writeFileSync(file, "# Markdown e2e heading\n\nA paragraph.\n");
  const res = json(["publish", file]);
  assert.equal(existsSync(join(dir, ".artifacts.json")), false);
  const page = await (await fetch(`${BASE}/a/${res.id}/`)).text();
  assert.match(page, /<h1 id="markdown-e2e-heading">Markdown e2e heading/);
  const got = json(["get", res.id]);
  assert.equal(got.title, "Markdown e2e heading");
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

test("list --search matches titles, case-insensitively and mid-word", () => {
  const dir = siteDir("<h1>search</h1>");
  const pub = json(["publish", dir, "--title", "Rebooking policy line"]);

  const hit = json(["list", "--search", "POLICY"]);
  assert.ok(hit.artifacts.some((a) => a.id === pub.id));
  assert.ok(hit.artifacts.every((a) => /policy/i.test(a.title)));
  assert.deepEqual(json(["list", "--search", "nothing-like-this"]).artifacts, []);
  // A wildcard is a character to look for, not a pattern to run.
  assert.deepEqual(json(["list", "--search", "%"]).artifacts, []);
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

test("rollback makes an older version live again without adding one", async () => {
  const dir = siteDir("<h1>v1 body</h1>");
  const pub = json(["publish", dir]);
  writeFileSync(join(dir, "index.html"), "<!doctype html><html><body><h1>v2 body</h1></body></html>");
  json(["publish", dir]);

  const back = json(["rollback", pub.id, "v1"]);
  assert.equal(back.version, 1);
  assert.equal(back.previous_version, 2);
  const page = await (await fetch(`${BASE}/a/${pub.id}/`)).text();
  assert.match(page, /v1 body/);
  const vs = json(["versions", pub.id]);
  assert.equal(vs.current_version, 1);
  assert.deepEqual(vs.versions.map((v) => [v.version, v.current]), [[1, true], [2, false]]);
  assert.equal(vs.versions[1].url, `${BASE}/a/${pub.id}/v/2/`);
  assert.equal(vs.versions[1].preview_url, `${BASE}/app/view/${pub.id}/v/2/`);

  const again = run(["rollback", pub.id, "1", "--json"]);
  assert.equal(again.code, 1);
  assert.equal(JSON.parse(again.stderr).error.code, "CONFLICT");
  assert.equal(run(["rollback", pub.id, "latest"]).code, 2);

  // The next publish takes v3, not a second v2.
  writeFileSync(join(dir, "index.html"), "<!doctype html><html><body><h1>v3 body</h1></body></html>");
  assert.equal(json(["publish", dir]).version, 3);
});

test("version rm deletes an old version but never the live one", async () => {
  const dir = siteDir("<h1>keep</h1>");
  const pub = json(["publish", dir]);
  writeFileSync(join(dir, "index.html"), "<!doctype html><html><body><h1>newer</h1></body></html>");
  json(["publish", dir]);

  const live = run(["version", "rm", pub.id, "2", "--json"]);
  assert.equal(live.code, 1);
  assert.equal(JSON.parse(live.stderr).error.code, "CONFLICT");

  const gone = json(["version", "rm", pub.id, "1"]);
  assert.equal(gone.deleted_version, 1);
  assert.equal(gone.versions_count, 1);
  assert.equal((await fetch(`${BASE}/a/${pub.id}/v/1/`)).status, 404);
  assert.equal(run(["version", "rm", pub.id]).code, 2);
  assert.equal(run(["version", "list", pub.id, "1"]).code, 2);
});

test("diff lists the files that changed between versions", () => {
  const dir = siteDir("<h1>one</h1>");
  writeFileSync(join(dir, "old.css"), "a{}");
  const pub = json(["publish", dir]);
  writeFileSync(join(dir, "index.html"), "<!doctype html><html><body><h1>two, longer</h1></body></html>");
  rmSync(join(dir, "old.css"));
  writeFileSync(join(dir, "new.css"), "b{}");
  json(["publish", dir]);

  const d = json(["diff", pub.id, "1"]);
  assert.equal(d.to, 2);
  assert.deepEqual(d.added.map((f) => f.path), ["new.css"]);
  assert.deepEqual(d.removed.map((f) => f.path), ["old.css"]);
  assert.deepEqual(d.changed.map((f) => f.path), ["index.html"]);
  const human = run(["diff", pub.id, "v1", "v2"]);
  assert.equal(human.code, 0);
  assert.match(human.stdout, /\+ new\.css/);
  assert.match(human.stdout, /- old\.css/);
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

test("comments --wait returns when a reader replies in an existing thread", async () => {
  const dir = siteDir("<p>Ship the beta on Friday.</p>");
  const pub = json(["publish", dir]);
  const post = (path, body) => fetch(`${BASE}${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  const thread = await (await post(`/c/${pub.id}/comments`, {
    author_name: "Reader", body: "Friday is tight", anchor: { kind: "text", quote: "on Friday" }, version: 1,
  })).json();

  const waiter = spawn(process.execPath, [BIN, "comments", pub.id, "--wait", "--interval", "5", "--timeout", "60", "--json"], {
    env: { ...process.env, ARTIFACTS_TOKEN: TOKEN, ARTIFACTS_API: BASE },
  });
  let out = "";
  waiter.stdout.on("data", (d) => { out += d; });
  const exited = new Promise((resolve) => waiter.on("close", resolve));

  await new Promise((r) => setTimeout(r, 1500));
  json(["reply", pub.id, thread.id, "moving it to Monday"]);
  const reply = await post(`/c/${pub.id}/comments/${thread.id}/reply`, { author_name: "Reader", body: "Monday works" });
  assert.equal(reply.status, 201);

  assert.equal(await exited, 0);
  const res = JSON.parse(out);
  assert.equal(res.timed_out, false);
  assert.equal(res.threads.length, 1);
  assert.equal(res.threads[0].comments.at(-1).body, "Monday works");
});

test("publish --no-comments removes the runtime and closes the viewer API; --comments restores them", async () => {
  const dir = siteDir("<p>Quiet page.</p>");
  const off = json(["publish", dir, "--no-comments"]);
  assert.equal(off.comments_enabled, false);
  assert.equal(json(["get", off.id]).comments_enabled, false);
  assert.match(run(["get", off.id]).stdout, /comments off/);

  let page = await (await fetch(`${BASE}/a/${off.id}/`)).text();
  assert.match(page, /Quiet page/);
  assert.doesNotMatch(page, /_rt\/comments\.js/);
  assert.equal((await fetch(`${BASE}/c/${off.id}/comments`)).status, 404);
  const attempt = await fetch(`${BASE}/c/${off.id}/comments`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ author_name: "Reader", body: "hi", anchor: { kind: "text", quote: "Quiet" }, version: 1 }),
  });
  assert.equal(attempt.status, 404);
  // The owner's listing still answers.
  assert.deepEqual(json(["comments", off.id, "--all"]).threads, []);

  // A plain republish keeps it off; --comments turns it back on.
  assert.equal(json(["publish", dir]).comments_enabled, false);
  const on = json(["publish", dir, "--comments"]);
  assert.equal(on.id, off.id);
  assert.equal(on.comments_enabled, true);
  page = await (await fetch(`${BASE}/a/${off.id}/`)).text();
  assert.match(page, /_rt\/comments\.js/);
  assert.equal((await fetch(`${BASE}/c/${off.id}/comments`)).status, 200);

  const both = run(["publish", dir, "--comments", "--no-comments", "--json"]);
  assert.equal(both.code, 2);
  assert.match(JSON.parse(both.stderr).error.message, /mutually exclusive/);
});

test("publish --project/--tag/--cover labels the artifact, serves the cover and filters list", async () => {
  const dir = siteDir("<h1>gallery</h1>");
  const coverSrc = join(tmp("artifacts-cover-"), "hero.svg");
  writeFileSync(coverSrc, '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1200 630"><rect width="1200" height="630" fill="#123"/></svg>');
  const pub = json(["publish", dir, "--title", "Gallery", "--project", "demo", "--tag", "a", "--tag", "b", "--cover", coverSrc]);
  assert.equal(pub.project, "demo");
  assert.deepEqual(pub.tags, ["a", "b"]);
  assert.equal(pub.cover_url, `${BASE}/a/${pub.id}/_cover.svg`);
  assert.equal(pub.files, 3); // index, css and the copied _cover.svg
  assert.equal(existsSync(join(dir, "_cover.svg")), true);
  assert.deepEqual(JSON.parse(readFileSync(join(dir, ".artifacts.json"), "utf8")), { id: pub.id, api: BASE, project: "demo", tags: ["a", "b"] });

  const cover = await fetch(`${BASE}/a/${pub.id}/_cover.svg`);
  assert.equal(cover.status, 200);
  assert.equal(cover.headers.get("content-type"), "image/svg+xml");
  assert.equal(cover.headers.get("cache-control"), "no-cache");
  assert.match(await cover.text(), /fill="#123"/);

  const page = await (await fetch(`${BASE}/a/${pub.id}/`)).text();
  assert.match(page, new RegExp(`<meta property="og:image" content="${BASE}/a/${pub.id}/_cover.svg">`));
  assert.match(page, /<meta property="og:title" content="Gallery">/);

  const got = json(["get", pub.id]);
  assert.equal(got.project, "demo");
  assert.deepEqual(got.tags, ["a", "b"]);
  assert.equal(got.cover, "_cover.svg");
  assert.equal(got.published_at, got.created_at);
  assert.match(run(["get", pub.id]).stdout, /demo  #a #b/);

  const filtered = json(["list", "--project", "demo"]);
  assert.ok(filtered.artifacts.some((a) => a.id === pub.id));
  assert.ok(filtered.artifacts.every((a) => a.project === "demo"));
  assert.deepEqual(json(["list", "--project", "no-such-project"]).artifacts, []);
  assert.match(run(["list", "--project", "demo"]).stdout, /\[demo  #a #b\]/);

  // A republish without flags keeps the labels from .artifacts.json.
  const again = json(["publish", dir]);
  assert.equal(again.id, pub.id);
  assert.equal(again.project, "demo");
  assert.deepEqual(again.tags, ["a", "b"]);

  // Validation errors name the field.
  const bad = run(["publish", dir, "--tag", "", "--json"]);
  assert.equal(bad.code, 1);
  assert.match(JSON.parse(bad.stderr).error.message, /tags\[0\]/);
  const badStyle = run(["publish", dir, "--cover-style", "plasma", "--json"]);
  assert.equal(badStyle.code, 1);
  assert.match(JSON.parse(badStyle.stderr).error.message, /cover_style/);
});

test("collections and collection read a project label back in publish order", () => {
  const project = "e2e course/one";
  const first = json(["publish", siteDir("<h1>lesson one</h1>"), "--title", "Lesson one", "--project", project]);
  const second = json(["publish", siteDir("<h1>lesson two</h1>"), "--title", "Lesson two", "--project", project]);
  // Republishing the first lesson makes it the newest, not the last.
  const firstDir = siteDir("<h1>lesson one again</h1>");
  const firstAgain = json(["publish", firstDir, "--id", first.id, "--title", "Lesson one"]);
  assert.equal(firstAgain.id, first.id);

  const listed = json(["collections"]).collections.find((c) => c.name === project);
  assert.equal(listed.count, 2);
  assert.equal(listed.share_url, null);
  assert.deepEqual(listed.latest.map((a) => a.id), [first.id, second.id]);
  const table = run(["collections"]).stdout.split("\n");
  assert.match(table[0], /^NAME\s+COUNT\s+UPDATED\s+SHARED$/);
  assert.ok(table.some((l) => l.startsWith(project) && /\s2\s/.test(l) && l.endsWith("-")));

  const got = json(["collection", project]);
  assert.equal(got.name, project);
  assert.equal(got.count, 2);
  assert.deepEqual(got.artifacts.map((a) => a.id), [first.id, second.id]);
  assert.equal(got.artifacts[0].url, `${BASE}/a/${first.id}/`);
  const human = run(["collection", project]).stdout.split("\n");
  assert.match(human[0], /^e2e course\/one  2 artifacts  updated /);
  assert.equal(human[1], `1. ${first.id}  Lesson one  ${BASE}/a/${first.id}/`);

  const missing = run(["collection", "no-such-collection", "--json"]);
  assert.equal(missing.code, 1);
  assert.equal(JSON.parse(missing.stderr).error.code, "NOT_FOUND");
});

test("a .byagent.json collection labels every publish below it", () => {
  const root = tmp("artifacts-proj-");
  writeFileSync(join(root, ".byagent.json"), JSON.stringify({ collection: "e2e folder" }));
  mkdirSync(join(root, "docs"));
  writeFileSync(join(root, "docs", "notes.md"), "# Folder notes\n\nhi\n");
  const pub = json(["publish", "docs/notes.md"], { cwd: root });
  assert.equal(pub.project, "e2e folder");
  assert.equal(json(["publish", "docs/notes.md", "--project", "other"], { cwd: root }).project, "other");
});

test("brief push keeps one private page per collection and skips an unchanged file", async () => {
  const root = tmp("artifacts-brief-");
  const home = tmp("artifacts-home-");
  const opts = { cwd: join(root, "src"), home };
  mkdirSync(join(root, "src"));
  writeFileSync(join(root, ".byagent.json"), JSON.stringify({ collection: "e2e brief" }));

  const none = run(["brief", "push"], opts);
  assert.equal(none.code, 2);
  assert.match(none.stderr, /no BRIEF\.md in /);

  writeFileSync(join(root, "BRIEF.md"), "# GA4 fix\n\nNext move: deploy the CSP change.\n");
  const first = json(["brief", "push"], opts);
  assert.equal(first.changed, true);
  assert.equal(first.version, 1);
  assert.equal(first.visibility, "private");
  const art = json(["get", first.id]);
  assert.equal(art.project, "e2e brief");
  assert.deepEqual(art.tags, ["brief"]);
  assert.equal(art.title, "GA4 fix");

  const same = run(["brief", "push"], opts);
  assert.equal(same.code, 0);
  assert.equal(same.stdout, "brief unchanged");

  writeFileSync(join(root, "BRIEF.md"), "# GA4 fix\n\nBlocked: needs the Fly token.\n");
  const second = json(["brief", "push"], opts);
  assert.equal(second.id, first.id);
  assert.equal(second.version, 2);

  // Another machine has no push state: it finds the same page by tag.
  const second_home = tmp("artifacts-home-");
  writeFileSync(join(root, "BRIEF.md"), "# GA4 fix\n\nDone.\n");
  const elsewhere = json(["brief", "push"], { ...opts, home: second_home });
  assert.equal(elsewhere.id, first.id);
  assert.equal(elsewhere.version, 3);

  const shown = json(["brief"], opts);
  assert.equal(shown.collection, "e2e brief");
  assert.equal(shown.exists, true);
  assert.match(shown.content, /Done\./);
  assert.equal(shown.artifact.id, first.id);
  assert.equal(shown.artifact.version, 3);
  const human = run(["brief"], opts).stdout.split("\n");
  assert.match(human[0], /^BRIEF\.md  \(collection e2e brief, v3, 0 open comments\)$/);
  assert.ok(human.includes("Done."));
  assert.equal(shown.source, "local");

  // A folder with the config but no BRIEF.md (a cloud agent, a fresh clone)
  // reads the pushed Markdown back from the server.
  const clone = tmp("artifacts-clone-");
  writeFileSync(join(clone, ".byagent.json"), JSON.stringify({ collection: "e2e brief" }));
  const remote = json(["brief"], { cwd: clone, home });
  assert.equal(remote.source, "remote");
  assert.equal(remote.content, "# GA4 fix\n\nDone.\n");
  assert.equal(run(["brief"], { cwd: clone, home }).stdout.split("\n")[0], "published brief  (collection e2e brief, v3, 0 open comments)");
  assert.equal(json(["brief", "--project", "e2e brief"], { cwd: tmp("artifacts-empty-"), home }).content, "# GA4 fix\n\nDone.\n");
});

test("an artifact without a cover gets a generated one in the requested style", async () => {
  const dir = siteDir("<h1>generated</h1>");
  const pub = json(["publish", dir, "--title", "Generated <cover>", "--cover-style", "rings"]);
  assert.equal(pub.cover_style, "rings");
  const cover = await fetch(`${BASE}/a/${pub.id}/_cover.svg`);
  assert.equal(cover.status, 200);
  const svg = await cover.text();
  assert.match(svg, /^<svg xmlns="http:\/\/www.w3.org\/2000\/svg" viewBox="0 0 1200 630"/);
  assert.match(svg, /Generated &lt;cover&gt;/);
  assert.doesNotMatch(svg, /<filter/);
  const cached = await fetch(`${BASE}/a/${pub.id}/_cover.svg`, { headers: { "If-None-Match": cover.headers.get("etag") } });
  assert.equal(cached.status, 304);
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

test("publish with nothing configured gets a guest key, saves it and reuses it", () => {
  const home = tmp("artifacts-guest-home-");
  const env = { ...process.env, HOME: home, ARTIFACTS_API: BASE };
  delete env.ARTIFACTS_TOKEN;
  const guestRun = (args) => {
    const r = spawnSync(process.execPath, [BIN, ...args, "--json"], { cwd: CLI_DIR, encoding: "utf8", env });
    assert.equal(r.status, 0, r.stderr);
    return JSON.parse(r.stdout);
  };
  const first = guestRun(["publish", siteDir("<p>guest one</p>")]);
  assert.equal(first.guest, true);
  assert.match(first.claim_url, /\/app\/claim\/t_/);
  assert.ok(Date.parse(first.expires_at) > Date.now() + 23 * 3600 * 1000);
  const saved = JSON.parse(readFileSync(join(home, ".artifacts", "config.json"), "utf8"));
  assert.equal(saved.api, BASE);
  assert.match(saved.token, /^ak_/);

  // The second publish reuses the saved key: same workspace, same claim link.
  const second = guestRun(["publish", siteDir("<p>guest two</p>")]);
  assert.equal(second.claim_url, first.claim_url);
  const me = guestRun(["whoami"]);
  assert.equal(me.guest, true);
  assert.equal(me.tenant.plan, "guest");

  // Guest pages are public only.
  const r = spawnSync(process.execPath, [BIN, "publish", siteDir(), "--private", "--json"], { cwd: CLI_DIR, encoding: "utf8", env });
  assert.equal(r.status, 1);
  assert.match(JSON.parse(r.stderr).error.message, /guest pages are public/);
});

test("BYAGENT_GUEST=0 keeps the not-configured error", () => {
  const env = { ...process.env, HOME: tmp("artifacts-guest-off-"), ARTIFACTS_API: BASE, BYAGENT_GUEST: "0" };
  delete env.ARTIFACTS_TOKEN;
  const r = spawnSync(process.execPath, [BIN, "publish", siteDir(), "--json"], { cwd: CLI_DIR, encoding: "utf8", env });
  assert.equal(r.status, 2);
  assert.equal(JSON.parse(r.stderr).error.code, "NOT_CONFIGURED");
});

test("mark shows readers what is happening to their comment", async () => {
  const pub = json(["publish", siteDir("<p>The quick brown fox.</p>")]);
  const created = await fetch(`${BASE}/c/${pub.id}/comments`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ author_name: "Reader", body: "colour or color?", anchor: { kind: "text", quote: "quick brown fox" }, version: 1 }),
  });
  const thread = await created.json();
  assert.equal(thread.progress, "");

  assert.equal(json(["mark", pub.id, thread.id, "working"]).progress, "working");
  const seenByReader = await (await fetch(`${BASE}/c/${pub.id}/comments`)).json();
  assert.equal(seenByReader.threads[0].progress, "working");

  const noNote = run(["mark", pub.id, thread.id, "declined", "--json"]);
  assert.equal(noNote.code, 2);
  const declined = json(["mark", pub.id, thread.id, "declined", "--note", "Keeping British spelling."]);
  assert.equal(declined.progress, "declined");
  assert.equal(declined.status, "resolved");
  assert.equal(declined.comments.at(-1).body, "Keeping British spelling.");
});

test("expire and publish --expires end a page; never keeps it", () => {
  const dir = siteDir("<p>short lived</p>");
  const pub = json(["publish", dir, "--expires", "2h"]);
  assert.ok(Date.parse(pub.expires_at) > Date.now() + 3600e3);
  assert.equal(json(["expire", pub.id, "never"]).expires_at, null);
  assert.ok(json(["expire", pub.id, "30m"]).expires_at);
  const bad = run(["expire", pub.id, "tomorrow", "--json"]);
  assert.equal(bad.code, 2);
});

// Views reach the store on a 30 second flush, so this checks the report
// itself; the counting is covered by the Go tests.
test("analytics reports a page and the workspace", async () => {
  const pub = json(["publish", siteDir("<p>counted</p>")]);
  const res = await fetch(pub.url, { headers: { Accept: "text/html", "User-Agent": "Mozilla/5.0 (Macintosh) Safari/605" } });
  assert.equal(res.status, 200);
  const page = json(["analytics", pub.id, "--range", "24h"]);
  assert.equal(page.id, pub.id);
  assert.equal(page.bucket, "hour");
  assert.equal(page.series.length, 24);
  assert.equal(typeof page.totals.range_views, "number");
  assert.ok(Array.isArray(page.top_referrers));
  const all = json(["analytics"]);
  assert.equal(all.range, "30d");
  assert.equal(all.series.length, 30);
  assert.ok(Array.isArray(all.top_pages));
  const human = run(["analytics", pub.id]);
  assert.equal(human.code, 0);
  assert.match(human.stdout, new RegExp(`^${pub.id}, last 30 days: \\d+ views, \\d+ visitors`));
  const raw = await fetch(`${BASE}/api/analytics?range=1y`, { headers: { Authorization: `Bearer ${TOKEN}` } });
  assert.equal(raw.status, 400);
  assert.equal(run(["analytics", "nope00000000", "--json"]).code, 1);
});
