import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { parseArgs } from "../src/args.js";
import { resolveConfig, saveConfig, configPath } from "../src/config.js";
import { collectFiles, readLink, writeLink } from "../src/files.js";
import { publish } from "../src/commands/publish.js";
import { list } from "../src/commands/artifacts.js";
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
  assert.match(JSON.parse(result.errors[0]).error.message, /usage: artifacts portal-link/);
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
  assert.match(help, /list.*--end-user/);
  assert.match(help, /artifacts whoami/);
  assert.match(help, /artifacts portal-link <end-user id>/);
  assert.match(help, /https:\/\/api\.example\/app\/keys/);
  assert.match(help, /POST \/api\/keys.*removed/);
  const explicit = await command(["login", "--api", "https://custom.example/", "--help"]);
  assert.match(explicit.output.join("\n"), /https:\/\/custom\.example\/app\/keys/);
});
