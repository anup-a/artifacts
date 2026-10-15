import { createServer } from "node:http";
import { readFileSync } from "node:fs";
import { extname } from "node:path";
import { fileURLToPath } from "node:url";
import { Store, localToken, newId, sha256 } from "./store.js";
import { indexPage } from "./home.js";

// The local server speaks the subset of the byagent API the CLI uses for
// pages and comments, so `byagent <command> --local` runs the same code
// paths as against byagent.dev. Pages are served at /a/<id>/ with the
// comment panel, and every byte stays on this machine.

const COMMENT_BYTES = 2048;
const ANCHOR_BYTES = 4096;
const MANIFEST_BYTES = 1 << 20;
const PAGE_BYTES = 50 << 20;

const TYPES = {
  ".html": "text/html; charset=utf-8", ".htm": "text/html; charset=utf-8", ".css": "text/css; charset=utf-8",
  ".js": "text/javascript; charset=utf-8", ".mjs": "text/javascript; charset=utf-8", ".json": "application/json",
  ".svg": "image/svg+xml", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
  ".webp": "image/webp", ".avif": "image/avif", ".ico": "image/x-icon", ".txt": "text/plain; charset=utf-8",
  ".md": "text/markdown; charset=utf-8", ".csv": "text/csv; charset=utf-8", ".xml": "application/xml",
  ".pdf": "application/pdf", ".woff": "font/woff", ".woff2": "font/woff2", ".ttf": "font/ttf", ".otf": "font/otf",
  ".mp4": "video/mp4", ".webm": "video/webm", ".mp3": "audio/mpeg", ".wav": "audio/wav", ".wasm": "application/wasm",
};

// These commands need the hosted service: sign-in, other people, the open
// internet. Local mode says so instead of pretending.
const HOSTED_ONLY = [
  [/^\/api\/artifacts\/[^/]+\/(share-code|access|expiry|analytics|state)$/, "sharing, access, expiry and analytics"],
  [/^\/api\/(analytics|variables|domains|end-users|collections)(\/|$)/, "this command"],
];

