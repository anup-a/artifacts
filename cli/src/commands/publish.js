import { apiJson } from "../client.js";
import { collectFiles, readLink, writeLink } from "../files.js";
import { CliError, usage } from "../errors.js";

// comments is true/false for --comments/--no-comments, undefined when neither
// is given so the server leaves the artifact's switch alone.
function manifestOf(files, title, comments) {
  const m = { files: files.map((f) => ({ path: f.path, sha256: f.sha256, size: f.size })) };
  if (title) m.title = title;
  if (comments !== undefined) m.comments_enabled = comments;
  return m;
}

function commentsFlag(flags) {
  if (flags.comments && flags["no-comments"]) throw usage("--comments and --no-comments are mutually exclusive");
  if (flags.comments) return true;
  if (flags["no-comments"]) return false;
  return undefined;
}

export async function publish(cfg, { positional, flags }) {
  const target = positional[0];
  if (!target) throw usage("usage: artifacts publish <dir|file.html> [--id <id>] [--title <t>] [--end-user <id>] [--comments|--no-comments]");

  const { files, dir } = collectFiles(target);
  const manifest = manifestOf(files, flags.title, commentsFlag(flags));

  // The link file only applies when it was written against this same API, so a
  // directory published to a staging server does not silently update production.
  const link = readLink(dir);
  const id = flags.id || (link.api === cfg.api ? link.id : "") || "";

  const pre = await apiJson(cfg, "POST", "/api/artifacts/preflight", {
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(manifest),
  });
  const missing = new Set(pre && Array.isArray(pre.missing) ? pre.missing : files.map((f) => f.sha256));

  const form = new FormData();
  // The server requires the manifest to be the first part.
  form.append("manifest", JSON.stringify(manifest));
  const sent = new Set();
  for (const f of files) {
    if (!missing.has(f.sha256) || sent.has(f.sha256)) continue;
    sent.add(f.sha256);
    form.append(f.sha256, new Blob([f.body]), f.sha256);
  }

  const headers = {};
  if (flags["end-user"] !== undefined) headers["X-Artifact-End-User"] = flags["end-user"];
  const res = id
    ? await apiJson(cfg, "PUT", `/api/artifacts/${encodeURIComponent(id)}`, { body: form, headers })
    : await apiJson(cfg, "POST", "/api/artifacts", { body: form, headers });
  if (!res || !res.id) throw new CliError("publish returned no artifact id", { code: "INTERNAL" });

  writeLink(dir, { id: res.id, api: cfg.api });

  const bytes = files.reduce((n, f) => n + f.size, 0);
  const commentsOn = res.comments_enabled !== false;
  return {
    json: {
      id: res.id, url: res.url, version: res.version, end_user: res.end_user, comments_enabled: commentsOn,
      files: files.length, bytes, uploaded: sent.size,
    },
    human: [
      `${res.url}`,
      `version ${res.version}, ${files.length} files, ${bytes} bytes, ${sent.size} uploaded${commentsOn ? "" : ", comments off"}`,
    ],
  };
}
