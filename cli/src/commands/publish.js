import { createHash } from "node:crypto";
import { guestLines } from "../guest.js";
import { parseWhen, setExpiry } from "./artifacts.js";
import { copyFileSync, readFileSync, statSync } from "node:fs";
import { dirname, extname, join, relative, resolve, sep } from "node:path";
import { apiJson } from "../client.js";
import { collectFiles, readLink, writeLink } from "../files.js";
import { findProject } from "../project.js";
import { CliError, usage } from "../errors.js";

const USAGE = "usage: byagent publish <dir|file.html|file.md> [--id <id>] [--title <t>] [--end-user <id>] [--private|--public] [--comments|--no-comments] [--project <name>] [--tag <t>]... [--cover <file>] [--cover-style <name>]";

// Image types the server will stream as a cover.
const COVER_EXTS = new Set([".svg", ".png", ".jpg", ".jpeg", ".webp"]);

// comments is true/false for --comments/--no-comments, undefined when neither
// is given so the server leaves the artifact's switch alone.
function manifestOf(files, title, comments, labels, visibility) {
  const m = { files: files.map((f) => ({ path: f.path, sha256: f.sha256, size: f.size })) };
  if (title) m.title = title;
  if (comments !== undefined) m.comments_enabled = comments;
  if (visibility !== undefined) m.visibility = visibility;
  if (labels.project !== undefined) m.project = labels.project;
  if (labels.tags !== undefined) m.tags = labels.tags;
  if (labels.cover !== undefined) m.cover = labels.cover;
  if (labels.coverStyle !== undefined) m.cover_style = labels.coverStyle;
  return m;
}

// provenanceOf says what produced this version, for the history: --agent and
// --model win, then BYAGENT_AGENT / BYAGENT_MODEL. Claude Code is detected by
// the CLAUDECODE=1 it sets for the commands it runs; nothing else is guessed.
export function provenanceOf(flags, env = process.env) {
  const agent = flags.agent || env.BYAGENT_AGENT || (env.CLAUDECODE === "1" ? "claude-code" : "");
  const model = flags.model || env.BYAGENT_MODEL || "";
  const out = {};
  if (agent) out.agent = String(agent).slice(0, 64);
  if (model) out.model = String(model).slice(0, 64);
  return out;
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
export function resolveCover(coverFlag, dir, files, title) {
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
    return { cover: name, files: collectFiles(dir, { title }).files };
  }
  const name = `_cover${ext}`;
  const body = readFileSync(abs);
  const file = { path: name, abs, body, sha256: createHash("sha256").update(body).digest("hex"), size: body.length };
  return { cover: name, files: [...files.filter((f) => f.path !== name), file] };
}

// labelsOf merges the flags with what the link file remembers. Flags win; a
// republish without flags keeps the remembered project and tags. With neither,
// the folder's .byagent.json collection is the project.
export function labelsOf(flags, link, collection) {
  const labels = {};
  if (flags.project !== undefined) labels.project = flags.project;
  else if (link.project) labels.project = link.project;
  else if (collection) labels.project = collection;
  if (flags.tag !== undefined) labels.tags = flags.tag;
  else if (Array.isArray(link.tags) && link.tags.length) labels.tags = link.tags;
  if (flags["cover-style"] !== undefined) labels.coverStyle = flags["cover-style"];
  return labels;
}

// source is not a flag: brief push sets it to keep the Markdown next to the page.
export async function publish(cfg, { positional, flags, source, env = process.env }) {
  const target = positional[0];
  if (!target) throw usage(USAGE);
  if (flags.private && flags.public) throw usage("--private and --public are mutually exclusive");
  const visibility = flags.private ? "private" : flags.public ? "public" : undefined;
  const expires = flags.expires !== undefined ? parseWhen(flags.expires) : undefined;

  // A Markdown target is rendered on the way in and supplies the title when
  // --title is absent (its first H1, else the file name).
  const collected = collectFiles(target, { title: flags.title, source });
  const dir = collected.dir;
  const link = readLink(dir);
  const folder = findProject(dir || dirname(resolve(target)));
  const labels = labelsOf(flags, link.api === cfg.api ? link : {}, folder && folder.collection);
  const { cover, files } = resolveCover(flags.cover, dir, collected.files, flags.title);
  if (cover !== undefined) labels.cover = cover;
  const manifest = { ...manifestOf(files, collected.title, commentsFlag(flags), labels, visibility), ...provenanceOf(flags, env) };

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

  // --expires is checked before anything is sent, and applied right after.
  if (flags.expires !== undefined) {
    const after = await setExpiry(cfg, res.id, expires);
    res.expires_at = after.expires_at;
  }

  const project = res.project === undefined ? labels.project : res.project;
  const tags = Array.isArray(res.tags) ? res.tags : labels.tags || [];
  writeLink(dir, { id: res.id, api: cfg.api, project, tags });

  const bytes = files.reduce((n, f) => n + f.size, 0);
  const commentsOn = res.comments_enabled !== false;
  const labelLine = [project ? `project ${project}` : "", tags.length ? `tags ${tags.join(", ")}` : ""].filter(Boolean).join(", ");
  return {
    json: {
      id: res.id, url: res.url, version: res.version, end_user: res.end_user, comments_enabled: commentsOn,
      visibility: res.visibility ?? "public",
      project: project ?? null, tags, cover_url: res.cover_url, cover_style: res.cover_style,
      files: files.length, bytes, uploaded: sent.size,
      expires_at: res.expires_at ?? null,
      ...(res.guest ? { guest: true, claim_url: res.claim_url } : {}),
    },
    human: [
      `${res.url}`,
      `version ${res.version}, ${files.length} files, ${bytes} bytes, ${sent.size} uploaded${commentsOn ? "" : ", comments off"}`,
      ...(labelLine ? [labelLine] : []),
      ...(res.visibility === "private" ? ["private — sign in to the owning workspace to view"] : []),
      ...(res.expires_at && !res.guest ? [`stops working at ${res.expires_at}, then is erased`] : []),
      ...guestLines(res),
    ],
  };
}
