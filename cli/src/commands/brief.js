import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { basename, join, resolve } from "node:path";
import { apiJson, apiText } from "../client.js";
import { usage } from "../errors.js";
import { findProject, PROJECT_FILE } from "../project.js";
import { publish } from "./publish.js";

const USAGE = "usage: byagent brief [push [--private|--public] [--project <name>]]";

// A brief is the page an agent leaves for whoever picks the work up next. It
// is found by tag inside the folder's collection, so any machine with the key
// reaches the same page without a committed id.
export const BRIEF_TAG = "brief";
const NAMES = ["BRIEF.md", "brief.md"];
// Each push ships the Markdown next to the rendered page under this path, so
// a machine without the folder can still read the brief as text.
export const SOURCE_PATH = "brief.md";

function sha256(buf) {
  return createHash("sha256").update(buf).digest("hex");
}

// where resolves the folder's brief: BRIEF.md next to the nearest
// .byagent.json, else in the working directory. --project names the
// collection when there is no config.
export function where(flags, cwd) {
  const project = findProject(cwd);
  const root = project ? project.root : resolve(cwd);
  const path = NAMES.map((n) => join(root, n)).find((p) => existsSync(p)) || join(root, NAMES[0]);
  return { root, path, collection: flags.project || (project && project.collection) || "" };
}

// Push state lives beside the config: which artifact a brief file went to and
// the hash of what was last sent, so an unchanged brief costs no request.
function statePath(home) {
  return join(home, ".artifacts", "briefs.json");
}

function loadState(home) {
  try {
    const parsed = JSON.parse(readFileSync(statePath(home), "utf8"));
    return parsed && typeof parsed === "object" ? parsed : {};
  } catch {
    return {};
  }
}

function saveState(home, state) {
  const path = statePath(home);
  mkdirSync(join(home, ".artifacts"), { recursive: true, mode: 0o700 });
  writeFileSync(`${path}.tmp`, JSON.stringify(state, null, 2) + "\n", { mode: 0o600 });
  renameSync(`${path}.tmp`, path);
}

// findBrief returns the collection's brief artifact, or null. Briefs are
// republished often, so the most recently updated page comes first.
export async function findBrief(cfg, collection) {
  const res = await apiJson(cfg, "GET", "/api/artifacts", { query: { project: collection, limit: 200 } });
  return (res.artifacts || []).find((a) => a.project === collection && Array.isArray(a.tags) && a.tags.includes(BRIEF_TAG)) || null;
}

function needCollection(root, collection) {
  if (!collection) throw usage(`no collection for ${root}: add ${PROJECT_FILE} with {"collection": "<name>"} or pass --project`);
}

async function push(cfg, flags, { cwd, home }) {
  if (flags.private && flags.public) throw usage("--private and --public are mutually exclusive");
  const { root, path, collection } = where(flags, cwd);
  needCollection(root, collection);
  if (!existsSync(path)) throw usage(`no ${NAMES[0]} in ${root}`);

  const sha = sha256(readFileSync(path));
  const state = loadState(home);
  const key = `${cfg.api} ${path}`;
  const known = state[key] && state[key].collection === collection ? state[key] : {};
  const visibilityChange = flags.private || flags.public;
  if (known.id && known.sha === sha && !visibilityChange) {
    return {
      json: { changed: false, id: known.id, url: known.url, collection, path },
      human: ["brief unchanged"],
    };
  }

  let id = known.id || "";
  if (!id) id = (await findBrief(cfg, collection))?.id || "";
  const send = (target) => {
    const f = { project: collection, tag: [BRIEF_TAG] };
    if (target) f.id = target;
    // A brief is working state, so a new one starts private.
    if (flags.public) f.public = true;
    else if (flags.private || !target) f.private = true;
    return publish(cfg, { positional: [path], flags: f, source: SOURCE_PATH });
  };
  let res;
  try {
    res = await send(id);
  } catch (e) {
    // The remembered page was deleted: look again, else start a new one.
    if (e.status !== 404 || !known.id) throw e;
    id = (await findBrief(cfg, collection))?.id || "";
    res = await send(id);
  }

  const { id: newId, url, version, visibility } = res.json;
  state[key] = { id: newId, url, sha, collection };
  saveState(home, state);
  return {
    json: { changed: true, id: newId, url, version, visibility, collection, path },
    human: [`brief v${version} ${url}`],
  };
}

// remoteSource reads the pushed Markdown back. Briefs pushed before the
// source shipped with them have none, which reads as no text.
async function remoteSource(cfg, id) {
  try {
    return await apiText(cfg, "GET", `/api/artifacts/${encodeURIComponent(id)}/files/${SOURCE_PATH}`);
  } catch (e) {
    if (e.status === 404) return "";
    throw e;
  }
}

// show prefers the local BRIEF.md; without one (another machine, a cloud
// agent) it reads the published copy.
async function show(cfg, flags, { cwd }) {
  const { root, path, collection } = where(flags, cwd);
  const found = collection ? await findBrief(cfg, collection) : null;
  const local = existsSync(path);
  const content = local ? readFileSync(path, "utf8") : found ? await remoteSource(cfg, found.id) : "";
  const exists = local || content !== "";
  const source = local ? "local" : exists ? "remote" : null;
  const artifact = found
    ? { id: found.id, url: found.url, version: found.current_version, visibility: found.visibility, open_comments: found.open_comments }
    : null;
  const threads = found && found.open_comments > 0
    ? (await apiJson(cfg, "GET", `/api/artifacts/${encodeURIComponent(found.id)}/comments`, { query: { status: "open" } })).threads || []
    : [];

  const human = [];
  const status = [collection && `collection ${collection}`, artifact && `v${artifact.version}`, artifact && `${artifact.open_comments} open comments`].filter(Boolean).join(", ");
  const label = local ? basename(path) : exists ? "published brief" : `no ${NAMES[0]} in ${root}`;
  human.push(`${label}${status ? `  (${status})` : ""}`);
  if (artifact) human.push(artifact.url);
  if (exists) human.push("", content.trimEnd());
  if (threads.length) {
    human.push("", "open comments:");
    for (const t of threads) {
      for (const c of t.comments || []) human.push(`  ${t.id}  ${c.author_name}: ${c.body}`);
    }
  }
  return { json: { collection: collection || null, path, exists, source, content, artifact, threads }, human };
}

export async function brief(cfg, { positional, flags, env = process.env, cwd = process.cwd() }) {
  const ctx = { cwd, home: env.HOME || homedir() };
  const [action] = positional;
  if (!action) return show(cfg, flags, ctx);
  if (action === "push") return push(cfg, flags, ctx);
  throw usage(USAGE);
}
