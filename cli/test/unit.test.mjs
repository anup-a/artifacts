import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { parseArgs } from "../src/args.js";
import { resolveConfig, saveConfig, configPath } from "../src/config.js";
import { collectFiles, readLink, writeLink } from "../src/files.js";
import { publish, labelsOf, resolveCover } from "../src/commands/publish.js";
import { list, share } from "../src/commands/artifacts.js";
import { main } from "../src/main.js";

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
  assert.match(help, /byagent collections\n/);
  assert.match(help, /byagent collection <name>/);
  assert.match(help, /https:\/\/api\.example\/app\/keys/);
  assert.match(help, /POST \/api\/keys.*removed/);
  const explicit = await command(["login", "--api", "https://custom.example/", "--help"]);
  assert.match(explicit.output.join("\n"), /https:\/\/custom\.example\/app\/keys/);
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