class HttpError extends Error {
  constructor(status, code, message) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

const invalid = (m) => new HttpError(400, "INVALID", m);
const notFound = (what) => new HttpError(404, "NOT_FOUND", `${what} not found`);

// runtimeBundle finds the comment panel script: packed into the CLI as
// runtime/comments.js, or in the repo's runtime build when run from source.
function runtimeBundle() {
  for (const rel of ["../../runtime/comments.js", "../../../runtime/dist/comments.js"]) {
    try {
      return readFileSync(fileURLToPath(new URL(rel, import.meta.url)));
    } catch {}
  }
  return null;
}

function send(res, status, body, headers = {}) {
  res.writeHead(status, { "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", ...headers });
  res.end(body);
}

function sendJSON(res, status, value) {
  send(res, status, JSON.stringify(value), { "Content-Type": "application/json" });
}

async function readBody(req, limit) {
  const chunks = [];
  let n = 0;
  for await (const c of req) {
    n += c.length;
    if (n > limit) throw new HttpError(413, "TOO_LARGE", `request body over ${limit} bytes`);
    chunks.push(c);
  }
  return Buffer.concat(chunks);
}

async function readJSON(req, limit) {
  const raw = await readBody(req, limit);
  if (!raw.length) return {};
  try {
    const v = JSON.parse(raw.toString("utf8"));
    if (v && typeof v === "object" && !Array.isArray(v)) return v;
  } catch {}
  throw invalid("body must be a JSON object");
}

// Comment text is plain text: tags are stripped and control characters other
// than newline and tab refused, as the hosted server does.
const TAG = /<[^>]*>/g;
const CONTROL = (allowWS) => (allowWS ? /[\u0000-\u0008\u000b-\u001f\u007f-\u009f]/ : /[\u0000-\u001f\u007f-\u009f]/);

function validName(n) {
  n = String(n ?? "");
  if (CONTROL(false).test(n)) throw invalid("author_name must not contain control characters");
  n = n.replace(TAG, "").trim();
  if (!n || [...n].length > 80) throw invalid("author_name must be 1..80 characters");
  return n;
}

function validBody(b, field = "body") {
  b = String(b ?? "");
  if (CONTROL(true).test(b)) throw invalid(`${field} must not contain control characters`);
  b = b.replace(TAG, "").trim();
  if (!b || Buffer.byteLength(b) > COMMENT_BYTES) throw invalid(`${field} must be 1..${COMMENT_BYTES} bytes`);
  return b;
}

function validAnchor(a) {
  if (!a || typeof a !== "object" || Array.isArray(a) || JSON.stringify(a).length > ANCHOR_BYTES) {
    throw invalid(`anchor must be a JSON object up to ${ANCHOR_BYTES} bytes`);
  }
  if (a.kind === "text" && a.quote) return a;
  if (a.kind === "element" && a.css) return a;
  throw invalid("anchor.kind must be text (with quote) or element (with css)");
}

function validPath(p) {
  if (typeof p !== "string" || !p || p.length > 512 || p.startsWith("/") || p.includes("\\") || p.split("/").some((s) => !s || s === "." || s === "..")) {
    throw invalid(`bad file path ${JSON.stringify(p)}`);
  }
  return p;
}

function validManifest(m) {
  if (!m || !Array.isArray(m.files) || !m.files.length) throw invalid("manifest needs at least one file");
  const seen = new Set();
  let total = 0;
  for (const f of m.files) {
    validPath(f.path);
    if (seen.has(f.path)) throw invalid(`duplicate path ${f.path}`);
    seen.add(f.path);
    if (!/^[0-9a-f]{64}$/.test(f.sha256 || "")) throw invalid(`bad sha256 for ${f.path}`);
    total += Number(f.size) || 0;
  }
  if (total > PAGE_BYTES) throw new HttpError(413, "TOO_LARGE", `a page is at most ${PAGE_BYTES >> 20} MB`);
  return m;
}

function injectRuntime(html, id, version, base) {
  const tag = `<script src="/_rt/comments.js" data-artifact="${id}" data-version="${version}" data-base="${base}" defer></script>`;
  const lower = html.toLowerCase();
  const i = lower.lastIndexOf("</body>");
  return i < 0 ? html + tag : html.slice(0, i) + tag + html.slice(i);
}

export function createLocalServer({ dir, port, now = () => new Date() }) {
  const store = new Store(dir);
  const token = localToken(dir);
  const runtime = runtimeBundle();
  const origin = `http://localhost:${port}`;
  const hosts = new Set([`localhost:${port}`, `127.0.0.1:${port}`, `[::1]:${port}`]);

  const pageURL = (a) => `${origin}/a/${a.id}/`;
  const iso = () => now().toISOString();
  const live = (a) => a.versions.find((v) => v.version === a.current_version);
  const openCount = (a) => a.threads.filter((t) => t.status === "open").length;

  function artifactView(a) {
    return {
      id: a.id, title: a.title, url: pageURL(a), current_version: a.current_version, versions_count: a.versions.length,
      open_comments: openCount(a), comments_enabled: a.comments_enabled, end_user: "", visibility: "local",
      project: a.project || null, tags: a.tags || [], created_at: a.created_at, updated_at: a.updated_at,
      published_at: a.created_at, local: true,
    };
  }

  function owned(id) {
    const a = store.artifact(id);
    if (!a) throw notFound("artifact");
    return a;
  }

  function thread(a, id) {
    const t = a.threads.find((x) => x.id === id);
    if (!t) throw notFound("thread");
    return t;
  }

  function comment(kind, name, body) {
    return { id: newId("c_"), author_name: name, author_kind: kind, body, created_at: iso(), author_verified: false };
  }

  // publish takes the multipart upload the CLI sends: the manifest first, then
  // one part per new file, named by its sha256.
  async function publish(req, a) {
    const type = req.headers["content-type"] || "";
    if (!type.startsWith("multipart/form-data")) throw invalid("expected multipart/form-data");
    const raw = await readBody(req, PAGE_BYTES + MANIFEST_BYTES);
    const form = await new Response(raw, { headers: { "Content-Type": type } }).formData();
    let m;
    try {
      m = JSON.parse(String(form.get("manifest") || ""));
    } catch {
      throw invalid("manifest must be JSON");
    }
    validManifest(m);
    for (const [name, value] of form.entries()) {
      if (name === "manifest" || typeof value === "string") continue;
      const body = Buffer.from(await value.arrayBuffer());
      if (sha256(body) !== name) throw invalid(`file ${name} does not match its hash`);
      store.putBlob(name, body);
    }
    const missing = m.files.filter((f) => !store.hasBlob(f.sha256)).map((f) => f.path);
    if (missing.length) throw invalid(`files not uploaded: ${missing.join(", ")}`);

    const at = iso();
    const created = !a;
    if (created) a = { id: newId(), created_at: at, versions: [], threads: [], comments_enabled: true, current_version: 0, next_version: 1 };
    const version = {
      version: a.next_version || a.versions.length + 1, created_at: at,
      files: m.files.map((f) => ({ path: f.path, sha256: f.sha256, size: Number(f.size) || 0 })),
      agent: String(m.agent || "").slice(0, 64), model: String(m.model || "").slice(0, 64),
    };
    a.versions.push(version);
    a.next_version = version.version + 1;
    a.current_version = version.version;
    a.updated_at = at;
    if (m.title) a.title = String(m.title).slice(0, 200);
    if (!a.title) a.title = "";
    if (typeof m.comments_enabled === "boolean") a.comments_enabled = m.comments_enabled;
    if (m.project !== undefined) a.project = m.project ? String(m.project).slice(0, 64) : null;
    if (Array.isArray(m.tags)) a.tags = m.tags.slice(0, 10).map((t) => String(t).slice(0, 32));
    store.put(a);
    return [created ? 201 : 200, { ...artifactView(a), version: version.version }];
  }

  function versionsView(a) {
    const counts = {};
    for (const t of a.threads) counts[t.version] = (counts[t.version] || 0) + 1;
    return {
      id: a.id, current_version: a.current_version,
      versions: a.versions.map((v) => ({
        version: v.version, created_at: v.created_at, bytes: v.files.reduce((n, f) => n + f.size, 0), files: v.files.length,
        comment_count: counts[v.version] || 0, agent: v.agent, model: v.model, key: "local", current: v.version === a.current_version,
        url: v.version === a.current_version ? pageURL(a) : `${pageURL(a)}v/${v.version}/`,
        ...(v.restored_at ? { restored_at: v.restored_at } : {}),
      })),
    };
  }

  function versionOf(a, n) {
    const v = a.versions.find((x) => x.version === Number(n));
    if (!v) throw notFound("version");
    return v;
  }

  function diff(a, q) {
    const from = versionOf(a, q.get("from"));
    const to = q.get("to") ? versionOf(a, q.get("to")) : live(a);
    const before = new Map(from.files.map((f) => [f.path, f]));
    const after = new Map(to.files.map((f) => [f.path, f]));
    const added = [], removed = [], changed = [];
    let unchanged = 0;
    for (const [p, f] of after) {
      const b = before.get(p);
      if (!b) added.push({ path: p, size: f.size });
      else if (b.sha256 !== f.sha256) changed.push({ path: p, from_size: b.size, to_size: f.size });
      else unchanged++;
    }
    for (const [p, f] of before) if (!after.has(p)) removed.push({ path: p, size: f.size });
    return { id: a.id, from: from.version, to: to.version, added, removed, changed, unchanged };
  }

  function threadsOf(a, status, since) {
    const sinceMs = since ? Date.parse(since) : NaN;
    return a.threads.filter((t) => (!status || t.status === status) && (Number.isNaN(sinceMs) || Date.parse(t.created_at) > sinceMs));
  }

  async function ownerAPI(req, res, url) {
    if (req.headers.authorization !== `Bearer ${token}`) throw new HttpError(401, "UNAUTHORIZED", "this local server wants the key in its data folder");
    const path = url.pathname;
    const m = req.method;
    for (const [re, what] of HOSTED_ONLY) {
      if (re.test(path)) throw new HttpError(400, "LOCAL_UNSUPPORTED", `${what} needs byagent.dev; publish again without --local`);
    }
    if (path === "/api/me" && m === "GET") return [200, { local: true, workspace: "local", data_dir: dir }];
    if (path === "/api/artifacts/preflight" && m === "POST") {
      const man = validManifest(await readJSON(req, MANIFEST_BYTES));
      return [200, { missing: [...new Set(man.files.map((f) => f.sha256).filter((h) => !store.hasBlob(h)))] }];
    }
    if (path === "/api/artifacts" && m === "POST") return publish(req, null);
    if (path === "/api/artifacts" && m === "GET") {
      const q = (url.searchParams.get("q") || "").toLowerCase();
      const project = url.searchParams.get("project");
      const rows = store.all()
        .filter((a) => (!project || a.project === project) && (!q || (a.title || "").toLowerCase().includes(q) || a.id.includes(q)))
        .sort((x, y) => (x.updated_at < y.updated_at ? 1 : -1))
        .map(artifactView);
      return [200, { artifacts: rows, next_cursor: "" }];
    }
    const parts = path.split("/").slice(3).map(decodeURIComponent);
    const a = owned(parts[0]);
    const [, sub, x, y] = parts;
    if (!sub) {
      if (m === "GET") return [200, artifactView(a)];
      if (m === "PUT") return publish(req, a);
      if (m === "DELETE") {
        store.remove(a.id);
        return [204, null];
      }
    }
    if (sub === "versions" && !x && m === "GET") return [200, versionsView(a)];
    if (sub === "versions" && x && m === "DELETE") {
      const v = versionOf(a, x);
      if (v.version === a.current_version) throw new HttpError(409, "CONFLICT", "the live version cannot be deleted; roll back to another first");
      a.versions = a.versions.filter((z) => z !== v);
      let orphaned = 0;
      for (const t of a.threads) if (t.version === v.version && t.anchor_status !== "orphaned") (t.anchor_status = "orphaned"), orphaned++;
      store.put(a);
      return [200, { id: a.id, deleted_version: v.version, current_version: a.current_version, versions_count: a.versions.length, orphaned_comments: orphaned }];
    }
    if (sub === "rollback" && x && m === "POST") {
      const v = versionOf(a, x);
      const previous = a.current_version;
      a.current_version = v.version;
      v.restored_at = iso();
      a.updated_at = v.restored_at;
      store.put(a);
      return [200, { id: a.id, url: pageURL(a), version: v.version, current_version: v.version, previous_version: previous }];
    }
    if (sub === "diff" && m === "GET") return [200, diff(a, url.searchParams)];
    if (sub === "files" && m === "GET") {
      const want = parts.slice(2).join("/");
      const f = live(a).files.find((z) => z.path === want);
      if (!f) throw notFound("file");
      return [200, store.blob(f.sha256), TYPES[extname(f.path).toLowerCase()] || "application/octet-stream"];
    }
    if (sub === "comments" && !x && m === "GET") {
      const status = url.searchParams.get("status") || "open";
      if (!["open", "resolved", "all"].includes(status)) throw invalid("status must be open, resolved or all");
      return [200, { threads: threadsOf(a, status === "all" ? "" : status, url.searchParams.get("since")) }];
    }
    if (sub === "comments" && x && m === "POST") {
      const t = thread(a, x);
      if (y === "reply") {
        const c = comment("owner", "Author", validBody((await readJSON(req, COMMENT_BYTES * 2)).body));
        t.comments.push(c);
        store.put(a);
        return [201, c];
      }
      if (y === "resolve") {
        if (t.status !== "resolved") (t.status = "resolved"), (t.resolved_at = iso());
        store.put(a);
        return [200, t];
      }
      if (y === "progress") {
        const body = await readJSON(req, COMMENT_BYTES * 2);
        if (!["seen", "working", "declined"].includes(body.progress)) throw invalid("progress must be seen, working or declined");
        if (body.progress === "declined" && !body.note) throw invalid("declined needs a note");
        if (body.note) t.comments.push(comment("owner", "Author", validBody(body.note, "note")));
        t.progress = body.progress;
        if (body.progress === "declined" && t.status !== "resolved") (t.status = "resolved"), (t.resolved_at = iso());
        store.put(a);
        return [200, t];
      }
    }
    throw notFound("route");
  }

  // The viewer API is what the comment panel in a page calls. It needs no
  // key, but only a page from this server may write to it.
  async function viewerAPI(req, res, url) {
    const [, , id, sub, x, y] = url.pathname.split("/").map(decodeURIComponent);
    const a = store.artifact(id);
    if (!a || !a.comments_enabled) throw notFound("artifact");
    if (sub === "meta" && req.method === "GET") return [200, { current_version: a.current_version, versions: a.versions.map((v) => v.version).sort((p, q) => p - q) }];
    if (sub !== "comments") throw notFound("route");
    if (!x && req.method === "GET") {
      const status = url.searchParams.get("status") || "open";
      if (!["open", "all"].includes(status)) throw invalid("status must be open or all");
      return [200, { threads: threadsOf(a, status === "all" ? "" : status) }];
    }
    if (req.method !== "POST") throw notFound("route");
    const body = await readJSON(req, ANCHOR_BYTES + COMMENT_BYTES * 2 + 1024);
    if (!x) {
      const name = validName(body.author_name);
      const text = validBody(body.body);
      const anchor = validAnchor(body.anchor);
      if (!a.versions.some((v) => v.version === body.version)) throw invalid("version does not exist");
      const suggestion = body.suggestion ? validBody(body.suggestion, "suggestion") : "";
      const t = {
        id: newId("t_"), version: body.version, status: "open", anchor_status: "unknown", progress: "", suggestion, anchor,
        created_at: iso(), resolved_at: null, comments: [comment("viewer", name, text)],
      };
      a.threads.push(t);
      store.put(a);
      return [201, t];
    }
    const t = thread(a, x);
    if (y === "reply") {
      const c = comment("viewer", validName(body.author_name), validBody(body.body));
      t.comments.push(c);
      store.put(a);
      return [201, c];
    }
    if (y === "seen") {
      if (["attached", "drifted", "orphaned"].includes(body.anchor_status) && body.version === a.current_version) {
        t.anchor_status = body.anchor_status;
        store.put(a);
      }
      return [204, null];
    }
    throw notFound("route");
  }

  // page serves /a/<id>/ (the live version) and /a/<id>/v/<n>/ (an old one).
  // HTML gets the comment panel unless comments are off.
  function page(res, url) {
    const parts = url.pathname.split("/").slice(2).map(decodeURIComponent);
    const a = store.artifact(parts[0]);
    if (!a) return send(res, 404, "No local page with that id.", { "Content-Type": "text/plain; charset=utf-8" });
    let rest = parts.slice(1);
    let v = live(a);
    let base = `/a/${a.id}/`;
    if (rest[0] === "v" && /^\d+$/.test(rest[1] || "")) {
      v = a.versions.find((z) => z.version === Number(rest[1]));
      if (!v) return send(res, 404, "No such version.", { "Content-Type": "text/plain; charset=utf-8" });
      base = `/a/${a.id}/v/${v.version}/`;
      rest = rest.slice(2);
    }
    if (parts.length === 1 || (rest.length === 0 && !url.pathname.endsWith("/"))) {
      return send(res, 308, "", { Location: url.pathname + "/" });
    }
    let want = rest.join("/");
    if (!want || want.endsWith("/")) want += "index.html";
    let f = v.files.find((z) => z.path === want) || v.files.find((z) => z.path === want + "/index.html") || v.files.find((z) => z.path === want + ".html");
    if (!f) return send(res, 404, "Not found.", { "Content-Type": "text/plain; charset=utf-8" });
    const type = TYPES[extname(f.path).toLowerCase()] || "application/octet-stream";
    let body = store.blob(f.sha256);
    if (type.startsWith("text/html") && a.comments_enabled && runtime) {
      body = injectRuntime(body.toString("utf8"), a.id, v.version, `/a/${a.id}/`);
    }
    send(res, 200, body, { "Content-Type": type, "Referrer-Policy": "no-referrer" });
  }

  async function route(req, res) {
    // Only this machine's own names: a page on some other site cannot reach
    // the server through a domain that resolves to 127.0.0.1.
    if (!hosts.has(req.headers.host || "")) return send(res, 421, "wrong host");
    const url = new URL(req.url, origin);
    const p = url.pathname;
    try {
      if (p === "/api/health") return sendJSON(res, 200, { ok: true, local: true, data_dir: dir });
      if (p.startsWith("/api/")) {
        const [status, value, type] = await ownerAPI(req, res, url);
        if (status === 204) return send(res, 204, "");
        return type ? send(res, status, value, { "Content-Type": type }) : sendJSON(res, status, value);
      }
      if (p.startsWith("/c/")) {
        const o = req.headers.origin;
        if (req.method !== "GET" && o && o !== origin && o !== `http://127.0.0.1:${port}`) throw new HttpError(403, "FORBIDDEN", "cross-origin write");
        const [status, value] = await viewerAPI(req, res, url);
        return status === 204 ? send(res, 204, "") : sendJSON(res, status, value);
      }
      if (p === "/_rt/comments.js") {
        if (!runtime) return send(res, 404, "");
        return send(res, 200, runtime, { "Content-Type": "text/javascript; charset=utf-8" });
      }
      if (p.startsWith("/a/")) return page(res, url);
      if (p === "/" || p === "/index.html") {
        const rows = store.all().sort((x, y) => (x.updated_at < y.updated_at ? 1 : -1)).map(artifactView);
        return send(res, 200, indexPage(rows, dir), { "Content-Type": "text/html; charset=utf-8" });
      }
      send(res, 404, "Not found.", { "Content-Type": "text/plain; charset=utf-8" });
    } catch (e) {
      const err = e instanceof HttpError ? e : new HttpError(500, "INTERNAL", e.message);
      sendJSON(res, err.status, { error: { code: err.code, message: err.message } });
    }
  }

  return createServer((req, res) => {
    route(req, res);
  });
}
