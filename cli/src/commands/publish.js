import { createHash } from "node:crypto";
import { copyFileSync, readFileSync, statSync } from "node:fs";
import { extname, join, relative, resolve, sep } from "node:path";
import { apiJson } from "../client.js";
import { collectFiles, readLink, writeLink } from "../files.js";
import { CliError, usage } from "../errors.js";

const USAGE = "usage: artifacts publish <dir|file.html> [--id <id>] [--title <t>] [--end-user <id>] [--comments|--no-comments] [--project <name>] [--tag <t>]... [--cover <file>] [--cover-style <name>]";

// Image types the server will stream as a cover.
const COVER_EXTS = new Set([".svg", ".png", ".jpg", ".jpeg", ".webp"]);

// comments is true/false for --comments/--no-comments, undefined when neither
// is given so the server leaves the artifact's switch alone.
function manifestOf(files, title, comments, labels) {
  const m = { files: files.map((f) => ({ path: f.path, sha256: f.sha256, size: f.size })) };
  if (title) m.title = title;
  if (comments !== undefined) m.comments_enabled = comments;
  if (labels.project !== undefined) m.project = labels.project;
  if (labels.tags !== undefined) m.tags = labels.tags;
  if (labels.cover !== undefined) m.cover = labels.cover;
  if (labels.coverStyle !== undefined) m.cover_style = labels.coverStyle;
  return m;
}

function commentsFlag(flags) {
  if (flags.comments && flags["no-comments"]) throw usage("--comments and --no-comments are mutually exclusive");
  if (flags.comments) return true;
  if (flags["no-comments"]) return false;
  return undefined;
}

// resolveCover returns the manifest path for --cover. A file inside the
// published directory is referenced as is; anything else is copied into the
// directory as _cover.<ext> so it ships with the page. With a single-file
// publish there is no directory, so the cover is added to the upload directly.
export function resolveCover(coverFlag, dir, files) {
  if (coverFlag === undefined) return { cover: undefined, files };
  const abs = resolve(coverFlag);
  const ext = extname(abs).toLowerCase();
  if (!COVER_EXTS.has(ext)) throw usage(`--cover must be an svg, png, jpg or webp file: ${coverFlag}`);
  let st;
  try {
    st = statSync(abs);
  } catch {
    throw usage(`no such cover file: ${coverFlag}`);
  }
  if (!st.isFile()) throw usage(`--cover must be a file: ${coverFlag}`);
  if (dir) {
    const rel = relative(resolve(dir), abs);
    const inside = rel && !rel.startsWith("..") && !rel.startsWith(sep) && !/^[A-Za-z]:/.test(rel);
    if (inside) {
      const path = rel.split(sep).join("/");
      if (!files.some((f) => f.path === path)) throw usage(`--cover ${coverFlag} is inside the directory but not publishable (hidden or skipped)`);
      return { cover: path, files };
    }
    const name = `_cover${ext}`;
    copyFileSync(abs, join(dir, name));
    return { cover: name, files: collectFiles(dir).files };
  }
  const name = `_cover${ext}`;
  const body = readFileSync(abs);
  const file = { path: name, abs, body, sha256: createHash("sha256").update(body).digest("hex"), size: body.length };
  return { cover: name, files: [...files.filter((f) => f.path !== name), file] };
}

// labelsOf merges the flags with what the link file remembers. Flags win; a
// republish without flags keeps the remembered project and tags.
export function labelsOf(flags, link) {
  const labels = {};
  if (flags.project !== undefined) labels.project = flags.project;
  else if (link.project) labels.project = link.project;
  if (flags.tag !== undefined) labels.tags = flags.tag;
  else if (Array.isArray(link.tags) && link.tags.length) labels.tags = link.tags;
  if (flags["cover-style"] !== undefined) labels.coverStyle = flags["cover-style"];
  return labels;
}

export async function publish(cfg, { positional, flags }) {
  const target = positional[0];
  if (!target) throw usage(USAGE);

  const collected = collectFiles(target);
  const dir = collected.dir;
  const link = readLink(dir);
  const labels = labelsOf(flags, link.api === cfg.api ? link : {});
  const { cover, files } = resolveCover(flags.cover, dir, collected.files);
  if (cover !== undefined) labels.cover = cover;
  const manifest = manifestOf(files, flags.title, commentsFlag(flags), labels);

  // The link file only applies when it was written against this same API, so a
  // directory published to a staging server does not silently update production.
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

  const project = res.project === undefined ? labels.project : res.project;
  const tags = Array.isArray(res.tags) ? res.tags : labels.tags || [];
  writeLink(dir, { id: res.id, api: cfg.api, project, tags });

  const bytes = files.reduce((n, f) => n + f.size, 0);
  const commentsOn = res.comments_enabled !== false;
  const labelLine = [project ? `project ${project}` : "", tags.length ? `tags ${tags.join(", ")}` : ""].filter(Boolean).join(", ");
  return {
    json: {
      id: res.id, url: res.url, version: res.version, end_user: res.end_user, comments_enabled: commentsOn,
      project: project ?? null, tags, cover_url: res.cover_url, cover_style: res.cover_style,
      files: files.length, bytes, uploaded: sent.size,
    },
    human: [
      `${res.url}`,
      `version ${res.version}, ${files.length} files, ${bytes} bytes, ${sent.size} uploaded${commentsOn ? "" : ", comments off"}`,
      ...(labelLine ? [labelLine] : []),
    ],
  };
}
