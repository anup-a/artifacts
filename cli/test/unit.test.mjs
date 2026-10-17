import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { parseArgs } from "../src/args.js";
import { resolveConfig, saveConfig, configPath } from "../src/config.js";
import { collectFiles, htmlTitle, readLink, writeLink } from "../src/files.js";
import { publish, labelsOf, resolveCover } from "../src/commands/publish.js";
import { list, share, state, versions } from "../src/commands/artifacts.js";
import { comments, waitForComments } from "../src/commands/comments.js";
import { findProject } from "../src/project.js";
import { main } from "../src/main.js";
import { upstreamsOf } from "../src/commands/vars.js";

function tmp() {
  return mkdtempSync(join(tmpdir(), "artifacts-cli-"));
}

test("parseArgs splits command, positionals and flags", () => {
  const a = parseArgs(["publish", "./site", "--id", "ar_1", "--title=Hi", "--json"]);
  assert.equal(a.command, "publish");
  assert.deepEqual(a.positional, ["./site"]);
  assert.deepEqual(a.flags, { id: "ar_1", title: "Hi", json: true });
});

test("parseArgs collects a repeated --tag into an array", () => {
  const a = parseArgs(["publish", "./site", "--tag", "a", "--tag=b", "--project", "Demo"]);
  assert.deepEqual(a.flags, { tag: ["a", "b"], project: "Demo" });
  assert.equal(parseArgs(["list"]).flags.tag, undefined);
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
  assert.throws(() => resolveConfig({ env: {}, home }), (e) => e.exit === 2 && e.code === "NOT_CONFIGURED"
    && /byagent login/.test(e.message) && !/run artifacts login/.test(e.message));
  // BYAGENT_* names win over the older ARTIFACTS_* ones.
  assert.deepEqual(resolveConfig({ env: { BYAGENT_TOKEN: "b", BYAGENT_API: "https://b.example", ARTIFACTS_TOKEN: "t", ARTIFACTS_API: "https://a.example" }, home }), {
    token: "b",
    api: "https://b.example",
  });
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

test("collectFiles names an html page by its <title> unless --title is given", () => {
  const dir = tmp();
  writeFileSync(join(dir, "index.html"), "<html><head><title>\n  Tools &amp; resources:  who decides\n</title></head><body></body></html>");
  assert.equal(collectFiles(dir).title, "Tools & resources: who decides");
  assert.equal(collectFiles(dir, { title: "Mine" }).title, "Mine");
  writeFileSync(join(dir, "page.html"), "<TITLE>Lone &#8220;page&#x201D;</TITLE>");
  assert.equal(collectFiles(join(dir, "page.html")).title, "Lone \u201cpage\u201d");
  writeFileSync(join(dir, "bare.html"), "<h1>no title tag</h1><title>   </title>");
  assert.equal(collectFiles(join(dir, "bare.html")).title, undefined);
});

test("htmlTitle keeps a long title under the server's 200-byte limit", () => {
  const t = htmlTitle(Buffer.from(`<title>${"\u00e9".repeat(150)}</title>`));
  assert.ok(Buffer.byteLength(t) <= 200);
  assert.equal(t, "\u00e9".repeat(100));
  assert.equal(htmlTitle(Buffer.from("<p>none</p>")), undefined);
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

test("writeLink persists project and tags only when present", () => {
  const dir = tmp();
  writeLink(dir, { id: "ar_1", api: "https://a.example", project: "Demo", tags: ["a", "b"] });
  assert.deepEqual(readLink(dir), { id: "ar_1", api: "https://a.example", project: "Demo", tags: ["a", "b"] });
  writeLink(dir, { id: "ar_1", api: "https://a.example", project: undefined, tags: [] });
  assert.deepEqual(readLink(dir), { id: "ar_1", api: "https://a.example" });
});

test("labelsOf prefers flags over the link file and keeps remembered labels", () => {
  const link = { project: "Old", tags: ["x"] };
  assert.deepEqual(labelsOf({}, link), { project: "Old", tags: ["x"] });
  assert.deepEqual(labelsOf({ project: "New", tag: ["y"], "cover-style": "rings" }, link), { project: "New", tags: ["y"], coverStyle: "rings" });
  assert.deepEqual(labelsOf({ project: "" }, link), { project: "", tags: ["x"] });
  assert.deepEqual(labelsOf({}, {}), {});
});

test("labelsOf falls back to the folder collection only when flags and link are silent", () => {
  assert.deepEqual(labelsOf({}, {}, "Puzzled"), { project: "Puzzled" });
  assert.deepEqual(labelsOf({}, { project: "Old" }, "Puzzled"), { project: "Old" });
  assert.deepEqual(labelsOf({ project: "New" }, {}, "Puzzled"), { project: "New" });
});

test("findProject finds the nearest .byagent.json and rejects a bad one", () => {
  const root = tmp();
  mkdirSync(join(root, "apps", "desk", "docs"), { recursive: true });
  writeFileSync(join(root, ".byagent.json"), JSON.stringify({ collection: " factory " }));
  writeFileSync(join(root, "apps", "desk", ".byagent.json"), JSON.stringify({ collection: "desk" }));
  assert.deepEqual(findProject(join(root, "apps")), { root, path: join(root, ".byagent.json"), collection: "factory" });
  assert.equal(findProject(join(root, "apps", "desk", "docs")).collection, "desk");
  assert.equal(findProject(join(root, "apps", "desk", "docs")).root, join(root, "apps", "desk"));

  const bad = tmp();
  writeFileSync(join(bad, ".byagent.json"), JSON.stringify({ collection: "" }));
  assert.throws(() => findProject(bad), (e) => e.code === "CONFIG" && /needs "collection"/.test(e.message));
  writeFileSync(join(bad, ".byagent.json"), "{nope");
  assert.throws(() => findProject(bad), (e) => e.code === "CONFIG");
});

test("collectFiles ships a markdown file's own text only when asked", () => {
  const dir = tmp();
  writeFileSync(join(dir, "BRIEF.md"), "# Brief\n");
  assert.deepEqual(collectFiles(join(dir, "BRIEF.md")).files.map((f) => f.path), ["index.html"]);
  const withSource = collectFiles(join(dir, "BRIEF.md"), { source: "brief.md" }).files;
  assert.deepEqual(withSource.map((f) => f.path), ["index.html", "brief.md"]);
  assert.equal(withSource[1].body.toString(), "# Brief\n");
});

test("collectFiles never uploads the project config", () => {
  const dir = tmp();
  writeFileSync(join(dir, "index.html"), "<h1>hi</h1>");
  writeFileSync(join(dir, ".byagent.json"), JSON.stringify({ collection: "c" }));
  assert.deepEqual(collectFiles(dir).files.map((f) => f.path), ["index.html"]);
});

test("resolveCover references a file inside the directory and copies one from outside", () => {
  const dir = tmp();
  writeFileSync(join(dir, "index.html"), "<h1>hi</h1>");
  mkdirSync(join(dir, "img"));
  writeFileSync(join(dir, "img", "hero.png"), "png");
  const { files } = collectFiles(dir);
  const inside = resolveCover(join(dir, "img", "hero.png"), dir, files);
  assert.equal(inside.cover, "img/hero.png");
  assert.equal(inside.files, files);

  const outside = join(tmp(), "cover.svg");
  writeFileSync(outside, "<svg/>");
  const copied = resolveCover(outside, dir, files);
  assert.equal(copied.cover, "_cover.svg");
  assert.equal(readFileSync(join(dir, "_cover.svg"), "utf8"), "<svg/>");
  assert.ok(copied.files.some((f) => f.path === "_cover.svg"));

  // Single-file publish: no directory to copy into, the cover joins the upload.
  const single = resolveCover(outside, null, [{ path: "index.html", sha256: "0".repeat(64), size: 1 }]);
  assert.equal(single.cover, "_cover.svg");
  assert.deepEqual(single.files.map((f) => f.path), ["index.html", "_cover.svg"]);
  assert.match(single.files[1].sha256, /^[0-9a-f]{64}$/);

  assert.throws(() => resolveCover(join(dir, "index.html"), dir, files), /svg, png, jpg or webp/);
  assert.throws(() => resolveCover(join(dir, "missing.png"), dir, files), /no such cover file/);
  assert.deepEqual(resolveCover(undefined, dir, files), { cover: undefined, files });
});

test("publish sends project, tags, cover and style in the manifest and remembers the labels", async (t) => {
  const dir = tmp();
  writeFileSync(join(dir, "index.html"), "<h1>labelled</h1>");
  writeFileSync(join(dir, "cover.svg"), "<svg/>");
  const calls = [];
  t.mock.method(globalThis, "fetch", async (url, options) => {
    calls.push({ url, ...options });
    return Response.json(url.pathname.endsWith("preflight") ? { missing: [] } : {
      id: "artifact1234", url: "https://share.example/a/artifact1234/", version: 1, end_user: null,
      project: "Demo", tags: ["a", "b"], cover_url: "https://share.example/a/artifact1234/_cover.svg", cover_style: "rings",
    });
  });
  const result = await publish({ token: "t", api: "https://api.example" }, {
    positional: [dir], flags: { project: "Demo", tag: ["A", "b"], cover: join(dir, "cover.svg"), "cover-style": "rings" },
  });
  const manifest = JSON.parse(calls[1].body.get("manifest"));
  assert.equal(manifest.project, "Demo");
  assert.deepEqual(manifest.tags, ["A", "b"]);
  assert.equal(manifest.cover, "cover.svg");
  assert.equal(manifest.cover_style, "rings");
  assert.equal(result.json.project, "Demo");
  assert.deepEqual(result.json.tags, ["a", "b"]);
  assert.equal(result.json.cover_style, "rings");
  assert.match(result.human[2], /project Demo, tags a, b/);
  assert.deepEqual(readLink(dir), { id: "artifact1234", api: "https://api.example", project: "Demo", tags: ["a", "b"] });

  // A republish without flags sends the remembered labels again.
  calls.length = 0;
  await publish({ token: "t", api: "https://api.example" }, { positional: [dir], flags: {} });
  const again = JSON.parse(calls[1].body.get("manifest"));
  assert.equal(again.project, "Demo");
  assert.deepEqual(again.tags, ["a", "b"]);
  assert.equal(again.cover, undefined);
  assert.equal(calls[1].method, "PUT");
});

test("list passes --project through and prints labels", async (t) => {
  let requested;
  t.mock.method(globalThis, "fetch", async (url) => {
    requested = url;
    return Response.json({ artifacts: [{ id: "artifact1234", current_version: 1, open_comments: 0, title: "T", project: "Demo", tags: ["a"] }] });
  });
  const result = await list({ token: "t", api: "https://api.example" }, { flags: { project: "Demo" } });
  assert.equal(requested.searchParams.get("project"), "Demo");
  assert.match(result.human[0], /\[Demo  #a\]/);
});

test("complete environment config does not read the saved config", () => {
  const configDir = tmp();
  mkdirSync(join(configDir, ".artifacts"));
  writeFileSync(configPath(configDir), "invalid json");
  assert.deepEqual(resolveConfig({
    env: { ARTIFACTS_TOKEN: "test-token", ARTIFACTS_API: "https://api.example/" },
    home: configDir,
  }), { token: "test-token", api: "https://api.example" });
});

test("publish attributes a new artifact and includes the server end user in JSON", async (t) => {
  const file = join(tmp(), "index.html");
  writeFileSync(file, "<h1>customer report</h1>");
  const calls = [];
  t.mock.method(globalThis, "fetch", async (url, options) => {
    calls.push({ url, ...options });
    const body = calls.length === 1 ? { missing: [] } : {
      id: "artifact1234", url: "https://share.example/a/artifact1234/", version: 1, end_user: "customer/42",
    };
    return Response.json(body);
  });
  const result = await publish({ token: "test-token", api: "https://api.example" }, {
    positional: [file], flags: { "end-user": "customer/42" },
  });
  assert.equal(calls[1].method, "POST");
  assert.equal(calls[1].headers["X-Artifact-End-User"], "customer/42");
  assert.equal(calls[0].headers["X-Artifact-End-User"], undefined);
  assert.equal(result.json.end_user, "customer/42");
});

test("publish updates send attribution and preserve server attribution when omitted", async (t) => {
  const file = join(tmp(), "index.html");
  writeFileSync(file, "<h1>updated report</h1>");
  const calls = [];
  t.mock.method(globalThis, "fetch", async (url, options) => {
    calls.push({ url, ...options });
    return Response.json(url.pathname.endsWith("preflight") ? { missing: [] } : {
      id: "artifact1234", url: "https://share.example/a/artifact1234/", version: 2, end_user: "customer/42",
    });
  });
  for (const attribution of [{ "end-user": "customer/42" }, {}]) {
    const result = await publish({ token: "test-token", api: "https://api.example" }, {
      positional: [file], flags: { id: "artifact1234", ...attribution },
    });
    assert.equal(calls.at(-1).method, "PUT");
    assert.equal(calls.at(-1).headers["X-Artifact-End-User"], attribution["end-user"]);
    assert.equal(result.json.end_user, "customer/42");
  }
});

test("unattributed publish JSON includes a null end user", async (t) => {
  const file = join(tmp(), "index.html");
  writeFileSync(file, "<h1>personal report</h1>");
  t.mock.method(globalThis, "fetch", async (url) => Response.json(
    url.pathname.endsWith("preflight") ? { missing: [] } : {
      id: "artifact1234", url: "https://share.example/a/artifact1234/", version: 1, end_user: null,
    },
  ));
  const result = await publish({ token: "test-token", api: "https://api.example" }, { positional: [file], flags: {} });
  assert.equal(result.json.end_user, null);
});

test("publish --no-comments and --comments set comments_enabled in the manifest", async (t) => {
  const file = join(tmp(), "index.html");
  writeFileSync(file, "<h1>quiet report</h1>");
  const cases = [
    [{ "no-comments": true }, false],
    [{ comments: true }, true],
    [{}, undefined],
  ];
  for (const [flags, expected] of cases) {
    const calls = [];
    t.mock.method(globalThis, "fetch", async (url, options) => {
      calls.push({ url, ...options });
      return Response.json(url.pathname.endsWith("preflight") ? { missing: [] } : {
        id: "artifact1234", url: "https://share.example/a/artifact1234/", version: 1, end_user: null,
        comments_enabled: expected !== false,
      });
    });
    const result = await publish({ token: "test-token", api: "https://api.example" }, { positional: [file], flags });
    const preflight = JSON.parse(calls[0].body);
    assert.equal(preflight.comments_enabled, expected);
    const manifest = JSON.parse(calls[1].body.get("manifest"));
    assert.equal(manifest.comments_enabled, expected);
    assert.equal(result.json.comments_enabled, expected !== false);
    assert.equal(result.human[1].endsWith("comments off"), expected === false);
  }
  await assert.rejects(
    () => publish({ token: "t", api: "https://api.example" }, { positional: [file], flags: { comments: true, "no-comments": true } }),
    /mutually exclusive/,
  );
});

test("publish --no-state, --state and --summary reach the manifest", async (t) => {
  const file = join(tmp(), "index.html");
  writeFileSync(file, "<h1>stateful report</h1>");
  const cases = [
    [{ "no-state": true, summary: "added the chart\\nfixed totals " }, false, "added the chart\nfixed totals"],
    [{ state: true }, true, undefined],
    [{}, undefined, undefined],
  ];
  for (const [flags, expected, summary] of cases) {
    const calls = [];
    t.mock.method(globalThis, "fetch", async (url, options) => {
      calls.push({ url, ...options });
      return Response.json(url.pathname.endsWith("preflight") ? { missing: [] } : {
        id: "artifact1234", url: "https://share.example/a/artifact1234/", version: 1, end_user: null,
        state_enabled: expected !== false,
      });
    });
    const result = await publish({ token: "test-token", api: "https://api.example" }, { positional: [file], flags });
    const manifest = JSON.parse(calls[1].body.get("manifest"));
    assert.equal(manifest.state_enabled, expected);
    assert.equal(manifest.summary, summary);
    assert.equal(result.json.state_enabled, expected !== false);
    assert.equal(result.human[1].endsWith("page state off"), expected === false);
  }
  await assert.rejects(
    () => publish({ token: "t", api: "https://api.example" }, { positional: [file], flags: { state: true, "no-state": true } }),
    /mutually exclusive/,
  );
  await assert.rejects(
    () => publish({ token: "t", api: "https://api.example" }, { positional: [file], flags: { summary: "x".repeat(1001) } }),
    /1000 characters/,
  );
});

test("state prints a page's state as JSON and --clear empties it", async (t) => {
  const calls = [];
  t.mock.method(globalThis, "fetch", async (url, options) => {
    calls.push(`${options.method} ${url.pathname}`);
    return Response.json(options.method === "DELETE" ? { id: "artifact1234", revision: 4, cleared: true } : { id: "artifact1234", revision: 3, items: { done: true } });
  });
  const cfg = { token: "t", api: "https://api.example" };
  const shown = await state(cfg, { positional: ["artifact1234"], flags: {} });
  assert.deepEqual(JSON.parse(shown.human.join("\n")), { done: true });
  assert.equal(shown.json.revision, 3);
  const cleared = await state(cfg, { positional: ["artifact1234"], flags: { clear: true } });
  assert.match(cleared.human[0], /cleared .*revision 4/);
  assert.deepEqual(calls, ["GET /api/artifacts/artifact1234/state", "DELETE /api/artifacts/artifact1234/state"]);
});

test("versions lists each version's summary as one change per line", async (t) => {
  t.mock.method(globalThis, "fetch", async () => Response.json({ id: "artifact1234", current_version: 2, versions: [
    { version: 1, files: 1, bytes: 9, comment_count: 0, created_at: "t1", summary: "" },
    { version: 2, current: true, files: 1, bytes: 9, comment_count: 0, created_at: "t2", summary: "added the chart\nfixed totals" },
  ] }));
  const res = await versions({ token: "t", api: "https://api.example" }, { positional: ["artifact1234"], flags: {} });
  assert.deepEqual(res.human.slice(1), ["v2  live  1 files  9 bytes  0 comments  t2", "    - added the chart", "    - fixed totals"]);
});

test("publish visibility flags set the manifest and omission preserves server visibility", async (t) => {
  const dir = tmp();
  writeFileSync(join(dir, "index.html"), "<p>workspace report</p>");
  writeLink(dir, { id: "artifact1234", api: "https://api.example", visibility: "public" });
  for (const [argv, expected] of [
    [["--private"], "private"],
    [["--public"], "public"],
    [[], undefined],
  ]) {
    const calls = [];
    t.mock.method(globalThis, "fetch", async (url, options) => {
      calls.push({ url, ...options });
      return Response.json(url.pathname.endsWith("preflight") ? { missing: [] } : {
        id: "artifact1234", url: "https://api.example/app/view/artifact1234/", version: 2,
        visibility: expected ?? "private",
      });
    });
    const result = await publish({ token: "t", api: "https://api.example" }, parseArgs(["publish", dir, ...argv]));
    assert.equal(calls[1].method, "PUT");
    assert.equal(JSON.parse(calls[0].body).visibility, expected);
    assert.equal(JSON.parse(calls[1].body.get("manifest")).visibility, expected);
    assert.equal(result.json.visibility, expected ?? "private");
    assert.equal(result.json.url, "https://api.example/app/view/artifact1234/");
  }
  t.mock.method(globalThis, "fetch", () => { throw new Error("must reject before networking"); });
  await assert.rejects(
    () => publish({ token: "t", api: "https://api.example" }, parseArgs(["publish", dir, "--private", "--public"])),
    /--private and --public are mutually exclusive/,
  );
  assert.throws(() => parseArgs(["publish", dir, "--private=true"]), /does not take a value/);
});

test("list filters by an opaque end user and returns the API JSON unchanged", async (t) => {
  const response = { artifacts: [{ id: "artifact1234", end_user: "customer/42?scope=1", current_version: 2 }] };
  let requested;
  t.mock.method(globalThis, "fetch", async (url) => {
    requested = url;
    return Response.json(response);
  });
  const result = await list({ token: "test-token", api: "https://api.example" }, {
    flags: { "end-user": "customer/42?scope=1", limit: "50", cursor: "next cursor" },
  });
  assert.equal(requested.searchParams.get("end_user"), "customer/42?scope=1");
  assert.equal(requested.searchParams.get("limit"), "50");
  assert.equal(requested.searchParams.get("cursor"), "next cursor");
  assert.deepEqual(result.json, response);
});

async function command(args, overrides = {}) {
  const output = [];
  const errors = [];
  const code = await main(args, {
    env: { ARTIFACTS_TOKEN: "test-token", ARTIFACTS_API: "https://api.example/" },
    out: (line) => output.push(line), err: (line) => errors.push(line), ...overrides,
  });
  return { code, output, errors };
}

test("whoami reports the current tenant and key in JSON and human output", async (t) => {
  const identity = { tenant: { id: "t_example", kind: "integrator", name: "Example" }, key: { id: 3, name: "CI" } };
  t.mock.method(globalThis, "fetch", async (url, options) => {
    assert.equal(url.pathname, "/api/me");
    assert.equal(options.method, "GET");
    return Response.json(identity);
  });
  const json = await command(["whoami", "--json"]);
  assert.equal(json.code, 0);
  assert.deepEqual(json.errors, []);
  assert.deepEqual(json.output.map(JSON.parse), [identity]);
  const human = await command(["whoami"]);
  assert.equal(human.code, 0);
  assert.match(human.output.join("\n"), /Example.*integrator.*t_example/);
  assert.match(human.output.join("\n"), /CI.*3/);
});

test("portal-link encodes the end user and prints only the URL unless JSON is requested", async (t) => {
  const response = { url: "https://api.example/p/test-portal", expires_at: "2026-09-23T12:00:00Z" };
  t.mock.method(globalThis, "fetch", async (url, options) => {
    assert.equal(url.pathname, "/api/end-users/customer%2F42%3Fscope%3D1/portal-link");
    assert.equal(options.method, "POST");
    return Response.json(response);
  });
  const human = await command(["portal-link", "customer/42?scope=1"]);
  assert.equal(human.code, 0);
  assert.deepEqual(human.output, [response.url]);
  const json = await command(["portal-link", "customer/42?scope=1", "--json"]);
  assert.equal(json.code, 0);
  assert.deepEqual(json.output.map(JSON.parse), [response]);
});

test("portal-link rejects a missing end user without making a request", async (t) => {
  const request = t.mock.method(globalThis, "fetch", () => { throw new Error("unexpected request"); });
  const result = await command(["portal-link", "--json"]);
  assert.equal(result.code, 2);
  assert.match(JSON.parse(result.errors[0]).error.message, /usage: byagent portal-link/);
  assert.equal(request.mock.callCount(), 0);
});

test("portal-link preserves a server error and request id", async (t) => {
  t.mock.method(globalThis, "fetch", async () => Response.json({
    error: { code: "FORBIDDEN", message: "integrator tenant required" },
  }, { status: 403, headers: { "X-Request-Id": "request-123" } }));
  const result = await command(["portal-link", "customer", "--json"]);
  assert.equal(result.code, 1);
  assert.deepEqual(result.output, []);
  assert.deepEqual(JSON.parse(result.errors[0]), {
    error: { code: "FORBIDDEN", message: "integrator tenant required", request_id: "request-123" },
  });
});

test("help explains tenant commands and resolves the key creation URL", async () => {
  const result = await command(["--help"]);
  assert.equal(result.code, 0);
  const help = result.output.join("\n");
  assert.match(help, /publish.*--end-user/);
  assert.match(help, /--comments\|--no-comments/);
  assert.match(help, /list.*--end-user.*--project/);
  assert.match(help, /--tag <t>/);
  assert.match(help, /--cover <file>/);
  assert.match(help, /--cover-style dots\|grid\|lines\|wash\|rings\|plain/);
  assert.match(help, /byagent whoami/);
  assert.match(help, /byagent portal-link <end-user id>/);
  assert.match(help, /byagent collections \[--names\]\n/);
  assert.match(help, /byagent collection <name>/);
  assert.match(help, /https:\/\/api\.example\/app\/keys/);
  assert.match(help, /POST \/api\/keys.*removed/);
  // The client manual says nothing about running a server, and has no placeholders.
  assert.doesNotMatch(help, /KEY_SIGNING_SECRET|RESEND_API_KEY|LIMIT_ARTIFACTS|<PUBLIC_API_ORIGIN>/);
  const explicit = await command(["login", "--api", "https://custom.example/", "--help"]);
  const loginHelp = explicit.output.join("\n");
  assert.match(loginHelp, /https:\/\/custom\.example\/app\/keys/);
  // <command> --help is that command alone, not the whole manual.
  assert.match(loginHelp, /byagent login --wait/);
  assert.match(loginHelp, /action_required\.message/);
  assert.doesNotMatch(loginHelp, /byagent publish|byagent domain/);
  assert.ok(loginHelp.length < help.length / 3);
  const publishHelp = (await command(["help", "publish"])).output.join("\n");
  assert.match(publishHelp, /byagent publish <dir/);
  assert.match(publishHelp, /--password/, "continuation lines of a usage entry are kept");
  assert.doesNotMatch(publishHelp, /byagent login/);
});

test("share sends the action in the request body, so --off actually revokes", async (t) => {
  for (const [argv, action] of [[[], "issue"], [["--off"], "revoke"]]) {
    const calls = [];
    t.mock.method(globalThis, "fetch", async (url, options) => {
      calls.push({ url, ...options });
      return Response.json(action === "revoke"
        ? { id: "artifact1234", share_code: null, share_url: null }
        : { id: "artifact1234", share_code: "481523", share_url: "https://share.example/a/artifact1234/?k=481523" });
    });
    const result = await share({ token: "t", api: "https://api.example" },
      parseArgs(["share", "artifact1234", ...argv]));
    assert.equal(calls.length, 1);
    assert.equal(calls[0].url.pathname, "/api/artifacts/artifact1234/share-code");
    assert.equal(calls[0].method, "POST");
    // The body is the whole point: a dropped one silently means "issue".
    assert.equal(JSON.parse(calls[0].body).action, action);
    if (action === "issue") {
      assert.equal(result.json.share_code, "481523");
      assert.ok(result.human.some((l) => l.includes("481523")));
    } else {
      assert.equal(result.json.share_code, null);
      assert.ok(result.human.some((l) => l.includes("no longer shared")));
    }
  }
});

test("collections prints one aligned row per collection and passes the JSON through", async (t) => {
  const response = {
    collections: [
      { name: "Async Rust", count: 3, description: "", updated_at: "2026-09-23T10:00:00Z", share_url: "https://share.example/s/slug12345678/", latest: [] },
      { name: "Hi Travel", count: 1, description: "trips", updated_at: "2026-09-22T09:00:00Z", share_url: null, latest: [] },
    ],
  };
  t.mock.method(globalThis, "fetch", async (url, options) => {
    assert.equal(url.pathname, "/api/collections");
    assert.equal(options.method, "GET");
    return Response.json(response);
  });
  const json = await command(["collections", "--json"]);
  assert.equal(json.code, 0);
  assert.deepEqual(json.output.map(JSON.parse), [response]);
  const human = await command(["collections"]);
  assert.equal(human.code, 0);
  assert.equal(human.output.length, 3);
  assert.match(human.output[0], /^NAME\s+COUNT\s+UPDATED\s+SHARED$/);
  assert.match(human.output[1], /^Async Rust\s+3\s+2026-09-23T10:00:00Z\s+https:\/\/share\.example\/s\/slug12345678\/$/);
  assert.match(human.output[2], /^Hi Travel\s+1\s+2026-09-22T09:00:00Z\s+-$/);
  // Columns line up.
  assert.equal(human.output[1].indexOf("3 "), human.output[0].indexOf("COUNT"));
});

test("collections says so when there are none", async (t) => {
  t.mock.method(globalThis, "fetch", async () => Response.json({ collections: [] }));
  const human = await command(["collections"]);
  assert.deepEqual(human.output, ["no collections; publish with --project <name> to start one"]);
});

test("collection encodes the name and lists artifacts in course order with urls", async (t) => {
  const response = {
    name: "Hi Travel/Q3", count: 2, description: "trips", updated_at: "2026-09-23T10:00:00Z",
    share_url: "https://share.example/s/travelslug01/",
    artifacts: [
      { id: "artifact0001", title: "Intro", url: "https://share.example/a/artifact0001/", visibility: "public" },
      { id: "artifact0002", title: "", url: "https://api.example/app/view/artifact0002/", visibility: "private" },
    ],
  };
  t.mock.method(globalThis, "fetch", async (url, options) => {
    assert.equal(url.pathname, "/api/collections/Hi%20Travel%2FQ3");
    assert.equal(options.method, "GET");
    return Response.json(response);
  });
  const json = await command(["collection", "Hi Travel/Q3", "--json"]);
  assert.equal(json.code, 0);
  assert.deepEqual(json.output.map(JSON.parse), [response]);
  const human = await command(["collection", "Hi Travel/Q3"]);
  assert.deepEqual(human.output, [
    "Hi Travel/Q3  2 artifacts  updated 2026-09-23T10:00:00Z",
    "trips",
    "shared https://share.example/s/travelslug01/",
    "1. artifact0001  Intro  https://share.example/a/artifact0001/",
    "2. artifact0002  (untitled)  https://api.example/app/view/artifact0002/  [private]",
  ]);
});

test("collection needs a name and reports a missing one as the server does", async (t) => {
  const request = t.mock.method(globalThis, "fetch", () => { throw new Error("unexpected request"); });
  const missing = await command(["collection", "--json"]);
  assert.equal(missing.code, 2);
  assert.match(JSON.parse(missing.errors[0]).error.message, /usage: byagent collection <name>/);
  assert.equal(request.mock.callCount(), 0);
  request.mock.mockImplementation(async () => Response.json({ error: { code: "NOT_FOUND", message: "collection not found" } }, { status: 404 }));
  const unknown = await command(["collection", "Nope"]);
  assert.equal(unknown.code, 1);
  assert.match(unknown.errors[0], /collection not found/);
});

test("share needs an artifact id", async () => {
  await assert.rejects(() => share({ token: "t", api: "https://api.example" }, parseArgs(["share"])),
    /usage: byagent share/);
});

const CFG = { token: "t", api: "https://api.example" };
const reader = (id, at = "2026-10-01T10:00:00Z") => ({ id, author_name: "Reader", author_kind: "viewer", body: id, created_at: at });
const owner = (id) => ({ id, author_name: "Author", author_kind: "owner", body: id, created_at: "2026-10-01T10:05:00Z" });

// fakeClock lets waitForComments poll without real delays: each sleep moves
// the clock forward by the requested amount.
function fakeClock() {
  let t = 0;
  return { now: () => t, sleep: async (ms) => { t += ms; } };
}

function servePolls(t, polls) {
  const seen = [];
  t.mock.method(globalThis, "fetch", async (url) => {
    seen.push(url);
    const next = polls[Math.min(seen.length - 1, polls.length - 1)];
    if (next instanceof Response) return next;
    return Response.json({ threads: next });
  });
  return seen;
}

test("comments --wait wakes on a reader reply inside an existing thread, not on owner replies", async (t) => {
  const old = { id: "th_1", status: "open", comments: [reader("c1")] };
  const seen = servePolls(t, [
    [old],
    [{ ...old, comments: [reader("c1"), owner("c2")] }],
    [{ ...old, comments: [reader("c1"), owner("c2"), reader("c3")] }],
  ]);
  const res = await waitForComments(CFG, "art1", { intervalMs: 20000, timeoutMs: 600000, ...fakeClock() });
  assert.equal(res.timed_out, false);
  assert.deepEqual(res.threads.map((th) => th.id), ["th_1"]);
  assert.equal(seen.length, 3);
  assert.equal(seen[0].searchParams.get("status"), "all");
});

test("comments --wait returns only threads with new reader comments", async (t) => {
  servePolls(t, [
    [{ id: "th_1", comments: [reader("c1")] }],
    [{ id: "th_1", comments: [reader("c1")] }, { id: "th_2", comments: [reader("c2")] }],
  ]);
  const res = await waitForComments(CFG, "art1", { intervalMs: 20000, timeoutMs: 600000, ...fakeClock() });
  assert.deepEqual(res.threads.map((th) => th.id), ["th_2"]);
});

test("comments --wait with --since counts comments written after it straight away", async (t) => {
  const seen = servePolls(t, [[
    { id: "th_old", comments: [reader("c1", "2026-10-01T09:00:00Z")] },
    { id: "th_new", comments: [reader("c2", "2026-10-01T11:00:00Z")] },
  ]]);
  const res = await waitForComments(CFG, "art1", { intervalMs: 20000, timeoutMs: 600000, since: "2026-10-01T10:00:00Z", ...fakeClock() });
  assert.deepEqual(res.threads.map((th) => th.id), ["th_new"]);
  assert.equal(seen.length, 1);
});

test("comments --wait times out with timed_out true", async (t) => {
  const seen = servePolls(t, [[{ id: "th_1", comments: [reader("c1")] }]]);
  const res = await waitForComments(CFG, "art1", { intervalMs: 20000, timeoutMs: 60000, ...fakeClock() });
  assert.deepEqual(res, { threads: [], timed_out: true });
  assert.equal(seen.length, 4); // polls at 0, 20, 40 and 60 seconds
});

test("comments --wait retries a server hiccup but stops on a missing artifact", async (t) => {
  servePolls(t, [
    Response.json({ error: { code: "INTERNAL", message: "boom" } }, { status: 503 }),
    [],
    [{ id: "th_1", comments: [reader("c1")] }],
  ]);
  const res = await waitForComments(CFG, "art1", { intervalMs: 20000, timeoutMs: 600000, ...fakeClock() });
  assert.deepEqual(res.threads.map((th) => th.id), ["th_1"]);

  t.mock.restoreAll();
  servePolls(t, [Response.json({ error: { code: "NOT_FOUND", message: "artifact not found" } }, { status: 404 })]);
  await assert.rejects(waitForComments(CFG, "gone", { intervalMs: 20000, timeoutMs: 600000, ...fakeClock() }), /artifact not found/);
});

test("comments --wait rejects --open, --all and a too-short interval", async () => {
  await assert.rejects(comments(CFG, { positional: ["a"], flags: { wait: true, open: true } }), /--wait/);
  await assert.rejects(comments(CFG, { positional: ["a"], flags: { wait: true, interval: "1" } }), /--interval must be/);
  await assert.rejects(comments(CFG, { positional: ["a"], flags: { wait: true, since: "yesterday" } }), /--since must be/);
});

test("provenanceOf: flags, then env, then Claude Code detection", async () => {
  const { provenanceOf } = await import("../src/commands/publish.js");
  assert.deepEqual(provenanceOf({}, {}), {});
  assert.deepEqual(provenanceOf({}, { CLAUDECODE: "1" }), { agent: "claude-code" });
  assert.deepEqual(provenanceOf({}, { CLAUDECODE: "1", BYAGENT_AGENT: "codex", BYAGENT_MODEL: "gpt" }), { agent: "codex", model: "gpt" });
  assert.deepEqual(provenanceOf({ agent: "cursor", model: "m" }, { BYAGENT_AGENT: "codex" }), { agent: "cursor", model: "m" });
});

test("parseWhen reads durations, RFC 3339 times and never", async () => {
  const { parseWhen } = await import("../src/commands/artifacts.js");
  const now = Date.parse("2026-10-03T00:00:00Z");
  assert.equal(parseWhen("30m", now), "2026-10-03T00:30:00.000Z");
  assert.equal(parseWhen("7d", now), "2026-10-10T00:00:00.000Z");
  assert.equal(parseWhen("2w", now), "2026-10-17T00:00:00.000Z");
  assert.equal(parseWhen("2026-12-01T10:00:00+05:30", now), "2026-12-01T04:30:00.000Z");
  assert.equal(parseWhen("never", now), null);
  assert.throws(() => parseWhen("tomorrow", now), /--expires takes/);
  assert.throws(() => parseWhen("2026-12-01", now), /--expires takes/);
});

function report(over = {}) {
  return {
    id: "ar_page1", url: "https://share.example/a/ar_page1/", range: "7d", bucket: "day",
    from: "2026-09-28T00:00:00Z", to: "2026-10-05T00:00:00Z", first_view_day: "2026-09-30",
    totals: { all_time_views: 40, range_views: 12, range_visitors: 7 },
    series: ["2026-09-28", "2026-09-29", "2026-09-30", "2026-10-01", "2026-10-02", "2026-10-03", "2026-10-04"]
      .map((bucket, i) => ({ bucket, views: [0, 0, 1, 2, 0, 3, 6][i], visitors: [0, 0, 1, 1, 0, 2, 3][i] })),
    top_referrers: [{ referrer: "news.ycombinator.com", views: 9 }, { referrer: "(direct)", views: 3 }],
    top_countries: [], top_paths: [{ path: "/", views: 12 }], top_devices: [{ device: "desktop", views: 12 }],
    ...over,
  };
}

test("analytics <id> asks for the page report and prints totals, the series and the top lists", async (t) => {
  const rep = report();
  t.mock.method(globalThis, "fetch", async (url, options) => {
    assert.equal(url.pathname, "/api/artifacts/ar_page1/analytics");
    assert.equal(url.searchParams.get("range"), "7d");
    assert.equal(options.method, "GET");
    return Response.json(rep);
  });
  const json = await command(["analytics", "ar_page1", "--range", "7d", "--json"]);
  assert.equal(json.code, 0);
  assert.deepEqual(json.output.map(JSON.parse), [rep]);
  const human = await command(["analytics", "ar_page1", "--range", "7d"]);
  assert.equal(human.code, 0);
  assert.equal(human.output[0], "ar_page1, last 7 days: 12 views, 7 visitors (40 all time)");
  assert.match(human.output[1], /^2026-09-28 {2}.{7} {2}2026-10-04$/);
  assert.ok(human.output.includes("DAY (UTC)   VIEWS  VISITORS"), human.output.join("\n"));
  assert.ok(human.output.includes("2026-10-04      6         3"), human.output.join("\n"));
  assert.ok(human.output.some((l) => /^news\.ycombinator\.com\s+9$/.test(l)));
  assert.ok(!human.output.some((l) => l.startsWith("COUNTRY")), "empty lists are left out");
});

test("analytics without an id reads the workspace report and lists the top pages", async (t) => {
  const rep = report({
    id: undefined, url: undefined, range: "30d",
    top_pages: [{ id: "ar_page1", title: "Launch\u001b[2J plan", url: "https://share.example/a/ar_page1/", views: 12, visitors: 7 }],
    top_paths: [{ id: "ar_page1", path: "/", views: 12 }],
  });
  t.mock.method(globalThis, "fetch", async (url) => {
    assert.equal(url.pathname, "/api/analytics");
    assert.equal(url.searchParams.has("range"), false);
    return Response.json(rep);
  });
  const human = await command(["analytics"]);
  assert.equal(human.code, 0);
  assert.equal(human.output[0], "all pages, last 30 days: 12 views, 7 visitors (40 all time)");
  assert.ok(human.output.some((l) => /^ar_page1\s+12\s+7\s+Launch\[2J plan$/.test(l)), human.output.join("\n"));
  assert.ok(!human.output.some((l) => l.includes("\u001b")), "control characters reach the terminal");
  assert.ok(human.output.some((l) => /^ar_page1 \/\s+12$/.test(l)));
});

test("analytics rejects an unknown range before making a request", async (t) => {
  t.mock.method(globalThis, "fetch", () => { throw new Error("must reject before networking"); });
  const res = await command(["analytics", "ar_page1", "--range", "1y", "--json"]);
  assert.equal(res.code, 2);
  assert.equal(JSON.parse(res.errors[0]).error.code, "USAGE");
});

test("analytics prints no series for a range without views", async () => {
  const { render } = await import("../src/commands/analytics.js");
  const lines = render(report({ totals: { all_time_views: 0, range_views: 0, range_visitors: 0 }, series: [{ bucket: "2026-10-04", views: 0, visitors: 0 }], top_referrers: [], top_paths: [], top_devices: [] }), "ar_page1");
  assert.deepEqual(lines, ["ar_page1, last 7 days: 0 views, 0 visitors (0 all time)"]);
});

test("detectAgent: BYAGENT_AGENT, then Codex, then Claude Code, then AI_AGENT", async () => {
  const { detectAgent, keyName, clientHeaders } = await import("../src/agent.js");
  assert.equal(detectAgent({}), "");
  assert.equal(detectAgent({ CLAUDECODE: "1" }), "claude-code");
  // Codex started from inside Claude Code inherits CLAUDECODE; Codex is the one publishing.
  assert.equal(detectAgent({ CLAUDECODE: "1", CODEX_THREAD_ID: "019" }), "codex");
  assert.equal(detectAgent({ CODEX_SANDBOX: "seatbelt" }), "codex");
  assert.equal(detectAgent({ AI_AGENT: "claude-code_2-1-289_agent" }), "claude-code");
  assert.equal(detectAgent({ AI_AGENT: "Not A Tool!" }), "");
  assert.equal(detectAgent({ CODEX_THREAD_ID: "1", BYAGENT_AGENT: "my-bot" }), "my-bot");

  assert.equal(keyName({ CLAUDECODE: "1" }, "soul-mbp.local"), "claude-code on soul-mbp");
  assert.equal(keyName({}, ""), "byagent-cli");
  assert.equal(clientHeaders({ CODEX_THREAD_ID: "1" })["X-Byagent-Client"], "codex");
  assert.equal(clientHeaders({})["X-Byagent-Client"], undefined);
  assert.match(clientHeaders({})["User-Agent"], /^byagent-cli\/\d/);
});

// withHome points os.homedir() at a temp dir so login writes nothing real.
function withHome(t) {
  const home = tmp();
  const prev = process.env.HOME;
  process.env.HOME = home;
  t.after(() => {
    process.env.HOME = prev;
  });
  return home;
}

test("login --code trades a one-time code for a key named after the agent and saves it", async (t) => {
  const home = withHome(t);
  const sent = [];
  t.mock.method(globalThis, "fetch", async (url, init) => {
    sent.push({ url: String(url), body: JSON.parse(init.body), client: init.headers["X-Byagent-Client"] });
    return Response.json(
      { status: "approved", token: "ak_secret123456", api: "https://api.example", key: { name: "codex on box", prefix: "ak_secret" }, workspace: { id: "t_1", name: "Ann" } },
      { status: 201 },
    );
  });
  const out = [];
  const code = await main(["login", "--code", "k7qd-m3xr", "--api", "https://api.example/", "--json"], {
    out: (l) => out.push(l),
    err: () => {},
    env: { CODEX_THREAD_ID: "1" },
  });
  assert.equal(code, 0);
  assert.equal(sent[0].url, "https://api.example/api/connect");
  assert.equal(sent[0].body.code, "k7qd-m3xr");
  assert.match(sent[0].body.name, /^codex on /);
  const res = JSON.parse(out[0]);
  assert.equal(res.ok, true);
  assert.deepEqual(res.workspace, { id: "t_1", name: "Ann" });
  assert.equal(JSON.stringify(res).includes("ak_secret123456"), false, "the key is never printed");
  assert.deepEqual(JSON.parse(readFileSync(configPath(home), "utf8")), { token: "ak_secret123456", api: "https://api.example" });
});

test("login --code reports a spent code without writing a config", async (t) => {
  const home = withHome(t);
  t.mock.method(globalThis, "fetch", async () =>
    Response.json({ error: { code: "NOT_FOUND", message: "this connect code has expired, was already used, or does not exist" } }, { status: 404 }),
  );
  const errs = [];
  const code = await main(["login", "--code", "K7QD-M3XR", "--json"], { out: () => {}, err: (l) => errs.push(l), env: {} });
  assert.equal(code, 1);
  assert.match(JSON.parse(errs[0]).error.message, /already used/);
  assert.throws(() => statSync(configPath(home)));
});

test("login --browser polls until approved; --no-wait and --wait split it in two", async (t) => {
  const { browserLogin } = await import("../src/commands/login.js");
  const home = tmp();
  const start = { device_code: "dc_x", user_code: "K7QD-M3XR", verify_url: "https://api.example/app/connect?code=K7QD-M3XR", expires_at: "2099-01-01T00:00:00Z", interval: 2 };
  const done = { status: "approved", token: "ak_new", api: "https://api.example", key: { name: "n" }, workspace: { name: "Ann" } };
  let polls = 0;
  t.mock.method(globalThis, "fetch", async (url) => {
    if (String(url).endsWith("/api/connect/device")) return Response.json(start, { status: 201 });
    polls++;
    return polls < 3 ? Response.json({ status: "pending", interval: 2 }, { status: 202 }) : Response.json(done, { status: 201 });
  });

  const later = await browserLogin("https://api.example", { name: "n", noWait: true, home, note: () => {} });
  assert.equal(later.json.pending, true);
  assert.equal(later.json.status, "awaiting_user");
  assert.equal(later.json.user_code, "K7QD-M3XR");
  // The handoff hands the agent the sentence for the person, and says to stop.
  const act = later.json.action_required;
  assert.equal(act.for, "user");
  assert.match(act.message, /https:\/\/api\.example\/app\/connect\?code=K7QD-M3XR/);
  assert.match(act.message, /K7QD-M3XR/);
  assert.ok(act.expires_in_seconds > 0);
  assert.match(later.json.instruction, /^Stop/);
  assert.equal(later.human[0], act.message);
  assert.equal(polls, 0);

  // Not approved within the timeout: exit 3, and the request is kept for --wait.
  const clock = fakeClock();
  await assert.rejects(browserLogin("https://api.example", { wait: true, timeoutMs: 1000, home, note: () => {}, ...clock }), (e) => e.exit === 3 && e.code === "PENDING");
  const res = await browserLogin("https://api.example", { wait: true, home, note: () => {}, ...fakeClock() });
  assert.equal(res.json.ok, true);
  assert.equal(polls, 3);
  assert.deepEqual(JSON.parse(readFileSync(configPath(home), "utf8")), { token: "ak_new", api: "https://api.example" });
  await assert.rejects(browserLogin("https://api.example", { wait: true, home, note: () => {} }), /nothing to wait for/);
});

test("login --wait gives up after 90 seconds by default, and says when the code expired", async (t) => {
  const { browserLogin, WAIT_MS } = await import("../src/commands/login.js");
  const home = tmp();
  const start = { device_code: "dc_y", user_code: "AB12-CD34", verify_url: "https://api.example/app/connect?code=AB12-CD34", expires_at: new Date(600_000).toISOString(), interval: 2 };
  t.mock.method(globalThis, "fetch", async (url) =>
    String(url).endsWith("/api/connect/device") ? Response.json(start, { status: 201 }) : Response.json({ status: "pending" }, { status: 202 }));
  await browserLogin("https://api.example", { name: "n", noWait: true, home, now: () => 0 });

  // No --timeout: back within WAIT_MS, before an agent's tool call is cut off.
  const clock = fakeClock();
  await assert.rejects(browserLogin("https://api.example", { wait: true, home, note: () => {}, ...clock }),
    (e) => e.exit === 3 && e.code === "PENDING" && /min left/.test(e.message));
  assert.ok(clock.now() <= WAIT_MS && WAIT_MS < 120_000);

  // Past the code's expiry: EXPIRED, the request is dropped, and it says how to start over.
  const late = fakeClock();
  await late.sleep(590_000);
  await assert.rejects(browserLogin("https://api.example", { wait: true, home, note: () => {}, ...late }),
    (e) => e.exit === 3 && e.code === "EXPIRED" && /login --browser --no-wait/.test(e.message));
  await assert.rejects(browserLogin("https://api.example", { wait: true, home, note: () => {} }), /nothing to wait for/);
});

test("init writes one byagent block, replaces it in place, and leaves the rest alone", async () => {
  const { init, BLOCK } = await import("../src/commands/init.js");
  const cwd = tmp();
  const home = tmp();
  writeFileSync(join(cwd, "CLAUDE.md"), "# Notes\nkeep me\n");
  const first = await init(null, { flags: {}, cwd, home });
  assert.deepEqual(first.json.files.map((f) => [f.path.split("/").pop(), f.action]), [["AGENTS.md", "created"], ["CLAUDE.md", "updated"]]);
  assert.equal(readFileSync(join(cwd, "AGENTS.md"), "utf8"), BLOCK + "\n");
  assert.equal(readFileSync(join(cwd, "CLAUDE.md"), "utf8"), "# Notes\nkeep me\n\n" + BLOCK + "\n");
  assert.match(BLOCK, /npx -y byagent@latest publish/);
  assert.match(BLOCK, /https:\/\/byagent\.dev\/skill\.md/);

  // Again: nothing changes. An old block is replaced, not duplicated.
  assert.ok((await init(null, { flags: {}, cwd, home })).json.files.every((f) => f.action === "unchanged"));
  writeFileSync(join(cwd, "AGENTS.md"), "top\n<!-- byagent -->\nold text\n<!-- /byagent -->\nbottom\n");
  await init(null, { flags: {}, cwd, home });
  assert.equal(readFileSync(join(cwd, "AGENTS.md"), "utf8"), "top\n" + BLOCK + "\nbottom\n");

  // A CLAUDE.md that only includes AGENTS.md is left alone.
  const cwd2 = tmp();
  writeFileSync(join(cwd2, "CLAUDE.md"), "@AGENTS.md\n");
  const r = await init(null, { flags: {}, cwd: cwd2, home });
  assert.equal(r.json.files[1].action, "skipped");
  assert.equal(readFileSync(join(cwd2, "CLAUDE.md"), "utf8"), "@AGENTS.md\n");

  // --global writes only for agents whose directory exists.
  mkdirSync(join(home, ".claude"));
  const g = await init(null, { flags: { global: true }, cwd, home });
  assert.deepEqual(g.json.files.map((f) => f.action), ["created", "skipped"]);
  assert.equal(readFileSync(join(home, ".claude", "CLAUDE.md"), "utf8"), BLOCK + "\n");
});

test("access sorts --allow into addresses and domains and replaces the list", async (t) => {
  const { splitEntries } = await import("../src/commands/access.js");
  assert.deepEqual(splitEntries(["sam@acme.com", "@acme.com", "other.org", "a@x.io,b@x.io"]),
    { emails: ["sam@acme.com", "a@x.io", "b@x.io"], domains: ["acme.com", "other.org"] });
  const calls = [];
  t.mock.method(globalThis, "fetch", async (url, options) => {
    calls.push({ url, ...options });
    return Response.json({ id: "artifact1234", mode: "restricted", visibility: "private", allowed_emails: ["sam@acme.com"], allowed_domains: ["acme.com"], url: "u", share_url: "https://share.example/a/artifact1234/" });
  });
  const res = await command(["access", "artifact1234", "--allow", "sam@acme.com", "--allow", "@acme.com", "--json"]);
  assert.equal(res.code, 0);
  assert.equal(calls[0].method, "PATCH");
  assert.equal(calls[0].url.pathname, "/api/artifacts/artifact1234/access");
  assert.deepEqual(JSON.parse(calls[0].body), { mode: "restricted", allowed_emails: ["sam@acme.com"], allowed_domains: ["acme.com"] });
  assert.equal(JSON.parse(res.output[0]).mode, "restricted");
});

test("access --add merges with the current list", async (t) => {
  const calls = [];
  t.mock.method(globalThis, "fetch", async (url, options) => {
    calls.push({ url, ...options });
    return Response.json({ id: "artifact1234", mode: "restricted", allowed_emails: ["sam@acme.com"], allowed_domains: [], url: "u", share_url: "s" });
  });
  const res = await command(["access", "artifact1234", "--add", "lee@x.org"]);
  assert.equal(res.code, 0);
  assert.equal(calls[0].method, "GET");
  assert.deepEqual(JSON.parse(calls[1].body).allowed_emails, ["sam@acme.com", "lee@x.org"]);
});

test("access never takes a password from argv and takes one mode at a time", async (t) => {
  t.mock.method(globalThis, "fetch", async () => assert.fail("nothing may be sent"));
  const argv = await command(["access", "artifact1234", "--password", "hunter2hunter2"]);
  assert.equal(argv.code, 2);
  assert.match(argv.errors[0], /never taken from the command line/);
  assert.equal((await command(["access", "artifact1234", "--password=hunter2hunter2"])).code, 2);
  assert.equal((await command(["access", "artifact1234", "--public", "--off"])).code, 2);
  assert.equal((await command(["publish", "./x", "--password", "--public"])).code, 2);
});

test("vars set takes --upstream more than once or comma-separated", () => {
  const { flags } = parseArgs(["vars", "set", "K", "--upstream", "a.example.com", "--upstream", "b.example.com, c.example.com"]);
  assert.deepEqual(upstreamsOf(flags), ["a.example.com", "b.example.com", "c.example.com"]);
  assert.deepEqual(upstreamsOf({}), []);
});

test("vars set refuses a value given as an argument before calling anything", async () => {
  const errs = [];
  const code = await main(["vars", "set", "K", "--upstream", "a.example.com", "--value", "sk-123", "--json"], {
    out: () => {},
    err: (l) => errs.push(l),
    env: { ARTIFACTS_TOKEN: "t", ARTIFACTS_API: "http://127.0.0.1:1" },
  });
  assert.equal(code, 2);
  assert.ok(!errs.join("").includes("sk-123"));
  assert.match(JSON.parse(errs[0]).error.message, /stdin/);
});

test("parseArgs treats --primary as a switch", () => {
  const a = parseArgs(["domain", "add", "docs.example.com", "--page", "ar_1", "--primary", "--path", "/x"]);
  assert.deepEqual(a.positional, ["add", "docs.example.com"]);
  assert.equal(a.flags.primary, true);
  assert.equal(a.flags.path, "/x");
});
