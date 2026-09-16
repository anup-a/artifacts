import { apiJson } from "../client.js";
import { collectFiles, readLink, writeLink } from "../files.js";
import { CliError, usage } from "../errors.js";

function manifestOf(files, title) {
  const m = { files: files.map((f) => ({ path: f.path, sha256: f.sha256, size: f.size })) };
  if (title) m.title = title;
  return m;
}

export async function publish(cfg, { positional, flags }) {
  const target = positional[0];
  if (!target) throw usage("usage: artifacts publish <dir|file.html> [--id <id>] [--title <t>] [--end-user <id>]");

  const { files, dir } = collectFiles(target);
  const manifest = manifestOf(files, flags.title);

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
  return {
    json: { id: res.id, url: res.url, version: res.version, end_user: res.end_user, files: files.length, bytes, uploaded: sent.size },
    human: [`${res.url}`, `version ${res.version}, ${files.length} files, ${bytes} bytes, ${sent.size} uploaded`],
  };
}
