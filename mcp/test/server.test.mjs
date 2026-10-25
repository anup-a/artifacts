import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { createServer } from "node:http";
import { createMcpServer } from "../server.js";

// A fake byagent API that records what the MCP server sent it.
const seen = [];
let api, mcp, mcpURL;

function listen(server) {
  return new Promise((resolve) => server.listen(0, "127.0.0.1", () => resolve(`http://127.0.0.1:${server.address().port}`)));
}

before(async () => {
  api = createServer(async (req, res) => {
    const chunks = [];
    for await (const c of req) chunks.push(c);
    const body = Buffer.concat(chunks);
    seen.push({ method: req.method, url: req.url, auth: req.headers.authorization, type: req.headers["content-type"] || "", body });
    const json = (status, v) => (res.writeHead(status, { "Content-Type": "application/json" }), res.end(JSON.stringify(v)));
    if (req.headers.authorization !== "Bearer sk_good") return json(401, { error: { code: "UNAUTHORIZED", message: "invalid key" } });
    if (req.url === "/api/artifacts/preflight") return json(200, { missing: [JSON.parse(body).files[0].sha256] });
    if (req.method === "POST" && req.url === "/api/artifacts") {
      return json(201, { id: "a1", url: "https://a1.byagent.dev/", version: 1, visibility: "public", title: "Hello" });
    }
    if (req.method === "PUT" && req.url === "/api/artifacts/a1") return json(200, { id: "a1", url: "https://a1.byagent.dev/", version: 2 });
    if (req.url.startsWith("/api/artifacts?")) {
      return json(200, { artifacts: [{ id: "b1", url: "u", project: "p", tags: ["brief"], current_version: 3, open_comments: 0 }] });
    }
    if (req.url === "/api/artifacts/b1/files/brief.md") return (res.writeHead(200), res.end("# Brief"));
    return json(404, { error: { code: "NOT_FOUND", message: "no such route" } });
  });
  const apiURL = await listen(api);
  mcp = createMcpServer({ api: apiURL });
  mcpURL = (await listen(mcp)) + "/mcp";
});

after(() => {
  api.close();
  mcp.close();
});

async function rpc(body, token = "sk_good") {
  const res = await fetch(mcpURL, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json, text/event-stream", ...(token ? { Authorization: `Bearer ${token}` } : {}) },
    body: JSON.stringify(body),
  });
  const text = await res.text();
  return { status: res.status, body: text ? JSON.parse(text) : null };
}

const call = (name, args, token) => rpc({ jsonrpc: "2.0", id: 1, method: "tools/call", params: { name, arguments: args } }, token);
const resultOf = (r) => JSON.parse(r.body.result.content[0].text);

test("initialize echoes a supported protocol version and falls back to the newest", async () => {
  const a = await rpc({ jsonrpc: "2.0", id: 1, method: "initialize", params: { protocolVersion: "2025-03-26" } }, "");
  assert.equal(a.body.result.protocolVersion, "2025-03-26");
  assert.equal(a.body.result.serverInfo.name, "byagent");
  const b = await rpc({ jsonrpc: "2.0", id: 1, method: "initialize", params: { protocolVersion: "1999-01-01" } }, "");
  assert.equal(b.body.result.protocolVersion, "2025-06-18");
});

test("a notification gets 202 and no body", async () => {
  const r = await rpc({ jsonrpc: "2.0", method: "notifications/initialized" });
  assert.equal(r.status, 202);
  assert.equal(r.body, null);
});

test("tools/list works without a key and includes publish", async () => {
  const r = await rpc({ jsonrpc: "2.0", id: 2, method: "tools/list" }, "");
  const names = r.body.result.tools.map((t) => t.name);
  assert.ok(names.includes("artifact_publish"));
  assert.ok(names.includes("brief_get"));
});

test("tools/call without a key is a tool error, and nothing reaches the API", async () => {
  const before = seen.length;
  const r = await call("artifact_list", {}, "");
  assert.equal(r.body.result.isError, true);
  assert.match(r.body.result.content[0].text, /missing byagent key/);
  assert.equal(seen.length, before);
});

test("publish renders Markdown, preflights, then uploads manifest first", async () => {
  seen.length = 0;
  const r = await call("artifact_publish", { markdown: "# Hello\n\nworld", project: "demo", private: false });
  const out = resultOf(r);
  assert.equal(out.url, "https://a1.byagent.dev/");
  const [pre, post] = seen;
  assert.equal(pre.url, "/api/artifacts/preflight");
  const manifest = JSON.parse(pre.body);
  assert.equal(manifest.title, "Hello");
  assert.equal(manifest.project, "demo");
  assert.equal(manifest.visibility, "public");
  assert.equal(manifest.files[0].path, "index.html");
  assert.match(post.type, /^multipart\/form-data/);
  const text = post.body.toString("utf8");
  assert.ok(text.indexOf('name="manifest"') < text.indexOf(manifest.files[0].sha256 + '"'));
  assert.match(text, /<h1 id="hello">Hello/);
});

test("publish with id puts a new version", async () => {
  seen.length = 0;
  const out = resultOf(await call("artifact_publish", { html: "<title>T</title><p>x</p>", id: "a1" }));
  assert.equal(out.version, 2);
  assert.equal(seen[1].method, "PUT");
  assert.equal(JSON.parse(seen[0].body).title, "T");
});

test("publish needs exactly one of markdown or html", async () => {
  const r = await call("artifact_publish", { markdown: "a", html: "<p>b</p>" });
  assert.equal(r.body.result.isError, true);
});

test("an API error comes back as a readable tool error", async () => {
  const r = await call("artifact_list", {}, "sk_bad");
  assert.equal(r.body.result.isError, true);
  assert.match(r.body.result.content[0].text, /invalid key/);
});

test("brief_get reads the brief source", async () => {
  const out = resultOf(await call("brief_get", { collection: "p" }));
  assert.equal(out.artifact.id, "b1");
  assert.equal(out.content, "# Brief");
});

test("X-Byagent-Token works in place of Authorization", async () => {
  const res = await fetch(mcpURL, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Byagent-Token": "sk_good" },
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/call", params: { name: "brief_get", arguments: { collection: "p" } } }),
  });
  const body = await res.json();
  assert.notEqual(body.result.isError, true);
});

test("GET /mcp is 405 and unknown methods are JSON-RPC errors", async () => {
  assert.equal((await fetch(mcpURL)).status, 405);
  const r = await rpc({ jsonrpc: "2.0", id: 9, method: "resources/list" });
  assert.equal(r.body.error.code, -32601);
});
